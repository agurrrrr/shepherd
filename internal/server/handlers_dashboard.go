package server

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/agurrrrr/shepherd/ent"
	entProject "github.com/agurrrrr/shepherd/ent/project"
	entSheep "github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// activityItem aggregates per-project task count and cost in a time window.
type activityItem struct {
	Project         string  `json:"project"`
	Tasks           int     `json:"tasks"`
	CostUSD         float64 `json:"cost_usd"`
	TotalTokenCount int64   `json:"total_tokens"`
}

// activityWindow holds the per-project breakdown plus totals so the UI can
// render header chips without re-summing on the client.
type activityWindow struct {
	Items     []activityItem `json:"items"`
	Total     int            `json:"total"`
	TotalCost float64        `json:"total_cost"`
}

// dashboardWindows are the rolling windows the Activity / By Model sections
// switch between. Keys are what the web UI tabs select; order is narrowest
// first so the last entry is the widest window to load from the DB.
var dashboardWindows = []struct {
	key   string
	since func(now time.Time) time.Time
}{
	{"5h", func(now time.Time) time.Time { return now.Add(-5 * time.Hour) }},
	{"1d", func(now time.Time) time.Time { return now.AddDate(0, 0, -1) }},
	{"7d", func(now time.Time) time.Time { return now.AddDate(0, 0, -7) }},
	{"1m", func(now time.Time) time.Time { return now.AddDate(0, -1, 0) }},
	{"3m", func(now time.Time) time.Time { return now.AddDate(0, -3, 0) }},
	{"6m", func(now time.Time) time.Time { return now.AddDate(0, -6, 0) }},
	{"1y", func(now time.Time) time.Time { return now.AddDate(-1, 0, 0) }},
}

// dashboardTasks loads every task created since `since` (optionally limited
// to one project) with only the columns the activity folds read. The task
// row carries the full output log, so selecting columns keeps a year-wide
// scan cheap enough to rerun on every SSE-triggered dashboard refresh.
func dashboardTasks(ctx context.Context, client *ent.Client, since time.Time, projectName string) ([]*ent.Task, error) {
	q := client.Task.Query().Where(entTask.CreatedAtGTE(since))
	if projectName != "" {
		q = q.Where(entTask.HasProjectWith(entProject.Name(projectName)))
	}
	return q.
		Select(
			entTask.FieldStatus,
			entTask.FieldModel,
			entTask.FieldCostUsd,
			entTask.FieldPromptTokens,
			entTask.FieldCompletionTokens,
			entTask.FieldStartedAt,
			entTask.FieldCompletedAt,
			entTask.FieldCreatedAt,
		).
		WithProject(func(pq *ent.ProjectQuery) { pq.Select(entProject.FieldName) }).
		WithSheep(func(sq *ent.SheepQuery) { sq.Select(entSheep.FieldProvider) }).
		All(ctx)
}

// dashboardActivity folds tasks into every dashboardWindows bucket for both
// the per-project and the per-model sections.
func dashboardActivity(tasks []*ent.Task, now time.Time) (map[string]activityWindow, map[string]modelStatWindow) {
	activity := make(map[string]activityWindow, len(dashboardWindows))
	byModel := make(map[string]modelStatWindow, len(dashboardWindows))
	for _, w := range dashboardWindows {
		since := w.since(now)
		inWindow := make([]*ent.Task, 0, len(tasks))
		for _, t := range tasks {
			if !t.CreatedAt.Before(since) {
				inWindow = append(inWindow, t)
			}
		}
		activity[w.key] = projectActivityFrom(inWindow)
		byModel[w.key] = modelActivityFrom(inWindow)
	}
	return activity, byModel
}

// projectActivityFrom buckets tasks by project and sums cost_usd. Tasks
// without a project edge fall under "(unassigned)" so they do not silently
// disappear from the totals.
func projectActivityFrom(tasks []*ent.Task) activityWindow {
	agg := map[string]*activityItem{}
	for _, t := range tasks {
		name := "(unassigned)"
		if t.Edges.Project != nil {
			name = t.Edges.Project.Name
		}
		if agg[name] == nil {
			agg[name] = &activityItem{Project: name}
		}
		agg[name].Tasks++
		agg[name].CostUSD += t.CostUsd
		agg[name].TotalTokenCount += t.PromptTokens + t.CompletionTokens
	}

	out := make([]activityItem, 0, len(agg))
	totalCost := 0.0
	for _, v := range agg {
		out = append(out, *v)
		totalCost += v.CostUSD
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CostUSD != out[j].CostUSD {
			return out[i].CostUSD > out[j].CostUSD
		}
		return out[i].Tasks > out[j].Tasks
	})

	return activityWindow{Items: out, Total: len(tasks), TotalCost: totalCost}
}

// modelStatItem aggregates completed-task stats for one statsKey bucket.
type modelStatItem struct {
	Key              string `json:"key"`
	Provider         string `json:"provider"`
	Completed        int    `json:"completed"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	TotalTokens      int64  `json:"total_tokens"`
	DurationSec      int64  `json:"duration_sec"`
}

// modelStatWindow is the per-key breakdown plus window totals for header chips.
type modelStatWindow struct {
	Items            []modelStatItem `json:"items"`
	TotalCompleted   int             `json:"total_completed"`
	TotalTokens      int64           `json:"total_tokens"`
	TotalDurationSec int64           `json:"total_duration_sec"`
}

// statsKey returns the dashboard aggregation key for a sheep provider + task model.
//
// Keying rules (intentionally different from queue groupKey):
//   - claude / grok / magi → provider only (task.model ignored; may be polluted with endpoint ids)
//   - auto → claude (same fold as groupKey)
//   - opencode / pi / embedded → "provider/model", or just provider when model is empty
//   - empty provider → "(unknown)"
func statsKey(provider, model string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	m := strings.TrimSpace(model)
	switch p {
	case "":
		return "(unknown)"
	case "claude", "grok", "magi":
		return p
	case "auto":
		return "claude"
	case "opencode", "pi", "embedded":
		if m == "" {
			return p
		}
		return p + "/" + m
	default:
		// Future providers: prefer provider/model when model is present.
		if m == "" {
			return p
		}
		return p + "/" + m
	}
}

// statsProvider normalizes the display provider for a stats row.
func statsProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	switch p {
	case "":
		return "(unknown)"
	case "auto":
		return "claude"
	default:
		return p
	}
}

// modelActivityFrom aggregates completed tasks by statsKey. Extracted so unit
// tests can exercise the pure fold without a live DB.
func modelActivityFrom(tasks []*ent.Task) modelStatWindow {
	agg := map[string]*modelStatItem{}
	for _, t := range tasks {
		if t.Status != entTask.StatusCompleted {
			continue
		}
		provider := ""
		if t.Edges.Sheep != nil {
			provider = string(t.Edges.Sheep.Provider)
		}
		key := statsKey(provider, t.Model)
		if agg[key] == nil {
			agg[key] = &modelStatItem{
				Key:      key,
				Provider: statsProvider(provider),
			}
		}
		item := agg[key]
		item.Completed++
		item.PromptTokens += t.PromptTokens
		item.CompletionTokens += t.CompletionTokens
		item.TotalTokens += t.PromptTokens + t.CompletionTokens
		item.DurationSec += taskDurationSec(t)
	}

	out := make([]modelStatItem, 0, len(agg))
	var totalCompleted int
	var totalTokens, totalDuration int64
	for _, v := range agg {
		out = append(out, *v)
		totalCompleted += v.Completed
		totalTokens += v.TotalTokens
		totalDuration += v.DurationSec
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Completed != out[j].Completed {
			return out[i].Completed > out[j].Completed
		}
		if out[i].TotalTokens != out[j].TotalTokens {
			return out[i].TotalTokens > out[j].TotalTokens
		}
		return out[i].Key < out[j].Key
	})

	return modelStatWindow{
		Items:            out,
		TotalCompleted:   totalCompleted,
		TotalTokens:      totalTokens,
		TotalDurationSec: totalDuration,
	}
}

// GET /api/dashboard
func (s *Server) handleDashboard(c *fiber.Ctx) error {
	ctx := context.Background()
	client := db.Client()

	// 1. Sheep list with project info
	sheepList, err := worker.List()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	type sheepItem struct {
		Name     string `json:"name"`
		Status   string `json:"status"`
		Provider string `json:"provider"`
		Project  string `json:"project,omitempty"`
	}
	var sheepItems []sheepItem
	for _, sh := range sheepList {
		item := sheepItem{
			Name:     sh.Name,
			Status:   string(sh.Status),
			Provider: string(sh.Provider),
		}
		if sh.Edges.Project != nil {
			item.Project = sh.Edges.Project.Name
		}
		sheepItems = append(sheepItems, item)
	}

	// 2. Task counts (all-time)
	counts, err := queue.CountByStatus()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// 3. Today's task counts
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	todayCompleted, _ := client.Task.Query().
		Where(
			entTask.StatusEQ(entTask.StatusCompleted),
			entTask.CreatedAtGTE(todayStart),
		).Count(ctx)

	todayFailed, _ := client.Task.Query().
		Where(
			entTask.StatusEQ(entTask.StatusFailed),
			entTask.CreatedAtGTE(todayStart),
		).Count(ctx)

	todayTotal, _ := client.Task.Query().
		Where(entTask.CreatedAtGTE(todayStart)).
		Count(ctx)

	// 4. Recent tasks (last 10)
	recentTasks, err := client.Task.Query().
		WithSheep().
		WithProject().
		Order(ent.Desc(entTask.FieldCreatedAt)).
		Limit(10).
		All(ctx)
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	type taskItem struct {
		ID          int    `json:"id"`
		Prompt      string `json:"prompt"`
		Status      string `json:"status"`
		Summary     string `json:"summary,omitempty"`
		Error       string `json:"error,omitempty"`
		Sheep       string `json:"sheep,omitempty"`
		Project     string `json:"project,omitempty"`
		CreatedAt   string `json:"created_at"`
		TotalTokens int64  `json:"total_tokens"`
		DurationSec int64  `json:"duration_sec"`
	}

	var recentItems []taskItem
	for _, t := range recentTasks {
		item := taskItem{
			ID:          t.ID,
			Prompt:      truncate(t.Prompt, 100),
			Status:      string(t.Status),
			Summary:     truncate(t.Summary, 120),
			Error:       truncate(t.Error, 120),
			CreatedAt:   t.CreatedAt.Format("2006-01-02 15:04:05"),
			TotalTokens: t.PromptTokens + t.CompletionTokens,
			DurationSec: taskDurationSec(t),
		}
		if t.Edges.Sheep != nil {
			item.Sheep = t.Edges.Sheep.Name
		}
		if t.Edges.Project != nil {
			item.Project = t.Edges.Project.Name
		}
		recentItems = append(recentItems, item)
	}

	// 5. Projects count
	projects, err := project.List()
	if err != nil {
		return fail(c, fiber.StatusInternalServerError, err.Error())
	}

	// 6. Activity + by-model windows (5h … 1y), optionally scoped to one
	// project. One column-trimmed scan over the widest window, then folded
	// per window in-process — avoids client-side time math across timezones.
	widest := dashboardWindows[len(dashboardWindows)-1]
	windowTasks, _ := dashboardTasks(ctx, client, widest.since(now), c.Query("project"))
	activity, byModel := dashboardActivity(windowTasks, now)

	// Sheep counts
	working := 0
	idle := 0
	errCount := 0
	for _, sh := range sheepList {
		switch sh.Status {
		case "working":
			working++
		case "idle":
			idle++
		case "error":
			errCount++
		}
	}

	return success(c, map[string]interface{}{
		"sheep": map[string]interface{}{
			"total":   len(sheepList),
			"working": working,
			"idle":    idle,
			"error":   errCount,
			"list":    sheepItems,
		},
		"tasks": map[string]interface{}{
			"pending":   counts[entTask.StatusPending],
			"running":   counts[entTask.StatusRunning],
			"completed": counts[entTask.StatusCompleted],
			"failed":    counts[entTask.StatusFailed],
			"stopped":   counts[entTask.StatusStopped],
		},
		"today": map[string]interface{}{
			"total":     todayTotal,
			"completed": todayCompleted,
			"failed":    todayFailed,
		},
		"recent_tasks": recentItems,
		"projects":     len(projects),
		"activity":     activity,
		"by_model":     byModel,
	})
}

// taskDurationSec returns how long a task ran, in whole seconds.
//   - completed/failed/stopped: completed_at - started_at
//   - running (no completed_at yet): now - started_at (live elapsed)
//   - missing started_at: 0 (unknown)
func taskDurationSec(t *ent.Task) int64 {
	if t.StartedAt.IsZero() {
		return 0
	}
	end := t.CompletedAt
	if end.IsZero() {
		if t.Status == entTask.StatusRunning {
			end = time.Now()
		} else {
			return 0
		}
	}
	d := end.Sub(t.StartedAt).Seconds()
	if d < 0 {
		return 0
	}
	return int64(d)
}

// truncate limits a string to maxLen characters.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
