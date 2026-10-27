package push

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agurrrrr/shepherd/internal/i18n"
)

// TestMain pins Korean for the wording tests below (they compare exact ko
// strings); individual tests that check en switch the language themselves and
// restore ko afterwards (#665).
func TestMain(m *testing.M) {
	i18n.Init("ko")
	m.Run()
}

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

func Test_ShepherdReportPayload_종류별제목과콘솔링크(t *testing.T) {
	cases := []struct {
		kind  string
		title string
	}{
		{"review", "목자 보고 · 검토"},
		{"patrol", "목자 보고 · 순찰"},
		{"command", "목자 보고 · 지시"},
		{"unknown", "목자 보고"},
	}
	for _, c := range cases {
		got := ShepherdReportPayload(41, c.kind, "이슈 #7을 done으로 닫았습니다")
		if got.Title != c.title {
			t.Errorf("kind %q title = %q, want %q", c.kind, got.Title, c.title)
		}
		if got.Body != "#41 이슈 #7을 done으로 닫았습니다" {
			t.Errorf("body = %q", got.Body)
		}
		if got.URL != "/tasks/41?from=shepherd&tab=history" {
			t.Errorf("url = %q, want /tasks/41?from=shepherd&tab=history", got.URL)
		}
		if got.Tag != "shepherd-report-41" {
			t.Errorf("tag = %q", got.Tag)
		}
		if got.Status != "completed" {
			t.Errorf("status = %q", got.Status)
		}
	}
}

// TestPayloads_NoHangulWhenEn pins #665: with language=en, no push payload
// (complete/fail/test/shepherd report) may contain Korean characters. The
// task-supplied body text rides through untranslated — the test feeds an
// English summary so only our own labels are checked.
func TestPayloads_NoHangulWhenEn(t *testing.T) {
	i18n.Init("en")
	defer i18n.Init("ko")

	payloads := []Payload{
		CompletePayload(1, "proj", "done"),
		FailPayload(2, "proj", "boom"),
		TestPayload(),
		ShepherdReportPayload(3, "review", "report"),
		ShepherdReportPayload(4, "patrol", "report"),
		ShepherdReportPayload(5, "command", "report"),
		ShepherdReportPayload(6, "other", "report"),
	}
	for _, p := range payloads {
		for _, s := range []string{p.Title, p.Body} {
			if hasHangul(s) {
				t.Errorf("en payload must not contain Hangul: title=%q body=%q", p.Title, p.Body)
			}
		}
	}

	if TestPayload().Body == "" {
		t.Error("en test body must not be empty")
	}
	if ShepherdReportLabel("review") == ShepherdReportLabel("unknown") {
		t.Error("en review label must differ from the plain report label")
	}
}

func hasHangul(s string) bool {
	for _, r := range s {
		if r >= 0xAC00 && r <= 0xD7A3 {
			return true
		}
	}
	return false
}
