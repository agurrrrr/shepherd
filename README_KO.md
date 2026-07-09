# Shepherd

여러 프로젝트에 걸쳐 AI 코딩 에이전트 무리를 관리하는 AI 코딩 오케스트레이션 플랫폼 — 양 떼를 돌보는 목자(shepherd)처럼.

> For the English version, see [README.md](README.md).

## 개요

Shepherd는 여러 AI 코딩 에이전트("양")를 서로 다른 코드베이스에서 병렬로 실행하고, 작업을 적절한 워커에 라우팅하며, 실시간 출력을 스트리밍하고, 전체 작업 히스토리를 보관합니다. **Web UI + 데몬** 중심으로 설계되었으며, 클라우드 CLI·자가호스팅 로컬 모델·내장 다중 모델 합의 엔진(MAGI)까지 폭넓은 AI 백엔드를 지원합니다.

![Shepherd Web UI](assets/webui-screenshot.png)

> **스크린샷 안내:** 가능하면 `assets/` 아래 최신 캡처(`webui-dashboard.png`, `webui-magi-stream.png`, 설정 패널 등)로 교체하세요. 재촬영 전까지는 위 히어로 이미지가 사용 가능한 최선본입니다.

세 가지 사용 방법:

- **Web UI** *(주력)* — 실시간 스트리밍, 작업 관리, 프로젝트 git, 파일 브라우저, 이슈, 위키, 스케줄, 스킬을 갖춘 대시보드
- **MCP 서버** — Claude Desktop 등 MCP 클라이언트와 통합
- **CLI** — 직접 명령 및 레거시 대화형/TUI 모드

### 핵심 개념

- **Shepherd (Manager, 목자)** — 각 작업을 분석해 적절한 워커로 라우팅
- **Sheep (Worker, 양)** — 개별 AI 에이전트 인스턴스. 각각 하나의 프로젝트와 프로바이더에 배정됨
- **Project (프로젝트)** — 양이 작업하는 코드베이스
- **Provider (프로바이더)** — 양이 사용하는 AI 백엔드 (Claude, OpenCode, Pi, Grok, Embedded, MAGI)
- **Sheep Personal Memory (양 개인 기억)** — `~/.shepherd/sheep/<이름>/` 아래, 프로젝트와 무관하게 양 이름 단위로 누적되는 노트 (moment / bond / voice)

## 프로바이더

Shepherd는 멀티 프로바이더 구조입니다. 각 양은 하나의 프로바이더로 동작하며, 무리 안에서 프로바이더를 섞을 수 있고 양마다 언제든 전환할 수 있습니다.

| 프로바이더 | 백엔드 | 인증 / 설정 | 용도 |
|----------|---------|--------------|----------|
| `claude` | Claude Code CLI | CLI가 자체 로그인 처리 | 기본 — 코드 작성, 복잡한 에이전트 작업 |
| `opencode` | OpenCode CLI | CLI 설정 | 리뷰, 웹 검색, 저렴/로컬 모델 |
| `pi` | Pi 코딩 에이전트 CLI (`pi --print --mode json`) | CLI 설정 | Claude급 범용 코딩 하네스 |
| `grok` | Grok / xAI CLI (`grok --output-format streaming-json`) | CLI 설정 | xAI 모델 위 Claude급 하네스 |
| `embedded` | 프로세스 내 로컬 LLM 에이전트 루프 | 엔드포인트(URL + 키) 설정 | llama.cpp, vLLM, Ollama 등 서브프로세스 없이 구동 |
| `magi` | 다중 모델 합의 엔진 | 위 프로바이더들을 조합 | 교차 모델 합의가 필요한 고위험 자문 질문 |
| `auto` | 자동 선택 | — | 프롬프트를 보고 매니저가 프로바이더 선택 |

클라우드 CLI 프로바이더(`claude`, `opencode`, `pi`, `grok`)는 자체 인증을 관리합니다 — **API 키가 Shepherd에 저장되지 않습니다**. `embedded`는 직접 설정한 LLM HTTP 엔드포인트와 통신하고, MAGI는 위 백엔드들을 조합합니다.

Settings에서 프로바이더별 전역 on/off(`provider_enabled_*`)와 기본 모델(`model_*`)을 지정할 수 있습니다.

### Embedded 프로바이더 (로컬 모델, in-process)

Embedded 프로바이더는 **Shepherd 프로세스 안에서** 도구 사용 에이전트 루프를 돌립니다 — CLI 서브프로세스 없음. OpenAI 호환 chat API를 사용하므로 llama.cpp, vLLM, Ollama, LM Studio 등 로컬 서버를 그대로 붙일 수 있습니다.

- Web UI (**Settings → Embedded**)에서 엔드포인트 설정: base URL, API 키, 모델, 컨텍스트 윈도우, 최대 이터레이션
- 엔드포인트 플래그: `thinking`(추론 모드), `vision`(비전 가능 모델에 이미지 전달)
- 네이티브 도구: `read_file`, `write_file`, `edit_file`, `grep`, `glob`, `bash` + Shepherd MCP 도구 전부(브라우저, 위키, 히스토리, 외부 MCP)
- 엔드포인트 설정은 `~/.shepherd/embedded.yaml`에 저장; API 응답에서 키는 마스킹
- 컨텍스트 관리: 룬 단위 토큰 추정, 히스토리 트리밍, 필요 시 **컨텍스트 핸드오프**(요약 후 후속 작업 큐잉)

### MAGI — 다중 모델 합의

MAGI는 삼자 심의 시스템에서 영감을 받은 합의 엔진입니다. **세 명의 proposer**가 동일 질문을 병렬로 다루고, 각각 다른 페르소나를 쓰며, 별도 **aggregator**가 판정·종합합니다. 합의 점수가 낮으면 익명 **debate 라운드** 후 최종 판정을 내립니다.

```
Task → Orchestrator
        ├─ Proposer ×3 (병렬, 페르소나 배정)
        │    └─ 각자 신뢰도 자가 평가 (0–10)
        ├─ Aggregator (판정/종합)
        │    ├─ agreement ≥ threshold → 종합 반환
        │    └─ agreement < threshold → Debate
        ├─ Debate (익명, 1 라운드)
        │    └─ 재판정 → 합의 또는 deadlock
        └─ 결과 + 비용 (토큰 / 호출)
```

- **페르소나**: `MELCHIOR-1`(과학자 — 기술 정밀도), `BALTHASAR-2`(어머니 — 보수·안전), `CASPER-3`(여성 — 실용·사용자 관점), 또는 `custom`
- **Proposer 백엔드**: `embedded`, `claude_cli`, `opencode_cli`, `grok_cli` — **모델 패밀리를 섞는 것**을 권장 (상관 오차가 합의 가치를 해침)
- **Aggregator 백엔드**: `claude_cli`, `opencode_cli`, `grok_cli`, 또는 embedded `endpoint`
- **도구 정책 (proposer)**:
  - **허용 (읽기 전용)**: `read_file`, `grep`, `glob`, 히스토리/위키, 읽기 전용 외부 MCP
  - **허용 (브라우저)**: 브라우저 자동화 전체(`browser_open`, `browser_click`, `browser_type` 등) — **proposer별 격리 세션**
  - **차단**: 파일 쓰기, bash, 그 외 FS/클러스터 변경 도구
- **모드**: `advisory`(Phase 1) — "이 설계가 타당한가?", "근본 원인은?" 같은 고위험 질문에 적합. 자율 실행용이 아님
- Web UI (**Settings → MAGI**)에서 설정; `~/.shepherd/embedded.yaml`의 `magi` 섹션에 저장

> MAGI는 자문 전용입니다. 계획을 실행하거나 코드를 수정하지 않습니다. 검증된 답을 받은 뒤 코딩 프로바이더에 넘기세요.

## 요구 사항

- **Go 1.25+** (빌드)
- **Node.js 18+** (Web UI 빌드)
- 최소 하나의 프로바이더 백엔드:
  - **Claude Code CLI**(기본) 및/또는 **OpenCode**, **Pi**, **Grok** CLI가 `PATH`에 있음
  - 및/또는 embedded용 **OpenAI 호환 LLM 엔드포인트**

## 설치

### 빠른 설치

```bash
git clone https://github.com/agurrrrr/shepherd.git
cd shepherd

# 최초 1회: Web UI 의존성 설치
cd web && npm install && cd ..

./install.sh
```

`install.sh`는 Svelte 프론트엔드를 빌드하고(`npm run build` — `node_modules`가 이미 있어야 함), Go 바이너리에 임베드한 뒤 `~/.local/bin/`에 설치하고 데몬을 재시작합니다.

### 수동 빌드

```bash
cd web && npm install && npm run build && cd ..   # 임베드 Web UI 빌드
go build -o shepherd ./cmd/shepherd
cp shepherd ~/.local/bin/
```

## 빠른 시작

```bash
# 1. 현재 디렉터리를 프로젝트로 등록
shepherd init

# 2. Web UI 인증 설정 (username + password)
shepherd auth setup

# 3. 데몬 시작
shepherd serve -d

# 4. Web UI 열기
#    http://localhost:8585
```

Web UI에서 양을 생성·프로젝트에 배정·프로바이더를 고르고 작업을 제출하면 실시간 출력을 볼 수 있습니다. CLI로 일회성 작업도 가능합니다:

```bash
shepherd "앱에 로그인 기능 추가해줘"
```

---

## Web UI

Web UI는 Go 바이너리에 임베드된 Svelte SPA입니다. 데몬 기동 후 `http://localhost:8585`에서 제공됩니다.

### 페이지

| 페이지 | 경로 | 설명 |
|------|------|-------------|
| 대시보드 | `/` | 양 상태 카드, 실행 중 작업, 커맨드 입력, 라이브 출력 |
| 양 | `/sheep` | 생성·삭제·배정·프로바이더/모델 변경 |
| 프로젝트 | `/projects` | 프로젝트 목록·관리 |
| 프로젝트 상세 | `/projects/:name` | 탭: **Live Output**, **Task History**, **Files**, **Git**, **Schedules**, **Skills**, **Issues**, **Wiki**, **Settings** |
| 작업 | `/tasks` | 필터·검색 가능한 작업 목록 |
| 작업 상세 | `/tasks/:id` | 전체 출력, 수정 파일, 비용, 에러, 재시도 |
| 스케줄 | `/schedules` | Cron / interval 스케줄 관리 |
| 스킬 | `/skills` | 스킬 생성, import/export, 프로젝트 동기화 |
| 설정 | `/settings` | 언어, 프로바이더, 모델, Embedded, MAGI, Discord, 위키, OpenCode thinking 프록시 |
| 로그인 | `/login` | 인증 |

### 프로젝트 상세 탭

| 탭 | 역할 |
|-----|----------------|
| Live Output | 실행 중 작업의 스트리밍 출력 (MAGI 다중 스트림 포함) |
| Task History | 이 프로젝트의 과거 작업 |
| Files | 프로젝트 파일 탐색기 (`enable_file_browser`로 on/off) |
| Git | 로그·브랜치·diff; UI에서 stage / unstage / commit / push |
| Schedules | 프로젝트 스코프 cron/interval |
| Skills | 프로젝트 스킬 |
| Issues | 경량 이슈 트래커; **Execute**로 이슈 → 작업 큐 |
| Wiki | 프로젝트 지식 베이스 (페이지, 버전, 완료 작업 자동 ingest) |
| Settings | 프로젝트 옵션 (예: MCP 서버 연결) |

### 실시간 업데이트 (SSE)

```
GET /api/events?token=<access_token>
```

이벤트: `task_start`, `task_complete`, `task_fail`, `output`, `status_change`, `schedule_triggered`.

### 외부 접근

네트워크 밖에서 HTTPS로 쓰려면 Nginx/Caddy 리버스 프록시 또는 cert-manager 기반 Kubernetes Ingress를 데몬 앞에 두세요.

---

## 파일 브라우저

`enable_file_browser`가 true(기본)이면 프로젝트마다 읽기 중심 파일 탐색기를 제공합니다:

- **Web UI**: 프로젝트 → **Files** 탭
- **REST**:
  - `GET /api/projects/:name/files?path=<dir>`
  - `GET /api/projects/:name/files/content/*`
  - `GET /api/projects/:name/files/download/*`

에이전트가 만든 산출물을 대시보드 안에서 확인할 때 유용합니다. API 파일 목록이 필요 없으면 `enable_file_browser: false`로 끄세요.

---

## 이슈

프로젝트별 경량 이슈 트래커가 있습니다:

1. 프로젝트 → **Issues**에서 이슈 생성 (또는 REST)
2. 상세·수락 기준 작성
3. **Execute** (`POST /api/projects/:name/issues/:id/execute`)로 해당 이슈에 연결된 코딩 작업을 큐에 넣음

흐름: **이슈 → execute → 작업 큐 → 양이 실행 → 결과가 이슈에 연결**. CLI로 외부 트래커 import도 가능합니다 (`shepherd queue import-issues …`).

---

## 위키

프로젝트마다 에이전트가 읽고 갱신하는 마크다운 위키를 둘 수 있습니다:

- **Web UI**: 프로젝트 → **Wiki** 탭
- **CLI**: `shepherd wiki list|create|edit|history …`
- **MCP**: `wiki_read_page`, `wiki_search`, `wiki_list_pages`
- **REST**: 페이지 CRUD, 버전 히스토리, lint, 작업 ingest ([REST API](#rest-api) 참고)
- **자동 ingest**: `wiki_auto_ingest`가 true면 완료 작업이 위키 갱신을 제안 (`wiki_max_context_pages`, `wiki_max_page_content_chars`로 주입 크기 제한)

아키텍처 결정, 함정, 런북을 위키에 두면 이후 작업이 프로젝트 지식을 컨텍스트로 시작하게 됩니다.

---

## 브라우저 자동화

내장 브라우저 도구(Rod)를 MCP와 CLI로 사용할 수 있습니다:

```bash
shepherd browser open <url> [-s sheep] [--headless]
shepherd browser get-text <selector> [-s sheep]
shepherd browser screenshot [path] [--selector <sel>]
shepherd browser fetch <url> [--selector <sel>]
shepherd browser list [-s sheep]
shepherd browser close [-s sheep]
```

일반적인 MCP 흐름: `browser_session_start` → `browser_open` → `browser_get_text` / `browser_click` / `browser_type` → `browser_session_stop`. 세션은 양 단위로 스코프되며, MAGI proposer는 각각 격리 세션을 갖습니다.

---

## 양 개인 기억 (Sheep Personal Memory)

각 양은 `~/.shepherd/sheep/<양이름>/` 아래에 **프로젝트와 무관한 개인 기억**을 쌓을 수 있습니다:

- `moment_*.md` — 사용자와의 인상적인 순간
- `bond_*.md` — 관계적 패턴 (예: 짧은 답을 선호)
- `voice_*.md` — 다음 세션 톤 연속성
- `MEMORY.md` — 위 노트의 인덱스

`include_sheep_memory`가 true이면 에이전트 프롬프트에 기억 섹션이 주입됩니다 (`sheep_memory_prompt`로 템플릿 커스터마이즈). 성격·연속성용이며, 프로젝트 사실은 위키에 두세요.

---

## 인증

설정 파일 기반 단일 사용자 인증 — JWT + bcrypt 비밀번호 해시.

```bash
shepherd auth setup             # 최초 설정 (username + password)
shepherd auth change-password   # 비밀번호 변경
```

- JWT secret은 서버 최초 기동 시 자동 생성
- Access 토큰 24h, refresh 토큰 7일
- API 요청은 `Authorization: Bearer <token>` 필요
- Health (`GET /api/health`)는 공개

---

## 데몬 & 서버

```bash
shepherd serve                # 포그라운드 (개발)
shepherd serve -d             # 백그라운드 데몬
shepherd serve status         # 데몬 상태
shepherd serve stop           # 데몬 중지
```

**파일:**
- PID: `~/.shepherd/shepherd.pid`
- DB: `~/.shepherd/shepherd.db`
- 설정: `~/.shepherd/config.yaml`
- Embedded / MAGI: `~/.shepherd/embedded.yaml`
- 양 기억: `~/.shepherd/sheep/<name>/`

### systemd 서비스 (선택)

```ini
# ~/.config/systemd/user/shepherd.service
[Unit]
Description=Shepherd AI Orchestration Daemon
After=network.target

[Service]
ExecStart=%h/.local/bin/shepherd serve
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
```

```bash
systemctl --user enable --now shepherd
```

---

## CLI 레퍼런스

주력 UI는 Web UI이지만, 동일 작업은 CLI로도 가능합니다.

### 양 관리

```bash
shepherd spawn                    # 양 생성 (자동 이름)
shepherd spawn -n dolly           # 이름 지정
shepherd spawn -p grok            # 프로바이더 지정 (claude, opencode, pi, grok, embedded, magi, auto)
shepherd flock                    # 양 목록
shepherd recall <name>            # 양 종료
shepherd recall --all             # 전체 종료
shepherd set-provider <name> auto # 프로바이더 변경
shepherd rename <old> <new>       # 이름 변경
```

### 프로젝트 관리

```bash
shepherd init [name]                            # 현재 디렉터리 등록
shepherd project add <name> <path> -d "desc"    # 프로젝트 추가
shepherd project list                            # 목록
shepherd project remove <name>                   # 제거
shepherd project assign <project> <sheep>        # 양 배정
```

### 작업 실행 & 큐

```bash
shepherd "<task>"                 # 작업 제출 (매니저 자동 라우팅)
shepherd task "<task>"            # 명시적 task 명령
shepherd task detail <id>         # 작업 상세
shepherd task stop <id>           # 실행 중 작업 중지
shepherd queue add <project> "<prompt>"          # 큐에 추가
shepherd queue list                               # 대기 목록
shepherd queue import-issues <project> <YouTrackProject> [query]  # YouTrack import
```

### 상태, 로그 & 위키

```bash
shepherd status                   # 시스템 개요
shepherd log [sheep] -n 50        # 작업 로그
shepherd history <project>        # 프로젝트 작업 히스토리
shepherd wiki list -p <project>   # 위키 페이지 목록
shepherd wiki create <slug> -p <project> -t "Title" -c "content"
shepherd wiki edit <slug> -p <project> --append "..."
```

### 설정 & 기타

```bash
shepherd config get <key>         # 설정 조회
shepherd config set <key> <val>   # 설정 변경
shepherd config path              # 설정 파일 경로
shepherd recover                  # 고착 양/작업 복구
shepherd mcp                      # MCP 서버로 실행
shepherd skill list               # 스킬 목록
shepherd tui                      # 레거시 터미널 UI
shepherd --version                # 버전
```

---

## 스케줄링

Web UI (`/schedules` 또는 프로젝트 → Schedules) 또는 REST API로 관리합니다. 두 종류:

- **Cron** — 표준 cron (예: `0 9 * * MON-FRI`)
- **Interval** — N초마다

```
POST /api/projects/:name/schedules
GET  /api/schedules/preview?cron=0 9 * * *     # 다음 실행 시각 미리보기
POST /api/projects/:name/schedules/:id/run     # 즉시 실행
```

설정된 시각에 자동으로 작업을 생성합니다.

---

## 스킬

프로젝트 또는 전역에 붙이는 재사용 프롬프트 템플릿입니다. Web UI (`/skills`) 또는 REST로 관리합니다.

- **Global** 스킬은 모든 프로젝트, **project** 스킬은 해당 프로젝트만
- **Bundled** 기본 스킬은 최초 기동 시 시드
- 마크다운 + YAML frontmatter로 **Import/Export**
- **Lazy loading** — 프롬프트에는 이름/설명만 주입; 에이전트가 `skill_load` MCP로 본문 로드
- **프로젝트 동기화** — `.claude/skills/`에 기록 가능

### 스킬 Frontmatter

```markdown
---
name: code-review
description: 코드 리뷰 체크리스트
tags: [review, quality]
scope: global
effort: medium
max_turns: 10
disallowed_tools: [Write, Bash]
---

(스킬 본문)
```

| 필드 | 설명 |
|-------|-------------|
| `effort` | 모델 추론 effort (`low`, `medium`, `high`) |
| `max_turns` | 최대 에이전트 턴 (0 = 무제한) |
| `disallowed_tools` | 사용 금지 도구 |

---

## 설정

메인 설정: `~/.shepherd/config.yaml` (embedded 엔드포인트와 MAGI는 `~/.shepherd/embedded.yaml`).

```yaml
language: ko                 # en, ko
default_provider: claude     # claude, opencode, pi, grok, embedded, magi, auto
max_sheep: 12                # 최대 동시 양 수
max_concurrent_tasks: 0      # 전역 동시 실행 상한 (0 = 무제한)
# 전역 상한 아래 프로바이더(+모델) 그룹별 제한
# 예: 로컬 opencode 순차(GPU 보호), 클라우드 claude 무제한
concurrency_limits: {}       # 예: { opencode: 1, claude: 0 }
task_timeout: 4h             # 작업 타임아웃 ("0"/"off" = 무제한)
server_port: 8585
server_host: 0.0.0.0
auto_approve: true
workspace_path: ""           # 선택적 기본 워크스페이스 루트
enable_file_browser: true    # Files 탭 + /files API

# 프로바이더 on/off 및 기본 모델
provider_enabled_claude: true
provider_enabled_opencode: true
provider_enabled_pi: true
provider_enabled_grok: true
provider_enabled_embedded: true
provider_enabled_magi: true
model_claude: ""             # 빈 값 = 각 CLI 기본 모델
model_opencode: ""
model_pi: ""
model_grok: ""

# 프롬프트 주입
session_reuse: true
include_task_history: true
include_mcp_guide: true
include_sheep_memory: true
sheep_memory_prompt: ""      # 빈 값 = 내장 기본 템플릿

# OpenCode thinking(추론) 모드 + 비표준 body 필드용 리버스 프록시
opencode_thinking_default: false
opencode_thinking_proxy_enabled: false
opencode_thinking_proxy_port: 8686
opencode_thinking_proxy_target: ""   # 예: http://127.0.0.1:8080
opencode_thinking_model: ""          # 프록시를 경유하는 provider/model

# 위키
wiki_enabled: true
wiki_auto_ingest: true
wiki_max_context_pages: 2
wiki_max_page_content_chars: 2000

# 디스코드 알림
discord_notifications_enabled: false
discord_webhook_url: ""
discord_notify_on_complete: true
discord_notify_on_fail: true

# 인증 ('shepherd auth setup'으로 설정)
auth_username: admin
auth_password_hash: "$2a$10$..."
auth_jwt_secret: "auto-generated"
```

### 동시성 게이트

작업 디스패치는 **두 게이트를 모두** 통과해야 합니다:

1. **전역** — `max_concurrent_tasks` (0 = 무제한)
2. **그룹** — `concurrency_limits[<provider>]` 또는 `concurrency_limits[<provider/model>]` (값 ≤ 0 = 그룹 제한 없음)

예: `{ opencode: 1, claude: 0 }` → 로컬 OpenCode는 한 번에 하나, Claude는 상한 없음.

---

## 아키텍처

```
User → Web UI / MCP client / CLI
     → Shepherd Daemon (Fiber REST API + SSE)
     → Manager (의도 분석, 작업 라우팅)
     → Worker가 양의 프로바이더 실행:
         claude / opencode / pi / grok  → 외부 CLI (스트리밍)
         embedded                       → in-process LLM 에이전트 루프
         magi                           → 3 proposer + aggregator 합의
     → Queue가 결과 기록 (출력, 비용, 파일)
     → SSE로 실시간 업데이트 → 연결된 클라이언트
```

### 프로젝트 구조

```
shepherd/
├── cmd/shepherd/          # CLI 엔트리포인트
├── ent/schema/            # Ent ORM 엔티티 (Sheep, Project, Task, Skill, Schedule, Issue, Wiki, ...)
├── internal/
│   ├── browser/           # 브라우저 자동화 (Rod)
│   ├── config/            # YAML 설정
│   ├── daemon/            # PID, 시그널, 라이프사이클
│   ├── db/                # SQLite
│   ├── discord/           # Discord 웹훅 알림
│   ├── embedded/          # in-process 로컬 LLM 에이전트 루프
│   ├── i18n/              # 국제화 (en, ko)
│   ├── llmproxy/          # OpenCode thinking 리버스 프록시
│   ├── magi/              # MAGI 합의 (proposer, aggregator, debate, orchestrator)
│   ├── manager/           # 작업 분석 & 라우팅
│   ├── mcp/               # JSON-RPC 2.0 MCP 서버 + 외부 MCP 클라이언트
│   ├── project/           # 프로젝트 CRUD
│   ├── queue/             # 작업 라이프사이클 + 동시성 게이트
│   ├── scheduler/         # Cron & interval
│   ├── server/            # Fiber HTTP, SSE, auth, 핸들러
│   ├── skill/             # 파일 기반 스킬
│   ├── spec/              # 스펙/템플릿 생성
│   ├── tui/               # 레거시 Bubbletea TUI
│   ├── wiki/              # 프로젝트 위키 + 자동 ingest
│   └── worker/            # 양 실행 & 프로바이더 디스패치
└── web/                   # Svelte SPA (JS only, TypeScript 없음)
```

---

## REST API

auth·health를 제외한 모든 엔드포인트는 JWT 인증이 필요합니다.

### Auth
```
POST /api/auth/login               # access + refresh 토큰
POST /api/auth/refresh             # access 토큰 갱신
```

### 리소스
```
GET|POST         /api/sheep
GET|DELETE       /api/sheep/:name
PATCH            /api/sheep/:name/provider

GET|POST         /api/projects
GET|DELETE       /api/projects/:name
POST             /api/projects/:name/assign

GET|POST         /api/tasks
GET              /api/tasks/:id                # cost_usd 포함
POST             /api/tasks/:id/stop
POST             /api/tasks/:id/retry
POST             /api/tasks/:id/retry-from
```

### 파일
```
GET /api/projects/:name/files                  # 디렉터리 목록 (?path=)
GET /api/projects/:name/files/content/*        # 파일 내용
GET /api/projects/:name/files/download/*       # 다운로드
```

### Git
```
GET  /api/projects/:name/git/log
GET  /api/projects/:name/git/branches
GET  /api/projects/:name/git/commits/:hash
GET  /api/projects/:name/git/commits/:hash/diff
GET  /api/projects/:name/git/changes
POST /api/projects/:name/git/stage             # body: { "paths": [...] }
POST /api/projects/:name/git/unstage
POST /api/projects/:name/git/commit            # body: { "message": "..." }
POST /api/projects/:name/git/push
```

### 이슈
```
GET|POST         /api/projects/:name/issues
GET|PATCH|DELETE /api/projects/:name/issues/:id
POST             /api/projects/:name/issues/:id/execute   # 작업 큐에 넣기
```

### 위키
```
GET|POST         /api/wiki/pages
GET|PUT|DELETE   /api/wiki/pages/:slug
GET              /api/wiki/pages/:slug/versions
POST             /api/wiki/lint
POST             /api/wiki/ingest/:task_id
```

### Config: Embedded, MAGI & 모델
```
GET|POST         /api/config/embedded
PUT|DELETE       /api/config/embedded/:id
POST             /api/config/embedded/:id/set-active
POST             /api/config/embedded/test
GET|PUT          /api/config/magi
GET              /api/config/model-options      # UI용 모델 선택지
GET|PATCH        /api/config
```

### 스케줄 & 스킬
```
GET|POST         /api/projects/:name/schedules
GET|PATCH|DELETE /api/projects/:name/schedules/:id
POST             /api/projects/:name/schedules/:id/run

GET|POST         /api/skills
POST             /api/skills/import
POST             /api/skills/sync-all
GET|PATCH|DELETE /api/skills/:id
GET              /api/skills/:id/export
GET|POST         /api/projects/:name/skills
```

### 시스템
```
GET  /api/health
GET  /api/system/status
POST /api/system/restart
GET  /api/events
POST /api/command
POST /api/upload
```

---

## MCP 서버

Claude Desktop 등 MCP 클라이언트와 연동:

```json
{
  "mcpServers": {
    "shepherd": {
      "command": "shepherd",
      "args": ["mcp"]
    }
  }
}
```

**사용 가능한 MCP 도구:** `task_start`, `task_complete`, `task_error`, `get_history`, `get_status`, `get_task_detail`, `skill_load`, `wiki_read_page`, `wiki_search`, `wiki_list_pages`, 브라우저 자동화 30+개 (`browser_session_start`, `browser_open`, `browser_click`, `browser_type`, `browser_screenshot`, `browser_get_text`, …).

---

## 안정성

### Rate-Limit 재시도
Rate limit 감지 시(HTTP 429 등) 지수 백오프로 최대 3회 재시도 — 30s → 60s → 120s (상한 5분). 진행 상황은 UI에 실시간 표시.

### Circuit Breaker
양이 연속 5회 실패하면 해당 양 실행을 일시 중지합니다. 트립 상태는 `status`와 대시보드에 표시되며, 수동 재시도 또는 성공 시 카운터가 리셋됩니다.

### 비용 추적
작업별 실행 비용(`cost_usd`)을 기록하고 프로젝트·전역으로 집계해 큐 상태에 표시합니다.

### 복구
데몬 기동 시 고착 양/작업을 자동 복구합니다. `shepherd recover`로 수동 실행 가능. SIGINT/SIGTERM 시 상태를 저장한 뒤 종료합니다.

---

## 개발

```bash
go build ./...                              # 전체 패키지 빌드
go test ./...                               # 전체 테스트
go test ./internal/magi -run TestName       # 특정 테스트
go generate ./ent                           # Ent ORM 재생성

cd web && npm install && npm run dev        # Web UI 개발 서버
cd web && npm run build                     # Web UI 프로덕션 빌드
```

## 기여

[CONTRIBUTING.md](CONTRIBUTING.md)를 참고하세요. 아키텍처는 [ARCHITECTURE.md](ARCHITECTURE.md)에 있습니다.

## 라이선스

MIT License — [LICENSE](LICENSE).

---

## 스크린샷 (권장 assets)

더미·비민감 데이터, dark UI 일관. `assets/` 권장 파일:

| 우선순위 | 파일 | 화면 |
|----------|------|--------|
| 필수 | `webui-dashboard.png` | 대시보드 — 양 카드, 실행 중 작업, 통계, 커맨드 입력 |
| 필수 | `webui-magi-stream.png` | MAGI Live Output — 3-way 병렬 + 페르소나 + 판정 |
| 필수 | `webui-project-output.png` | 프로젝트 → Live Output (최신 탭 바 전부) |
| 필수 | `webui-settings-providers.png` | Settings — 프로바이더 enable |
| 필수 | `webui-settings-embedded.png` | Settings → Embedded 엔드포인트 |
| 필수 | `webui-settings-magi.png` | Settings → MAGI proposer + aggregator |
| 권장 | `webui-task-detail.png`, `webui-git.png`, `webui-wiki.png`, `webui-issues.png`, `webui-files.png`, `webui-skills.png`, `webui-schedules.png`, `webui-sheep.png` | 보조 페이지 |

---

> ***"여호와는 나의 목자시니 내게 부족함이 없으리로다."*** — 시편 23:1
