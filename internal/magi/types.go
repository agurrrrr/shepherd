// Package magi implements the multi-model consensus pipeline (MAGI):
// three persona-bearing proposers deliberate blindly in parallel and an
// aggregator judges/synthesizes the final answer. Design doc:
// docs/magi-consensus-design.md
package magi

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/internal/embedded"
)

// EndpointRef is a resolved LLM endpoint. Populated by the wiring layer from
// config.EmbeddedEndpoint so this package stays free of config imports.
type EndpointRef struct {
	ID            string
	BaseURL       string
	APIKey        string
	Model         string
	ContextTokens int
}

// ProposerProvider identifies which LLM backend a proposer uses.
type ProposerProvider string

const (
	ProviderEmbedded    ProposerProvider = "embedded"
	ProviderClaudeCLI   ProposerProvider = "claude_cli"
	ProviderOpenCodeCLI ProposerProvider = "opencode_cli"
	ProviderGrokCLI     ProposerProvider = "grok_cli"
)

// ProposerSpec is one deliberation member: an endpoint plus its persona.
type ProposerSpec struct {
	Provider     ProposerProvider // embedded | claude_cli | opencode_cli | grok_cli
	Endpoint     EndpointRef     // used when Provider == "embedded"
	ModelID      string           // model alias for claude_cli/opencode_cli (empty = default)
	PersonaKey   string           // melchior | balthasar | casper | custom
	DisplayName  string           // custom display name; overrides MELCHIOR-N when non-empty
	CustomPrompt string           // used when PersonaKey == "custom"
	// Timeout overrides the round-level per-proposer timeout for this slot.
	// Zero means inherit RunProposersOptions.Timeout (task #7205).
	Timeout time.Duration
}

// ProposerResult is one proposer's round answer.
type ProposerResult struct {
	Spec       ProposerSpec
	Answer     string // confidence line stripped
	Confidence int    // 0-10, -1 when the model did not report one
	Err        error  // non-nil when this proposer failed (timeout/HTTP)
	Usage      embedded.ChatUsage
}

// Verdict is the aggregator's structured judgment (design §5.3).
type Verdict struct {
	Verdict       string `json:"verdict"` // unanimous | majority | split
	AgreementAxis string `json:"agreement_axis"`
	Synthesis     string `json:"synthesis"`
	Dissent       string `json:"dissent"`
	Confidence    int    `json:"confidence"`

	// Abstained lists the display names of deliberators the judge excluded
	// from the tally under the abstention rule (echoing the "### <name>"
	// headers of BuildJudgePrompt). Optional: an empty list simply means no
	// second chance fires (task #7182). Populated names drive the abstain
	// second chance and the debate exclusion (step-11).
	Abstained []string `json:"abstained"`
}

// ParseVerdict extracts and validates the verdict JSON from raw model output.
// Models often wrap JSON in ```json fences or prepend prose, so this scans for
// the first balanced {...} object and unmarshals it.
func ParseVerdict(raw string) (*Verdict, error) {
	s := stripCodeFence(raw)

	// Find the first '{' and scan for a balanced closing '}'.
	start := strings.Index(s, "{")
	if start < 0 {
		return nil, fmt.Errorf("no JSON object found in verdict output")
	}

	depth := 0
	inString := false
	escaped := false
	end := -1

	for i := start; i < len(s); i++ {
		c := s[i]

		if escaped {
			escaped = false
			continue
		}

		if c == '\\' && inString {
			escaped = true
			continue
		}

		if c == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
	}

	if end < 0 {
		return nil, fmt.Errorf("unbalanced braces in verdict JSON")
	}

	jsonStr := s[start:end]

	var v Verdict
	if err := json.Unmarshal([]byte(jsonStr), &v); err != nil {
		return nil, fmt.Errorf("parse verdict JSON: %w", err)
	}

	// Validate verdict field.
	switch v.Verdict {
	case "unanimous", "majority", "split":
	default:
		return nil, fmt.Errorf("invalid verdict %q", v.Verdict)
	}

	// Synthesis must not be empty.
	if v.Synthesis == "" {
		return nil, fmt.Errorf("verdict has empty synthesis")
	}

	// Clamp confidence to 0–10.
	if v.Confidence < 0 {
		v.Confidence = 0
	} else if v.Confidence > 10 {
		v.Confidence = 10
	}

	return &v, nil
}

// stripCodeFence removes ```json ... ``` or ``` ... ``` wrapping if present.
func stripCodeFence(raw string) string {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Skip past the opening fence line (```json or ```).
	nlIdx := strings.Index(s, "\n")
	if nlIdx < 0 {
		return s // single-line fence with no content — leave as-is
	}
	s = s[nlIdx+1:]
	// Remove trailing ```.
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

// ExtractConfidence parses the trailing "CONFIDENCE: <n>" self-report line
// (design §5.1). Returns the cleaned answer and the score, or -1 when absent.
//
// Searches only the last 5 lines of the answer. Accepts "CONFIDENCE:"
// (case-insensitive) and Korean variant "신뢰도:". Number formats accepted:
// "8", "8/10", "8.5" (decimals are rounded). The score is clamped to 0–10.
func ExtractConfidence(answer string) (cleaned string, confidence int) {
	lines := strings.Split(answer, "\n")

	start := len(lines) - 5
	if start < 0 {
		start = 0
	}

	for i := len(lines) - 1; i >= start; i-- {
		trimmed := strings.TrimSpace(lines[i])
		lower := strings.ToLower(trimmed)

		var numStr string
		switch {
		case strings.HasPrefix(lower, "confidence:"):
			numStr = trimmed[len("confidence:"):]
		case strings.HasPrefix(trimmed, "신뢰도:"):
			numStr = trimmed[len("신뢰도:"):]
		default:
			continue
		}

		conf := parseConfidenceNumber(numStr)
		if conf < 0 {
			continue // couldn't parse — try next line
		}

		if conf > 10 {
			conf = 10
		}

		// Remove this line from the answer.
		result := make([]string, 0, len(lines)-1)
		result = append(result, lines[:i]...)
		result = append(result, lines[i+1:]...)
		return strings.TrimSpace(strings.Join(result, "\n")), conf
	}

	return answer, -1
}

// parseConfidenceNumber extracts an integer from strings like "8", "8/10",
// "8.5". Returns -1 on parse failure.
func parseConfidenceNumber(s string) int {
	s = strings.TrimSpace(s)

	// Handle "8/10" format — take numerator.
	if idx := strings.Index(s, "/"); idx >= 0 {
		s = s[:idx]
	}

	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return -1
	}

	return int(math.Round(f))
}

// capText truncates s to max runes with a "... [truncated]" suffix.
// Used to keep proposer answers bounded inside aggregator prompts.
func capText(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "... [truncated]"
}
