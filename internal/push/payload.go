package push

import (
	"fmt"
	"strings"
	"unicode/utf8"
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
		Title:  title("작업 완료", projectName),
		Body:   bodyLine(taskID, summary),
		URL:    fmt.Sprintf("/tasks/%d", taskID),
		Tag:    fmt.Sprintf("task-%d", taskID),
		Status: "completed",
	}
}

// FailPayload builds a notification for a failed task.
func FailPayload(taskID int, projectName, errMsg string) Payload {
	return Payload{
		Title:  title("작업 실패", projectName),
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
		Body:   "테스트 알림입니다. 작업이 끝나면 이런 식으로 알려 드립니다.",
		URL:    "/settings",
		Tag:    "shepherd-test",
		Status: "test",
	}
}

// ShepherdReportPayload builds the "목자 보고" notification for a finished
// shepherd task (#602). It links to the Shepherd console instead of the task
// page, because the report's meaning lives next to the issue board.
func ShepherdReportPayload(taskID int, kind, report string) Payload {
	label := "목자 보고"
	switch kind {
	case "review":
		label = "목자 보고 · 검토"
	case "patrol":
		label = "목자 보고 · 순찰"
	case "command":
		label = "목자 보고 · 지시"
	}
	return Payload{
		Title:  label,
		Body:   bodyLine(taskID, report),
		URL:    "/shepherd",
		Tag:    fmt.Sprintf("shepherd-report-%d", taskID),
		Status: "completed",
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
