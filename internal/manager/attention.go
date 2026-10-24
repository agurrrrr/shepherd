// Needs-attention collection for the Shepherd screen (#599).
//
// CollectAttention walks the whole management scope (manager.ScopeProjects)
// and reports problems a human should look at: failed tasks, tasks running
// too long, tripped circuit breakers, stale pending backlogs, watched issues
// that hit the review-round cap, tasks the user stopped themselves, and
// shepherd work that cannot leave pending. No LLM is involved — every rule
// is plain data access with exactly one query per kind, never one per
// project. Stage #600's patrol loop reuses this function verbatim.
//
// Acknowledging ("확인함") stores the item's fingerprint in the
// shepherd_acks table; an already-acked fingerprint is dropped from later
// collections. Fingerprints embed the target id (task id, sheep name + trip
// count, issue id + round), so a *new* failure or a *new* round surfaces as
// a new item even when the old one was acked. Acks older than 30 days are
// pruned on every collection.
package manager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entshepherdack "github.com/agurrrrr/shepherd/ent/shepherdack"
	entIssue "github.com/agurrrrr/shepherd/ent/issue"
	"github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/queue"
)

// Attention kinds. The string doubles as the fingerprint prefix, so it must
// stay stable across releases — old ack rows keep suppressing the same kind.
const (
	AttentionFailedTask      = "failed_task"
	AttentionLongRunning     = "long_running"
	AttentionCircuitOpen     = "circuit_open"
	AttentionPendingBacklog  = "pending_backlog"
	AttentionReviewLimit     = "review_limit"
	AttentionUserStopped     = "user_stopped"
	AttentionShepherdBlocked = "shepherd_blocked"
	AttentionCostLimit       = "cost_limit"
)

// Defaults for the two new settings (#599). A stored value <= 0 falls back to
// these, matching wake.go's treatment of the review-round keys.
const (
	stuckMinutesDefault        = 120 // shepherd_stuck_minutes: running longer than this is suspicious
	pendingStaleMinutesDefault = 60  // shepherd_pending_stale_minutes: oldest pending task older than this is a backlog
	failedTaskWindow           = 24 * time.Hour
	ackRetention               = 30 * 24 * time.Hour
)

// AttentionItem is one problem the user should see. Severity orders the panel
// (higher first); ActionKind + TargetID/TargetName tell the UI which button
// and which route to render. Detail carries kind-specific extras (issue id,
// counts, round numbers) so the panel never needs a follow-up request.
type AttentionItem struct {
	Kind        string                 `json:"kind"`
	Severity    int                    `json:"severity"`
	Fingerprint string                 `json:"fingerprint"`
	Title       string                 `json:"title"`
	Message     string                 `json:"message"`
	ActionKind  string                 `json:"action_kind"`
	TargetID    int                    `json:"target_id,omitempty"`
	TargetName  string                 `json:"target_name,omitempty"`
	Project     string                 `json:"project,omitempty"`
	CreatedAt   time.Time              `json:"created_at"`
	Detail      map[string]interface{} `json:"detail,omitempty"`
}

// queryCounter records how many DB queries one CollectAttention run issued,
// per kind. Tests install the map via startQueryCounting; production runs pay
// nothing (the pointer stays nil). Not safe for parallel collections — the
// daemon collects serially from one ticker/console request at a time.
var queryCounter *map[string]int

func startQueryCounting() {
	m := map[string]int{}
	queryCounter = &m
}

func stopQueryCounting() map[string]int {
	if queryCounter == nil {
		return nil
	}
	m := *queryCounter
	queryCounter = nil
	return m
}

func countQuery(kind string) {
	if queryCounter != nil {
		(*queryCounter)[kind]++
	}
}

// stuckMinutes / pendingStaleMinutes expose the two new settings.
func stuckMinutes() int {
	n := config.GetInt("shepherd_stuck_minutes")
	if n <= 0 {
		return stuckMinutesDefault
	}
	return n
}

func pendingStaleMinutes() int {
	n := config.GetInt("shepherd_pending_stale_minutes")
	if n <= 0 {
		return pendingStaleMinutesDefault
	}
	return n
}

// CollectAttention is the data pass both the panel and stage #600's patrol
// share: one query per kind, acked fingerprints dropped, acks older than 30
// days pruned. Items are sorted by severity desc, then created_at asc (the
// oldest problem leads within one severity).
func CollectAttention(now time.Time) ([]AttentionItem, error) {
	ctx := context.Background()
	client := db.Client()
	if client == nil {
		return nil, errors.New("database is not initialized")
	}

	if err := pruneAcks(ctx, now); err != nil {
		return nil, err
	}

	scope, err := ScopeProjects()
	if err != nil {
		return nil, fmt.Errorf("scope: %w", err)
	}
	projectIDs := make([]int, 0, len(scope))
	for _, p := range scope {
		projectIDs = append(projectIDs, p.ID)
	}

	items := make([]AttentionItem, 0, 16)

	// Project-scoped kinds need the scope id list; with an empty scope (all
	// projects excluded) they report nothing and skip their queries. The two
	// flock/global kinds below still run — a tripped breaker or blocked
	// shepherd matters even when no project is managed.
	if len(projectIDs) > 0 {
		failures, err := attentionFailedTasks(ctx, projectIDs, now)
		if err != nil {
			return nil, err
		}
		items = append(items, failures...)

		longRunning, err := attentionLongRunningTasks(ctx, projectIDs, now)
		if err != nil {
			return nil, err
		}
		items = append(items, longRunning...)

		backlogs, err := attentionPendingBacklogs(ctx, scope, now)
		if err != nil {
			return nil, err
		}
		items = append(items, backlogs...)

		reviewLimits, err := attentionReviewLimits(ctx, projectIDs)
		if err != nil {
			return nil, err
		}
		items = append(items, reviewLimits...)

		userStopped, err := attentionUserStopped(ctx, projectIDs)
		if err != nil {
			return nil, err
		}
		items = append(items, userStopped...)
	}

	circuits, err := attentionCircuitOpen(ctx)
	if err != nil {
		return nil, err
	}
	items = append(items, circuits...)

	blocked, err := attentionShepherdBlocked(ctx)
	if err != nil {
		return nil, err
	}
	items = append(items, blocked...)

	costItems, err := attentionCostLimit(now)
	if err != nil {
		return nil, err
	}
	items = append(items, costItems...)

	acked, err := ackedFingerprints(ctx)
	if err != nil {
		return nil, err
	}
	filtered := make([]AttentionItem, 0, len(items))
	for _, it := range items {
		if acked[it.Fingerprint] {
			continue
		}
		filtered = append(filtered, it)
	}

	sortAttentionItems(filtered)
	return filtered, nil
}

// sortAttentionItems orders by severity desc, then created_at asc. Small n,
// so an insertion sort keeps the ordering rule in one readable place.
func sortAttentionItems(items []AttentionItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && attentionLess(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func attentionLess(a, b AttentionItem) bool {
	if a.Severity != b.Severity {
		return a.Severity > b.Severity
	}
	return a.CreatedAt.Before(b.CreatedAt)
}

// ---- kind: failed_task (severity 6) --------------------------------------
// status=failed within the last 24h in an in-scope project. The manager's
// automatic review/patrol tasks are skipped — their failures surface through
// the console and shepherd_blocked instead, and a failing patrol would
// double-report. Failed *command* shepherd tasks do show: the user sent it.
// Fingerprint: failed_task:<task id> — every new failure is a new item.

func attentionFailedTasks(ctx context.Context, projectIDs []int, now time.Time) ([]AttentionItem, error) {
	countQuery(AttentionFailedTask)
	tasks, err := db.Client().Task.Query().
		Where(
			entTask.StatusEQ(entTask.StatusFailed),
			entTask.HasProjectWith(project.IDIn(projectIDs...)),
			entTask.CompletedAtGTE(now.Add(-failedTaskWindow)),
		).
		WithIssue().WithProject().
		Order(ent.Asc(entTask.FieldCompletedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed_task query: %w", err)
	}

	items := make([]AttentionItem, 0, len(tasks))
	for _, t := range tasks {
		if t.ShepherdKind != "" && t.ShepherdKind != KindCommand {
			continue
		}
		it := AttentionItem{
			Kind:        AttentionFailedTask,
			Severity:    6,
			Fingerprint: fmt.Sprintf("%s:%d", AttentionFailedTask, t.ID),
			Title:       fmt.Sprintf("작업 #%d 실패", t.ID),
			Message:     firstLine(t.Error, "실패 원인이 기록되지 않았습니다"),
			ActionKind:  "retry_task",
			TargetID:    t.ID,
			CreatedAt:   t.CompletedAt,
		}
		if iss := t.Edges.Issue; iss != nil {
			it.TargetName = iss.Title
			it.Detail = map[string]interface{}{"issue_id": iss.ID}
		}
		if p := t.Edges.Project; p != nil {
			it.Project = p.Name
		}
		items = append(items, it)
	}
	return items, nil
}

// ---- kind: long_running (severity 5) -------------------------------------
// status=running for longer than shepherd_stuck_minutes. started_at drives
// the age; rows with a missing started_at fall back to created_at so a
// half-written row cannot hide forever. The number of running tasks is small
// (bounded by the concurrency settings), so they are filtered in Go after
// one query instead of pushing time math into SQL — the stored time format
// is Go's String() text, which SQLite date functions cannot parse.
// Fingerprint: long_running:<task id>:<bucket>, bucket = how many whole
// stuck-minutes the task has been running. Acking at 2h hides the item for
// another 2h; a still-stuck task reappears with bucket+1.

func attentionLongRunningTasks(ctx context.Context, projectIDs []int, now time.Time) ([]AttentionItem, error) {
	countQuery(AttentionLongRunning)
	stuck := time.Duration(stuckMinutes()) * time.Minute

	tasks, err := db.Client().Task.Query().
		Where(
			entTask.StatusEQ(entTask.StatusRunning),
			entTask.HasProjectWith(project.IDIn(projectIDs...)),
		).
		WithIssue().WithSheep().WithProject().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("long_running query: %w", err)
	}

	items := make([]AttentionItem, 0, 4)
	for _, t := range tasks {
		start := t.StartedAt
		if start.IsZero() {
			start = t.CreatedAt
		}
		runningFor := now.Sub(start)
		if runningFor < stuck {
			continue
		}
		bucket := int64(runningFor / stuck)
		sheepName := ""
		if s := t.Edges.Sheep; s != nil {
			sheepName = s.Name
		}
		it := AttentionItem{
			Kind:        AttentionLongRunning,
			Severity:    5,
			Fingerprint: fmt.Sprintf("%s:%d:%d", AttentionLongRunning, t.ID, bucket),
			Title:       fmt.Sprintf("작업 #%d가 %s 넘게 실행 중", t.ID, formatMinutes(int(runningFor.Minutes()))),
			Message:     longRunningMessage(t.Prompt, sheepName),
			ActionKind:  "stop_task",
			TargetID:    t.ID,
			TargetName:  sheepName,
			CreatedAt:   start,
		}
		if iss := t.Edges.Issue; iss != nil {
			it.Detail = map[string]interface{}{"issue_id": iss.ID}
		}
		if p := t.Edges.Project; p != nil {
			it.Project = p.Name
		}
		items = append(items, it)
	}
	return items, nil
}

func longRunningMessage(prompt, sheepName string) string {
	line := firstLine(prompt, "프롬프트 없음")
	if sheepName != "" {
		return fmt.Sprintf("%s 양에서 실행 중 — %s", sheepName, line)
	}
	return line
}

// ---- kind: circuit_open (severity 7) -------------------------------------
// Sheep whose consecutive_failures reached queue.CircuitBreakerThreshold; the
// dispatcher skips them, so their pending work silently stalls. The manager
// sheep follows the same row rule (dispatchShepherd gates on it), and its
// name is included here too — one query over the whole flock.
// Fingerprint: circuit_open:<sheep>:<failures> — a further failure changes
// the count and re-surfaces an acked item.

func attentionCircuitOpen(ctx context.Context) ([]AttentionItem, error) {
	countQuery(AttentionCircuitOpen)
	flocks, err := db.Client().Sheep.Query().
		Where(sheep.ConsecutiveFailuresGTE(queue.CircuitBreakerThreshold)).
		WithProject().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("circuit_open query: %w", err)
	}

	items := make([]AttentionItem, 0, len(flocks))
	for _, s := range flocks {
		projectName := ""
		if p := s.Edges.Project; p != nil {
			projectName = p.Name
		}
		label := s.Name
		if names.IsManager(s.Name) {
			label = "목자"
		}
		items = append(items, AttentionItem{
			Kind:        AttentionCircuitOpen,
			Severity:    7,
			Fingerprint: fmt.Sprintf("%s:%s:%d", AttentionCircuitOpen, s.Name, s.ConsecutiveFailures),
			Title:       fmt.Sprintf("%s 양의 서킷 브레이커가 켜졌습니다", label),
			Message:     fmt.Sprintf("연속 실패 %d회 — 대기 작업이 멈췄습니다. 재시도 전에 서킷을 초기화하세요.", s.ConsecutiveFailures),
			ActionKind:  "reset_sheep",
			TargetName:  s.Name,
			Project:     projectName,
			Detail:      map[string]interface{}{"consecutive_failures": s.ConsecutiveFailures},
		})
	}
	return items, nil
}

// ---- kind: pending_backlog (severity 4, drained state lowered to 2) ------
// A project whose OLDEST pending task is older than
// shepherd_pending_stale_minutes. One GROUP BY query covers every in-scope
// project, so the query count never follows the number of projects. The age
// check runs in Go because the stored time text is Go's String() format,
// which SQLite's date functions cannot parse (see parseStoredTime). The
// oldest task per project is MIN(id): ids are auto-increment, so the lowest
// id is the earliest row, and its created_at is fetched in the same query.
// Fingerprint: pending_backlog:<project id>:<oldest task id> — a new oldest
// task (the old one dispatched or cancelled) starts a new item. The rule is
// unchanged (#631): severity and message may vary under the same fingerprint,
// but the fingerprint itself never gains or loses parts, so a drain-state
// flip does not manufacture a new patrol wake.
//
// #631 false positives: four patrols (2026-10-24/25) rang this alarm while
// the assigned sheep was simply draining its per-sheep FIFO behind a live
// running task — the age of the oldest pending says nothing about whether
// that queue is moving. Two designs were compared:
//
//   - Chosen: keep firing, but show the judgement material in the item.
//     When the oldest pending's assigned sheep currently has a running task,
//     that task id and its start time go into the message (serial-wait
//     evidence the shepherd can dismiss without re-running the SQL/ps
//     diagnosis), and severity drops 4 → 2. The item also reports how many
//     pending tasks in the same project are assigned to the manager sheep
//     with an empty shepherd_kind — the dispatch-deadzone shape (wiki
//     manager-assigned-task-dispatch-deadzone) that hides behind a normal
//     oldest row.
//   - Rejected: suppress the item entirely while the sheep is running. The
//     4th false positive (2026-10-25 06:22) showed why: the oldest pending
//     was a normal FIFO row while a genuinely stuck manager-assigned empty-
//     kind row (#11047) sat in the SAME project. Suppressing on "assigned
//     sheep is running" silences the whole item and misses the real stall.
//     A running task can also be wedged, in which case the backlog is real
//     and suppression would hide it; the message keeps it visible instead.
//
// Severity 2 (not 0/1) because a serial queue behind one long task still
// deserves a look when it lasts for hours — long_running covers a wedged
// running task separately, but the backlog itself stays on the panel.

func attentionPendingBacklogs(ctx context.Context, scope []*ent.Project, now time.Time) ([]AttentionItem, error) {
	countQuery(AttentionPendingBacklog)
	stale := pendingStaleMinutes()

	raw := db.RawDB()
	if raw == nil {
		return nil, errors.New("raw db handle is not initialized")
	}
	projectIDs := projectIDsOf(scope)
	placeholders := make([]string, len(projectIDs))
	args := make([]interface{}, 0, len(projectIDs))
	for i, id := range projectIDs {
		placeholders[i] = "?"
		args = append(args, id)
	}

	// One query still. The joins add the #631 judgement material per row:
	// the oldest pending's assigned sheep, that sheep's current running
	// task (MIN(id) is deterministic; a sheep runs at most one task at a
	// time), and the per-project count of manager-assigned empty-kind
	// pendings. The running task may belong to another project — the queue
	// is per-sheep, not per-project, so it blocks this backlog just the
	// same.
	rows, err := raw.QueryContext(ctx,
		`SELECT t.project_tasks, t.id, t.created_at, agg.pending_count,
		        s.name, run.id, run.started_at, IFNULL(dead.deadzone_count, 0)
		   FROM tasks t
		   JOIN (SELECT project_tasks, MIN(id) AS oldest_id, COUNT(*) AS pending_count
		           FROM tasks
		          WHERE status = 'pending' AND project_tasks IN (`+strings.Join(placeholders, ",")+`)
		          GROUP BY project_tasks) agg
		     ON t.id = agg.oldest_id
		   LEFT JOIN sheep s
		     ON s.id = t.sheep_tasks
		   LEFT JOIN (SELECT r.sheep_tasks AS sid, MIN(r.id) AS rid
		                FROM tasks r
		               WHERE r.status = 'running' AND r.sheep_tasks IS NOT NULL
		               GROUP BY r.sheep_tasks) m
		     ON m.sid = t.sheep_tasks
		   LEFT JOIN tasks run
		     ON run.id = m.rid
		   LEFT JOIN (SELECT d.project_tasks AS pid, COUNT(*) AS deadzone_count
		                FROM tasks d
		                JOIN sheep ms
		                  ON ms.id = d.sheep_tasks AND ms.name = '`+names.ManagerName+`'
		               WHERE d.status = 'pending'
		                 AND (d.shepherd_kind IS NULL OR d.shepherd_kind = '')
		               GROUP BY d.project_tasks) dead
		     ON dead.pid = agg.project_tasks`,
		args...)
	if err != nil {
		return nil, fmt.Errorf("pending_backlog query: %w", err)
	}
	defer rows.Close()

	scopeNames := make(map[int]string, len(scope))
	for _, p := range scope {
		scopeNames[p.ID] = p.Name
	}

	items := make([]AttentionItem, 0, 4)
	for rows.Next() {
		var projectID, oldestTaskID, count int
		var oldestRaw string
		var sheepName sql.NullString
		var runningID sql.NullInt64
		var runningStartedRaw sql.NullString
		var deadzoneCount int
		if err := rows.Scan(&projectID, &oldestTaskID, &oldestRaw, &count,
			&sheepName, &runningID, &runningStartedRaw, &deadzoneCount); err != nil {
			return nil, fmt.Errorf("pending_backlog scan: %w", err)
		}
		name := scopeNames[projectID]
		if name == "" {
			continue // project vanished between scope and query — skip quietly
		}

		oldest, ok := parseStoredTime(oldestRaw)
		if !ok || now.Sub(oldest) < time.Duration(stale)*time.Minute {
			continue
		}

		item := AttentionItem{
			Kind:        AttentionPendingBacklog,
			Severity:    4,
			Fingerprint: fmt.Sprintf("%s:%d:%d", AttentionPendingBacklog, projectID, oldestTaskID),
			Title:       fmt.Sprintf("%s 프로젝트에 pending 작업이 %d개", name, count),
			Message:     fmt.Sprintf("가장 오래된 작업 #%d가 %s 넘게 대기를 기다립니다.", oldestTaskID, formatMinutes(stale)),
			ActionKind:  "open_project",
			TargetName:  name,
			Project:     name,
			CreatedAt:   oldest,
			Detail:      map[string]interface{}{"count": count, "oldest_task_id": oldestTaskID},
		}

		if sheepName.Valid {
			item.Detail["assigned_sheep"] = sheepName.String
			if runningID.Valid {
				// Serial-wait evidence: name the live running task the queue
				// is lined up behind, whatever else this item reports.
				item.Detail["draining_behind_task"] = int(runningID.Int64)
				msg := fmt.Sprintf(" 담당 양 %s이(가) 지금 #%d를 실행 중이므로, 이 pending은 직렬 큐에서 순서를 기다리는 것일 수 있습니다.",
					sheepName.String, runningID.Int64)
				if st, ok2 := parseStoredTime(runningStartedRaw.String); ok2 {
					item.Detail["draining_started_at"] = st.Format(time.RFC3339)
					msg += fmt.Sprintf(" 실행 시작 시각은 %s입니다.", st.Format("2006-01-02 15:04"))
				}
				item.Message += msg
				if deadzoneCount == 0 {
					// Pure serial-wait shape — nothing in the project looks
					// dispatch-stuck — so the alarm is worth less than a
					// real (assigned sheep idle) backlog.
					item.Severity = 2
				}
			} else {
				item.Message += fmt.Sprintf(" 담당 양 %s은(는) 지금 대기 상태(idle)이므로 실제 정체일 가능성이 있습니다.", sheepName.String)
			}
		}
		if deadzoneCount > 0 {
			item.Detail["manager_pending_no_kind"] = deadzoneCount
			item.Message += fmt.Sprintf(
				" 같은 프로젝트에 목자(shepherd) 양에게 배정되었고 shepherd_kind이 빈 pending이 %d개 있습니다. #629 수정이 배포되기 전의 데몬에서는 이런 작업이 실행되지 않고 고착됩니다.",
				deadzoneCount)
		}
		items = append(items, item)
	}
	return items, nil
}

// ---- kind: review_limit (severity 3) -------------------------------------
// Watched issues that reached shepherd_rounds >= shepherd_max_review_rounds:
// wake.go refuses to wake the shepherd for them anymore (#597 rule 5), so a
// human has to reset the rounds or finish the issue. One query; scope is
// enforced through the project edge predicate.
// Fingerprint embeds the round, so resetting rounds and hitting the cap
// again (rounds climbing to max again) produces a new item.

func attentionReviewLimits(ctx context.Context, projectIDs []int) ([]AttentionItem, error) {
	countQuery(AttentionReviewLimit)
	maxRounds := maxReviewRounds()
	issues, err := db.Client().Issue.Query().
		Where(
			entIssue.ShepherdWatch(true),
			entIssue.ShepherdRoundsGTE(maxRounds),
			entIssue.HasProjectWith(project.IDIn(projectIDs...)),
		).
		WithProject().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("review_limit query: %w", err)
	}

	items := make([]AttentionItem, 0, len(issues))
	for _, iss := range issues {
		projectName := ""
		if p := iss.Edges.Project; p != nil {
			projectName = p.Name
		}
		items = append(items, AttentionItem{
			Kind:        AttentionReviewLimit,
			Severity:    3,
			Fingerprint: fmt.Sprintf("%s:%d:%d", AttentionReviewLimit, iss.ID, iss.ShepherdRounds),
			Title:       fmt.Sprintf("이슈 #%d가 검토 회차 상한에 닿았습니다", iss.ID),
			Message:     fmt.Sprintf("회차 %d/%d — 회차를 초기화한 뒤 다시 적재하거나 이슈를 마감하세요.", iss.ShepherdRounds, maxRounds),
			ActionKind:  "open_issue",
			TargetID:    iss.ID,
			TargetName:  iss.Title,
			Project:     projectName,
			CreatedAt:   issueActivityTime(iss),
			Detail:      map[string]interface{}{"rounds": iss.ShepherdRounds, "max_rounds": maxRounds},
		})
	}
	return items, nil
}

// ---- kind: user_stopped (severity 2) --------------------------------------
// Tasks the user stopped on a watched issue (#597 wake rule 3): the shepherd
// deliberately does not wake, so the panel must remind the user that the
// issue is sitting there untouched. One query: stopped tasks whose error
// column matches a user-stop reason, still linked to a watched issue.
// Fingerprint: user_stopped:<task id>.

func attentionUserStopped(ctx context.Context, projectIDs []int) ([]AttentionItem, error) {
	countQuery(AttentionUserStopped)
	tasks, err := db.Client().Task.Query().
		Where(
			entTask.StatusEQ(entTask.StatusStopped),
			entTask.HasProjectWith(project.IDIn(projectIDs...)),
			entTask.ErrorIn(userStopReasons()...),
			entTask.HasIssueWith(entIssue.ShepherdWatch(true)),
		).
		WithIssue().WithProject().
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("user_stopped query: %w", err)
	}

	items := make([]AttentionItem, 0, len(tasks))
	for _, t := range tasks {
		iss := t.Edges.Issue
		if iss == nil {
			continue
		}
		it := AttentionItem{
			Kind:        AttentionUserStopped,
			Severity:    2,
			Fingerprint: fmt.Sprintf("%s:%d", AttentionUserStopped, t.ID),
			Title:       fmt.Sprintf("이슈 #%d의 작업 #%d를 사용자가 중지했습니다", iss.ID, t.ID),
			Message:     "사용자가 직접 중지한 작업입니다. 감시 이슈로 남아 있지만 목자는 깨어나지 않습니다.",
			ActionKind:  "open_issue",
			TargetID:    iss.ID,
			TargetName:  iss.Title,
			CreatedAt:   t.CompletedAt,
			Detail:      map[string]interface{}{"task_id": t.ID},
		}
		if p := t.Edges.Project; p != nil {
			it.Project = p.Name
		}
		items = append(items, it)
	}
	return items, nil
}

// userStopReasons mirrors queue.isUserStopReason (the queue package does not
// export it; wake rules pin the same strings).
func userStopReasons() []string {
	return []string{
		"stopped by user",
		"cancelled by user",
		"task was cancelled",
		"cancelled: stale task from previous session",
	}
}

// ---- kind: shepherd_blocked (severity 7) ----------------------------------
// Pending shepherd tasks that cannot dispatch. Two block states are reported:
//   - workspace unset/invalid → the dispatcher leaves the task pending
//     forever (#594); the fix lives in Settings → 목자;
//   - provider disabled → the run cannot start; same destination.
// A tripped circuit breaker is NOT this kind — circuit_open covers it with
// its own reset action. One query fetches the oldest pending shepherd task;
// the workspace/provider state decides whether it is reported. The
// fingerprint is state-scoped: a fixed one per reason means acking hides the
// nag until the *reason* changes, which is what an operator expects for a
// settings problem.

func attentionShepherdBlocked(ctx context.Context) ([]AttentionItem, error) {
	countQuery(AttentionShepherdBlocked)
	t, err := db.Client().Task.Query().
		Where(
			entTask.StatusEQ(entTask.StatusPending),
			entTask.ShepherdKindNEQ(""),
		).
		Order(ent.Asc(entTask.FieldPriority), ent.Asc(entTask.FieldID)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return []AttentionItem{}, nil
		}
		return nil, fmt.Errorf("shepherd_blocked query: %w", err)
	}

	mgr, err := db.Client().Sheep.Query().Where(sheep.Name(names.ManagerName)).Only(ctx)
	if err != nil {
		// No manager row — pending shepherd tasks could not exist without one.
		return []AttentionItem{}, nil
	}

	reason := ""
	fingerprint := ""
	switch {
	case !config.IsProviderEnabled(string(mgr.Provider)):
		reason = fmt.Sprintf("목자의 프로바이더 '%s'가 설정에서 꺼져 있습니다.", string(mgr.Provider))
		fingerprint = AttentionShepherdBlocked + ":provider:" + string(mgr.Provider)
	default:
		if _, wsErr := config.ValidateWorkspacePath(config.GetString("workspace_path")); wsErr != nil {
			reason = "워크스페이스가 지정되지 않았거나 유효하지 않습니다."
			fingerprint = AttentionShepherdBlocked + ":workspace"
		}
	}
	if reason == "" {
		return []AttentionItem{}, nil
	}

	return []AttentionItem{{
		Kind:        AttentionShepherdBlocked,
		Severity:    7,
		Fingerprint: fingerprint,
		Title:       fmt.Sprintf("목자 작업 #%d가 pending에 머물러 있습니다", t.ID),
		Message:     reason + " 설정의 목자 탭을 확인하세요.",
		ActionKind:  "open_settings",
		CreatedAt:   t.CreatedAt,
		Detail:      map[string]interface{}{"task_id": t.ID, "shepherd_kind": t.ShepherdKind},
	}}, nil
}

// ---- kind: cost_limit (severity 6) ----------------------------------------
// Today's combined spend (shepherd tasks + watched-issue tasks, #601) has
// reached shepherd_daily_cost_limit_usd: automatic waking and shepherd
// delegation are refused until local midnight rolls the window over. The
// fingerprint embeds the LOCAL DATE, so yesterday's ack never suppresses
// today's item and the item disappears by itself the next day (the window
// resets; no cleanup job needed). One query; limit<=0 means "no cap" and
// reports nothing.

func attentionCostLimit(now time.Time) ([]AttentionItem, error) {
	countQuery(AttentionCostLimit)
	exceeded, spent, limit, err := CostLimitStatus(now)
	if err != nil {
		return nil, fmt.Errorf("cost_limit query: %w", err)
	}
	if !exceeded {
		return []AttentionItem{}, nil
	}
	return []AttentionItem{{
		Kind:        AttentionCostLimit,
		Severity:    6,
		Fingerprint: fmt.Sprintf("%s:%s", AttentionCostLimit, now.Format("2006-01-02")),
		Title:       fmt.Sprintf("하루 비용 상한에 닿았습니다 ($%.2f/$%.2f)", spent, limit),
		Message:     "목자 작업과 감시 이슈 작업의 오늘 합산 비용이 상한을 넘었습니다. 자동 깨우기와 목자의 이슈 적재가 거부됩니다. 자정이 지나면 자동으로 풀립니다.",
		ActionKind:  "open_settings",
		CreatedAt:   now,
		Detail:      map[string]interface{}{"spent_usd": spent, "limit_usd": limit},
	}}, nil
}

// ---- acks -----------------------------------------------------------------

// AckAttention stores a fingerprint so CollectAttention hides it from then on.
// Re-acking the same fingerprint refreshes its 30-day clock. (The upsert code
// generation feature is not enabled in this project, so this is a get →
// update-or-create pair instead of ON CONFLICT.)
func AckAttention(fingerprint string) error {
	fingerprint = strings.TrimSpace(fingerprint)
	if fingerprint == "" {
		return errors.New("fingerprint is required")
	}
	ctx := context.Background()
	client := db.Client()

	existing, err := client.ShepherdAck.Query().
		Where(entshepherdack.Fingerprint(fingerprint)).
		Only(ctx)
	switch {
	case err == nil:
		if _, err := client.ShepherdAck.UpdateOneID(existing.ID).
			SetAckedAt(time.Now()).
			Save(ctx); err != nil {
			return fmt.Errorf("ack attention: %w", err)
		}
		return nil
	case !ent.IsNotFound(err):
		return fmt.Errorf("ack attention: %w", err)
	}

	if err := client.ShepherdAck.Create().
		SetFingerprint(fingerprint).
		Exec(ctx); err != nil {
		return fmt.Errorf("ack attention: %w", err)
	}
	return nil
}

// ackedFingerprints returns every unexpired ack (one query).
func ackedFingerprints(ctx context.Context) (map[string]bool, error) {
	rows, err := db.Client().ShepherdAck.Query().All(ctx)
	if err != nil {
		return nil, fmt.Errorf("ack query: %w", err)
	}
	set := make(map[string]bool, len(rows))
	for _, r := range rows {
		set[r.Fingerprint] = true
	}
	return set, nil
}

// pruneAcks deletes acks older than 30 days. Cheap (indexed on acked_at), and
// running it inside every collection means no separate janitor is needed.
func pruneAcks(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-ackRetention)
	_, err := db.Client().ShepherdAck.Delete().
		Where(entshepherdack.AckedAtLT(cutoff)).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("prune acks: %w", err)
	}
	return nil
}

// ---- helpers ---------------------------------------------------------------

// projectIDsOf extracts ids from a scope slice (kept out of the hot path of
// each kind so the scope is queried exactly once per collection).
func projectIDsOf(scope []*ent.Project) []int {
	ids := make([]int, 0, len(scope))
	for _, p := range scope {
		ids = append(ids, p.ID)
	}
	return ids
}

// parseStoredTime parses the TEXT form ent's SQLite driver stores for
// time.Time columns (Go's time.String(), optionally with the monotonic
// "m=+..." suffix). Returns ok=false for NULL/empty/unparseable values.
func parseStoredTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "NULL" {
		return time.Time{}, false
	}
	if i := strings.Index(s, " m="); i >= 0 {
		s = s[:i]
	}
	layouts := []string{
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999Z07:00",
		time.RFC3339Nano,
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

// firstLine returns the first non-empty line of text, capped for panel use.
func firstLine(text, fallback string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return fallback
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		text = text[:idx]
	}
	runes := []rune(text)
	if len(runes) > 160 {
		return string(runes[:160]) + "…"
	}
	return string(runes)
}

func formatMinutes(m int) string {
	if m < 60 {
		return fmt.Sprintf("%d분", m)
	}
	h := m / 60
	if h >= 24 && h%24 == 0 {
		return fmt.Sprintf("%d일", h/24)
	}
	return fmt.Sprintf("%d시간", h)
}

// issueActivityTime picks the most recent meaningful timestamp of an issue
// for panel ordering.
func issueActivityTime(iss *ent.Issue) time.Time {
	if iss.CompletedAt != nil && !iss.CompletedAt.IsZero() {
		return *iss.CompletedAt
	}
	if iss.StartedAt != nil && !iss.StartedAt.IsZero() {
		return *iss.StartedAt
	}
	return iss.UpdatedAt
}
