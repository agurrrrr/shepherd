# Step 11 — 제안자 읽기 컨텍스트 사전 주입 (Phase 1.5)

> 배경: 작업 #7031/#7033 — Phase 1 심의는 무툴(tool-free)이라 "원인 분석해줘" 같은
> **코드 조사형 태스크에서는 제안자 전원이 추정밖에 할 수 없다.**
> 즉시 패치(작업 #7034: MAGI 전용 베이스 프롬프트 + 내용 게이트 + 기권 규칙)는
> "도구가 있다고 거짓말하는 프롬프트"와 "무내용 답변이 합의로 포장되는 문제"를 막았지만,
> 제안자가 코드를 못 본다는 근본 한계는 그대로다.
> 이 단계는 그 한계를 완화한다: **위링 레이어가 읽기 전용 프로젝트 컨텍스트를 미리 수집해
> 제안자의 유저 프롬프트에 주입한다.** (Phase 2 tool-augmented 심의 전까지의 중간 단계)
>
> 선행 단계: step-08 (worker wiring). 즉시 패치 커밋 이후에만 작업하라.

## 목표

MAGI 실행 직전에 위링 레이어(server.go의 magi executor)가 `git log`·최근 변경 파일·
태스크 키워드 매칭 파일 발췌를 수집해, 제안자 3명의 유저 프롬프트에
`[프로젝트 읽기 컨텍스트]` 블록으로 붙인다. 제안자는 여전히 무툴이지만,
추정 대신 실제 코드 스니펫을 근거로 심의할 수 있게 된다.

## 채택하지 않은 대안 (참고 — 다시 제안하지 마라)

- **코드 조사형 태스크를 MAGI에서 제외(라우팅)**: 태스크 유형 분류가 LLM 호출 없이는
  불안정하고, 분류 오판 시 사용자가 명시적으로 고른 provider가 무시된다.
  컨텍스트 주입은 조사형이 아닌 태스크에도 해가 없으므로 주입 쪽을 채택한다.
- **LLM으로 관련 파일 선정**: Phase 2(tool-augmented 심의) 영역. 이 단계에서는
  결정적(deterministic) 휴리스틱만 쓴다.

## 생성/수정할 파일 (이 외 파일 금지)

- 생성: `internal/worker/magi_context.go`, `internal/worker/magi_context_test.go`
- 수정: `internal/config/magi.go` (+ 필요시 `magi_test.go`)
- 수정: `internal/magi/orchestrator.go` (Options 필드 + 유저 프롬프트 조립)
- 수정: `internal/magi/orchestrator_test.go`
- 수정: `internal/worker/embedded.go` (`BuildSystemPromptForMagi`에 한 줄 추가)
- 수정: `internal/server/server.go` (magi executor 배선)

## 작업 내용

### 1. 설정 (`internal/config/magi.go`)

```go
// MagiContextInjection controls pre-collected read-only context for proposers.
type MagiContextInjection struct {
	Enabled  bool `mapstructure:"enabled" json:"enabled" yaml:"enabled"`
	MaxChars int  `mapstructure:"max_chars" json:"max_chars,omitempty" yaml:"max_chars,omitempty"`
}
```

- `MagiConfig`에 `ContextInjection MagiContextInjection` 필드 추가 (mapstructure/json/yaml 태그 3종 모두 — step-01 회귀 주의).
- `ApplyMagiDefaults`: `MaxChars <= 0`이면 `8000`. `Enabled`의 기본값은 **false**
  (기존 동작을 조용히 바꾸지 않는다 — 사용자가 `embedded.yaml`에서 켠다).
- WebUI/설정 API 확장은 이 단계 범위 밖 (별도 단계로 미룬다).

### 2. 수집기 (`internal/worker/magi_context.go`)

```go
// CollectMagiContext gathers read-only project context for MAGI proposers.
// Never fails the run: any command error skips that section silently.
// Returns "" when nothing could be collected.
func CollectMagiContext(ctx context.Context, projectPath, taskPrompt string, maxChars int) string
```

수집 섹션 (순서 고정, 예산 소진 시 즉시 중단):

1. `git log --oneline -20` — 최근 변경이 조사형 태스크의 1순위 단서.
2. `git status --porcelain` — 커밋 안 된 변경 유무.
3. `git diff --stat HEAD~5..HEAD` — 최근에 어떤 파일이 움직였는지.
4. **키워드 매칭 파일 발췌**:
   - 태스크 프롬프트에서 식별자형 토큰 추출: 정규식 `[A-Za-z_][A-Za-z0-9_]{2,}` 매치 +
     확장자 있는 파일명 패턴. 중복 제거, 최대 8개.
   - 토큰별 `git grep -l -i <token>` → 파일별 매치 토큰 수로 랭킹 → 상위 3개 파일.
   - 파일당 첫 120행, 2,000자 캡. `.git/`, `node_modules/`, `vendor/`, 바이너리 제외
     (git grep이 추적 파일만 보므로 대부분 자동 해결).

구현 규칙:

- 모든 명령은 `exec.CommandContext` + `cmd.Dir = projectPath`, 전체 10초 데드라인.
- **읽기 전용** — 쓰기/네트워크 명령 절대 금지.
- 섹션마다 헤더를 붙여 조립: `## 최근 커밋`, `## 변경 상태`, `## 최근 변경 파일`, `## 관련 파일 발췌: <path>`.
- 총합 `maxChars` 초과분은 잘라내고 `... [truncated]` 표기.
- git 저장소가 아니면(1번 명령 실패) 나머지 git 섹션은 건너뛴다 — 빈 문자열 반환 가능.

### 3. 오케스트레이터 (`internal/magi/orchestrator.go`)

- `Options`에 필드 추가:

```go
ReadContext string // read-only project context collected by the wiring layer ("" = none)
```

- Round 1 유저 프롬프트 조립을 헬퍼로 분리:

```go
// BuildProposerUserPrompt appends the wiring-collected read context block.
func BuildProposerUserPrompt(taskPrompt, readContext string) string
```

  `readContext == ""`이면 taskPrompt 그대로. 아니면:

```
<taskPrompt>

[프로젝트 읽기 컨텍스트 — 시스템이 수집한 참고 정보]
<readContext>
```

- **토론 라운드에는 재주입하지 않는다** (1라운드 답변에 이미 반영됨 — 토론 프롬프트 비대화 방지).
- **판정자 프롬프트(`BuildJudgePrompt`)에도 주입하지 않는다** — `[원 태스크]`는 원문 유지.
  magi 패키지의 dependency-free 원칙 유지: 수집은 worker, 전달은 Options 필드로만.

### 4. 시스템 프롬프트 (`internal/worker/embedded.go`)

`BuildSystemPromptForMagi`의 `[답변 규칙]`에 한 줄 추가:

```
- 유저 프롬프트에 [프로젝트 읽기 컨텍스트] 블록이 있으면 그것을 일반 지식보다 우선하는 1차 근거로 사용하라.
```

### 5. 배선 (`internal/server/server.go` magi executor)

`magiOpts` 조립 직전에:

```go
if magiCfg.ContextInjection.Enabled {
	collected := worker.CollectMagiContext(ctx, projectPath, prompt, magiCfg.ContextInjection.MaxChars)
	if collected != "" {
		magiOpts.ReadContext = collected
		if opts.OnOutput != nil {
			opts.OnOutput(fmt.Sprintf("📚 읽기 컨텍스트 %d자 수집 — 제안자에게 주입\n", len([]rune(collected))))
		}
	}
}
```

### 6. 테스트

- `magi_context_test.go`: `t.TempDir()`에 git repo를 만들어(`git init` + 커밋 2개 + 키워드 포함 파일)
  ① 섹션 헤더 존재, ② maxChars 캡 동작, ③ git repo 아닐 때 빈 문자열/무패닉, ④ 키워드 파일 발췌 포함 확인.
- `orchestrator_test.go`: fake `callEndpoint`로 유저 프롬프트를 캡처해
  ① `ReadContext`가 Round 1 유저 프롬프트에 포함, ② 판정 프롬프트에는 미포함,
  ③ `ReadContext == ""`이면 기존 프롬프트와 동일(회귀 없음).

## 하지 말 것

- `internal/magi`에서 `os/exec`·`internal/config` import 금지 — 패키지는 dependency-free 유지.
- 기존 함수 시그니처 변경 금지 (이 문서가 명시한 추가/수정 제외).
- LLM 호출로 파일 선정 금지, 태스크 유형 분류/라우팅 금지 (비채택 대안 참조).
- WebUI·설정 API 수정 금지 (별도 단계).
- 바이너리 설치·서버 재시작 금지 (README 공통 규칙).

## 완료 검증

```bash
go build ./...
go test ./internal/magi/ ./internal/worker/ ./internal/config/
```

커밋 메시지: `feat(magi): read-context injection for proposers (magi-tasks step-11)`
