package queue

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/i18n"
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
)

// capOutputLinesHeadTail trims an output lines slice to fit within
// maxOutputLinesBytes, keeping the first ~20% (head) and the last ~80%
// (tail) with a truncation marker inserted between them. Returns the
// trimmed slice and its total byte size.
//
// The head/tail split ratio favors the tail because the end of the output
// typically contains the task result and summary, which are more valuable
// for debugging than the beginning.
func capOutputLinesHeadTail(lines []string) ([]string, int) {
	// Calculate total bytes.
	totalBytes := 0
	for _, l := range lines {
		totalBytes += len(l)
	}

	if totalBytes <= maxOutputLinesBytes {
		return lines, totalBytes
	}

	// Target: keep maxOutputLinesBytes worth of data.
	// Split: head = 20%, tail = 80%.
	headBudget := maxOutputLinesBytes / 5  // 4 MB
	tailBudget := maxOutputLinesBytes - headBudget // 16 MB

	// Collect head lines (from the start).
	headEnd := 0
	headBytes := 0
	for i, l := range lines {
		if headBytes+len(l) > headBudget {
			break
		}
		headBytes += len(l)
		headEnd = i + 1
	}

	// Collect tail lines (from the end).
	tailStart := len(lines)
	tailBytes := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if tailBytes+len(lines[i]) > tailBudget {
			break
		}
		tailBytes += len(lines[i])
		tailStart = i
	}

	// Ensure no overlap between head and tail.
	if headEnd >= tailStart {
		// Edge case: head and tail overlap. Just keep the tail.
		headEnd = 0
		headBytes = 0
	}

	marker := "...[output truncated — head + tail preserved]..."
	result := make([]string, 0, headEnd+(len(lines)-tailStart)+1)
	result = append(result, lines[:headEnd]...)
	result = append(result, marker)
	result = append(result, lines[tailStart:]...)

	totalKept := headBytes + tailBytes + len(marker)
	return result, totalKept
}

// Processor handles automatic execution of pending tasks.
type Processor struct {
	interval time.Duration
	stopCh   chan struct{}
	running  bool
	mu       sync.Mutex

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
func (p *Processor) checkAndExecutePendingTasks() {
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
		if !config.IsProviderEnabled(string(s.Provider)) {
			reason := fmt.Sprintf("provider '%s' is disabled in settings", string(s.Provider))
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
		// may still dispatch this tick.
		gKey := groupKey(string(s.Provider), task.Model)
		if limit := groupConcurrencyLimit(groupLimits, gKey, string(s.Provider)); limit > 0 {
			if groupRunning[gKey]+dispatchedByGroup[gKey] >= limit {
				continue
			}
		}

		// Use project name from task (MCP-specified project takes priority)
		projectName := s.Edges.Project.Name
		if task.Edges.Project != nil {
			projectName = task.Edges.Project.Name
		}

		// Execute task (in goroutine)
		go p.executeTask(s.Name, projectName, task.ID, task.Prompt)
		dispatched++
		dispatchedByGroup[gKey]++
	}
}

// executeTask executes a single task with rate limit retry and circuit breaker.
// projectName is the project name stored in the Task (value specified by MCP)
func (p *Processor) executeTask(sheepName, projectName string, taskID int, prompt string) {
	// Change status: working
	if p.OnStatusChange != nil {
		p.OnStatusChange(sheepName, "working")
	}

	// Invoke callback
	if p.OnTaskStart != nil {
		p.OnTaskStart(taskID, sheepName, projectName, prompt)
	}

	// Start task
	if err := StartTask(taskID); err != nil {
		if p.OnStatusChange != nil {
			p.OnStatusChange(sheepName, "error")
		}
		if p.OnTaskFail != nil {
			p.OnTaskFail(taskID, sheepName, projectName, err.Error())
		}
		_ = FailTask(taskID, err.Error())
		return
	}

	// Set TaskID on RunningTask (for saving output on interruption)
	worker.SetRunningTaskID(sheepName, taskID)

	// Output collection — capped at maxOutputLinesBytes to prevent unbounded
	// memory growth. We keep head + tail: the first ~20% (head) and the last
	// ~80% (tail), with a truncation marker in between. This preserves both
	// the beginning of the task (useful for context) and the end (where the
	// result/summary lives) while staying within a bounded memory footprint.
	var outputLines []string
	var outputLinesBytes int

	// Set output callback
	opts := worker.DefaultInteractiveOptions(
		func(text string) {
			// Collect output
			outputLines = append(outputLines, text)
			outputLinesBytes += len(text)

			// Enforce byte budget with head+tail preservation.
			if outputLinesBytes > maxOutputLinesBytes {
				outputLines, outputLinesBytes = capOutputLinesHeadTail(outputLines)
			}

			// Also save output to RunningTask (used on interruption)
			worker.AppendOutput(sheepName, text)
			if p.OnOutput != nil {
				p.OnOutput(sheepName, projectName, text)
			}
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
	// Per-task model override (OpenCode only). Persisted on the task row, so it
	// survives restarts and feeds the per-group concurrency accounting.
	opts.Model = GetTaskModel(taskID)

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

		// Rate limit: retry with exponential backoff
		if IsRateLimitError(execErr.Error()) && retryCount < MaxRateLimitRetries {
			retryCount++
			delay := calculateBackoff(retryCount)

			// Notify user of retry
			retryMsg := fmt.Sprintf("⏳ Rate limit hit, retrying in %v (attempt %d/%d)\n", delay, retryCount, MaxRateLimitRetries)
			outputLines = append(outputLines, retryMsg)
			if p.OnOutput != nil {
				p.OnOutput(sheepName, projectName, retryMsg)
			}

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

		break // Non-retryable error
	}

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

		if p.OnStatusChange != nil {
			p.OnStatusChange(sheepName, "error")
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

	// Complete task with cost and tokens
	costUSD := float64(0)
	var promptTokens, completionTokens int64
	if result != nil {
		costUSD = result.CostUSD
		promptTokens = result.PromptTokens
		completionTokens = result.CompletionTokens
	}

	if err := CompleteTaskWithTokens(taskID, result.Result, result.FilesModified, outputLines, costUSD, promptTokens, completionTokens); err != nil {
		if p.OnTaskFail != nil {
			p.OnTaskFail(taskID, sheepName, projectName, err.Error())
		}
		return
	}

	// Auto-ingest: trigger wiki update after successful task completion
	if config.GetBool("wiki_auto_ingest") {
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
