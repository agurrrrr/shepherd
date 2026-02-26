package main

import (
	cryptoRand "crypto/rand"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/task"
	"github.com/agurrrrr/shepherd/internal/browser"
	"github.com/agurrrrr/shepherd/internal/config"
	"github.com/agurrrrr/shepherd/internal/daemon"
	"github.com/agurrrrr/shepherd/internal/db"
	"github.com/agurrrrr/shepherd/internal/i18n"
	"github.com/agurrrrr/shepherd/internal/manager"
	"github.com/agurrrrr/shepherd/internal/mcp"
	"github.com/agurrrrr/shepherd/internal/names"
	"github.com/agurrrrr/shepherd/internal/project"
	"github.com/agurrrrr/shepherd/internal/queue"
	"github.com/agurrrrr/shepherd/internal/scheduler"
	"github.com/agurrrrr/shepherd/internal/server"
	"github.com/agurrrrr/shepherd/internal/skill"
	"github.com/agurrrrr/shepherd/internal/tui"
	"github.com/agurrrrr/shepherd/internal/envutil"
	"github.com/agurrrrr/shepherd/internal/worker"
	"github.com/chzyer/readline"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	version   = "0.2.0"
	buildTime = "unknown"
)

// Readline instance for chat mode (used in interactive execution)
var chatReadline *readline.Instance

var rootCmd = &cobra.Command{
	Use:   "shepherd [prompt]",
	Short: "AI coding orchestration CLI",
	Long: `shepherd is an AI coding orchestration tool that manages multiple Claude Code sessions.

The shepherd analyzes tasks and assigns them to the appropriate sheep (workers),
with each project having a dedicated worker to carry out the work.

Usage:
  shepherd                           # Interactive mode
  shepherd "Add login feature"       # Single task execution
  shepherd task "Fix the bug"`,
	Args: cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			// No arguments: enter interactive mode
			runChatMode()
			return
		}
		// Arguments provided: treat as a task
		prompt := strings.Join(args, " ")
		executeTask(prompt)
	},
}

// config command
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
	Long:  "View or modify shepherd configuration.",
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Get a configuration value",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		value := config.Get(key)
		if value == nil {
			fmt.Printf("Configuration '%s' not found\n", key)
			return
		}
		fmt.Println(value)
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		key := args[0]
		value := args[1]
		if err := config.Set(key, value); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save configuration: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Configuration '%s' = '%s' saved\n", key, value)
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print configuration file path",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(config.GetConfigPath())
	},
}

// spawn command
var spawnName string
var spawnProvider string

var spawnCmd = &cobra.Command{
	Use:   "spawn",
	Short: "Create a new sheep",
	Long:  "Creates a new sheep (worker). If no name is specified, one is assigned automatically.",
	Run: func(cmd *cobra.Command, args []string) {
		s, err := worker.CreateWithOptions(worker.CreateOptions{
			Name:     spawnName,
			Provider: spawnProvider,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create sheep: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🐏 %s created (provider: %s)\n", s.Name, s.Provider)
	},
}

// flock command
var flockCmd = &cobra.Command{
	Use:   "flock",
	Short: "List all sheep",
	Long:  "Displays a list of all currently created sheep (workers).",
	Run: func(cmd *cobra.Command, args []string) {
		sheepList, err := worker.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list sheep: %v\n", err)
			os.Exit(1)
		}

		if len(sheepList) == 0 {
			fmt.Println("No sheep found. Create one with 'shepherd spawn'.")
			return
		}

		for _, s := range sheepList {
			projectName := ""
			if s.Edges.Project != nil {
				projectName = fmt.Sprintf(" (%s)", s.Edges.Project.Name)
			}
			status := worker.StatusToKorean(s.Status)
			provider := worker.ProviderToKorean(s.Provider)
			fmt.Printf("🐏 %s%s - %s [%s]\n", s.Name, projectName, status, provider)
		}
	},
}

// set-provider command
var setProviderCmd = &cobra.Command{
	Use:   "set-provider <sheep-name> <provider>",
	Short: "Change a sheep's AI provider",
	Long:  "Changes the AI provider for a sheep (worker). (claude, opencode, auto)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		provider := args[1]
		if err := worker.UpdateProvider(name, provider); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to change provider: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🐏 %s's provider changed to %s\n", name, provider)
	},
}

// recall command
var recallAll bool

var recallCmd = &cobra.Command{
	Use:   "recall [name]",
	Short: "Terminate a sheep",
	Long:  "Terminates a sheep (worker). Use the --all flag to terminate all sheep.",
	Run: func(cmd *cobra.Command, args []string) {
		if recallAll {
			count, err := worker.DeleteAll()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to terminate all sheep: %v\n", err)
				os.Exit(1)
			}
			if count == 0 {
				fmt.Println("No sheep to terminate.")
			} else {
				fmt.Printf("🐏 %d sheep terminated\n", count)
			}
			return
		}

		if len(args) == 0 {
			fmt.Fprintln(os.Stderr, "Specify a sheep name or use the --all flag.")
			os.Exit(1)
		}

		name := args[0]
		if err := worker.Delete(name); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to terminate sheep: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🐏 %s terminated\n", name)
	},
}

// names command
var namesCmd = &cobra.Command{
	Use:   "names",
	Short: "Manage sheep name pool",
	Long:  "View, add, or remove names from the sheep name pool.",
}

var namesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List name pool",
	Run: func(cmd *cobra.Command, args []string) {
		nameList, err := names.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list names: %v\n", err)
			os.Exit(1)
		}

		if len(nameList) == 0 {
			fmt.Println("No names registered.")
			return
		}

		fmt.Println("🐏 Sheep name pool:")
		for i, n := range nameList {
			fmt.Printf("  %2d. %s\n", i+1, n.Name)
		}
		fmt.Printf("\nTotal: %d names\n", len(nameList))
	},
}

var namesAddCmd = &cobra.Command{
	Use:   "add <name> [name2] [name3] ...",
	Short: "Add names",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		for _, name := range args {
			if err := names.Add(name); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to add '%s': %v\n", name, err)
			} else {
				fmt.Printf("✅ '%s' added\n", name)
			}
		}
	},
}

var namesRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a name",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		if err := names.Remove(name); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove name: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("🗑️ '%s' removed\n", name)
	},
}

// project command
var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage projects",
	Long:  "Add, remove, or list projects.",
}

var projectAddDesc string

var projectAddCmd = &cobra.Command{
	Use:   "add <name> <path>",
	Short: "Add a project",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		path := args[1]
		p, err := project.Add(name, path, projectAddDesc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to add project: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("📁 %s added (%s)\n", p.Name, p.Path)
	},
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects",
	Run: func(cmd *cobra.Command, args []string) {
		projects, err := project.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list projects: %v\n", err)
			os.Exit(1)
		}

		if len(projects) == 0 {
			fmt.Println("No projects found. Add one with 'shepherd project add'.")
			return
		}

		for _, p := range projects {
			sheepName := "unassigned"
			if p.Edges.Sheep != nil {
				sheepName = p.Edges.Sheep.Name
			}
			fmt.Printf("📁 %s (%s) - %s\n", p.Name, p.Path, sheepName)
		}
	},
}

var projectRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a project",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		if err := project.Remove(name); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove project: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("📁 %s removed\n", name)
	},
}

var projectAssignCmd = &cobra.Command{
	Use:   "assign <project> <sheep>",
	Short: "Assign a sheep to a project",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]
		sheepName := args[1]
		err := project.AssignSheep(projectName, sheepName)
		if err != nil {
			// If UNIQUE constraint fails, unassign existing sheep and retry
			if strings.Contains(err.Error(), "UNIQUE constraint") || strings.Contains(err.Error(), "constraint failed") {
				if unErr := project.UnassignSheep(projectName); unErr != nil {
					fmt.Fprintf(os.Stderr, "Failed to unassign existing sheep: %v\n", unErr)
					os.Exit(1)
				}
				if err = project.AssignSheep(projectName, sheepName); err != nil {
					fmt.Fprintf(os.Stderr, "Failed to assign sheep: %v\n", err)
					os.Exit(1)
				}
			} else {
				fmt.Fprintf(os.Stderr, "Failed to assign sheep: %v\n", err)
				os.Exit(1)
			}
		}
		fmt.Printf("📁 🐏 %s assigned to %s\n", sheepName, projectName)
	},
}

// skill commands
var (
	skillAddFile    string
	skillAddProject string
	skillAddDesc    string
	skillAddTags    string
	skillListProject string
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage skills",
	Long:  "Create, list, show, enable, disable, or remove skills.",
}

var skillListCmd = &cobra.Command{
	Use:   "list",
	Short: "List skills",
	Run: func(cmd *cobra.Command, args []string) {
		var skills []*ent.Skill
		var err error

		if skillListProject != "" {
			skills, err = skill.ListByProject(skillListProject)
		} else {
			skills, err = skill.ListAll()
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list skills: %v\n", err)
			os.Exit(1)
		}

		if len(skills) == 0 {
			fmt.Println("No skills found.")
			return
		}

		fmt.Printf("%-4s %-25s %-8s %-8s %-8s %s\n", "ID", "NAME", "SCOPE", "ENABLED", "BUNDLED", "TAGS")
		fmt.Println(strings.Repeat("-", 80))
		for _, s := range skills {
			enabled := "yes"
			if !s.Enabled {
				enabled = "no"
			}
			bundled := ""
			if s.Bundled {
				bundled = "bundled"
			}
			tags := ""
			if len(s.Tags) > 0 {
				tags = strings.Join(s.Tags, ", ")
			}
			fmt.Printf("%-4d %-25s %-8s %-8s %-8s %s\n", s.ID, s.Name, s.Scope, enabled, bundled, tags)
		}
	},
}

var skillAddCmd = &cobra.Command{
	Use:   "add <name> --file <path>",
	Short: "Add a skill from a markdown file",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]

		if skillAddFile == "" {
			fmt.Fprintf(os.Stderr, "Error: --file flag is required\n")
			os.Exit(1)
		}

		content, err := os.ReadFile(skillAddFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to read file: %v\n", err)
			os.Exit(1)
		}

		fm, body, err := skill.ParseSkillFile(string(content))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to parse skill file: %v\n", err)
			os.Exit(1)
		}

		// CLI args override frontmatter
		description := skillAddDesc
		scope := "global"
		var tags []string
		var projectID *int

		if fm != nil {
			if description == "" {
				description = fm.Description
			}
			if fm.Tags != nil && skillAddTags == "" {
				tags = fm.Tags
			}
			if fm.Scope != "" {
				scope = fm.Scope
			}
		}

		if skillAddTags != "" {
			tags = strings.Split(skillAddTags, ",")
			for i := range tags {
				tags[i] = strings.TrimSpace(tags[i])
			}
		}

		if skillAddProject != "" {
			scope = "project"
			p, err := project.Get(skillAddProject)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to find project '%s': %v\n", skillAddProject, err)
				os.Exit(1)
			}
			pid := p.ID
			projectID = &pid
		}

		if body == "" {
			body = string(content)
		}

		sk, err := skill.CreateSkill(projectID, name, description, body, scope, tags)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create skill: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Skill '%s' created (ID: %d, scope: %s)\n", sk.Name, sk.ID, sk.Scope)
	},
}

var skillRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a skill by name",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		sk, err := findSkillByName(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		if sk.Bundled {
			fmt.Fprintf(os.Stderr, "Cannot remove bundled skill '%s'\n", name)
			os.Exit(1)
		}
		if err := skill.DeleteSkill(sk.ID); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove skill: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Skill '%s' removed\n", name)
	},
}

var skillShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Show skill details",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		sk, err := findSkillByName(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Name:        %s\n", sk.Name)
		fmt.Printf("ID:          %d\n", sk.ID)
		fmt.Printf("Scope:       %s\n", sk.Scope)
		fmt.Printf("Enabled:     %v\n", sk.Enabled)
		fmt.Printf("Bundled:     %v\n", sk.Bundled)
		if sk.Description != "" {
			fmt.Printf("Description: %s\n", sk.Description)
		}
		if len(sk.Tags) > 0 {
			fmt.Printf("Tags:        %s\n", strings.Join(sk.Tags, ", "))
		}
		if sk.Edges.Project != nil {
			fmt.Printf("Project:     %s\n", sk.Edges.Project.Name)
		}
		fmt.Printf("\n--- Content ---\n%s\n", sk.Content)
	},
}

var skillEnableCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Enable a skill",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		sk, err := findSkillByName(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		if _, err := skill.ToggleEnabled(sk.ID, true); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to enable skill: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Skill '%s' enabled\n", name)
	},
}

var skillDisableCmd = &cobra.Command{
	Use:   "disable <name>",
	Short: "Disable a skill",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		sk, err := findSkillByName(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
		if _, err := skill.ToggleEnabled(sk.ID, false); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to disable skill: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Skill '%s' disabled\n", name)
	},
}

// findSkillByName finds a skill by name from all skills.
func findSkillByName(name string) (*ent.Skill, error) {
	skills, err := skill.ListAll()
	if err != nil {
		return nil, fmt.Errorf("failed to list skills: %w", err)
	}
	for _, s := range skills {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, fmt.Errorf("skill '%s' not found", name)
}

// task command
var taskCmd = &cobra.Command{
	Use:   "task [prompt]",
	Short: "Request a task or manage tasks",
	Long: `Requests a task from the shepherd, or manage tasks with subcommands.

Subcommands:
  stop <id>       Stop a running task
  cancel-all      Cancel all running and pending tasks

Examples:
  shepherd task "Fix the bug"
  shepherd task stop 123
  shepherd task cancel-all`,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			cmd.Help()
			return
		}
		prompt := strings.Join(args, " ")
		executeTask(prompt)
	},
}

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Manage task queue",
}

var queueAddCmd = &cobra.Command{
	Use:   "add <project> <prompt>",
	Short: "Add a task to the queue",
	Long:  "Adds a task to the queue for the specified project's sheep. The processor will execute it automatically.",
	Args:  cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]
		prompt := strings.Join(args[1:], " ")

		// Look up project
		proj, err := project.Get(projectName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to find project: %v\n", err)
			os.Exit(1)
		}

		// Check if project has an assigned sheep
		if proj.Edges.Sheep == nil {
			fmt.Fprintf(os.Stderr, "No sheep assigned to project '%s'\n", projectName)
			os.Exit(1)
		}

		sheepID := proj.Edges.Sheep.ID
		sheepName := proj.Edges.Sheep.Name

		// Create task
		t, err := queue.CreateTask(prompt, sheepID, proj.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create task: %v\n", err)
			os.Exit(1)
		}

		pendingCount, _ := queue.CountPendingTasksBySheep(sheepID)
		fmt.Printf("📋 Task #%d added to queue\n", t.ID)
		fmt.Printf("   Project: %s\n", projectName)
		fmt.Printf("   Sheep: %s\n", sheepName)
		fmt.Printf("   Pending: %d\n", pendingCount)
		fmt.Println("   Will be executed automatically in the TUI.")
	},
}

var queueListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pending tasks",
	Run: func(cmd *cobra.Command, args []string) {
		tasks, err := queue.ListTasksByStatus(task.StatusPending)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list tasks: %v\n", err)
			os.Exit(1)
		}

		if len(tasks) == 0 {
			fmt.Println("No pending tasks.")
			return
		}

		fmt.Printf("📋 Pending tasks (%d):\n", len(tasks))
		for _, t := range tasks {
			sheepName := "-"
			projectName := "-"
			if t.Edges.Sheep != nil {
				sheepName = t.Edges.Sheep.Name
			}
			if t.Edges.Project != nil {
				projectName = t.Edges.Project.Name
			}
			prompt := t.Prompt
			if len(prompt) > 50 {
				prompt = prompt[:50] + "..."
			}
			fmt.Printf("  #%d [%s → %s] %s\n", t.ID, sheepName, projectName, prompt)
		}
	},
}

var queueImportIssuesCmd = &cobra.Command{
	Use:   "import-issues <project> <YouTrackProject> [query]",
	Short: "Import issues from issue tracker into the task queue",
	Long: `Fetches issues from YouTrack and adds them to the specified shepherd project's task queue.

Examples:
  shepherd queue import-issues bori BORI "State: Open"
  shepherd queue import-issues bori BORI "State: InProgress"`,
	Args: cobra.MinimumNArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]
		youtrackProject := args[1]
		youtrackQuery := ""
		if len(args) > 2 {
			youtrackQuery = strings.Join(args[2:], " ")
		}

		// Look up project
		proj, err := project.Get(projectName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to find project: %v\n", err)
			os.Exit(1)
		}

		// Check if project has an assigned sheep
		if proj.Edges.Sheep == nil {
			fmt.Fprintf(os.Stderr, "No sheep assigned to project '%s'\n", projectName)
			os.Exit(1)
		}

		sheepID := proj.Edges.Sheep.ID
		sheepName := proj.Edges.Sheep.Name

		fmt.Printf("🔍 Fetching YouTrack issues... (project: %s", youtrackProject)
		if youtrackQuery != "" {
			fmt.Printf(", query: %s", youtrackQuery)
		}
		fmt.Println(")")

		// Fetch issues and create tasks through the shepherd
		prompt := fmt.Sprintf(`Fetch issues from YouTrack and add each one as a task.

1. Use mcp__atsel-mcp__list_issues tool to fetch issues
   - project: %s
   - query: %s
   - max: 10

2. For each fetched issue, use mcp__shepherd__task_start tool to add a task
   - sheep_name: %s
   - project_name: %s
   - prompt: "[IssueID] Issue title\n\nIssue description" format

Report the list of fetched issues and how many tasks were added.`, youtrackProject, youtrackQuery, sheepName, projectName)

		// Execute shepherd (MCP settings are applied automatically inside ExecuteInteractive)
		opts := worker.DefaultInteractiveOptions(
			func(text string) {
				fmt.Print(text)
			},
			nil,
		)

		result, err := worker.ExecuteInteractive(names.ManagerName, prompt, opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\nFailed to import issues: %v\n", err)
			os.Exit(1)
		}

		fmt.Println()
		fmt.Println(result.Result)

		// Check pending task count
		pendingCount, _ := queue.CountPendingTasksBySheep(sheepID)
		fmt.Printf("\n📋 Sheep '%s' pending tasks: %d\n", sheepName, pendingCount)
	},
}

var queueCancelCmd = &cobra.Command{
	Use:   "cancel <id>",
	Short: "Cancel a pending task",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid task ID: %s\n", args[0])
			os.Exit(1)
		}

		t, err := queue.GetTask(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Task not found: %v\n", err)
			os.Exit(1)
		}

		if t.Status != task.StatusPending {
			fmt.Fprintf(os.Stderr, "Task #%d is not pending (status: %s). Use 'shepherd task stop %d' for running tasks.\n", id, t.Status, id)
			os.Exit(1)
		}

		if err := queue.FailTask(id, "cancelled by user"); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to cancel task: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("📋 Task #%d cancelled.\n", id)
	},
}

var queueClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Cancel all pending tasks",
	Run: func(cmd *cobra.Command, args []string) {
		count, err := queue.CancelPendingTasks()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to clear queue: %v\n", err)
			os.Exit(1)
		}

		if count == 0 {
			fmt.Println("No pending tasks to cancel.")
		} else {
			fmt.Printf("📋 %d pending task(s) cancelled.\n", count)
		}
	},
}

var taskStopCmd = &cobra.Command{
	Use:   "stop <id>",
	Short: "Stop a running task",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid task ID: %s\n", args[0])
			os.Exit(1)
		}

		t, err := queue.GetTask(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Task not found: %v\n", err)
			os.Exit(1)
		}

		if t.Status != task.StatusRunning {
			fmt.Fprintf(os.Stderr, "Task #%d is not running (status: %s).\n", id, t.Status)
			os.Exit(1)
		}

		if t.Edges.Sheep == nil {
			fmt.Fprintf(os.Stderr, "Task #%d has no assigned sheep.\n", id)
			os.Exit(1)
		}

		result, err := worker.StopTask(t.Edges.Sheep.Name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to stop task: %v\n", err)
			os.Exit(1)
		}

		_ = queue.FailTaskWithOutput(id, "stopped by user", result.OutputLines)
		fmt.Printf("🛑 Task #%d stopped.\n", id)
	},
}

var taskCancelAllCmd = &cobra.Command{
	Use:   "cancel-all",
	Short: "Cancel all running and pending tasks",
	Run: func(cmd *cobra.Command, args []string) {
		// Stop running tasks' processes first
		runningTasks, _ := queue.ListTasksByStatus(task.StatusRunning)
		for _, t := range runningTasks {
			if t.Edges.Sheep != nil {
				_, _ = worker.StopTask(t.Edges.Sheep.Name)
			}
		}

		runningCount, err := queue.CancelRunningTasks()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to cancel running tasks: %v\n", err)
		}

		pendingCount, err := queue.CancelPendingTasks()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to cancel pending tasks: %v\n", err)
		}

		total := runningCount + pendingCount
		if total == 0 {
			fmt.Println("No active tasks to cancel.")
		} else {
			fmt.Printf("🛑 Cancelled %d task(s) (running: %d, pending: %d).\n", total, runningCount, pendingCount)
		}
	},
}

var projectUnassignCmd = &cobra.Command{
	Use:   "unassign <project>",
	Short: "Remove sheep assignment from a project",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]
		if err := project.UnassignSheep(projectName); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to unassign sheep: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("📁 Sheep unassigned from %s\n", projectName)
	},
}

// classifyUserIntent classifies the user's intent using Claude Code.
func classifyUserIntent(prompt string) *manager.Intent {
	cwd, _ := os.Getwd()
	intent, err := manager.ClassifyIntent(prompt, cwd)
	if err != nil {
		// Default fallback on classification failure
		return &manager.Intent{Type: "coding_task"}
	}
	return intent
}

// handleShepherdCommand handles shepherd-specific commands from natural language.
func handleShepherdCommand(intent *manager.Intent, prompt string) bool {
	cwd, _ := os.Getwd()

	// Record shepherd commands as tasks
	var taskID int
	var taskSummary string
	recordManagerTask := func(summary string) {
		mgr, err := worker.GetOrCreateManager()
		if err != nil {
			return
		}
		t, err := queue.CreateManagerTask(prompt, mgr.ID)
		if err != nil {
			return
		}
		taskID = t.ID
		_ = queue.StartTask(taskID)
		taskSummary = summary
	}
	completeManagerTask := func() {
		if taskID > 0 {
			_ = queue.CompleteTask(taskID, taskSummary, nil)
		}
	}

	switch intent.Type {
	case "register_project":
		recordManagerTask("Register project")
		defer completeManagerTask()

		// If a git URL is provided, clone first
		if intent.GitURL != "" {
			// Extract project name from git URL
			repoName := extractRepoName(intent.GitURL)
			clonePath := filepath.Join(cwd, repoName)

			// Check if already exists
			if _, err := os.Stat(clonePath); err == nil {
				fmt.Printf("📁 Directory '%s' already exists\n", repoName)
			} else {
				// Run git clone
				fmt.Printf("🔄 git clone %s ...\n", intent.GitURL)
				cmd := exec.Command("git", "clone", intent.GitURL, clonePath)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr
				if err := cmd.Run(); err != nil {
					fmt.Printf("❌ git clone failed: %v\n", err)
					return true
				}
				fmt.Printf("✅ '%s' cloned successfully\n", repoName)
			}

			// Register the cloned directory as a project
			_, err := project.Add(repoName, clonePath, "")
			if err != nil {
				if strings.Contains(err.Error(), "already exists") {
					fmt.Printf("📁 '%s' already registered\n", repoName)
				} else {
					fmt.Printf("❌ Failed to register '%s': %v\n", repoName, err)
				}
			} else {
				fmt.Printf("📁 '%s' registered (%s)\n", repoName, clonePath)
			}
		} else if len(intent.RegisterNames) > 0 {
			// Register specific directories
			for _, name := range intent.RegisterNames {
				path := filepath.Join(cwd, name)
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					_, err := project.Add(name, path, "")
					if err != nil {
						if strings.Contains(err.Error(), "already exists") {
							fmt.Printf("📁 '%s' already registered\n", name)
						} else {
							fmt.Printf("❌ Failed to register '%s': %v\n", name, err)
						}
					} else {
						fmt.Printf("📁 '%s' registered (%s)\n", name, path)
					}
				}
			}
		} else {
			// Register subdirectories of the current directory
			entries, _ := os.ReadDir(cwd)
			registered := 0
			for _, entry := range entries {
				if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
					path := filepath.Join(cwd, entry.Name())
					_, err := project.Add(entry.Name(), path, "")
					if err == nil {
						fmt.Printf("📁 '%s' registered\n", entry.Name())
						registered++
					}
				}
			}
			if registered == 0 {
				fmt.Println("No directories to register.")
			}
		}

		// Create a sheep if none exist
		sheepList, _ := worker.List()
		if len(sheepList) == 0 {
			s, _ := worker.Create("")
			if s != nil {
				fmt.Printf("🐏 %s created\n", s.Name)
			}
		}
		return true

	case "delete_project":
		recordManagerTask("Delete project")
		defer completeManagerTask()

		// Delete projects
		if len(intent.ProjectNames) > 0 {
			for _, name := range intent.ProjectNames {
				if err := project.Remove(name); err != nil {
					fmt.Printf("❌ Failed to delete '%s': %v\n", name, err)
				} else {
					fmt.Printf("🗑️  '%s' deleted\n", name)
				}
			}
		} else {
			fmt.Println("Please specify the project name to delete.")
		}
		return true

	case "delete_and_register":
		recordManagerTask("Delete and re-register projects")
		defer completeManagerTask()

		// Delete projects then register new ones
		// 1. First, look up paths of projects to delete
		var deletedPaths []string
		for _, name := range intent.ProjectNames {
			proj, err := project.Get(name)
			if err == nil {
				deletedPaths = append(deletedPaths, proj.Path)
			}
			if err := project.Remove(name); err != nil {
				fmt.Printf("❌ Failed to delete '%s': %v\n", name, err)
			} else {
				fmt.Printf("🗑️  '%s' deleted\n", name)
			}
		}

		// 2. Determine directories to register
		var foldersToRegister []string
		if len(intent.RegisterNames) > 0 {
			// Specific directories specified
			for _, name := range intent.RegisterNames {
				path := filepath.Join(cwd, name)
				if info, err := os.Stat(path); err == nil && info.IsDir() {
					foldersToRegister = append(foldersToRegister, path)
				}
			}
		} else {
			// Register subdirectories of the deleted project paths
			for _, deletedPath := range deletedPaths {
				entries, err := os.ReadDir(deletedPath)
				if err != nil {
					continue
				}
				for _, entry := range entries {
					if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
						foldersToRegister = append(foldersToRegister, filepath.Join(deletedPath, entry.Name()))
					}
				}
			}
			// Fall back to current directory subdirectories if no deleted paths
			if len(foldersToRegister) == 0 {
				entries, _ := os.ReadDir(cwd)
				for _, entry := range entries {
					if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
						foldersToRegister = append(foldersToRegister, filepath.Join(cwd, entry.Name()))
					}
				}
			}
		}

		// 3. Register directories
		registered := 0
		for _, path := range foldersToRegister {
			name := filepath.Base(path)
			_, err := project.Add(name, path, "")
			if err != nil {
				if strings.Contains(err.Error(), "already exists") {
					fmt.Printf("📁 '%s' already registered\n", name)
				} else {
					fmt.Printf("❌ Failed to register '%s': %v\n", name, err)
				}
			} else {
				fmt.Printf("📁 '%s' registered (%s)\n", name, path)
				registered++
			}
		}

		if registered == 0 && len(foldersToRegister) == 0 {
			fmt.Println("No directories to register.")
		}

		// Create a sheep if none exist
		sheepList, _ := worker.List()
		if len(sheepList) == 0 {
			s, _ := worker.Create("")
			if s != nil {
				fmt.Printf("🐏 %s created\n", s.Name)
			}
		}
		return true

	case "list_projects":
		recordManagerTask("List projects")
		defer completeManagerTask()

		// Project list
		projects, _ := project.List()
		if len(projects) == 0 {
			fmt.Println("No projects registered.")
		} else {
			fmt.Println("📁 Projects:")
			for _, p := range projects {
				sheepName := "unassigned"
				if p.Edges.Sheep != nil {
					sheepName = p.Edges.Sheep.Name
				}
				fmt.Printf("   %s (%s) - %s\n", p.Name, p.Path, sheepName)
			}
		}
		return true

	case "shepherd_command":
		recordManagerTask("Show shepherd commands")
		defer completeManagerTask()

		// Other shepherd commands - display help
		fmt.Println("Shepherd commands:")
		fmt.Println("   shepherd init           - Register current directory as a project")
		fmt.Println("   shepherd spawn          - Create a new sheep")
		fmt.Println("   shepherd flock          - List all sheep")
		fmt.Println("   shepherd status         - Show overall status")
		fmt.Println("   shepherd project list   - List projects")
		return true
	}

	return false
}

// autoInitProject automatically initializes the current directory as a project.
func autoInitProject() (string, string, error) {
	// Current directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}

	projectName := filepath.Base(cwd)

	// Register project
	_, err = project.Add(projectName, cwd, "")
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return "", "", err
	}

	// Create a sheep if none exist
	sheepList, err := worker.List()
	if err != nil {
		return "", "", err
	}

	var sheepName string
	if len(sheepList) == 0 {
		s, err := worker.Create("")
		if err != nil {
			return "", "", err
		}
		sheepName = s.Name
		fmt.Printf("🐏 %s created\n", sheepName)
	} else {
		for _, s := range sheepList {
			if s.Edges.Project == nil {
				sheepName = s.Name
				break
			}
		}
		if sheepName == "" {
			sheepName = sheepList[0].Name
		}
	}

	// Assign sheep to project
	_ = project.AssignSheep(projectName, sheepName)

	return projectName, sheepName, nil
}

// runChatMode runs the interactive chat interface.
func runChatMode() {
	// Recover from abnormal termination before starting chat mode
	recoverFromAbnormalTermination()

	// Current directory
	cwd, _ := os.Getwd()
	cwdName := filepath.Base(cwd)

	// Welcome message
	fmt.Println()
	fmt.Println("🐏 Shepherd - AI Coding Orchestration")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("📁 Current directory: %s\n", cwd)

	// Display project status
	projects, _ := project.List()
	sheepList, _ := worker.List()
	fmt.Printf("📊 Projects: %d, Sheep: %d\n", len(projects), len(sheepList))
	fmt.Println()
	fmt.Println("Commands: exit, status, projects, help")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()

	// Configure readline
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          fmt.Sprintf("\033[36m%s\033[0m 🐏 > ", cwdName),
		HistoryFile:     filepath.Join(os.Getenv("HOME"), ".shepherd", "history"),
		InterruptPrompt: "^C",
		EOFPrompt:       "exit",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize readline: %v\n", err)
		return
	}
	defer rl.Close()

	// Set global readline instance (used in interactive execution)
	chatReadline = rl

	for {
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				if len(line) == 0 {
					fmt.Println("\n👋 Goodbye!")
					break
				}
				continue
			}
			if err == io.EOF {
				fmt.Println("\n👋 Goodbye!")
				break
			}
			continue
		}

		input := strings.TrimSpace(line)
		if input == "" {
			continue
		}

		// Handle built-in commands
		switch strings.ToLower(input) {
		case "exit", "quit", "q":
			fmt.Println("👋 Goodbye!")
			return

		case "help", "?":
			printChatHelp()
			continue

		case "status":
			printStatus()
			continue

		case "projects":
			printProjects()
			continue

		case "flock":
			printFlock()
			continue

		case "log", "logs", "history":
			printTaskLog()
			continue

		case "clear", "cls":
			fmt.Print("\033[H\033[2J")
			continue
		}

		// Handle natural language commands related to task logs
		lowered := strings.ToLower(input)
		if strings.Contains(lowered, "task") && (strings.Contains(lowered, "done") || strings.Contains(lowered, "list") || strings.Contains(lowered, "completed")) {
			printTaskLog()
			continue
		}

		// Query specific task details (e.g., "#10 detail", "task 10")
		if taskID := extractTaskID(input); taskID > 0 {
			printTaskDetail(taskID)
			continue
		}

		// Execute general task (interactive mode)
		fmt.Println()
		executeTaskInteractive(input, rl)
		fmt.Println()
	}
}

// printChatHelp prints help message for chat mode.
func printChatHelp() {
	fmt.Println()
	fmt.Println("🐏 Shepherd Interactive Mode Help")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
	fmt.Println("💬 Request tasks in natural language:")
	fmt.Println("   \"Add login feature\"")
	fmt.Println("   \"Fix the bug\"")
	fmt.Println("   \"Write tests\"")
	fmt.Println()
	fmt.Println("📁 Project management:")
	fmt.Println("   \"Register project\"")
	fmt.Println("   \"Delete project\"")
	fmt.Println("   \"Show project list\"")
	fmt.Println()
	fmt.Println("⌨️  Built-in commands:")
	fmt.Println("   status    - Show overall status")
	fmt.Println("   projects  - List projects")
	fmt.Println("   flock     - List sheep")
	fmt.Println("   clear     - Clear screen")
	fmt.Println("   help      - Show this help")
	fmt.Println("   exit      - Quit")
	fmt.Println()
}

// printStatus prints the current status in chat mode.
func printStatus() {
	fmt.Println()

	// Sheep status
	sheepList, _ := worker.List()
	fmt.Println("🐏 Sheep status:")
	if len(sheepList) == 0 {
		fmt.Println("   (none)")
	} else {
		for _, s := range sheepList {
			projectName := "-"
			if s.Edges.Project != nil {
				projectName = s.Edges.Project.Name
			}
			status := worker.StatusToKorean(s.Status)
			fmt.Printf("   %-10s %-15s %s\n", s.Name, projectName, status)
		}
	}
	fmt.Println()

	// Project status
	projects, _ := project.List()
	fmt.Println("📁 Project status:")
	if len(projects) == 0 {
		fmt.Println("   (none)")
	} else {
		for _, p := range projects {
			sheepName := "-"
			if p.Edges.Sheep != nil {
				sheepName = p.Edges.Sheep.Name
			}
			fmt.Printf("   %-15s %-10s %s\n", p.Name, sheepName, p.Path)
		}
	}
	fmt.Println()

	// Task status
	counts, _ := queue.CountByStatus()
	fmt.Printf("📋 Tasks: pending %d, running %d, completed %d, failed %d\n",
		counts[task.StatusPending], counts[task.StatusRunning], counts[task.StatusCompleted], counts[task.StatusFailed])
	fmt.Println()
}

// printProjects prints project list in chat mode.
func printProjects() {
	fmt.Println()
	projects, _ := project.List()
	if len(projects) == 0 {
		fmt.Println("📁 No projects registered.")
		fmt.Println("   Use \"register project\" or 'shepherd init' to register one.")
	} else {
		fmt.Println("📁 Projects:")
		for _, p := range projects {
			sheepName := "unassigned"
			if p.Edges.Sheep != nil {
				sheepName = p.Edges.Sheep.Name
			}
			fmt.Printf("   %-15s %s (%s)\n", p.Name, p.Path, sheepName)
		}
	}
	fmt.Println()
}

// printFlock prints sheep list in chat mode.
func printFlock() {
	fmt.Println()
	sheepList, _ := worker.List()
	if len(sheepList) == 0 {
		fmt.Println("🐏 No sheep created.")
		fmt.Println("   Create one with 'shepherd spawn'.")
	} else {
		fmt.Println("🐏 Sheep list:")
		for _, s := range sheepList {
			projectName := "-"
			if s.Edges.Project != nil {
				projectName = s.Edges.Project.Name
			}
			status := worker.StatusToKorean(s.Status)
			fmt.Printf("   %-10s %-15s %s\n", s.Name, projectName, status)
		}
	}
	fmt.Println()
}

// extractTaskID extracts task ID from input like "#10", "task 10", "10번", "작업 10"
func extractTaskID(input string) int {
	// #number pattern
	re := regexp.MustCompile(`#(\d+)`)
	if matches := re.FindStringSubmatch(input); len(matches) > 1 {
		id, _ := strconv.Atoi(matches[1])
		return id
	}

	// "task N" pattern (English)
	re = regexp.MustCompile(`(?i)task\s*(\d+)`)
	if matches := re.FindStringSubmatch(input); len(matches) > 1 {
		id, _ := strconv.Atoi(matches[1])
		return id
	}

	// "작업 N" pattern (Korean)
	re = regexp.MustCompile(`작업\s*(\d+)`)
	if matches := re.FindStringSubmatch(input); len(matches) > 1 {
		id, _ := strconv.Atoi(matches[1])
		return id
	}

	// "N번" pattern (Korean counter)
	re = regexp.MustCompile(`(\d+)\s*번`)
	if matches := re.FindStringSubmatch(input); len(matches) > 1 {
		id, _ := strconv.Atoi(matches[1])
		return id
	}

	return 0
}

// printTaskDetail prints detailed information about a specific task.
func printTaskDetail(taskID int) {
	fmt.Println()
	task, err := queue.GetTask(taskID)
	if err != nil {
		fmt.Printf("❌ Task #%d not found\n", taskID)
		return
	}

	sheepName := "-"
	if task.Edges.Sheep != nil {
		sheepName = task.Edges.Sheep.Name
	}
	projectName := "-"
	if task.Edges.Project != nil {
		projectName = task.Edges.Project.Name
	}
	status := queue.StatusToKorean(task.Status)

	fmt.Printf("📋 Task #%d details\n", taskID)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf("Status: %s\n", status)
	fmt.Printf("Created: %s\n", task.CreatedAt.Format("2006-01-02 15:04:05"))
	if !task.StartedAt.IsZero() {
		fmt.Printf("Started: %s\n", task.StartedAt.Format("2006-01-02 15:04:05"))
	}
	if !task.CompletedAt.IsZero() {
		fmt.Printf("Completed: %s\n", task.CompletedAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Printf("🐏 Sheep: %s\n", sheepName)
	fmt.Printf("📁 Project: %s\n", projectName)
	fmt.Println()
	fmt.Println("📝 Prompt:")
	fmt.Println(task.Prompt)
	fmt.Println()
	if task.Summary != "" {
		fmt.Println("✅ Result:")
		fmt.Println(task.Summary)
		fmt.Println()
	}
	if len(task.FilesModified) > 0 {
		fmt.Printf("📁 Modified files: %s\n", strings.Join(task.FilesModified, ", "))
	}
	if task.Error != "" {
		fmt.Println("❌ Error:")
		fmt.Println(task.Error)
	}
	fmt.Println()
}

// printTaskLog prints recent task log in chat mode.
func printTaskLog() {
	fmt.Println()
	tasks, err := queue.ListTasks(10)
	if err != nil {
		fmt.Printf("Failed to list tasks: %v\n", err)
		return
	}

	if len(tasks) == 0 {
		fmt.Println("📋 No task history.")
	} else {
		fmt.Println("📋 Recent tasks:")
		for _, t := range tasks {
			sheepName := "-"
			if t.Edges.Sheep != nil {
				sheepName = t.Edges.Sheep.Name
			}
			projectName := "-"
			if t.Edges.Project != nil {
				projectName = t.Edges.Project.Name
			}
			status := queue.StatusToKorean(t.Status)
			timeStr := t.CreatedAt.Format("01/02 15:04")

			fmt.Printf("\n   #%d [%s] %s\n", t.ID, status, timeStr)
			fmt.Printf("      🐏 %s → 📁 %s\n", sheepName, projectName)
			fmt.Printf("      Prompt: %s\n", truncate(t.Prompt, 50))
			if t.Summary != "" {
				fmt.Printf("      Result: %s\n", truncate(t.Summary, 50))
			}
		}
	}
	fmt.Println()
}

// executeTask executes a task through the full workflow.
func executeTask(prompt string) {
	// Recover from abnormal termination before single task execution
	recoverFromAbnormalTermination()

	fmt.Println("🐕 Shepherd is analyzing the task...")

	// Classify intent first
	intent := classifyUserIntent(prompt)

	// Handle shepherd commands and exit
	if handleShepherdCommand(intent, prompt) {
		return
	}

	// Proceed with coding task
	// Check for existing projects/sheep
	projects, _ := project.List()
	sheepList, _ := worker.List()

	// No projects or sheep: auto-register current directory
	if len(projects) == 0 || len(sheepList) == 0 {
		fmt.Println("📁 No projects found. Auto-registering current directory...")
		_, _, err := autoInitProject()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Auto-registration failed: %v\n", err)
			fmt.Println()
			fmt.Println("To register manually:")
			fmt.Println("   shepherd init")
			os.Exit(1)
		}
		fmt.Println()
		// Continue (re-fetch projects and sheep)
		projects, _ = project.List()
		sheepList, _ = worker.List()
	}

	// 1. Shepherd analyzes task and decides assignment
	decision, err := manager.Analyze(prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to analyze task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("📋 %s\n", decision.Reason)
	fmt.Printf("🐏 %s will work on %s\n\n", decision.SheepName, decision.ProjectName)

	// 2. Assign sheep to project (if not already assigned)
	proj, err := project.Get(decision.ProjectName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to find project: %v\n", err)
		os.Exit(1)
	}

	sheep, err := worker.Get(decision.SheepName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to find sheep: %v\n", err)
		os.Exit(1)
	}

	// Re-assign if sheep is assigned to a different project
	if sheep.Edges.Project == nil || sheep.Edges.Project.ID != proj.ID {
		if err := project.AssignSheep(decision.ProjectName, decision.SheepName); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to assign sheep: %v\n", err)
			os.Exit(1)
		}
	}

	// 3. Add to task queue
	task, err := queue.CreateTask(prompt, sheep.ID, proj.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create task: %v\n", err)
		os.Exit(1)
	}

	// 4. Check if sheep is currently working
	isWorking, err := worker.IsWorking(decision.SheepName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to check sheep status: %v\n", err)
		os.Exit(1)
	}

	if isWorking {
		// Sheep is busy: wait in queue
		pendingCount, _ := queue.CountPendingTasksBySheep(sheep.ID)
		fmt.Printf("⏸ %s is currently working. Added to queue (pending: %d)\n", decision.SheepName, pendingCount)
		fmt.Println("   Will start automatically when the previous task completes.")

		// Wait while checking sheep status
		for {
			time.Sleep(2 * time.Second)
			isWorking, err = worker.IsWorking(decision.SheepName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to check sheep status: %v\n", err)
				os.Exit(1)
			}
			if !isWorking {
				break
			}
		}
		fmt.Printf("🐏 %s is ready. Starting task...\n", decision.SheepName)
	}

	// 5. Start task
	if err := queue.StartTask(task.ID); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start task: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("⏸ Task in progress...")
	fmt.Println()

	// 6. Execute Claude Code
	result, err := worker.Execute(decision.SheepName, prompt)
	if err != nil {
		// Handle task failure
		_ = queue.FailTask(task.ID, err.Error())
		fmt.Fprintf(os.Stderr, "❌ Task failed: %v\n", err)
		os.Exit(1)
	}

	// 7. Complete task
	if err := queue.CompleteTask(task.ID, result.Result, result.FilesModified); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to complete task: %v\n", err)
		os.Exit(1)
	}

	// 8. Display results
	fmt.Printf("✅ %s task completed\n", decision.SheepName)
	if len(result.FilesModified) > 0 {
		fmt.Printf("   Modified: %s\n", strings.Join(result.FilesModified, ", "))
	}
	if result.Result != "" {
		fmt.Printf("   Summary: %s\n", truncate(result.Result, 200))
	}
}

// findIdleSheep finds an idle sheep not assigned to any project.
func findIdleSheep() string {
	sheepList, _ := worker.List()
	for _, s := range sheepList {
		if s.Edges.Project == nil {
			return s.Name
		}
	}
	return ""
}

// truncate truncates a string to maxLen characters.
func truncate(s string, maxLen int) string {
	// Remove newlines and use only the first line
	lines := strings.Split(s, "\n")
	s = lines[0]
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// extractRepoName extracts the repository name from a git URL.
// Supports: git@github.com:user/repo.git, https://github.com/user/repo.git
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

// executeTaskInteractive executes a task with interactive Claude Code session.
// This is used in chat mode to allow Q&A between Claude and the user.
func executeTaskInteractive(prompt string, rl *readline.Instance) {
	fmt.Println("🐕 Shepherd is analyzing the task...")

	// Classify intent first
	intent := classifyUserIntent(prompt)

	// Handle shepherd commands and exit
	if handleShepherdCommand(intent, prompt) {
		return
	}

	// Proceed with coding task
	projects, _ := project.List()
	sheepList, _ := worker.List()

	// No projects or sheep: auto-register current directory
	if len(projects) == 0 || len(sheepList) == 0 {
		fmt.Println("📁 No projects found. Auto-registering current directory...")
		_, _, err := autoInitProject()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Auto-registration failed: %v\n", err)
			return
		}
		fmt.Println()
	}

	// Shepherd analyzes task and decides assignment
	decision, err := manager.Analyze(prompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to analyze task: %v\n", err)
		return
	}

	fmt.Printf("📋 %s\n", decision.Reason)
	fmt.Printf("🐏 %s will work on %s\n\n", decision.SheepName, decision.ProjectName)

	// Assign sheep to project
	proj, err := project.Get(decision.ProjectName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to find project: %v\n", err)
		return
	}

	// Use existing sheep if already assigned to the project
	var actualSheepName string
	if proj.Edges.Sheep != nil {
		actualSheepName = proj.Edges.Sheep.Name
	} else {
		// Try to assign sheep
		if err := project.AssignSheep(decision.ProjectName, decision.SheepName); err != nil {
			// Assignment failed - find or create another sheep
			idleSheep := findIdleSheep()
			if idleSheep != "" {
				if err := project.AssignSheep(decision.ProjectName, idleSheep); err != nil {
					// Create new sheep
					newSheep, createErr := worker.Create("")
					if createErr != nil {
						fmt.Fprintf(os.Stderr, "Failed to create sheep: %v\n", createErr)
						return
					}
					fmt.Printf("🐏 %s created\n", newSheep.Name)
					if err := project.AssignSheep(decision.ProjectName, newSheep.Name); err != nil {
						fmt.Fprintf(os.Stderr, "Failed to assign sheep: %v\n", err)
						return
					}
					actualSheepName = newSheep.Name
				} else {
					actualSheepName = idleSheep
				}
			} else {
				// Create new sheep
				newSheep, createErr := worker.Create("")
				if createErr != nil {
					fmt.Fprintf(os.Stderr, "Failed to create sheep: %v\n", createErr)
					return
				}
				fmt.Printf("🐏 %s created\n", newSheep.Name)
				if err := project.AssignSheep(decision.ProjectName, newSheep.Name); err != nil {
					fmt.Fprintf(os.Stderr, "Failed to assign sheep: %v\n", err)
					return
				}
				actualSheepName = newSheep.Name
			}
		} else {
			actualSheepName = decision.SheepName
		}
	}

	s, err := worker.Get(actualSheepName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to find sheep: %v\n", err)
		return
	}

	if actualSheepName != decision.SheepName {
		fmt.Printf("🐏 %s will work instead\n", actualSheepName)
	}

	// Add to task queue
	task, err := queue.CreateTask(prompt, s.ID, proj.ID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create task: %v\n", err)
		return
	}

	// Check if sheep is currently working
	isWorking, err := worker.IsWorking(actualSheepName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to check sheep status: %v\n", err)
		return
	}

	if isWorking {
		// Sheep is busy: wait in queue
		pendingCount, _ := queue.CountPendingTasksBySheep(s.ID)
		fmt.Printf("⏸ %s is currently working. Added to queue (pending: %d)\n", actualSheepName, pendingCount)
		fmt.Println("   Will start automatically when the previous task completes.")

		// Wait while checking sheep status
		for {
			time.Sleep(2 * time.Second)
			isWorking, err = worker.IsWorking(actualSheepName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to check sheep status: %v\n", err)
				return
			}
			if !isWorking {
				break
			}
		}
		fmt.Printf("🐏 %s is ready. Starting task...\n", actualSheepName)
	}

	if err := queue.StartTask(task.ID); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start task: %v\n", err)
		return
	}

	fmt.Println("⏸ Task in progress...")
	fmt.Println()

	// Configure interactive execution options
	opts := worker.DefaultInteractiveOptions(
		// Output handler - display Claude's output to user
		func(output string) {
			fmt.Print(output)
		},
		// Input handler - get user input when Claude asks a question
		func(promptText string) (string, error) {
			rl.SetPrompt("   💬 답변 > ")
			defer rl.SetPrompt(fmt.Sprintf("\033[36m%s\033[0m 🐏 > ", filepath.Base(proj.Path)))

			line, err := rl.Readline()
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(line), nil
		},
	)

	// Execute interactive Claude Code (using actualSheepName!)
	result, err := worker.ExecuteInteractive(actualSheepName, prompt, opts)
	if err != nil {
		_ = queue.FailTask(task.ID, err.Error())
		fmt.Fprintf(os.Stderr, "\n❌ Task failed: %v\n", err)
		return
	}

	// Complete task
	var filesModified []string
	var summary string
	if result != nil {
		filesModified = result.FilesModified
		summary = result.Result
	}

	if err := queue.CompleteTask(task.ID, summary, filesModified); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to complete task: %v\n", err)
		return
	}

	// Display results
	fmt.Printf("\n✅ %s task completed\n", actualSheepName)
	if result != nil {
		if result.Result != "" {
			// Display result summary (max 500 chars)
			resultText := result.Result
			if len(resultText) > 500 {
				resultText = resultText[:500] + "..."
			}
			fmt.Printf("\n📝 Result:\n%s\n", resultText)
		}
		if len(result.FilesModified) > 0 {
			fmt.Printf("\n📁 Modified files: %s\n", strings.Join(result.FilesModified, ", "))
		}
	}
}

// status command
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show overall status",
	Long:  "Displays the overall status of sheep, projects, and tasks.",
	Run: func(cmd *cobra.Command, args []string) {
		// Sheep status
		sheepList, err := worker.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list sheep: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("=== 🐏 Sheep Status ===")
		if len(sheepList) == 0 {
			fmt.Println("  (none)")
		} else {
			for _, s := range sheepList {
				projectName := "-"
				if s.Edges.Project != nil {
					projectName = s.Edges.Project.Name
				}
				status := worker.StatusToKorean(s.Status)
				fmt.Printf("  %-10s %-10s %s\n", s.Name, projectName, status)
			}
		}
		fmt.Println()

		// Project status
		projects, err := project.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list projects: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("=== 📁 Project Status ===")
		if len(projects) == 0 {
			fmt.Println("  (none)")
		} else {
			for _, p := range projects {
				sheepName := "-"
				if p.Edges.Sheep != nil {
					sheepName = p.Edges.Sheep.Name
				}
				fmt.Printf("  %-15s %-10s %s\n", p.Name, sheepName, p.Path)
			}
		}
		fmt.Println()

		// Task status
		counts, err := queue.CountByStatus()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get task status: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("=== 📋 Task Status ===")
		fmt.Printf("  Pending: %d, Running: %d, Completed: %d, Failed: %d\n",
			counts[task.StatusPending], counts[task.StatusRunning], counts[task.StatusCompleted], counts[task.StatusFailed])

		// Recent 5 tasks
		tasks, err := queue.ListTasks(5)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to list tasks: %v\n", err)
			os.Exit(1)
		}

		if len(tasks) > 0 {
			fmt.Println("\n  Recent tasks:")
			for _, t := range tasks {
				sheepName := "-"
				if t.Edges.Sheep != nil {
					sheepName = t.Edges.Sheep.Name
				}
				status := queue.StatusToKorean(t.Status)
				prompt := truncate(t.Prompt, 30)
				fmt.Printf("    #%-4d %-10s %-6s %s\n", t.ID, sheepName, status, prompt)
			}
		}
	},
}

// log command
var logLimit int

var logCmd = &cobra.Command{
	Use:   "log [sheep]",
	Short: "Task log",
	Long:  "Displays task logs per sheep. Shows all logs if no sheep name is specified.",
	Run: func(cmd *cobra.Command, args []string) {
		var tasks []*ent.Task
		var err error

		if len(args) > 0 {
			// Logs for a specific sheep
			sheepName := args[0]
			tasks, err = queue.ListTasksBySheep(sheepName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to fetch logs: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("=== 🐏 %s Task Log ===\n", sheepName)
		} else {
			// All logs
			tasks, err = queue.ListTasks(logLimit)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to fetch logs: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("=== 📋 All Task Logs ===")
		}

		if len(tasks) == 0 {
			fmt.Println("  No task history.")
			return
		}

		for _, t := range tasks {
			sheepName := "-"
			if t.Edges.Sheep != nil {
				sheepName = t.Edges.Sheep.Name
			}
			projectName := "-"
			if t.Edges.Project != nil {
				projectName = t.Edges.Project.Name
			}
			status := queue.StatusToKorean(t.Status)
			timeStr := t.CreatedAt.Format("01/02 15:04")

			fmt.Printf("\n#%d [%s] %s\n", t.ID, status, timeStr)
			fmt.Printf("  Sheep: %s, Project: %s\n", sheepName, projectName)
			fmt.Printf("  Prompt: %s\n", truncate(t.Prompt, 50))

			if t.Summary != "" {
				fmt.Printf("  Result: %s\n", truncate(t.Summary, 50))
			}
			if len(t.FilesModified) > 0 {
				fmt.Printf("  Modified: %s\n", strings.Join(t.FilesModified, ", "))
			}
			if t.Error != "" {
				fmt.Printf("  Error: %s\n", truncate(t.Error, 50))
			}
		}
	},
}

// history command
var historyCmd = &cobra.Command{
	Use:   "history <project>",
	Short: "Project history",
	Long:  "Displays the task history for a specific project.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]

		// Check project exists
		proj, err := project.Get(projectName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to find project: %v\n", err)
			os.Exit(1)
		}

		tasks, err := queue.ListTasksByProject(projectName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to fetch history: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("=== 📁 %s History ===\n", proj.Name)
		fmt.Printf("Path: %s\n", proj.Path)
		if proj.Description != "" {
			fmt.Printf("Description: %s\n", proj.Description)
		}
		if proj.Edges.Sheep != nil {
			fmt.Printf("Assigned: %s\n", proj.Edges.Sheep.Name)
		}
		fmt.Println()

		if len(tasks) == 0 {
			fmt.Println("No task history.")
			return
		}

		// Statistics
		var completed, failed int
		for _, t := range tasks {
			switch t.Status {
			case "completed":
				completed++
			case "failed":
				failed++
			}
		}
		fmt.Printf("Total: %d (completed: %d, failed: %d)\n\n", len(tasks), completed, failed)

		// Task list
		for _, t := range tasks {
			sheepName := "-"
			if t.Edges.Sheep != nil {
				sheepName = t.Edges.Sheep.Name
			}
			status := queue.StatusToKorean(t.Status)
			timeStr := t.CreatedAt.Format("01/02 15:04")

			fmt.Printf("#%d [%s] %s - %s\n", t.ID, status, timeStr, sheepName)
			fmt.Printf("   %s\n", truncate(t.Prompt, 60))
			if t.Summary != "" {
				fmt.Printf("   → %s\n", truncate(t.Summary, 60))
			}
		}
	},
}

// mcp command
var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run MCP server",
	Long: `Runs the MCP (Model Context Protocol) server.
Claude Code can communicate with shepherd through this server.

Configuration example (~/.claude/claude_desktop_config.json):
{
  "mcpServers": {
    "shepherd": {
      "command": "shepherd",
      "args": ["mcp"]
    }
  }
}`,
	Run: func(cmd *cobra.Command, args []string) {
		// Initialize for MCP
		if err := mcp.InitForTools(); err != nil {
			fmt.Fprintf(os.Stderr, "Initialization failed: %v\n", err)
			os.Exit(1)
		}
		defer db.Close()

		minimal, _ := cmd.Flags().GetBool("minimal")

		// Run MCP server
		server := mcp.NewServer(minimal)
		if err := server.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "MCP server error: %v\n", err)
			os.Exit(1)
		}
	},
}

// browser command group
var browserCmd = &cobra.Command{
	Use:   "browser",
	Short: "Browser automation",
	Long:  "Control a browser to open web pages, extract content, or interact with them.",
}

var browserSheepName string
var browserPageName string
var browserHeadless bool
var browserSelector string
var browserTimeout int

var browserOpenCmd = &cobra.Command{
	Use:   "open <url>",
	Short: "Open a URL",
	Long:  "Starts a browser session and opens the URL.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		url := args[0]
		sheepName := browserSheepName
		if sheepName == "" {
			sheepName = names.ManagerName
		}

		mgr := browser.GetManager()
		sess, err := mgr.GetOrCreateSession(sheepName, &browser.SessionOptions{
			Headless: browserHeadless,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create browser session: %v\n", err)
			os.Exit(1)
		}

		page, err := sess.OpenPage(url, browserPageName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open page: %v\n", err)
			os.Exit(1)
		}

		info, _ := page.Info()
		fmt.Printf("🌐 Page opened: %s\n", info.Title)
		fmt.Printf("   URL: %s\n", info.URL)
	},
}

var browserGetHTMLCmd = &cobra.Command{
	Use:   "get-html",
	Short: "Get HTML",
	Long:  "Gets the HTML of the current page or a specific element.",
	Run: func(cmd *cobra.Command, args []string) {
		sheepName := browserSheepName
		if sheepName == "" {
			sheepName = names.ManagerName
		}

		mgr := browser.GetManager()
		sess := mgr.GetSession(sheepName)
		if sess == nil {
			fmt.Fprintf(os.Stderr, "No session found. Open a page first with 'browser open <url>'.\n")
			os.Exit(1)
		}

		html, err := browser.GetHTML(sess, browserPageName, browserSelector)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get HTML: %v\n", err)
			os.Exit(1)
		}

		fmt.Println(html)
	},
}

var browserGetTextCmd = &cobra.Command{
	Use:   "get-text <selector>",
	Short: "Get text",
	Long:  "Gets the text content of a specific element.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		selector := args[0]
		sheepName := browserSheepName
		if sheepName == "" {
			sheepName = names.ManagerName
		}

		mgr := browser.GetManager()
		sess := mgr.GetSession(sheepName)
		if sess == nil {
			fmt.Fprintf(os.Stderr, "No session found. Open a page first with 'browser open <url>'.\n")
			os.Exit(1)
		}

		text, err := browser.GetText(sess, browserPageName, selector)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to get text: %v\n", err)
			os.Exit(1)
		}

		fmt.Println(text)
	},
}

var browserCloseCmd = &cobra.Command{
	Use:   "close",
	Short: "Close browser session",
	Long:  "Closes the browser session.",
	Run: func(cmd *cobra.Command, args []string) {
		sheepName := browserSheepName
		if sheepName == "" {
			sheepName = names.ManagerName
		}

		mgr := browser.GetManager()
		if err := mgr.CloseSession(sheepName); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to close session: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("🌐 Browser session closed (%s)\n", sheepName)
	},
}

var browserListCmd = &cobra.Command{
	Use:   "list",
	Short: "List open pages",
	Long:  "Displays a list of currently open pages.",
	Run: func(cmd *cobra.Command, args []string) {
		sheepName := browserSheepName
		if sheepName == "" {
			sheepName = names.ManagerName
		}

		mgr := browser.GetManager()
		sess := mgr.GetSession(sheepName)
		if sess == nil {
			fmt.Println("No open sessions.")
			return
		}

		pages := sess.ListPages()
		if len(pages) == 0 {
			fmt.Println("No open pages.")
			return
		}

		fmt.Printf("Open pages (%d):\n", len(pages))
		for _, p := range pages {
			defaultMark := ""
			if p.IsDefault {
				defaultMark = " (default)"
			}
			fmt.Printf("  🌐 %s%s\n", p.Name, defaultMark)
			fmt.Printf("     %s\n", p.URL)
		}
	},
}

var browserScreenshotCmd = &cobra.Command{
	Use:   "screenshot [path]",
	Short: "Capture screenshot",
	Long:  "Captures a screenshot of the current page.",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		sheepName := browserSheepName
		if sheepName == "" {
			sheepName = names.ManagerName
		}

		path := ""
		if len(args) > 0 {
			path = args[0]
		}

		mgr := browser.GetManager()
		sess := mgr.GetSession(sheepName)
		if sess == nil {
			fmt.Fprintf(os.Stderr, "No session found. Open a page first with 'browser open <url>'.\n")
			os.Exit(1)
		}

		data, err := browser.Screenshot(sess, browserPageName, browserSelector, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to capture screenshot: %v\n", err)
			os.Exit(1)
		}

		if path != "" {
			fmt.Printf("📸 Screenshot saved: %s (%d bytes)\n", path, len(data))
		} else {
			fmt.Printf("📸 Screenshot captured (%d bytes)\n", len(data))
		}
	},
}

var browserFetchCmd = &cobra.Command{
	Use:   "fetch <url>",
	Short: "Open URL and fetch content",
	Long:  "Opens a URL and fetches the rendered HTML or text. The session is closed automatically.",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		url := args[0]
		sheepName := browserSheepName
		if sheepName == "" {
			sheepName = names.ManagerName
		}

		mgr := browser.GetManager()
		sess, err := mgr.GetOrCreateSession(sheepName, &browser.SessionOptions{
			Headless: browserHeadless,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create browser session: %v\n", err)
			os.Exit(1)
		}
		defer mgr.CloseSession(sheepName)

		_, err = sess.OpenPage(url, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to open page: %v\n", err)
			os.Exit(1)
		}

		if browserSelector != "" {
			// Get text of a specific element
			text, err := browser.GetText(sess, "", browserSelector)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to get text: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(text)
		} else {
			// Get full HTML
			html, err := browser.GetHTML(sess, "", "")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to get HTML: %v\n", err)
				os.Exit(1)
			}
			fmt.Println(html)
		}
	},
}

// tui command
// serve command
var serveDaemon bool
var serveCORSOrigin string

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Shepherd daemon server",
	Long: `Starts the Shepherd daemon with REST API server.
The daemon runs the task queue processor and serves the API.

Use --daemon / -d to run in background.`,
	Run: func(cmd *cobra.Command, args []string) {
		// Check if already running
		if daemon.IsRunning() {
			pid, _ := daemon.ReadPID()
			fmt.Printf("⚠️  Shepherd daemon is already running (PID: %d)\n", pid)
			os.Exit(1)
		}

		// Background mode: re-exec with serve-foreground hidden command
		if serveDaemon {
			exe, _ := os.Executable()
			child := exec.Command(exe, "serve-foreground")
			child.Stdout = nil
			child.Stderr = nil
			child.Stdin = nil
			child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			envutil.SetCleanEnv(child)
			// Forward --cors-origin flag as env var to daemon process
			if serveCORSOrigin != "" {
				child.Env = append(child.Env, "SHEPHERD_CORS_ORIGIN="+serveCORSOrigin)
			}
			if err := child.Start(); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to start daemon: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("🐑 Shepherd daemon started (PID: %d)\n", child.Process.Pid)
			return
		}

		// Foreground mode (when called without -d)
		runServeForeground()
	},
}

var serveForegroundCmd = &cobra.Command{
	Use:    "serve-foreground",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		runServeForeground()
	},
}

func runServeForeground() {
	// Recover stuck tasks/sheep
	recoverFromAbnormalTermination()

	// Check auth setup
	if config.GetString("auth_username") == "" {
		fmt.Println("⚠️  Authentication not configured. Run 'shepherd auth setup' first.")
		fmt.Println("   API will accept all requests without authentication.")
	}

	// Ensure JWT secret exists — auto-generate if missing
	if config.GetString("auth_jwt_secret") == "" {
		secretBytes := make([]byte, 32)
		if _, err := cryptoRand.Read(secretBytes); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to generate JWT secret: %v\n", err)
			os.Exit(1)
		}
		jwtSecret := fmt.Sprintf("%x", secretBytes)
		config.Set("auth_jwt_secret", jwtSecret)
		fmt.Println("🔑 JWT secret auto-generated and saved to config")
	}

	// Start queue processor
	processor := queue.NewProcessor(2 * time.Second)
	processor.Start()
	defer processor.Stop()

	// Start scheduler (checks schedules every 1 minute)
	sched := scheduler.New(1 * time.Minute)
	sched.Start()
	defer sched.Stop()

	// Seed bundled skills
	if err := skill.SeedBundledSkills(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to seed bundled skills: %v\n", err)
	}

	// Check CORS origin configuration
	corsOrigin := serveCORSOrigin
	if corsOrigin == "" {
		corsOrigin = os.Getenv("SHEPHERD_CORS_ORIGIN")
	}
	if corsOrigin == "" || corsOrigin == "*" {
		fmt.Println("⚠️  CORS origin not configured. Set SHEPHERD_CORS_ORIGIN or use --cors-origin for production use.")
	}

	// Create and start server with embedded web UI
	server.Version = version
	srv := server.New(processor, sched, server.WebDistFS(), serveCORSOrigin)
	srv.WireProcessorCallbacks()
	srv.WireSchedulerCallbacks()

	// Write PID
	if err := daemon.WritePID(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  Failed to write PID file: %v\n", err)
	}
	defer daemon.RemovePID()

	addr := fmt.Sprintf("%s:%d", config.GetString("server_host"), config.GetInt("server_port"))
	fmt.Printf("🐑 Shepherd daemon starting on %s\n", addr)

	// Start server in goroutine
	go func() {
		if err := srv.Listen(addr); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Server error: %v\n", err)
			os.Exit(1)
		}
	}()

	// Wait for signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh

	fmt.Printf("\n🛑 Signal received (%v), shutting down...\n", sig)

	// Cleanup
	sheepCount, _ := worker.RecoverStuckSheep()
	if sheepCount > 0 {
		fmt.Printf("🐏 %d sheep status recovered\n", sheepCount)
	}
	taskCount, _ := queue.RecoverStuckTasks()
	if taskCount > 0 {
		fmt.Printf("📋 %d tasks interrupted\n", taskCount)
	}

	srv.Shutdown()
	db.Close()
	os.Exit(0)
}

var serveStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running Shepherd daemon",
	Run: func(cmd *cobra.Command, args []string) {
		if !daemon.IsRunning() {
			fmt.Println("ℹ️  Shepherd daemon is not running")
			return
		}
		if err := daemon.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			os.Exit(1)
		}
		fmt.Println("🛑 Shepherd daemon stopped")
	},
}

var serveStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check Shepherd daemon status",
	Run: func(cmd *cobra.Command, args []string) {
		pid, running := daemon.GetStatus()
		if running {
			fmt.Printf("🟢 Shepherd daemon is running (PID: %d)\n", pid)
		} else {
			fmt.Println("🔴 Shepherd daemon is not running")
		}
	},
}

// auth command
var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication",
}

var authSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup authentication credentials",
	Run: func(cmd *cobra.Command, args []string) {
		reader := readline.NewCancelableStdin(os.Stdin)
		defer reader.Close()

		// Username
		fmt.Print("Username (default: admin): ")
		var username string
		fmt.Scanln(&username)
		if username == "" {
			username = "admin"
		}

		// Password (with echo disabled)
		fmt.Print("Password: ")
		passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to read password: %v\n", err)
			os.Exit(1)
		}
		password := string(passwordBytes)

		// Confirm password
		fmt.Print("Confirm password: ")
		confirmBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to read password: %v\n", err)
			os.Exit(1)
		}

		if password != string(confirmBytes) {
			fmt.Fprintln(os.Stderr, "❌ Passwords do not match")
			os.Exit(1)
		}

		// Generate bcrypt hash
		hash, err := server.HashPassword(password)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to hash password: %v\n", err)
			os.Exit(1)
		}

		// Generate JWT secret if not set
		jwtSecret := config.GetString("auth_jwt_secret")
		if jwtSecret == "" {
			secretBytes := make([]byte, 32)
			if _, err := cryptoRand.Read(secretBytes); err != nil {
				fmt.Fprintf(os.Stderr, "❌ Failed to generate JWT secret: %v\n", err)
				os.Exit(1)
			}
			jwtSecret = fmt.Sprintf("%x", secretBytes)
		}

		// Save config
		config.Set("auth_username", username)
		config.Set("auth_password_hash", hash)
		config.Set("auth_jwt_secret", jwtSecret)

		fmt.Println("✅ Authentication configured successfully")
		fmt.Printf("   Username: %s\n", username)
	},
}

var authChangePasswordCmd = &cobra.Command{
	Use:   "change-password",
	Short: "Change authentication password",
	Run: func(cmd *cobra.Command, args []string) {
		storedHash := config.GetString("auth_password_hash")
		if storedHash == "" {
			fmt.Fprintln(os.Stderr, "❌ No password configured. Run 'shepherd auth setup' first")
			os.Exit(1)
		}

		// Current password
		fmt.Print("Current password: ")
		currentBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to read password: %v\n", err)
			os.Exit(1)
		}

		if err := server.ComparePassword(storedHash, string(currentBytes)); err != nil {
			fmt.Fprintln(os.Stderr, "❌ Current password is incorrect")
			os.Exit(1)
		}

		// New password
		fmt.Print("New password: ")
		newBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to read password: %v\n", err)
			os.Exit(1)
		}

		fmt.Print("Confirm new password: ")
		confirmBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to read password: %v\n", err)
			os.Exit(1)
		}

		if string(newBytes) != string(confirmBytes) {
			fmt.Fprintln(os.Stderr, "❌ Passwords do not match")
			os.Exit(1)
		}

		hash, err := server.HashPassword(string(newBytes))
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to hash password: %v\n", err)
			os.Exit(1)
		}

		config.Set("auth_password_hash", hash)
		fmt.Println("✅ Password changed successfully")
	},
}

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Run TUI mode",
	Long: `Runs the TUI (Terminal User Interface) mode.
Monitor and manage multiple sheep tasks in real-time.

Key bindings:
  1         Split view (view all sheep simultaneously)
  2         Dashboard view (list + details)
  Up/Down   Navigate project list
  Tab       Switch panels
  Enter     Input/execute command
  Esc       Cancel input
  q         Quit`,
	Run: func(cmd *cobra.Command, args []string) {
		// Recover from abnormal termination before TUI starts
		recoverFromAbnormalTermination()

		if err := tui.Run(); err != nil {
			fmt.Fprintf(os.Stderr, i18n.T().CLITUIErrorFmt, err)
			os.Exit(1)
		}
	},
}

// recover command
var recoverCmd = &cobra.Command{
	Use:   "recover",
	Short: i18n.T().CLIRecoverShort,
	Long:  i18n.T().CLIRecoverLong,
	Run: func(cmd *cobra.Command, args []string) {
		sheepCount, err := worker.RecoverStuckSheep()
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.T().CLISheepRecoverFailFmt, err)
			os.Exit(1)
		}

		taskCount, err := queue.RecoverStuckTasks()
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.T().CLITaskRecoverFailFmt, err)
			os.Exit(1)
		}

		if sheepCount == 0 && taskCount == 0 {
			fmt.Println(i18n.T().CLINothingToRecover)
		} else {
			if sheepCount > 0 {
				fmt.Printf(i18n.T().CLISheepRecoveredFmt, sheepCount)
			}
			if taskCount > 0 {
				fmt.Printf(i18n.T().CLITaskRecoveredFmt, taskCount)
			}
		}
	},
}

// init command (shepherd init)
var initCmd = &cobra.Command{
	Use:   "init [name]",
	Short: i18n.T().CLIInitShort,
	Long:  i18n.T().CLIInitLong,
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.T().CLIGetCwdFailFmt, err)
			os.Exit(1)
		}

		projectName := filepath.Base(cwd)
		if len(args) > 0 {
			projectName = args[0]
		}

		_, err = project.Add(projectName, cwd, "")
		if err != nil {
			if !strings.Contains(err.Error(), "already exists") {
				fmt.Fprintf(os.Stderr, i18n.T().CLIProjectAddFailFmt, err)
				os.Exit(1)
			}
			fmt.Printf(i18n.T().CLIProjectAlreadyFmt, projectName)
		} else {
			fmt.Printf(i18n.T().CLIProjectRegisteredFmt, projectName, cwd)
		}

		sheepList, err := worker.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, i18n.T().CLISheepListFailFmt, err)
			os.Exit(1)
		}

		var sheepName string
		if len(sheepList) == 0 {
			s, err := worker.Create("")
			if err != nil {
				fmt.Fprintf(os.Stderr, i18n.T().CLISheepCreateFailFmt, err)
				os.Exit(1)
			}
			sheepName = s.Name
			fmt.Printf(i18n.T().CLISheepCreatedFmt, sheepName)
		} else {
			for _, s := range sheepList {
				if s.Edges.Project == nil {
					sheepName = s.Name
					break
				}
			}
			if sheepName == "" {
				sheepName = sheepList[0].Name
			}
		}

		if err := project.AssignSheep(projectName, sheepName); err != nil {
			if !strings.Contains(err.Error(), "already") {
				fmt.Fprintf(os.Stderr, i18n.T().CLISheepAssignFailFmt, err)
				os.Exit(1)
			}
		} else {
			fmt.Printf(i18n.T().CLISheepAssignedFmt, projectName, sheepName)
		}

		fmt.Println()
		fmt.Println(i18n.T().CLIInitReady)
		fmt.Println(i18n.T().CLIInitExample)
	},
}

func init() {
	rootCmd.Version = version
	rootCmd.SetVersionTemplate(fmt.Sprintf("shepherd version {{.Version}} (built %s)\n", buildTime))

	// Register config commands
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configPathCmd)
	rootCmd.AddCommand(configCmd)

	// Register spawn command
	spawnCmd.Flags().StringVarP(&spawnName, "name", "n", "", i18n.T().CLIFlagSheepName)
	spawnCmd.Flags().StringVarP(&spawnProvider, "provider", "p", "claude", "AI provider (claude, opencode, auto)")
	rootCmd.AddCommand(spawnCmd)

	// Register flock command
	rootCmd.AddCommand(flockCmd)

	// Register set-provider command
	rootCmd.AddCommand(setProviderCmd)

	// Register recall command
	recallCmd.Flags().BoolVarP(&recallAll, "all", "a", false, i18n.T().CLIFlagRecallAll)
	rootCmd.AddCommand(recallCmd)

	// Register names command
	namesCmd.AddCommand(namesListCmd)
	namesCmd.AddCommand(namesAddCmd)
	namesCmd.AddCommand(namesRemoveCmd)
	rootCmd.AddCommand(namesCmd)

	// Register project command
	projectAddCmd.Flags().StringVarP(&projectAddDesc, "desc", "d", "", i18n.T().CLIFlagProjectDesc)
	projectCmd.AddCommand(projectAddCmd)
	projectCmd.AddCommand(projectListCmd)
	projectCmd.AddCommand(projectRemoveCmd)
	projectCmd.AddCommand(projectAssignCmd)
	projectCmd.AddCommand(projectUnassignCmd)
	rootCmd.AddCommand(projectCmd)

	// Register skill command
	skillAddCmd.Flags().StringVarP(&skillAddFile, "file", "f", "", "Path to markdown skill file (required)")
	skillAddCmd.Flags().StringVarP(&skillAddProject, "project", "p", "", "Associate with a project (sets scope to project)")
	skillAddCmd.Flags().StringVarP(&skillAddDesc, "desc", "d", "", "Skill description")
	skillAddCmd.Flags().StringVarP(&skillAddTags, "tags", "t", "", "Comma-separated tags")
	skillListCmd.Flags().StringVarP(&skillListProject, "project", "p", "", "Filter by project name")
	skillCmd.AddCommand(skillListCmd)
	skillCmd.AddCommand(skillAddCmd)
	skillCmd.AddCommand(skillRemoveCmd)
	skillCmd.AddCommand(skillShowCmd)
	skillCmd.AddCommand(skillEnableCmd)
	skillCmd.AddCommand(skillDisableCmd)
	rootCmd.AddCommand(skillCmd)

	// Register task command
	taskCmd.AddCommand(taskStopCmd)
	taskCmd.AddCommand(taskCancelAllCmd)
	rootCmd.AddCommand(taskCmd)

	// Register queue command
	queueCmd.AddCommand(queueAddCmd)
	queueCmd.AddCommand(queueListCmd)
	queueCmd.AddCommand(queueCancelCmd)
	queueCmd.AddCommand(queueClearCmd)
	queueCmd.AddCommand(queueImportIssuesCmd)
	rootCmd.AddCommand(queueCmd)

	// Register status command
	rootCmd.AddCommand(statusCmd)

	// Register log command
	logCmd.Flags().IntVarP(&logLimit, "limit", "n", 20, i18n.T().CLIFlagLogLimit)
	rootCmd.AddCommand(logCmd)

	// Register history command
	rootCmd.AddCommand(historyCmd)

	// Register mcp command
	mcpCmd.Flags().Bool("minimal", false, "Expose only core task tools (no browser tools), for LLMs with small context windows")
	rootCmd.AddCommand(mcpCmd)

	// Register browser command
	browserCmd.PersistentFlags().StringVarP(&browserSheepName, "sheep", "s", "", i18n.T().CLIFlagBrowserSheep)
	browserCmd.PersistentFlags().StringVarP(&browserPageName, "page", "p", "", i18n.T().CLIFlagBrowserPage)
	browserCmd.PersistentFlags().BoolVar(&browserHeadless, "headless", true, i18n.T().CLIFlagBrowserHead)
	browserOpenCmd.Flags().StringVar(&browserSelector, "selector", "", i18n.T().CLIFlagBrowserWait)
	browserGetHTMLCmd.Flags().StringVar(&browserSelector, "selector", "", "CSS selector (all if empty)")
	browserScreenshotCmd.Flags().StringVar(&browserSelector, "selector", "", i18n.T().CLIFlagBrowserCapture)
	browserFetchCmd.Flags().StringVar(&browserSelector, "selector", "", "CSS selector (text if set, HTML if empty)")
	browserCmd.AddCommand(browserOpenCmd)
	browserCmd.AddCommand(browserGetHTMLCmd)
	browserCmd.AddCommand(browserGetTextCmd)
	browserCmd.AddCommand(browserCloseCmd)
	browserCmd.AddCommand(browserListCmd)
	browserCmd.AddCommand(browserScreenshotCmd)
	browserCmd.AddCommand(browserFetchCmd)
	rootCmd.AddCommand(browserCmd)

	// Register serve command
	serveCmd.Flags().BoolVarP(&serveDaemon, "daemon", "d", false, "Run as background daemon")
	serveCmd.Flags().StringVar(&serveCORSOrigin, "cors-origin", "", "Allowed CORS origins (comma-separated, overrides SHEPHERD_CORS_ORIGIN)")
	serveCmd.AddCommand(serveStopCmd)
	serveCmd.AddCommand(serveStatusCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(serveForegroundCmd)

	// Register auth command
	authCmd.AddCommand(authSetupCmd)
	authCmd.AddCommand(authChangePasswordCmd)
	rootCmd.AddCommand(authCmd)

	// Register tui command
	rootCmd.AddCommand(tuiCmd)

	// Register recover command
	rootCmd.AddCommand(recoverCmd)

	// Register init command
	rootCmd.AddCommand(initCmd)
}

func main() {
	if err := config.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize config: %v\n", err)
		os.Exit(1)
	}

	i18n.Init(config.GetString("language"))

	if err := db.Init(); err != nil {
		fmt.Fprintf(os.Stderr, i18n.T().CLIDBInitFailFmt, err)
		os.Exit(1)
	}
	defer db.Close()

	setupGracefulShutdown()

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// recoverFromAbnormalTermination recovers stuck sheep and tasks from previous abnormal termination.
func recoverFromAbnormalTermination() {
	sheepCount, err := worker.RecoverStuckSheep()
	if err != nil {
		fmt.Fprintf(os.Stderr, i18n.T().CLIWarnSheepRecoverFmt, err)
	} else if sheepCount > 0 {
		fmt.Printf(i18n.T().CLISheepRecoveredInfoFmt, sheepCount)
	}

	taskCount, err := queue.RecoverStuckTasks()
	if err != nil {
		fmt.Fprintf(os.Stderr, i18n.T().CLIWarnTaskRecoverFmt, err)
	} else if taskCount > 0 {
		fmt.Printf(i18n.T().CLITaskRecoveredInfoFmt, taskCount)
	}
}

// setupGracefulShutdown sets up signal handlers for graceful shutdown.
func setupGracefulShutdown() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		fmt.Printf(i18n.T().CLISignalReceivedFmt, sig)

		count, _ := worker.RecoverStuckSheep()
		if count > 0 {
			fmt.Printf(i18n.T().CLISheepCleanedFmt, count)
		}

		taskCount, _ := queue.RecoverStuckTasks()
		if taskCount > 0 {
			fmt.Printf(i18n.T().CLITaskInterruptedFmt, taskCount)
		}

		db.Close()
		os.Exit(0)
	}()
}
