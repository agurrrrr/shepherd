# Shepherd

여러 Claude Code 세션을 관리하는 AI 코딩 오케스트레이션 CLI — 목자가 양떼를 돌보듯.

> 영문 버전: [README.md](README.md)

## 개요

Shepherd는 여러 AI 코딩 에이전트를 서로 다른 프로젝트에서 동시에 실행할 수 있게 합니다. 세 가지 인터페이스를 제공합니다:

![Shepherd Web UI](assets/webui-screenshot.png)

- **CLI** — 인터랙티브 채팅 모드와 직접 명령어
- **Web UI** — 실시간 스트리밍을 지원하는 풀 기능 대시보드
- **MCP 서버** — Claude Desktop 및 기타 MCP 클라이언트 연동

### 핵심 개념

- **목자 (Shepherd/Manager)**: 작업을 분석하고 적절한 워커에게 라우팅
- **양 (Sheep/Workers)**: 각 프로젝트에 배정된 개별 Claude Code 인스턴스
- **프로젝트 (Projects)**: 양이 작업하는 코드베이스

> **API 키 불필요.** Shepherd는 모든 AI 작업을 Claude Code CLI에 위임하며, CLI가 자체 인증을 처리합니다.

## 요구사항

- **Go 1.21+** (빌드용)
- **Node.js 18+** (Web UI 빌드용)
- **Claude Code CLI** 설치 및 `PATH`에 등록

## 설치

### 빠른 설치

```bash
# 전체 빌드 (CLI + Web UI + 데몬)
git clone https://github.com/agurrrrr/shepherd.git
cd shepherd
./install.sh
```

설치 스크립트가 Svelte 프론트엔드 빌드, Go 바이너리 컴파일, `~/.local/bin/` 설치, 데몬 시작을 수행합니다.

### 수동 빌드

```bash
go build -o shepherd ./cmd/shepherd
cp shepherd ~/.local/bin/
```

## 빠른 시작

```bash
# 1. 현재 디렉토리를 프로젝트로 등록
shepherd init

# 2. 인증 설정 (Web UI용)
shepherd auth setup

# 3. 데몬 시작
shepherd serve -d

# 4. Web UI 접속
#    http://localhost:8585

# 5. 또는 인터랙티브 CLI 사용
shepherd
```

### 한 줄 작업

```bash
shepherd "앱에 로그인 기능 추가해줘"
```

---

## 인터랙티브 모드

`shepherd`를 인자 없이 실행하면 인터랙티브 채팅 모드에 진입합니다:

```bash
$ shepherd
🐑 Shepherd (my-project) > 다크 모드 지원 추가
🐑 Shepherd (my-project) > status
🐑 Shepherd (my-project) > #42
```

**내장 명령어:**

| 명령어 | 설명 |
|--------|------|
| `help`, `?` | 사용 가능한 명령어 표시 |
| `status` | 시스템 현황 |
| `projects` | 프로젝트 목록 |
| `flock` | 양 목록 |
| `log`, `history` | 작업 이력 |
| `clear` | 화면 지우기 |
| `#<id>` | 작업 상세 보기 |
| `exit`, `quit`, `q` | 종료 |

---

## 데몬 & 서버

Shepherd는 Web UI와 REST API를 제공하는 백그라운드 데몬으로 실행됩니다.

```bash
shepherd serve                # 포어그라운드 (개발용)
shepherd serve -d             # 백그라운드 데몬
shepherd serve status         # 데몬 상태 확인
shepherd serve stop           # 데몬 중지
```

**플래그:**

| 플래그 | 설명 | 기본값 |
|--------|------|--------|
| `-d`, `--daemon` | 백그라운드 데몬으로 실행 | `false` |
| `--cors-origin` | 허용 CORS 오리진 (쉼표 구분) | `*` |

**환경변수:**

| 변수 | 설명 |
|------|------|
| `SHEPHERD_CORS_ORIGIN` | 허용 CORS 오리진 (`--cors-origin` 대체) |

**파일 위치:**
- PID 파일: `~/.shepherd/shepherd.pid`
- 데이터베이스: `~/.shepherd/shepherd.db`
- 설정: `~/.shepherd/config.yaml`

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

## 인증

Shepherd는 설정 기반 단일 사용자 인증을 사용하며, JWT 토큰과 bcrypt 패스워드 해싱을 지원합니다.

```bash
# 초기 설정 (인터랙티브: 사용자명 + 패스워드)
shepherd auth setup

# 패스워드 변경
shepherd auth change-password
```

- JWT 시크릿은 첫 서버 시작 시 자동 생성
- 액세스 토큰 24시간, 리프레시 토큰 7일 만료
- API 요청에 `Authorization: Bearer <token>` 헤더 필요
- 헬스 엔드포인트 (`GET /api/health`)는 공개

---

## Web UI

Web UI는 Go 바이너리에 내장된 Svelte SPA입니다. 데몬 시작 후 `http://localhost:8585`에서 접속합니다.

### 페이지

| 페이지 | 경로 | 설명 |
|--------|------|------|
| 대시보드 | `/` | 양 상태 카드, 실행 중 작업, 명령 입력 |
| 양 관리 | `/sheep` | 생성, 삭제, 프로바이더 변경 |
| 프로젝트 | `/projects` | 프로젝트 목록 및 관리 |
| 프로젝트 상세 | `/projects/:name` | Git 로그, 브랜치, 문서, 스케줄, 스킬 |
| 작업 목록 | `/tasks` | 필터링 및 검색 지원 작업 목록 |
| 작업 상세 | `/tasks/:id` | 전체 출력, 수정 파일, 에러 상세 |
| 스케줄 | `/schedules` | Cron/인터벌 스케줄 관리 |
| 스킬 | `/skills` | 스킬 생성, import/export |
| 설정 | `/settings` | 언어, 프로바이더, 설정 변경 |
| 로그인 | `/login` | 인증 |

### 실시간 업데이트 (SSE)

Web UI는 Server-Sent Events를 통해 실시간 업데이트를 수신합니다:

```
GET /api/events?token=<access_token>
```

이벤트: `task_start`, `task_complete`, `task_fail`, `output`, `status_change`, `schedule_triggered`

---

## 명령어 레퍼런스

### 양 관리

```bash
shepherd spawn                    # 양 생성 (자동 이름)
shepherd spawn -n dolly           # 특정 이름으로 생성
shepherd spawn -p vibe            # Vibe 프로바이더로 생성
shepherd flock                    # 전체 양 목록
shepherd recall <name>            # 양 해제
shepherd recall --all             # 전체 양 해제
shepherd set-provider <name> auto # 프로바이더 변경
```

### 프로젝트 관리

```bash
shepherd init [name]                            # 현재 디렉토리 등록
shepherd project add <name> <path> -d "설명"     # 프로젝트 추가
shepherd project list                            # 프로젝트 목록
shepherd project remove <name>                   # 프로젝트 삭제
shepherd project assign <project> <sheep>        # 양 배정
```

### 작업 실행

```bash
shepherd "<작업>"                 # 작업 제출 (매니저가 자동 라우팅)
shepherd task "<작업>"            # 명시적 작업 명령
```

### 작업 큐

```bash
shepherd queue add <project> "<prompt>"                       # 큐에 작업 추가
shepherd queue list                                            # 대기 작업 목록
shepherd queue import-issues <project> <YouTrackProject> [query]  # YouTrack 이슈 가져오기
```

### 브라우저 자동화

```bash
shepherd browser open <url> [-s sheep] [--headless]   # URL 열기
shepherd browser get-text <selector> [-s sheep]       # 텍스트 추출
shepherd browser get-html [--selector <sel>]           # HTML 가져오기
shepherd browser screenshot [path] [--selector <sel>]  # 스크린샷 캡처
shepherd browser fetch <url> [--selector <sel>]        # 콘텐츠 가져오기
shepherd browser list [-s sheep]                        # 열린 페이지 목록
shepherd browser close [-s sheep]                       # 세션 종료
```

### 상태 & 로그

```bash
shepherd status                   # 시스템 현황
shepherd log                      # 전체 작업 로그
shepherd log <sheep> -n 50        # 특정 양의 로그
shepherd history <project>        # 프로젝트 작업 이력
```

### 양 이름

```bash
shepherd names list               # 커스텀 이름 목록
shepherd names add Dolly Shaun    # 커스텀 이름 추가
shepherd names remove Dolly       # 이름 제거
```

### 설정

```bash
shepherd config get <key>         # 설정값 조회
shepherd config set <key> <val>   # 설정값 변경
shepherd config path              # 설정 파일 경로 표시
```

### 기타

```bash
shepherd tui                      # 터미널 UI 대시보드
shepherd recover                  # 중단된 양/작업 복구
shepherd mcp                      # MCP 서버 실행
shepherd --version                # 버전 표시
```

---

## 스케줄링

스케줄은 Web UI (`/schedules`) 또는 REST API로 관리합니다. 두 가지 타입 지원:

- **Cron**: 표준 cron 표현식 (예: `0 9 * * MON-FRI`)
- **Interval**: N초마다 실행

```
POST /api/projects/:name/schedules
GET  /api/schedules/preview?cron=0 9 * * *    # 다음 5회 실행 시간 미리보기
POST /api/projects/:name/schedules/:id/run    # 즉시 실행
```

스케줄은 설정된 시간에 자동으로 작업을 생성합니다.

---

## 스킬

스킬은 프로젝트에 연결하거나 글로벌로 사용할 수 있는 재사용 가능한 프롬프트 템플릿입니다. Web UI (`/skills`) 또는 REST API로 관리합니다.

- **글로벌 스킬**: 모든 프로젝트에서 사용 가능
- **프로젝트 스킬**: 특정 프로젝트에 한정
- **번들 스킬**: 첫 시작 시 자동 설치되는 기본 스킬
- **Import/Export**: 파일로 스킬 공유

```
GET  /api/skills                    # 글로벌 스킬 목록
POST /api/skills/import             # 파일에서 가져오기
GET  /api/skills/:id/export         # 파일로 내보내기
```

---

## 설정

설정 파일: `~/.shepherd/config.yaml`

```yaml
language: ko               # en, ko
default_provider: claude   # claude, vibe, auto
max_sheep: 12              # 최대 양 수
db_path: ~/.shepherd/shepherd.db
log_level: info            # debug, info, warn, error
server_port: 8585
server_host: 0.0.0.0
auto_approve: true

# 인증 (shepherd auth setup으로 설정)
auth_username: admin
auth_password_hash: "$2a$10$..."
auth_jwt_secret: "자동 생성"
```

---

## 아키텍처

```
사용자 입력 → 인터랙티브 CLI / Web UI / MCP 클라이언트
          → Shepherd 데몬 (REST API + SSE)
          → 매니저 (Claude Code CLI로 의도 분석)
          → 적절한 양에게 라우팅
          → 워커가 Claude Code 실행 (--print [--resume SESSION_ID])
          → 큐가 결과 기록
          → SSE로 실시간 업데이트 → 모든 연결된 클라이언트
```

상세 아키텍처 문서: [ARCHITECTURE_KO.md](ARCHITECTURE_KO.md)

### 멀티 프로바이더 지원

| 프로바이더 | CLI | 용도 |
|-----------|-----|------|
| `claude` | Claude Code | 기본 — 코드 작성, 복잡한 작업 |
| `vibe` | Mistral Vibe | 리뷰, 웹 검색, 단순 작업 |
| `auto` | 자동 선택 | 프롬프트 분석 후 최적 프로바이더 선택 |

### 프로젝트 구조

```
shepherd/
├── cmd/shepherd/          # CLI 진입점 (~2000줄, 전체 명령어)
├── ent/schema/            # Ent ORM 엔티티 (Sheep, Project, Task, Skill, Schedule)
├── internal/
│   ├── agent/             # AI 프로바이더 추상화 (Claude, Vibe)
│   ├── browser/           # 브라우저 자동화 (Rod)
│   ├── config/            # Viper 기반 YAML 설정
│   ├── daemon/            # PID 파일, 시그널 처리, 생명주기
│   ├── db/                # SQLite 데이터베이스
│   ├── i18n/              # 다국어 지원 (en, ko)
│   ├── manager/           # 작업 분석 및 라우팅
│   ├── mcp/               # JSON-RPC 2.0 MCP 서버
│   ├── names/             # 양 이름 풀
│   ├── project/           # 프로젝트 CRUD
│   ├── queue/             # 작업 생명주기 관리
│   ├── scheduler/         # Cron & 인터벌 스케줄링
│   ├── server/            # Fiber HTTP 서버, SSE, 인증, 핸들러
│   ├── skill/             # 파일 기반 스킬 시스템
│   ├── tui/               # Bubbletea 터미널 UI
│   └── worker/            # 양 실행 및 세션 관리
└── web/                   # Svelte SPA (순수 JavaScript, TypeScript 미사용)
```

---

## REST API

인증 및 헬스를 제외한 모든 엔드포인트는 JWT 인증이 필요합니다.

### 인증
```
POST /api/auth/login               # 액세스 + 리프레시 토큰 발급
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
GET              /api/tasks/:id                # 상세 조회
POST             /api/tasks/:id/stop           # 실행 중 작업 중지
```

### Git (읽기 전용)
```
GET /api/projects/:name/git/log                # 커밋 이력
GET /api/projects/:name/git/branches           # 브랜치 목록
GET /api/projects/:name/git/commits/:hash      # 커밋 상세
GET /api/projects/:name/git/commits/:hash/diff # 커밋 diff
GET /api/projects/:name/git/changes            # 미커밋 변경사항
```

### 스케줄 & 스킬
```
GET|POST         /api/projects/:name/schedules      # 목록 / 생성
GET|PATCH|DELETE /api/projects/:name/schedules/:id   # CRUD
POST             /api/projects/:name/schedules/:id/run  # 즉시 실행

GET|POST         /api/skills                    # 글로벌 목록 / 생성
POST             /api/skills/import             # 가져오기
GET|PATCH|DELETE /api/skills/:id                # CRUD
GET              /api/skills/:id/export         # 내보내기
GET|POST         /api/projects/:name/skills     # 프로젝트 범위 스킬
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

Shepherd를 Claude Desktop 연동용 MCP 서버로 실행:

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

**사용 가능한 MCP 도구:** `task_start`, `task_complete`, `task_error`, `get_history`, `get_status`, 그리고 20+ 브라우저 자동화 도구 (`browser_open`, `browser_click`, `browser_type`, `browser_screenshot` 등)

---

## 양 이름

기본 이름 풀 (한국어 양 이름):

양동이, 양말이, 양철이, 양순이, 메에롱, 깜순이, 흰둥이, 복실이, 숀, 뭉치, 구름이, 몽실이

커스텀 이름 추가: `shepherd names add <name>`

---

## 에러 처리 & 복구

- **자동 복구**: 데몬/TUI 시작 시 중단된 양과 작업 자동 복구
- **수동 복구**: `shepherd recover`
- **안전 종료**: SIGINT/SIGTERM 처리, 종료 전 상태 저장
- **타임아웃**: 60초 (작업 분석), 30분 (인터랙티브 실행)

---

## 개발

```bash
go build ./...                              # 전체 패키지 빌드
go test ./...                               # 전체 테스트
go test ./internal/worker -run TestName     # 특정 테스트
go generate ./ent                           # Ent ORM 코드 재생성

cd web && npm install && npm run dev        # Web UI 개발 서버
cd web && npm run build                     # Web UI 프로덕션 빌드
```

## 기여

[CONTRIBUTING.md](CONTRIBUTING.md)를 참고하세요.

## 라이선스

MIT License — [LICENSE](LICENSE) 참고.

---

> ***"여호와는 나의 목자시니 내게 부족함이 없으리로다"*** — 시편 23:1
