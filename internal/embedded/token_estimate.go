package embedded

import (
	"bytes"
	"encoding/json"
	"math"
	"unicode"
)

// Prompt token estimation for context trimming and handoff (issue #348).
//
// The per-character weights come from a least-squares fit against exact token
// counts from a Qwen-family server (Strata count_tokens) over 77 samples — Go,
// JS, Svelte and Python code, read_file output, git/grep/journal/ls output,
// JSON, Korean task prompts, wiki pages and summaries — scaled up ~7% so the
// heuristic leans toward overestimating: mean 1.09× of the real count, worst
// case 0.82× (`ls -la` output). The previous rule (non-ASCII 1, ASCII 1/4)
// averaged 0.91×, fell to 0.45× on shell output and overcounted Korean prose.
//
// Weights are in sixtieths of a token so the per-character sum is exact
// integer math.
const (
	tokenUnits     = 60
	letterUnits    = 10 // 1/6: ASCII letters — words and identifier pieces merge
	digitUnits     = 84 // 1.4: Qwen tokenizes numbers digit by digit
	punctUnits     = 42 // 0.7
	newlineUnits   = 84 // 1.4
	spaceRunUnits  = 20 // 1/3 per run of spaces/tabs, plus spaceUnits per char
	spaceUnits     = 3  // 0.05
	cjkUnits       = 39 // 0.65: Hangul, Han, kana
	otherRuneUnits = 60 // 1: other non-ASCII — →, emoji, box drawing, …
)

const (
	// Chat-template framing measured on Strata (Qwen template): a plain
	// message costs ~8 tokens, a tool call plus its result ~40 beyond the
	// name, arguments and result text.
	msgOverheadTokens      = 10
	toolCallOverheadTokens = 20

	// Tool definitions are rendered as JSON inside the prompt. The text
	// weights overcount schema JSON by ~1.2×, and the template adds fixed
	// tool-use instructions. Measured: 1 tool = 507 tokens, 56 tools = 7,385;
	// this formula lands 0–9% above for subsets of 1 to 56 tools.
	toolDefsHeaderTokens = 250
	toolDefsScale        = 0.85
)

// estimateTextTokens estimates the tokens a string costs. See the weights
// above; CJK text is cheaper per character than the old 1-per-rune rule, code
// and machine output are dearer than the old 4-ASCII-chars-per-token rule.
func estimateTextTokens(s string) int {
	units := 0
	inSpace := false
	for _, r := range s {
		space := r == ' ' || r == '\t' || r == '\r'
		switch {
		case space:
			if !inSpace {
				units += spaceRunUnits
			}
			units += spaceUnits
		case r == '\n':
			units += newlineUnits
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			units += letterUnits
		case r >= '0' && r <= '9':
			units += digitUnits
		case r < 128:
			units += punctUnits
		case unicode.In(r, unicode.Hangul, unicode.Han, unicode.Hiragana, unicode.Katakana):
			units += cjkUnits
		default:
			units += otherRuneUnits
		}
		inSpace = space
	}
	return (units + tokenUnits - 1) / tokenUnits
}

// estimateMessageTokens estimates the token count for a single message.
// Includes Content, ToolCalls (function name + JSON args), and template framing.
func estimateMessageTokens(msg ChatMessage) int {
	tokens := estimateTextTokens(msg.Content)
	for _, p := range msg.ContentParts {
		tokens += estimateTextTokens(p.Text)
		if p.ImageURL != nil {
			// Local LLM servers (llama.cpp, vLLM) tokenize the entire base64
			// data URL as regular text — the cost scales with payload size, not
			// a fixed vision-encoder constant. A 200KB screenshot (~270KB data
			// URL) costs ~68K tokens, far more than the old fixed 2048. Using
			// the actual URL length prevents context overflow that caused
			// "empty response loop detected" failures (task #6698).
			tokens += EstimateImageTokens(p.ImageURL.URL)
		}
	}
	for _, tc := range msg.ToolCalls {
		tokens += estimateTextTokens(tc.Func.Name) + estimateTextTokens(tc.Func.Args) + toolCallOverheadTokens
	}
	return tokens + msgOverheadTokens
}

// estimateToolDefsTokens estimates the prompt tokens taken by the tool
// definitions sent with every request. They used to be left out entirely,
// which made iteration 0 of a production task (56 tools) about half its real
// size (issue #348).
func estimateToolDefsTokens(defs []OpenAIToolDef) int {
	if len(defs) == 0 {
		return 0
	}
	// Encode like the request body would, but without HTML escaping: the
	// server sees a literal "<" in descriptions, not a 6-char unicode escape.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(defs); err != nil {
		return toolDefsHeaderTokens
	}
	return toolDefsHeaderTokens + scaleTokens(estimateTextTokens(buf.String()), toolDefsScale)
}

// promptEstimator predicts the prompt tokens of the next request. The text
// heuristic alone is off by ±20% depending on content and tokenizer, so once
// the server reports usage.prompt_tokens the estimate is anchored to it: the
// reported size of the last request plus the heuristic size of the messages
// appended since. When earlier history changed (trimming dropped turns), the
// observed actual/heuristic ratio scales the heuristic instead. Without any
// usage report it falls back to the heuristic.
type promptEstimator struct {
	toolTokens int // tool definitions, part of every request

	anchorLen    int // len(messages) of the last request with usable usage
	anchorHeur   int // heuristic estimate of that request
	anchorActual int // its reported prompt tokens; 0 = no anchor yet
	// ratio is anchorActual/anchorHeur, the heuristic's observed bias.
	ratio float64
}

func newPromptEstimator(toolDefs []OpenAIToolDef) *promptEstimator {
	return &promptEstimator{toolTokens: estimateToolDefsTokens(toolDefs)}
}

// estimate returns the calibrated estimate for a request carrying messages
// (plus the tool definitions) and the uncalibrated heuristic for comparison.
func (e *promptEstimator) estimate(messages []ChatMessage) (est, heur int) {
	heur = e.toolTokens
	prefix := -1 // heuristic of messages[:anchorLen]
	for i, m := range messages {
		if i == e.anchorLen {
			prefix = heur
		}
		heur += estimateMessageTokens(m)
	}
	if len(messages) == e.anchorLen {
		prefix = heur
	}
	if e.anchorActual == 0 {
		return heur, heur
	}
	// The history is append-only between trims, so an unchanged prefix
	// estimate means the anchored request is still the head of this one.
	// Appended messages are never counted below their heuristic size.
	if prefix == e.anchorHeur {
		return e.anchorActual + scaleTokens(heur-prefix, math.Max(1, e.ratio)), heur
	}
	return scaleTokens(heur, e.ratio), heur
}

// observe records the server-reported prompt size of a request whose
// heuristic estimate was heur. Reports under half the heuristic are ignored:
// some servers (Ollama) count only the uncached part of the prompt, and
// anchoring to that would let the history grow past the context window.
func (e *promptEstimator) observe(msgCount, heur int, promptTokens int64) {
	if heur <= 0 || promptTokens*2 < int64(heur) {
		return
	}
	e.anchorLen = msgCount
	e.anchorHeur = heur
	e.anchorActual = int(promptTokens)
	e.ratio = float64(promptTokens) / float64(heur)
}

// removalCredit is how much of a dropped message's heuristic size trimming
// may count as freed. Capped at 1 so a bad ratio can only make trimming drop
// extra turns, never too few.
func (e *promptEstimator) removalCredit() float64 {
	if e.anchorActual == 0 {
		return 1
	}
	return math.Min(1, e.ratio)
}

func scaleTokens(n int, f float64) int {
	return int(math.Ceil(float64(n) * f))
}

// trimMessages truncates the message list to stay within context token limits.
// Removes complete "turns" (assistant message + its tool results) from the
// oldest position, preserving the system prompt and the first user message.
// The size check counts the tool definitions and uses the calibrated estimate.
func trimMessages(messages []ChatMessage, maxTokens int, est *promptEstimator) []ChatMessage {
	if len(messages) <= 2 {
		return messages
	}

	totalTokens, _ := est.estimate(messages)

	// Leave 25% headroom for the model's reply
	limit := maxTokens * 3 / 4
	if totalTokens <= limit {
		return messages
	}

	// Always preserve: [0] system, [1] user (original request)
	system := messages[0]
	userMsg := messages[1]
	candidates := messages[2:] // older turns are at the front
	credit := est.removalCredit()

	for len(candidates) > 0 && totalTokens > limit {
		// Find how many messages belong to the next "turn":
		// one assistant message + all immediately following tool results.
		groupEnd := 1
		if candidates[0].Role == ChatRoleAssistant {
			for groupEnd < len(candidates) && candidates[groupEnd].Role == ChatRoleTool {
				groupEnd++
			}
		}
		// Remove the group
		for i := 0; i < groupEnd; i++ {
			totalTokens -= scaleTokens(estimateMessageTokens(candidates[i]), credit)
		}
		candidates = candidates[groupEnd:]
	}

	result := make([]ChatMessage, 0, 2+len(candidates))
	result = append(result, system, userMsg)
	result = append(result, candidates...)
	return result
}
