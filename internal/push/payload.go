package push

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/agurrrrr/shepherd/internal/i18n"
)

// Payload is the JSON body delivered to the service worker `push` event.
type Payload struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
	Tag    string `json:"tag"`
	Status string `json:"status"`
}

const bodyMaxRunes = 120

// CompletePayload builds a notification for a finished task.
func CompletePayload(taskID int, projectName, summary string) Payload {
	return Payload{
		Title:  title(i18n.T().Push.TaskComplete, projectName),
		Body:   bodyLine(taskID, summary),
		URL:    fmt.Sprintf("/tasks/%d", taskID),
		Tag:    fmt.Sprintf("task-%d", taskID),
		Status: "completed",
	}
}

// FailPayload builds a notification for a failed task.
func FailPayload(taskID int, projectName, errMsg string) Payload {
	return Payload{
		Title:  title(i18n.T().Push.TaskFailed, projectName),
		Body:   bodyLine(taskID, errMsg),
		URL:    fmt.Sprintf("/tasks/%d", taskID),
		Tag:    fmt.Sprintf("task-%d", taskID),
		Status: "failed",
	}
}

// TestPayload is used by POST /api/push/test.
func TestPayload() Payload {
	return Payload{
		Title:  "Shepherd",
		Body:   i18n.T().Push.TestBody,
		URL:    "/settings",
		Tag:    "shepherd-test",
		Status: "test",
	}
}

// ShepherdReportPayload builds the "목자 보고" notification for a finished
// shepherd task (#602). The report body IS that task's result, so #623 links
// straight to the task page with from=shepherd&tab=history — the back link
// there returns to the 목자 작업 tab.
func ShepherdReportPayload(taskID int, kind, report string) Payload {
	return Payload{
		Title:  ShepherdReportLabel(kind),
		Body:   bodyLine(taskID, report),
		URL:    fmt.Sprintf("/tasks/%d?from=shepherd&tab=history", taskID),
		Tag:    fmt.Sprintf("shepherd-report-%d", taskID),
		Status: "completed",
	}
}

// ShepherdReportLabel renders the "목자 보고" title (with the kind suffix) in
// the current language. Discord's report notifier shares it (#665).
func ShepherdReportLabel(kind string) string {
	switch kind {
	case "review":
		return i18n.T().Push.ReportReview
	case "patrol":
		return i18n.T().Push.ReportPatrol
	case "command":
		return i18n.T().Push.ReportCommand
	default:
		return i18n.T().Push.ShepherdReport
	}
}

func title(kind, projectName string) string {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" || projectName == "-" {
		return "Shepherd · " + kind
	}
	return "Shepherd · " + kind + " · " + projectName
}

func bodyLine(taskID int, text string) string {
	text = collapseSpace(text)
	if text == "" {
		return fmt.Sprintf("#%d", taskID)
	}
	return truncateRunes(fmt.Sprintf("#%d %s", taskID, text), bodyMaxRunes)
}

func collapseSpace(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}
