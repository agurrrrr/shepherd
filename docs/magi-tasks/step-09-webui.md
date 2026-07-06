# Step 09 — 설정 API + WebUI (MAGI 섹션)

> 설계서 참조: §7 (설정 UI), §2.2·§4 (경고 표시)
> 선행 단계: step-01, step-02 (step-08과는 독립)

## 목표

WebUI Settings > Embedded 탭에 "MAGI 합의" 서브섹션을 추가하고, 이를 위한 REST API 2개를 만든다. 양(sheep)의 프로바이더 선택지에도 `magi`를 노출한다.

**언어 규칙 재확인: Svelte는 순수 JavaScript만. `lang="ts"`·타입 표기 금지.**

## 생성/수정할 파일

- **수정**: `internal/server/handlers_system.go` — 핸들러 2개 추가
- **수정**: `internal/server/server.go` — 라우트 2개 등록 (~105행, `/api/config/embedded` 계열 옆)
- **수정**: `web/src/lib/components/Settings/EmbeddedSettings.svelte` — MAGI 서브섹션
- **수정**: 프로바이더 선택지가 열거된 Svelte 파일들 (아래 4번 grep으로 확인)

## 작업 내용

### 1. API 핸들러 (`handlers_system.go`)

기존 임베디드 핸들러들(~347행부터)의 스타일(에러 형식, 인증 미들웨어 가정)을 그대로 따른다.

**`GET /api/config/magi`** — `handleGetMagiConfig`:
```json
{
  "magi": { ...MagiConfig 또는 null... },
  "warnings": ["all proposers use the same model ..."],
  "errors": []
}
```
- `config.LoadEmbeddedConfig()` → `cfg.Magi`가 nil이면 `magi: null` (프론트가 기본 폼을 그림).
- nil이 아니면 `config.ApplyMagiDefaults` 적용 후 반환, `config.ValidateMagiConfig(cfg)`의 errs/warnings 포함.

**`PUT /api/config/magi`** — `handleUpdateMagiConfig`:
- body를 `config.MagiConfig`로 파싱 → `ApplyMagiDefaults` → 전체 cfg에 끼워 `ValidateMagiConfig` 실행.
- 하드 에러가 있으면 **400** + `{"error": "...", "errors": [...]}` 저장하지 않음.
- 경고만 있으면 저장하고 **200** + `{"ok": true, "warnings": [...]}` (경고는 저장을 막지 않는다 — 설계서 §2.2 "경고를 출력한다").
- 저장은 `config.SaveMagiConfig` (endpoints 보존 확인 — read-modify-write).

### 2. 라우트 등록 (`server.go`)

```go
api.Get("/config/magi", s.handleGetMagiConfig)
api.Put("/config/magi", s.handleUpdateMagiConfig)
```
기존 `/config/embedded` 그룹 바로 아래에.

### 3. `EmbeddedSettings.svelte` — MAGI 서브섹션

기존 파일의 마크업 패턴(섹션 헤더, 폼 레이아웃, 저장 버튼, fetch 헬퍼)을 재사용해 페이지 하단에 추가한다:

- **활성 토글**: `magi.enabled`
- **Proposer 슬롯 3개** (고정 3개, 추가/삭제 없음): 각각
  - 엔드포인트 드롭다운 — 기존 `GET /api/config/embedded`의 endpoints 중 `enabled`인 것만 옵션으로
  - 페르소나 셀렉트 — `melchior (🔬 과학자)`, `balthasar (🛡 어머니)`, `casper (🎭 여성)`, `custom`
  - `custom` 선택 시에만 커스텀 프롬프트 textarea 노출
- **Aggregator**: 타입 라디오(`claude_cli` / `endpoint`), `endpoint`일 때만 엔드포인트 드롭다운
- **에스컬레이션**: confidence threshold 슬라이더(0–10, 기본 7), 토론 라운드 수 셀렉트(0/1), proposer 타임아웃 숫자 입력(초)
- **경고/에러 표시**: GET·PUT 응답의 `warnings`는 노란 배너, `errors`는 빨간 배너. 400 응답 시 저장 실패 메시지.
- 저장 버튼 → PUT. 저장 성공 후 GET 재호출로 서버 반영값 리프레시.

UI 문구는 한국어 (기존 탭과 동일).

### 4. 프로바이더 선택지에 `magi` 추가

프로바이더가 열거된 Svelte 파일을 찾아 모두 반영한다:

```bash
grep -rn "opencode" web/src --include="*.svelte" --include="*.js" -l
```

각 파일을 열어 프로바이더 **선택지/표시 매핑**이 있으면 `magi`를 추가한다 (표시명 `MAGI 🧠`). 반드시 확인할 곳:
- `web/src/lib/components/Settings/ProviderEnableToggle.svelte` — `provider_enabled_magi` 토글
- `web/src/lib/components/CommandInput.svelte` — 프로바이더 선택 UI가 있으면
- 양 생성/수정 폼 (grep 결과에서 식별)

OpenCode/Pi 전용 설정 파일(`OpenCodeSettings.svelte` 등)은 건드리지 마라.

### 5. 빌드

```bash
cd web && npm install && npm run build
```
빌드 산출물(`internal/server/web_dist` 또는 `web/build` — 기존 .gitignore 정책 확인)이 저장소에 커밋되는 방식인지 확인하고 **기존 정책을 그대로** 따른다.

## 하지 말 것

- TypeScript 문법 금지 (재차 강조 — 이 저장소의 Svelte는 순수 JS).
- 기존 임베디드 엔드포인트 CRUD UI 로직 수정 금지.
- 새 설정 저장소 만들지 마라 — magi 설정은 `embedded.yaml`의 `magi` 섹션 하나다 (viper/config.yaml 금지).

## 완료 검증

```bash
go build ./...
go test ./internal/server/ 2>/dev/null || true   # server 패키지에 테스트가 있으면 통과 확인
cd web && npm run build                           # 프론트 빌드 성공
```

수동 스모크(서버 실행 없이 확인 불가한 부분)는 step-10 체크리스트로 미룬다.

커밋 메시지: `feat(magi): settings API and WebUI MAGI section (magi-tasks step-09)`
