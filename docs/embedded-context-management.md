# Embedded Provider — 컨텍스트 관리 및 핸드오프 설계

작성일: 2026-06-11

임베디드 프로바이더(`internal/embedded`)가 컨텍스트 윈도우 한계를 어떻게 다루는지,
그리고 5978/5981 작업에서 발견된 "thinking(💭) 무한 반복" 버그의 원인과 수정 내용을 정리한다.

## 배경: 5978/5981 버그

컨텍스트 초과 후 모델이 망가져서 매 턴 깨진 reasoning_content만 뱉는 상황에서,
빈-응답 루프 감지가 발동하지 않아 max iterations까지 💭 출력이 무한 반복됐다.

원인은 두 겹:

1. **토큰 추정이 한글에서 크게 어긋남** — `바이트수/4` 휴리스틱은 영어 기준.
   한글은 글자당 3바이트인데 토큰은 글자당 약 1개라, 실제 토큰의 절반 이하로
   추정되어 트리밍이 제때 발동하지 않았고 실제 컨텍스트를 초과한 요청이 나갔다.
2. **llama.cpp는 초과 시 에러 대신 조용히 컨텍스트를 잘라냄** (context shift) —
   시스템 프롬프트/대화 앞부분이 날아간 채 생성이 계속돼 퇴행 출력(깨진 reasoning 반복)이
   발생. shepherd 입장에선 "정상 응답"이라 사후 감지가 발동하지 않았다.
   여기에 빈-응답 카운터가 reasoning-only 턴마다 0으로 리셋되는 회귀(커밋 2be50a7)가
   겹쳐 루프가 영원히 끝나지 않았다.

## 현재 처리 구조 (3겹 방어)

### 1. 예방: 요청 전 메시지 트리밍 (`loop.go` → `types.go trimMessages`)

매 요청 직전 전체 히스토리 토큰을 추정해 엔드포인트 `ContextTokens`(기본 32768)의
75%를 넘으면, **시스템 프롬프트와 최초 사용자 요청은 보존**하고 가장 오래된 턴
(assistant 메시지 + 딸린 tool 결과)부터 통째로 제거한다.

**토큰 추정 (`estimateTextTokens`)**: 룬 단위 계산으로 수정됨.
- ASCII: 4글자당 1토큰
- 비ASCII(한글·CJK): 글자당 1토큰
- 이미지 base64 data URL: `len(dataURL)/4` — 로컬 LLM 서버(llama.cpp, vLLM)는 data URL 전체를 일반 텍스트로 토큰화하므로, 실제 payload 길이에 비례하여 추정 (task #6698 수정)

### 2. 예방: 개별 크기 제한

- tool 결과는 히스토리 저장 시 8,000자로 절단 (`truncateToolResult`)
- 응답 `MaxTokens`는 `ContextTokens/4`

### 3. 사후: 초과/퇴행이 실제 발생했을 때

- 서버 HTTP 에러 → `"API error: ..."` incomplete 처리
- `finish_reason: "length"` + 빈 content → `"response truncated"` incomplete 처리
- **빈-응답 루프 감지 (수정됨)**: content가 빈 턴은 항상 카운터 증가.
  reasoning_content가 있거나 `finish_reason: "stop"`인 턴은 한도를 3→6으로
  완화하되 **리셋하지 않는다**. 퇴행 모델이 reasoning-only 턴을 무한 반복해도
  6턴에서 끊고 incomplete 처리.

## 컨텍스트 핸드오프 (신규)

트리밍은 컨텍스트를 파괴해 모델 품질을 떨어뜨린다. 그래서 **실제로 턴이 잘려나가야
하는 시점**에, **해당 sheep의 큐에 대기 작업이 없으면** 트리밍 대신 핸드오프한다:

1. 모델에게 도구 없이 마지막 요청: "지금까지 한 일 요약 + `===NEXT_TASK===` 마커
   아래 남은 작업 프롬프트 작성" (후속 작업은 이 대화를 볼 수 없으므로 파일 경로·
   결정사항·주의점을 모두 포함하도록 지시)
2. 요약을 결과로 현재 작업을 **정상 완료** 처리
3. `NEXT_TASK` 섹션이 있으면 `queue.CreateTask`로 후속 작업을 큐에 추가
   (라이브 출력: "📋 남은 작업을 후속 작업으로 큐에 추가했습니다")

**폴백**: 핸드오프 요청 실패/빈 요약 → 기존 트리밍으로 계속 진행.
큐 추가 실패 → 후속 프롬프트를 결과 텍스트에 포함시켜 유실 방지.

**조건**: 큐에 이미 대기 작업이 있으면 핸드오프하지 않고 트리밍한다
(후속 작업이 대기 작업들과 순서가 꼬이는 것을 방지).

## 구현 위치

| 파일 | 내용 |
|------|------|
| `internal/embedded/types.go` | `estimateTextTokens`, `trimMessages`, `ExecuteOptions.ShouldHandoff/EnqueueFollowUp` |
| `internal/embedded/loop.go` | 트리밍/핸드오프 분기, `attemptHandoff`, 빈-응답 루프 감지 |
| `internal/server/server.go` | `initEmbeddedExecutor`에서 콜백 와이어링 (`worker.Get` + `queue.CountPendingTasksBySheep` / `queue.CreateTask`) |

콜백 구조인 이유: `embedded` 패키지가 `queue`를 직접 import하면 사이클이 생기므로
(`mcp → queue → worker → mcp`), 큐 접근은 server 레이어에서 주입한다.
