package i18n

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Dictionary completeness (#665). Every field of Messages — including each
// nested sub-struct — must exist in both dictionaries, must be non-empty, and
// must take the same format verbs in the same order. Callers build text with
// fmt.Sprintf against these fields, so a missing verb in one language is a
// runtime bug that only surfaces when the user flips the setting; this test
// catches it at build time instead.

var verbRE = regexp.MustCompile(`%(\d+\$)?[-+#0]*[\d.]*[a-zA-Z]`)

// koreanRE matches Hangul syllables and compatibility jamo.
var koreanRE = regexp.MustCompile(`[\p{Hangul}\x{3131}-\x{318E}]`)

// formatVerbs lists a format string's verbs, ignoring the literal `%%`.
func formatVerbs(s string) []string {
	s = strings.ReplaceAll(s, "%%", "\x00")
	verbs := make([]string, 0, 4)
	for _, m := range verbRE.FindAllStringSubmatch(s, -1) {
		verbs = append(verbs, m[0][1:])
	}
	return verbs
}

// walkFields compares two parallel structs field by field and returns how
// many string fields it checked.
func walkFields(t *testing.T, prefix string, koVal, enVal reflect.Value) int {
	t.Helper()

	if koVal.Type() != enVal.Type() {
		t.Errorf("%s: ko struct type %s differs from en %s", prefix, koVal.Type(), enVal.Type())
		return 0
	}

	checked := 0
	for i := 0; i < koVal.NumField(); i++ {
		name := prefix + "." + koVal.Type().Field(i).Name
		kf, ef := koVal.Field(i), enVal.Field(i)

		if kf.Kind() == reflect.Struct {
			checked += walkFields(t, name, kf, ef)
			continue
		}
		if kf.Kind() != reflect.String {
			t.Errorf("%s: unexpected kind %s (dictionaries hold strings only)", name, kf.Kind())
			continue
		}

		checked++
		if kf.String() == "" {
			t.Errorf("%s: ko dictionary has an empty value", name)
		}
		if ef.String() == "" {
			t.Errorf("%s: en dictionary has an empty value", name)
		}

		kv, ev := formatVerbs(kf.String()), formatVerbs(ef.String())
		if strings.Join(kv, ",") != strings.Join(ev, ",") {
			t.Errorf("%s: format verbs differ\n  ko %v (%q)\n  en %v (%q)",
				name, kv, kf.String(), ev, ef.String())
		}
	}
	return checked
}

func TestDictionariesAreCompleteAndParallel(t *testing.T) {
	Init("ko")
	ko := *T()
	Init("en")
	en := *T()
	defer Init("ko")

	checked := walkFields(t, "Messages", reflect.ValueOf(ko), reflect.ValueOf(en))

	// The #665 blocks alone (push, attention, requests, revert, patrol, wiki,
	// server, changes, issue status, provider) carry well over this many
	// fields; a much smaller count means the walk stopped recursing.
	if checked < 200 {
		t.Fatalf("only %d string fields checked — the reflect walk is not covering the dictionaries", checked)
	}
	t.Logf("checked %d dictionary fields in ko and en", checked)
}

// TestEnglishHasNoKorean keeps leftover Korean out of the en dictionary: a
// single missed call site would show up as a half-translated panel.
func TestEnglishHasNoKorean(t *testing.T) {
	Init("en")
	defer Init("ko")

	v := reflect.ValueOf(*T())
	for i := 0; i < v.NumField(); i++ {
		fv := v.Field(i)
		if fv.Kind() == reflect.Struct {
			for j := 0; j < fv.NumField(); j++ {
				checkEnglishCell(t, "Messages."+v.Type().Field(i).Name+"."+fv.Type().Field(j).Name, fv.Field(j))
			}
			continue
		}
		checkEnglishCell(t, "Messages."+v.Type().Field(i).Name, fv)
	}
}

func checkEnglishCell(t *testing.T, name string, f reflect.Value) {
	t.Helper()
	if f.Kind() != reflect.String {
		return
	}
	if koreanRE.MatchString(f.String()) {
		t.Errorf("%s: en dictionary still holds Korean text: %q", name, f.String())
	}
}

// TestPushAndAttentionRenderInEnglish pins the two blocks whose text leaves
// the server (web push, Discord, the needs-attention panel): with language=en
// nothing may fall back to Korean.
func TestPushAndAttentionRenderInEnglish(t *testing.T) {
	Init("en")
	defer Init("ko")

	m := T()

	for _, s := range []string{
		m.Push.TaskComplete, m.Push.TaskFailed, m.Push.TestBody,
		m.Push.ShepherdReport, m.Push.ReportReview, m.Push.ReportPatrol, m.Push.ReportCommand,
		m.Push.RequestTitleFmt, m.Push.RequestAwaitingFmt,
		m.Push.CostCapTitle, m.Push.CostCapBodyFmt,
	} {
		if s == "" || koreanRE.MatchString(s) {
			t.Errorf("push dictionary leaked: %q", s)
		}
	}

	attention := reflect.ValueOf(m.Attention)
	for i := 0; i < attention.NumField(); i++ {
		s := attention.Field(i).String()
		if s == "" || koreanRE.MatchString(s) {
			t.Errorf("attention en field %s leaked: %q", attention.Type().Field(i).Name, s)
		}
	}

	if got := fmt.Sprintf(m.Attention.FailedTaskTitleFmt, 7); got != "Task #7 failed" {
		t.Errorf("attention title render = %q", got)
	}
	if got := fmt.Sprintf(m.Requests.DeleteDoneFmt, 12); got != "issue #12 deleted" {
		t.Errorf("request result render = %q", got)
	}
}

// TestKoreanMatchesPreConversionText spot-checks strings that existed before
// #665: the ko dictionary must reproduce them byte for byte, so moving text
// into i18n cannot change what current users see.
func TestKoreanMatchesPreConversionText(t *testing.T) {
	Init("ko")

	m := T()
	if m.Push.TaskComplete != "작업 완료" {
		t.Errorf("Push.TaskComplete = %q", m.Push.TaskComplete)
	}
	if m.Push.TestBody != "테스트 알림입니다. 작업이 끝나면 이런 식으로 알려 드립니다." {
		t.Errorf("Push.TestBody = %q", m.Push.TestBody)
	}
	if m.Requests.RejectedDefault != "사용자가 거절했습니다." {
		t.Errorf("Requests.RejectedDefault = %q", m.Requests.RejectedDefault)
	}

	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"Attention.MinutesFmt", fmt.Sprintf(m.Attention.MinutesFmt, 45), "45분"},
		{"Attention.HoursFmt", fmt.Sprintf(m.Attention.HoursFmt, 5), "5시간"},
		{"Attention.DaysFmt", fmt.Sprintf(m.Attention.DaysFmt, 2), "2일"},
		{
			"Attention.CircuitOpenMessageFmt",
			fmt.Sprintf(m.Attention.CircuitOpenMessageFmt, 3),
			"연속 실패 3회 — 대기 작업이 멈췄습니다. 재시도 전에 서킷을 초기화하세요.",
		},
		{
			"Revert.OtherTaskRunningFmt",
			fmt.Sprintf(m.Revert.OtherTaskRunningFmt, 12),
			"같은 저장소에서 작업 #12이(가) 실행 중입니다. 그 작업이 끝난 뒤에 되돌려 주세요",
		},
	} {
		if tc.got != tc.want {
			t.Errorf("%s rendered %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}
