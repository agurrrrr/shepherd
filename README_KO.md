# Shepherd

여러 프로젝트에 걸쳐 AI 코딩 에이전트 무리를 관리하는 AI 코딩 오케스트레이션 플랫폼 — 양 떼를 돌보는 목자(shepherd)처럼.

> For the English version, see [README.md](README.md).

## 개요

Shepherd는 여러 AI 코딩 에이전트("양")를 서로 다른 코드베이스에서 병렬로 실행하고, 작업을 적절한 워커에 라우팅하며, 실시간 출력을 스트리밍하고, 전체 작업 히스토리를 보관합니다. **Web UI + 데몬** 중심으로 설계되었으며, 클라우드 CLI·자가호스팅 로컬 모델·내장 다중 모델 합의 엔진(MAGI)까지 폭넓은 AI 백엔드를 지원합니다.

![Shepherd Web UI](assets/webui-screenshot.png)

세 가지 사용 방법:

- **Web UI** *(주력)* — 실시간 스트리밍, 작업 관리, 프로젝트 git 뷰, 스케줄, 스킬을 갖춘 완전한 대시보드
- **MCP 서버** — Claude Desktop 등 MCP 클라이언트와 통합
- **CLI** — 직접 명령 및 레거시 대화형/TUI 모드

### 핵심 개념

- **Shepherd (Manager, 목자)** — 각 작업을 분석해 적절한 워커로 라우팅
- **Sheep (Worker, 양)** — 개별 AI 에이전트 인스턴스. 각각 하나의 프로젝트와 프로바이더에 배정됨
- **Project (프로젝트)** — 양이 작업하는 코드베이스
- **Provider (프로바이더)** — 양이 사용하는 AI 백엔드 (Claude, OpenCode, Pi, Grok, Embedded, MAGI)

## 프로바이더

Shepherd는 멀티 프로바이더 구조입니다. 각 양은 하나의 프로바이더로 동작하며, 무리 안에서 프로바이더를 섞을 수 있고 양마다 언제든 전환할 수 있습니다.

| 프로바이더 | 백엔드 | 인증 / 설정 | 용도 |
|-----------|--------|------------|------|
| `claude` | Claude Code CLI | CLI가 자체 로그인 처리 | 기본값 — 코드 작성, 복잡한 에이전틱 작업 |
| `opencode` | OpenCode CLI | CLI 설정 | 리뷰, 웹 검색, 저렴한/로컬 모델 |
| `pi` | Pi 코딩 에이전트 CLI (`pi --print --mode json`) | CLI 설정 | Claude급 범용 코딩 하네스 |
| `grok` | Grok / xAI CLI (`grok --output-format streaming-json`) | CLI 설정 | xAI 모델 기반 Claude급 하네스 |
| `embedded` | in-process 로컬 LLM 에이전트 루프 | 엔드포인트 설정(URL + 키) | 자가호스팅 모델(llama.cpp, vLLM, Ollama)을 별도 서브프로세스 없이 |
| `magi` | 다중 모델 합의 엔진 | 위 프로바이더들을 조합 | 교차 모델 합의가 필요한 중대한 자문형 질문 |
| `auto` | 자동 선택 | — | 프롬프트를 보고 매니저가 최적 프로바이더 선택 |

클라우드 CLI 프로바이더(`claude`, `opencode`, `pi`, `grok`)는 각자 자체 인증을 관리하므로 **Shepherd 안에 API 키가 저장되지 않습니다.** `embedded` 프로바이더는 사용자가 설정한 LLM HTTP 엔드포인트와 직접 통신하고, MAGI는 위 프로바이더들을 조합합니다.

각 프로바이더는 전역에서 켜고 끌 수 있으며(`provider_enabled_*`), Settings에서 기본 모델(`model_*`)을 지정할 수 있습니다.

### Embedded 프로바이더 (로컬 모델, in-process)

Embedded 프로바이더는 완전한 도구 사용 에이전트 루프를 **Shepherd 프로세스 내부에서** 실행합니다 — CLI 서브프로세스가 없습니다. OpenAI 호환 chat API로 통신하므로 어떤 로컬 서버든(llama.cpp, vLLM, Ollama, LM Studio 등) 구동할 수 있습니다.

- Web UI(**Settings → Embedded**)에서 엔드포인트 설정: base URL, API 키, 모델, 컨텍스트 윈도우, 최대 반복 횟수
- 엔드포인트별 플래그: `thinking`(추론 모드), `vision`(비전 지원 모델에 이미지 파일을 실제 이미지로 노출)
- 네이티브 도구: `read_file`, `write_file`, `edit_file`, `grep`, `glob`, `bash` + 모든 Shepherd MCP 도구(브라우저 자동화, 위키, 히스토리, 외부 MCP 서버)
- 엔드포인트 설정은 `~/.shepherd/embedded.yaml`에 저장되며, API 응답에서 키는 마스킹됩니다

### MAGI — 다중 모델 합의

MAGI는 3원 MAGI 시스템에서 영감을 받은 심의 엔진입니다. **세 명의 제안자(proposer)**에게 같은 질문을 병렬로 던지되 각자 서로 다른 페르소나를 부여하고, 별도의 **판정자(aggregator)**가 이를 심사·종합해 하나의 답을 냅니다. 합의도가 낮으면 익명화된 **토론 라운드**를 거쳐 최종 판정을 내립니다.

```
Task → Orchestrator
        ├─ Proposer ×3 (병렬, 페르소나 부여, 읽기 전용 도구)
        │    └─ 각자 신뢰도 자체 평가 (0–10)
        ├─ Aggregator (판정/종합)
        │    ├─ 합의도 ≥ threshold → 종합 답변 반환
        │    └─ 합의도 < threshold → 토론 진입
        ├─ Debate (익명화, 1라운드)
        │    └─ 재판정 → 합의 또는 교착
        └─ 결과 + 비용(토큰 / 호출 수)
```

- **페르소나**: `MELCHIOR-1`(과학자 — 기술적 정밀성), `BALTHASAR-2`(어머니 — 보수성과 안전), `CASPER-3`(여성 — 실용성과 사용자 관점), 또는 `custom` 페르소나
- **제안자 백엔드**: 각 제안자는 `embedded`, `claude_cli`, `opencode_cli`, `grok_cli` 중 하나 — 모델 *계열*을 섞는 것을 강력히 권장(오류가 상관되면 합의 가치가 무너짐)
- **판정자 백엔드**: `claude_cli`, `opencode_cli`, `grok_cli`, 또는 embedded `endpoint`
- **읽기 전용 도구**: 제안자는 `read_file`, `grep`, `glob`, 히스토리/위키 읽기, 읽기 전용 외부 MCP 도구 조회가 가능하지만 — 파일 쓰기·bash 실행·상태 변경은 불가
- **모드**: `advisory`(Phase 1) — 중대한 질문("이 설계가 타당한가?", "근본 원인이 무엇인가?")에 적합하며 자율 실행은 하지 않음
- Web UI(**Settings → MAGI**)에서 설정하며, `~/.shepherd/embedded.yaml`의 `magi` 섹션에 저장됨

> MAGI는 자문 전용입니다: 플랜을 실행하거나 코드를 수정하지 않습니다. 잘 검증된 답을 얻은 뒤, 그 플랜을 코딩 프로바이더에 넘기는 용도로 쓰세요.

## 요구 사항

- **Go 1.21+** (빌드용)
- **Node.js 18+** (Web UI 빌드용)
- 하나 이상의 프로바이더 백엔드:
  - `PATH`에 설치된 **Claude Code CLI**(기본), 그리고/또는 **OpenCode**, **Pi**, **Grok** CLI
  - 그리고/또는 embedded 프로바이더용 로컬 **OpenAI 호환 LLM 엔드포인트**

## 설치

### 빠른 설치

```bash
git clone https://github.com/agurrrrr/shepherd.git
cd shepherd
./install.sh
```

`install.sh`는 Svelte 프론트엔드를 빌드하고, Go 바이너리를 컴파일해 `~/.local/bin/`에 설치한 뒤 데몬을 시작합니다.

### 수동 빌드

```bash
cd web && npm install && npm run build && cd ..   # 내장 Web UI 빌드
go build -o shepherd ./cmd/shepherd
cp shepherd ~/.local/bin/
```

## 빠른 시작

```bash
# 1. 현재 디렉토리를 프로젝트로 등록
shepherd init

# 2. Web UI 인증 설정 (아이디 + 비밀번호)
shepherd auth setup

# 3. 데몬 시작
shepherd serve -d

# 4. Web UI 열기
#    http://localhost:8585
```

Web UI에서 양을 생성하고, 프로젝트에 배정하고, 프로바이더를 고르고, 작업을 제출할 수 있습니다 — 전부 실시간 스트리밍 출력과 함께. CLI에서 단발 작업을 바로 실행할 수도 있습니다:

```bash
shepherd "앱에 로그인 기능 추가해줘"
```

---

## Web UI

Web UI는 Go 바이너리에 내장된 Svelte SPA입니다. 데몬 시작 후 `http://localhost:8585`에서 제공됩니다.

### 페이지

| 페이지 | 경로 | 설명 |
|--------|------|------|
| 대시보드 | `/` | 양 상태 카드, 실행 중 작업, 명령 입력, 실시간 출력 |
| Sheep | `/sheep` | 양 생성/삭제/배정, 프로바이더·모델 변경 |
| Projects | `/projects` | 프로젝트 목록 및 관리 |
| 프로젝트 상세 | `/projects/:name` | git 로그/브랜치/diff, 문서, 스케줄, 스킬, 작업 히스토리 |
| Tasks | `/tasks` | 필터링·검색 가능한 작업 목록 |
| 작업 상세 | `/tasks/:id` | 전체 출력, 변경 파일, 비용, 에러 상세, 재시도 |
| Schedules | `/schedules` | Cron / interval 스케줄 관리 |
| Skills | `/skills` | 스킬 생성, 가져오기/내보내기, 프로젝트 동기화 |
| Settings | `/settings` | 언어, 프로바이더, 모델, Embedded 엔드포인트, MAGI, Discord, 위키 |
| Login | `/login` | 인증 |

### 실시간 업데이트 (SSE)

Web UI는 Server-Sent Events로 실시간 업데이트를 받습니다:

```
GET /api/events?token=<access_token>
```

이벤트: `task_start`, `task_complete`, `task_fail`, `output`, `status_change`, `schedule_triggered` 등.

### 외부 접속

네트워크 외부에서 HTTPS로 접속하려면 데몬 앞에 리버스 프록시(Nginx, Caddy)나 cert-manager를 붙인 Kubernetes Ingress를 두세요.

---

## 인증

Shepherd는 JWT 토큰과 bcrypt 비밀번호 해싱을 사용하는 설정 기반 단일 사용자 인증을 씁니다.

```bash
shepherd auth setup             # 초기 설정 (아이디 + 비밀번호)
shepherd auth change-password   # 비밀번호 변경
```

- JWT 시크릿은 서버 최초 시작 시 자동 생성
- 액세스 토큰 24시간, 리프레시 토큰 7일 만료
- API 요청에는 `Authorization: Bearer <token>` 헤더 필요
- 헬스 엔드포인트(`GET /api/health`)는 공개

---

## 데몬 & 서버

```bash
shepherd serve                # 포그라운드 (개발용)
shepherd serve -d             # 백그라운드 데몬
shepherd serve status         # 데몬 상태 확인
shepherd serve stop           # 데몬 중지
```

**파일:**
- PID 파일: `~/.shepherd/shepherd.pid`
- 데이터베이스: `~/.shepherd/shepherd.db`
- 설정: `~/.shepherd/config.yaml`
- Embedded / MAGI 설정: `~/.shepherd/embedded.yaml`

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

Web UI가 주력 인터페이스지만, 모든 작업은 CLI로도 가능합니다.

### 양 관리

```bash
shepherd spawn                    # 양 생성 (자동 이름)
shepherd spawn -n dolly           # 특정 이름으로 생성
shepherd spawn -p grok            # 프로바이더 지정 (claude, opencode, pi, grok, embedded, magi, auto)
shepherd flock                    # 전체 양 목록
shepherd recall <name>            # 양 종료
shepherd recall --all             # 전체 양 종료
shepherd set-provider <name> auto # 양의 프로바이더 변경
```

### 프로젝트 관리

```bash
shepherd init [name]                            # 현재 디렉토리 등록
shepherd project add <name> <path> -d "설명"     # 프로젝트 추가
shepherd project list                            # 프로젝트 목록
shepherd project remove <name>                   # 프로젝트 삭제
shepherd project assign <project> <sheep>        # 양을 프로젝트에 배정
```

### 작업 실행 & 큐

```bash
shepherd "<작업>"                 # 작업 제출 (매니저가 자동 라우팅)
shepherd task "<작업>"            # 명시적 task 명령
shepherd queue add <project> "<프롬프트>"         # 큐에 작업 추가
shepherd queue list                               # 대기 작업 목록
shepherd queue import-issues <project> <YouTrackProject> [query]  # YouTrack에서 가져오기
```

### 브라우저 자동화

```bash
shepherd browser open <url> [-s sheep] [--headless]
shepherd browser get-text <selector> [-s sheep]
shepherd browser screenshot [path] [--selector <sel>]
shepherd browser fetch <url> [--selector <sel>]
shepherd browser list [-s sheep]
shepherd browser close [-s sheep]
```

### 상태, 로그 & 위키

```bash
shepherd status                   # 시스템 개요
shepherd log [sheep] -n 50        # 작업 로그
shepherd history <project>        # 프로젝트 작업 히스토리
shepherd wiki list -p <project>   # 프로젝트 위키 페이지
shepherd wiki create <slug> -p <project> -t "제목" -c "내용"
```

### 설정 & 기타

```bash
shepherd config get <key>         # 설정 값 조회
shepherd config set <key> <val>   # 설정 값 지정
shepherd config path              # 설정 파일 경로 표시
shepherd recover                  # 멈춘 양 / 작업 복구
shepherd mcp                      # MCP 서버로 실행
shepherd tui                      # 레거시 터미널 UI 대시보드
shepherd --version                # 버전 표시
```

---

## 스케줄링

스케줄은 Web UI(`/schedules`) 또는 REST API로 관리합니다. 두 가지 유형:

- **Cron** — 표준 cron 표현식 (예: `0 9 * * MON-FRI`)
- **Interval** — N초마다

```
POST /api/projects/:name/schedules
GET  /api/schedules/preview?cron=0 9 * * *     # 다음 실행 시각 미리보기
POST /api/projects/:name/schedules/:id/run     # 즉시 실행
```

스케줄은 설정된 시각에 자동으로 작업을 생성합니다.

---

## 스킬

스킬은 프로젝트에 붙이거나 전역으로 공유하는 재사용 가능한 프롬프트 템플릿입니다. Web UI(`/skills`) 또는 REST API로 관리합니다.

- **전역** 스킬은 모든 프로젝트에 적용, **프로젝트** 스킬은 범위 한정
- **번들** 기본 스킬은 최초 시작 시 자동 시딩
- YAML frontmatter가 포함된 마크다운 파일로 **가져오기/내보내기**
- **지연 로딩** — 프롬프트에는 스킬 이름/설명만 주입되고, 에이전트는 `skill_load` MCP 도구로 필요할 때 전체 내용을 로드
- **프로젝트 동기화** — 각 프로젝트의 `.claude/skills/` 디렉토리에 스킬 기록 가능

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

(스킬 내용)
```

| 필드 | 설명 |
|------|------|
| `effort` | 모델 추론 강도 (`low`, `medium`, `high`) |
| `max_turns` | 최대 에이전트 턴 수 (0 = 무제한) |
| `disallowed_tools` | 에이전트가 사용할 수 없는 도구 |

---

## 설정

메인 설정: `~/.shepherd/config.yaml` (embedded 엔드포인트와 MAGI는 `~/.shepherd/embedded.yaml`에 있음).

```yaml
language: ko                 # en, ko
default_provider: claude     # claude, opencode, pi, grok, embedded, magi, auto
max_sheep: 12                # 최대 동시 양 수
max_concurrent_tasks: 0      # 전역 동시 실행 상한 (0 = 무제한)
task_timeout: 4h             # 작업별 실행 타임아웃 ("0"/"off" = 무제한)
server_port: 8585
server_host: 0.0.0.0
auto_approve: true

# 프로바이더별 토글 및 기본 모델
provider_enabled_claude: true
provider_enabled_opencode: true
provider_enabled_pi: true
provider_enabled_grok: true
provider_enabled_embedded: true
provider_enabled_magi: true
model_claude: ""             # 빈 값 = 각 CLI의 기본 모델
model_opencode: ""
model_pi: ""
model_grok: ""

# 프롬프트 주입
session_reuse: true
include_task_history: true
include_mcp_guide: true
include_sheep_memory: true

# 통합
discord_notifications_enabled: false
wiki_enabled: true
wiki_auto_ingest: true

# 인증 ('shepherd auth setup'으로 설정)
auth_username: admin
auth_password_hash: "$2a$10$..."
auth_jwt_secret: "auto-generated"
```

---

## 아키텍처

```
사용자 → Web UI / MCP 클라이언트 / CLI
      → Shepherd 데몬 (Fiber REST API + SSE)
      → Manager (의도 분석, 작업 라우팅)
      → Worker가 양의 프로바이더 실행:
          claude / opencode / pi / grok  → 외부 CLI (스트리밍)
          embedded                       → in-process LLM 에이전트 루프
          magi                           → 3 제안자 + 판정자 합의
      → Queue가 결과 기록 (출력, 비용, 파일)
      → SSE로 실시간 업데이트 → 연결된 모든 클라이언트
```

### 프로젝트 구조

```
shepherd/
├── cmd/shepherd/          # CLI 진입점 (전체 명령)
├── ent/schema/            # Ent ORM 엔티티 (Sheep, Project, Task, Skill, Schedule, ...)
├── internal/
│   ├── browser/           # 브라우저 자동화 (Rod)
│   ├── config/            # YAML 설정 (config.go, magi.go, embedded 엔드포인트)
│   ├── daemon/            # PID 파일, 시그널 처리, 라이프사이클
│   ├── db/                # SQLite 데이터베이스
│   ├── discord/           # Discord 웹훅 알림
│   ├── embedded/          # in-process 로컬 LLM 에이전트 루프 (client, loop, tools)
│   ├── i18n/              # 국제화 (en, ko)
│   ├── llmproxy/          # OpenCode용 thinking 모드 리버스 프록시
│   ├── magi/              # MAGI 합의 (proposer, aggregator, debate, orchestrator)
│   ├── manager/           # 작업 분석 & 라우팅
│   ├── mcp/               # JSON-RPC 2.0 MCP 서버 + 외부 MCP 클라이언트
│   ├── project/           # 프로젝트 CRUD
│   ├── queue/             # 작업 라이프사이클 관리
│   ├── scheduler/         # Cron & interval 스케줄링
│   ├── server/            # Fiber HTTP 서버, SSE, 인증, 핸들러
│   ├── skill/             # 파일 기반 스킬 시스템
│   ├── spec/              # 스펙/템플릿 생성
│   ├── tui/               # 레거시 Bubbletea 터미널 UI
│   ├── wiki/              # 프로젝트 위키
│   └── worker/            # 양 실행 & 프로바이더 디스패치 (claude/opencode/pi/grok/embedded/magi)
└── web/                   # Svelte SPA (JavaScript 전용, TypeScript 미사용)
```

---

## REST API

auth와 health를 제외한 모든 엔드포인트는 JWT 인증이 필요합니다.

### 인증
```
POST /api/auth/login               # 액세스 + 리프레시 토큰 반환
POST /api/auth/refresh             # 액세스 토큰 갱신
```

### 리소스
```
GET|POST         /api/sheep                    # 목록 / 생성
GET|DELETE       /api/sheep/:name              # 조회 / 삭제
PATCH            /api/sheep/:name/provider     # 프로바이더 변경

GET|POST         /api/projects                 # 목록 / 생성
GET|DELETE       /api/projects/:name           # 조회 / 삭제
POST             /api/projects/:name/assign    # 양 배정

GET|POST         /api/tasks                    # 목록 / 생성
GET              /api/tasks/:id                # 상세 (cost_usd 포함)
POST             /api/tasks/:id/stop           # 실행 중 작업 중지
POST             /api/tasks/:id/retry          # 실패/중지 작업 재시도
POST             /api/tasks/:id/retry-from     # 이 작업 이후 일괄 재시도
```

### Git (읽기 전용)
```
GET /api/projects/:name/git/log
GET /api/projects/:name/git/branches
GET /api/projects/:name/git/commits/:hash
GET /api/projects/:name/git/commits/:hash/diff
GET /api/projects/:name/git/changes
```

### 설정: Embedded & MAGI
```
GET|POST         /api/config/embedded          # 엔드포인트 목록 / 생성
PUT|DELETE       /api/config/embedded/:id       # 수정 / 삭제
POST             /api/config/embedded/:id/set-active
POST             /api/config/embedded/test      # 연결 테스트
GET|PUT          /api/config/magi               # MAGI 설정 읽기 / 저장
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
GET  /api/health                   # 헬스 체크 (공개)
GET  /api/system/status            # 시스템 통계
POST /api/system/restart           # 데몬 재시작
GET  /api/events                   # SSE 스트림
POST /api/command                  # 자연어 명령
POST /api/upload                   # 파일 업로드 (10MB 제한)
```

---

## MCP 서버

Claude Desktop 등 MCP 클라이언트와 통합하려면 Shepherd를 MCP 서버로 실행하세요:

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

**제공 MCP 도구:** `task_start`, `task_complete`, `task_error`, `get_history`, `get_status`, `get_task_detail`, `skill_load`, `wiki_read_page`, `wiki_search`, `wiki_list_pages`, 그리고 30개 이상의 브라우저 자동화 도구(`browser_open`, `browser_click`, `browser_type`, `browser_screenshot`, `browser_get_text` 등).

---

## 안정성

### 레이트 리밋 재시도
레이트 리밋 에러(HTTP 429, "too many requests" 등)가 감지되면 지수 백오프로 재시도 — 최대 3회, 30초 → 60초 → 120초(최대 5분 상한). 재시도 진행 상황이 UI에 실시간 스트리밍됩니다.

### 서킷 브레이커
한 양이 연속 5회 실패하면 리소스 낭비를 막기 위해 해당 양의 실행을 일시 중지합니다. 트립된 브레이커는 `status`와 대시보드에 표시되며, 수동 재시도나 성공한 작업이 카운터를 리셋합니다.

### 비용 추적
작업별로 실행 비용(`cost_usd`)을 수집해 프로젝트별·전역으로 집계하고 큐 상태에 표시합니다.

### 복구
멈춘 양과 작업은 데몬 시작 시 자동 복구됩니다. `shepherd recover`로 수동 복구할 수 있습니다. 정상 종료는 SIGINT/SIGTERM을 처리하고 종료 전 상태를 저장합니다.

---

## 개발

```bash
go build ./...                              # 전체 패키지 빌드
go test ./...                               # 전체 테스트 실행
go test ./internal/magi -run TestName       # 특정 테스트 실행
go generate ./ent                           # Ent ORM 코드 재생성

cd web && npm install && npm run dev        # Web UI 개발 서버
cd web && npm run build                     # Web UI 프로덕션 빌드
```

## 기여

[CONTRIBUTING.md](CONTRIBUTING.md)를 참고하세요. 아키텍처 노트는 [ARCHITECTURE_KO.md](ARCHITECTURE_KO.md)에 있습니다.

## 라이선스

MIT License — [LICENSE](LICENSE) 참고.

---

> ***"여호와는 나의 목자시니 내게 부족함이 없으리로다."*** — 시편 23:1
