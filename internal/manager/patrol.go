// Periodic patrol loop for the shepherd (#600).
//
// Every interval the daemon re-checks the whole management scope with plain
// code — CollectAttention (#599), no LLM — and compares the un-acked
// fingerprints against the previous pass. Only fingerprints that were NOT in
// the previous pass count as new. When at least one is new, waking is
// allowed, no patrol task of the shepherd is still pending/running, and
// today's wake budget (shepherd_max_wakeups_per_day, shared with review
// wakes) has room, the loop enqueues a `patrol` shepherd task through
// EnqueueShepherdTask. Otherwise it records why it skipped.
//
// Every pass — wake or not, findings or none — stores one shepherd_patrols
// row (findings JSON, new_count, task_id, skipped_reason). Rows older than 30
// days are pruned by the pass itself, same janitor-free trick as the acks.
//
// Loop shape mirrors internal/scheduler: a goroutine with a ticker and a
// stop channel. The ticker beats once per minute (patrolHeartbeat) and each
// beat re-reads shepherd_patrol_interval, so a settings change takes effect
// from the next beat without restarting the loop. "0" turns patrolling off;
// any positive value is clamped to a 5-minute floor. Tests inject the clock
// and the collection function (Patrol fields / RunPatrolPassWithCollect) so
// no test has to wait in real time.
package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	entpatrol "github.com/agurrrrr/shepherd/ent/shepherdpatrol"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
)

// Patrol defaults and floors (#600).
const (
	defaultPatrolInterval = 30 * time.Minute // shepherd_patrol_interval unset/typo
	minPatrolInterval     = 5 * time.Minute  // any nonzero setting is raised to this
	patrolHeartbeat       = time.Minute      // loop beat; interval gate runs per beat
	patrolRetention       = 30 * 24 * time.Hour
)

// Skipped reasons stored on shepherd_patrols rows. The wording is user-facing
// (the Shepherd screen prints it verbatim), so it stays Korean.
const (
	SkipWakeDisabled   = "깨우기 꺼짐"
	SkipPatrolBusy     = "순찰 작업 진행 중"
	SkipDailyCap       = "하루 깨우기 상한 도달"
	SkipCostCap        = "하루 비용 상한 도달"
	SkipCollectionFail = "점검 실패"
)

// PatrolFinding is one attention item stored in a patrol record. The
// fingerprint list of the previous record is what "new" is measured against;
// title/message ride along so the UI (and a reviewing human) can read a pass
// without re-running the collection.
type PatrolFinding struct {
	Fingerprint string `json:"fingerprint"`
	Kind        string `json:"kind"`
	Severity    int    `json:"severity"`
	Title       string `json:"title"`
	Message     string `json:"message"`
	Project     string `json:"project,omitempty"`
}

// PatrolRecord is the API shape of one shepherd_patrols row. TaskSummary is
// the first line of the woken task's summary/error (empty when nothing was
// woken or the task vanished), filled in by ListPatrols.
type PatrolRecord struct {
	ID            int             `json:"id"`
	CreatedAt     time.Time       `json:"created_at"`
	NewCount      int             `json:"new_count"`
	TaskID        int             `json:"task_id,omitempty"`
	TaskSummary   string          `json:"task_summary,omitempty"`
	SkippedReason string          `json:"skipped_reason,omitempty"`
	Findings      []PatrolFinding `json:"findings"`
}

// ---- settings --------------------------------------------------------------

// PatrolInterval resolves shepherd_patrol_interval: "" / unparseable → 30m,
// "0" (or any non-positive duration) → off, otherwise clamped up to the
// 5-minute floor. Reading it on every beat is what makes a settings change
// apply from the next tick.
func PatrolInterval() time.Duration {
	raw := strings.TrimSpace(config.GetString("shepherd_patrol_interval"))
	if raw == "" {
		return defaultPatrolInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return defaultPatrolInterval // typo guard, mirrors GetTaskTimeout
	}
	if d <= 0 {
		return 0 // explicit "0" (or negative) = off
	}
	if d < minPatrolInterval {
		return minPatrolInterval
	}
	return d
}

// patrolWakeEnabled reports shepherd_patrol_wake. Unset defaults to true —
// tests run with viper.Reset() so the absence has to mean the documented
// default, not GetBool's false.
func patrolWakeEnabled() bool {
	if v := config.Get("shepherd_patrol_wake"); v == nil {
		return true
	}
	return config.GetBool("shepherd_patrol_wake")
}

// NextPatrolAt answers the status card's "다음 순찰" cell. Off → ok=false.
// The baseline is the newest patrol row (a manual run reschedules the auto
// pass too); with no rows yet the next beat runs a pass, so "now" is honest.
func NextPatrolAt(now time.Time) (time.Time, bool) {
	interval := PatrolInterval()
	if interval <= 0 {
		return time.Time{}, false
	}
	last, err := latestPatrolCreatedAt(now)
	if err != nil || last.IsZero() {
		return now, true
	}
	return last.Add(interval), true
}

func latestPatrolCreatedAt(now time.Time) (time.Time, error) {
	row, err := db.Client().ShepherdPatrol.Query().
		Order(ent.Desc(entpatrol.FieldCreatedAt)).
		Select(entpatrol.FieldCreatedAt).
		First(context.Background())
	if err != nil {
		if ent.IsNotFound(err) {
			return time.Time{}, nil
		}
		return time.Time{}, err
	}
	return row.CreatedAt, nil
}

// ---- the pass --------------------------------------------------------------

// runMu serializes passes: the ticker beat and POST /api/shepherd/patrols/run
// must not diff-and-store concurrently (the second would diff against the
// first's row half-written).
var runMu sync.Mutex

// RunPatrolPass runs one pass at `now` with the real collection and returns
// the stored record. It is the single entry point shared by the ticker loop
// and the manual "지금 순찰" button, so both obey exactly the same rules.
func RunPatrolPass(now time.Time) (*PatrolRecord, error) {
	return RunPatrolPassWithCollect(now, CollectAttention)
}

// RunPatrolPassWithCollect is the seam-injected form: tests hand in a fake
// collector (fixed items or an error) and never wait or seed every attention
// kind. Everything else — diffing, wake gates, the record — is the production
// path.
func RunPatrolPassWithCollect(now time.Time, collect func(time.Time) ([]AttentionItem, error)) (*PatrolRecord, error) {
	runMu.Lock()
	defer runMu.Unlock()

	ctx := context.Background()
	if db.Client() == nil {
		return nil, fmt.Errorf("database is not initialized")
	}

	items, collectErr := collect(now)

	// Cost-limit state for this pass (#601 §3): when today's combined spend
	// has reached the cap, automatic waking stops (the SkipCostCap branch
	// below) and the one-per-day notice fires. Computing it once here keeps
	// the notice independent of the other wake gates — a pass with patrolling
	// off in the wake switch still notifies. CollectAttention raises the
	// cost_limit attention item for the panel.
	costExceeded, costSpent, costLimit := false, 0.0, 0.0
	if collectErr == nil {
		var costErr error
		costExceeded, costSpent, costLimit, costErr = CostLimitStatus(now)
		if costErr != nil {
			log.Printf("[shepherd-patrol] cost-limit check: %v", costErr)
			costExceeded = true // fail closed like wakeBudgetExhausted: no wakes on a broken count
		} else if costExceeded {
			MaybeNotifyCostLimit(now, costSpent, costLimit)
		}
	}

	// The previous pass's fingerprints are the baseline for "new". No previous
	// pass (fresh install / retention wiped everything) means every current
	// item is new — the first pass after enabling patrol reports what is
	// already broken, which is what the operator just enabled it for.
	prevFingerprints, err := previousFindingSet(ctx)
	if err != nil {
		return nil, err
	}

	findings := make([]PatrolFinding, 0, len(items))
	newItems := make([]AttentionItem, 0, 4)
	for _, it := range items {
		findings = append(findings, PatrolFinding{
			Fingerprint: it.Fingerprint,
			Kind:        it.Kind,
			Severity:    it.Severity,
			Title:       it.Title,
			Message:     it.Message,
			Project:     it.Project,
		})
		if !prevFingerprints[it.Fingerprint] {
			newItems = append(newItems, it)
		}
	}

	rec := &PatrolRecord{
		CreatedAt: now,
		NewCount:  len(newItems),
		Findings:  findings,
	}

	if collectErr != nil {
		// Record the failure but never wake on it — a broken query is not a
		// finding about the flock, and a shepherd task could do nothing about
		// it.
		rec.SkippedReason = SkipCollectionFail + ": " + collectErr.Error()
	} else if len(newItems) > 0 {
		switch {
		case !patrolWakeEnabled():
			rec.SkippedReason = SkipWakeDisabled
		case patrolTaskActive(ctx):
			rec.SkippedReason = SkipPatrolBusy
		case wakeBudgetExhausted(now):
			rec.SkippedReason = SkipDailyCap
		case costExceeded:
			rec.SkippedReason = SkipCostCap
		default:
			t, enqErr := EnqueueShepherdTask(KindPatrol, buildPatrolPrompt(newItems), 0)
			if enqErr != nil {
				rec.SkippedReason = "목자 작업 적재 실패: " + enqErr.Error()
			} else {
				rec.TaskID = t.ID
				if wakeProcessor != nil {
					wakeProcessor.ProcessPendingNow()
				}
				log.Printf("[shepherd-patrol] %d new finding(s) — enqueued patrol task #%d", len(newItems), t.ID)
			}
		}
	}

	raw, err := json.Marshal(findings)
	if err != nil {
		return nil, fmt.Errorf("marshal findings: %w", err)
	}

	stored, err := db.Client().ShepherdPatrol.Create().
		SetCreatedAt(now).
		SetFindings(string(raw)).
		SetNewCount(rec.NewCount).
		SetTaskID(rec.TaskID).
		SetSkippedReason(rec.SkippedReason).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("store patrol record: %w", err)
	}
	rec.ID = stored.ID

	if err := prunePatrols(ctx, now); err != nil {
		log.Printf("[shepherd-patrol] prune: %v", err) // housekeeping; the pass itself succeeded
	}

	return rec, nil
}

// previousFindingSet returns the fingerprints of the newest patrol row. A row
// with empty/corrupt findings contributes nothing (everything counts as new)
// rather than failing the pass — self-healing, same spirit as the wake path's
// fail-open checks.
func previousFindingSet(ctx context.Context) (map[string]bool, error) {
	row, err := db.Client().ShepherdPatrol.Query().
		Order(ent.Desc(entpatrol.FieldCreatedAt), ent.Desc(entpatrol.FieldID)).
		Select(entpatrol.FieldFindings).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("load previous patrol: %w", err)
	}
	set := map[string]bool{}
	if strings.TrimSpace(row.Findings) == "" {
		return set, nil
	}
	var findings []PatrolFinding
	if err := json.Unmarshal([]byte(row.Findings), &findings); err != nil {
		return set, nil
	}
	for _, f := range findings {
		set[f.Fingerprint] = true
	}
	return set, nil
}

// patrolTaskActive reports whether the shepherd already owns a pending or
// running patrol task — one wake per outstanding pass, no stacking. Unlike
// review wakes, patrol passes do NOT coalesce into the pending task's prompt:
// the pending pass re-collects when it runs, so a second wake would only
// duplicate work.
func patrolTaskActive(ctx context.Context) bool {
	n, err := db.Client().Task.Query().
		Where(
			entTask.ShepherdKindEQ(KindPatrol),
			entTask.StatusIn(entTask.StatusPending, entTask.StatusRunning),
		).
		Count(ctx)
	if err != nil {
		// Fail open like wake.go's bundle check: a query hiccup must not
		// silently drop the wake.
		log.Printf("[shepherd-patrol] patrol-task check: %v", err)
		return false
	}
	return n > 0
}

// wakeBudgetExhausted applies the shared per-day cap (#597 key; patrol wakes
// now count toward it too — wakeupsToday counts review AND patrol kinds).
func wakeBudgetExhausted(now time.Time) bool {
	today, err := wakeupsToday()
	if err != nil {
		log.Printf("[shepherd-patrol] wake budget check: %v", err)
		return true // a broken count must not spam wakes; skip this pass
	}
	return today >= maxWakeupsPerDay()
}

// prunePatrols deletes records older than 30 days.
func prunePatrols(ctx context.Context, now time.Time) error {
	_, err := db.Client().ShepherdPatrol.Delete().
		Where(entpatrol.CreatedAtLT(now.Add(-patrolRetention))).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("prune patrols: %w", err)
	}
	return nil
}

// ---- the loop --------------------------------------------------------------

// Patrol is the daemon's ticker wrapper. The clock and the collector are
// fields so tests drive ticks without sleeping and without seeding every
// attention kind.
type Patrol struct {
	stopCh  chan struct{}
	running bool
	mu      sync.Mutex

	clock   func() time.Time
	collect func(time.Time) ([]AttentionItem, error) // nil → CollectAttention
}

// NewPatrol creates the loop with production seams.
func NewPatrol() *Patrol {
	return &Patrol{
		stopCh:  make(chan struct{}),
		clock:   time.Now,
		collect: nil,
	}
}

// Start begins the beat loop (mirrors scheduler.Scheduler.Start).
func (p *Patrol) Start() {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.stopCh = make(chan struct{})
	p.mu.Unlock()

	go p.loop()
}

// Stop halts the beat loop.
func (p *Patrol) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running {
		return
	}
	p.running = false
	close(p.stopCh)
}

// IsRunning reports whether the loop goroutine is live. The interval setting
// decides whether a live loop actually runs passes (see beat).
func (p *Patrol) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

func (p *Patrol) loop() {
	ticker := time.NewTicker(patrolHeartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.beat(p.clock())
		case <-p.stopCh:
			return
		}
	}
}

// beat applies the interval gate for one tick. Off ("0") → nothing happens,
// which is also what makes the "주기 0이면 루프가 돌지 않는다" rule testable
// without a goroutine: beat with interval 0 must never reach the pass.
func (p *Patrol) beat(now time.Time) {
	interval := PatrolInterval()
	if interval <= 0 {
		return
	}
	last, err := latestPatrolCreatedAt(now)
	if err != nil {
		log.Printf("[shepherd-patrol] last-pass lookup: %v", err)
		return // retry on the next beat; a DB hiccup must not wedge the loop
	}
	if !last.IsZero() && now.Sub(last) < interval {
		return // not due yet under the CURRENT setting — a shortened interval
	}                      // applies from the next beat
	p.runPass(now)
}

// runPass is the loop's pass call, routed through the collect seam.
func (p *Patrol) runPass(now time.Time) {
	if p.collect == nil {
		if _, err := RunPatrolPass(now); err != nil {
			log.Printf("[shepherd-patrol] pass failed: %v", err)
		}
		return
	}
	if _, err := RunPatrolPassWithCollect(now, p.collect); err != nil {
		log.Printf("[shepherd-patrol] pass failed: %v", err)
	}
}

// ---- prompt ----------------------------------------------------------------

const patrolPromptHeader = `[Shepherd patrol report]
The periodic patrol (plain code checks, no LLM) found problems that were NOT present at the previous pass. Work through the list; act on what you are allowed to act on.`

// patrolActionNotice is the fixed closing instruction. #601 gave the shepherd
// the request_user_action tool, so stop/delete/settings asks now go through a
// user-approval request instead of only the final report.
const patrolActionNotice = "Do the actions you are allowed to do yourself: re-enqueue issues with added guidance (issue_upsert + issue_execute), create follow-up issues, close issues whose goal is met. Anything that needs stopping a task, deleting things or changing settings: raise it with request_user_action(kind, project_name, target_id, reason) and note it in your final message too — never work around a permission refusal."

// patrolActionCandidates maps an attention kind to the shepherd-side action
// candidate printed under each item. These mirror AttentionPanel's buttons
// (#599) translated into what the shepherd is allowed to do.
var patrolActionCandidates = map[string]string{
	AttentionFailedTask:      "Candidate: read get_task_detail for the task; if the linked issue needs guidance, append it (issue_upsert) and re-enqueue the SAME issue (issue_execute); otherwise create a follow-up issue.",
	AttentionLongRunning:     "Candidate: inspect with get_task_detail/get_status. Stopping a task is a user action — report the task id and what it appears stuck on.",
	AttentionCircuitOpen:     "Candidate: resetting a sheep's circuit breaker is a sheep-side change — report the sheep name and the failure count; do not touch sheep settings yourself.",
	AttentionPendingBacklog:  "Candidate: check why the queue stalls (get_status). If a stalled issue only needs re-enqueuing, do it; otherwise report the project and the oldest task id.",
	AttentionReviewLimit:     "Candidate: if the issue's goal is met, close it (issue_upsert status=done); if not, create a follow-up issue with new scope. Resetting review rounds is a user action — mention it.",
	AttentionUserStopped:     "Candidate: the user stopped this on purpose — do NOT re-enqueue; note it in your report only.",
	AttentionShepherdBlocked: "Candidate: a settings problem (workspace/provider) blocks shepherd work — report which setting to fix; you cannot change settings yourself.",
}

// buildPatrolPrompt renders the NEW items only — the patrol task's own run
// re-reads the full picture through the attention panel / get_status; the
// prompt's job is to say what changed since the last pass.
func buildPatrolPrompt(newItems []AttentionItem) string {
	var sb strings.Builder
	sb.WriteString(patrolPromptHeader)
	sb.WriteString(fmt.Sprintf("\n\nNew problems since the previous patrol (%d):\n\n", len(newItems)))

	for _, it := range newItems {
		target := ""
		switch {
		case it.TargetID > 0 && it.TargetName != "":
			target = fmt.Sprintf("target #%d %q", it.TargetID, it.TargetName)
		case it.TargetID > 0:
			target = fmt.Sprintf("target #%d", it.TargetID)
		case it.TargetName != "":
			target = fmt.Sprintf("target %q", it.TargetName)
		}
		project := it.Project
		if project == "" {
			project = "-"
		}
		sb.WriteString(fmt.Sprintf("- [%s severity %d] project: %s | %s\n  %s: %s\n",
			it.Kind, it.Severity, project, target, it.Title, it.Message))
		if cand, ok := patrolActionCandidates[it.Kind]; ok {
			sb.WriteString("  " + cand + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString(patrolActionNotice)
	return sb.String()
}

// ---- listing for the API ---------------------------------------------------

// ListPatrols returns the newest patrol records with the woken task's first
// summary line attached (the console's task list already links by id).
func ListPatrols(limit int) ([]PatrolRecord, error) {
	ctx := context.Background()
	if limit <= 0 {
		limit = 20
	}

	rows, err := db.Client().ShepherdPatrol.Query().
		Order(ent.Desc(entpatrol.FieldCreatedAt), ent.Desc(entpatrol.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list patrols: %w", err)
	}

	taskIDs := make([]int, 0, len(rows))
	for _, r := range rows {
		if r.TaskID > 0 {
			taskIDs = append(taskIDs, r.TaskID)
		}
	}

	summaries := map[int]string{}
	if len(taskIDs) > 0 {
		tasks, err := db.Client().Task.Query().
			Where(entTask.IDIn(taskIDs...)).
			Select(entTask.FieldID, entTask.FieldSummary, entTask.FieldError).
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("patrol task summaries: %w", err)
		}
		for _, t := range tasks {
			text := strings.TrimSpace(t.Summary)
			if text == "" {
				text = strings.TrimSpace(t.Error)
			}
			if line := firstLine(text, ""); line != "" {
				summaries[t.ID] = line
			}
		}
	}

	records := make([]PatrolRecord, 0, len(rows))
	for _, r := range rows {
		rec := PatrolRecord{
			ID:            r.ID,
			CreatedAt:     r.CreatedAt,
			NewCount:      r.NewCount,
			TaskID:        r.TaskID,
			SkippedReason: r.SkippedReason,
			Findings:      []PatrolFinding{},
		}
		rec.TaskSummary = summaries[r.TaskID]
		if strings.TrimSpace(r.Findings) != "" {
			var fs []PatrolFinding
			if err := json.Unmarshal([]byte(r.Findings), &fs); err == nil {
				rec.Findings = fs
			}
		}
		records = append(records, rec)
	}
	return records, nil
}
