package queue

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/checkpoint"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/wiki"
	"github.com/agurrrrr/shepherd/internal/worker"
)

const (
	// Rate limit retry settings
	MaxRateLimitRetries = 3                // Maximum retries on rate limit
	InitialRetryDelay   = 30 * time.Second // Initial backoff delay
	MaxRetryDelay       = 5 * time.Minute  // Maximum backoff delay

	// Circuit breaker settings
	CircuitBreakerThreshold = 5 // Consecutive failures to trip breaker

	// maxOutputLinesBytes is the byte budget for the per-task output lines
	// collected in processor.go for DB storage. When exceeded, the slice is
	// trimmed to head + tail with a truncation marker, preserving both the
	// beginning and the end of the task output.
	maxOutputLinesBytes = 20 * 1024 * 1024 // 20 MB

	// checkpointTimeout bounds a single start/end snapshot so a huge or wedged
	// repository cannot stall the queue.
	checkpointTimeout = 60 * time.Second
)

// outputTruncationMarker separates the preserved head from the tail in
// capOutputLinesHeadTail output. Its presence at the front of the slice marks
// a slice that has already been trimmed once.
const outputTruncationMarker = "...[output truncated — head + tail preserved]..."

// capOutputLinesHeadTail trims an output lines slice to fit within
// maxOutputLinesBytes, keeping the first ~20% (head) and the last ~80%
// (tail) with a truncation marker inserted between them. Returns the
// trimmed slice and its total byte size.
//
// The head/tail split ratio favors the tail because the end of the output
// typically contains the task result and summary, which are more valuable
// for debugging than the beginning.
//
// The first trim caps the tail at the full tail budget. Later trims keep the
// already-decided head (+ marker) untouched and shrink only the tail to 80%
// of its budget, so trimming runs at multi-MB intervals instead of every few
// lines once the slice sits right at the limit.
func capOutputLinesHeadTail(lines []string) ([]string, int) {
	// Calculate total bytes.
	totalBytes := 0
	for _, l := range lines {
		totalBytes += len(l)
	}

	if totalBytes <= maxOutputLinesBytes {
		return lines, totalBytes
	}

	headBudget := maxOutputLinesBytes / 5          // 4 MB
	tailBudget := maxOutputLinesBytes - headBudget // 16 MB

	// If the slice was trimmed before, the head is already fixed as
	// lines[:markerIdx]. Keep it verbatim and only re-trim the tail,
	// leaving 20% headroom so the next trim is not triggered immediately.
	markerIdx := -1
	for i, l := range lines {
		if l == outputTruncationMarker {
			markerIdx = i
			break
		}
	}
	if markerIdx >= 0 {
		// Head is lines[:markerIdx]; the old marker is replaced by exactly
		// one new marker so duplicate markers never accumulate.
		headBytes := 0
		for _, l := range lines[:markerIdx] {
			headBytes += len(l)
		}
		tail, tailBytes := keepTail(lines, markerIdx+1, tailBudget*4/5)
		result := make([]string, 0, markerIdx+len(tail)+1)
		result = append(result, lines[:markerIdx]...)
		result = append(result, outputTruncationMarker)
		result = append(result, tail...)
		return result, headBytes + len(outputTruncationMarker) + tailBytes
	}

	// First trim: collect head lines (from the start) and tail lines
	// (from the end) within their budgets, ensuring no overlap.
	headEnd := 0
	headBytes := 0
	for i, l := range lines {
		if headBytes+len(l) > headBudget {
			break
		}
		headBytes += len(l)
		headEnd = i + 1
	}

	// Reserve room for the marker so the first trim stays within the budget.
	tail, tailBytes := keepTail(lines, headEnd, tailBudget-len(outputTruncationMarker))

	result := make([]string, 0, headEnd+len(tail)+1)
	result = append(result, lines[:headEnd]...)
	result = append(result, outputTruncationMarker)
	result = append(result, tail...)
	return result, headBytes + len(outputTruncationMarker) + tailBytes
}

// keepTail returns lines[from:] trimmed from the front so the kept bytes stay
// within budget. from is the index just after the head region (or after the
// previous marker). When even one tail line exceeds the budget, the newest
// (last) line is still kept.
func keepTail(lines []string, from, budget int) ([]string, int) {
	tailStart := len(lines)
	tailBytes := 0
	for i := len(lines) - 1; i >= from; i-- {
		if tailBytes > 0 && tailBytes+len(lines[i]) > budget {
			break
		}
		tailBytes += len(lines[i])
		tailStart = i
	}
	return lines[tailStart:], tailBytes
}

// Processor handles automatic execution of pending tasks.
type Processor struct {
	interval time.Duration
	stopCh   chan struct{}
	running  bool
	mu       sync.Mutex // guards running flag / Start / Stop
	// dispatchMu serializes checkAndExecutePendingTasks so concurrent
	// ProcessPendingNow + ticker cannot double-dispatch the same sheep.
	dispatchMu sync.Mutex

	// shepherdWorkspaceWarned records the last workspace-validation failure
	// logged by dispatchShepherd, so a misconfigured workspace logs once on
	// the transition instead of every tick. Guarded by dispatchMu.
	shepherdWorkspaceWarned string

	// Callbacks: called on task start/complete/fail/stop
	OnTaskStart    func(taskID int, sheepName, projectName, prompt string)
	OnTaskComplete func(taskID int, sheepName, projectName, summary string)
	OnTaskFail     func(taskID int, sheepName, projectName, errMsg string)
	OnTaskStop     func(taskID int, sheepName, projectName, reason string)

	// Callback: output streaming (projectName is the project assigned to the task)
	OnOutput func(sheepName, projectName, text string)
	// Callback: status change (working, idle, error)
	OnStatusChange func(sheepName, status string)
}

// shouldAutoHealError is true when a sheep stuck in StatusError has no live
// in-memory task and can safely be returned to idle before dispatch.
// Circuit breaker (consecutive_failures) remains the durable pause gate.
func shouldAutoHealError(status sheep.Status, taskRunning bool) bool {
	return status == sheep.StatusError && !taskRunning
}

// NewProcessor creates a new task queue processor.
func NewProcessor(interval time.Duration) *Processor {
	return &Processor{
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the background processing loop.
func (p *Processor) Start() {
	p.mu.Lock()
	if p.running {
		p.mu.Unlock()
		return
	}
	p.running = true
	p.stopCh = make(chan struct{})
	p.mu.Unlock()

	go p.processLoop()
}

// Stop halts the background processing loop.
func (p *Processor) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return
	}
	p.running = false
	close(p.stopCh)
}

// IsRunning returns whether the processor is running.
func (p *Processor) IsRunning() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running
}

// processLoop runs the main processing loop.
func (p *Processor) processLoop() {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.checkAndExecutePendingTasks()
		case <-p.stopCh:
			return
		}
	}
}

// groupKey returns the canonical concurrency-group key for a task, computed
// from its provider and its effective model. The effective model is the task's
// own per-task override when set (so several local OpenCode systems each get
// their own group), otherwise the global per-provider config default. The
// runtime "auto" provider currently routes to Claude, so it folds into Claude's
// group. An empty effective model yields a provider-only key (e.g. "opencode").
func groupKey(provider, model string) string {
	p := provider
	if p == "auto" {
		p = "claude"
	}
	model = strings.TrimSpace(model)
	if model == "" {
		switch p {
		case "claude":
			model = strings.TrimSpace(config.GetString("model_claude"))
		case "opencode":
			model = strings.TrimSpace(config.GetString("model_opencode"))
		case "pi":
			model = strings.TrimSpace(config.GetString("model_pi"))
		case "grok":
			model = strings.TrimSpace(config.GetString("model_grok"))
		}
	}
	// embedded: model field holds the endpoint ID (see server.go initEmbeddedExecutor).
	// Each endpoint gets its own concurrency group for per-endpoint concurrency control.
	// Note: this groupKey operates at the queue dispatcher level only.
	// In-task goroutines (MAGI, spawn_subagents) bypass this limit and are
	// controlled by LLM call-level gating (step-03).
	if p == "embedded" {
		if model != "" {
			return "embedded/" + model
		}
		return "embedded"
	}
	if model == "" {
		return p
	}
	return p + "/" + model
}

// groupConcurrencyLimit resolves the configured concurrency limit for a group.
// It prefers an exact provider/model key, then falls back to the provider-only
// key (which is what the settings UI configures today). 0 means no group limit.
func groupConcurrencyLimit(limits map[string]int, groupKey, provider string) int {
	if len(limits) == 0 {
		return 0
	}
	if v, ok := limits[groupKey]; ok {
		return v
	}
	p := provider
	if p == "auto" {
		p = "claude"
	}
	if v, ok := limits[p]; ok {
		return v
	}
	return 0
}

// checkAndExecutePendingTasks checks for idle sheep with pending tasks and executes them.
//
// Two concurrency gates apply, and a task must pass BOTH to dispatch:
//  1. Global ceiling   — max_concurrent_tasks across all running tasks.
//  2. Per-group limit  — concurrency_limits[<provider+model group>], so e.g.
//     local opencode can run sequentially (GPU protection) while cloud claude
//     stays unlimited, without one starving the other.
//
// Concurrent ProcessPendingNow + ticker calls are serialized via p.mu so two
// loops cannot both read the same sheep as idle before either claims it.
func (p *Processor) checkAndExecutePendingTasks() {
	// Serialize dispatch loops. ProcessPendingNow runs this in a goroutine and
	// the ticker also calls it — without a lock both can pass the idle check
	// for one sheep and start two tasks ~ms apart.
	p.dispatchMu.Lock()
	defer p.dispatchMu.Unlock()

	maxConcurrent := config.GetInt("max_concurrent_tasks")
	groupLimits := config.GetConcurrencyLimits()

	// Snapshot running tasks already grouped by their (provider+model) key, so
	// per-model OpenCode systems are counted independently. One query per tick
	// feeds both gates.
	groupRunning, err := CountRunningByGroup()
	if err != nil {
		return
	}
	totalRunning := 0
	for _, n := range groupRunning {
		totalRunning += n
	}

	// Global ceiling reached: nothing can dispatch this tick.
	if maxConcurrent > 0 && totalRunning >= maxConcurrent {
		return
	}

	// Query all sheep
	sheepList, err := worker.List()
	if err != nil {
		return
	}

	dispatched := 0
	dispatchedByGroup := make(map[string]int)
	for _, s := range sheepList {
		// Auto-heal sticky error: if the sheep is in error with no live
		// in-memory task, return it to idle so pending work can resume.
		// Legacy non-interactive Execute used to leave StatusError forever
		// and starve the project queue (#7630 / #7633). The circuit breaker
		// (consecutive_failures) remains the only durable pause gate.
		if shouldAutoHealError(s.Status, worker.IsTaskRunning(s.Name)) {
			if err := worker.UpdateStatus(s.Name, sheep.StatusIdle); err == nil {
				s.Status = sheep.StatusIdle
				if p.OnStatusChange != nil {
					p.OnStatusChange(s.Name, "idle")
				}
				log.Printf("[processor] auto-healed sheep %s from error → idle", s.Name)
			} else {
				log.Printf("[processor] auto-heal sheep %s failed: %v", s.Name, err)
			}
		}

		// Only process sheep in idle status
		if s.Status != sheep.StatusIdle {
			continue
		}

		// Defence against double-dispatch: even if the DB shows this sheep
		// idle (e.g. an external process reset its status), skip it when we
		// already hold a live in-memory task for it. Without this, a second
		// process would spawn on the same sheep, clobbering the first's
		// process handle and mixing output streams.
		if worker.IsTaskRunning(s.Name) {
			continue
		}

		// Skip sheep without assigned project
		if s.Edges.Project == nil {
			continue
		}

		// Circuit breaker: skip sheep with too many consecutive failures
		if s.ConsecutiveFailures >= CircuitBreakerThreshold {
			continue
		}

		// Check if the sheep has pending tasks
		task, err := GetPendingTaskBySheep(s.ID)
		if err != nil || task == nil {
			continue
		}

		// Gate 0: provider must be enabled in settings. A disabled provider
		// blocks execution; fail the task with a clear reason so it surfaces
		// instead of sitting pending forever (the project UI also hides
		// disabled providers, so this mainly catches MCP/queued/retry paths).
		// A rate-limit handoff follow-up (#675) runs on its provider_override
		// instead of the sheep's column, so the gate checks the effective
		// provider.
		effectiveProvider := string(s.Provider)
		if task.ProviderOverride != "" {
			effectiveProvider = task.ProviderOverride
		}
		if !config.IsProviderEnabled(effectiveProvider) {
			reason := fmt.Sprintf("provider '%s' is disabled in settings", effectiveProvider)
			_ = FailTask(task.ID, reason)
			if p.OnTaskFail != nil {
				p.OnTaskFail(task.ID, s.Name, s.Edges.Project.Name, reason)
			}
			continue
		}

		// Gate 1: global ceiling (accounts for in-loop dispatches).
		if maxConcurrent > 0 && totalRunning+dispatched >= maxConcurrent {
			break
		}

		// Gate 2: per-group limit, keyed by this task's effective (provider+model)
		// group. Saturating one group only skips that group; other groups' sheep
		// may still dispatch this tick. The provider part follows the task's
		// provider_override when present (#675), so a handoff follow-up counts
		// against its target provider's group, not the sheep's own.
		gKey := groupKey(effectiveProvider, task.Model)
		if limit := groupConcurrencyLimit(groupLimits, gKey, effectiveProvider); limit > 0 {
			if groupRunning[gKey]+dispatchedByGroup[gKey] >= limit {
				continue
			}
		}

		// Use project name from task (MCP-specified project takes priority)
		projectName := s.Edges.Project.Name
		if task.Edges.Project != nil {
			projectName = task.Edges.Project.Name
		}

		// Claim in-memory + DB before launching the goroutine so a concurrent
		// dispatch loop cannot also select this sheep.
		if !worker.ClaimDispatch(s.Name, task.ID) {
			continue
		}
		if err := worker.UpdateStatus(s.Name, sheep.StatusWorking); err != nil {
			worker.ReleaseDispatch(s.Name, task.ID)
			log.Printf("[processor] failed to claim sheep %s: %v", s.Name, err)
			continue
		}

		// Execute task (in goroutine). The checkpoint uses the sheep's assigned
		// project path because that is the directory ExecuteInteractive runs in.
		go p.executeTask(s.Name, projectName, s.Edges.Project.Path, task.ID, task.Prompt)
		dispatched++
		dispatchedByGroup[gKey]++
	}

	// Shepherd (목자) dispatch (#594). worker.List() excludes the manager sheep
	// (internal/worker/worker.go), and the loop above also skips project-less
	// sheep, so the manager needs its own pass. It is NOT folded into the loop:
	// that would make the manager show up on the dashboard sheep cards.
	// The same gates apply as for ordinary sheep, and the in-loop dispatch
	// counts are carried over so both paths share one global ceiling.
	p.dispatchShepherd(maxConcurrent, groupLimits, groupRunning, totalRunning, dispatched, dispatchedByGroup)
}

// dispatchShepherd dispatches one pending manager (목자) task to the manager
// sheep (#594). The manager has no project row by design, so the configured
// workspace_path is its working directory. When the workspace is unset or not
// a directory the task stays pending — shepherd work is user-visible intent,
// so silently failing it would lose the request. The warning is logged only
// when the state changes so a misconfigured daemon does not spam the log every
// tick.
//
// Fields guarded by dispatchMu: shepherdWorkspaceWarned (checkAndExecutePending
// Tasks holds the lock for the whole tick, including this call).
func (p *Processor) dispatchShepherd(maxConcurrent int, groupLimits map[string]int, groupRunning map[string]int, totalRunning int, dispatched int, dispatchedByGroup map[string]int) {
	// worker.Get (unlike worker.List) includes the manager. No manager row
	// means no shepherd tasks can exist yet — skip without creating one.
	mgr, err := worker.Get(names.ManagerName)
	if err != nil {
		return
	}

	// Auto-heal sticky error, same as the ordinary path: a manager stuck in
	// error with no live in-memory task returns to idle so pending shepherd
	// work can resume. The circuit breaker below remains the durable pause gate.
	if shouldAutoHealError(mgr.Status, worker.IsTaskRunning(names.ManagerName)) {
		if err := worker.UpdateStatus(names.ManagerName, sheep.StatusIdle); err == nil {
			mgr.Status = sheep.StatusIdle
			if p.OnStatusChange != nil {
				p.OnStatusChange(names.ManagerName, "idle")
			}
			log.Printf("[processor] auto-healed manager sheep from error → idle")
		} else {
			log.Printf("[processor] auto-heal manager sheep failed: %v", err)
		}
	}

	if mgr.Status != sheep.StatusIdle {
		return
	}
	// Double-dispatch defence, same as the ordinary path.
	if worker.IsTaskRunning(names.ManagerName) {
		return
	}
	if mgr.ConsecutiveFailures >= CircuitBreakerThreshold {
		return
	}

	task, err := GetPendingShepherdTaskBySheep(mgr.ID)
	if err != nil || task == nil {
		return
	}

	// Gate 0: provider must be enabled. Fail the task with a clear reason
	// (same contract as the ordinary path) so it surfaces instead of sitting
	// pending forever.
	if !config.IsProviderEnabled(string(mgr.Provider)) {
		reason := fmt.Sprintf("provider '%s' is disabled in settings", string(mgr.Provider))
		_ = FailTask(task.ID, reason)
		if p.OnTaskFail != nil {
			p.OnTaskFail(task.ID, names.ManagerName, "", reason)
		}
		return
	}

	// Work directory. Two shapes reach here (#629):
	//   - kind-marked shepherd tasks (command/review/patrol) have no project
	//     row by design, so the configured workspace_path is their working
	//     directory. Unset or missing → leave the task pending for the next
	//     tick: shepherd work is user-visible intent, so failing it silently
	//     would lose the request.
	//   - project-linked manager tasks (issue_execute, POST /api/tasks with an
	//     explicit project, retry copies) run in that project's path — the
	//     same directory an ordinary sheep would use for the same work. They
	//     must not depend on workspace_path: the issue asked for a task to run
	//     against its project, and that is exactly what the link records.
	workDir := ""
	projectName := ""
	if task.Edges.Project != nil {
		workDir = task.Edges.Project.Path
		projectName = task.Edges.Project.Name
	} else {
		workspace, wsErr := p.shepherdWorkspace()
		if wsErr != "" {
			return
		}
		workDir = workspace
	}

	// Gate 1: global ceiling (includes tasks dispatched earlier this tick).
	if maxConcurrent > 0 && totalRunning+dispatched >= maxConcurrent {
		return
	}

	// Gate 2: per-group limit.
	gKey := groupKey(string(mgr.Provider), task.Model)
	if limit := groupConcurrencyLimit(groupLimits, gKey, string(mgr.Provider)); limit > 0 {
		if groupRunning[gKey]+dispatchedByGroup[gKey] >= limit {
			return
		}
	}

	if !worker.ClaimDispatch(names.ManagerName, task.ID) {
		return
	}
	if err := worker.UpdateStatus(names.ManagerName, sheep.StatusWorking); err != nil {
		worker.ReleaseDispatch(names.ManagerName, task.ID)
		log.Printf("[processor] failed to claim manager sheep: %v", err)
		return
	}

	// projectName is empty for kind-marked shepherd tasks: there is no project
	// row. The SSE hub, Discord notifier, and web push all tolerate an empty
	// project (Discord renders "-", push titles omit it). Project-linked
	// manager tasks (#629) carry their real project name instead.
	go p.executeTask(names.ManagerName, projectName, workDir, task.ID, task.Prompt)
	dispatchedByGroup[gKey]++
}

// shepherdWorkspace returns the validated workspace_path for shepherd tasks.
// The second return is a non-empty reason string when unusable. The warning
// log fires only when the unusable reason changes (or becomes usable again),
// not on every tick.
func (p *Processor) shepherdWorkspace() (string, string) {
	normalized, err := config.ValidateWorkspacePath(config.GetString("workspace_path"))
	if err == nil && normalized != "" {
		p.shepherdWorkspaceWarned = ""
		return normalized, ""
	}

	reason := "workspace_path is not set"
	if err != nil {
		reason = err.Error()
	}
	if p.shepherdWorkspaceWarned != reason {
		log.Printf("[processor] shepherd task deferred: %s (task stays pending until the workspace is configured)", reason)
		p.shepherdWorkspaceWarned = reason
	}
	return "", reason
}

// executeTask executes a single task with rate limit retry and circuit breaker.
// projectName is the project name stored in the Task (value specified by MCP).
// Caller must ClaimDispatch(sheepName, taskID) and set sheep working before go.
//
// A panic inside a task goroutine must never take down the whole daemon
// (tasks #7812/#7845/#7854: unrecovered panics kill every sheep). Recover,
// mark the task failed, restore the sheep to idle, and keep the process alive.
func (p *Processor) executeTask(sheepName, projectName, projectPath string, taskID int, prompt string) {
	// Drop ClaimDispatch placeholder if execute exits without registerRunningTask
	// (early StartTask failure, or ExecuteInteractive error before process start).
	// No-op once a real RunningTask (with Cancel/Cmd) replaced the placeholder
	// and was unregistered normally.
	defer worker.ReleaseDispatch(sheepName, taskID)

	// finishCheckpoint is installed after the start snapshot succeeds. The panic
	// recovery defer invokes it too, so a panicking task still records its end
	// snapshot before the task row is marked failed.
	var finishCheckpointOnce sync.Once
	finishCheckpointFn := func() {}

	// Isolate task panics from the daemon process. Go kills the entire process
	// on an unrecovered panic in any goroutine; without this, a nil-pointer or
	// tool-path bug during one LLM task stops every other sheep.
	defer func() {
		if r := recover(); r != nil {
			stack := string(debug.Stack())
			errMsg := fmt.Sprintf("task panic (recovered, daemon kept alive): %v", r)
			log.Printf("[processor] %s sheep=%s task=#%d\n%s", errMsg, sheepName, taskID, stack)

			finishCheckpointFn()
			_ = worker.UpdateStatus(sheepName, sheep.StatusIdle)
			if p.OnStatusChange != nil {
				p.OnStatusChange(sheepName, "idle")
			}
			if p.OnTaskFail != nil {
				p.OnTaskFail(taskID, sheepName, projectName, errMsg)
			}
			// Best-effort: FailTask if still running; ignore if already terminal.
			_ = FailTask(taskID, errMsg)
			// Surface a short stack in the live log so WebUI shows the failure.
			if p.OnOutput != nil {
				// Cap stack noise in the UI.
				if len(stack) > 2000 {
					stack = stack[:2000] + "\n…(truncated)"
				}
				p.OnOutput(sheepName, projectName, "⚠️ "+errMsg+"\n"+stack+"\n")
			}
		}
	}()

	// Change status: working (DB already claimed by dispatcher; notify SSE)
	if p.OnStatusChange != nil {
		p.OnStatusChange(sheepName, "working")
	}

	// Invoke callback
	if p.OnTaskStart != nil {
		p.OnTaskStart(taskID, sheepName, projectName, prompt)
	}

	// Start task
	if err := StartTask(taskID); err != nil {
		_ = worker.UpdateStatus(sheepName, sheep.StatusIdle)
		if p.OnStatusChange != nil {
			p.OnStatusChange(sheepName, "idle")
		}
		if p.OnTaskFail != nil {
			p.OnTaskFail(taskID, sheepName, projectName, err.Error())
		}
		_ = FailTask(taskID, err.Error())
		return
	}

	// TaskID already set by ClaimDispatch; keep SetRunningTaskID for paths that
	// register after ClaimDispatch without a prior TaskID.
	worker.SetRunningTaskID(sheepName, taskID)

	// Output collection — capped at maxOutputLinesBytes to prevent unbounded
	// memory growth. We keep head + tail: the first ~20% (head) and the last
	// ~80% (tail), with a truncation marker in between. This preserves both
	// the beginning of the task (useful for context) and the end (where the
	// result/summary lives) while staying within a bounded memory footprint.
	var outputLines []string
	var outputLinesBytes int

	// Set output callback.
	//
	// Line coalescing (task #7209): raw streaming chunks (especially from
	// Grok per-token deltas and MAGI proposer tokens) may not end with '\n'.
	// Previously, each chunk became its own array element, causing:
	//   - BPE fragment lines ("screens" + "hots" on separate lines)
	//   - MAGI slot cross-contamination when the frontend tried to merge
	//     open lines from different slots
	//
	// The LineCoalescer buffers incomplete chunks and emits only complete
	// lines (ending with '\n'). This makes the line array identical whether
	// viewed live (SSE) or after reload (DB), and prevents MAGI prefix
	// collision. The frontend appendLiveOutput is now a thin wrapper that
	// simply pushes lines.
	coalescer := worker.NewLineCoalescer(func(line string) {
		outputLines = append(outputLines, line)
		outputLinesBytes += len(line)

		// Enforce byte budget with head+tail preservation.
		if outputLinesBytes > maxOutputLinesBytes {
			outputLines, outputLinesBytes = capOutputLinesHeadTail(outputLines)
		}

		// Also save output to RunningTask (used on interruption)
		worker.AppendOutput(sheepName, line)
		if p.OnOutput != nil {
			p.OnOutput(sheepName, projectName, line)
		}
	})

	// Shepherd (목자) task handling (#594, extended by #629). shepherd_kind is
	// non-empty only on manager tasks created by manager.EnqueueShepherdTask.
	// A manager task WITHOUT a kind but WITH a project link (issue_execute,
	// POST /api/tasks, retry copies) is a normal coding job for the manager
	// sheep: it checkpoints and reports like an ordinary task, but still needs
	// opts.WorkDir because the manager sheep itself has no project edge —
	// selectWorkDir would otherwise fail with "no project assigned".
	shepherdKind := GetTaskShepherdKind(taskID)
	isShepherdTask := shepherdKind != ""
	isManagerProjectTask := !isShepherdTask && names.IsManager(sheepName)

	// Start checkpoint: snapshot the working tree before the agent runs. The
	// snapshot is best effort — a failure is recorded on the task but never
	// blocks execution.
	//
	// Shepherd tasks skip the checkpoint entirely (#594): the shepherd never
	// edits code, and when the workspace is a git repository the snapshot
	// would capture every project under it.
	var checkpointStarted bool
	var checkpointErr error
	if isShepherdTask {
		emitCheckpointLine(coalescer.Append, "📸 checkpoint skipped: shepherd task")
	} else {
		checkpointStarted, checkpointErr = beginCheckpoint(context.Background(), taskID, projectPath, coalescer.Append)
	}
	if checkpointErr != nil {
		log.Printf("[processor] task #%d checkpoint start: %v", taskID, checkpointErr)
	}
	finishCheckpointFn = func() {
		finishCheckpointOnce.Do(func() {
			if err := finishCheckpoint(context.Background(), taskID, projectPath, checkpointStarted, coalescer.Append); err != nil {
				log.Printf("[processor] task #%d checkpoint end: %v", taskID, err)
			}
		})
	}
	defer finishCheckpointFn()

	opts := worker.DefaultInteractiveOptions(
		func(text string) {
			coalescer.Append(text)
		},
		nil, // Input handler (not used in queue processing)
	)
	// Carry the task ID into the run so the embedded handoff path can resolve
	// this task's handoff_depth (registry TaskID isn't set yet at this point).
	opts.TaskID = taskID
	// Per-task reasoning toggle (OpenCode only). Resolved here so explicit
	// per-request overrides win over the global default.
	opts.Thinking = GetTaskThinking(taskID)
	defer ClearTaskThinking(taskID)
	// Per-task model override. Persisted on the task row, so it survives
	// restarts and feeds per-group concurrency accounting. OpenCode/pi/grok:
	// model id. Embedded: endpoint ID (or unique label/model). Handoff
	// follow-ups inherit this so they do not fall back to the provider default.
	opts.Model = GetTaskModel(taskID)
	// Per-task provider override (#675). Set when a rate-limit handoff created
	// this follow-up: ExecuteInteractive then runs the target provider instead
	// of the sheep's own. Persisted on the task row, so it survives restarts
	// and never mutates the sheep (concurrent tasks keep their own provider).
	taskProviderOverride := GetTaskProvider(taskID)
	opts.Provider = taskProviderOverride

	if isShepherdTask || isManagerProjectTask {
		// The dispatcher passes the resolved working directory as projectPath;
		// the manager sheep has no project edge of its own, so
		// ExecuteInteractive takes the directory from opts.WorkDir instead.
		// For kind-marked shepherd tasks that is the validated workspace_path;
		// for project-linked manager tasks (#629) it is the task's project path.
		opts.WorkDir = projectPath
	}
	if isShepherdTask {
		// review/patrol prompts already carry every context they need, so they
		// always start a fresh session — resuming would grow the context with
		// every review round. command follows the global session_reuse config.
		opts.FreshSession = shepherdKind != "command"
	}

	// Execute with rate limit retry
	var result *worker.ExecuteResult
	var execErr error
	retryCount := 0

	for {
		result, execErr = worker.ExecuteInteractive(sheepName, prompt, opts)

		if execErr == nil {
			break // Success
		}

		// Question or cancel: don't retry
		if worker.IsQuestionError(execErr) || worker.IsCancelledError(execErr) {
			break
		}

		// Rate limit: hand off to the configured provider, or retry with
		// exponential backoff (#675). The handoff attempt runs before the
		// backoff branch: when limit_handoff_provider is set to a provider
		// other than the one that just failed, and this task is not itself a
		// handoff continuation, we create a priority-1 follow-up on the target
		// provider immediately (no sleep) and finish this task as completed —
		// never as an error, so the circuit breaker stays untouched. When any
		// condition fails (feature off, same provider, already a continuation,
		// or follow-up creation itself fails), the existing backoff retry path
		// below runs unchanged.
		if IsRateLimitError(execErr.Error()) {
			if p.tryRateLimitHandoff(sheepName, projectName, taskID, prompt, outputLines, coalescer, taskProviderOverride) {
				return
			}

			if retryCount < MaxRateLimitRetries {
				retryCount++
				delay := calculateBackoff(retryCount)

				// Notify user of retry. Flush first so the retry notice starts
				// on its own line instead of merging with a partial line left by
				// the failed attempt, then route it through the coalescer so byte
				// accounting, AppendOutput, and OnOutput all see it (otherwise the
				// notice vanishes from MergeRunningOutput after a live refresh).
				retryMsg := fmt.Sprintf("⏳ Rate limit hit, retrying in %v (attempt %d/%d)\n", delay, retryCount, MaxRateLimitRetries)
				coalescer.Flush()
				coalescer.Append(retryMsg)

				log.Printf("[processor] %s: rate limit, retry %d/%d after %v", sheepName, retryCount, MaxRateLimitRetries, delay)

				// Wait before retry
				select {
				case <-time.After(delay):
					continue // Retry
				case <-p.stopCh:
					execErr = fmt.Errorf("task cancelled during rate limit wait")
					break
				}
			}
		}

		break // Non-retryable error
	}

	// Flush any remaining buffered content from the line coalescer so the
	// final partial line is not lost (task #7209).
	coalescer.Flush()

	// Finish the checkpoint before the completion/failure/stop branches so
	// every terminal path has an end snapshot. Completion callbacks can then
	// query the diff immediately.
	finishCheckpointFn()

	// Handle error
	if execErr != nil {
		// Question or cancel error: task stops, sheep goes idle (not error)
		if worker.IsQuestionError(execErr) || worker.IsCancelledError(execErr) {
			if p.OnStatusChange != nil {
				p.OnStatusChange(sheepName, "idle")
			}
			if p.OnTaskStop != nil {
				p.OnTaskStop(taskID, sheepName, projectName, execErr.Error())
			}
			_ = StopTaskWithOutput(taskID, execErr.Error(), outputLines)
			return
		}

		// Track consecutive failures for circuit breaker
		incrementConsecutiveFailures(sheepName)

		// ExecuteInteractive already restores the sheep to idle in the DB.
		// Broadcast idle (not error) so SSE/UI match DB and the queue can
		// pick up the next pending task. Pause is via consecutive_failures
		// only (#7633 — remove sticky StatusError gate).
		if p.OnStatusChange != nil {
			p.OnStatusChange(sheepName, "idle")
		}

		errMsg := execErr.Error()
		if retryCount > 0 {
			errMsg = fmt.Sprintf("%s (after %d retries)", errMsg, retryCount)
		}

		// Check circuit breaker and notify
		failures := getConsecutiveFailures(sheepName)
		if failures >= CircuitBreakerThreshold {
			circuitMsg := fmt.Sprintf("🔴 Circuit breaker tripped: %s has %d consecutive failures, pausing task execution", sheepName, failures)
			if p.OnOutput != nil {
				p.OnOutput(sheepName, projectName, circuitMsg+"\n")
			}
			log.Printf("[processor] %s", circuitMsg)
		}

		if p.OnTaskFail != nil {
			p.OnTaskFail(taskID, sheepName, projectName, errMsg)
		}
		// Save output even on failure
		_ = FailTaskWithOutput(taskID, errMsg, outputLines)
		return
	}

	// Success: reset consecutive failures
	resetConsecutiveFailures(sheepName)

	// Change status: idle
	if p.OnStatusChange != nil {
		p.OnStatusChange(sheepName, "idle")
	}

	// Complete task with cost and tokens. Guard nil result: a provider that
	// returns (nil, nil) must not panic the goroutine (and used to kill the
	// daemon when panic recovery was missing).
	if result == nil {
		errMsg := "provider returned nil result without error"
		if p.OnTaskFail != nil {
			p.OnTaskFail(taskID, sheepName, projectName, errMsg)
		}
		_ = FailTaskWithOutput(taskID, errMsg, outputLines)
		return
	}

	costUSD := result.CostUSD
	promptTokens := result.PromptTokens
	completionTokens := result.CompletionTokens

	if err := CompleteTaskWithTokens(taskID, result.Result, result.FilesModified, outputLines, costUSD, promptTokens, completionTokens); err != nil {
		if p.OnTaskFail != nil {
			p.OnTaskFail(taskID, sheepName, projectName, err.Error())
		}
		return
	}

	// Auto-ingest: trigger wiki update after successful task completion.
	// Shepherd tasks skip it (#594): the shepherd changes no code, so there is
	// nothing to document, and the ingest callback runs on the manager sheep
	// itself (it would nest a manager run inside a manager run). Project-linked
	// manager tasks (#629) DO change code, but the same nesting hazard applies
	// while the manager run is still live — skip them too.
	if config.GetBool("wiki_auto_ingest") && !isShepherdTask && !isManagerProjectTask {
		wiki.TriggerIngest(taskID, projectName, prompt, result.Result, result.FilesModified)
	}

	// Log cost if tracked
	if costUSD > 0 {
		log.Printf("[processor] task #%d cost: $%.4f", taskID, costUSD)
	}

	// Complete callback (with provider emoji)
	if p.OnTaskComplete != nil {
		provider, _ := worker.GetProvider(sheepName)
		emoji := worker.ProviderEmoji(provider)
		p.OnTaskComplete(taskID, sheepName, projectName, result.Result+" "+emoji)
	}
}

// beginCheckpoint deletes any stale refs for a reused task ID, takes the start
// snapshot, and records it on the task row. It returns whether a start snapshot
// exists so finishCheckpoint knows whether to take an end snapshot.
func beginCheckpoint(ctx context.Context, taskID int, dir string, emit func(string)) (bool, error) {
	if !config.GetBool("task_checkpoint_enabled") || dir == "" {
		return false, nil
	}

	cpCtx, cancel := context.WithTimeout(ctx, checkpointTimeout)
	defer cancel()

	// Fresh databases can reuse task IDs, so remove old start/end refs before
	// creating new ones. Skip the cleanup when dir is not a repository; Take
	// below reports that case as a normal skip.
	if _, _, probeErr := checkpoint.Probe(cpCtx, dir); probeErr == nil {
		if err := checkpoint.DeleteTaskRefs(cpCtx, dir, taskID); err != nil {
			log.Printf("[processor] task #%d checkpoint: delete stale refs: %v", taskID, err)
		}
	}

	snap, err := checkpoint.Take(cpCtx, dir,
		checkpoint.TaskRef(taskID, "start"),
		fmt.Sprintf("shepherd checkpoint task #%d start", taskID))
	if err != nil {
		reason := checkpointErrorReason(cpCtx, err)
		emitCheckpointLine(emit, "📸 checkpoint skipped: "+reason)
		return false, SetTaskCheckpointStart(taskID, dir, "", reason)
	}

	if err := SetTaskCheckpointStart(taskID, dir, snap.Commit, ""); err != nil {
		return false, err
	}
	emitCheckpointLine(emit, "📸 checkpoint start "+shortSHA(snap.Commit))
	return true, nil
}

// finishCheckpoint takes the end snapshot and saves it. It is skipped when the
// feature is disabled, no project path is known, or the start snapshot was not
// taken.
func finishCheckpoint(ctx context.Context, taskID int, dir string, started bool, emit func(string)) error {
	if !config.GetBool("task_checkpoint_enabled") || !started || dir == "" {
		return nil
	}

	cpCtx, cancel := context.WithTimeout(ctx, checkpointTimeout)
	defer cancel()

	snap, err := checkpoint.Take(cpCtx, dir,
		checkpoint.TaskRef(taskID, "end"),
		fmt.Sprintf("shepherd checkpoint task #%d end", taskID))
	if err != nil {
		reason := checkpointErrorReason(cpCtx, err)
		emitCheckpointLine(emit, "📸 checkpoint skipped: "+reason)
		return SetTaskCheckpointEnd(taskID, "", reason)
	}

	if err := SetTaskCheckpointEnd(taskID, snap.Commit, ""); err != nil {
		return err
	}
	emitCheckpointLine(emit, "📸 checkpoint end "+shortSHA(snap.Commit))
	return nil
}

// checkpointErrorReason maps checkpoint failures to the short reason stored in
// checkpoint_error and shown in the task log.
func checkpointErrorReason(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, checkpoint.ErrNotGitRepo):
		return "not a git repository"
	case errors.Is(err, checkpoint.ErrTooLarge):
		return err.Error()
	case ctx.Err() == context.DeadlineExceeded, errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	default:
		return err.Error()
	}
}

func emitCheckpointLine(emit func(string), line string) {
	if emit != nil {
		emit(line + "\n")
	}
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// calculateBackoff returns the delay for exponential backoff.
func calculateBackoff(attempt int) time.Duration {
	delay := InitialRetryDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
	}
	if delay > MaxRetryDelay {
		delay = MaxRetryDelay
	}
	return delay
}

// incrementConsecutiveFailures increments the consecutive failure count for a sheep.
func incrementConsecutiveFailures(sheepName string) {
	ctx := context.Background()
	client := db.Client()
	_, _ = client.Sheep.Update().
		Where(sheep.Name(sheepName)).
		AddConsecutiveFailures(1).
		Save(ctx)
}

// resetConsecutiveFailures resets the consecutive failure count for a sheep.
func resetConsecutiveFailures(sheepName string) {
	ctx := context.Background()
	client := db.Client()
	_, _ = client.Sheep.Update().
		Where(sheep.Name(sheepName)).
		SetConsecutiveFailures(0).
		Save(ctx)
}

// getConsecutiveFailures returns the current consecutive failure count for a sheep.
func getConsecutiveFailures(sheepName string) int {
	ctx := context.Background()
	client := db.Client()
	s, err := client.Sheep.Query().
		Where(sheep.Name(sheepName)).
		Only(ctx)
	if err != nil {
		return 0
	}
	return s.ConsecutiveFailures
}

// ResetCircuitBreaker resets the circuit breaker for a sheep (called on manual retry).
func ResetCircuitBreaker(sheepName string) {
	resetConsecutiveFailures(sheepName)
}

// ProcessPendingNow immediately processes pending tasks without waiting for the next tick.
func (p *Processor) ProcessPendingNow() {
	go p.checkAndExecutePendingTasks()
}

// GetQueueStatus returns the current queue status.
func GetQueueStatus() (string, error) {
	counts, err := CountByStatus()
	if err != nil {
		return "", err
	}

	// Include total cost
	totalCost, _ := GetTotalCost()

	status := fmt.Sprintf(i18n.T().QueueStatusFmt,
		counts["pending"], counts["running"], counts["completed"], counts["failed"], counts["stopped"])

	if totalCost > 0 {
		status += fmt.Sprintf(" | Cost: $%.2f", totalCost)
	}

	// Check circuit breakers
	var tripped []string
	sheepList, _ := worker.List()
	for _, s := range sheepList {
		if s.ConsecutiveFailures >= CircuitBreakerThreshold {
			tripped = append(tripped, s.Name)
		}
	}
	if len(tripped) > 0 {
		status += fmt.Sprintf(" | ⚠ Circuit breaker: %s", strings.Join(tripped, ", "))
	}

	return status, nil
}
