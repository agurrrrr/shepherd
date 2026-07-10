# MAGI 안정화(Hardening) 계획 — 느린 모델 내성 확보

> 근거 분석: 작업 #7205 (2026-07-10), 위키 `magi-convergence-reserve-starvation`
> 발생 사례: #7178 (1라운드 수렴 타임아웃), #7182 (토론 라운드 타임아웃 → 기권 → 2:1 교착)
> 작업 지시서: `docs/magi-hardening-tasks/` (step-01 ~ step-12)

## 목표

**느린 로컬 모델이 심의에 섞여 있어도 심의가 깨지지 않는다.**

- 슬롯이 느려서 시간을 초과해도: 이미 생성된 부분 응답을 살리고(salvage), 실패하더라도 원인이 진단 가능한 메시지로 남는다.
- 느린 슬롯에는 더 긴 시간을 줄 수 있다 (슬롯별 타임아웃).
- 결론 없는 답변(내레이션·신뢰도 미보고)이 판정을 오염시키지 않는다 (nudge + 기권 재기회).
- 토론 라운드가 1라운드보다 무거워지지 않는다 (경량화).
- 비용·시간·단계 정보가 정확히 집계된다 (관측성).

## #7205 제안 → 단계 매핑

| # | 제안 | 단계 |
|---|------|------|
| 7 | 비용 집계 수정 (실패 경로 Usage, 핸드오프 요약 usage) | step-01 |
| 1(후반) | 실패 메시지에 단계·크기 태그 + 심의 텔레메트리 | step-02 |
| 5 | unanimous 특례 (threshold-1 허용) | step-03 |
| 6 | 반복 도구 호출 감지 | step-04 |
| 1(전반) | 수렴 타임아웃 시 부분 응답 salvage | step-05 |
| 2 | 수렴 직전 컨텍스트 다이어트 (절대 크기 기준) | step-06 |
| 운영-1 | **슬롯별 타임아웃 override** (백엔드) | step-07 |
| 운영-1 | 슬롯별 타임아웃 WebUI | step-08 |
| 3 | CONFIDENCE 누락 → nudge 1회 (재질문 헬퍼 포함) | step-09 |
| 운영-2 | **기권 슬롯 재기회** (판정자 기권 → tools-off 재질문 → 재판정) | step-10 |
| 4 | 토론 라운드 경량화 (tools 제거, peer cap 6K, 기권 제외) | step-11 |
| — | 통합 검증 | step-12 |

## 핵심 설계 결정

### 1. 슬롯별 타임아웃 (step-07/08)
- `MagiProposer`에 `timeout_seconds` 필드 추가 (yaml/json `timeout_seconds`). **0 = 전역값 상속.**
- `magi.ProposerSpec`에 `Timeout time.Duration` 추가. `RunProposers`가 슬롯별로 `spec.Timeout > 0`이면 그 값을, 아니면 `opts.Timeout`(전역)을 쓴다.
- 토론 라운드는 round-1의 Spec을 그대로 재사용하므로 자동으로 같은 override가 적용된다.
- 검증: 음수는 하드 에러, 1~29초는 소프트 경고("too short").
- 목적: 로컬 27B 같은 느린 모델 슬롯에만 900초를 주고, 빠른 원격 슬롯은 300초 유지 — 전체 심의가 최악 슬롯 하나에 지배되지 않게 한다.

### 2. 재질문(reask) 헬퍼 통합 (step-09)
CONFIDENCE nudge와 기권 재기회는 같은 기계 장치를 공유한다: **tools-off 단발 재질문**.
- `reaskProposer` (package var, 테스트 fake 가능): provider별 분기(embedded 단발 chatTurn / claude·opencode·grok CLI 단발 호출). 프롬프트 = 원 태스크(4K cap) + 자기 이전 답변(12K cap) + 지시문.
- 재질문 예산: `clamp(유효 슬롯 타임아웃/3, 30s, 120s)` — 재질문 프롬프트는 작으므로 짧아도 충분.
- **실패해도 절대 기존 답변을 버리지 않는다** (보수적 게이트 원칙, #7000 교훈). 재질문 결과가 게이트 통과 + CONFIDENCE 보고일 때만 교체.
- salvage된 부분 응답(`[부분 응답 …]` 마커 포함)은 nudge 대상에서 제외 — 방금 시간 초과한 엔드포인트에 재질문해봐야 또 초과한다.

### 3. 기권 슬롯 재기회 (step-10)
- 판정 JSON 스키마에 `"abstained": [심의자 이름]` 필드 추가 (`Verdict.Abstained []string`). 판정자가 기권 처리한 슬롯을 명시하게 한다 — 지금까지는 dissent 산문에 묻혀 있어 프로그램적으로 알 수 없었다.
- 1차 판정이 `isAcceptable` 실패 **그리고** abstained가 비어있지 않으면: 각 기권 슬롯에 재질문 1회 → 답변이 갱신된 슬롯이 있으면 **재판정 1회** → 재판정이 acceptable이면 토론 없이 종결.
- 재판정 실패(백엔드 에러/파싱 불능) 시 1차 verdict 유지 (보수적).
- 파이프라인당 1회만. #7182형 "유효표 부족 2:1 교착"의 다수를 3표 판정으로 되돌리는 게 목적.

### 4. 수렴 salvage + 진단 태그 (step-02/05)
- `forceFinalAnswer`가 onToken 스트림을 버퍼에 복사해 두고, 수렴이 DeadlineExceeded로 죽으면 버퍼의 부분 프로즈가 게이트를 통과하는 경우 `\n\n[부분 응답 — 수렴 시간 초과로 중단됨]` 마커를 붙여 **성공으로 채택**한다 (CONFIDENCE 없음 → 판정자에게 신뢰도 미보고로 전달, 마커로 부분 응답임을 알림).
- salvage 불가 시 raw `scan SSE: context deadline exceeded` 대신 `convergence stage timed out (reserve 150s, est prompt 41000 tokens): …` 형태의 단계 태그 에러로 교체.
- 탐색 단계 전송 에러도 `exploration failed (convergence could not salvage): …`로 태그.

### 5. 수렴 직전 컨텍스트 다이어트 (step-06)
- 기존 핸드오프는 컨텍스트 윈도우 **%** 기준(65/85%)만 봤다. 128K 윈도우의 40K 토큰은 31%라 발동 안 하지만, 로컬 27B에게 40K prompt 재평가는 reserve 150초를 초과한다 — **처리량 문제는 절대 크기의 함수**다.
- 강제 수렴 진입 직전, 추정 토큰이 `convergenceDietTokens`(20,000) 이상이면 요약 핸드오프를 1회 먼저 실행하고 요약본으로 수렴한다. 다이어트 예산은 `clamp(reserve/3, 10s, 45s)`의 독립(detached) 컨텍스트 — 실패하면 원본 메시지로 그냥 수렴 (기존 실패 시맨틱과 동일).
- reserve를 프롬프트 크기에 비례해 **늘리는 방안은 채택하지 않음** — 프롬프트를 줄이는 쪽이 근본적 (#7205 결론).

### 6. 토론 라운드 경량화 (step-11)
- 토론에서 **tools 제거** (orchestrator가 debate opts에 ToolDefs/ToolDispatch를 전달하지 않음). 설계 지시문 자체가 "남의 답을 보고 수정하라"이지 재조사가 아니다. CLI 프로바이더는 자체 도구를 막을 수 없으므로 토론 지시문에 "재조사 금지" 문구를 추가해 완화.
- 동료 답변 cap 12,000 → 6,000 룬 (자기 답변 cap은 12,000 유지).
- 재기회 후에도 기권 상태인 슬롯은 토론에서 제외: `RunProposersOptions.Skip []bool` + `errSlotSkipped` 센티널로 슬롯 정렬을 유지한 채 스킵하고, 동료 프롬프트에서도 해당 답변을 뺀다. 기권 제외 후 유효 심의자가 2명 미만이면 토론 자체를 생략하고 교착 처리로 종결 (토론해봐야 악화만 됨 — #7182 교훈).

### 7. unanimous 특례 (step-03)
- `isAcceptable`: verdict가 `unanimous`면 `threshold-1`까지 허용, `majority`는 기존대로 `threshold`. #7182는 unanimous 8/10(threshold 9)이 토론으로 끌려가 232K 토큰을 쓰고 교착으로 악화됐다.

### 8. 반복 도구 호출 감지 (step-04)
- 탐색 루프에서 `(tool name, raw args)` 시그니처 카운트. 동일 시그니처 3번째 호출부터 실행하지 않고 "동일 호출 반복 — 다른 접근을 하거나 결론을 내려라" tool 결과를 주입. (#7178: 같은 파일을 같은 offset 실수로 4회+ 리딩하며 컨텍스트 증식.)

### 9. 관측성 (step-01/02)
- `RunProposers` 실패 경로(전송 에러·게이트 실패 모두)에 `result.Usage = usage` 설정 — 실패 슬롯의 토큰 유실 수정.
- `inPlaceContextRefresh`가 usage를 반환하고 호출부가 집계.
- 슬롯 완료/실패 라인에 소요 시간 추가: `응답 완료 — 신뢰도 8/10 (74초)`.
- 단계별 라인: `[MAGI:*] ⏱️ 1라운드 118초 — 성공 3/3`, `⏱️ 판정 12초`, `⏱️ 토론 라운드 95초`, `⏱️ 재판정 9초`.
- 수렴 진입 시 슬롯 패널에 `[수렴 단계 진입 — 탐색 N턴, 추정 컨텍스트 M tokens, reserve S]` 출력.

## 하지 않는 것 (명시적 제외)

- reserve를 프롬프트 크기 비례로 확대 — 다이어트로 대체 (§5).
- 다중 라운드 토론 — Phase 1 클램프 유지.
- 판정자(aggregator) 경로의 CONFIDENCE 요구 — 판정자는 JSON을 내므로 대상 아님 (reask는 proposer 전용 경로에만 배선).
- WebUI MagiStreamPanel 변경 — 새 라인들은 모두 기존 `[MAGI:n]`/`[MAGI:*]` 프리픽스 규약을 따르므로 프론트 수정 불필요.

## 단계 순서와 의존 (모두 같은 파일들을 수정하므로 **반드시 순서대로**)

| 단계 | 내용 | 주요 파일 | 선행 |
|------|------|-----------|------|
| 01 | 비용 집계 수정 | proposer.go | — |
| 02 | 단계 태그 + 텔레메트리 | proposer.go, orchestrator.go | 01 |
| 03 | unanimous 특례 | orchestrator.go | 02 |
| 04 | 반복 도구 호출 감지 | proposer.go | 02 |
| 05 | 수렴 salvage | proposer.go | 02 |
| 06 | 수렴 직전 다이어트 | proposer.go | 01, 05 |
| 07 | 슬롯별 타임아웃 백엔드 | config/magi.go, magi/types.go, proposer.go, server.go | 06 |
| 08 | 슬롯별 타임아웃 WebUI | MagiSettings.svelte | 07 |
| 09 | reask 헬퍼 + CONFIDENCE nudge | proposer.go | 05, 07 |
| 10 | 기권 슬롯 재기회 | types.go, aggregator.go, orchestrator.go | 09 |
| 11 | 토론 경량화 | debate.go, proposer.go, orchestrator.go | 10 |
| 12 | 통합 검증 | — | 전부 |

## 리스크와 완화

- **package var 시그니처 변경** (`chatTurn`/`callEndpoint`/`formatProposerLine`/`BuildDebatePrompt` 등): 테스트 fake가 많다 — 각 단계 지시서에 테스트 갱신을 명시. 시그니처를 바꾸는 단계는 반드시 `go test ./internal/magi/` 전체 통과 후 완료.
- **WithoutCancel 확장**: salvage·다이어트·재질문이 사용자 취소를 최대 45~120초 지연시킬 수 있다 — 기존 수렴 reserve와 동일한 의도적 트레이드오프 (완결성 > 즉시 취소). 각 detached 구간에 상한이 있어 무한 지연은 없다.
- **판정자가 abstained 필드를 안 채우는 경우**: 필드는 optional — 비면 재기회가 발동하지 않을 뿐 기존 동작과 동일 (하위 호환).
- **비용 증가**: 재질문·재판정은 조건부(판정 실패 + 기권 존재)에만 발동하고 각 1회 상한. 토론 경량화(tools 제거, cap 축소)가 상쇄한다.

## 투입 방법

작업 하나에 한 단계씩, 순서대로:

> `docs/magi-hardening-tasks/step-01-usage-accounting.md` 를 읽고 그대로 수행해. 공통 규칙은 같은 폴더 README.md.

## 구현 상태

| 단계 | 커밋 | 요약 |
|------|------|------|
| 01 | `199e8b0` | 실패 경로 토큰 집계 수정 |
| 02 | `1c97dfa` | 단계 태그 실패 메시지 + 소요시간 텔레메트리 |
| 03 | `2d2b49d` | unanimous는 threshold-1 허용 |
| 04 | `24437cc` | 동일 도구 호출 반복 감지 |
| 05 | `afa5013` | 수렴 타임아웃 시 부분 응답 salvage |
| 06 | `f8cc1d5` | 수렴 직전 절대 크기 기준 컨텍스트 다이어트 |
| 07 | `5612a52` | 슬롯별 타임아웃 (config + 백엔드 배선) |
| 08 | `be380b8` | 슬롯별 타임아웃 WebUI 입력 |
| 09 | `7eae7ae` | reask 헬퍼 + CONFIDENCE 누락 nudge |
| 10 | `4be360c` | 기권 슬롯 재기회 + 재판정 |
| 11 | `a2379f4` | 토론 라운드 경량화 (tools 제거·cap 축소·기권 제외) |
| 12 | (본 문서 갱신) | 통합 검증 체크리스트 |
