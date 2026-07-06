# MAGI 합의 시스템 — 단계별 구현 지시서

> **설계 원본**: `docs/magi-consensus-design.md` (작업 #7010) — 각 단계 문서가 §번호로 참조한다.
> **범위**: Phase 0 (설정·배선) + Phase 1 (자문형 합의). Phase 2/3은 별도 설계 후 진행하므로 **이 지시서에 없는 것을 임의로 앞당겨 구현하지 마라**.

이 디렉토리의 각 `step-NN-*.md`는 **독립된 하나의 작업 단위**다. 한 태스크에서 한 단계만 수행하고, 완료 검증을 통과한 뒤 커밋 1개로 마무리한다.

---

## 단계 목록과 의존 관계

| 단계 | 파일 | 내용 | 선행 |
|------|------|------|------|
| 1 | `step-01-config-schema.md` | `embedded.yaml`에 `magi` 섹션 (config 패키지) | 없음 |
| 2 | `step-02-provider-registration.md` | `magi` 프로바이더 등록 (ent enum, 검증, 이모지) | 없음 |
| 3 | `step-03-magi-types-persona.md` | `internal/magi` 패키지 뼈대: wire 타입, 판정 JSON 파서, 페르소나 | 1 |
| 4 | `step-04-proposers.md` | proposer 3개 블라인드 병렬 호출 | 3 |
| 5 | `step-05-aggregator.md` | aggregator 판정 (claude CLI / 로컬 endpoint) | 3 |
| 6 | `step-06-debate.md` | 토론 에스컬레이션 라운드 | 4, 5 |
| 7 | `step-07-orchestrator.md` | 합의 파이프라인 오케스트레이터 + 라이브 출력 | 4, 5, 6 |
| 8 | `step-08-worker-wiring.md` | worker/server 배선 (`SetMagiExecutor`) + 폴백 | 2, 7 |
| 9 | `step-09-webui.md` | 설정 API + WebUI (EmbeddedSettings의 MAGI 섹션) | 1, 2 |
| 10 | `step-10-verification.md` | 통합 검증 체크리스트 + A/B 측정 준비 | 8, 9 |

1↔2는 서로 독립이라 순서를 바꿔도 된다. 9는 8과 독립이다.

---

## 공통 규칙 (모든 단계에 적용 — 필독)

### 작업 방식
1. 시작 전에 해당 단계 문서 **전체**와, 문서가 참조하는 설계서 §절을 읽어라.
2. **단계 문서에 명시된 파일만** 생성/수정하라. 다른 파일을 고치고 싶어지면 멈추고 작업 결과에 사유를 적어라.
3. 기존 함수/타입의 이름·시그니처를 **변경하지 마라**. 새 코드를 추가하는 방식으로만 작업하라 (단계 문서가 명시적으로 수정을 지시한 곳 제외).
4. 지시가 모호하거나 실제 코드와 다르면, **임의로 설계를 바꾸지 말고** 가장 보수적인 해석(기존 코드 패턴을 그대로 따라하기)을 택한 뒤 작업 결과에 그 판단을 기록하라.
5. 각 단계 문서의 "완료 검증"을 **전부 통과한 후에만** 완료로 보고하라. 검증 실패 상태로 완료 보고 금지.

### 빌드·테스트 (중요)
- 검증은 `go build ./...` 와 `go test <해당 패키지>` 까지만.
- **절대 금지**: 바이너리 설치(`go install`, `cp` 등), `shepherd serve` 실행/재시작, 실행 중인 서버 건드리기. 빌드·설치·재시작은 사용자가 직접 한다 (바이너리를 교체하면 실행 중인 서버가 죽고 진행 중 작업이 fail 처리된다).
- ent 스키마를 수정한 단계는 `go generate ./ent` 후 빌드하라.

### 코드 스타일
- 에러 메시지·코드 주석은 **영문** (기존 코드베이스 규칙).
- 라이브 출력(`OnOutput`) 문자열은 **한국어** (기존 임베디드 프로바이더와 동일).
- Svelte 코드는 **순수 JavaScript만** — TypeScript 문법(`lang="ts"`, 타입 표기) 금지.
- 주변 코드의 주석 밀도·네이밍·패턴을 그대로 따라라.

### 커밋
- 단계당 커밋 1개. 메시지 형식: `feat(magi): <단계 요약> (magi-tasks step-NN)`.
- 이 저장소는 **오픈소스**다. 커밋 전에 diff에서 IP 주소·토큰·API 키·비밀번호가 없는지 확인하라. 예시 값이 필요하면 `192.168.x.a`, `<api-key>` 같은 플레이스홀더를 써라.

### 완료 보고 형식
작업 결과에 다음을 포함하라:
1. 생성/수정한 파일 목록
2. 실행한 검증 명령과 결과 (성공/실패 그대로)
3. 지시서와 다르게 판단한 부분과 그 이유 (없으면 "없음")

---

## 코드베이스 핵심 위치 (참조용)

| 무엇 | 어디 |
|------|------|
| 임베디드 에이전트 루프 | `internal/embedded/loop.go` — `Run(ctx, ExecuteOptions)` |
| OpenAI 호환 클라이언트 | `internal/embedded/client.go` — `NewClient(baseURL, apiKey, model)`, `AccumulateStreamWithRetry(ctx, req, onOutput) (*ChatMessage, finishReason, *ChatUsage, error)` |
| chat wire 타입 | `internal/embedded/types.go` — `ChatRequest`, `ChatMessage`, `ChatUsage`, `ExecuteResult` |
| embedded.yaml 로드/저장 | `internal/config/config.go` — `LoadEmbeddedConfig`, `SaveEmbeddedConfig`, `GetEmbeddedEndpointByID`, `GetActiveEmbeddedEndpoint` |
| 프로바이더 enum | `ent/schema/sheep.go` — `field.Enum("provider")` |
| 프로바이더 분기 | `internal/worker/interactive.go` — `switch s.Provider` |
| 임베디드 executor 주입 패턴 | `internal/worker/embedded.go` (`SetEmbeddedExecutor`) + `internal/server/server.go` ~502행 (실제 주입) |
| 시스템 프롬프트 빌더 | `internal/worker/embedded.go` — `BuildSystemPromptForEmbedded(sheepName, projectPath, mcpGuide)` |
| claude CLI 호출 패턴 | `internal/worker/process.go` ~143행 (`--print` + stdin 프롬프트) |
| 설정 API 라우트 | `internal/server/server.go` ~105행 (`/api/config/embedded` 계열) + `internal/server/handlers_system.go` |
| WebUI 임베디드 설정 | `web/src/lib/components/Settings/EmbeddedSettings.svelte` |

### 알아둬야 할 함정
- **`embedded.yaml`의 키 형식**: `UnmarshalEmbeddedYAML`은 `gopkg.in/yaml.v3`를 쓰는데, yaml.v3는 `mapstructure` 태그를 **무시**하고 필드명을 소문자로 붙여 쓴다 (`BaseURL` → `baseurl`, `MaxIterations` → `maxiterations`). 실제 사용자 파일도 그 형식이다. **신규 magi 구조체에는 명시적 `yaml:"snake_case"` 태그를 달아라** (step-01에 상세).
- `GetEmbeddedEndpointByID`는 **disabled 엔드포인트면 nil을 반환**한다 (에러 아님). nil 체크 필수.
- 임베디드 루프의 incomplete 처리 철학: 판정 게이트는 **보수적으로**. 답이 존재하면 마킹만 하고 완료 처리한다 (#7000 오탐 교훈, 설계서 §5.3).
