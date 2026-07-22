package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/issue"
	"github.com/spf13/cobra"
)

// LLM-friendly issue CLI: stable arg order, rich --help examples, optional --json.

var (
	issueJSON bool // global for issue command group

	// create
	issueCreateTitle string
	issueCreateType  string
	issueCreateBody  string
	issueCreateGoal  string

	// list
	issueListStatus string
	issueListType   string
	issueListQuery  string
	issueListLimit  int
	issueListPage   int
	issueListAsc    bool

	// update
	issueUpdateTitle  string
	issueUpdateType   string
	issueUpdateBody   string
	issueUpdateGoal   string
	issueUpdateStatus string

	// delete
	issueDeleteYes bool

	// execute
	issueExecSheep string
	issueExecModel string
)

var issueCmd = &cobra.Command{
	Use:   "issue",
	Short: "Manage project issues (design / feature / bug)",
	Long: `Manage shepherd built-in issues for a project.

Issues track work items (design, feature, bug) with status and optional goal.
Use "execute" to enqueue a task that implements an issue.

Typical LLM workflow:
  1. shepherd issue create <project> --title "..." --type bug --body "..." --goal "..."
  2. shepherd issue list <project> --status todo --json
  3. shepherd issue show <project> <id> --json
  4. shepherd issue execute <project> <id>
  5. shepherd issue update <project> <id> --status done

Types:    design | feature | bug
Statuses: todo | in_progress | testing | failed | done

All subcommands accept --json for machine-readable output.`,
}

var issueCreateCmd = &cobra.Command{
	Use:   "create <project>",
	Short: "Create an issue",
	Long: `Create a new issue under a project.

Examples:
  shepherd issue create shepherd --title "Add issue CLI" --type feature
  shepherd issue create shepherd -t "Login broken" --type bug --body "401 on refresh" --goal "Refresh works"
  shepherd issue create shepherd -t "API design" --type design --json`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]
		iss, err := issue.Create(issue.CreateInput{
			Project: projectName,
			Title:   issueCreateTitle,
			Type:    issueCreateType,
			Body:    issueCreateBody,
			Goal:    issueCreateGoal,
		})
		if err != nil {
			exitIssueErr(err)
		}
		if issueJSON {
			printIssueJSON(map[string]interface{}{
				"id":         iss.ID,
				"project":    projectName,
				"title":      iss.Title,
				"type":       string(iss.Type),
				"status":     string(iss.Status),
				"body":       iss.Body,
				"goal":       iss.Goal,
				"created_at": issue.FormatTime(iss.CreatedAt),
			})
			return
		}
		fmt.Printf("Created issue #%d [%s] %s (status: %s)\n", iss.ID, iss.Type, iss.Title, iss.Status)
	},
}

var issueListCmd = &cobra.Command{
	Use:   "list <project>",
	Short: "List issues for a project",
	Long: `List issues with optional filters.

Examples:
  shepherd issue list shepherd
  shepherd issue list shepherd --status todo
  shepherd issue list shepherd --type bug --query "login"
  shepherd issue list shepherd --status in_progress --json
  shepherd issue list shepherd --limit 50 --page 1`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]
		result, err := issue.List(issue.ListFilter{
			Project: projectName,
			Status:  issueListStatus,
			Type:    issueListType,
			Query:   issueListQuery,
			Page:    issueListPage,
			Limit:   issueListLimit,
			SortAsc: issueListAsc,
		})
		if err != nil {
			exitIssueErr(err)
		}

		if issueJSON {
			items := make([]map[string]interface{}, 0, len(result.Items))
			for _, iss := range result.Items {
				items = append(items, issueSummaryMap(iss, projectName, result.TaskCounts[iss.ID]))
			}
			printIssueJSON(map[string]interface{}{
				"project":     projectName,
				"items":       items,
				"total":       result.Total,
				"page":        result.Page,
				"limit":       result.Limit,
				"total_pages": result.TotalPages,
			})
			return
		}

		if len(result.Items) == 0 {
			fmt.Printf("No issues found in project %q.\n", projectName)
			return
		}

		fmt.Printf("Issues in %q (%d total, page %d/%d):\n\n", projectName, result.Total, result.Page, max(1, result.TotalPages))
		fmt.Printf("%-6s %-12s %-12s %-5s %s\n", "ID", "STATUS", "TYPE", "TASKS", "TITLE")
		fmt.Println(strings.Repeat("-", 80))
		for _, iss := range result.Items {
			fmt.Printf("%-6d %-12s %-12s %-5d %s\n",
				iss.ID, iss.Status, iss.Type, result.TaskCounts[iss.ID], iss.Title)
		}
	},
}

var issueShowCmd = &cobra.Command{
	Use:   "show <project> <id>",
	Short: "Show issue detail (including linked tasks)",
	Long: `Show a single issue with body, goal, and linked tasks.

Examples:
  shepherd issue show shepherd 12
  shepherd issue show shepherd 12 --json`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]
		id, err := strconv.Atoi(args[1])
		if err != nil {
			exitIssueErr(fmt.Errorf("invalid issue id %q", args[1]))
		}
		iss, err := issue.Get(projectName, id)
		if err != nil {
			exitIssueErr(err)
		}

		if issueJSON {
			printIssueJSON(issueDetailMap(iss, projectName))
			return
		}

		fmt.Printf("Issue #%d — %s\n", iss.ID, iss.Title)
		fmt.Printf("  Project:  %s\n", projectName)
		fmt.Printf("  Type:     %s\n", iss.Type)
		fmt.Printf("  Status:   %s\n", iss.Status)
		fmt.Printf("  Created:  %s\n", issue.FormatTime(iss.CreatedAt))
		fmt.Printf("  Updated:  %s\n", issue.FormatTime(iss.UpdatedAt))
		if s := issue.FormatTimePtr(iss.StartedAt); s != "" {
			fmt.Printf("  Started:  %s\n", s)
		}
		if s := issue.FormatTimePtr(iss.CompletedAt); s != "" {
			fmt.Printf("  Completed:%s\n", s)
		}
		if iss.Body != "" {
			fmt.Printf("\n## Body\n%s\n", iss.Body)
		}
		if iss.Goal != "" {
			fmt.Printf("\n## Goal\n%s\n", iss.Goal)
		}
		if len(iss.Edges.Tasks) > 0 {
			fmt.Printf("\n## Linked tasks (%d)\n", len(iss.Edges.Tasks))
			for _, t := range iss.Edges.Tasks {
				summary := t.Summary
				if summary == "" {
					summary = truncateStr(t.Prompt, 60)
				}
				fmt.Printf("  #%d [%s] %s\n", t.ID, t.Status, summary)
			}
		} else {
			fmt.Println("\n## Linked tasks\n  (none)")
		}
	},
}

var issueUpdateCmd = &cobra.Command{
	Use:   "update <project> <id>",
	Short: "Update an issue (partial)",
	Long: `Update fields on an existing issue. Only flags you pass are changed.

Examples:
  shepherd issue update shepherd 12 --status done
  shepherd issue update shepherd 12 --title "New title" --type feature
  shepherd issue update shepherd 12 --body "Updated repro steps" --goal "Tests pass"
  shepherd issue update shepherd 12 --status in_progress --json

Statuses: todo | in_progress | testing | failed | done
Types:    design | feature | bug`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]
		id, err := strconv.Atoi(args[1])
		if err != nil {
			exitIssueErr(fmt.Errorf("invalid issue id %q", args[1]))
		}

		in := issue.UpdateInput{}
		if cmd.Flags().Changed("title") {
			t := issueUpdateTitle
			in.Title = &t
		}
		if cmd.Flags().Changed("type") {
			t := issueUpdateType
			in.Type = &t
		}
		if cmd.Flags().Changed("body") {
			b := issueUpdateBody
			in.Body = &b
		}
		if cmd.Flags().Changed("goal") {
			g := issueUpdateGoal
			in.Goal = &g
		}
		if cmd.Flags().Changed("status") {
			s := issueUpdateStatus
			in.Status = &s
		}

		iss, err := issue.Update(projectName, id, in)
		if err != nil {
			exitIssueErr(err)
		}

		if issueJSON {
			printIssueJSON(map[string]interface{}{
				"id":         iss.ID,
				"project":    projectName,
				"title":      iss.Title,
				"type":       string(iss.Type),
				"status":     string(iss.Status),
				"body":       iss.Body,
				"goal":       iss.Goal,
				"updated_at": issue.FormatTime(iss.UpdatedAt),
			})
			return
		}
		fmt.Printf("Updated issue #%d [%s/%s] %s\n", iss.ID, iss.Type, iss.Status, iss.Title)
	},
}

var issueDeleteCmd = &cobra.Command{
	Use:   "delete <project> <id>",
	Short: "Delete an issue (linked tasks are kept)",
	Long: `Delete an issue. Linked tasks are preserved (issue link cleared).

Examples:
  shepherd issue delete shepherd 12 --yes
  shepherd issue delete shepherd 12 --yes --json`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]
		id, err := strconv.Atoi(args[1])
		if err != nil {
			exitIssueErr(fmt.Errorf("invalid issue id %q", args[1]))
		}
		if !issueDeleteYes {
			exitIssueErr(fmt.Errorf("refusing to delete without --yes (issue #%d)", id))
		}
		if err := issue.Delete(projectName, id); err != nil {
			exitIssueErr(err)
		}
		if issueJSON {
			printIssueJSON(map[string]interface{}{
				"deleted": true,
				"id":      id,
				"project": projectName,
			})
			return
		}
		fmt.Printf("Deleted issue #%d from project %q\n", id, projectName)
	},
}

var issueExecuteCmd = &cobra.Command{
	Use:   "execute <project> <id>",
	Short: "Enqueue a task to implement the issue",
	Long: `Build a prompt from the issue and add a pending task for a sheep.

Sheep resolution order:
  1. --sheep <name> if provided
  2. Project-assigned sheep

The task runs when the shepherd daemon/processor is active (same as queue add).

Examples:
  shepherd issue execute shepherd 12
  shepherd issue execute shepherd 12 --sheep 햄찌
  shepherd issue execute shepherd 12 --model gpt-5 --json`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]
		id, err := strconv.Atoi(args[1])
		if err != nil {
			exitIssueErr(fmt.Errorf("invalid issue id %q", args[1]))
		}
		result, err := issue.Execute(issue.ExecuteInput{
			Project:   projectName,
			IssueID:   id,
			SheepName: issueExecSheep,
			Model:     issueExecModel,
		})
		if err != nil {
			exitIssueErr(err)
		}
		if issueJSON {
			printIssueJSON(map[string]interface{}{
				"task_id":    result.TaskID,
				"sheep_name": result.SheepName,
				"issue_id":   result.IssueID,
				"project":    projectName,
				"status":     "queued",
			})
			return
		}
		fmt.Printf("Enqueued task #%d for issue #%d (sheep: %s)\n", result.TaskID, result.IssueID, result.SheepName)
		fmt.Println("Issue status set to in_progress. Task will run via the queue processor.")
	},
}

func initIssueCmd() {
	issueCmd.PersistentFlags().BoolVar(&issueJSON, "json", false, "Output machine-readable JSON (recommended for LLMs)")

	issueCreateCmd.Flags().StringVarP(&issueCreateTitle, "title", "t", "", "Issue title (required)")
	issueCreateCmd.Flags().StringVar(&issueCreateType, "type", "feature", "Issue type: design | feature | bug")
	issueCreateCmd.Flags().StringVarP(&issueCreateBody, "body", "b", "", "Issue description / body")
	issueCreateCmd.Flags().StringVarP(&issueCreateGoal, "goal", "g", "", "Success criteria (goal)")
	_ = issueCreateCmd.MarkFlagRequired("title")

	issueListCmd.Flags().StringVar(&issueListStatus, "status", "", "Filter by status: todo | in_progress | testing | failed | done")
	issueListCmd.Flags().StringVar(&issueListType, "type", "", "Filter by type: design | feature | bug")
	issueListCmd.Flags().StringVarP(&issueListQuery, "query", "q", "", "Filter title contains query")
	issueListCmd.Flags().IntVarP(&issueListLimit, "limit", "n", 20, "Page size (max 100)")
	issueListCmd.Flags().IntVar(&issueListPage, "page", 1, "Page number (1-based)")
	issueListCmd.Flags().BoolVar(&issueListAsc, "asc", false, "Sort oldest first (default: newest first)")

	issueUpdateCmd.Flags().StringVarP(&issueUpdateTitle, "title", "t", "", "New title")
	issueUpdateCmd.Flags().StringVar(&issueUpdateType, "type", "", "New type: design | feature | bug")
	issueUpdateCmd.Flags().StringVarP(&issueUpdateBody, "body", "b", "", "New body (pass empty string to clear)")
	issueUpdateCmd.Flags().StringVarP(&issueUpdateGoal, "goal", "g", "", "New goal (pass empty string to clear)")
	issueUpdateCmd.Flags().StringVar(&issueUpdateStatus, "status", "", "New status: todo | in_progress | testing | failed | done")

	issueDeleteCmd.Flags().BoolVarP(&issueDeleteYes, "yes", "y", false, "Confirm deletion (required)")

	issueExecuteCmd.Flags().StringVarP(&issueExecSheep, "sheep", "s", "", "Sheep name (default: project-assigned sheep)")
	issueExecuteCmd.Flags().StringVar(&issueExecModel, "model", "", "Optional model override for the task")

	issueCmd.AddCommand(issueCreateCmd)
	issueCmd.AddCommand(issueListCmd)
	issueCmd.AddCommand(issueShowCmd)
	issueCmd.AddCommand(issueUpdateCmd)
	issueCmd.AddCommand(issueDeleteCmd)
	issueCmd.AddCommand(issueExecuteCmd)
}

func exitIssueErr(err error) {
	if issueJSON {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]interface{}{
			"error": err.Error(),
		})
	} else {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	}
	os.Exit(1)
}

func printIssueJSON(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "Error encoding JSON: %v\n", err)
		os.Exit(1)
	}
}

func issueSummaryMap(iss *ent.Issue, project string, taskCount int) map[string]interface{} {
	m := map[string]interface{}{
		"id":         iss.ID,
		"project":    project,
		"title":      iss.Title,
		"type":       string(iss.Type),
		"status":     string(iss.Status),
		"body":       iss.Body,
		"goal":       iss.Goal,
		"task_count": taskCount,
		"created_at": issue.FormatTime(iss.CreatedAt),
		"updated_at": issue.FormatTime(iss.UpdatedAt),
	}
	if s := issue.FormatTimePtr(iss.StartedAt); s != "" {
		m["started_at"] = s
	}
	if s := issue.FormatTimePtr(iss.CompletedAt); s != "" {
		m["completed_at"] = s
	}
	return m
}

func issueDetailMap(iss *ent.Issue, project string) map[string]interface{} {
	m := issueSummaryMap(iss, project, len(iss.Edges.Tasks))
	tasks := make([]map[string]interface{}, 0, len(iss.Edges.Tasks))
	for _, t := range iss.Edges.Tasks {
		tm := map[string]interface{}{
			"id":         t.ID,
			"status":     string(t.Status),
			"summary":    t.Summary,
			"created_at": t.CreatedAt.Format("2006-01-02 15:04:05"),
		}
		if !t.CompletedAt.IsZero() {
			tm["completed_at"] = t.CompletedAt.Format("2006-01-02 15:04:05")
		}
		tasks = append(tasks, tm)
	}
	m["tasks"] = tasks
	return m
}

func truncateStr(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
