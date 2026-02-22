package tui

import "time"

// SheepStatus sheep status
type SheepStatus int

const (
	StatusIdle         SheepStatus = iota // 💤 Idle
	StatusWorking                         // 🔄 Working
	StatusWaitingInput                    // ❓ Waiting for input
	StatusDone                            // ✅ Done
	StatusError                           // ❌ Error
)

func (s SheepStatus) String() string {
	switch s {
	case StatusIdle:
		return "Idle"
	case StatusWorking:
		return "Working"
	case StatusWaitingInput:
		return "Waiting for input"
	case StatusDone:
		return "Done"
	case StatusError:
		return "Error"
	default:
		return "Unknown"
	}
}

// SheepOutputMsg sheep output message
type SheepOutputMsg struct {
	SheepName string
	Text      string
}

// SheepStatusMsg sheep status change message
type SheepStatusMsg struct {
	SheepName string
	Status    SheepStatus
	Error     error
}

// SheepQuestionMsg sheep question message
type SheepQuestionMsg struct {
	SheepName string
	Question  string
	AnswerCh  chan string
}

// TaskCompleteMsg task completion message
type TaskCompleteMsg struct {
	SheepName string
	TaskID    int
	Result    string
}

// TaskStartMsg task start message
type TaskStartMsg struct {
	SheepName   string
	TaskID      int
	Prompt      string
	ProjectName string
}

// SheepListUpdatedMsg sheep list updated message
type SheepListUpdatedMsg struct {
	Sheep []SheepInfo
}

// SheepInfo sheep information
type SheepInfo struct {
	Name        string
	ProjectName string
	ProjectPath string
	SessionID   string
	Provider    string // AI provider (claude, vibe, auto)
}

// Task represents a task
type Task struct {
	ID          int
	Prompt      string
	SheepName   string
	ProjectName string
	Status      string
	Result      string
	CreatedAt   time.Time
}

// TickMsg periodic tick message
type TickMsg time.Time

// ErrorMsg error message
type ErrorMsg struct {
	Error error
}
