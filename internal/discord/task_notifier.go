package discord

import (
	"fmt"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/internal/config"
)

const (
	colorGreen  = 0x00FF00
	colorRed    = 0xFF0000
	colorOrange = 0xFFA500
)

// TaskNotifier handles task completion/failure notifications to Discord.
type TaskNotifier struct {
	notifier *Notifier
}

// NewTaskNotifier creates a new task notifier.
func NewTaskNotifier() *TaskNotifier {
	return &TaskNotifier{
		notifier: New(),
	}
}

// SendTaskComplete sends a task completion notification.
func (n *TaskNotifier) SendTaskComplete(taskID int, sheepName, projectName, summary string, costUSD float64, filesModified []string) {
	if !config.GetBool("discord_notifications_enabled") {
		return
	}
	if !config.GetBool("discord_notify_on_complete") {
		return
	}

	webhookURL := config.GetString("discord_webhook_url")
	if webhookURL == "" {
		return
	}

	truncate := func(s string, max int) string {
		r := strings.ReplaceAll(s, "\n", " ")
		r = strings.ReplaceAll(r, "\r", "")
		r = strings.Join(strings.Fields(r), " ")
		if len(r) > max {
			return r[:max] + "..."
		}
		return r
	}

	// Shepherd tasks have no project (#594); Discord rejects empty embed values.
	if projectName == "" {
		projectName = "-"
	}

	fields := []EmbedField{
		{Name: "Task", Value: fmt.Sprintf("#%d", taskID), Inline: true},
		{Name: "Sheep", Value: sheepName, Inline: true},
		{Name: "Project", Value: projectName, Inline: true},
	}

	if costUSD > 0 {
		fields = append(fields, EmbedField{Name: "Cost", Value: fmt.Sprintf("$%.4f", costUSD), Inline: true})
	}

	if len(filesModified) > 0 {
		fileStr := strings.Join(filesModified, ", ")
		if len(fileStr) > 100 {
			fileStr = fileStr[:97] + "..."
		}
		fields = append(fields, EmbedField{Name: "Files", Value: "`" + fileStr + "`", Inline: false})
	}

	embed := Embed{
		Title:       "Task Completed",
		Description: truncate(summary, 1000),
		Color:       colorGreen,
		Fields:      fields,
		Footer:      &EmbedFooter{Text: "Shepherd"},
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := n.notifier.Send(webhookURL, "", []Embed{embed}); err != nil {
		// Silent fail - don't block task flow
		_ = err
	}
}

// SendShepherdReport sends the "목자 보고" notification for a finished
// shepherd (목자) task (#602). It replaces the generic completion notice for
// those tasks: the title names the kind (검토/순찰/지시) and the body carries
// the shepherd's own report. Gate: the discord master switch only — the
// per-report decision (shepherd_notify, quiet patrol) is made by manager
// before calling this.
func (n *TaskNotifier) SendShepherdReport(taskID int, kind, report string, costUSD float64) {
	if !config.GetBool("discord_notifications_enabled") {
		return
	}

	webhookURL := config.GetString("discord_webhook_url")
	if webhookURL == "" {
		return
	}

	truncate := func(s string, max int) string {
		r := strings.ReplaceAll(s, "\n", " ")
		r = strings.ReplaceAll(r, "\r", "")
		r = strings.Join(strings.Fields(r), " ")
		if len(r) > max {
			return r[:max] + "..."
		}
		return r
	}

	fields := []EmbedField{
		{Name: "Report", Value: shepherdReportLabel(kind), Inline: true},
		{Name: "Task", Value: fmt.Sprintf("#%d", taskID), Inline: true},
	}
	if costUSD > 0 {
		fields = append(fields, EmbedField{Name: "Cost", Value: fmt.Sprintf("$%.4f", costUSD), Inline: true})
	}

	embed := Embed{
		Title:       "🐑 " + shepherdReportLabel(kind),
		Description: truncate(report, 1500),
		Color:       0x6C8AE4, // periwinkle — the shepherd's own colour
		Fields:      fields,
		Footer:      &EmbedFooter{Text: "Shepherd"},
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := n.notifier.Send(webhookURL, "", []Embed{embed}); err != nil {
		// Silent fail - don't block task flow
		_ = err
	}
}

// shepherdReportLabel renders the Korean title for one shepherd report kind.
func shepherdReportLabel(kind string) string {
	switch kind {
	case "review":
		return "목자 보고 · 검토"
	case "patrol":
		return "목자 보고 · 순찰"
	case "command":
		return "목자 보고 · 지시"
	default:
		return "목자 보고"
	}
}

// SendHandoffDepthAlert warns when a task's context-overflow handoff chain has
// grown deep — a sign the model may be looping without making progress. It
// shares the master discord_notifications_enabled switch (no separate toggle)
// and the fail-notification gate, since a runaway handoff is a failure-adjacent
// signal worth surfacing.
func (n *TaskNotifier) SendHandoffDepthAlert(sheepName, projectName string, depth int) {
	if !config.GetBool("discord_notifications_enabled") {
		return
	}
	if !config.GetBool("discord_notify_on_fail") {
		return
	}

	webhookURL := config.GetString("discord_webhook_url")
	if webhookURL == "" {
		return
	}

	if projectName == "" {
		projectName = "-" // Discord rejects empty embed field values
	}

	fields := []EmbedField{
		{Name: "Sheep", Value: sheepName, Inline: true},
		{Name: "Project", Value: projectName, Inline: true},
		{Name: "Handoff depth", Value: fmt.Sprintf("%d", depth), Inline: true},
	}

	embed := Embed{
		Title:       "⚠️ Deep handoff chain",
		Description: fmt.Sprintf("A task has handed itself off %d times due to context overflow. It may be stuck in a no-progress loop — consider checking in.", depth),
		Color:       colorOrange,
		Fields:      fields,
		Footer:      &EmbedFooter{Text: "Shepherd"},
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := n.notifier.Send(webhookURL, "", []Embed{embed}); err != nil {
		// Silent fail - don't block task flow
		_ = err
	}
}

// SendTaskFail sends a task failure notification.
func (n *TaskNotifier) SendTaskFail(taskID int, sheepName, projectName, errMsg string) {
	if !config.GetBool("discord_notifications_enabled") {
		return
	}
	if !config.GetBool("discord_notify_on_fail") {
		return
	}

	webhookURL := config.GetString("discord_webhook_url")
	if webhookURL == "" {
		return
	}

	truncate := func(s string, max int) string {
		r := strings.ReplaceAll(s, "\n", " ")
		r = strings.ReplaceAll(r, "\r", "")
		r = strings.Join(strings.Fields(r), " ")
		if len(r) > max {
			return r[:max] + "..."
		}
		return r
	}

	// Shepherd tasks have no project (#594); Discord rejects empty embed values.
	if projectName == "" {
		projectName = "-"
	}

	fields := []EmbedField{
		{Name: "Task", Value: fmt.Sprintf("#%d", taskID), Inline: true},
		{Name: "Sheep", Value: sheepName, Inline: true},
		{Name: "Project", Value: projectName, Inline: true},
	}

	embed := Embed{
		Title:       "Task Failed",
		Description: truncate(errMsg, 1000),
		Color:       colorRed,
		Fields:      fields,
		Footer:      &EmbedFooter{Text: "Shepherd"},
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
	}

	if err := n.notifier.Send(webhookURL, "", []Embed{embed}); err != nil {
		// Silent fail - don't block task flow
		_ = err
	}
}
