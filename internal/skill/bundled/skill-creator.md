---
name: skill-creator
description: 새 스킬(SKILL.md)을 설계·작성·검증하는 인터랙티브 메타 가이드. 유스케이스 정의, YAML frontmatter 생성, 지침 작성, 트리거/기능 검증까지 단계별로 안내합니다. 사용자가 "스킬 만들어줘", "skill 생성", "SKILL.md 작성", "스킬 빌딩", "이 워크플로우를 스킬로", "shepherd 스킬 추가"를 요청할 때 사용하세요.
tags: [meta, skill, authoring]
scope: global
effort: medium
maxTurns: 20
---
# Skill Creator — 스킬을 만드는 스킬

Anthropic의 *The Complete Guide to Building Skills for Claude* 방법론을 그대로 반영한 가이드입니다. 새 스킬을 만들 때 아래 단계를 **순서대로** 밟으세요. 15~30분이면 동작하는 스킬 하나를 만들 수 있습니다.

## 스킬이란?

스킬은 하나의 폴더입니다:

- `SKILL.md` (필수): YAML frontmatter가 붙은 Markdown 지침
- `scripts/` (선택): 실행 가능한 코드 (Python, Bash 등)
- `references/` (선택): 필요할 때 읽는 상세 문서
- `assets/` (선택): 출력에 쓰는 템플릿·폰트·아이콘

핵심 원리는 **점진적 공개(Progressive Disclosure)** 3단계입니다:

1. **1단계 — frontmatter**: 항상 시스템 프롬프트에 로드. "이 스킬을 언제 써야 하는지"만 담아 전체를 컨텍스트에 올리지 않게 함.
2. **2단계 — SKILL.md 본문**: 관련 있다고 판단될 때 로드. 전체 지침·가이드.
3. **3단계 — 링크된 파일**: 필요할 때만 골라 읽는 `references/`, `scripts/`.

이렇게 해야 토큰을 아끼면서 전문성을 유지합니다.

## 1단계 — 유스케이스부터 정의 (코드 짜기 전에)

스킬이 가능하게 할 **구체적 유스케이스 2~3개**를 먼저 적으세요.

좋은 유스케이스 정의 예시:

```
Use Case: 프로젝트 스프린트 계획
Trigger: 사용자가 "이번 스프린트 계획 짜줘" 또는 "스프린트 태스크 만들어줘"
Steps:
  1. 현재 프로젝트 상태 조회
  2. 팀 속도/여력 분석
  3. 태스크 우선순위 제안
  4. 라벨·추정치 붙여 태스크 생성
Result: 태스크까지 생성된 완성된 스프린트 계획
```

스스로 물어보세요:

- 사용자가 무엇을 달성하려 하는가?
- 어떤 다중 단계 워크플로우가 필요한가?
- 어떤 도구가 필요한가 (내장 기능 or MCP)?
- 어떤 도메인 지식·베스트 프랙티스를 심어야 하는가?

세 가지 흔한 유형:

- **문서·에셋 생성**: 일관된 고품질 산출물 (문서, 프레젠테이션, 코드, 디자인). 외부 도구 불필요.
- **워크플로우 자동화**: 일관된 방법론이 필요한 다중 단계 프로세스. 검증 게이트·반복 개선 루프 포함.
- **MCP 강화**: MCP 서버가 노출한 도구 접근을 워크플로우 지침으로 보강.

## 2단계 — 성공 기준 정의

"이 스킬이 잘 동작하는지" 판단할 대략적 목표(엄밀한 임계값 아님)를 정합니다.

정량 지표:

- 관련 질의의 90%에서 스킬이 트리거되는가
- 워크플로우를 N번의 도구 호출 안에 완료하는가 (스킬 on/off 비교)
- 워크플로우당 실패한 API 호출 0건인가

정성 지표:

- 사용자가 다음 단계를 매번 지시하지 않아도 되는가
- 사용자 교정 없이 워크플로우가 끝나는가
- 세션이 달라도 일관된 결과가 나오는가

## 3단계 — 파일 구조와 네이밍 규칙

```
your-skill-name/
├── SKILL.md              # 필수 — 메인 스킬 파일
├── scripts/              # 선택 — 실행 코드
│   ├── process_data.py
│   └── validate.sh
├── references/           # 선택 — 상세 문서
│   └── api-guide.md
└── assets/               # 선택 — 템플릿 등
    └── report-template.md
```

지켜야 할 규칙:

- **파일명은 정확히 `SKILL.md`** (대소문자 구분). `SKILL.MD`, `skill.md` 전부 거부됨.
- **폴더명은 kebab-case**: `notion-project-setup` (O). 공백·언더스코어·대문자 금지 (`Notion Project Setup`, `notion_project_setup`, `NotionProjectSetup` 전부 X).
- **스킬 폴더 안에 `README.md` 금지**. 모든 문서는 `SKILL.md` 또는 `references/`로. (GitHub 배포용 저장소 레벨 README는 별개 — 사람 대상.)

## 4단계 — YAML frontmatter (가장 중요한 부분)

frontmatter는 Claude가 "이 스킬을 로드할지" 판단하는 근거입니다. 최소 형식:

```
---
name: your-skill-name
description: 무엇을 하는지 + 언제 쓰는지. 사용자가 [구체적 표현]을 말할 때.
---
```

**name (필수)**: kebab-case만, 공백·대문자 금지, 폴더명과 일치.

**description (필수)** — 반드시 아래를 담을 것:

- **무엇을 하는지 (WHAT)** + **언제 쓰는지 (WHEN, 트리거 조건)**
- 1024자 이하
- XML 태그(`<`, `>`) 금지
- 사용자가 실제로 말할 법한 **구체적 트리거 문구** 포함
- 관련 있으면 파일 형식 언급 (예: .fig, PDF)

구조 공식: `[무엇을 하는지] + [언제 쓰는지] + [핵심 능력]`

좋은 description 예:

```
description: Figma 디자인 파일을 분석해 개발자 핸드오프 문서를 생성. 사용자가
  .fig 파일을 올리거나 "디자인 스펙", "컴포넌트 문서", "디자인→코드 핸드오프"를
  요청할 때 사용.
```

나쁜 description 예:

```
# 너무 모호 — 트리거 없음
description: 프로젝트를 도와줍니다.

# 너무 기술적 — 사용자 트리거 없음
description: 계층적 관계를 가진 Project 엔티티 모델을 구현.
```

선택 필드:

- `license`: 오픈소스 배포 시 (MIT, Apache-2.0 등)
- `compatibility`: 환경 요구사항 (1~500자)
- `allowed-tools`: 도구 접근 제한 (예: `Bash(python:*) Bash(npm:*) WebFetch`)
- `metadata`: author, version, mcp-server 등 커스텀 키-값

**보안 제약**: frontmatter는 시스템 프롬프트에 들어가므로 `<`,`>` 금지, 이름에 `claude`/`anthropic` 접두사 금지(예약). YAML에서 코드 실행 금지(안전 파서 사용).

## 5단계 — 본문 지침 작성

frontmatter 뒤에 Markdown으로 실제 지침을 씁니다. 권장 골격:

```
# Your Skill Name

## Instructions

### Step 1: [첫 번째 주요 단계]
무엇이 일어나는지 명확히 설명.

### Step 2: ...

## Examples
### Example 1: [흔한 시나리오]
User says: "..."
Actions: 1. ... 2. ...
Result: ...

## Troubleshooting
### Error: [흔한 에러 메시지]
Cause: [왜 발생]
Solution: [해결 방법]
```

지침 베스트 프랙티스:

- **구체적이고 실행 가능하게**. (O) "`python scripts/validate.py --input {filename}` 실행. 실패 시 흔한 원인: 필수 필드 누락, 날짜 형식(YYYY-MM-DD) 오류." (X) "진행 전에 데이터를 검증하세요."
- **에러 핸들링 포함**. 흔한 실패와 복구 절차를 번호 목록으로.
- **점진적 공개 활용**. SKILL.md는 핵심 지침에 집중하고 상세는 `references/`로 이동 후 링크. `SKILL.md는 5,000단어 이하` 유지.
- **번들 리소스는 명확히 참조**. 예: "쿼리 작성 전 `references/api-patterns.md`의 rate limiting·pagination·error code 확인."
- 중요한 검증은 언어 해석 대신 **결정론적 스크립트**로 처리하는 것을 고려 (`scripts/`).

## 6단계 — 검증과 테스트

세 영역을 테스트하세요. **한 태스크에서 완성될 때까지 반복한 뒤** 다른 케이스로 확장하는 것이 가장 효율적입니다.

**1) 트리거 테스트** — 적절한 순간에 로드되는가.

```
Should trigger:
- "새 ProjectHub 워크스페이스 세팅 도와줘"
- "ProjectHub에 프로젝트 만들어야 해"

Should NOT trigger:
- "샌프란시스코 날씨 어때?"
- "파이썬 코드 짜줘"
```

**2) 기능 테스트** — 올바른 산출물이 나오는가 (유효 출력, API 성공, 에러 핸들링, 엣지 케이스).

**3) 성능 비교** — 스킬 없음(왕복 15회, 실패 3회, 12,000토큰) vs 스킬 있음(자동 워크플로우, 확인 질문 2회, 실패 0회, 6,000토큰).

## 흔한 문제 해결

- **트리거 안 됨(under-triggering)**: description에 디테일·키워드(특히 기술 용어) 추가. "이 스킬 언제 쓰냐"고 물어 description을 되읽게 해 빠진 부분 파악.
- **너무 자주 트리거(over-triggering)**: 네거티브 트리거 추가("단순 데이터 탐색에는 쓰지 말 것"), 더 구체적으로, 스코프 명확화.
- **지침 안 따름**: 지침을 간결하게, 핵심을 맨 위에(`## Important`/`## Critical`), 모호어 제거. 결정론적 검증은 스크립트로.
- **응답 느림/저하(large context)**: 상세를 `references/`로 이동, SKILL.md 5,000단어 이하, 동시 활성 스킬 20~50개 초과 시 선택적 활성화.

## 업로드 전 빠른 체크리스트

- [ ] 유스케이스 2~3개 정의됨
- [ ] 폴더명 kebab-case, `SKILL.md` 정확한 철자
- [ ] frontmatter에 `---` 구분자, name(kebab), description(WHAT+WHEN+트리거)
- [ ] XML 태그(`<`,`>`) 없음
- [ ] 지침이 구체적·실행 가능, 에러 핸들링·예시 포함
- [ ] references 링크 명확
- [ ] 명백한 태스크·의역된 요청에 트리거되고, 무관한 주제엔 안 됨

## shepherd에 스킬 추가하기 (이 프로젝트 특화)

shepherd의 스킬 frontmatter는 위 Claude 표준과 필드가 조금 다릅니다. shepherd 파서(`internal/skill.SkillFrontmatter`)가 인식하는 필드:

```
---
name: <kebab-case>
description: <무엇을+언제+트리거 문구>
tags: [tag1, tag2]         # 선택
scope: global | project    # 선택 (기본 project)
effort: low | medium | high # 선택 — 워커 노력 수준
maxTurns: 20               # 선택 — 최대 턴
disallowedTools: [Edit, Write] # 선택 — 읽기전용 등으로 제한
---
```

등록 방법 세 가지:

1. **WebUI**: Skills 탭에서 새 스킬 작성/붙여넣기.
2. **CLI import**: frontmatter가 붙은 `.md`를 임포트.
3. **내장(bundled) 스킬**: `internal/skill/bundled/<name>.md`에 파일 추가 → `SeedBundledSkills()`가 부팅 시 DB에 시드(있으면 내용 변경 시 업데이트). `scope: global`로 두면 모든 프로젝트에서 사용 가능. 새 파일을 추가한 뒤 shepherd를 재빌드해야 embed에 포함됩니다.

내장 스킬은 단일 Markdown 파일이라 `references/`·`scripts/` 3단계 구조 대신 **자기완결적 SKILL.md 하나**로 작성하세요.

## 참고

- 상세 요약: 위키 `claude-skills-building-guide`, 볼트 `Claude-Skills-빌딩-완벽가이드-요약.md`
- 원문: Anthropic, *The Complete Guide to Building Skills for Claude*
- 공개 스킬 저장소: github.com/anthropics/skills
