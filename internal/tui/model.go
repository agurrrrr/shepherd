package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/agurrrrr/shepherd/ent"
	entProject "github.com/agurrrrr/shepherd/ent/project"
	entSheep "github.com/agurrrrr/shepherd/ent/sheep"
	entTask "github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

// ViewMode view mode
type ViewMode int

const (
	ViewModeDashboard ViewMode = iota // 2 - Dashboard (default)
	ViewModeSplit                     // 1 - Split
	ViewModeTaskList                  // 3 - Task list
	ViewModeSettings                  // 4 - Settings (F10)
)

// ShepherdName is the internal identifier for the shepherd manager.
const ShepherdName = "shepherd"

// Layout constants
const (
	SidebarWidth    = 16 // Sidebar Place width (+ border 2 = 18 columns)
	SidebarInnerMax = 16 // Sidebar inner content max width (including padding)
	SidebarTextMax  = 11 // Project name max width (padding 2 + icon 2 + space 1 = 5 columns excluded)
)

// SharedState shared state between TUI and Model
type SharedState struct {
	mu              sync.RWMutex
	sheep           map[string]*SheepState
	sheepList       []string
	selectedIdx     int
	focusedPane     int
	viewMode        ViewMode
	selectedTaskIdx     int // Selected task index in task list
	viewingTaskID       int // Task ID being viewed in detail (-1 for list view)
	sidebarScrollOffset int // Sidebar project list scroll offset
	taskListScrollOffset int // Task list scroll offset
}

// IsShepherdSelected checks if the shepherd (manager) is selected
func (s *SharedState) IsShepherdSelected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.selectedIdx == -1
}

// GetSelectedSheep returns the currently selected sheep info (thread safe)
// Returns nil when shepherd is selected
func (s *SharedState) GetSelectedSheep() *SheepState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.sheepList) == 0 {
		return nil
	}
	var idx int
	if s.viewMode == ViewModeDashboard || s.viewMode == ViewModeTaskList {
		idx = s.selectedIdx
	} else {
		idx = s.focusedPane
	}
	// Return nil when shepherd is selected (-1)
	if idx < 0 || idx >= len(s.sheepList) {
		return nil
	}
	name := s.sheepList[idx]
	return s.sheep[name]
}

// Model TUI main model
type Model struct {
	// Shared state (shared with TUI)
	shared *SharedState

	// Task queue
	taskQueue []Task // Pending task queue

	// Input
	input      textinput.Model // Text input
	inputFocus bool            // Input focus

	// View related
	width  int // Terminal width
	height int // Terminal height

	// Key bindings
	keys KeyMap

	// Help display
	showHelp bool

	// Pending question
	pendingQuestion *SheepQuestionMsg

	// Error message
	lastError string

	// Sidebar hidden state
	sidebarHidden bool

	// Shepherd output buffer
	shepherdOutput   []string
	shepherdViewport viewport.Model // Viewport for shepherd output

	// Task detail viewport
	taskDetailViewport viewport.Model // Viewport for task detail scrolling

	// Settings view
	settingsIdx      int             // Selected setting item (0: language, 1: provider, 2: workspace)
	settingsEditing  bool            // Currently editing workspace path
	settingsInput    textinput.Model // Text input for workspace path editing
	previousViewMode ViewMode        // View mode to restore on settings close

	// Callbacks
	onCommand func(cmd string) // Command execution callback
}

// SheepState individual sheep state
type SheepState struct {
	Name        string         // Sheep name
	Project     string         // Project name
	ProjectPath string         // Project path
	Status      SheepStatus    // Current status
	SessionID   string         // Claude Code session ID
	Provider    string         // AI provider (claude, vibe, auto)
	Output      []string       // Output buffer
	Viewport    viewport.Model // Scrollable viewport
	CurrentTask *Task          // Current task
	TaskHistory []TaskRecord   // Task history
	LastError   error          // Last error
}

// TaskRecord task record
type TaskRecord struct {
	ID        int
	DBTaskID  int       // DB Task ID
	Prompt    string
	Output    []string  // Full task output
	Status    string    // pending, working, completed, error
	StartedAt time.Time
	EndedAt   time.Time
}

// saveTaskToDB saves a task to the DB
func saveTaskToDB(sheepName, prompt string) (*ent.Task, error) {
	ctx := context.Background()
	client := db.Client()

	// Query sheep (including project)
	s, err := client.Sheep.Query().
		Where(entSheep.Name(sheepName)).
		WithProject().
		Only(ctx)
	if err != nil {
		return nil, err
	}

	// Create task
	now := time.Now()
	taskCreate := client.Task.Create().
		SetPrompt(prompt).
		SetStatus(entTask.StatusRunning).
		SetStartedAt(now).
		SetSheep(s)

	// Connect project if available
	if s.Edges.Project != nil {
		taskCreate = taskCreate.SetProject(s.Edges.Project)
	}

	task, err := taskCreate.Save(ctx)
	if err != nil {
		return nil, err
	}

	return task, nil
}

// completeTaskInDB marks a task as complete in the DB
func completeTaskInDB(taskID int, output []string, status string) error {
	ctx := context.Background()
	client := db.Client()

	taskStatus := entTask.StatusCompleted
	if status == "error" {
		taskStatus = entTask.StatusFailed
	}

	_, err := client.Task.UpdateOneID(taskID).
		SetStatus(taskStatus).
		SetCompletedAt(time.Now()).
		SetOutput(output).
		Save(ctx)
	return err
}

// refreshSelectedTaskHistoryLocked refreshes task history for the selected sheep from DB.
// Must be called with m.shared.mu held.
func (m *Model) refreshSelectedTaskHistoryLocked() {
	selectedIdx := m.shared.selectedIdx
	var sheepName string

	if selectedIdx == -1 {
		sheepName = ShepherdName
	} else if selectedIdx >= 0 && selectedIdx < len(m.shared.sheepList) {
		sheepName = m.shared.sheepList[selectedIdx]
	} else {
		return
	}

	s, ok := m.shared.sheep[sheepName]
	if !ok {
		return
	}

	if history, err := loadTaskHistoryFromDB(sheepName); err == nil {
		s.TaskHistory = history
	}
}

// loadTaskHistoryFromDB loads task history from DB (by sheep)
func loadTaskHistoryFromDB(sheepName string) ([]TaskRecord, error) {
	ctx := context.Background()
	client := db.Client()

	// Query sheep (including project info)
	s, err := client.Sheep.Query().
		Where(entSheep.Name(sheepName)).
		WithProject().
		Only(ctx)
	if err != nil {
		return nil, err
	}

	// If project exists, query by project
	if s.Edges.Project != nil {
		return loadTaskHistoryByProjectID(s.Edges.Project.ID)
	}

	// If no project, query by sheep
	return loadTaskHistoryBySheepID(s.ID)
}

// loadTaskHistoryByProjectID loads task history by project ID
func loadTaskHistoryByProjectID(projectID int) ([]TaskRecord, error) {
	ctx := context.Background()
	client := db.Client()

	tasks, err := client.Task.Query().
		Where(entTask.HasProjectWith(entProject.ID(projectID))).
		Order(ent.Desc(entTask.FieldCreatedAt)).
		Limit(50).
		All(ctx)
	if err != nil {
		return nil, err
	}

	return tasksToRecords(tasks), nil
}

// loadTaskHistoryBySheepID loads task history by sheep ID
func loadTaskHistoryBySheepID(sheepID int) ([]TaskRecord, error) {
	ctx := context.Background()
	client := db.Client()

	tasks, err := client.Task.Query().
		Where(entTask.HasSheepWith(entSheep.ID(sheepID))).
		Order(ent.Desc(entTask.FieldCreatedAt)).
		Limit(50).
		All(ctx)
	if err != nil {
		return nil, err
	}

	return tasksToRecords(tasks), nil
}

// tasksToRecords converts ent.Task slice to TaskRecord slice
func tasksToRecords(tasks []*ent.Task) []TaskRecord {
	var records []TaskRecord
	for i, t := range tasks {
		record := TaskRecord{
			ID:        len(tasks) - i, // Assign IDs in reverse order
			DBTaskID:  t.ID,
			Prompt:    t.Prompt,
			Output:    t.Output,
			Status:    string(t.Status),
			StartedAt: t.StartedAt,
		}
		if !t.CompletedAt.IsZero() {
			record.EndedAt = t.CompletedAt
		}
		records = append(records, record)
	}

	// Sort by oldest first (reverse)
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}

	return records
}

// NewModel creates a new model
func NewModel() Model {
	ti := textinput.New()
	ti.Placeholder = "Enter command..."
	ti.CharLimit = 500
	ti.Width = 50
	ti.Focus() // Enable input focus

	shared := &SharedState{
		sheep:         make(map[string]*SheepState),
		sheepList:     []string{},
		viewMode:      ViewModeDashboard,
		viewingTaskID: -1,
	}

	// Shepherd viewport
	shepherdVp := viewport.New(80, 20)

	// Task detail viewport
	taskDetailVp := viewport.New(80, 20)

	// Initialize shepherd state
	shepherdState := &SheepState{
		Name:     ShepherdName,
		Status:   StatusIdle,
		Output:   []string{},
		Viewport: viewport.New(80, 20),
	}
	// Load shepherd task history from DB
	if history, err := loadTaskHistoryFromDB(ShepherdName); err == nil {
		shepherdState.TaskHistory = history
	}
	shared.sheep[ShepherdName] = shepherdState

	return Model{
		shared:             shared,
		input:              ti,
		inputFocus:         true,
		keys:               DefaultKeyMap,
		shepherdViewport:   shepherdVp,
		taskDetailViewport: taskDetailVp,
	}
}

// GetSharedState returns shared state (used by TUI)
func (m *Model) GetSharedState() *SharedState {
	return m.shared
}

// SetOnCommand sets the command callback
func (m *Model) SetOnCommand(fn func(cmd string)) {
	m.onCommand = fn
}

// Init initialization
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		tickCmd(),
		m.input.Focus(), // Input focus
	)
}

// tickCmd periodic tick
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// Update handles updates
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = msg.Width - 4

		// Update shepherd viewport size
		m.shepherdViewport.Width = msg.Width - SidebarWidth - 12
		m.shepherdViewport.Height = msg.Height - 16 // Excluding header, input, guide text, etc.

		// Update viewport sizes (use Lock since this is a write operation)
		m.shared.mu.Lock()
		for _, s := range m.shared.sheep {
			s.Viewport.Width = m.getViewportWidthLocked()
			s.Viewport.Height = m.getViewportHeightLocked()
		}
		m.shared.mu.Unlock()

	case tea.KeyMsg:
		// If waiting for a question, only process input
		if m.pendingQuestion != nil {
			return m.handleQuestionInput(msg)
		}

		// Settings view key handling
		m.shared.mu.RLock()
		isSettings := m.shared.viewMode == ViewModeSettings
		m.shared.mu.RUnlock()

		if isSettings {
			return m.handleSettingsInput(msg)
		}

		// If input is focused, prioritize input handling
		if m.inputFocus {
			switch {
			case key.Matches(msg, m.keys.Escape):
				m.inputFocus = false
				m.input.Blur()
				return m, nil
			case key.Matches(msg, m.keys.Enter):
				// In task list view, if input is empty, view task detail
				m.shared.mu.RLock()
				isTaskList := m.shared.viewMode == ViewModeTaskList
				viewingTask := m.shared.viewingTaskID
				m.shared.mu.RUnlock()

				if m.input.Value() == "" && isTaskList {
					if viewingTask == -1 {
						m.selectCurrentTask()
					} else {
						m.shared.mu.Lock()
						m.shared.viewingTaskID = -1
						m.shared.mu.Unlock()
					}
					return m, nil
				}

				if m.input.Value() != "" {
					cmd := m.input.Value()
					m.input.SetValue("")
					if m.onCommand != nil {
						m.onCommand(cmd)
					}
				}
				return m, nil
			case key.Matches(msg, m.keys.Up):
				// Arrow keys to move between projects even while typing
				m.prevItem()
				return m, nil
			case key.Matches(msg, m.keys.Down):
				// Arrow keys to move between projects even while typing
				m.nextItem()
				return m, nil
			case key.Matches(msg, m.keys.SplitView):
				// View switching available even while typing (key 1)
				m.shared.mu.Lock()
				m.shared.viewMode = ViewModeSplit
				m.shared.viewingTaskID = -1
				m.recalculateViewportSizesLocked()
				m.shared.mu.Unlock()
				return m, nil
			case key.Matches(msg, m.keys.DashboardView):
				// View switching available even while typing (key 2)
				m.shared.mu.Lock()
				m.shared.viewMode = ViewModeDashboard
				m.shared.viewingTaskID = -1
				m.recalculateViewportSizesLocked()
				m.shared.mu.Unlock()
				return m, nil
			case key.Matches(msg, m.keys.TaskListView):
				// View switching available even while typing (key 3)
				m.shared.mu.Lock()
				m.shared.viewMode = ViewModeTaskList
				m.shared.viewingTaskID = -1
				m.shared.selectedTaskIdx = 0
				m.recalculateViewportSizesLocked()
				m.shared.mu.Unlock()
				return m, nil
			case key.Matches(msg, m.keys.ToggleSidebar):
				// Sidebar toggle available even while typing
				m.sidebarHidden = !m.sidebarHidden
				m.shared.mu.Lock()
				m.recalculateViewportSizesLocked()
				m.shared.mu.Unlock()
				return m, nil
			case key.Matches(msg, m.keys.ToggleProvider):
				// Provider change available even while typing
				m.shared.mu.RLock()
				selectedIdx := m.shared.selectedIdx
				sheepList := m.shared.sheepList
				sheep := m.shared.sheep
				m.shared.mu.RUnlock()

				if selectedIdx >= 0 && selectedIdx < len(sheepList) {
					sheepName := sheepList[selectedIdx]
					if s, ok := sheep[sheepName]; ok {
						nextProvider := "claude"
						switch s.Provider {
						case "claude":
							nextProvider = "opencode"
						case "opencode":
							nextProvider = "auto"
						case "auto":
							nextProvider = "claude"
						}
						if err := worker.UpdateProvider(sheepName, nextProvider); err == nil {
							m.shared.mu.Lock()
							if ss, ok := m.shared.sheep[sheepName]; ok {
								ss.Provider = nextProvider
							}
							m.shared.mu.Unlock()
						}
					}
				}
				return m, nil
			case key.Matches(msg, m.keys.Tab):
				// Tab to switch views even while typing
				m.shared.mu.Lock()
				switch m.shared.viewMode {
				case ViewModeSplit:
					m.shared.viewMode = ViewModeDashboard
				case ViewModeDashboard:
					m.shared.viewMode = ViewModeTaskList
				case ViewModeTaskList:
					m.shared.viewMode = ViewModeSplit
				}
				m.shared.viewingTaskID = -1
				m.recalculateViewportSizesLocked()
				m.shared.mu.Unlock()
				return m, nil
			case key.Matches(msg, m.keys.ShiftTab):
				// Shift+Tab to reverse switch views even while typing
				m.shared.mu.Lock()
				switch m.shared.viewMode {
				case ViewModeSplit:
					m.shared.viewMode = ViewModeTaskList
				case ViewModeDashboard:
					m.shared.viewMode = ViewModeSplit
				case ViewModeTaskList:
					m.shared.viewMode = ViewModeDashboard
				}
				m.shared.viewingTaskID = -1
				m.recalculateViewportSizesLocked()
				m.shared.mu.Unlock()
				return m, nil
			default:
				// Forward key to input field (left/right arrows, etc.)
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
		}

		// Global keys when unfocused
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Escape):
			// ESC: Stop the selected sheep's task
			return m, m.stopSelectedTask()
		case key.Matches(msg, m.keys.Up):
			m.prevItem()
			return m, nil
		case key.Matches(msg, m.keys.Down):
			m.nextItem()
			return m, nil
		case key.Matches(msg, m.keys.Tab):
			// View cycle: Split -> Dashboard -> Task list -> Split
			m.shared.mu.Lock()
			switch m.shared.viewMode {
			case ViewModeSplit:
				m.shared.viewMode = ViewModeDashboard
			case ViewModeDashboard:
				m.shared.viewMode = ViewModeTaskList
			case ViewModeTaskList:
				m.shared.viewMode = ViewModeSplit
			}
			m.shared.viewingTaskID = -1
			m.recalculateViewportSizesLocked()
			m.shared.mu.Unlock()
			return m, nil
		case key.Matches(msg, m.keys.ShiftTab):
			// View reverse cycle: Split -> Task list -> Dashboard -> Split
			m.shared.mu.Lock()
			switch m.shared.viewMode {
			case ViewModeSplit:
				m.shared.viewMode = ViewModeTaskList
			case ViewModeDashboard:
				m.shared.viewMode = ViewModeSplit
			case ViewModeTaskList:
				m.shared.viewMode = ViewModeDashboard
			}
			m.shared.viewingTaskID = -1
			m.recalculateViewportSizesLocked()
			m.shared.mu.Unlock()
			return m, nil
		case key.Matches(msg, m.keys.SplitView):
			m.shared.mu.Lock()
			m.shared.viewMode = ViewModeSplit
			m.shared.viewingTaskID = -1
			m.recalculateViewportSizesLocked()
			m.shared.mu.Unlock()
			return m, nil
		case key.Matches(msg, m.keys.DashboardView):
			m.shared.mu.Lock()
			m.shared.viewMode = ViewModeDashboard
			m.shared.viewingTaskID = -1
			m.recalculateViewportSizesLocked()
			m.shared.mu.Unlock()
			return m, nil
		case key.Matches(msg, m.keys.TaskListView):
			m.shared.mu.Lock()
			m.shared.viewMode = ViewModeTaskList
			m.shared.viewingTaskID = -1
			m.shared.selectedTaskIdx = 0
			m.recalculateViewportSizesLocked()
			m.shared.mu.Unlock()
			return m, nil
		case key.Matches(msg, m.keys.ToggleSidebar):
			m.sidebarHidden = !m.sidebarHidden
			m.shared.mu.Lock()
			m.recalculateViewportSizesLocked()
			m.shared.mu.Unlock()
			return m, nil
		case key.Matches(msg, m.keys.ToggleProvider):
			// Change the selected sheep's provider (claude -> local -> auto -> claude)
			m.shared.mu.RLock()
			selectedIdx := m.shared.selectedIdx
			sheepList := m.shared.sheepList
			sheep := m.shared.sheep
			m.shared.mu.RUnlock()

			if selectedIdx >= 0 && selectedIdx < len(sheepList) {
				sheepName := sheepList[selectedIdx]
				if s, ok := sheep[sheepName]; ok {
					// Cycle to next provider
					nextProvider := "claude"
					switch s.Provider {
					case "claude":
						nextProvider = "opencode"
					case "opencode":
						nextProvider = "auto"
					case "auto":
						nextProvider = "claude"
					}
					// Change provider
					if err := worker.UpdateProvider(sheepName, nextProvider); err == nil {
						m.shared.mu.Lock()
						if ss, ok := m.shared.sheep[sheepName]; ok {
							ss.Provider = nextProvider
						}
						m.shared.mu.Unlock()
					}
				}
			}
			return m, nil
		case key.Matches(msg, m.keys.Settings):
			// F10: Open settings
			m.shared.mu.Lock()
			m.previousViewMode = m.shared.viewMode
			m.shared.viewMode = ViewModeSettings
			m.shared.mu.Unlock()
			m.settingsIdx = 0
			m.settingsEditing = false
			return m, nil
		case key.Matches(msg, m.keys.Slash), key.Matches(msg, m.keys.Enter):
			// In task list view, Enter opens task detail
			m.shared.mu.RLock()
			isTaskList := m.shared.viewMode == ViewModeTaskList
			viewingTask := m.shared.viewingTaskID
			m.shared.mu.RUnlock()

			if isTaskList && viewingTask == -1 {
				m.selectCurrentTask()
			} else if isTaskList && viewingTask >= 0 {
				// In detail view, Enter returns to list
				m.shared.mu.Lock()
				m.shared.viewingTaskID = -1
				m.shared.mu.Unlock()
			} else {
				m.inputFocus = true
				m.input.Focus()
			}
		case key.Matches(msg, m.keys.Escape):
			// In task detail, ESC returns to list
			m.shared.mu.Lock()
			if m.shared.viewingTaskID >= 0 {
				m.shared.viewingTaskID = -1
			}
			m.shared.mu.Unlock()
		case key.Matches(msg, m.keys.PageUp):
			// PageUp in task detail view
			m.shared.mu.RLock()
			isTaskDetail := m.shared.viewMode == ViewModeTaskList && m.shared.viewingTaskID >= 0
			m.shared.mu.RUnlock()
			if isTaskDetail {
				m.taskDetailViewport.ViewUp()
			}
		case key.Matches(msg, m.keys.PageDown):
			// PageDown in task detail view
			m.shared.mu.RLock()
			isTaskDetail := m.shared.viewMode == ViewModeTaskList && m.shared.viewingTaskID >= 0
			m.shared.mu.RUnlock()
			if isTaskDetail {
				m.taskDetailViewport.ViewDown()
			}
		case key.Matches(msg, m.keys.Home):
			// Home in task detail view
			m.shared.mu.RLock()
			isTaskDetail := m.shared.viewMode == ViewModeTaskList && m.shared.viewingTaskID >= 0
			m.shared.mu.RUnlock()
			if isTaskDetail {
				m.taskDetailViewport.GotoTop()
			}
		case key.Matches(msg, m.keys.End):
			// End in task detail view
			m.shared.mu.RLock()
			isTaskDetail := m.shared.viewMode == ViewModeTaskList && m.shared.viewingTaskID >= 0
			m.shared.mu.RUnlock()
			if isTaskDetail {
				m.taskDetailViewport.GotoBottom()
			}
		case key.Matches(msg, m.keys.Help):
			m.showHelp = !m.showHelp
		}

	case SheepOutputMsg:
		m.shared.mu.Lock()
		// Special handling for shepherd output
		if msg.SheepName == ShepherdName {
			if m.shepherdOutput == nil {
				m.shepherdOutput = []string{}
			}
			m.shepherdOutput = append(m.shepherdOutput, msg.Text)
			// Update viewport content (with line wrapping)
			wrapWidth := m.shepherdViewport.Width - 2
			if wrapWidth < 20 {
				wrapWidth = 20
			}
			var wrappedLines []string
			for _, line := range m.shepherdOutput {
				wrappedLines = append(wrappedLines, wrapText(line, wrapWidth)...)
			}
			m.shepherdViewport.SetContent(strings.Join(wrappedLines, "\n"))
			m.shepherdViewport.GotoBottom()
		} else if s, ok := m.shared.sheep[msg.SheepName]; ok {
			s.Output = append(s.Output, msg.Text)
			// Update viewport content (with line wrapping)
			wrapWidth := s.Viewport.Width - 2
			if wrapWidth < 20 {
				wrapWidth = 20
			}
			var wrappedLines []string
			for _, line := range s.Output {
				wrappedLines = append(wrappedLines, wrapText(line, wrapWidth)...)
			}
			s.Viewport.SetContent(strings.Join(wrappedLines, "\n"))
			s.Viewport.GotoBottom()
			// Save output to in-progress task history as well
			if len(s.TaskHistory) > 0 {
				lastIdx := len(s.TaskHistory) - 1
				if s.TaskHistory[lastIdx].Status == "working" {
					s.TaskHistory[lastIdx].Output = append(s.TaskHistory[lastIdx].Output, msg.Text)
				}
			}
		}
		m.shared.mu.Unlock()

	case SheepStatusMsg:
		m.shared.mu.Lock()
		if s, ok := m.shared.sheep[msg.SheepName]; ok {
			s.Status = msg.Status
			if msg.Error != nil {
				s.LastError = msg.Error
			}
		}
		m.shared.mu.Unlock()

	case SheepQuestionMsg:
		m.pendingQuestion = &msg
		m.inputFocus = true
		m.input.Focus()
		m.input.Placeholder = msg.Question

	case SheepListUpdatedMsg:
		m.updateSheepList(msg.Sheep)

	case TaskStartMsg:
		m.shared.mu.Lock()
		if s, ok := m.shared.sheep[msg.SheepName]; ok {
			s.Status = StatusWorking
			taskID := len(s.TaskHistory) + 1

			// Save task to DB
			var dbTaskID int
			if dbTask, err := saveTaskToDB(msg.SheepName, msg.Prompt); err == nil {
				dbTaskID = dbTask.ID
			}

			s.CurrentTask = &Task{
				ID:          taskID,
				Prompt:      msg.Prompt,
				ProjectName: msg.ProjectName,
			}
			s.Output = []string{"> " + msg.Prompt}
			s.Viewport.SetContent(strings.Join(s.Output, "\n"))
			// Add to task history
			s.TaskHistory = append(s.TaskHistory, TaskRecord{
				ID:        taskID,
				DBTaskID:  dbTaskID,
				Prompt:    msg.Prompt,
				Output:    []string{},
				Status:    "working",
				StartedAt: time.Now(),
			})
		}
		m.shared.mu.Unlock()

	case TaskCompleteMsg:
		m.shared.mu.Lock()
		if s, ok := m.shared.sheep[msg.SheepName]; ok {
			// Check for error
			isError := strings.HasPrefix(msg.Result, "error:")
			if isError {
				s.Status = StatusError
			} else {
				s.Status = StatusDone
			}

			if s.CurrentTask != nil {
				s.CurrentTask.Result = msg.Result
				if isError {
					s.CurrentTask.Status = "error"
				} else {
					s.CurrentTask.Status = "completed"
				}
			}

			// Update history
			if len(s.TaskHistory) > 0 {
				lastIdx := len(s.TaskHistory) - 1
				if isError {
					s.TaskHistory[lastIdx].Status = "error"
				} else {
					s.TaskHistory[lastIdx].Status = "completed"
				}
				s.TaskHistory[lastIdx].EndedAt = time.Now()
				// Copy output from s.Output
				s.TaskHistory[lastIdx].Output = make([]string, len(s.Output))
				copy(s.TaskHistory[lastIdx].Output, s.Output)

				// DB update
				if s.TaskHistory[lastIdx].DBTaskID > 0 {
					status := "completed"
					if isError {
						status = "error"
					}
					_ = completeTaskInDB(s.TaskHistory[lastIdx].DBTaskID, s.TaskHistory[lastIdx].Output, status)
				}
			}

			// Reload latest task history from DB (reflects tasks run through Processor)
			if history, err := loadTaskHistoryFromDB(msg.SheepName); err == nil {
				s.TaskHistory = history
			}
		}
		m.shared.mu.Unlock()

	case TickMsg:
		cmds = append(cmds, tickCmd())

		// Refresh selected sheep's task history from DB when in TaskList view
		m.shared.mu.Lock()
		if m.shared.viewMode == ViewModeTaskList {
			m.refreshSelectedTaskHistoryLocked()
		}
		m.shared.mu.Unlock()

	case ErrorMsg:
		m.lastError = msg.Error.Error()
	}

	return m, tea.Batch(cmds...)
}

// handleQuestionInput handles question input
func (m Model) handleQuestionInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Enter):
		if m.pendingQuestion != nil && m.input.Value() != "" {
			answer := m.input.Value()
			m.pendingQuestion.AnswerCh <- answer
			m.pendingQuestion = nil
			m.input.SetValue("")
			m.input.Placeholder = "Enter a command..."
		}
	case key.Matches(msg, m.keys.Escape):
		if m.pendingQuestion != nil {
			m.pendingQuestion.AnswerCh <- ""
			m.pendingQuestion = nil
			m.input.Placeholder = "Enter a command..."
		}
		m.inputFocus = false
		m.input.Blur()
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// handleSettingsInput handles key input in settings view
func (m Model) handleSettingsInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// If editing workspace path, handle text input
	if m.settingsEditing {
		switch {
		case key.Matches(msg, m.keys.Enter):
			// Save workspace path
			_ = config.Set("workspace_path", m.settingsInput.Value())
			m.settingsEditing = false
			m.settingsInput.Blur()
			return m, nil
		case key.Matches(msg, m.keys.Escape):
			// Cancel editing
			m.settingsEditing = false
			m.settingsInput.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			m.settingsInput, cmd = m.settingsInput.Update(msg)
			return m, cmd
		}
	}

	switch {
	case key.Matches(msg, m.keys.Up):
		if m.settingsIdx > 0 {
			m.settingsIdx--
		}
		return m, nil
	case key.Matches(msg, m.keys.Down):
		if m.settingsIdx < 2 {
			m.settingsIdx++
		}
		return m, nil
	case key.Matches(msg, m.keys.Enter):
		switch m.settingsIdx {
		case 0: // Language toggle
			lang := config.GetString("language")
			if lang == "ko" {
				lang = "en"
			} else {
				lang = "ko"
			}
			_ = config.Set("language", lang)
			i18n.SetLanguage(lang)
		case 1: // Provider cycle
			provider := config.GetString("default_provider")
			switch provider {
			case "claude":
				provider = "opencode"
			case "opencode":
				provider = "auto"
			default:
				provider = "claude"
			}
			_ = config.Set("default_provider", provider)
		case 2: // Workspace path edit
			m.settingsEditing = true
			m.settingsInput = textinput.New()
			m.settingsInput.SetValue(config.GetString("workspace_path"))
			m.settingsInput.Focus()
			m.settingsInput.Width = 40
		}
		return m, nil
	case key.Matches(msg, m.keys.Escape):
		// Close settings
		m.shared.mu.Lock()
		m.shared.viewMode = m.previousViewMode
		m.shared.mu.Unlock()
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	}
	return m, nil
}

// updateSheepList updates the sheep list
func (m *Model) updateSheepList(sheep []SheepInfo) {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()

	m.shared.sheepList = []string{}
	for _, info := range sheep {
		m.shared.sheepList = append(m.shared.sheepList, info.Name)
		if _, ok := m.shared.sheep[info.Name]; !ok {
			vp := viewport.New(m.getViewportWidthLocked(), m.getViewportHeightLocked())
			state := &SheepState{
				Name:        info.Name,
				Project:     info.ProjectName,
				ProjectPath: info.ProjectPath,
				SessionID:   info.SessionID,
				Provider:    info.Provider,
				Status:      StatusIdle,
				Output:      []string{},
				Viewport:    vp,
			}
			// Load task history from DB
			if history, err := loadTaskHistoryFromDB(info.Name); err == nil {
				state.TaskHistory = history
			}
			m.shared.sheep[info.Name] = state
		} else {
			// Update existing info
			s := m.shared.sheep[info.Name]
			s.Project = info.ProjectName
			s.ProjectPath = info.ProjectPath
			s.SessionID = info.SessionID
			s.Provider = info.Provider
			// Reload task history from DB
			if history, err := loadTaskHistoryFromDB(info.Name); err == nil {
				s.TaskHistory = history
			}
		}
	}

	// Update shepherd state too (task history)
	m.updateShepherdStateLocked()

	// Recalculate all viewport sizes (layout refresh)
	m.recalculateViewportSizesLocked()
}

// updateShepherdStateLocked updates shepherd state (requires lock)
func (m *Model) updateShepherdStateLocked() {
	if _, ok := m.shared.sheep[ShepherdName]; !ok {
		vp := viewport.New(m.getViewportWidthLocked(), m.getViewportHeightLocked())
		m.shared.sheep[ShepherdName] = &SheepState{
			Name:     ShepherdName,
			Status:   StatusIdle,
			Output:   []string{},
			Viewport: vp,
		}
	}
	// Load shepherd task history from DB
	if history, err := loadTaskHistoryFromDB(ShepherdName); err == nil {
		m.shared.sheep[ShepherdName].TaskHistory = history
	}
}

// nextPane moves to the next pane (Tab)
func (m *Model) nextPane() {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()

	if len(m.shared.sheepList) == 0 {
		return
	}
	if m.shared.viewMode == ViewModeSplit {
		m.shared.focusedPane = (m.shared.focusedPane + 1) % len(m.shared.sheepList)
	}
}

// prevPane moves to the previous pane (Shift+Tab)
func (m *Model) prevPane() {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()

	if len(m.shared.sheepList) == 0 {
		return
	}
	if m.shared.viewMode == ViewModeSplit {
		m.shared.focusedPane--
		if m.shared.focusedPane < 0 {
			m.shared.focusedPane = len(m.shared.sheepList) - 1
		}
	}
}

// nextItem moves to the next item (Down)
func (m *Model) nextItem() {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()

	// Shepherd (-1) + project list
	totalItems := len(m.shared.sheepList) + 1

	switch m.shared.viewMode {
	case ViewModeDashboard, ViewModeTaskList:
		if m.shared.viewMode == ViewModeTaskList && m.shared.viewingTaskID == -1 {
			// Select task in task list
			var s *SheepState
			if m.shared.selectedIdx == -1 {
				// Shepherd selected
				s = m.shared.sheep[ShepherdName]
			} else if m.shared.selectedIdx < len(m.shared.sheepList) {
				name := m.shared.sheepList[m.shared.selectedIdx]
				s = m.shared.sheep[name]
			}
			if s != nil && len(s.TaskHistory) > 0 {
				m.shared.selectedTaskIdx = (m.shared.selectedTaskIdx + 1) % len(s.TaskHistory)
				m.adjustTaskListScrollLocked(len(s.TaskHistory))
				return
			}
		}
		// -1(shepherd) -> 0 -> 1 -> ... -> len-1 -> -1
		m.shared.selectedIdx++
		if m.shared.selectedIdx >= len(m.shared.sheepList) {
			m.shared.selectedIdx = -1
		}
		m.shared.taskListScrollOffset = 0
		m.adjustSidebarScrollLocked()
	case ViewModeSplit:
		if len(m.shared.sheepList) > 0 {
			m.shared.focusedPane = (m.shared.focusedPane + 1) % len(m.shared.sheepList)
		}
	}
	_ = totalItems // suppress unused warning
}

// stopSelectedTask stops the selected sheep's task
func (m *Model) stopSelectedTask() tea.Cmd {
	m.shared.mu.RLock()
	selectedIdx := m.shared.selectedIdx
	sheepList := m.shared.sheepList
	sheep := m.shared.sheep
	viewMode := m.shared.viewMode
	focusedPane := m.shared.focusedPane
	m.shared.mu.RUnlock()

	// Determine the selected sheep
	var targetName string
	if viewMode == ViewModeSplit {
		if focusedPane >= 0 && focusedPane < len(sheepList) {
			targetName = sheepList[focusedPane]
		}
	} else {
		if selectedIdx >= 0 && selectedIdx < len(sheepList) {
			targetName = sheepList[selectedIdx]
		}
	}

	if targetName == "" {
		return nil
	}

	// Check if working
	s, ok := sheep[targetName]
	if !ok || s.Status != StatusWorking {
		return nil
	}

	// Stop task
	result, err := worker.StopTask(targetName)
	if err != nil {
		return func() tea.Msg {
			return ErrorMsg{Error: err}
		}
	}

	// Save output of stopped task
	if result != nil && result.TaskID > 0 {
		_ = queue.FailTaskWithOutput(result.TaskID, "Stopped by user", result.OutputLines)
	}

	return func() tea.Msg {
		return SheepStatusMsg{
			SheepName: targetName,
			Status:    StatusIdle,
		}
	}
}

// prevItem 이전 항목으로 이동 (↑)
func (m *Model) prevItem() {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()

	switch m.shared.viewMode {
	case ViewModeDashboard, ViewModeTaskList:
		if m.shared.viewMode == ViewModeTaskList && m.shared.viewingTaskID == -1 {
			// Select task in task list
			var s *SheepState
			if m.shared.selectedIdx == -1 {
				// Shepherd selected
				s = m.shared.sheep[ShepherdName]
			} else if m.shared.selectedIdx < len(m.shared.sheepList) {
				name := m.shared.sheepList[m.shared.selectedIdx]
				s = m.shared.sheep[name]
			}
			if s != nil && len(s.TaskHistory) > 0 {
				m.shared.selectedTaskIdx--
				if m.shared.selectedTaskIdx < 0 {
					m.shared.selectedTaskIdx = len(s.TaskHistory) - 1
				}
				m.adjustTaskListScrollLocked(len(s.TaskHistory))
				return
			}
		}
		// len-1 -> ... -> 1 -> 0 -> -1(목자) -> len-1
		m.shared.selectedIdx--
		if m.shared.selectedIdx < -1 {
			m.shared.selectedIdx = len(m.shared.sheepList) - 1
		}
		m.shared.taskListScrollOffset = 0
		m.adjustSidebarScrollLocked()
	case ViewModeSplit:
		if len(m.shared.sheepList) > 0 {
			m.shared.focusedPane--
			if m.shared.focusedPane < 0 {
				m.shared.focusedPane = len(m.shared.sheepList) - 1
			}
		}
	}
}

// adjustSidebarScrollLocked 선택 항목이 보이도록 스크롤 오프셋 조정 (lock 보유 상태에서 호출)
func (m *Model) adjustSidebarScrollLocked() {
	idx := m.shared.selectedIdx // -1=목자, 0~=프로젝트
	totalProjects := len(m.shared.sheepList)

	// 목자 선택 시 스크롤을 맨 위로
	if idx == -1 {
		m.shared.sidebarScrollOffset = 0
		return
	}

	// 프로젝트 목록에 사용 가능한 줄 수 계산 (header 4줄 + footer 6줄 + 테두리 2줄)
	availableLines := m.height - 6 - 4 - 6 - 2
	if availableLines < 1 {
		availableLines = 1
	}

	// 스크롤이 불필요하면 리셋
	if totalProjects <= availableLines {
		m.shared.sidebarScrollOffset = 0
		return
	}

	// 선택 항목이 인디케이터(▲/▼)에 가리지 않도록 1칸 여유 확보
	// 위쪽: 스크롤 중이면 첫 줄이 ▲ 인디케이터이므로 idx가 offset+1 이상이어야 함
	if m.shared.sidebarScrollOffset > 0 && idx <= m.shared.sidebarScrollOffset {
		m.shared.sidebarScrollOffset = idx - 1
		if m.shared.sidebarScrollOffset < 0 {
			m.shared.sidebarScrollOffset = 0
		}
	} else if idx < m.shared.sidebarScrollOffset {
		m.shared.sidebarScrollOffset = idx
	}
	// 아래쪽: 끝이 아니면 마지막 줄이 ▼ 인디케이터이므로 idx가 offset+available-2 이하여야 함
	endIdx := m.shared.sidebarScrollOffset + availableLines
	if endIdx < totalProjects && idx >= endIdx-1 {
		m.shared.sidebarScrollOffset = idx - availableLines + 2
	} else if idx >= endIdx {
		m.shared.sidebarScrollOffset = idx - availableLines + 1
	}

	// 범위 보정
	maxOffset := totalProjects - availableLines
	if m.shared.sidebarScrollOffset > maxOffset {
		m.shared.sidebarScrollOffset = maxOffset
	}
	if m.shared.sidebarScrollOffset < 0 {
		m.shared.sidebarScrollOffset = 0
	}
}

// adjustTaskListScrollLocked 작업 목록 스크롤 오프셋 조정 (lock 보유 상태에서 호출)
func (m *Model) adjustTaskListScrollLocked(totalTasks int) {
	idx := m.shared.selectedTaskIdx

	// 작업 목록 사용 가능한 줄 수 (contentHeight - 헤더 4줄 - 테두리 2줄)
	availableLines := m.height - 6 - 4 - 2
	if availableLines < 1 {
		availableLines = 1
	}

	if totalTasks <= availableLines {
		m.shared.taskListScrollOffset = 0
		return
	}

	// 인디케이터에 가리지 않도록 1칸 여유
	if m.shared.taskListScrollOffset > 0 && idx <= m.shared.taskListScrollOffset {
		m.shared.taskListScrollOffset = idx - 1
		if m.shared.taskListScrollOffset < 0 {
			m.shared.taskListScrollOffset = 0
		}
	} else if idx < m.shared.taskListScrollOffset {
		m.shared.taskListScrollOffset = idx
	}

	endIdx := m.shared.taskListScrollOffset + availableLines
	if endIdx < totalTasks && idx >= endIdx-1 {
		m.shared.taskListScrollOffset = idx - availableLines + 2
	} else if idx >= endIdx {
		m.shared.taskListScrollOffset = idx - availableLines + 1
	}

	// 범위 보정
	maxOffset := totalTasks - availableLines
	if m.shared.taskListScrollOffset > maxOffset {
		m.shared.taskListScrollOffset = maxOffset
	}
	if m.shared.taskListScrollOffset < 0 {
		m.shared.taskListScrollOffset = 0
	}
}

// scrollUp 위로 스크롤
func (m *Model) scrollUp() {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()

	if len(m.shared.sheepList) == 0 {
		return
	}
	var idx int
	if m.shared.viewMode == ViewModeDashboard {
		idx = m.shared.selectedIdx
	} else {
		idx = m.shared.focusedPane
	}
	if idx < len(m.shared.sheepList) {
		name := m.shared.sheepList[idx]
		if s, ok := m.shared.sheep[name]; ok {
			s.Viewport.LineUp(1)
		}
	}
}

// scrollDown 아래로 스크롤
func (m *Model) scrollDown() {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()

	if len(m.shared.sheepList) == 0 {
		return
	}
	var idx int
	if m.shared.viewMode == ViewModeDashboard {
		idx = m.shared.selectedIdx
	} else {
		idx = m.shared.focusedPane
	}
	if idx < len(m.shared.sheepList) {
		name := m.shared.sheepList[idx]
		if s, ok := m.shared.sheep[name]; ok {
			s.Viewport.LineDown(1)
		}
	}
}

// getViewportWidth 뷰포트 너비 (락 획득 후 호출)
func (m *Model) getViewportWidth() int {
	m.shared.mu.RLock()
	defer m.shared.mu.RUnlock()
	return m.getViewportWidthLocked()
}

// recalculateViewportSizesLocked 뷰포트 크기 재계산 (락이 잡혀있는 상태에서 호출)
func (m *Model) recalculateViewportSizesLocked() {
	vpWidth := m.getViewportWidthLocked()
	vpHeight := m.getViewportHeightLocked()

	for _, s := range m.shared.sheep {
		s.Viewport.Width = vpWidth
		s.Viewport.Height = vpHeight
		// 줄바꿈 다시 적용
		if len(s.Output) > 0 {
			wrapWidth := vpWidth - 2
			if wrapWidth < 20 {
				wrapWidth = 20
			}
			var wrappedLines []string
			for _, line := range s.Output {
				wrappedLines = append(wrappedLines, wrapText(line, wrapWidth)...)
			}
			s.Viewport.SetContent(strings.Join(wrappedLines, "\n"))
		}
	}
	// 목자 뷰포트도 업데이트
	if m.sidebarHidden {
		m.shepherdViewport.Width = m.width - 6
	} else {
		m.shepherdViewport.Width = m.width - SidebarWidth - 12
	}
	m.shepherdViewport.Height = m.height - 16
	// 목자 출력 줄바꿈 다시 적용
	if len(m.shepherdOutput) > 0 {
		wrapWidth := m.shepherdViewport.Width - 2
		if wrapWidth < 20 {
			wrapWidth = 20
		}
		var wrappedLines []string
		for _, line := range m.shepherdOutput {
			wrappedLines = append(wrappedLines, wrapText(line, wrapWidth)...)
		}
		m.shepherdViewport.SetContent(strings.Join(wrappedLines, "\n"))
	}
}

// getViewportWidthLocked 뷰포트 너비 (이미 락이 잡혀있는 상태에서 호출)
func (m *Model) getViewportWidthLocked() int {
	if m.width < 40 {
		return 40
	}
	viewMode := m.shared.viewMode

	if viewMode == ViewModeDashboard || viewMode == ViewModeTaskList {
		if m.sidebarHidden {
			return m.width - 4 // 테두리만
		}
		return m.width - SidebarWidth - 4 // 사이드바 - 테두리
	}
	// 분할 뷰
	cols := m.getSplitColsLocked()
	return (m.width / cols) - 4
}

// getViewportHeight 뷰포트 높이 (락 획득 후 호출)
func (m *Model) getViewportHeight() int {
	m.shared.mu.RLock()
	defer m.shared.mu.RUnlock()
	return m.getViewportHeightLocked()
}

// GetMainViewWidth 메인뷰 너비 반환 (줄바꿈 계산용)
func (m *Model) GetMainViewWidth() int {
	if m.width < 40 {
		return 60 // 기본값
	}
	// 대시보드: 전체 너비 - 사이드바(20) - 테두리(4)
	return m.width - 24
}

// getViewportHeightLocked 뷰포트 높이 (이미 락이 잡혀있는 상태에서 호출)
func (m *Model) getViewportHeightLocked() int {
	if m.height < 10 {
		return 10
	}
	// 헤더(1) + 입력창(3: 테두리2 + 내용1) + 본문 테두리(2) + 상세 헤더(5) + 여백(1)
	return m.height - 12
}

// getSplitCols 분할 열 수 (락 획득 후 호출)
func (m *Model) getSplitCols() int {
	m.shared.mu.RLock()
	defer m.shared.mu.RUnlock()
	return m.getSplitColsLocked()
}

// getSplitColsLocked 분할 열 수 (이미 락이 잡혀있는 상태에서 호출)
func (m *Model) getSplitColsLocked() int {
	count := len(m.shared.sheepList)
	if count <= 2 {
		if count == 0 {
			return 1
		}
		return count
	}
	return 2
}

// View 렌더링
func (m Model) View() string {
	if m.width == 0 {
		return i18n.T().Loading
	}

	m.shared.mu.RLock()
	viewMode := m.shared.viewMode
	m.shared.mu.RUnlock()

	var content string
	switch viewMode {
	case ViewModeSplit:
		content = m.renderSplitView()
	case ViewModeDashboard:
		content = m.renderDashboardView()
	case ViewModeTaskList:
		content = m.renderTaskListView()
	case ViewModeSettings:
		content = m.renderSettingsView()
	}

	header := m.renderHeader()
	input := m.renderInput()

	// 전체를 정확한 높이로 배치
	full := lipgloss.JoinVertical(
		lipgloss.Top,
		header,
		content,
		input,
	)

	// 각 줄을 터미널 너비로 패딩하여 이전 내용 덮어쓰기
	return padLines(full, m.width, m.height)
}

// padLines 각 줄을 지정된 너비로 패딩하고, 총 높이를 맞춤
func padLines(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	result := make([]string, height)

	for i := 0; i < height; i++ {
		if i < len(lines) {
			lineWidth := lipgloss.Width(lines[i])
			if lineWidth < width {
				result[i] = lines[i] + strings.Repeat(" ", width-lineWidth)
			} else {
				result[i] = lines[i]
			}
		} else {
			result[i] = strings.Repeat(" ", width)
		}
	}

	return strings.Join(result, "\n")
}

// renderHeader 헤더 렌더링
func (m Model) renderHeader() string {
	title := i18n.T().TUITitle

	viewModeStr := i18n.T().ViewSwitch
	help := i18n.T().Help

	left := HeaderStyle.Render(title)
	right := StatusBarStyle.Render(viewModeStr + "  " + help)

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}

	return left + strings.Repeat(" ", gap) + right
}

// renderInput 입력창 렌더링
func (m Model) renderInput() string {
	style := InputStyle
	if m.inputFocus {
		style = FocusedInputStyle
	}

	prompt := "> "
	if m.pendingQuestion != nil {
		prompt = "❓ "
	}

	input := prompt + m.input.View()

	// 에러 메시지
	if m.lastError != "" {
		input += "\n" + ErrorStyle.Render(i18n.T().ErrorPrefix+m.lastError)
	}

	return style.Width(m.width - 2).Render(input)
}

// renderDashboardView 대시보드 뷰 렌더링
func (m Model) renderDashboardView() string {
	m.shared.mu.RLock()
	sheepList := m.shared.sheepList
	selectedIdx := m.shared.selectedIdx
	sheep := m.shared.sheep
	m.shared.mu.RUnlock()

	// 사이드바: 양 목록
	sidebar := m.renderSheepList()

	// 메인: 선택된 양 상세 또는 목자 화면
	var main string
	if selectedIdx == -1 {
		// 목자 선택됨
		main = m.renderShepherdDetail()
	} else if selectedIdx >= 0 && selectedIdx < len(sheepList) {
		name := sheepList[selectedIdx]
		if s, ok := sheep[name]; ok {
			main = m.renderSheepDetail(s)
		}
	} else if len(sheepList) == 0 {
		main = i18n.T().NoProjects
	}

	contentHeight := m.height - 6

	// 사이드바 숨김 처리
	if m.sidebarHidden {
		mainWidth := m.width - 2
		mainPlaced := lipgloss.Place(mainWidth, contentHeight, lipgloss.Left, lipgloss.Top, main)
		return BorderStyle.Render(mainPlaced)
	}

	mainWidth := m.width - SidebarWidth - 4

	// 내용을 정확한 크기로 배치
	sidebarPlaced := lipgloss.Place(SidebarWidth, contentHeight, lipgloss.Left, lipgloss.Top, sidebar)
	mainPlaced := lipgloss.Place(mainWidth, contentHeight, lipgloss.Left, lipgloss.Top, main)

	sidebarBox := BorderStyle.Render(sidebarPlaced)
	mainBox := BorderStyle.Render(mainPlaced)

	return lipgloss.JoinHorizontal(lipgloss.Top, sidebarBox, mainBox)
}

// renderSplitView 분할 뷰 렌더링 (작업 중인 프로젝트만 표시)
func (m Model) renderSplitView() string {
	m.shared.mu.RLock()
	sheepList := m.shared.sheepList
	sheep := m.shared.sheep
	focusedPane := m.shared.focusedPane
	m.shared.mu.RUnlock()

	if len(sheepList) == 0 {
		return m.renderEmptyState()
	}

	// 작업 중인 양만 필터링 (StatusWorking, StatusWaitingInput)
	var workingSheep []string
	for _, name := range sheepList {
		if s, ok := sheep[name]; ok {
			if s.Status == StatusWorking || s.Status == StatusWaitingInput {
				workingSheep = append(workingSheep, name)
			}
		}
	}

	// 작업 중인 양이 없으면 안내 메시지 표시
	if len(workingSheep) == 0 {
		return m.renderNoWorkingState()
	}

	count := len(workingSheep)
	cols := m.getSplitColsForCount(count)
	rows := (count + cols - 1) / cols

	paneWidth := (m.width / cols) - 2
	// 헤더(1) + 입력창(3) = 4, 각 패널 테두리(2)
	paneHeight := (m.height-4)/rows - 2

	var rowViews []string
	for row := 0; row < rows; row++ {
		var colViews []string
		for col := 0; col < cols; col++ {
			idx := row*cols + col
			if idx < count {
				name := workingSheep[idx]
				if s, ok := sheep[name]; ok {
					// 원래 sheepList에서의 인덱스 찾기
					originalIdx := -1
					for i, n := range sheepList {
						if n == name {
							originalIdx = i
							break
						}
					}
					focused := originalIdx == focusedPane
					colViews = append(colViews, m.renderSheepPane(s, paneWidth, paneHeight, focused))
				}
			}
		}
		rowViews = append(rowViews, lipgloss.JoinHorizontal(lipgloss.Top, colViews...))
	}

	return lipgloss.JoinVertical(lipgloss.Top, rowViews...)
}

// renderNoWorkingState 작업 중인 양이 없을 때 표시
func (m Model) renderNoWorkingState() string {
	msg := "\n" + i18n.T().NoWorkingProjects + "\n\n" + i18n.T().SwitchToDashboard + "\n"
	return lipgloss.NewStyle().
		Width(m.width - 4).
		Height(m.height - 6).
		Align(lipgloss.Center, lipgloss.Center).
		Foreground(ColorMuted).
		Render(msg)
}

// getSplitColsForCount 주어진 개수에 대한 분할 열 수 계산
func (m *Model) getSplitColsForCount(count int) int {
	if count <= 2 {
		if count == 0 {
			return 1
		}
		return count
	}
	return 2
}

// renderEmptyState 빈 상태 렌더링
func (m Model) renderEmptyState() string {
	msg := "\n" + i18n.T().EmptyState + "\n"
	return lipgloss.NewStyle().
		Width(m.width - 4).
		Height(m.height - 6).
		Align(lipgloss.Center, lipgloss.Center).
		Foreground(ColorMuted).
		Render(msg)
}

// renderSheepList 프로젝트 목록 렌더링 (스크롤 지원)
func (m Model) renderSheepList() string {
	m.shared.mu.RLock()
	sheepList := m.shared.sheepList
	sheep := m.shared.sheep
	selectedIdx := m.shared.selectedIdx
	scrollOffset := m.shared.sidebarScrollOffset
	m.shared.mu.RUnlock()

	contentHeight := m.height - 6

	// 헤더 (타이틀 + 구분선 + 목자 + 구분선 = 4줄)
	var header []string
	header = append(header, TitleStyle.Render(i18n.T().ProjectListTitle))
	header = append(header, DividerStyle.Render(strings.Repeat("─", SidebarInnerMax)))

	// 목자 (매니저) 항목
	shepherdIcon := "👤"
	if s, ok := sheep[ShepherdName]; ok && s.Status == StatusWorking {
		shepherdIcon = "🔧"
	}
	shepherdItem := shepherdIcon + " " + i18n.T().ShepherdSidebar
	if selectedIdx == -1 {
		shepherdItem = SelectedStyle.Render(shepherdItem)
	} else {
		shepherdItem = NormalItemStyle.Render(shepherdItem)
	}
	header = append(header, shepherdItem)
	header = append(header, DividerStyle.Render(strings.Repeat("─", SidebarInnerMax)))

	// 통계 (하단 고정 = 빈줄 + 구분선 + 통계 제목 + 3개 = 6줄)
	working, done, idle := 0, 0, 0
	for _, s := range sheep {
		switch s.Status {
		case StatusWorking, StatusWaitingInput:
			working++
		case StatusDone:
			done++
		default:
			idle++
		}
	}
	var footer []string
	footer = append(footer, "")
	footer = append(footer, DividerStyle.Render(strings.Repeat("─", SidebarInnerMax)))
	footer = append(footer, StatusBarStyle.Render(i18n.T().Stats))
	footer = append(footer, StatusBarStyle.Render(fmt.Sprintf(i18n.T().WorkingFmt, working)))
	footer = append(footer, StatusBarStyle.Render(fmt.Sprintf(i18n.T().CompletedFmt, done)))
	footer = append(footer, StatusBarStyle.Render(fmt.Sprintf(i18n.T().IdleFmt, idle)))

	// 프로젝트 목록에 사용 가능한 줄 수 계산
	// contentHeight에서 header(4줄) + footer(6줄) + 테두리(2줄) 제외
	headerLines := len(header)
	footerLines := len(footer)
	availableLines := contentHeight - headerLines - footerLines - 2
	if availableLines < 1 {
		availableLines = 1
	}

	// 프로젝트 항목 생성
	var projectItems []string
	for i, name := range sheepList {
		if s, ok := sheep[name]; ok {
			icon := StatusIcons[s.Status]
			displayName := s.Project
			if displayName == "" {
				displayName = s.Name
			}
			if lipgloss.Width(displayName) > SidebarTextMax {
				displayName = truncateToWidth(displayName, SidebarTextMax-1) + "…"
			}
			item := icon + " " + displayName

			if i == selectedIdx {
				item = SelectedStyle.Render(item)
			} else {
				item = NormalItemStyle.Render(item)
			}
			projectItems = append(projectItems, item)
		}
	}

	// 스크롤이 필요한 경우 보이는 범위만 추출
	if len(projectItems) > availableLines {
		// 스크롤 오프셋 범위 보정
		maxOffset := len(projectItems) - availableLines
		if scrollOffset > maxOffset {
			scrollOffset = maxOffset
		}
		if scrollOffset < 0 {
			scrollOffset = 0
		}

		end := scrollOffset + availableLines
		if end > len(projectItems) {
			end = len(projectItems)
		}

		// 스크롤 인디케이터 표시
		visibleItems := projectItems[scrollOffset:end]
		if scrollOffset > 0 {
			visibleItems[0] = DividerStyle.Render("▲ " + fmt.Sprintf("%d more", scrollOffset))
		}
		if end < len(projectItems) {
			remaining := len(projectItems) - end
			visibleItems[len(visibleItems)-1] = DividerStyle.Render("▼ " + fmt.Sprintf("%d more", remaining))
		}
		projectItems = visibleItems
	}

	// 헤더 + 프로젝트 목록 + 푸터 합치기
	var items []string
	items = append(items, header...)
	items = append(items, projectItems...)
	items = append(items, footer...)

	return lipgloss.JoinVertical(lipgloss.Left, items...)
}

// renderShepherdDetail 목자 상세 렌더링
func (m Model) renderShepherdDetail() string {
	var lines []string

	lines = append(lines, SheepNameStyle.Render(i18n.T().ShepherdManager))
	lines = append(lines, DividerStyle.Render(strings.Repeat("─", m.width-SidebarWidth-10)))

	// 목자 출력이 있으면 뷰포트로 표시 (스크롤 가능)
	if len(m.shepherdOutput) > 0 {
		lines = append(lines, "")
		lines = append(lines, m.shepherdViewport.View())
		lines = append(lines, "")
		lines = append(lines, DividerStyle.Render(strings.Repeat("─", m.width-SidebarWidth-10)))
	} else {
		// 출력이 없으면 도움말 표시
		lines = append(lines, "")
		lines = append(lines, i18n.T().ShepherdHelp)
		lines = append(lines, "")
		lines = append(lines, SheepNameStyle.Render(i18n.T().Examples))
		lines = append(lines, "  • "+i18n.T().Example1)
		lines = append(lines, "  • "+i18n.T().Example2)
		lines = append(lines, "  • "+i18n.T().Example3)
		lines = append(lines, "  • "+i18n.T().Example4)
		lines = append(lines, "")
		lines = append(lines, SheepNameStyle.Render(i18n.T().RememberTitle))
		lines = append(lines, "  • "+i18n.T().RememberHelp)
		lines = append(lines, "")
		lines = append(lines, DividerStyle.Render(strings.Repeat("─", m.width-SidebarWidth-10)))

		// 저장된 기억 표시
		memories := m.loadMemories()
		if len(memories) > 0 {
			lines = append(lines, SheepNameStyle.Render(i18n.T().SavedMemories))
			for _, mem := range memories {
				if len(mem) > m.width-SidebarWidth-14 {
					mem = mem[:m.width-SidebarWidth-17] + "..."
				}
				lines = append(lines, "  • "+mem)
			}
		} else {
			lines = append(lines, StatusBarStyle.Render(i18n.T().NoMemories))
		}
	}

	lines = append(lines, "")
	lines = append(lines, StatusBarStyle.Render(i18n.T().InputHint))

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderShepherdTaskList 목자 작업 목록 렌더링 (스크롤 지원)
func (m Model) renderShepherdTaskList(s *SheepState, selectedIdx int) string {
	var headerLines []string

	// 헤더
	header := SheepNameStyle.Render(i18n.T().ShepherdTaskList)
	headerLines = append(headerLines, header)
	headerLines = append(headerLines, DividerStyle.Render(strings.Repeat("─", m.width-SidebarWidth-10)))

	if len(s.TaskHistory) == 0 {
		headerLines = append(headerLines, StatusBarStyle.Render(i18n.T().NoTasks))
		headerLines = append(headerLines, "")
		headerLines = append(headerLines, StatusBarStyle.Render(i18n.T().GiveCommand))
		headerLines = append(headerLines, "")
		headerLines = append(headerLines, i18n.T().Examples+":")
		headerLines = append(headerLines, "  • "+i18n.T().Example1)
		headerLines = append(headerLines, "  • "+i18n.T().Example3)
		return lipgloss.JoinVertical(lipgloss.Left, headerLines...)
	}

	headerLines = append(headerLines, StatusBarStyle.Render(i18n.T().TaskListKeys))
	headerLines = append(headerLines, "")

	// 작업 항목 생성
	var taskItems []string
	for i := len(s.TaskHistory) - 1; i >= 0; i-- {
		task := s.TaskHistory[i]
		displayIdx := len(s.TaskHistory) - i

		var statusIcon string
		switch task.Status {
		case "completed":
			statusIcon = "✅"
		case "working":
			statusIcon = "🔄"
		case "error":
			statusIcon = "❌"
		default:
			statusIcon = "⏸"
		}

		prompt := task.Prompt
		// 메인 영역 너비 기준으로 truncation
		mainInner := m.width - SidebarWidth - 8
		prefix := fmt.Sprintf(" %s %2d. ", statusIcon, displayIdx)
		maxPromptWidth := mainInner - lipgloss.Width(prefix)
		if maxPromptWidth < 10 {
			maxPromptWidth = 10
		}
		if lipgloss.Width(prompt) > maxPromptWidth {
			prompt = truncateToWidth(prompt, maxPromptWidth-1) + "…"
		}

		actualIdx := len(s.TaskHistory) - 1 - i
		line := prefix + prompt
		if actualIdx == selectedIdx {
			line = SelectedStyle.Render("▶" + line[1:])
		}
		taskItems = append(taskItems, line)
	}

	// 스크롤 적용
	contentHeight := m.height - 6
	availableLines := contentHeight - len(headerLines) - 2
	if availableLines < 1 {
		availableLines = 1
	}

	m.shared.mu.RLock()
	scrollOffset := m.shared.taskListScrollOffset
	m.shared.mu.RUnlock()

	if len(taskItems) > availableLines {
		maxOffset := len(taskItems) - availableLines
		if scrollOffset > maxOffset {
			scrollOffset = maxOffset
		}
		if scrollOffset < 0 {
			scrollOffset = 0
		}

		end := scrollOffset + availableLines
		if end > len(taskItems) {
			end = len(taskItems)
		}

		visibleItems := make([]string, end-scrollOffset)
		copy(visibleItems, taskItems[scrollOffset:end])

		if scrollOffset > 0 {
			visibleItems[0] = DividerStyle.Render(fmt.Sprintf("▲ %d more", scrollOffset))
		}
		if end < len(taskItems) {
			remaining := len(taskItems) - end
			visibleItems[len(visibleItems)-1] = DividerStyle.Render(fmt.Sprintf("▼ %d more", remaining))
		}
		taskItems = visibleItems
	}

	var lines []string
	lines = append(lines, headerLines...)
	lines = append(lines, taskItems...)
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderSheepDetail 프로젝트 상세 렌더링
func (m Model) renderSheepDetail(s *SheepState) string {
	var headerLines []string

	// 헤더: 프로젝트명 (양이름)
	var header string
	if s.Project != "" {
		header = SheepNameStyle.Render("🔍 "+s.Project) + " "
		header += ProjectNameStyle.Render("(" + s.Name + ")")
	} else {
		header = SheepNameStyle.Render("🔍 " + s.Name)
	}
	headerLines = append(headerLines, header)
	headerLines = append(headerLines, DividerStyle.Render(strings.Repeat("─", m.width-SidebarWidth-10)))

	// 상태
	statusIcon := StatusIcons[s.Status]
	statusText := StatusStyle(s.Status).Render(s.Status.String())
	headerLines = append(headerLines, i18n.T().StatusLabel+statusIcon+" "+statusText)

	// Provider 표시
	providerName := s.Provider
	if providerName == "" {
		providerName = "claude"
	}
	var providerDisplay string
	switch providerName {
	case "claude":
		providerDisplay = "🤖 Claude"
	case "opencode":
		providerDisplay = "🟢 " + worker.GetOpenCodeDisplayName()
	case "auto":
		providerDisplay = "⚡ Auto"
	default:
		providerDisplay = providerName
	}
	headerLines = append(headerLines, i18n.T().ProviderLabel+ProjectNameStyle.Render(providerDisplay))

	if s.SessionID != "" {
		headerLines = append(headerLines, i18n.T().SessionLabel+ProjectNameStyle.Render(s.SessionID[:8]+"..."))
	}

	headerLines = append(headerLines, DividerStyle.Render(strings.Repeat("─", m.width-SidebarWidth-10)))

	headerContent := lipgloss.JoinVertical(lipgloss.Left, headerLines...)

	// 출력 (Viewport 사용하여 스크롤 가능)
	var outputContent string
	if len(s.Output) > 0 {
		outputContent = s.Viewport.View()
	} else {
		outputContent = StatusBarStyle.Render(i18n.T().NoOutput)
	}

	return lipgloss.JoinVertical(lipgloss.Left, headerContent, outputContent)
}

// renderSheepPane 프로젝트 패널 렌더링 (분할 뷰용)
func (m Model) renderSheepPane(s *SheepState, width, height int, focused bool) string {
	var headerLines []string

	// 헤더: 프로젝트명 (양이름)
	statusIcon := StatusIcons[s.Status]
	var header string
	if s.Project != "" {
		header = statusIcon + " " + SheepNameStyle.Render(s.Project)
		header += " " + ProjectNameStyle.Render("("+s.Name+")")
	} else {
		header = statusIcon + " " + SheepNameStyle.Render(s.Name)
	}
	headerLines = append(headerLines, header)
	headerLines = append(headerLines, DividerStyle.Render(strings.Repeat("─", width-4)))

	headerContent := lipgloss.JoinVertical(lipgloss.Left, headerLines...)
	headerHeight := 2 // 헤더 + 구분선

	// 출력 (Viewport 사용하여 스크롤 가능)
	// Viewport 크기를 패널 크기에 맞게 조정
	outputHeight := height - headerHeight - 2 // 테두리 제외
	if outputHeight < 1 {
		outputHeight = 1
	}

	var outputContent string
	if len(s.Output) > 0 {
		// Viewport 크기가 맞지 않으면 조정
		if s.Viewport.Width != width-4 || s.Viewport.Height != outputHeight {
			s.Viewport.Width = width - 4
			s.Viewport.Height = outputHeight
		}
		outputContent = s.Viewport.View()
	} else {
		outputContent = StatusBarStyle.Render(i18n.T().NoOutput)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, headerContent, outputContent)

	style := BorderStyle
	if focused {
		style = FocusedBorderStyle
	}

	return style.Width(width).Height(height).Render(content)
}

// renderTaskListView 작업 목록 뷰 렌더링
func (m Model) renderTaskListView() string {
	m.shared.mu.RLock()
	sheepList := m.shared.sheepList
	selectedIdx := m.shared.selectedIdx
	sheep := m.shared.sheep
	viewingTaskID := m.shared.viewingTaskID
	selectedTaskIdx := m.shared.selectedTaskIdx
	m.shared.mu.RUnlock()

	if len(sheepList) == 0 {
		return m.renderEmptyState()
	}

	// 사이드바: 프로젝트 목록
	sidebar := m.renderSheepList()

	// 메인: 작업 목록 또는 상세
	var main string
	if selectedIdx == -1 {
		// 목자 선택 시 목자 작업 목록 표시
		if s, ok := sheep[ShepherdName]; ok {
			if viewingTaskID >= 0 {
				main = m.renderTaskDetail(s, viewingTaskID)
			} else {
				main = m.renderShepherdTaskList(s, selectedTaskIdx)
			}
		} else {
			main = m.renderShepherdDetail()
		}
	} else if selectedIdx >= 0 && selectedIdx < len(sheepList) {
		name := sheepList[selectedIdx]
		if s, ok := sheep[name]; ok {
			if viewingTaskID >= 0 {
				main = m.renderTaskDetail(s, viewingTaskID)
			} else {
				main = m.renderTaskList(s, selectedTaskIdx)
			}
		}
	}

	contentHeight := m.height - 6

	// 사이드바 숨김 처리
	if m.sidebarHidden {
		mainWidth := m.width - 2
		mainPlaced := lipgloss.Place(mainWidth, contentHeight, lipgloss.Left, lipgloss.Top, main)
		return BorderStyle.Render(mainPlaced)
	}

	mainWidth := m.width - SidebarWidth - 4

	// 내용을 정확한 크기로 배치
	sidebarPlaced := lipgloss.Place(SidebarWidth, contentHeight, lipgloss.Left, lipgloss.Top, sidebar)
	mainPlaced := lipgloss.Place(mainWidth, contentHeight, lipgloss.Left, lipgloss.Top, main)

	sidebarBox := BorderStyle.Render(sidebarPlaced)
	mainBox := BorderStyle.Render(mainPlaced)

	return lipgloss.JoinHorizontal(lipgloss.Top, sidebarBox, mainBox)
}

// renderTaskList 작업 목록 렌더링 (스크롤 지원)
func (m Model) renderTaskList(s *SheepState, selectedIdx int) string {
	var headerLines []string

	// 헤더
	var header string
	if s.Project != "" {
		header = SheepNameStyle.Render(fmt.Sprintf(i18n.T().TaskListTitleFmt, s.Project))
	} else {
		header = SheepNameStyle.Render(fmt.Sprintf(i18n.T().TaskListTitleFmt, s.Name))
	}
	headerLines = append(headerLines, header)
	headerLines = append(headerLines, DividerStyle.Render(strings.Repeat("─", m.width-SidebarWidth-10)))

	if len(s.TaskHistory) == 0 {
		headerLines = append(headerLines, StatusBarStyle.Render(i18n.T().NoTasks))
		headerLines = append(headerLines, "")
		headerLines = append(headerLines, StatusBarStyle.Render(i18n.T().StartCommand))
		return lipgloss.JoinVertical(lipgloss.Left, headerLines...)
	}

	headerLines = append(headerLines, StatusBarStyle.Render(i18n.T().TaskListKeys))
	headerLines = append(headerLines, "")

	// 작업 항목 생성
	var taskItems []string
	for i := len(s.TaskHistory) - 1; i >= 0; i-- {
		task := s.TaskHistory[i]
		displayIdx := len(s.TaskHistory) - i

		var statusIcon string
		switch task.Status {
		case "completed":
			statusIcon = "✅"
		case "working":
			statusIcon = "🔄"
		case "error":
			statusIcon = "❌"
		default:
			statusIcon = "⏸"
		}

		prompt := task.Prompt
		timeStr := task.StartedAt.Format("15:04")
		// 메인 영역 너비 기준으로 truncation (사이드바+테두리+패딩 제외)
		mainInner := m.width - SidebarWidth - 8
		prefix := fmt.Sprintf("%d. %s ", displayIdx, statusIcon)
		suffix := fmt.Sprintf(" [%s]", timeStr)
		maxPromptWidth := mainInner - lipgloss.Width(prefix) - lipgloss.Width(suffix)
		if maxPromptWidth < 10 {
			maxPromptWidth = 10
		}
		if lipgloss.Width(prompt) > maxPromptWidth {
			prompt = truncateToWidth(prompt, maxPromptWidth-1) + "…"
		}

		item := prefix + prompt + suffix

		actualIdx := len(s.TaskHistory) - 1 - i
		if actualIdx == selectedIdx {
			item = SelectedStyle.Render(item)
		} else {
			item = NormalItemStyle.Render(item)
		}
		taskItems = append(taskItems, item)
	}

	// 스크롤 적용: contentHeight에서 헤더(4줄) + 테두리(2줄) 제외
	contentHeight := m.height - 6
	availableLines := contentHeight - len(headerLines) - 2
	if availableLines < 1 {
		availableLines = 1
	}

	m.shared.mu.RLock()
	scrollOffset := m.shared.taskListScrollOffset
	m.shared.mu.RUnlock()

	if len(taskItems) > availableLines {
		maxOffset := len(taskItems) - availableLines
		if scrollOffset > maxOffset {
			scrollOffset = maxOffset
		}
		if scrollOffset < 0 {
			scrollOffset = 0
		}

		end := scrollOffset + availableLines
		if end > len(taskItems) {
			end = len(taskItems)
		}

		visibleItems := make([]string, end-scrollOffset)
		copy(visibleItems, taskItems[scrollOffset:end])

		if scrollOffset > 0 {
			visibleItems[0] = DividerStyle.Render(fmt.Sprintf("▲ %d more", scrollOffset))
		}
		if end < len(taskItems) {
			remaining := len(taskItems) - end
			visibleItems[len(visibleItems)-1] = DividerStyle.Render(fmt.Sprintf("▼ %d more", remaining))
		}
		taskItems = visibleItems
	}

	var lines []string
	lines = append(lines, headerLines...)
	lines = append(lines, taskItems...)
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

// renderTaskDetail 작업 상세 렌더링 (viewport 스크롤 지원)
func (m Model) renderTaskDetail(s *SheepState, taskID int) string {
	// viewport가 이미 설정되어 있으면 그대로 사용
	return m.taskDetailViewport.View()
}

// updateTaskDetailViewport 작업 상세 viewport 내용 업데이트
func (m *Model) updateTaskDetailViewport(s *SheepState, taskID int) {
	var lines []string

	// taskID(DBTaskID)로 작업 찾기
	var task *TaskRecord
	for i := range s.TaskHistory {
		if s.TaskHistory[i].DBTaskID == taskID {
			task = &s.TaskHistory[i]
			break
		}
	}

	// viewport 크기 계산
	viewportWidth := m.width - SidebarWidth - 12
	viewportHeight := m.height - 8
	if viewportWidth < 40 {
		viewportWidth = 40
	}
	contentWidth := viewportWidth - 4 // 패딩 고려

	if task == nil {
		lines = append(lines, ErrorStyle.Render(i18n.T().TaskNotFound))
		m.taskDetailViewport.SetContent(lipgloss.JoinVertical(lipgloss.Left, lines...))
		return
	}

	// 헤더
	lines = append(lines, SheepNameStyle.Render(fmt.Sprintf(i18n.T().TaskDetailFmt, task.ID)))
	lines = append(lines, DividerStyle.Render(strings.Repeat("─", contentWidth)))

	// 상태
	var statusIcon, statusText string
	switch task.Status {
	case "completed":
		statusIcon = "✅"
		statusText = i18n.T().StatusCompleted
	case "working":
		statusIcon = "🔄"
		statusText = i18n.T().StatusInProgress
	case "error":
		statusIcon = "❌"
		statusText = i18n.T().StatusError
	default:
		statusIcon = "⏸"
		statusText = i18n.T().StatusPending
	}
	lines = append(lines, i18n.T().StatusLabel+statusIcon+" "+statusText)

	// 시간
	lines = append(lines, fmt.Sprintf(i18n.T().StartedFmt, task.StartedAt.Format("2006-01-02 15:04:05")))
	if !task.EndedAt.IsZero() {
		duration := task.EndedAt.Sub(task.StartedAt)
		lines = append(lines, fmt.Sprintf(i18n.T().CompletedTimeFmt, task.EndedAt.Format("15:04:05"), duration.Round(time.Second)))
	}

	lines = append(lines, DividerStyle.Render(strings.Repeat("─", contentWidth)))

	// 프롬프트 (줄바꿈 적용)
	lines = append(lines, SheepNameStyle.Render(i18n.T().PromptLabel))
	promptLines := wrapText(task.Prompt, contentWidth-2)
	for _, pl := range promptLines {
		lines = append(lines, "  "+pl)
	}

	lines = append(lines, DividerStyle.Render(strings.Repeat("─", contentWidth)))

	// 출력 (줄바꿈 적용)
	lines = append(lines, SheepNameStyle.Render(i18n.T().OutputLabel))
	if len(task.Output) > 0 {
		for _, line := range task.Output {
			// 각 출력 라인을 너비에 맞게 줄바꿈
			wrappedLines := wrapText(line, contentWidth-2)
			for _, wl := range wrappedLines {
				lines = append(lines, "  "+wl)
			}
		}
	} else {
		lines = append(lines, StatusBarStyle.Render("  "+i18n.T().NoOutput))
	}

	lines = append(lines, "")
	lines = append(lines, StatusBarStyle.Render(i18n.T().TaskDetailKeys))

	// 전체 내용을 viewport에 설정
	content := lipgloss.JoinVertical(lipgloss.Left, lines...)

	// viewport 크기 조정
	if m.taskDetailViewport.Width != viewportWidth || m.taskDetailViewport.Height != viewportHeight {
		m.taskDetailViewport.Width = viewportWidth
		m.taskDetailViewport.Height = viewportHeight
	}

	m.taskDetailViewport.SetContent(content)
	m.taskDetailViewport.GotoTop()
}

// wrapText 텍스트를 지정된 너비로 줄바꿈
func wrapText(text string, width int) []string {
	if width <= 0 {
		width = 80
	}

	var result []string
	// 먼저 기존 줄바꿈을 처리
	existingLines := strings.Split(text, "\n")

	for _, line := range existingLines {
		if line == "" {
			result = append(result, "")
			continue
		}

		// lipgloss.Width를 사용하여 실제 표시 너비 계산
		lineWidth := lipgloss.Width(line)
		if lineWidth <= width {
			result = append(result, line)
			continue
		}

		// 너비를 초과하면 줄바꿈
		var currentLine strings.Builder
		currentWidth := 0
		runes := []rune(line)

		for _, r := range runes {
			runeWidth := lipgloss.Width(string(r))
			if currentWidth+runeWidth > width && currentWidth > 0 {
				result = append(result, currentLine.String())
				currentLine.Reset()
				currentWidth = 0
			}
			currentLine.WriteRune(r)
			currentWidth += runeWidth
		}

		if currentLine.Len() > 0 {
			result = append(result, currentLine.String())
		}
	}

	return result
}

// selectCurrentTask 현재 선택된 작업 상세 보기
func (m *Model) selectCurrentTask() {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()

	idx := m.shared.selectedIdx

	var s *SheepState
	var ok bool
	if idx == -1 {
		// 목자 선택
		s, ok = m.shared.sheep[ShepherdName]
	} else if idx >= 0 && idx < len(m.shared.sheepList) {
		name := m.shared.sheepList[idx]
		s, ok = m.shared.sheep[name]
	}

	if !ok || s == nil || len(s.TaskHistory) == 0 {
		return
	}

	// selectedTaskIdx를 실제 TaskHistory 인덱스로 변환
	// (목록은 역순으로 표시되므로)
	actualIdx := len(s.TaskHistory) - 1 - m.shared.selectedTaskIdx
	if actualIdx >= 0 && actualIdx < len(s.TaskHistory) {
		taskID := s.TaskHistory[actualIdx].DBTaskID
		m.shared.viewingTaskID = taskID
		// viewport 내용 업데이트
		m.updateTaskDetailViewport(s, taskID)
	}
}

// AddSheep 양 추가
func (m *Model) AddSheep(info SheepInfo) {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()

	if _, ok := m.shared.sheep[info.Name]; !ok {
		m.shared.sheepList = append(m.shared.sheepList, info.Name)
		vp := viewport.New(m.getViewportWidthLocked(), m.getViewportHeightLocked())
		state := &SheepState{
			Name:        info.Name,
			Project:     info.ProjectName,
			ProjectPath: info.ProjectPath,
			SessionID:   info.SessionID,
			Status:      StatusIdle,
			Output:      []string{},
			Viewport:    vp,
		}
		// DB에서 작업 히스토리 로드
		if history, err := loadTaskHistoryFromDB(info.Name); err == nil {
			state.TaskHistory = history
		}
		m.shared.sheep[info.Name] = state
	}
}

// AppendOutput 출력 추가
func (m *Model) AppendOutput(sheepName, text string) {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()

	if s, ok := m.shared.sheep[sheepName]; ok {
		s.Output = append(s.Output, text)
	}
}

// GetSelectedSheep 현재 선택된 양 정보 반환 (deprecated: use SharedState.GetSelectedSheep)
func (m *Model) GetSelectedSheep() *SheepState {
	return m.shared.GetSelectedSheep()
}

// SetStatus 상태 설정
func (m *Model) SetStatus(sheepName string, status SheepStatus) {
	m.shared.mu.Lock()
	defer m.shared.mu.Unlock()

	if s, ok := m.shared.sheep[sheepName]; ok {
		s.Status = status
	}
}

// getMemoriesPath 기억 저장 경로
func getMemoriesPath() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".shepherd", "memories.md")
}

// loadMemories 저장된 기억 로드
func (m Model) loadMemories() []string {
	path := getMemoriesPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var memories []string
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			memories = append(memories, strings.TrimPrefix(line, "- "))
		}
	}
	return memories
}

// SaveMemory 기억 저장
func SaveMemory(memory string) error {
	path := getMemoriesPath()

	// 디렉토리 생성
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// 기존 내용 읽기
	var content string
	data, err := os.ReadFile(path)
	if err == nil {
		content = string(data)
	} else {
		content = i18n.T().MemoryFileHeader
	}

	// 새 기억 추가
	content += fmt.Sprintf("- %s\n", memory)

	return os.WriteFile(path, []byte(content), 0644)
}

// GetMemoriesContent 저장된 기억 전체 내용 반환
func GetMemoriesContent() string {
	path := getMemoriesPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// truncateToWidth 문자열을 지정된 너비로 자름 (유니코드 고려)
func truncateToWidth(s string, maxWidth int) string {
	if lipgloss.Width(s) <= maxWidth {
		return s
	}

	// 한 글자씩 추가하면서 너비 체크
	result := ""
	for _, r := range s {
		test := result + string(r)
		if lipgloss.Width(test) > maxWidth {
			break
		}
		result = test
	}
	return result
}

// renderSettingsView renders the settings panel
func (m Model) renderSettingsView() string {
	t := i18n.T()

	panelWidth := 56
	if m.width-4 < panelWidth {
		panelWidth = m.width - 4
	}
	innerWidth := panelWidth - 4 // padding

	// Title
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(ColorPrimary).
		Render(t.SettingsTitle)

	divider := lipgloss.NewStyle().
		Foreground(ColorBorder).
		Render(strings.Repeat("━", innerWidth))

	// Settings items
	type settingItem struct {
		label string
		value string
	}

	// Get current values
	lang := config.GetString("language")
	langDisplay := "English"
	if lang != "en" {
		langDisplay = "한국어"
	}

	provider := config.GetString("default_provider")
	providerDisplay := "Claude"
	switch provider {
	case "opencode":
		providerDisplay = "OpenCode"
	case "auto":
		providerDisplay = "Auto"
	}

	workspace := config.GetString("workspace_path")
	if workspace == "" {
		workspace = t.SettingsNotSet
	}

	items := []settingItem{
		{t.SettingsLanguage, langDisplay},
		{t.SettingsProvider, providerDisplay},
		{t.SettingsWorkspace, workspace},
	}

	// Render items
	var rows []string
	labelWidth := 20
	for idx, item := range items {
		marker := "  "
		labelStyle := lipgloss.NewStyle().Foreground(ColorMuted)
		valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB"))

		if idx == m.settingsIdx {
			marker = "▸ "
			labelStyle = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
			valueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF")).Bold(true)
		}

		label := labelStyle.Render(item.label)
		// Pad label to fixed width
		labelVisualWidth := lipgloss.Width(label)
		padding := ""
		if labelVisualWidth < labelWidth {
			padding = strings.Repeat(" ", labelWidth-labelVisualWidth)
		}

		value := item.value
		// If editing workspace, show text input
		if idx == 2 && m.settingsEditing {
			value = m.settingsInput.View()
		}

		row := marker + label + padding + valueStyle.Render(value)
		rows = append(rows, row)
	}

	// Help text
	help := lipgloss.NewStyle().
		Foreground(ColorMuted).
		Render(t.SettingsKeys)

	// Compose panel content
	content := lipgloss.JoinVertical(lipgloss.Left,
		"",
		title,
		divider,
		"",
		strings.Join(rows, "\n"),
		"",
		divider,
		help,
		"",
	)

	// Box style
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 2).
		Width(panelWidth).
		Render(content)

	// Center vertically
	panelHeight := lipgloss.Height(panel)
	contentHeight := m.height - 4 // header + input
	topPadding := (contentHeight - panelHeight) / 2
	if topPadding < 0 {
		topPadding = 0
	}

	// Center horizontally
	panelVisualWidth := lipgloss.Width(panel)
	leftPadding := (m.width - panelVisualWidth) / 2
	if leftPadding < 0 {
		leftPadding = 0
	}

	var lines []string
	for i := 0; i < topPadding; i++ {
		lines = append(lines, "")
	}
	for _, line := range strings.Split(panel, "\n") {
		lines = append(lines, strings.Repeat(" ", leftPadding)+line)
	}

	// Fill to content height
	for len(lines) < contentHeight {
		lines = append(lines, "")
	}

	return strings.Join(lines, "\n")
}
