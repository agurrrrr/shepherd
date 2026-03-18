package spec

import "fmt"

// SpecTypes is the list of available spec types.
var SpecTypes = []string{
	"overview", "api", "db-schema", "db-erd", "screen",
	"flow", "requirements", "infra", "env",
}

// Generate creates spec template content for the given type and title.
func Generate(specType, title string) (string, error) {
	tmpl, ok := Templates[specType]
	if !ok {
		return "", fmt.Errorf("unknown spec type: %s\nValid types: overview, api, db-schema, db-erd, screen, flow, requirements, infra, env", specType)
	}
	return fmt.Sprintf(tmpl, title), nil
}

// Templates contains markdown templates for each spec type.
var Templates = map[string]string{
	"overview": `# %s

## 1. 개요
<!-- 프로젝트 목표, 타겟 사용자, 핵심 가치를 작성하세요 -->

## 2. 기술 스택

| 구분 | 기술 | 비고 |
|------|------|------|
| Backend | | |
| Frontend | | |
| Database | | |
| Infra | | |

## 3. 시스템 아키텍처

` + "```mermaid" + `
graph TD
  Client[Client] --> API[API Server]
  API --> DB[(Database)]
` + "```" + `

## 4. 주요 기능

- [ ] 기능 1
- [ ] 기능 2
- [ ] 기능 3

## 5. 일정

| 단계 | 기간 | 내용 |
|------|------|------|
| Phase 1 | | |
| Phase 2 | | |
`,

	"api": `# %s — API 명세서

## Base URL

` + "`" + `https://api.example.com/v1` + "`" + `

## 인증

| 방식 | 설명 |
|------|------|
| Bearer Token | Authorization: Bearer {token} |

## 엔드포인트

### 인증

| Method | Path | 설명 | 인증 |
|--------|------|------|------|
| POST | /auth/login | 로그인 | 불필요 |
| POST | /auth/signup | 회원가입 | 불필요 |
| GET | /auth/me | 내 정보 | 필요 |

#### POST /auth/login

**Request**
` + "```json" + `
{
  "email": "user@example.com",
  "password": "password123"
}
` + "```" + `

**Response 200**
` + "```json" + `
{
  "token": "eyJhbG...",
  "user": { "id": "...", "email": "..." }
}
` + "```" + `

**Errors**

| Code | 설명 |
|------|------|
| 400 | 잘못된 요청 |
| 401 | 인증 실패 |

### 리소스

| Method | Path | 설명 | 인증 |
|--------|------|------|------|
| GET | /resources | 목록 조회 | 필요 |
| POST | /resources | 생성 | 필요 |
| GET | /resources/{id} | 상세 조회 | 필요 |
| PUT | /resources/{id} | 수정 | 필요 |
| DELETE | /resources/{id} | 삭제 | 필요 |
`,

	"db-schema": `# %s — DB 스키마 명세서

## 테이블 목록

| 테이블 | 설명 |
|--------|------|
| users | 사용자 |

## users

| 컬럼 | 타입 | 제약조건 | 설명 |
|------|------|----------|------|
| id | VARCHAR(36) | PK | UUID |
| email | VARCHAR(255) | UNIQUE, NOT NULL | 이메일 |
| password_hash | VARCHAR(255) | NOT NULL | 비밀번호 해시 |
| name | VARCHAR(100) | | 이름 |
| created_at | DATETIME | DEFAULT NOW | 생성일시 |
| updated_at | DATETIME | DEFAULT NOW | 수정일시 |

### 인덱스

| 이름 | 컬럼 | 유형 |
|------|------|------|
| idx_users_email | email | UNIQUE |
`,

	"db-erd": `# %s — DB ERD

` + "```mermaid" + `
erDiagram
  USERS {
    string id PK
    string email UK
    string password_hash
    string name
    datetime created_at
    datetime updated_at
  }

  PROJECTS {
    string id PK
    string user_id FK
    string name
    string description
    datetime created_at
    datetime updated_at
  }

  USERS ||--o{ PROJECTS : owns
` + "```" + `
`,

	"screen": `# %s

## 기본 정보

| 항목 | 내용 |
|------|------|
| 경로 | / |
| 인증 | 필요/불필요 |
| 설명 | |

## 와이어프레임

<!-- html 코드블럭으로 작성하면 Wireframe CSS Kit이 적용되어 렌더링됩니다.
     디바이스: wf-browser(웹), wf-phone(모바일), wf-tablet(태블릿)
     레이아웃: wf-navbar, wf-sidebar, wf-content, wf-footer, wf-row, wf-col
     컴포넌트: wf-card, wf-list, wf-table, wf-tabs, wf-modal, wf-toast
     폼: wf-input, wf-textarea, wf-select, wf-checkbox, wf-radio, wf-search
     버튼: wf-btn, wf-btn-primary, wf-btn-danger, wf-btn-success, wf-btn-sm/lg/block
     텍스트: wf-title, wf-subtitle, wf-label, wf-text, wf-text-muted, wf-badge
     기타: wf-avatar, wf-icon, wf-placeholder, wf-divider
     유틸: wf-flex-1, wf-items-center, wf-justify-between, wf-gap-8/12/16, wf-p-16, wf-mt-8 -->

` + "```html" + `
<div class="wf-browser">
  <div class="wf-navbar">
    <span class="wf-navbar-brand">AppName</span>
    <div class="wf-navbar-links">
      <span>메뉴1</span>
      <span>메뉴2</span>
    </div>
  </div>
  <div class="wf-row" style="min-height:400px">
    <div class="wf-sidebar">
      <div class="wf-label">카테고리</div>
      <ul class="wf-list">
        <li class="wf-list-item">항목 1</li>
        <li class="wf-list-item">항목 2</li>
      </ul>
    </div>
    <div class="wf-content">
      <div class="wf-title">페이지 제목</div>
      <div class="wf-search">검색...</div>
      <div class="wf-card">
        <div class="wf-card-header">카드 제목</div>
        <div class="wf-text">콘텐츠 내용</div>
      </div>
      <div class="wf-row wf-gap-8 wf-mt-8">
        <span class="wf-btn wf-btn-primary">확인</span>
        <span class="wf-btn">취소</span>
      </div>
    </div>
  </div>
  <div class="wf-footer">Footer</div>
</div>
` + "```" + `

## 상태 전이

` + "```mermaid" + `
stateDiagram-v2
  [*] --> Default
  Default --> Loading : 액션 수행
  Loading --> Success : 성공
  Loading --> Error : 실패
  Error --> Default : 재시도
` + "```" + `

## UI 요소

| 요소 | 타입 | 설명 | 동작 |
|------|------|------|------|
| | | | |

## 예외 처리

- [ ] 빈 입력 처리
- [ ] 네트워크 에러 시 재시도
- [ ] 권한 없음 처리
`,

	"flow": `# %s — 사용자 플로우

## 메인 플로우

` + "```mermaid" + `
flowchart TD
  A[시작] --> B{로그인 여부}
  B -- 로그인됨 --> C[대시보드]
  B -- 미로그인 --> D[로그인 페이지]
  D --> E{로그인 성공?}
  E -- Yes --> C
  E -- No --> D
  C --> F[기능 수행]
  F --> G[완료]
` + "```" + `

## 상세 플로우

### 플로우 1: [플로우명]

` + "```mermaid" + `
sequenceDiagram
  actor User
  participant Client
  participant Server
  participant DB

  User->>Client: 액션
  Client->>Server: API 요청
  Server->>DB: 쿼리
  DB-->>Server: 결과
  Server-->>Client: 응답
  Client-->>User: UI 업데이트
` + "```" + `
`,

	"requirements": `# %s — 요구사항 명세

## 기능 요구사항

### 필수 (Must Have)

- [ ] FR-001:
- [ ] FR-002:
- [ ] FR-003:

### 선택 (Should Have)

- [ ] FR-010:
- [ ] FR-011:

### 향후 (Could Have)

- [ ] FR-020:

## 비기능 요구사항

### 성능

| 항목 | 기준 |
|------|------|
| 응답시간 | < 200ms (95th percentile) |
| 동시접속 | 100명 이상 |
| 가용성 | 99.9%% |

### 보안

- [ ] NFR-001: HTTPS 필수
- [ ] NFR-002: 비밀번호 해시 저장 (bcrypt)
- [ ] NFR-003: JWT 토큰 만료 설정
- [ ] NFR-004: SQL Injection 방어
- [ ] NFR-005: XSS 방어

### 호환성

- [ ] NFR-010: Chrome, Safari, Firefox 최신 버전
- [ ] NFR-011: 모바일 반응형 (768px 이하)
`,

	"infra": `# %s — 인프라/배포 설계

## 환경 구성

| 환경 | URL | 용도 |
|------|-----|------|
| Development | localhost:8080 | 로컬 개발 |
| Staging | | 테스트 |
| Production | | 운영 |

## 아키텍처

` + "```mermaid" + `
graph LR
  Client --> LB[Load Balancer]
  LB --> App1[App Server 1]
  LB --> App2[App Server 2]
  App1 --> DB[(Database)]
  App2 --> DB
` + "```" + `

## CI/CD

` + "```mermaid" + `
flowchart LR
  Push[Git Push] --> Build[Build]
  Build --> Test[Test]
  Test --> Deploy[Deploy]
` + "```" + `

## 모니터링

| 항목 | 도구 | 설명 |
|------|------|------|
| 로그 | | |
| 메트릭 | | |
| 알림 | | |
`,

	"env": `# %s — 환경변수/설정 명세

## 환경변수

| 변수명 | 필수 | 기본값 | 설명 | 예시 |
|--------|------|--------|------|------|
| PORT | N | 8080 | 서버 포트 | 8080 |
| DB_PATH | N | ./app.db | DB 파일 경로 | /data/app.db |
| JWT_SECRET | Y | | JWT 서명 키 | my-secret-key |
| TZ | N | UTC | 타임존 | Asia/Seoul |

## 환경별 설정

### Development

` + "```env" + `
PORT=8080
DB_PATH=./dev.db
JWT_SECRET=dev-secret
TZ=Asia/Seoul
` + "```" + `

### Staging

` + "```env" + `
PORT=8080
DB_PATH=/data/staging.db
JWT_SECRET=
TZ=Asia/Seoul
` + "```" + `

### Production

` + "```env" + `
PORT=8080
DB_PATH=/data/prod.db
JWT_SECRET=
TZ=Asia/Seoul
` + "```" + `

## 시크릿 관리

| 시크릿 | 저장소 | 갱신 주기 |
|--------|--------|----------|
| JWT_SECRET | K8s Secret | 분기별 |
`,
}
