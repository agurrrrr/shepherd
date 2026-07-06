# Step 04 — Proposer 블라인드 병렬 호출

> 설계서 참조: §5.1 (Round 1), §5.2 (라이브 출력)
> 선행 단계: step-03

## 목표

proposer 3개를 병렬로 호출해 `[]ProposerResult`를 모으는 `proposer.go`를 만든다. 서로의 답은 노출하지 않는다(블라인드). 개별 실패는 전체를 중단시키지 않는다.

## 생성할 파일 (이 외 파일 금지)

- `internal/magi/proposer.go`
- `internal/magi/proposer_test.go`

## 작업 내용

### 1. 호출 한 개의 구현

`internal/embedded/client.go`의 기존 클라이언트를 그대로 쓴다. 신규 HTTP 코드 작성 금지.

```go
// callEndpoint sends one no-tools chat request and returns the final message.
// It is the single seam for tests (override via var for fakes).
var callEndpoint = func(ctx context.Context, ep EndpointRef, systemPrompt, userPrompt string, temperature float32, maxTokens int) (string, embedded.ChatUsage, error) {
	client := embedded.NewClient(ep.BaseURL, ep.APIKey, ep.Model)
	req := &embedded.ChatRequest{
		Model: ep.Model,
		Messages: []embedded.ChatMessage{
			{Role: embedded.ChatRoleSystem, Content: systemPrompt},
			{Role: embedded.ChatRoleUser, Content: userPrompt},
		},
		Temperature: temperature,
		MaxTokens:   maxTokens,
		Stream:      true,
	}
	msg, _, usage, err := client.AccumulateStreamWithRetry(ctx, req, nil)
	// ... nil 가드 후 msg.Content, usage 반환
}
```

주의:
- `Tools`는 넣지 않는다 (자문형 심의는 무툴 — 설계서 §8 Phase 1).
- `MaxTokens`: `ep.ContextTokens / 4`, `ContextTokens`가 0이면 `embedded.DefaultContextTokens / 4`. (임베디드 루프와 동일한 상한 규칙.)
- `AccumulateStreamWithRetry`의 시그니처는 `(ctx, req, onOutput func(string)) (*embedded.ChatMessage, string, *embedded.ChatUsage, error)` — 두 번째 반환값은 finish reason이고 여기선 무시해도 된다. usage가 nil일 수 있으니 가드하라.
- 반환된 `msg`가 nil이거나 `msg.Content`가 빈 문자열이면 에러로 취급: `fmt.Errorf("empty response from %s", ep.ID)`.

### 2. 병렬 실행

```go
// RunProposersOptions bundles inputs for one blind parallel round.
type RunProposersOptions struct {
	Proposers      []ProposerSpec
	BaseSystem     string        // base system prompt from the wiring layer
	UserPrompts    []string      // per-slot user prompt (round 1: all identical; debate round: per-slot)
	Timeout        time.Duration // per-proposer timeout (design: default 120s, set by caller)
	Temperature    float32       // 0 → default 0.7 (diversity)
	OnOutput       func(string)  // live output sink, may be nil
}

// RunProposers calls every proposer in parallel and returns one result per
// slot, in slot order. Individual failures are recorded in Result.Err —
// callers decide whether enough succeeded (design §5.1).
func RunProposers(ctx context.Context, opts RunProposersOptions) []ProposerResult
```

구현 지침:
- `sync.WaitGroup` + 슬롯 인덱스로 결과 슬라이스에 직접 쓰기. **`errgroup`을 쓰지 마라** — errgroup은 첫 에러에서 컨텍스트를 취소해 나머지 proposer까지 죽인다. 개별 실패는 격리해야 한다.
- proposer마다 `context.WithTimeout(ctx, opts.Timeout)` — 가장 느린 모델이 전체를 인질로 잡지 않게 개별 데드라인 (설계서 §5.1).
- 시스템 프롬프트: `BuildProposerSystemPrompt(opts.BaseSystem, spec, slot)` (step-03).
- 응답 수신 후 `ExtractConfidence`로 신뢰도를 분리해 `ProposerResult`에 채운다.
- 완료 시마다 라이브 출력 (성공/실패, 설계서 §5.2 형식):
  - 성공: `  🔬 MELCHIOR-1 (qwen3-27b) 응답 완료 — 신뢰도 8/10\n` (신뢰도 -1이면 `— 신뢰도 미보고`)
  - 실패: `  🔬 MELCHIOR-1 (qwen3-27b) 응답 실패 — <err>\n`
  - 이모지·표시명은 step-03의 `Persona` 메타데이터 사용, 모델명은 `spec.Endpoint.Model`.
- `OnOutput` 호출은 여러 goroutine에서 동시에 일어나므로 `sync.Mutex`로 감싸라.

```go
// SuccessfulResults filters out failed slots, preserving order.
func SuccessfulResults(results []ProposerResult) []ProposerResult
```

### 3. 테스트 (`proposer_test.go`)

`callEndpoint`를 테스트에서 fake로 교체하는 방식을 기본으로 하라 (패키지 변수이므로 `defer func(){ callEndpoint = orig }()` 패턴). 실제 HTTP 스텁이 필요하면 `internal/embedded/client_test.go`의 httptest 패턴을 참고해도 된다.

최소 케이스:
1. 3개 모두 성공 → 결과 3개, 슬롯 순서 유지, confidence 파싱 확인
2. 1개가 에러 → 해당 슬롯만 `Err` 설정, 나머지 정상 (전체 중단 없음)
3. 1개가 타임아웃(fake가 ctx.Done 대기) → 다른 2개는 제시간에 반환되는지, 타임아웃 슬롯은 Err인지
4. 라이브 출력에 페르소나 표시명이 포함되는지 (mutex 보호 하에 문자열 수집)
5. `SuccessfulResults` 필터 동작

## 하지 말 것

- 재시도 로직 자작 금지 — `AccumulateStreamWithRetry`가 이미 transient 재시도를 한다.
- proposer끼리 답을 공유하는 코드 금지 — Round 1은 블라인드가 원칙 (설계서 §2.3 ①).
- aggregator·토론·폴백 로직은 이 단계 범위 밖.

## 완료 검증

```bash
go build ./...
go test ./internal/magi/
```
커밋 메시지: `feat(magi): blind parallel proposer round (magi-tasks step-04)`
