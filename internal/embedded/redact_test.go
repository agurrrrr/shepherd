package embedded

import (
	"strings"
	"testing"
)

// fixture joins fragments so realistic-looking fake tokens never appear
// contiguously in the source (keeps secret scanners / push protection quiet).
func fixture(parts ...string) string {
	return strings.Join(parts, "")
}

func TestRedactSecrets_APIKeyPrefixes(t *testing.T) {
	cases := []struct {
		label string
		in    string
	}{
		{"openai sk-", fixture("key: sk-", "abcdefghijklmnopqrstuvwxyz0123")},
		{"anthropic sk-ant-", fixture("key: sk-ant-", "api03_abcdefghijklmnopqrstuvwxyz")},
		{"xai key", fixture("key: xai-", "abc123XYZdef456GHIjkl789")},
		{"stripe sk_", fixture("token sk_live_", "0123456789abcdefghijABCD")},
	}
	for _, tc := range cases {
		out := redactSecrets(tc.in)
		if !strings.Contains(out, "[REDACTED:api_key]") {
			t.Errorf("%s: expected redaction, got %q", tc.label, out)
		}
		if strings.Contains(out, "sk-") || strings.Contains(out, "sk_") || strings.Contains(out, "xai-") {
			// redacted placeholder must not still carry the raw prefix body
			if !strings.Contains(out, "[REDACTED:api_key]") {
				t.Errorf("%s: raw key leaked: %q", tc.label, out)
			}
		}
	}
}

func TestRedactSecrets_JWT(t *testing.T) {
	// Three-part base64url JWT shape (header.payload.signature), dummy only.
	jwt := fixture(
		"eyJhbGciOiJIUzI1NiJ9",
		".",
		"eyJzdWIiOiIxMjM0NTY3ODkwIn0",
		".",
		"SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
	)
	in := "deployment key " + jwt
	out := redactSecrets(in)
	if !strings.Contains(out, "[REDACTED:jwt]") {
		t.Fatalf("JWT not redacted: %q", out)
	}
	if strings.Contains(out, "eyJ") {
		t.Fatalf("JWT body leaked: %q", out)
	}
}

func TestRedactSecrets_PEMPrivateKey(t *testing.T) {
	in := "key:\n-----BEGIN PRIVATE KEY-----\nMIIabc123def456\nMIIxyz789\n-----END PRIVATE KEY-----\ndone"
	out := redactSecrets(in)
	if !strings.Contains(out, "[REDACTED:pem]") {
		t.Fatalf("PEM not redacted: %q", out)
	}
	if strings.Contains(out, "MIIabc123") || strings.Contains(out, "BEGIN PRIVATE KEY") {
		t.Fatalf("PEM body/header leaked: %q", out)
	}
	if !strings.Contains(out, "done") {
		t.Fatalf("surrounding text lost: %q", out)
	}
}

func TestRedactSecrets_RSAPrivateKey(t *testing.T) {
	in := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----"
	out := redactSecrets(in)
	if !strings.Contains(out, "[REDACTED:pem]") {
		t.Fatalf("RSA PEM not redacted: %q", out)
	}
}

func TestRedactSecrets_AWSAccessKey(t *testing.T) {
	cases := []struct {
		label string
		in    string
	}{
		{"AKIA", fixture("aws AKIA", "ABCDEFGHIJKLMNOP key")},
		{"ASIA", fixture("aws ASIA", "ABCDEFGHIJKLMNOP creds")},
	}
	for _, tc := range cases {
		out := redactSecrets(tc.in)
		if !strings.Contains(out, "[REDACTED:aws_key]") {
			t.Errorf("%s: not redacted: %q", tc.label, out)
		}
		if strings.Contains(out, "AKIA") || strings.Contains(out, "ASIA") {
			t.Errorf("%s: raw key leaked: %q", tc.label, out)
		}
	}
}

func TestRedactSecrets_GitHubAndVendorTokens(t *testing.T) {
	cases := []struct {
		label string
		in    string
		tag   string
	}{
		{
			"github classic",
			fixture("token ghp_", "0123456789abcdefghijABCDEFGHIJ012345"),
			"[REDACTED:github_token]",
		},
		{
			"github fine-grained",
			fixture("github_pat_", "11ABCDE0123456789_abcdefghijklmnopqrstuvwxyz0123456789"),
			"[REDACTED:github_token]",
		},
		{
			"gitlab",
			fixture("glpat-", "0123456789abcdefABCD here"),
			"[REDACTED:vendor_token]",
		},
		{
			"slack bot",
			fixture("xoxb-", "2420837490-2420837490-AbCdEfGhIjKlMnOpQr"),
			"[REDACTED:vendor_token]",
		},
		{
			"google",
			fixture("AIza", "SyD0123456789abcdefghijklmnopqrstuvw"),
			"[REDACTED:google_api_key]",
		},
	}
	for _, tc := range cases {
		out := redactSecrets(tc.in)
		if !strings.Contains(out, tc.tag) {
			t.Errorf("%s: expected %s in %q", tc.label, tc.tag, out)
		}
	}
}

func TestRedactSecrets_NoFalsePositives(t *testing.T) {
	// High-confidence patterns only: ordinary code / lookalikes must survive.
	clean := []string{
		"just a normal log line",
		"model=grok-3",
		// \b anchor: mid-word "sk-" must not fold the suffix
		"task-deadbeefdeadbeefdeadbeef0123",
		"disk-0123456789abcdefghijklmno",
		"risk-0123456789abcdefghijklmno",
		// short sk- bodies under the length floor
		"sk-short",
		"sk-tooshort123",
		// public PEM-looking markers without PRIVATE KEY
		"-----BEGIN CERTIFICATE-----\nMIIabc\n-----END CERTIFICATE-----",
		"-----BEGIN PUBLIC KEY-----\nMIIabc\n-----END PUBLIC KEY-----",
		// incomplete JWT (only 2 parts)
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0",
		// normal URLs / code
		"https://api.example.com/v1/health?region=us-east-1",
		"func main() { apiKey := cfg.Key }",
		"password field is optional",
		// Korean prose
		"시크릿이 없으면 그대로 통과해야 합니다",
	}
	for _, in := range clean {
		out := redactSecrets(in)
		if out != in {
			t.Errorf("over-redacted %q → %q", in, out)
		}
	}
}

func TestRedactSecrets_Disabled(t *testing.T) {
	t.Cleanup(ResetSecretsRedactionEnabled)
	SetSecretsRedactionEnabled(false)
	in := fixture("key: sk-", "abcdefghijklmnopqrstuvwxyz0123")
	if got := redactSecrets(in); got != in {
		t.Fatalf("disabled redaction still changed input: %q", got)
	}
	SetSecretsRedactionEnabled(true)
	if got := redactSecrets(in); !strings.Contains(got, "[REDACTED:api_key]") {
		t.Fatalf("re-enabled redaction failed: %q", got)
	}
}

func TestRedactSecrets_EmptyAndUnchanged(t *testing.T) {
	if got := redactSecrets(""); got != "" {
		t.Fatalf("empty: got %q", got)
	}
	in := "no secrets here at all"
	if got := redactSecrets(in); got != in {
		t.Fatalf("clean string changed: %q", got)
	}
}

func TestTruncateToolResult_RedactsSecrets(t *testing.T) {
	// Universal history path must scrub before storage.
	in := "stdout: " + fixture("sk-", "abcdefghijklmnopqrstuvwxyz0123")
	out := truncateToolResult(in, "bash")
	if strings.Contains(out, "sk-abcdefghijklmnopqrstuvwxyz0123") {
		t.Fatalf("truncateToolResult leaked key: %q", out)
	}
	if !strings.Contains(out, "[REDACTED:api_key]") {
		t.Fatalf("truncateToolResult did not redact: %q", out)
	}
}

func TestIndentResult_RedactsSecrets(t *testing.T) {
	// SSE live preview path must scrub too.
	in := "token " + fixture("xai-", "abc123XYZdef456GHIjkl789")
	out := indentResult(in)
	if strings.Contains(out, "xai-abc123") {
		t.Fatalf("indentResult leaked key: %q", out)
	}
	if !strings.Contains(out, "[REDACTED:api_key]") {
		t.Fatalf("indentResult did not redact: %q", out)
	}
	// Still indented for the web UI result box.
	if !strings.HasPrefix(out, "  ") {
		t.Fatalf("indent lost: %q", out)
	}
}

func TestRedactSecrets_MultipleInOneString(t *testing.T) {
	in := fixture("a=sk-", "abcdefghijklmnopqrstuvwxyz0123") +
		" and " +
		fixture("AKIA", "ABCDEFGHIJKLMNOP") +
		" end"
	out := redactSecrets(in)
	if !strings.Contains(out, "[REDACTED:api_key]") || !strings.Contains(out, "[REDACTED:aws_key]") {
		t.Fatalf("expected both redactions: %q", out)
	}
	if strings.Contains(out, "sk-abc") || strings.Contains(out, "AKIA") {
		t.Fatalf("raw secrets leaked: %q", out)
	}
}
