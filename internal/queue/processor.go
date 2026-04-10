package queue

import (
	"fmt"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/worker"
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

// executeTask executes a single task.
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

	// Execute Claude Code (with streaming output support)
	result, err := worker.ExecuteInteractive(sheepName, prompt, opts)
	if err != nil {
		// Question or cancel error: task stops, sheep goes idle (not error)
		if worker.IsQuestionError(err) || worker.IsCancelledError(err) {
			if p.OnStatusChange != nil {
				p.OnStatusChange(sheepName, "idle")
			}
			if p.OnTaskStop != nil {
				p.OnTaskStop(taskID, sheepName, projectName, err.Error())
			}
			_ = StopTaskWithOutput(taskID, err.Error(), outputLines)
			return
		}

		if p.OnStatusChange != nil {
			p.OnStatusChange(sheepName, "error")
		}
		if p.OnTaskFail != nil {
			p.OnTaskFail(taskID, sheepName, projectName, err.Error())
		}
		// Save output even on failure
		_ = FailTaskWithOutput(taskID, err.Error(), outputLines)
		return
	}

	// Change status: idle
	if p.OnStatusChange != nil {
		p.OnStatusChange(sheepName, "idle")
	}

	// 작업 완료 (출력 포함)
	if err := CompleteTaskWithOutput(taskID, result.Result, result.FilesModified, outputLines); err != nil {
		if p.OnTaskFail != nil {
			p.OnTaskFail(taskID, sheepName, projectName, err.Error())
		}
		return
	}

	// 완료 콜백 (프로바이더 이모지 포함)
	if p.OnTaskComplete != nil {
		provider, _ := worker.GetProvider(sheepName)
		emoji := worker.ProviderEmoji(provider)
		p.OnTaskComplete(taskID, sheepName, projectName, result.Result+" "+emoji)
	}
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

	return fmt.Sprintf(i18n.T().QueueStatusFmt,
		counts["pending"], counts["running"], counts["completed"], counts["failed"], counts["stopped"]), nil
}
