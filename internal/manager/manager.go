package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/sheep"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/worker"
)


const (
	// AnalysisTimeout is the timeout for task analysis
	AnalysisTimeout = 60 * time.Second
)

// getManagerProvider returns the manager sheep's configured provider.
// Defaults to "claude" if manager doesn't exist yet.
func getManagerProvider() string {
	mgr, err := worker.GetOrCreateManager()
	if err != nil {
		return "claude"
	}
	return string(mgr.Provider)
}

// JSON Schema for structured output
const decisionSchema = `{"type":"object","properties":{"project":{"type":"string","description":"Selected project name"},"sheep":{"type":"string","description":"Selected sheep name"},"reason":{"type":"string","description":"Reason for selection"}},"required":["project","sheep","reason"]}`

// JSON Schema for intent classification
const intentSchema = `{"type":"object","properties":{"intent":{"type":"string","enum":["register_project","delete_project","delete_and_register","list_projects","shepherd_command","coding_task","question","other"],"description":"Classified user intent"},"project_names":{"type":"array","items":{"type":"string"},"description":"Project/folder names to delete"},"register_names":{"type":"array","items":{"type":"string"},"description":"Project/folder names to register (empty means all subdirectories)"},"git_url":{"type":"string","description":"Git clone URL (git@... or https://...git format)"},"reason":{"type":"string","description":"Reason for classification"}},"required":["intent","reason"]}`

// Intent represents the classified user intent.
type Intent struct {
	Type          string   // register_project, delete_project, delete_and_register, list_projects, shepherd_command, coding_task, question, other
	ProjectNames  []string // Project/folder names to delete
	RegisterNames []string // Project/folder names to register (empty means all subdirectories)
	GitURL        string   // Git clone URL
	Reason        string
}

// Decision represents the manager's decision on task assignment.
type Decision struct {
	ProjectName string // Project to assign
	SheepName   string // Sheep (worker) to assign
	Reason      string // Reason for assignment
}

// claudeOutput represents the JSON output from Claude Code CLI.
type claudeOutput struct {
	Type             string          `json:"type"`
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	SessionID        string          `json:"session_id"`
}

// decisionOutput represents the structured output for decision.
type decisionOutput struct {
	Project string `json:"project"`
	Sheep   string `json:"sheep"`
	Reason  string `json:"reason"`
}

// ClassifyIntent classifies the user's intent using Claude Code CLI.
// Falls back to Vibe if Claude hits rate limit.
func ClassifyIntent(prompt string, currentDir string) (*Intent, error) {
	classifyPrompt := fmt.Sprintf(`Classify the user's request. This is an AI coding orchestration CLI tool called 'shepherd'.

Current directory: %s

Classification criteria:
- register_project: Requests to register projects/folders. Includes creating folders (mkdir) then registering, or git clone then registering.
  e.g. "register project", "create folder and register", "register subdirectories"
  e.g. "checkout git@github.com:user/repo.git and register"
- delete_project: Requests to only delete projects
  e.g. "delete project", "remove code project"
- delete_and_register: Requests to delete and register together
  e.g. "delete code and register subdirectories"
- list_projects: Requests to list projects
  e.g. "what projects are there?", "show me the list"
- coding_task: Requests to perform actual work on a specific project - writing/modifying/analyzing/building/deploying/testing/reviewing code
  e.g. "add login feature", "fix bug", "create API endpoint", "write test code"
  e.g. "review shepherd code", "check task history", "deploy it", "build and test"
  e.g. "add feature to shepherd", "write documentation", "analyze this code"
  e.g. "update issue status", "list issues", "check BORI-123"
  e.g. "verify opencode integration", "check k8s status"
- shepherd_command: Commands to control shepherd CLI itself (creating/deleting sheep, changing providers, etc.)
  e.g. "create sheep", "make 3 sheep", "change provider", "run TUI"
- question: Questions about how to use shepherd
  e.g. "how do I use shepherd?", "what commands are there?"
- other: Anything that doesn't fit the above

Key decision criteria:
1. Requests asking to perform/execute/do something are mostly coding_task
2. Not just code changes - analysis, review, verification, queries, deploy, build, test are also coding_task
3. coding_task is any work that gets assigned to a sheep (worker) for execution
4. shepherd_command is ONLY for management commands of the shepherd tool itself
5. When ambiguous, classify as coding_task (safer to send to a sheep for processing)

project_names: Project names to delete
register_names: Folder names to register (empty means all subdirectories)
git_url: Git clone URL (only if git@... or https://...git format is present)

User request: %s`, currentDir, prompt)

	// Check manager's provider
	provider := getManagerProvider()
	output, err := runClassifyCLI(provider, classifyPrompt)
	if err != nil {
		return nil, err
	}

	if output.IsError {
		return nil, fmt.Errorf("CLI error: %s", output.Result)
	}

	var intentOutput struct {
		Intent        string   `json:"intent"`
		ProjectNames  []string `json:"project_names"`
		RegisterNames []string `json:"register_names"`
		GitURL        string   `json:"git_url"`
		Reason        string   `json:"reason"`
	}
	if err := json.Unmarshal(output.StructuredOutput, &intentOutput); err != nil {
		return nil, fmt.Errorf("failed to parse intent: %w", err)
	}

	return &Intent{
		Type:          intentOutput.Intent,
		ProjectNames:  intentOutput.ProjectNames,
		RegisterNames: intentOutput.RegisterNames,
		GitURL:        intentOutput.GitURL,
		Reason:        intentOutput.Reason,
	}, nil
}

// runClassifyCLI runs classification with specified CLI (claude or local)
func runClassifyCLI(cli, prompt string) (*claudeOutput, error) {
	if cli == "opencode" {
		return runWithOpenCode(prompt, intentSchema, 30*time.Second)
	}
	return runWithClaude(prompt, intentSchema, 30*time.Second)
}

// runWithClaude runs analysis with Claude CLI
func runWithClaude(prompt, schema string, timeout time.Duration) (*claudeOutput, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude",
		"--print",
		"--output-format", "json",
		"--json-schema", schema,
	)
	cmd.Stdin = strings.NewReader(prompt)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("Claude timeout")
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return nil, fmt.Errorf("Claude execution failed: %s", errMsg)
		}
		return nil, fmt.Errorf("Claude execution failed: %w", err)
	}

	var output claudeOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		return nil, fmt.Errorf("failed to parse output: %w", err)
	}

	return &output, nil
}

// runWithOpenCode runs analysis with OpenCode CLI (local LLM)
func runWithOpenCode(prompt, schema string, timeout time.Duration) (*claudeOutput, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// OpenCode doesn't support --json-schema, so include JSON format instructions in the prompt
	enhancedPrompt := fmt.Sprintf(`%s

You MUST output only JSON matching the schema below. No other text, only JSON:
%s`, prompt, schema)

	args := []string{"run", "--format", "json"}
	args = append(args, enhancedPrompt)

	cmd := exec.CommandContext(ctx, config.GetOpenCodeBinary(), args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("OpenCode timeout")
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return nil, fmt.Errorf("OpenCode execution failed: %s", errMsg)
		}
		return nil, fmt.Errorf("OpenCode execution failed: %w", err)
	}

	// Extract JSON from OpenCode output
	return parseOpenCodeOutput(stdout.Bytes())
}

// parseOpenCodeOutput parses OpenCode JSON event stream and extracts structured output
func parseOpenCodeOutput(data []byte) (*claudeOutput, error) {
	lines := strings.Split(string(data), "\n")
	var lastText string
	var sessionID string

	for _, line := range lines {
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var event struct {
			Type      string `json:"type"`
			SessionID string `json:"sessionID"`
			Part      struct {
				Text string `json:"text"`
			} `json:"part"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.SessionID != "" {
			sessionID = event.SessionID
		}
		if event.Part.Text != "" {
			lastText += event.Part.Text
		} else if event.Text != "" {
			lastText += event.Text
		}
	}

	if lastText == "" {
		return nil, fmt.Errorf("OpenCode output is empty")
	}

	// Extract JSON from text
	jsonStr := extractJSON(lastText)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON found in OpenCode response: %s", lastText)
	}

	return &claudeOutput{
		Type:             "result",
		Result:           lastText,
		StructuredOutput: json.RawMessage(jsonStr),
		SessionID:        sessionID,
	}, nil
}

// extractJSON extracts a JSON object from text (handles markdown code blocks)
func extractJSON(text string) string {
	// First check ```json ... ``` blocks
	if idx := strings.Index(text, "```json"); idx != -1 {
		start := idx + 7
		end := strings.Index(text[start:], "```")
		if end != -1 {
			return strings.TrimSpace(text[start : start+end])
		}
	}
	// Check ``` ... ``` blocks
	if idx := strings.Index(text, "```"); idx != -1 {
		start := idx + 3
		// Start after newline
		if nl := strings.Index(text[start:], "\n"); nl != -1 {
			start += nl + 1
		}
		end := strings.Index(text[start:], "```")
		if end != -1 {
			candidate := strings.TrimSpace(text[start : start+end])
			if strings.HasPrefix(candidate, "{") {
				return candidate
			}
		}
	}
	// Find raw { }
	start := strings.Index(text, "{")
	if start == -1 {
		return ""
	}
	// Count nested braces
	depth := 0
	for i := start; i < len(text); i++ {
		if text[i] == '{' {
			depth++
		} else if text[i] == '}' {
			depth--
			if depth == 0 {
				return text[start : i+1]
			}
		}
	}
	return ""
}

// Analyze analyzes a user prompt and decides which project and sheep to assign.
func Analyze(prompt string) (*Decision, error) {
	// Collect current state
	projects, err := project.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list projects: %w", err)
	}

	sheepList, err := worker.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list sheep: %w", err)
	}

	if len(projects) == 0 {
		return nil, fmt.Errorf("no projects registered")
	}

	if len(sheepList) == 0 {
		return nil, fmt.Errorf("no sheep created")
	}

	// Build project info string
	var projectInfo strings.Builder
	for _, p := range projects {
		projectInfo.WriteString(fmt.Sprintf("- %s: %s", p.Name, p.Path))
		if p.Description != "" {
			projectInfo.WriteString(fmt.Sprintf(" (%s)", p.Description))
		}
		// Assigned sheep info
		sheepEdge, _ := p.Edges.SheepOrErr()
		if sheepEdge != nil {
			projectInfo.WriteString(fmt.Sprintf(" [assigned: %s]", sheepEdge.Name))
		}
		projectInfo.WriteString("\n")
	}

	// Build sheep status string
	var sheepInfo strings.Builder
	for _, s := range sheepList {
		status := worker.StatusToKorean(s.Status)
		sheepInfo.WriteString(fmt.Sprintf("- %s: %s", s.Name, status))
		proj, _ := s.Edges.ProjectOrErr()
		if proj != nil {
			sheepInfo.WriteString(fmt.Sprintf(" (assigned project: %s)", proj.Name))
		}
		sheepInfo.WriteString("\n")
	}

	// Call Claude CLI
	decision, err := callClaudeCLI(prompt, projectInfo.String(), sheepInfo.String())
	if err != nil {
		return nil, err
	}

	// Validate decision
	if err := validateDecision(decision, projects, sheepList); err != nil {
		return nil, err
	}

	return decision, nil
}

// callClaudeCLI calls Claude Code CLI for task analysis.
func callClaudeCLI(userPrompt, projectInfo, sheepInfo string) (*Decision, error) {
	// Build analysis prompt
	analysisPrompt := fmt.Sprintf(`You are the shepherd (manager). Analyze the user's request and assign the appropriate project and sheep (worker).

Projects:
%s
Sheep status:
%s
Assignment rules:
1. If the request mentions a project name/keyword, select that project
2. If the project has an assigned sheep, use that sheep
3. If no assigned sheep, select an idle sheep
4. If all sheep are working, select the one likely to become idle first
5. If the project cannot be determined, select the most relevant project based on the request

User request: %s

Decide project, sheep, and reason as JSON.`, projectInfo, sheepInfo, userPrompt)

	// Check manager's provider
	provider := getManagerProvider()
	output, err := runAnalysisCLI(provider, analysisPrompt)
	if err != nil {
		return nil, err
	}

	if output.IsError {
		return nil, fmt.Errorf("CLI error: %s", output.Result)
	}

	// Extract decision from structured_output
	if len(output.StructuredOutput) == 0 {
		return nil, fmt.Errorf("no structured output received")
	}

	var decision decisionOutput
	if err := json.Unmarshal(output.StructuredOutput, &decision); err != nil {
		return nil, fmt.Errorf("failed to parse decision: %w", err)
	}

	return &Decision{
		ProjectName: decision.Project,
		SheepName:   decision.Sheep,
		Reason:      decision.Reason,
	}, nil
}

// runAnalysisCLI runs analysis with specified CLI (claude or local)
func runAnalysisCLI(cli, prompt string) (*claudeOutput, error) {
	if cli == "opencode" {
		return runWithOpenCode(prompt, decisionSchema, AnalysisTimeout)
	}
	return runWithClaude(prompt, decisionSchema, AnalysisTimeout)
}


// validateDecision validates that the decision references existing entities.
func validateDecision(d *Decision, projects []*ent.Project, sheepList []*ent.Sheep) error {
	// Check project exists
	projectExists := false
	for _, p := range projects {
		if p.Name == d.ProjectName {
			projectExists = true
			break
		}
	}
	if !projectExists {
		return fmt.Errorf("project '%s' does not exist", d.ProjectName)
	}

	// Check sheep exists
	sheepExists := false
	for _, s := range sheepList {
		if s.Name == d.SheepName {
			sheepExists = true
			break
		}
	}
	if !sheepExists {
		return fmt.Errorf("sheep '%s' does not exist", d.SheepName)
	}

	return nil
}

// AutoAssign automatically assigns the best available sheep for a project.
// Returns the sheep name. If the project already has an assigned sheep, returns that.
func AutoAssign(projectName string) (string, error) {
	// Look up the project
	proj, err := project.Get(projectName)
	if err != nil {
		return "", err
	}

	// Return already-assigned sheep
	assignedSheep, _ := proj.Edges.SheepOrErr()
	if assignedSheep != nil {
		return assignedSheep.Name, nil
	}

	// Find an idle sheep
	sheepList, err := worker.List()
	if err != nil {
		return "", err
	}

	for _, s := range sheepList {
		if s.Status == sheep.StatusIdle {
			// Sheep not assigned to another project
			proj, _ := s.Edges.ProjectOrErr()
			if proj == nil {
				// Assign this sheep to the project
				if err := project.AssignSheep(projectName, s.Name); err != nil {
					return "", err
				}
				return s.Name, nil
			}
		}
	}

	return "", fmt.Errorf("no available sheep (all working or assigned to other projects)")
}
