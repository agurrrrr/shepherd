# Step 08 — worker/server 배선: `SetMagiExecutor`와 폴백

> 설계서 참조: §4 (아키텍처·의존 방향), §5.1 (폴백)
> 선행 단계: step-02, step-07

## 목표

`magi` 프로바이더 양이 태스크를 받으면 `magi.Run`이 실행되도록 배선한다. 기존 `SetEmbeddedExecutor` 패턴(`internal/worker/embedded.go` + `internal/server/server.go` ~502행)을 **그대로 복제**한다.

## 의존 방향 (그림 — 위반 금지)

```
server ──▶ worker ──▶ magi ──▶ embedded
   │                   ▲
   └───────────────────┘  (server가 executor 클로저를 주입)
```
- `worker`가 `magi`를 import하는 것은 허용 (sentinel 에러 확인용).
- `magi`는 `worker`를 import하면 **안 된다** (cycle). 시스템 프롬프트는 server 클로저가 `worker.BuildSystemPromptForEmbedded`로 만들어 `magi.Options.BaseSystem`에 넣는다.

## 생성/수정할 파일 (이 외 파일 금지)

- **생성**: `internal/worker/magi.go`
- **수정**: `internal/worker/interactive.go` — switch에 케이스 1개 추가
- **수정**: `internal/server/server.go` — `SetMagiExecutor` 주입 클로저 추가

## 작업 내용

### 1. `internal/worker/magi.go`

`internal/worker/embedded.go`를 옆에 열어두고 같은 구조로 작성한다:

```go
// magiExecutor mirrors embeddedExecutor: injected from the server package
// to avoid import cycles (see SetEmbeddedExecutor).
var magiExecutor func(
	ctx context.Context,
	sheepName, projectPath string,
	prompt string,
	opts InteractiveOptions,
	cancel context.CancelFunc,
) (*ExecuteResult, error)

func SetMagiExecutor(fn ...) { magiExecutor = fn }

func executeWithMagi(ctx context.Context, sheepName, projectPath, prompt string, opts InteractiveOptions, cancel context.CancelFunc) (*ExecuteResult, error) {
	if magiExecutor == nil {
		return nil, fmt.Errorf("magi executor not initialized")
	}
	rt := registerRunningTask(sheepName, cancel, nil)
	defer unregisterRunningTask(sheepName, rt)

	result, err := magiExecutor(ctx, sheepName, projectPath, prompt, opts, cancel)

	// Fewer than 2 proposers answered — fall back to a single embedded run
	// (design §5.1). executeWithEmbedded registers its own running-task entry,
	// so unregister ours first via the deferred call order... (아래 주의 참조)
	if errors.Is(err, magi.ErrInsufficientProposers) {
		if opts.OnOutput != nil {
			opts.OnOutput("🔶 단일 임베디드 실행으로 폴백\n")
		}
		return executeWithEmbedded(ctx, sheepName, projectPath, prompt, opts, cancel)
	}
	return result, err
}
```

주의사항:
- injectCh: Phase 1(자문형)은 중간 주입을 지원하지 않는다. embedded와 달리 inject 채널을 만들지 않는다 (시그니처에서 제외).
- **running-task 등록 중복**: `executeWithEmbedded`도 내부에서 `registerRunningTask`를 호출한다. 폴백 경로에서는 우리 등록을 **먼저 해제한 뒤** 호출하라 (defer에 맡기지 말고 명시적으로 `unregisterRunningTask(sheepName, rt)` 후 defer가 중복 해제해도 안전한지 `unregisterRunningTask`의 self-guard 구현을 읽고 확인하라 — `internal/worker/embedded.go` 상단 주석과 `running_task_test.go` 참고).
- import에 `errors`, `github.com/agurrrrr/shepherd/internal/magi` 추가.

### 2. `interactive.go` 분기 추가

`switch s.Provider` (~377행)에 embedded 케이스(~386행) **바로 아래** 같은 형태로:

```go
case sheep.ProviderMagi:
	// Magi: multi-model consensus deliberation, no subprocess
	result, execErr = executeWithMagi(ctx, sheepName, proj.Path, prompt, opts, cancel)
	// Same incomplete-surfacing rule as embedded (see #5468 note above).
	if execErr == nil && result != nil && result.Incomplete {
		execErr = fmt.Errorf("incomplete: %s", result.IncompleteReason)
	}
```

### 3. `server.go` 주입 클로저

기존 `worker.SetEmbeddedExecutor(...)` 블록(~502행) **바로 아래**에 `worker.SetMagiExecutor(...)`를 추가한다. 클로저가 할 일:

1. `config.GetMagiConfig()` 로드.
   - `nil`이거나 `!Enabled` → `fmt.Errorf("magi is not configured or disabled. Configure it in Settings > Embedded > MAGI")`
2. `config.ValidateMagiConfig`의 **하드 에러**가 있으면 그 목록을 담아 에러 반환 (경고는 무시하고 진행).
3. proposer 3개의 `EndpointID`를 `config.GetEmbeddedEndpointByID`로 해석 → `magi.EndpointRef`로 변환. nil(미존재/disabled)이면 에러: `fmt.Errorf("magi proposer endpoint %q not found or disabled", id)`.
4. aggregator 해석:
   - `type: endpoint` → 해당 엔드포인트를 `EndpointRef`로.
   - `type: claude_cli` → `AggregatorSpec{Type: "claude_cli", WorkDir: projectPath, FallbackEndpoint: <첫 번째 proposer의 EndpointRef>}`.
5. base system prompt: 임베디드 클로저가 쓰는 것과 같은 방식으로 MCP guide **없이** — `worker.BuildSystemPromptForEmbedded(sheepName, projectPath, " ")` 처럼 빈 guide를 넘기지 말고, **정확히** `worker.BuildSystemPromptForEmbedded(sheepName, projectPath, "")`를 호출하되 결과에 도구 안내가 섞여도 무해하다 (proposer 요청에 `Tools`가 없으므로 실제 툴콜은 불가능하고, 페르소나 블록의 "도구를 사용할 수 없다" 규칙이 우선한다).
6. `magi.Options`를 조립해 `magi.Run(ctx, ...)` 호출:
   - `ConfidenceThreshold/MaxDebateRounds/ProposerTimeout`은 config 값 사용 (`ApplyMagiDefaults`가 이미 적용됨)
   - `OnOutput: opts.OnOutput`
7. 반환된 `*embedded.ExecuteResult`를 `*worker.ExecuteResult`로 변환 — 임베디드 클로저 하단의 변환 코드를 찾아 **같은 필드 매핑**으로 (Result, PromptTokens, CompletionTokens, Incomplete, IncompleteReason).
8. `magi.ErrInsufficientProposers`는 **변환하지 말고 그대로 반환** — worker 쪽 폴백 분기가 `errors.Is`로 식별해야 한다.

### 4. sheep 생성 CLI 확인

`shepherd add`(또는 양 생성 경로)에서 provider 인자로 `magi`가 통과되는지 확인만 하라 — step-02에서 검증 문자열을 고쳤으므로 통과해야 정상이다. 통과 안 되면 놓친 열거 지점이므로 step-02 방식으로 추가.

## 하지 말 것

- 이 단계에서 WebUI 수정 금지 (step-09).
- `executeWithEmbedded`/기존 임베디드 클로저 수정 금지 — 참고·재사용만.
- magi 태스크에 MCP 도구/외부 서버 연결을 붙이지 마라 — Phase 1은 무툴 심의다.

## 완료 검증

```bash
go build ./...
go test ./internal/worker/ ./internal/magi/
```

추가 수동 확인 (서버 실행 없이): `grep -n "SetMagiExecutor" internal/server/server.go internal/worker/magi.go` 로 주입-소비 양쪽이 존재하는지.

커밋 메시지: `feat(magi): wire magi provider through worker/server executor injection (magi-tasks step-08)`
