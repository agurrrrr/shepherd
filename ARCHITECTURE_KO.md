# 아키텍처

> 영문 버전: [ARCHITECTURE.md](ARCHITECTURE.md)

## 설계 원칙

1. **모든 AI 상호작용은 CLI 서브프로세스를 통해 수행** — 직접 API 호출 없음, API 키 관리 불필요
2. **데몬이 모든 비즈니스 로직을 소유** — CLI, Web UI, MCP는 순수 프레젠테이션 계층
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
│  WebUI  │   │  CLI 클라이언트 │  │   서버     │
│(브라우저)│   │  (터미널)   │  │  (stdio)   │
└─────────┘   └─────────────┘  └────────────┘
```

## 데이터 흐름

### 작업 실행

```
1. 사용자가 작업 제출
   ├── CLI:   shepherd "로그인 기능 추가" → POST /api/embedded/run (cwd에서 임베디드 실행)
   ├── WebUI: POST /api/tasks (프로젝트 화면) 또는 POST /api/shepherd/tasks (목자 콘솔)
   └── MCP:   task_start 도구 호출

2. 작업이 큐에 추가
   └── 큐 프로세서가 대기 중인 작업을 양에게 배정
       (프로젝트 작업 → 그 프로젝트의 양, 목자 작업 → 매니저 양)

3. 워커가 실행
   ├── Claude Code CLI: claude --print --resume <session_id> -p <prompt>
   ├── 또는 OpenCode CLI: opencode --resume <session_id> -p <prompt>
   └── 출력이 SSE를 통해 실시간 스트리밍

4. 결과 기록
   ├── 작업 상태: running → completed/failed
   ├── 요약, 수정된 파일, 에러 (있을 경우)
   └── 대화 연속성을 위해 세션 ID 보존
```

### 임베디드 직접 실행 (CLI)

`shepherd`와 `shepherd "..."`는 위 2~3단계를 건너뛰는 지름길을 사용합니다.
CLI가 `{project_path: cwd, prompt}`를 `POST /api/embedded/run`으로 보내면,
데몬이 이미 배선된 임베디드 executor를 해당 디렉토리에서 실행하고 출력을
요청 단위 SSE로 돌려줍니다(전역 `/api/events` 허브를 거치지 않습니다).
프로젝트 등록, 양 할당, 큐 작업이 관여하지 않습니다.

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
│   ├── opencode.go      # OpenCode CLI 래퍼
│   └── router.go        # 프롬프트 분석 기반 자동 선택 로직
│
├── browser/             # 브라우저 자동화 (Rod)
│   ├── manager.go       # 브라우저 생명주기 (실행, 연결, 종료)
│   ├── session.go       # 양별 세션 관리, 멀티 페이지
│   └── actions.go       # 20+ 액션 (클릭, 입력, 스크린샷 등)
│
├── checkpoint/          # 숨은 git ref로 작업 트리 스냅샷
│   └── checkpoint.go    # Probe, Take, Changes, Transplant, 작업 ref
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

## 작업 체크포인트

`internal/checkpoint`는 git 플러밍만으로 프로젝트 작업 트리를 스냅샷합니다. `internal/procutil`과 표준 라이브러리만 import하고 `ent`, `queue`, `server`는 import하지 않으므로, 큐와 API가 순환 의존 없이 함께 사용할 수 있습니다.

- **Ref 구조**: `refs/shepherd/tasks/<id>/start`, `end`, `pre-revert`, `reverted`. 이 prefix만 조회·삭제하므로 호출 버그가 사용자의 브랜치나 태그를 건드릴 수 없습니다.
- **임시 인덱스**: 스냅샷마다 실제 인덱스를 mtime을 보존해 복사한 뒤 git 공통 디렉터리 아래 `shepherd-ckpt-index-<랜덤>`을 만들고 `GIT_INDEX_FILE`로 `git add -A`를 실행합니다. 사용자의 실제 인덱스는 바뀌지 않으며, 임시 인덱스와 lock 파일은 성공·실패 모두에서 지웁니다. 커밋이 없는 저장소에서는 부모 없는 스냅샷 커밋을 만듭니다.
- **크기 제한**: 추적되지 않은 파일 20,000개, 파일 하나 50MB, 합계 200MB. 제한을 넘으면 `ErrTooLarge`를 돌려주고 ref를 만들지 않습니다. 큰 파일만 빼고 찍으면 시작과 종료 스냅샷의 대상이 달라져 가짜 추가·삭제가 생기기 때문입니다.
- **Diff와 복원**: diff에는 항상 `--no-color --no-ext-diff --no-textconv --src-prefix=a/ --dst-prefix=b/`를 붙여 사용자 git 설정이 파싱을 깨지 않게 합니다. 복원은 `git merge-tree --write-tree`로 3-way 병합하고 `git apply`(`--index` 없이)로 적용해 사용자의 인덱스를 건드리지 않습니다. 충돌이면 파일 목록을 돌려주고 작업 트리는 바꾸지 않습니다.
- **프로세스 안전성**: `Take`와 `Transplant`는 저장소 최상위 경로별 프로세스 내 뮤텍스로 직렬화합니다. merge-tree 충돌 메시지를 파싱하므로 모든 git 호출에 `LC_ALL=C`를 사용합니다. 작성자와 커미터는 `shepherd <shepherd@localhost>`로 고정합니다.
- **보존 정책**: `task_checkpoint_retention_days`(기본값 30, `0`이면 비활성)에 따라 데몬 시작 직후 한 번, 그 뒤 매일 정리합니다. 작업 ID별 ref를 모아 DB에 없는 작업과 기준 시각(`completed_at`, 되돌린 작업은 `reverted_at`)이 지난 작업의 ref를 지우고 `[checkpoint] pruned N refs in <dir>`를 로그로 남깁니다. 스냅샷 객체는 git 기본 gc(도달 불가 객체 2주 후 삭제)에 맡깁니다. 작업 삭제 시에는 행을 지우기 전에 ref를 지우고, 실패하면 로그만 남기고 다음 정리에서 다시 시도합니다.
- **알려진 한계**: 스냅샷이 작업 트리 전체이므로 작업 중 다른 프로세스가 만든 변경도 함께 포함됩니다. 데몬이 작업 중 비정상 종료하면 종료 스냅샷이 없고, 복구된 작업에는 조용한 부분 diff 대신 `checkpoint_error`가 기록됩니다. 내보내기·가져오기는 체크포인트 메타데이터만 옮기고 ref는 옮기지 않으므로 다른 곳에서 복원하면 diff 이력이 없습니다.
- **작업 삭제**: `queue.DeleteTask`가 행을 지우기 전에 체크포인트 ref를 지웁니다. 실패는 로그만 남기고 삭제를 막지 않습니다.

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
| 임베디드 직접 실행 엔드포인트 | CLI `shepherd "..."`가 `/api/embedded/run`으로 cwd에서 에이전트를 실행해 매니저·양·큐를 우회 |
| Rod로 브라우저 자동화 | Chromium DevTools Protocol, 외부 브라우저 드라이버 불필요 |
| 설정 기반 인증 | 단일 사용자 도구; DB에 사용자 테이블 불필요 |
| 1:1 양-프로젝트 매핑 | 세션 연속성; 각 양이 대화 컨텍스트 유지 |

## 멀티 프로바이더 아키텍처

```
AgentProvider (인터페이스)
├── ClaudeProvider    # claude --print --output-format json
├── OpenCodeProvider  # opencode -p <prompt> --resume <session>
└── AutoRouter        # 프롬프트 분석 → 최적 프로바이더 선택
                      # Rate limit 시 자동 폴백
```

양별 프로바이더 선택: `claude` (기본), `opencode`, 또는 `auto`.

## 보안 모델

- **인증**: JWT + bcrypt, 설정 기반 단일 사용자
- **JWT 자동 생성**: 첫 시작 시 32바이트 랜덤 시크릿
- **빈 JWT 거부**: 401 반환 (익명 접근 차단)
- **CORS**: `SHEPHERD_CORS_ORIGIN` 환경변수로 설정 가능
- **Git 입력 검증**: 브랜치명, 커밋 해시, 파일 경로 검증
- **경로 순회 보호**: Docs 엔드포인트는 프로젝트 내 `.md` 파일만 제공
- **레포에 시크릿 없음**: 설정 파일은 `~/.shepherd/`에 위치, 프로젝트 디렉토리 아님
