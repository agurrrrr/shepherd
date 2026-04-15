package queue

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/worker"
)

const (
	// Rate limit retry settings
	MaxRateLimitRetries = 3                // Maximum retries on rate limit
	InitialRetryDelay   = 30 * time.Second // Initial backoff delay
	MaxRetryDelay       = 5 * time.Minute  // Maximum backoff delay

	// Circuit breaker settings
	CircuitBreakerThreshold = 5 // Consecutive failures to trip breaker
)

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

// checkAndExecutePendingTasks checks for idle sheep with pending tasks and executes them.
func (p *Processor) checkAndExecutePendingTasks() {
	// Query all sheep
	sheepList, err := worker.List()
	if err != nil {
		return
	}

	for _, s := range sheepList {
		// Only process sheep in idle status
		if s.Status != sheep.StatusIdle {
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

		// Use project name from task (MCP-specified project takes priority)
		projectName := s.Edges.Project.Name
		if task.Edges.Project != nil {
			projectName = task.Edges.Project.Name
		}

		// Execute task (in goroutine)
		go p.executeTask(s.Name, projectName, task.ID, task.Prompt)
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

	// Output collection slice
	var outputLines []string

	// Set output callback
	opts := worker.DefaultInteractiveOptions(
		func(text string) {
			// Collect output
			outputLines = append(outputLines, text)
			// Also save output to RunningTask (used on interruption)
			worker.AppendOutput(sheepName, text)
			if p.OnOutput != nil {
				p.OnOutput(sheepName, projectName, text)
			}
		},
		nil, // Input handler (not used in queue processing)
	)

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

	// Complete task with cost
	costUSD := float64(0)
	if result != nil {
		costUSD = result.CostUSD
	}

	if err := CompleteTaskWithCost(taskID, result.Result, result.FilesModified, outputLines, costUSD); err != nil {
		if p.OnTaskFail != nil {
			p.OnTaskFail(taskID, sheepName, projectName, err.Error())
		}
		return
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
