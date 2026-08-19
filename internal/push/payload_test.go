package push

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func Test_CompletePayload_빈요약_작업번호만(t *testing.T) {
	got := CompletePayload(12, "shepherd", "   ")
	if got.Title != "Shepherd · 작업 완료 · shepherd" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Body != "#12" {
		t.Errorf("body = %q", got.Body)
	}
	if got.URL != "/tasks/12" || got.Tag != "task-12" || got.Status != "completed" {
		t.Errorf("payload = %+v", got)
	}
}

func Test_FailPayload_프로젝트없음_제목에프로젝트생략(t *testing.T) {
	got := FailPayload(3, "-", "boom")
	if got.Title != "Shepherd · 작업 실패" {
		t.Errorf("title = %q", got.Title)
	}
	if got.Body != "#3 boom" {
		t.Errorf("body = %q", got.Body)
	}
	if got.Status != "failed" {
		t.Errorf("status = %q", got.Status)
	}
}

func Test_CompletePayload_긴요약_룬기준으로자름(t *testing.T) {
	long := strings.Repeat("가", 200)
	got := CompletePayload(1, "p", long)
	if utf8.RuneCountInString(got.Body) != bodyMaxRunes+1 { // 120 + ellipsis
		t.Errorf("rune count = %d, want %d", utf8.RuneCountInString(got.Body), bodyMaxRunes+1)
	}
	if !strings.HasSuffix(got.Body, "…") {
		t.Errorf("body should end with ellipsis: %q", got.Body)
	}
}

func Test_CompletePayload_개행은공백으로(t *testing.T) {
	got := CompletePayload(9, "p", "hello\n\nworld")
	if got.Body != "#9 hello world" {
		t.Errorf("body = %q", got.Body)
	}
}
