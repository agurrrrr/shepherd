package manager

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entissue "github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
)

// OperatingInstructions is the base guidance prepended to every shepherd
// (목자) task's system context (#595). Written in English to match the other
// system prompts. It states the role, the delegate-only rule (issues are the
// only unit of work), the duplicate check, the review procedure, the bans, and
// the report format.
//
// Limits are enforced by the code, not by this text: the provider runs with
// --dangerously-skip-permissions, so the "do not edit code / do not stop tasks
// / do not delete issues" rules are guidance only until stage #601 adds
// request_user_action and the autonomy tiers.
const OperatingInstructions = `[Shepherd Orchestrator — operating instructions]
You are the shepherd (orchestrator) of this system. Your working directory is the workspace: you may read project code and judge its state, but you do NOT edit files or commit anything yourself.

Delegation rules:
- All work goes through issues. Create an issue with issue_upsert, then enqueue it with issue_execute(project_name, id, watch=true). Never run the work yourself.
- Write the issue body concretely: the background, where to change (file/function paths), and the goal (how success is judged). Split large work into a parent issue plus child issues.
- Before creating a new issue, check with issue_list whether the same work already exists.

Review procedure (when this task is a review of a delegated task):
- Use get_task_detail to read the result and the change statistics, compare them against the issue goal, then choose exactly one:
  1. Goal met: close the issue with issue_upsert(status=done).
  2. Not good enough: append your correction instructions to the issue body (issue_upsert) and re-enqueue the SAME issue with issue_execute.
  3. Follow-up with a different scope: create a new issue and enqueue it.
- If the task stopped on a question: if you can answer it, append the answer to the issue body and re-enqueue. If only the user can answer, do NOT re-enqueue — put the question verbatim into your report.

Actions beyond your permission tier:
- The [Autonomy] section states what you may do. Anything refused by a permission error must NOT be attempted another way: call request_user_action(kind, project_name, target_id, reason) and wait — the user decides on the Shepherd screen, and the decision wakes you back up as a review task.
- Deleting an issue is never yours to do at any tier; raise request_user_action(kind=delete_issue) instead.
- Changing sheep settings stays out of reach: report what must change instead.

Report format:
- Your final message is a report to the user. State briefly what you checked, which issues you created/closed/re-enqueued, and what the user must decide. If the configured language is "ko", write the report in Korean.`

// AutonomyNotice is the fixed fallback line for the [Autonomy] section when
// the live numbers cannot be read (DB down). #601 filled the section with the
// real tier, caps, today's spend, and active delegations — see autonomySection.
const AutonomyNotice = "issue create + execute allowed; stop/delete not allowed"

// autonomySection renders the live [Autonomy] block (#601 §5): the effective
// tier with what each tier allows, both caps with their current readings, and
// the request_user_action pointer that replaces the old flat bans. It degrades
// to AutonomyNotice plus whatever is known when a query fails — a broken
// counter must not cost the shepherd its instructions.
func autonomySection() string {
	tier := AutonomyTier()

	var sb strings.Builder
	sb.WriteString("[Autonomy]\n")
	fmt.Fprintf(&sb, "tier: %s — %s\n", tier, tierDescription(tier))

	maxActive := maxActiveDelegations()
	if active, err := ActiveDelegations(); err == nil {
		fmt.Fprintf(&sb, "delegation cap: at most %d watched-issue tasks pending/running at once (currently %d)\n", maxActive, active)
	} else {
		fmt.Fprintf(&sb, "delegation cap: at most %d watched-issue tasks pending/running at once (current count unavailable)\n", maxActive)
	}

	limit := dailyCostLimitUSD()
	if limit > 0 {
		if spent, err := DailyCostToday(time.Now()); err == nil {
			fmt.Fprintf(&sb, "daily cost cap: $%.2f — used today $%.2f (shepherd + watched-issue work combined)\n", limit, spent)
		} else {
			fmt.Fprintf(&sb, "daily cost cap: $%.2f (today's spend unavailable)\n", limit)
		}
	} else {
		sb.WriteString("daily cost cap: none\n")
	}

	sb.WriteString("Anything a permission error refuses: call request_user_action(kind, project_name, target_id, reason) and wait — the user's decision wakes you as a review task. Deleting issues is never yours at any tier. Providers that cannot pass the caller task id are not permission-checked; the rules above still bind you.")
	return sb.String()
}

// tierDescription spells out one tier for the prompt (English, like the rest
// of the system context).
func tierDescription(tier string) string {
	switch tier {
	case AutonomyReport:
		return "read and report only; creating or enqueueing issues is refused"
	case AutonomyPlan:
		return "issue create/edit allowed; enqueueing (issue_execute) is refused"
	case AutonomyExecute:
		return "issue create/edit + enqueue allowed (caps apply); stopping tasks is refused"
	case AutonomyFull:
		return "issue create/edit + enqueue + task_stop allowed; issue delete still needs the user"
	default:
		return AutonomyNotice
	}
}

// scopeTableMaxChars caps the [Managed Projects] section. The scope can hold
// more than 50 projects, so the table must not grow without bound; when it
// overflows, idle projects (no open issues, no pending/running tasks) are
// reduced to bare names first (see reduceScopeTable).
const scopeTableMaxChars = 8000

// openIssueStatuses are the issue statuses counted as "open" in the scope
// table. done is excluded.
var openIssueStatuses = []entissue.Status{
	entissue.StatusTodo,
	entissue.StatusInProgress,
	entissue.StatusTesting,
	entissue.StatusFailed,
}

// BuildShepherdContext returns the whole manager-side system context for a
// shepherd task: the operating instructions first, then the workspace, the
// management-scope table, the autonomy notice, and the user's extra
// instructions. worker calls it through the provider registered at daemon
// start (worker.SetShepherdContextProvider) — worker cannot import manager
// directly because manager imports worker.
//
// It is safe to call before any project exists; each section degrades to a
// short "none/unset" line instead of failing.
func BuildShepherdContext() string {
	var sb strings.Builder

	sb.WriteString(OperatingInstructions)
	sb.WriteString("\n\n")

	// [Shepherd Workspace]
	workspace := strings.TrimSpace(config.GetString("workspace_path"))
	if workspace == "" {
		sb.WriteString("[Shepherd Workspace]\n(unset — set workspace_path in settings)\n\n")
	} else {
		sb.WriteString("[Shepherd Workspace]\n" + workspace + "\n\n")
	}

	// [Managed Projects]
	table, err := BuildScopeTable()
	if err != nil {
		table = "(failed to read project state: " + err.Error() + ")"
	}
	sb.WriteString("[Managed Projects]\n")
	sb.WriteString(table)
	sb.WriteString("\n\n")

	// [Autonomy] — live tier + caps + today's numbers (#601).
	sb.WriteString(autonomySection())
	sb.WriteString("\n")

	// [User Shepherd Instructions] — the user's own additions, always last.
	if instr := strings.TrimSpace(config.GetString("shepherd_instructions")); instr != "" {
		sb.WriteString("\n[User Shepherd Instructions]\n")
		sb.WriteString(instr)
		sb.WriteString("\n")
	}

	return sb.String()
}

// scopeStat is one project's live queue/issue state used to render a table
// line.
type scopeStat struct {
	name          string
	path          string
	sheepName     string
	sheepProvider string
	sheepStatus   string
	pending       int
	runningIDs    []int
	openIssues    map[entissue.Status]int
}

// idle reports whether a project has nothing in flight — no open issues, no
// pending or running tasks. Idle projects are the first to be reduced to bare
// names when the table exceeds its length cap.
func (s *scopeStat) idle() bool {
	if s.pending > 0 || len(s.runningIDs) > 0 {
		return false
	}
	for _, n := range s.openIssues {
		if n > 0 {
			return false
		}
	}
	return true
}

func (s *scopeStat) fullLine() string {
	sheep := "-"
	if s.sheepName != "" {
		sheep = s.sheepName
		if s.sheepProvider != "" {
			sheep += "(" + s.sheepProvider + ")"
		}
		if s.sheepStatus != "" {
			sheep += " " + string(s.sheepStatus)
		}
	}

	running := "-"
	if len(s.runningIDs) > 0 {
		ids := make([]string, len(s.runningIDs))
		for i, id := range s.runningIDs {
			ids[i] = fmt.Sprintf("#%d", id)
		}
		running = strings.Join(ids, ",")
	}

	issues := "-"
	for i, st := range openIssueStatuses {
		n := s.openIssues[st]
		if i == 0 {
			issues = fmt.Sprintf("%s=%d", st, n)
		} else {
			issues += fmt.Sprintf("/%s=%d", st, n)
		}
	}

	return fmt.Sprintf("%s | %s | %s | pending=%d | running=%s | issues %s",
		s.name, s.path, sheep, s.pending, running, issues)
}

// BuildScopeTable renders the [Managed Projects] lines: one per in-scope
// project, short enough to survive the 50+ project scale. The whole section is
// capped at scopeTableMaxChars; idle projects lose detail first.
//
// Query count is FIXED regardless of project count (wiki:
// project-list-latency-analysis): one project list (with the sheep edge
// eager-loaded), one pending-task group-by, one running-task select, one
// issue group-by. Pinned by TestBuildScopeTableQueryCountIsFixed.
func BuildScopeTable() (string, error) {
	projects, err := ScopeProjects()
	if err != nil {
		return "", err
	}
	if len(projects) == 0 {
		return "(no managed projects)", nil
	}

	stats, err := collectScopeStats(projects)
	if err != nil {
		return "", err
	}

	lines := make([]string, len(stats))
	for i, s := range stats {
		lines[i] = s.fullLine()
	}

	if totalLen(lines) <= scopeTableMaxChars {
		return strings.Join(lines, "\n"), nil
	}
	return reduceScopeTable(stats), nil
}

// collectScopeStats runs the three aggregate queries (pending tasks, running
// tasks, open issues) once each and merges them into per-project stats.
func collectScopeStats(projects []*ent.Project) ([]*scopeStat, error) {
	ctx := context.Background()
	client := db.ReadClient()

	byName := make(map[string]*scopeStat, len(projects))
	stats := make([]*scopeStat, 0, len(projects))
	idToName := make(map[int]string, len(projects))
	for _, p := range projects {
		s := &scopeStat{name: p.Name, path: p.Path, openIssues: map[entissue.Status]int{}}
		if p.Edges.Sheep != nil {
			s.sheepName = p.Edges.Sheep.Name
			s.sheepProvider = string(p.Edges.Sheep.Provider)
			s.sheepStatus = string(p.Edges.Sheep.Status)
		}
		byName[p.Name] = s
		stats = append(stats, s)
		idToName[p.ID] = p.Name
	}

	// 1) pending task counts per project — one grouped query.
	var pendingRows []struct {
		ProjectTasks int `json:"project_tasks"`
		Count        int `json:"count"`
	}
	if err := client.Task.Query().
		Where(task.StatusEQ(task.StatusPending)).
		GroupBy("project_tasks").
		Aggregate(ent.Count()).
		Scan(ctx, &pendingRows); err != nil {
		return nil, fmt.Errorf("pending count: %w", err)
	}
	for _, row := range pendingRows {
		if s := statOf(byName, idToName, row.ProjectTasks); s != nil {
			s.pending = row.Count
		}
	}

	// 2) running task IDs per project — running tasks are few, so reading the
	// rows (id + FK column only) stays cheap and gives the IDs the report needs.
	var runningRows []struct {
		ID           int `json:"id"`
		ProjectTasks int `json:"project_tasks"`
	}
	if err := client.Task.Query().
		Where(task.StatusEQ(task.StatusRunning)).
		Select(task.FieldID, "project_tasks").
		Scan(ctx, &runningRows); err != nil {
		return nil, fmt.Errorf("running tasks: %w", err)
	}
	for _, row := range runningRows {
		if s := statOf(byName, idToName, row.ProjectTasks); s != nil {
			s.runningIDs = append(s.runningIDs, row.ID)
		}
	}

	// 3) open issue counts per project and status — one grouped query.
	var issueRows []struct {
		ProjectIssues int             `json:"project_issues"`
		Status        entissue.Status `json:"status"`
		Count         int             `json:"count"`
	}
	if err := client.Issue.Query().
		Where(entissue.StatusIn(openIssueStatuses...)).
		GroupBy("project_issues", entissue.FieldStatus).
		Aggregate(ent.Count()).
		Scan(ctx, &issueRows); err != nil {
		return nil, fmt.Errorf("issue counts: %w", err)
	}
	for _, row := range issueRows {
		if s := statOf(byName, idToName, row.ProjectIssues); s != nil {
			s.openIssues[row.Status] += row.Count
		}
	}

	return stats, nil
}

// statOf maps an FK column value (project_tasks / project_issues) back to a
// scope stat. FK 0 means "no project" (manager tasks) and out-of-scope project
// IDs are absent from idToName, so both return nil.
func statOf(byName map[string]*scopeStat, idToName map[int]string, projectID int) *scopeStat {
	name, ok := idToName[projectID]
	if !ok {
		return nil
	}
	return byName[name]
}

// reduceScopeTable enforces the length cap: idle projects (no open issues, no
// pending/running tasks) collapse to bare names, cheapest detail first. If
// even that does not fit, the joined text is cut at the cap with a marker so
// the manager knows the table is truncated.
func reduceScopeTable(stats []*scopeStat) string {
	lines := make([]string, len(stats))
	reduced := make([]bool, len(stats))
	for i, s := range stats {
		lines[i] = s.fullLine()
	}

	// Collapse idle projects one at a time (largest line first) until the
	// total fits.
	for totalLen(lines) > scopeTableMaxChars {
		worst := -1
		for i, s := range stats {
			if reduced[i] || !s.idle() {
				continue
			}
			if worst == -1 || len(lines[i]) > len(lines[worst]) {
				worst = i
			}
		}
		if worst == -1 {
			break // nothing left to reduce
		}
		reduced[worst] = true
		lines[worst] = stats[worst].name + " | (idle)"
	}

	joined := strings.Join(lines, "\n")
	if utf8Len(joined) > scopeTableMaxChars {
		runes := []rune(joined)
		joined = string(runes[:scopeTableMaxChars]) + "\n…(truncated)"
	}
	return joined
}

func totalLen(lines []string) int {
	n := 0
	for _, l := range lines {
		n += utf8Len(l) + 1
	}
	return n
}

func utf8Len(s string) int {
	return len([]rune(s))
}
