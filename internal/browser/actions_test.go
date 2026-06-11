package browser

import "testing"

func TestXPathLiteral(t *testing.T) {
	cases := map[string]string{
		"Sign in":   "'Sign in'",
		"":          "''",
		"it's":      `concat('it',"'",'s')`,
		"'":         `concat('',"'",'')`,
		"a'b'c":     `concat('a',"'",'b',"'",'c')`,
	}
	for in, want := range cases {
		if got := xpathLiteral(in); got != want {
			t.Errorf("xpathLiteral(%q) = %q, want %q", in, got, want)
		}
	}
}
