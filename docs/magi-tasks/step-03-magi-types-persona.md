# Step 03 — `internal/magi` 패키지 뼈대: wire 타입, 판정 파서, 페르소나

> 설계서 참조: §5.1 (CONFIDENCE 자기보고), §5.3 (판정 JSON), §6 (페르소나)
> 선행 단계: step-01

## 목표

`internal/magi` 패키지를 신설하고, 합의 파이프라인이 쓸 **순수 데이터 타입과 파서**를 만든다. 이 단계에는 네트워크 호출이 전혀 없다 — 전부 순수 함수라 테스트가 쉽다.

## 의존 방향 (중요 — 위반 시 import cycle)

- `internal/magi`는 `internal/embedded`만 import한다 (chat 타입 재사용).
- `internal/magi`는 **`internal/worker`·`internal/config`·`internal/server`를 import하지 않는다.** 시스템 프롬프트와 엔드포인트 정보는 배선부(step-08)가 만들어서 주입한다.

## 생성할 파일 (이 외 파일 금지)

- `internal/magi/types.go`
- `internal/magi/persona.go`
- `internal/magi/types_test.go`
- `internal/magi/persona_test.go`

## 작업 내용

### 1. `types.go`

```go
// Package magi implements the multi-model consensus pipeline (MAGI):
// three persona-bearing proposers deliberate blindly in parallel and an
// aggregator judges/synthesizes the final answer. Design doc:
// docs/magi-consensus-design.md
package magi

import "github.com/agurrrrr/shepherd/internal/embedded"

// EndpointRef is a resolved LLM endpoint. Populated by the wiring layer from
// config.EmbeddedEndpoint so this package stays free of config imports.
type EndpointRef struct {
	ID            string
	BaseURL       string
	APIKey        string
	Model         string
	ContextTokens int
}

// ProposerSpec is one deliberation member: an endpoint plus its persona.
type ProposerSpec struct {
	Endpoint     EndpointRef
	PersonaKey   string // melchior | balthasar | casper | custom
	CustomPrompt string // used when PersonaKey == "custom"
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
}
```

추가 함수:

```go
// ParseVerdict extracts and validates the verdict JSON from raw model output.
// Models often wrap JSON in ```json fences or prepend prose, so this scans for
// the first balanced {...} object and unmarshals it.
func ParseVerdict(raw string) (*Verdict, error)
```
구현 지침:
- 먼저 ```` ```json ... ``` ```` / ```` ``` ... ``` ```` 펜스를 벗긴다.
- 첫 `{`부터 중괄호 짝을 세어(문자열 리터럴 내부 `{}`와 이스케이프 처리 포함) 균형이 맞는 지점까지를 잘라 `json.Unmarshal`.
- `Verdict` 필드가 `unanimous|majority|split` 외 값이면 에러 (`"invalid verdict %q"`).
- `Synthesis`가 빈 문자열이면 에러 (`"verdict has empty synthesis"`).
- `Confidence`는 0~10으로 클램프.

```go
// ExtractConfidence parses the trailing "CONFIDENCE: <n>" self-report line
// (design §5.1). Returns the cleaned answer and the score, or -1 when absent.
func ExtractConfidence(answer string) (cleaned string, confidence int)
```
구현 지침:
- 답변 **마지막 5줄 안에서** `CONFIDENCE:` (대소문자 무시, 앞뒤 공백 허용, `신뢰도:`도 허용)로 시작하는 줄을 찾는다.
- 숫자는 `8`, `8/10`, `8.5` 형태 모두 허용 (소수는 반올림). 0~10 클램프.
- 그 줄을 제거한 나머지를 `strings.TrimSpace`해 반환. 못 찾으면 원문 그대로 + `-1`.

```go
// capText truncates s to max runes with a "... [truncated]" suffix.
// Used to keep proposer answers bounded inside aggregator prompts.
func capText(s string, max int) string
```

### 2. `persona.go`

설계서 §6의 텍스트를 그대로 상수로 넣는다:

```go
// Persona display metadata. Emoji/name appear in live output (design §5.2)
// and in aggregator prompts (identity masking — only persona names are shown).
type Persona struct {
	Key         string // melchior
	DisplayName string // MELCHIOR-1
	Emoji       string // 🔬
	Prompt      string // system prompt block
}
```

내장 페르소나 3개 (`GetPersona(key string) (Persona, bool)`로 조회):

| Key | DisplayName | Emoji | Prompt 요지 (전문은 설계서 §6을 한국어 그대로) |
|-----|-------------|-------|-------|
| melchior | MELCHIOR-1 | 🔬 | 과학자 — 기술적 정밀성. 논리 결함·엣지케이스·반례 우선 탐색. 근거 없는 주장 금지 |
| balthasar | BALTHASAR-2 | 🛡 | 어머니 — 보수성과 안전. 리스크·부작용·비가역 변경 경계. 확신 없으면 낮은 신뢰도 보고 |
| casper | CASPER-3 | 🎭 | 여성 — 실용성과 사용자 관점. 실제 사용 상황 상상, 더 단순한 해법 우선 주장 |

Prompt는 다음 형식의 블록으로 만든다 (한국어):

```
[MAGI 심의자 페르소나: MELCHIOR-1]
너는 MAGI 심의 시스템의 MELCHIOR-1이다. 관점: 과학자 — 기술적 정밀성.
- 논리 결함, 엣지케이스, 반례를 우선 탐색하라.
- 근거 없는 주장은 하지 마라.

[심의 규칙]
- 이 심의에서 너는 도구를 사용할 수 없다. 텍스트로만 완결된 답을 작성하라.
- 다른 심의자의 존재를 언급하지 마라. 너의 독립적 결론만 제시하라.
- 답변의 마지막 줄에 반드시 "CONFIDENCE: <0-10 정수>" 한 줄을 추가하라.
```

`[심의 규칙]` 블록은 3개 페르소나에 공통이므로 상수로 빼서 조합하라. `custom` 페르소나는 `ProposerSpec.CustomPrompt` + 공통 심의 규칙 블록을 쓰고, DisplayName은 `CUSTOM-N`(N은 슬롯 번호), Emoji는 `🔮`.

```go
// BuildProposerSystemPrompt composes the base system prompt (built by the
// wiring layer) with the persona block for slot i (0-based).
func BuildProposerSystemPrompt(base string, spec ProposerSpec, slot int) string
```

### 3. 테스트

`types_test.go`:
- `ParseVerdict`: ① 순수 JSON ② ```` ```json ```` 펜스 안 JSON ③ 앞에 산문이 붙은 JSON ④ 깨진 JSON → 에러 ⑤ `verdict: "maybe"` → 에러 ⑥ synthesis 빈 값 → 에러 ⑦ confidence 15 → 10으로 클램프
- `ExtractConfidence`: ① `CONFIDENCE: 8` ② `confidence: 8/10` ③ `신뢰도: 9` ④ 없음 → -1과 원문 유지 ⑤ 본문 중간에 있는 CONFIDENCE 문자열은 무시(마지막 5줄 규칙)

`persona_test.go`:
- `GetPersona` 3키 조회 + 미지 키 false
- `BuildProposerSystemPrompt`가 base와 페르소나 블록, 심의 규칙을 모두 포함하는지

## 하지 말 것

- HTTP 호출, goroutine, `embedded.NewClient` 사용 금지 — 이 단계는 순수 함수만.
- 페르소나 프롬프트를 영어로 쓰지 마라 (라이브 출력·프롬프트는 한국어, 코드 주석·에러는 영문).

## 완료 검증

```bash
go build ./...
go test ./internal/magi/
```
커밋 메시지: `feat(magi): add magi package skeleton — wire types, verdict parser, personas (magi-tasks step-03)`
