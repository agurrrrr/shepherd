# 아키텍처

> 영문 버전: [ARCHITECTURE.md](ARCHITECTURE.md)

## 설계 원칙

1. **모든 AI 상호작용은 CLI 서브프로세스를 통해 수행** — 직접 API 호출 없음, API 키 관리 불필요
2. **데몬이 모든 비즈니스 로직을 소유** — CLI, Web UI, TUI는 순수 프레젠테이션 계층
3. **양 1마리 = 프로젝트 1개** — 각 프로젝트에 전담 Claude Code 워커가 세션을 유지
4. **단일 바이너리 배포** — Web UI를 `go:embed`로 내장

## 시스템 개요

```
┌─────────────────────────────────────────────────────────────┐
│                     Shepherd 데몬                             │
│                    (shepherd serve)                           │
│                                                              │
│  ┌──────────┐  ┌───────────┐  ┌──────────┐  ┌───────────┐ │
│  │  Fiber   │  │   큐      │  │   인증   │  │ 스케줄러  │ │
│  │ REST API │  │  프로세서  │  │ JWT+     │  │ Cron/     │ │
│  │ + SSE    │  │           │  │ bcrypt   │  │ Interval  │ │
│  └────┬─────┘  └─────┬─────┘  └──────────┘  └───────────┘ │
│       │              │                                       │
│  ┌────┴──────────────┴──────┐                               │
│  │      SSE 이벤트 허브       │                               │
│  │   (실시간 브로드캐스트)     │                               │
│  └────┬──────────────┬──────┘                               │
│       │              │                                       │
│  ┌────┴────┐   ┌─────┴──────┐   ┌───────────┐             │
│  │  워커   │   │  SQLite    │   │   스킬    │             │
│  │  관리자  │   │ (Ent ORM)  │   │   엔진   │             │
│  └─────────┘   └────────────┘   └───────────┘             │
└──────┬──────────────┬──────────────┬────────────────────────┘
       │              │              │
┌──────┴──┐   ┌──────┴──────┐  ┌───┴────────┐
│  Svelte │   │ 인터랙티브  │  │    MCP     │
│  WebUI  │   │  CLI / TUI  │  │   서버     │
│(브라우저)│   │  (터미널)   │  │  (stdio)   │
└─────────┘   └─────────────┘  └────────────┘
```

## 데이터 흐름

### 작업 실행

```
1. 사용자가 작업 제출
   ├── CLI:   shepherd "로그인 기능 추가"
   ├── WebUI: POST /api/command 또는 POST /api/tasks
   └── MCP:   task_start 도구 호출

2. 매니저가 의도 분석 (Claude Code CLI 서브프로세스)
   └── 결정: 대상 프로젝트, 대상 양, 우선순위

3. 작업이 큐에 추가
   └── 큐 프로세서가 대기 중인 작업 처리

4. 워커가 실행
   ├── Claude Code CLI: claude --print --resume <session_id> -p <prompt>
   ├── 또는 Vibe CLI:   vibe --resume <session_id> -p <prompt>
   └── 출력이 SSE를 통해 실시간 스트리밍

5. 결과 기록
   ├── 작업 상태: running → completed/failed
   ├── 요약, 수정된 파일, 에러 (있을 경우)
   └── 대화 연속성을 위해 세션 ID 보존
```

### 인증 흐름

```
POST /api/auth/login {username, password}
  → config의 해시와 bcrypt 검증
  → JWT 액세스 토큰 (24시간) + 리프레시 토큰 (7일) 발급
  → 클라이언트가 토큰 저장, 매 요청마다 Bearer 헤더 전송

AuthMiddleware:
  → Authorization 헤더에서 Bearer 토큰 추출
  → JWT 서명 및 만료 검증
  → JWT 시크릿이 비어있으면 거부 (보안: 익명 접근 차단)
```

## 패키지 구조

```
cmd/shepherd/
└── main.go              # CLI 진입점 (~2000줄)
                         # 모든 Cobra 명령어 정의

ent/
└── schema/              # 데이터베이스 엔티티
    ├── sheep.go         # id, name, status, session_id, provider, project_id
    ├── project.go       # id, name, path, description
    ├── task.go          # id, prompt, status, summary, files_modified, error
    ├── skill.go         # id, name, content, tags, project_id (nullable)
    └── schedule.go      # id, type, expression, prompt, enabled, project_id

internal/
├── agent/               # AI 프로바이더 추상화
│   ├── provider.go      # AgentProvider 인터페이스
│   ├── claude.go        # Claude Code CLI 래퍼
│   ├── vibe.go          # Mistral Vibe CLI 래퍼
│   └── router.go        # 프롬프트 분석 기반 자동 선택 로직
│
├── browser/             # 브라우저 자동화 (Rod)
│   ├── manager.go       # 브라우저 생명주기 (실행, 연결, 종료)
│   ├── session.go       # 양별 세션 관리, 멀티 페이지
│   └── actions.go       # 20+ 액션 (클릭, 입력, 스크린샷 등)
│
├── config/              # Viper 기반 YAML 설정
│   └── config.go        # 기본값, 로드, get/set, 파일 경로
│
├── daemon/              # 데몬 프로세스 관리
│   └── daemon.go        # PID 파일, 시그널 처리, 시작/중지/상태
│
├── db/                  # SQLite 데이터베이스
│   └── db.go            # Ent 클라이언트 초기화
│
├── i18n/                # 다국어 지원
│   └── i18n.go          # Messages 구조체 (ko/en), T() 접근자
│
├── manager/             # 작업 분석 및 라우팅
│   └── manager.go       # Claude CLI 기반 의도 분석 (JSON 스키마)
│
├── mcp/                 # MCP 서버 (JSON-RPC 2.0, stdio)
│   └── server.go        # 도구 등록, 요청 처리
│
├── names/               # 양 이름 관리
│   └── names.go         # 기본 이름 풀 + 커스텀 이름 CRUD
│
├── project/             # 프로젝트 관리
│   └── project.go       # 추가, 삭제, 목록, 양 배정 (1:1 매핑)
│
├── queue/               # 작업 생명주기
│   └── queue.go         # 생성, 처리, 완료, 실패, 목록
│
├── scheduler/           # 예약 작업 실행
│   └── scheduler.go     # Cron 파서, 인터벌 타이머, 자동 작업 생성
│
├── server/              # HTTP 서버 (Fiber)
│   ├── server.go        # 앱 초기화, 라우트 등록, 미들웨어
│   ├── auth.go          # JWT 발급/검증, bcrypt, 로그인/갱신
│   ├── middleware.go     # 인증 미들웨어, CORS
│   ├── sse.go           # SSE 이벤트 허브 (전체 클라이언트 브로드캐스트)
│   ├── handlers_sheep.go
│   ├── handlers_project.go
│   ├── handlers_task.go
│   ├── handlers_system.go
│   ├── handlers_git.go       # 읽기 전용 git 연산
│   ├── handlers_docs.go      # 프로젝트 마크다운 뷰어
│   ├── handlers_skill.go
│   ├── handlers_schedule.go
│   └── handlers_upload.go
│
├── skill/               # 스킬 시스템
│   └── skill.go         # CRUD, 번들 스킬, import/export
│
├── tui/                 # 터미널 UI (Bubbletea)
│   ├── tui.go           # TUI 생명주기, 하이브리드 모드 (독립/클라이언트)
│   └── views/           # 분할 뷰, 대시보드, 렌더러
│
└── worker/              # 양 실행
    ├── worker.go        # CRUD, 상태 관리
    └── interactive.go   # CLI 서브프로세스 실행, 출력 파싱

web/                     # Svelte SPA (순수 JavaScript, TypeScript 미사용)
├── src/
│   ├── lib/
│   │   ├── api.js       # REST 클라이언트 (Bearer 토큰 자동 첨부)
│   │   ├── sse.js       # SSE 연결 (자동 재연결)
│   │   └── stores.js    # Svelte 스토어 (인증, 양, 작업 등)
│   └── routes/          # SvelteKit 파일 기반 라우팅
│       ├── +page.svelte           # 대시보드
│       ├── login/+page.svelte     # 로그인
│       ├── sheep/+page.svelte     # 양 관리
│       ├── projects/+page.svelte  # 프로젝트 목록
│       ├── projects/[name]/       # 프로젝트 상세 (git, docs, 스케줄)
│       ├── tasks/+page.svelte     # 작업 목록
│       ├── tasks/[id]/            # 작업 상세
│       ├── schedules/+page.svelte # 스케줄 관리
│       ├── skills/+page.svelte    # 스킬 관리
│       └── settings/+page.svelte  # 설정
└── build/               # 프로덕션 빌드 → go:embed로 내장
```

## 엔티티 관계

```
Sheep 1:1 Project       # 각 양은 정확히 하나의 프로젝트에 배정
Project 1:N Task        # 프로젝트는 여러 작업을 가짐
Sheep 1:N Task          # 양은 여러 작업을 실행
Project 1:N Schedule    # 프로젝트는 여러 스케줄을 가짐
Project 0:N Skill       # 프로젝트는 프로젝트 범위 스킬을 가질 수 있음
(global) 0:N Skill      # project_id가 없는 스킬은 글로벌
```

## 주요 기술적 결정

| 결정 | 이유 |
|------|------|
| AI에 CLI 서브프로세스 사용 | API 키 관리 불필요; Claude Code가 인증 처리 |
| SQLite + Ent ORM | 타입 안전, 코드 자동 생성, 외부 DB 의존성 없음 |
| Fiber HTTP 프레임워크 | 빠르고 Express 스타일 API, 내장 미들웨어 |
| WebSocket 대신 SSE | 더 단순, HTTP 네이티브, 단방향 스트리밍에 충분 |
| Svelte (JS만 사용) | 경량, TypeScript 복잡성 없음 |
| go:embed로 WebUI 내장 | 단일 바이너리 배포, 별도 정적 서버 불필요 |
| Bubbletea TUI | 분할 뷰와 실시간 업데이트를 지원하는 풍부한 터미널 UI |
| Rod로 브라우저 자동화 | Chromium DevTools Protocol, 외부 브라우저 드라이버 불필요 |
| 설정 기반 인증 | 단일 사용자 도구; DB에 사용자 테이블 불필요 |
| 1:1 양-프로젝트 매핑 | 세션 연속성; 각 양이 대화 컨텍스트 유지 |

## 멀티 프로바이더 아키텍처

```
AgentProvider (인터페이스)
├── ClaudeProvider    # claude --print --output-format json
├── VibeProvider      # vibe -p <prompt> --resume <session>
└── AutoRouter        # 프롬프트 분석 → 최적 프로바이더 선택
                      # Rate limit 시 자동 폴백
```

양별 프로바이더 선택: `claude` (기본), `vibe`, 또는 `auto`.

## 보안 모델

- **인증**: JWT + bcrypt, 설정 기반 단일 사용자
- **JWT 자동 생성**: 첫 시작 시 32바이트 랜덤 시크릿
- **빈 JWT 거부**: 401 반환 (익명 접근 차단)
- **CORS**: `SHEPHERD_CORS_ORIGIN` 환경변수로 설정 가능
- **Git 입력 검증**: 브랜치명, 커밋 해시, 파일 경로 검증
- **경로 순회 보호**: Docs 엔드포인트는 프로젝트 내 `.md` 파일만 제공
- **레포에 시크릿 없음**: 설정 파일은 `~/.shepherd/`에 위치, 프로젝트 디렉토리 아님
