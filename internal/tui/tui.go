package tui

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	entProject "github.com/agurrrrr/shepherd/ent/project"
	entSheep "github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/apiclient"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/daemon"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/manager"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/worker"
)

//go:embed skills/*.md
var skillsFS embed.FS

// TUI main TUI struct
type TUI struct {
	model     *Model
	shared    *SharedState
	program            *tea.Program
	processor          *queue.Processor
	mu                 sync.Mutex
	shepherdSessionID  string // Shepherd session ID (for context persistence)
}

// New creates a new TUI
func New() *TUI {
	m := NewModel()
	return &TUI{
		model:  &m,
		shared: m.GetSharedState(),
	}
}

// Run starts the TUI in the appropriate mode.
// If the daemon is running, connects as a client. Otherwise runs standalone.
func Run() error {
	if daemon.IsRunning() {
		return RunAsClient()
	}
	return RunStandalone()
}

// RunStandalone runs the TUI with direct DB access and local Processor (original mode).
func RunStandalone() error {
	t := New()

	// Initialize DB
	if err := db.Init(); err != nil {
		return fmt.Errorf("DB init failed: %w", err)
	}
	defer db.Close()

	// Load sheep list
	sheep, err := loadSheepList()
	if err != nil {
		return fmt.Errorf("failed to load sheep list: %w", err)
	}

	for _, s := range sheep {
		t.model.AddSheep(s)
	}

	// Set command callback
	t.model.SetOnCommand(func(cmd string) {
		go t.handleCommand(cmd)
	})

	// Start task queue processor
	t.processor = queue.NewProcessor(2 * time.Second)
	t.processor.OnTaskStart = func(taskID int, sheepName, projectName, prompt string) {
		t.sendOutput(sheepName, fmt.Sprintf("🚀 Task #%d started: %s", taskID, prompt))
	}
	t.processor.OnTaskComplete = func(taskID int, sheepName, projectName, summary string) {
		t.sendOutput(sheepName, fmt.Sprintf("✅ Task #%d completed", taskID))
		if summary != "" {
			t.sendOutput(sheepName, summary)
		}
	}
	t.processor.OnTaskFail = func(taskID int, sheepName, projectName, errMsg string) {
		t.sendOutput(sheepName, fmt.Sprintf("❌ Task #%d failed: %s", taskID, errMsg))
	}
	// Output streaming callback (route output to the sheep assigned to the project)
	t.processor.OnOutput = func(sheepName, projectName, text string) {
		// Find sheep assigned to the project
		targetSheep := t.findSheepByProject(projectName)
		if targetSheep != "" {
			t.sendOutput(targetSheep, text)
		} else {
			// If no sheep is assigned to the project, output to the original sheep
			t.sendOutput(sheepName, text)
		}
	}
	// Status change callback (update TUI emoji)
	t.processor.OnStatusChange = func(sheepName, status string) {
		var s SheepStatus
		switch status {
		case "working":
			s = StatusWorking
		case "error":
			s = StatusError
		default:
			s = StatusIdle
		}
		t.program.Send(SheepStatusMsg{SheepName: sheepName, Status: s})
	}
	t.processor.Start()
	defer t.processor.Stop()

	// Create and run the program
	t.program = tea.NewProgram(
		*t.model,
		tea.WithAltScreen(),
	)

	_, err = t.program.Run()
	return err
}

// RunAsClient runs the TUI as a client connected to the daemon via REST API + SSE.
// model.go rendering code is NOT changed — SSE events are converted to the same
// Bubbletea message types (SheepOutputMsg, SheepStatusMsg, etc.).
func RunAsClient() error {
	t := New()

	client := apiclient.New()

	// Auto-login if auth is configured
	username := config.GetString("auth_username")
	if username != "" {
		// In client mode, try connecting without auth first (local access)
		// The middleware skips auth if jwt_secret is empty
	}

	// Load sheep list from daemon API
	sheepList, err := client.ListSheep()
	if err != nil {
		return fmt.Errorf("failed to load sheep from daemon: %w", err)
	}

	for _, s := range sheepList {
		t.model.AddSheep(SheepInfo{
			Name:        s.Name,
			ProjectName: s.Project,
			Provider:    s.Provider,
		})
	}

	// Set initial status for working sheep
	for _, s := range sheepList {
		if s.Status == "working" {
			// Will be applied after program starts
			defer func(name string) {
				t.program.Send(SheepStatusMsg{SheepName: name, Status: StatusWorking})
			}(s.Name)
		}
	}

	// Command handler: route through API instead of local DB
	t.model.SetOnCommand(func(cmd string) {
		go t.handleCommandAsClient(client, cmd)
	})

	// Connect to SSE event stream
	go t.listenSSE(client)

	// Show client mode indicator
	t.sendOutput("System", "🔗 Connected to Shepherd daemon (client mode)")

	// Create and run the program
	t.program = tea.NewProgram(
		*t.model,
		tea.WithAltScreen(),
	)

	_, err = t.program.Run()
	return err
}

// handleCommandAsClient handles commands in client mode (via API).
func (t *TUI) handleCommandAsClient(client *apiclient.Client, cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}

	// Local-only commands
	switch strings.ToLower(cmd) {
	case "quit", "exit", "q":
		t.program.Send(tea.Quit())
		return
	case "help", "?":
		t.sendOutput("System", "Available commands: quit, help, or give task instructions in natural language")
		t.sendOutput("System", "🔗 Running in client mode (connected to daemon)")
		return
	case "refresh":
		t.refreshSheepListFromAPI(client)
		return
	}

	// Check if a specific project is selected
	selected := t.shared.GetSelectedSheep()
	if selected != nil && selected.Project != "" && !t.shared.IsShepherdSelected() {
		// Direct task creation for the selected project
		result, err := client.CreateTask(cmd, selected.Name, selected.Project)
		if err != nil {
			t.sendOutput("System", "❌ "+err.Error())
			return
		}
		t.sendOutput(selected.Name, fmt.Sprintf("📋 Task #%d registered", result.TaskID))
		return
	}

	// Natural language command → API analyzes and routes
	result, err := client.PostCommand(cmd)
	if err != nil {
		t.sendOutput("System", "❌ "+err.Error())
		return
	}

	t.sendOutput("System", fmt.Sprintf("📋 Task #%d created → %s (%s)",
		result.TaskID, result.SheepName, result.ProjectName))
}

// listenSSE connects to the SSE stream and converts events to Bubbletea messages.
func (t *TUI) listenSSE(client *apiclient.Client) {
	events, err := client.ConnectSSE()
	if err != nil {
		t.sendOutput("System", "❌ SSE connection failed: "+err.Error())
		return
	}

	for event := range events {
		switch event.Type {
		case "output":
			var data struct {
				SheepName   string `json:"sheep_name"`
				ProjectName string `json:"project_name"`
				Text        string `json:"text"`
			}
			if json.Unmarshal(event.Data, &data) != nil {
				continue
			}
			targetSheep := t.findSheepByProject(data.ProjectName)
			if targetSheep != "" {
				t.sendOutput(targetSheep, data.Text)
			} else {
				t.sendOutput(data.SheepName, data.Text)
			}

		case "status_change":
			var data struct {
				SheepName string `json:"sheep_name"`
				Status    string `json:"status"`
			}
			if json.Unmarshal(event.Data, &data) != nil {
				continue
			}
			t.program.Send(SheepStatusMsg{
				SheepName: data.SheepName,
				Status:    mapStatusString(data.Status),
			})

		case "task_start":
			var data struct {
				TaskID      int    `json:"task_id"`
				SheepName   string `json:"sheep_name"`
				ProjectName string `json:"project_name"`
				Prompt      string `json:"prompt"`
			}
			if json.Unmarshal(event.Data, &data) != nil {
				continue
			}
			t.sendOutput(data.SheepName,
				fmt.Sprintf("🚀 Task #%d started: %s", data.TaskID, data.Prompt))

		case "task_complete":
			var data struct {
				TaskID      int    `json:"task_id"`
				SheepName   string `json:"sheep_name"`
				ProjectName string `json:"project_name"`
				Summary     string `json:"summary"`
			}
			if json.Unmarshal(event.Data, &data) != nil {
				continue
			}
			t.sendOutput(data.SheepName,
				fmt.Sprintf("✅ Task #%d completed", data.TaskID))
			if data.Summary != "" {
				t.sendOutput(data.SheepName, data.Summary)
			}

		case "task_fail":
			var data struct {
				TaskID      int    `json:"task_id"`
				SheepName   string `json:"sheep_name"`
				ProjectName string `json:"project_name"`
				Error       string `json:"error"`
			}
			if json.Unmarshal(event.Data, &data) != nil {
				continue
			}
			t.sendOutput(data.SheepName,
				fmt.Sprintf("❌ Task #%d failed: %s", data.TaskID, data.Error))
		}
	}
}

// refreshSheepListFromAPI refreshes sheep list via API.
func (t *TUI) refreshSheepListFromAPI(client *apiclient.Client) {
	sheepList, err := client.ListSheep()
	if err != nil {
		t.sendOutput("System", "❌ Failed to refresh: "+err.Error())
		return
	}

	var infos []SheepInfo
	for _, s := range sheepList {
		infos = append(infos, SheepInfo{
			Name:        s.Name,
			ProjectName: s.Project,
			Provider:    s.Provider,
		})
	}
	t.program.Send(SheepListUpdatedMsg{Sheep: infos})
}

// mapStatusString converts a status string to SheepStatus.
func mapStatusString(s string) SheepStatus {
	switch s {
	case "working":
		return StatusWorking
	case "error":
		return StatusError
	default:
		return StatusIdle
	}
}

// truncateStr truncates a string to maxLen characters.
func truncateStr(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// loadSheepList loads sheep list (only sheep assigned to projects)
func loadSheepList() ([]SheepInfo, error) {
	ctx := context.Background()
	client := db.Client()

	sheepList, err := client.Sheep.Query().
		Where(entSheep.HasProject()).
		WithProject().
		All(ctx)
	if err != nil {
		return nil, err
	}

	var result []SheepInfo
	for _, s := range sheepList {
		info := SheepInfo{
			Name:      s.Name,
			SessionID: s.SessionID,
			Provider:  string(s.Provider),
		}
		if s.Edges.Project != nil {
			info.ProjectName = s.Edges.Project.Name
			info.ProjectPath = s.Edges.Project.Path
		}
		result = append(result, info)
	}

	return result, nil
}

// handleCommand processes commands
func (t *TUI) handleCommand(cmd string) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return
	}

	// Special command handling
	switch strings.ToLower(cmd) {
	case "quit", "exit", "q":
		t.program.Send(tea.Quit())
		return
	case "help", "?":
		t.sendOutput("System", "Available commands: quit, help, stop, or give task instructions in natural language")
		return
	case "refresh":
		t.refreshSheepList()
		return
	case "stop":
		t.stopSelectedTask()
		return
	}

	// Remember command handling
	lowerCmd := strings.ToLower(cmd)
	if strings.HasPrefix(lowerCmd, "remember:") || strings.HasPrefix(lowerCmd, "remember :") {
		memory := strings.TrimSpace(cmd[strings.Index(cmd, ":")+1:])
		if memory != "" {
			if err := SaveMemory(memory); err != nil {
				t.sendError(fmt.Errorf("failed to save memory: %w", err))
			} else {
				t.sendOutput("System", "✅ Remembered: "+memory)
			}
		}
		return
	}

	// Check if shepherd is selected
	if t.shared.IsShepherdSelected() {
		// Forward command to shepherd
		go t.processShepherdCommand(cmd)
		return
	}

	// Process natural language command
	t.processNaturalCommand(cmd)
}

// processNaturalCommand processes natural language commands
func (t *TUI) processNaturalCommand(cmd string) {
	var projectName, sheepName string

	// First check if a project is currently selected (arrow-selected project takes priority)
	selected := t.shared.GetSelectedSheep()
	if selected != nil && selected.Project != "" {
		projectName = selected.Project
		sheepName = selected.Name
	} else {
		// If no project is selected, analyze the command with manager.Analyze
		decision, err := manager.Analyze(cmd)
		if err != nil {
			t.sendError(fmt.Errorf("task analysis failed: %w", err))
			return
		}

		if decision.ProjectName != "" && decision.SheepName != "" {
			projectName = decision.ProjectName

			// Find or create sheep
			var err2 error
			sheepName, err2 = t.findOrCreateSheep(decision.ProjectName, decision.SheepName)
			if err2 != nil {
				t.sendError(err2)
				return
			}
		} else {
			t.sendOutput("System", "Please specify a project. Example: 'add /health endpoint to test-api' or select a project from the sidebar")
			return
		}
	}

	// Add to task queue
	t.addTaskToQueue(sheepName, projectName, cmd)
}

// findOrCreateSheep finds or creates a sheep
func (t *TUI) findOrCreateSheep(projectName, suggestedSheep string) (string, error) {
	ctx := context.Background()
	client := db.Client()

	// Query sheep list
	sheepList, err := client.Sheep.Query().
		WithProject().
		All(ctx)
	if err != nil {
		return "", err
	}

	// Find sheep assigned to this project
	for _, s := range sheepList {
		if s.Edges.Project != nil && s.Edges.Project.Name == projectName {
			return s.Name, nil
		}
	}

	// Check if the suggested sheep is available for assignment
	for _, s := range sheepList {
		if s.Name == suggestedSheep && s.Edges.Project == nil {
			// Attempt assignment
			proj, err := client.Project.Query().
				Where(entProject.Name(projectName)).
				Only(ctx)
			if err != nil {
				return "", err
			}
			_, err = proj.Update().SetSheepID(s.ID).Save(ctx)
			if err != nil {
				return "", err
			}
			return s.Name, nil
		}
	}

	// If no sheep is available, create a new one
	newSheep, err := worker.Create("")
	if err != nil {
		return "", fmt.Errorf("failed to create sheep: %w", err)
	}
	t.sendOutput("System", "🐏 "+newSheep.Name+" created")

	// Assign to project
	proj, err := client.Project.Query().
		Where(entProject.Name(projectName)).
		Only(ctx)
	if err != nil {
		return "", err
	}
	_, err = proj.Update().SetSheepID(newSheep.ID).Save(ctx)
	if err != nil {
		return "", err
	}

	return newSheep.Name, nil
}

// addTaskToQueue adds a task to the queue
func (t *TUI) addTaskToQueue(sheepName, projectName, prompt string) {
	ctx := context.Background()
	client := db.Client()

	// Query sheep
	s, err := client.Sheep.Query().
		Where(entSheep.Name(sheepName)).
		WithProject().
		Only(ctx)
	if err != nil {
		t.sendError(fmt.Errorf("failed to query sheep: %w", err))
		return
	}

	// Project ID
	var projectID int
	if s.Edges.Project != nil {
		projectID = s.Edges.Project.ID
	}

	// Create task
	task, err := queue.CreateTask(prompt, s.ID, projectID)
	if err != nil {
		t.sendError(fmt.Errorf("failed to create task: %w", err))
		return
	}

	// Check pending task count
	pendingCount, _ := queue.CountPendingTasksBySheep(s.ID)

	if pendingCount > 1 {
		t.sendOutput(sheepName, fmt.Sprintf("📋 Task #%d queued (pending: %d)", task.ID, pendingCount))
	} else {
		t.sendOutput(sheepName, fmt.Sprintf("📋 Task #%d registered", task.ID))
	}

	// Try immediate processing (Processor handles on next tick, but for faster response)
	if t.processor != nil {
		t.processor.ProcessPendingNow()
	}
}

// executeTask executes a task (called from Processor)
func (t *TUI) executeTask(sheepName, prompt string) {
	t.program.Send(SheepStatusMsg{
		SheepName: sheepName,
		Status:    StatusWorking,
	})

	// Start message
	t.sendOutput(sheepName, "🚀 Running Claude...")

	// Execution options (use defaults)
	opts := worker.DefaultInteractiveOptions(
		func(text string) {
			// Ignore blank lines or whitespace-only text
			if strings.TrimSpace(text) != "" {
				t.sendOutput(sheepName, text)
			}
		},
		nil, // Input handler (handled separately in TUI)
	)

	// Execute task
	result, err := worker.ExecuteInteractive(sheepName, prompt, opts)
	if err != nil {
		t.program.Send(SheepStatusMsg{
			SheepName: sheepName,
			Status:    StatusError,
			Error:     err,
		})
		t.sendError(err)
		// Send TaskCompleteMsg even on error to save output
		t.program.Send(TaskCompleteMsg{
			SheepName: sheepName,
			Result:    "error: " + err.Error(),
		})
		return
	}

	// Output completion message (modified files only - results already streamed)
	if result != nil && len(result.FilesModified) > 0 {
		t.sendOutput(sheepName, "📁 Modified files: "+strings.Join(result.FilesModified, ", "))
	}

	// Send completion status (triggers DB save)
	var resultStr string
	if result != nil {
		resultStr = result.Result
	}
	t.program.Send(TaskCompleteMsg{
		SheepName: sheepName,
		Result:    resultStr,
	})
}

// sendOutput sends output
func (t *TUI) sendOutput(sheepName, text string) {
	// Send raw text without line breaks (viewport handles its own)
	t.program.Send(SheepOutputMsg{
		SheepName: sheepName,
		Text:      text,
	})
}

// sendError sends an error
func (t *TUI) sendError(err error) {
	t.program.Send(ErrorMsg{Error: err})
}

// processShepherdCommand processes shepherd (manager) commands
func (t *TUI) processShepherdCommand(cmd string) {
	t.sendOutput(ShepherdName, "👤 Shepherd is processing the command...")
	t.sendOutput(ShepherdName, "> "+cmd)

	// Get saved memories
	memories := GetMemoriesContent()

	// Get current directory
	currentDir, _ := os.Getwd()

	// Classify intent
	intent, err := manager.ClassifyIntent(cmd, currentDir)
	if err != nil {
		t.sendError(fmt.Errorf("intent classification failed: %w", err))
		return
	}

	t.sendOutput(ShepherdName, fmt.Sprintf("📋 Classification: %s - %s", intent.Type, intent.Reason))

	// Task recording helper
	var taskID int
	recordTask := func(summary string) {
		mgr, err := worker.GetOrCreateManager()
		if err != nil {
			return
		}
		task, err := queue.CreateManagerTask(cmd, mgr.ID)
		if err != nil {
			return
		}
		taskID = task.ID
		_ = queue.StartTask(taskID)
	}
	completeTask := func(summary string) {
		if taskID > 0 {
			_ = queue.CompleteTask(taskID, summary, nil)
		}
	}

	// Handle based on intent
	switch intent.Type {
	case "register_project":
		recordTask("Register project")
		t.handleRegisterProject(intent, currentDir)
		completeTask("Project registration complete")
	case "delete_project":
		recordTask("Delete project")
		t.handleDeleteProject(intent)
		completeTask("Project deletion complete")
	case "delete_and_register":
		recordTask("Delete and register project")
		t.handleDeleteAndRegister(intent, currentDir)
		completeTask("Delete and register complete")
	case "list_projects":
		recordTask("List projects")
		t.handleListProjects()
		completeTask("Project listing complete")
	case "shepherd_command":
		// Forward shepherd command to Claude
		t.executeShepherdTask(cmd, memories)
	case "coding_task":
		// Coding tasks require selecting a project first
		t.sendOutput(ShepherdName, "⚠️ For coding tasks, please select a project from the sidebar first.")
	default:
		// Forward other commands directly to Claude
		t.executeShepherdTask(cmd, memories)
	}
}

// handleRegisterProject handles project registration
func (t *TUI) handleRegisterProject(intent *manager.Intent, currentDir string) {
	// If git URL exists, clone first
	if intent.GitURL != "" {
		repoName := extractRepoName(intent.GitURL)
		clonePath := filepath.Join(currentDir, repoName)

		// Check if already exists
		if _, err := os.Stat(clonePath); err == nil {
			t.sendOutput(ShepherdName, fmt.Sprintf("📁 '%s' folder already exists", repoName))
		} else {
			// Run git clone
			t.sendOutput(ShepherdName, fmt.Sprintf("🔄 git clone %s ...", intent.GitURL))
			cmd := exec.Command("git", "clone", intent.GitURL, clonePath)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.sendOutput(ShepherdName, fmt.Sprintf("❌ git clone failed: %v\n%s", err, string(output)))
				return
			}
			t.sendOutput(ShepherdName, fmt.Sprintf("✅ '%s' clone complete", repoName))
		}

		// Register cloned folder as project
		result := project.AddWithResult(repoName, clonePath, "")
		if result.AssignError != nil && result.Project == nil {
			if strings.Contains(result.AssignError.Error(), "already exists") {
				t.sendOutput(ShepherdName, fmt.Sprintf("📁 '%s' already registered", repoName))
			} else {
				t.sendOutput(ShepherdName, fmt.Sprintf("❌ '%s' registration failed: %v", repoName, result.AssignError))
			}
		} else {
			t.sendOutput(ShepherdName, fmt.Sprintf("📁 '%s' registered (%s)", repoName, clonePath))
			if result.AssignedSheep != nil {
				if result.SheepCreated {
					t.sendOutput(ShepherdName, fmt.Sprintf("🐏 '%s' created and assigned", result.AssignedSheep.Name))
				} else {
					t.sendOutput(ShepherdName, fmt.Sprintf("🐏 '%s' assigned", result.AssignedSheep.Name))
				}
			}
		}
	} else if len(intent.RegisterNames) > 0 {
		// Register specific folders only
		for _, name := range intent.RegisterNames {
			path := filepath.Join(currentDir, name)
			result := project.AddWithResult(name, path, "")
			if result.AssignError != nil && result.Project == nil {
				t.sendOutput(ShepherdName, fmt.Sprintf("❌ %s registration failed: %v", name, result.AssignError))
			} else {
				t.sendOutput(ShepherdName, fmt.Sprintf("✅ %s registered", name))
				if result.AssignedSheep != nil {
					if result.SheepCreated {
						t.sendOutput(ShepherdName, fmt.Sprintf("🐏 '%s' created and assigned", result.AssignedSheep.Name))
					} else {
						t.sendOutput(ShepherdName, fmt.Sprintf("🐏 '%s' assigned", result.AssignedSheep.Name))
					}
				}
			}
		}
	} else {
		// Register all subdirectories
		entries, err := os.ReadDir(currentDir)
		if err != nil {
			t.sendError(err)
			return
		}
		for _, entry := range entries {
			if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
				name := entry.Name()
				path := filepath.Join(currentDir, name)
				result := project.AddWithResult(name, path, "")
				if result.AssignError != nil && result.Project == nil {
					t.sendOutput(ShepherdName, fmt.Sprintf("⚠️ %s: already registered or error", name))
				} else {
					t.sendOutput(ShepherdName, fmt.Sprintf("✅ %s registered", name))
					if result.AssignedSheep != nil {
						if result.SheepCreated {
							t.sendOutput(ShepherdName, fmt.Sprintf("🐏 '%s' created and assigned", result.AssignedSheep.Name))
						} else {
							t.sendOutput(ShepherdName, fmt.Sprintf("🐏 '%s' assigned", result.AssignedSheep.Name))
						}
					}
				}
			}
		}
	}
	t.refreshSheepList()
}

// extractRepoName extracts repository name from git URL
func extractRepoName(gitURL string) string {
	// Remove .git suffix
	url := strings.TrimSuffix(gitURL, ".git")

	// Extract name after the last / or :
	if idx := strings.LastIndex(url, "/"); idx != -1 {
		return url[idx+1:]
	}
	if idx := strings.LastIndex(url, ":"); idx != -1 {
		return url[idx+1:]
	}
	return url
}

// handleDeleteProject handles project deletion
func (t *TUI) handleDeleteProject(intent *manager.Intent) {
	for _, name := range intent.ProjectNames {
		if err := project.Remove(name); err != nil {
			t.sendOutput(ShepherdName, fmt.Sprintf("❌ %s deletion failed: %v", name, err))
		} else {
			t.sendOutput(ShepherdName, fmt.Sprintf("✅ %s deleted", name))
		}
	}
	t.refreshSheepList()
}

// handleDeleteAndRegister handles project deletion then registration (sequential to prevent DB lock)
func (t *TUI) handleDeleteAndRegister(intent *manager.Intent, currentDir string) {
	// 1. Delete (without calling refreshSheepList)
	for _, name := range intent.ProjectNames {
		if err := project.Remove(name); err != nil {
			t.sendOutput(ShepherdName, fmt.Sprintf("❌ %s deletion failed: %v", name, err))
		} else {
			t.sendOutput(ShepherdName, fmt.Sprintf("✅ %s deleted", name))
		}
	}

	// 2. Register
	t.handleRegisterProject(intent, currentDir)
}

// handleListProjects displays project list
func (t *TUI) handleListProjects() {
	projects, err := project.List()
	if err != nil {
		t.sendError(err)
		return
	}

	if len(projects) == 0 {
		t.sendOutput(ShepherdName, "No registered projects.")
		return
	}

	t.sendOutput(ShepherdName, fmt.Sprintf("📋 Registered projects (%d):", len(projects)))
	for _, p := range projects {
		sheepName := "-"
		if s, _ := p.Edges.SheepOrErr(); s != nil {
			sheepName = s.Name
		}
		t.sendOutput(ShepherdName, fmt.Sprintf("  • %s (%s) [%s]", p.Name, p.Path, sheepName))
	}
}

// executeShepherdTask executes a shepherd task (Claude CLI)
func (t *TUI) executeShepherdTask(userCmd, memories string) {
	t.sendOutput(ShepherdName, "🔧 Requesting Claude...")

	// Current directory
	currentDir, _ := os.Getwd()

	// Load skills
	skills := loadSkills()

	// Compose prompt
	prompt := fmt.Sprintf(`You are the manager of the shepherd tool.
Process the user's request using the shepherd CLI or MCP tools.
For commands starting with slash (/), follow the corresponding skill instructions.

## Current directory
%s

%s
`, currentDir, skills)

	// Add memories
	if memories != "" {
		prompt += fmt.Sprintf(`## Reference memories
%s

`, memories)
		t.sendOutput(ShepherdName, "📝 Referencing saved memories")
	}

	prompt += fmt.Sprintf(`## User request
%s

If it's a slash command, follow the corresponding skill instructions.
Otherwise, use the appropriate CLI command or MCP tool to handle the request.
Briefly report the execution result.`, userCmd)

	// Run Claude
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	args := []string{
		"--print",
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
		"--mcp-config", worker.GetMCPConfigJSON(),
	}

	// Resume if there's a previous session
	if t.shepherdSessionID != "" {
		args = append(args, "--resume", t.shepherdSessionID)
	}

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = currentDir
	cmd.Stdin = strings.NewReader(prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.sendOutput(ShepherdName, "❌ Pipe creation failed: "+err.Error())
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.sendOutput(ShepherdName, "❌ stderr pipe creation failed: "+err.Error())
		return
	}

	if err := cmd.Start(); err != nil {
		t.sendOutput(ShepherdName, "❌ Failed to start Claude: "+err.Error())
		return
	}

	// Stream stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			// Extract session ID
			if sessionID := extractSessionID(line); sessionID != "" {
				t.shepherdSessionID = sessionID
			}
			parsed := parseShepherdStreamLine(line)
			if parsed != "" {
				t.sendOutput(ShepherdName, parsed)
			}
		}
	}()

	// Read stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) != "" {
				t.sendOutput(ShepherdName, "⚠️ "+line)
			}
		}
	}()

	// Wait for completion
	err = cmd.Wait()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			t.sendOutput(ShepherdName, "❌ Timeout")
		} else {
			t.sendOutput(ShepherdName, "❌ Execution error: "+err.Error())
		}
	} else {
		t.sendOutput(ShepherdName, "✅ Done")
	}

	// Refresh sheep list
	t.refreshSheepList()
}

// parseShepherdStreamLine parses Claude stream-json output
func parseShepherdStreamLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}

	// Try JSON parsing
	if strings.HasPrefix(line, "{") {
		var msg struct {
			Type    string `json:"type"`
			Content string `json:"content"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}

		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			switch msg.Type {
			case "assistant":
				// Extract text from assistant message
				for _, c := range msg.Message.Content {
					if c.Type == "text" && c.Text != "" {
						return c.Text
					}
				}
			case "content_block_delta":
				if msg.Content != "" {
					return msg.Content
				}
			}
		}
	}

	return ""
}

// extractSessionID extracts session ID from stream-json output
func extractSessionID(line string) string {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return ""
	}

	var msg struct {
		Type      string `json:"type"`
		SessionID string `json:"session_id"`
	}

	if err := json.Unmarshal([]byte(line), &msg); err == nil {
		if msg.SessionID != "" {
			return msg.SessionID
		}
	}
	return ""
}

// refreshSheepList refreshes the sheep list
func (t *TUI) refreshSheepList() {
	sheep, err := loadSheepList()
	if err != nil {
		t.sendError(err)
		return
	}
	t.program.Send(SheepListUpdatedMsg{Sheep: sheep})
}

// stopSelectedTask stops the selected sheep's task
func (t *TUI) stopSelectedTask() {
	selected := t.shared.GetSelectedSheep()
	if selected == nil {
		t.sendOutput("System", "⚠️ Please select a project first.")
		return
	}

	if selected.Status != StatusWorking {
		t.sendOutput("System", "⚠️ This project is not currently working.")
		return
	}

	result, err := worker.StopTask(selected.Name)
	if err != nil {
		t.sendError(err)
		return
	}

	// Save output of stopped task
	if result != nil && result.TaskID > 0 {
		_ = queue.FailTaskWithOutput(result.TaskID, "Stopped by user", result.OutputLines)
	}

	t.sendOutput(selected.Name, "🛑 Task has been stopped.")
	t.program.Send(SheepStatusMsg{
		SheepName: selected.Name,
		Status:    StatusIdle,
	})
}

// findSheepByProject finds the sheep name assigned to a project
func (t *TUI) findSheepByProject(projectName string) string {
	t.shared.mu.RLock()
	defer t.shared.mu.RUnlock()

	for _, s := range t.shared.sheep {
		if s.Project == projectName {
			return s.Name
		}
	}
	return ""
}

// loadSkills loads skill files and returns them as a prompt string
func loadSkills() string {
	// Read skill files from embed.FS
	entries, err := skillsFS.ReadDir("skills")
	if err != nil {
		return ""
	}

	var skills []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		content, err := skillsFS.ReadFile("skills/" + entry.Name())
		if err != nil {
			continue
		}

		skills = append(skills, string(content))
	}

	if len(skills) == 0 {
		return ""
	}

	return "## Available skills (slash commands)\n\n" + strings.Join(skills, "\n---\n\n")
}
