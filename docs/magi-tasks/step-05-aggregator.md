# Step 05 — Aggregator 판정·종합

> 설계서 참조: §5.3 (판정), §2.6 (judge 편향 완화), §7 (aggregator 설정)
> 선행 단계: step-03

## 목표

세 proposer 답변을 받아 판정 JSON(`Verdict`)을 산출하는 `aggregator.go`를 만든다. 백엔드는 두 가지: `claude_cli`(claude CLI print 모드)와 `endpoint`(로컬 OpenAI 호환 엔드포인트).

## 생성할 파일 (이 외 파일 금지)

- `internal/magi/aggregator.go`
- `internal/magi/aggregator_test.go`

## 작업 내용

### 1. 백엔드 추상화

```go
// AggregatorSpec selects the judging backend (resolved by the wiring layer).
type AggregatorSpec struct {
	Type     string      // "claude_cli" | "endpoint"
	Endpoint EndpointRef // used when Type == "endpoint"
	// FallbackEndpoint is used when the claude CLI fails (design §7:
	// the first proposer endpoint doubles as aggregator).
	FallbackEndpoint EndpointRef
	WorkDir          string // project path for the claude CLI subprocess
}

// aggregatorComplete sends one prompt to the aggregator backend.
// Package-level var so tests can fake it.
var aggregatorComplete = func(ctx context.Context, spec AggregatorSpec, systemPrompt, userPrompt string) (string, embedded.ChatUsage, error)
```

- `Type == "endpoint"`: step-04의 `callEndpoint` 재사용. Temperature `0.2` (판정은 결정적이어야 함).
- `Type == "claude_cli"`: 서브프로세스 1회 호출. `internal/worker/process.go` ~143행 패턴 참고하되 단순화:
  ```go
  cmd := exec.CommandContext(ctx, "claude", "--print")
  cmd.Dir = spec.WorkDir
  cmd.Stdin = strings.NewReader(systemPrompt + "\n\n" + userPrompt)
  envutil.SetCleanEnv(cmd) // github.com/agurrrrr/shepherd/internal/envutil
  ```
  stdout이 답변 텍스트다 (`--output-format json` 쓰지 마라 — 텍스트만 필요). 타임아웃은 호출자 ctx에 맡긴다. usage는 CLI에서 못 얻으므로 zero value 반환 (비용 표시는 orchestrator가 "호출 N회"로만 집계).
  실패(에러/빈 stdout) 시: 라이브 출력에 `⚠️ Claude aggregator 실패 — 로컬 폴백 사용\n`을 남기고 `FallbackEndpoint`로 endpoint 방식 재시도 (설계서 §7 폴백).

### 2. 판정 프롬프트 (`BuildJudgePrompt`)

```go
// BuildJudgePrompt renders the three answers in random order with persona
// names only (identity masking) and instructs a JSON-only verdict.
// Returns the prompt string. Order randomization mitigates position bias
// (design §2.6).
func BuildJudgePrompt(results []ProposerResult, taskPrompt string) string
```

구성 (한국어 프롬프트):
1. 역할 선언: "너는 MAGI 합의 시스템의 판정자다. 아래 심의자들의 답변을 평가하고 종합하라."
2. 편향 억제 지시 (설계서 §2.6): "답변의 **길이가 아니라 근거의 질**로 평가하라. 어느 모델이 썼는지는 알 수 없으며 추측하지 마라."
3. 원 태스크 프롬프트 (`taskPrompt`, `capText`로 4000자 제한)
4. 답변들: `rand.Shuffle`로 순서 랜덤화, 각 항목은 페르소나 표시명만 헤더로 (`### MELCHIOR-1 (신뢰도 8/10)`), 본문은 `capText(answer, 12000)`
5. 출력 지시: 설계서 §5.3의 JSON 스키마를 그대로 제시하고 "JSON 객체 하나만 출력하라. 다른 텍스트 금지." 각 필드 의미 설명 포함:
   - `verdict`: 핵심 결론이 모두 일치하면 unanimous, 2개 일치면 majority, 모두 다르면 split
   - `synthesis`: 종합 답변 — **이것이 사용자에게 전달되는 최종 산출물**이므로 완결된 답으로 작성
   - `dissent`: 소수의견 요약 (없으면 빈 문자열)
   - `confidence`: 종합 답변에 대한 확신 0-10

### 3. 판정 실행 (`Judge`)

```go
// Judge runs the aggregator once, re-prompting once on JSON failure.
// Returns (nil, usage, nil) when both attempts fail to parse — the caller
// falls back to side-by-side output (design §5.3: never mark incomplete;
// an answer exists, so gate conservatively — lesson from task #7000).
func Judge(ctx context.Context, spec AggregatorSpec, results []ProposerResult, taskPrompt string, onOutput func(string)) (*Verdict, embedded.ChatUsage, int, error)
```

- 반환값 마지막의 `int`는 실제 호출 횟수 (비용 집계용, 재프롬프트·폴백 포함).
- 1차 호출 → `ParseVerdict` 성공이면 반환.
- 실패 시 재프롬프트 1회: 이전 출력 + "출력이 유효한 JSON이 아니다. 스키마에 맞는 JSON 객체 하나만 다시 출력하라."
- 그래도 실패 → `(nil, usage합, 호출수, nil)`. **에러를 반환하지 마라** — 파싱 실패는 폴백 경로이지 실패가 아니다. (백엔드 자체가 폴백까지 전부 죽은 경우에만 `error` 반환.)

```go
// SideBySideFallback renders all answers with persona headers when the
// aggregator verdict could not be obtained (design §5.3).
func SideBySideFallback(results []ProposerResult) string
```
- 헤더: `⚠️ MAGI 판정 실패 — 세 심의자의 답변을 원문 병기합니다.` + 페르소나명 섹션별 원문.

### 4. 테스트 (`aggregator_test.go`)

`aggregatorComplete`를 fake로 교체:
1. 1차에 정상 JSON → Verdict 반환, 호출 1회
2. 1차 깨진 출력 → 재프롬프트로 성공 → 호출 2회
3. 2차도 깨짐 → `(nil, _, 2, nil)` (에러 아님)
4. `BuildJudgePrompt`에 페르소나명은 있고 endpoint ID·모델명은 **없는지** (정체 마스킹 — 설계서 §2.6)
5. `SideBySideFallback`에 세 답변과 경고 헤더 포함
6. (선택) claude_cli 폴백: exec 실패를 시뮬레이션하기 어려우면 `aggregatorComplete` 내부를 작은 함수로 쪼개 폴백 분기만 단위 테스트

## 하지 말 것

- 판정 실패를 `Incomplete`/에러로 처리하지 마라 — #7000 오탐 교훈 (README "함정" 참조).
- aggregator 프롬프트에 모델명·엔드포인트 ID를 노출하지 마라 (자기 선호 편향).
- 랜덤 시드 고정 금지 (테스트에서는 순서 무관하게 검증하라).

## 완료 검증

```bash
go build ./...
go test ./internal/magi/
```
커밋 메시지: `feat(magi): aggregator judging with claude_cli/endpoint backends (magi-tasks step-05)`
