# Step 07 — 오케스트레이터: 합의 파이프라인 조립

> 설계서 참조: §5 전체 (파이프라인·UX·비용), §2.4 (DOWN 게이트)
> 선행 단계: step-04, step-05, step-06

## 목표

Round 1 → 판정 → (필요시) 토론 → 최종 산출물의 전체 흐름을 `orchestrator.go` 하나로 조립한다. 이 단계가 끝나면 `magi.Run(ctx, opts)` 한 번으로 자문형 합의가 완결된다 (배선 전이므로 아직 태스크로는 실행 불가).

## 생성할 파일 (이 외 파일 금지)

- `internal/magi/orchestrator.go`
- `internal/magi/orchestrator_test.go`

## 작업 내용

### 1. 옵션과 에러 정의

```go
// Options bundles everything the pipeline needs. The wiring layer (worker/
// server) resolves config and prompts; this package stays dependency-free.
type Options struct {
	SheepName    string
	TaskPrompt   string // the user's task prompt
	BaseSystem   string // base system prompt (BuildSystemPromptForEmbedded output)
	Proposers    []ProposerSpec
	Aggregator   AggregatorSpec
	ConfidenceThreshold int           // default 7 (caller applies config defaults)
	MaxDebateRounds     int           // 0 = never debate, 1 = design default
	ProposerTimeout     time.Duration // per-proposer (default 120s)
	OnOutput            func(string)  // live output sink, may be nil
}

// ErrInsufficientProposers signals that fewer than 2 proposers answered.
// The wiring layer falls back to a single embedded run (design §5.1).
var ErrInsufficientProposers = errors.New("magi: fewer than 2 proposers succeeded")
```

### 2. 파이프라인 (`Run`)

```go
// Run executes the advisory consensus pipeline (design §5) and returns an
// embedded.ExecuteResult so the worker wiring can reuse the existing
// conversion path.
func Run(ctx context.Context, opts Options) (*embedded.ExecuteResult, error)
```

흐름 (순서 엄수):

1. **개시 출력**: `🧠 MAGI 심의 개시 — MELCHIOR·BALTHASAR·CASPER\n` (표시명은 실제 페르소나 구성으로 조합)
2. **Round 1**: `RunProposers` (블라인드, 동일 user prompt = `opts.TaskPrompt`)
3. **성공 수 확인**:
   - 성공 ≤ 1 → `⚠️ MAGI 심의 불가 (성공 응답 N/3) — 단일 임베디드 실행으로 폴백합니다\n` 출력 후 `(nil, ErrInsufficientProposers)` 반환. 폴백 실행 자체는 배선(step-08) 책임.
   - 성공 == 2 → `⚠️ 심의자 1명 이탈 — 2인 심의로 계속합니다\n` 출력 후 계속.
4. **판정**: `Judge(...)` (step-05)
   - `Verdict == nil` (판정 불능) → `SideBySideFallback` 결과로 **정상 완료** (Incomplete 아님)
5. **DOWN 게이트** (설계서 §2.4, §5.3):
   - `verdict ∈ {unanimous, majority}` **이고** `confidence >= threshold` → 채택:
     - `✅ 합의 도달 (<verdict>, 신뢰도 N/10) — 종합 응답 채택\n`
     - 최종 텍스트 = `synthesis`. `verdict == majority`이고 `dissent`가 비어있지 않으면 뒤에 `\n\n---\n📎 소수의견: <dissent>` 병기.
   - 그 외 (`split` 또는 저신뢰):
     - `MaxDebateRounds == 0`이면 토론 없이 교착 처리(아래 7번의 형식)로 종결.
     - 아니면 `⚖️ 합의 판정: <verdict>, 신뢰도 N/10 — 토론 라운드 진입\n  (쟁점: <agreement_axis>)\n` 출력 후 토론으로.
6. **토론**: `RunDebateRound` (step-06) → `Judge` 재판정. **이번엔 무조건 종결** (설계서 §5.4):
   - 재판정 `Verdict == nil` → `SideBySideFallback`(토론 후 답변 기준)으로 정상 완료
   - `unanimous/majority` → 5번 채택 경로와 동일 (`✅ 합의 도달 ...`)
   - 여전히 `split` → `DeadlockResult(v)` (casting vote — step-06)
7. **비용 집계** (설계서 §5.5):
   - 모든 호출(round1 + 판정 + 토론 + 재판정)의 usage를 합산해 `ExecuteResult.PromptTokens/CompletionTokens`에 채운다. `CostUSD`는 0 (Claude CLI usage는 집계 불가 — step-05 참조).
   - 말미 출력: `📊 MAGI 심의 비용: N 토큰 (호출 M회)\n` — M은 실패 호출 포함 실제 시도 횟수.
8. **반환**: `&embedded.ExecuteResult{Result: 최종텍스트, PromptTokens: ..., CompletionTokens: ...}`, `Incomplete`는 항상 false (이 파이프라인에서 답이 나오는 모든 경로는 완료다).

컨텍스트 취소(`ctx.Err() != nil`)는 각 지점에서 확인해 즉시 에러 반환 (사용자 stop 대응).

### 3. 테스트 (`orchestrator_test.go`)

`callEndpoint`와 `aggregatorComplete`를 fake로 교체해 경로별 시나리오를 검증한다. fake는 호출 순서를 기록하게 만들어라.

1. **평상 경로**: 3 성공 + unanimous/신뢰도 9 → 토론 없이 synthesis 채택, 호출 수 = 4 (proposer 3 + judge 1)
2. **majority + dissent** → synthesis 뒤 소수의견 병기
3. **split → 토론 → 합의** → 토론 라운드 진행 후 채택, 호출 수 = 8 (3+1+3+1)
4. **split → 토론 → 여전히 split** → `⚠️ MAGI 교착` 헤더 + 소수의견 병기
5. **저신뢰(majority, confidence 4 < 7) → 토론 진입** 확인 (DOWN 게이트)
6. **성공 1개** → `ErrInsufficientProposers`
7. **판정 2회 모두 파싱 실패** → `SideBySideFallback` 텍스트로 정상 완료 (`Incomplete == false`, `err == nil`)
8. **`MaxDebateRounds: 0` + split** → 토론 없이 즉시 종결
9. 토큰 합산이 fake usage의 합과 일치

## 하지 말 것

- 토론을 1회 초과로 돌리는 루프 금지 (설계서 §2.3 ② — `MaxDebateRounds`가 1보다 커도 Phase 1에서는 1회로 클램프하고 주석으로 명시).
- 어떤 경로에서도 `Incomplete: true`를 반환하지 마라. 진짜 실패(모든 백엔드 죽음, ctx 취소)는 `error` 반환으로 처리한다.
- worker/config 패키지 import 금지 (README 의존 방향).

## 완료 검증

```bash
go build ./...
go test ./internal/magi/
```
커밋 메시지: `feat(magi): consensus orchestrator with DOWN escalation gate (magi-tasks step-07)`
