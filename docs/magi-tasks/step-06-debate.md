# Step 06 — 토론 에스컬레이션 라운드

> 설계서 참조: §5.4 (토론 라운드), §2.3 (동조 방지 원칙)
> 선행 단계: step-04, step-05

## 목표

합의 실패(split) 또는 저신뢰 시 1회 실행되는 토론 라운드 `debate.go`를 만든다. 핵심 원칙: **익명화, 최대 1라운드, 동의를 목표로 삼지 않기**.

## 생성할 파일 (이 외 파일 금지)

- `internal/magi/debate.go`
- `internal/magi/debate_test.go`

## 작업 내용

### 1. 토론 프롬프트

```go
// BuildDebatePrompt renders the debate-round user prompt for slot i:
// the proposer's own previous answer plus the other answers anonymized as
// 심의자 A/B (design §2.3 ④ — identity exposure causes authority bias).
func BuildDebatePrompt(taskPrompt string, results []ProposerResult, slot int, agreementAxis string) string
```

구성 (한국어):
1. 원 태스크 프롬프트 (`capText` 4000자)
2. "너의 이전 답변:" + 자신의 답 (`results[slot].Answer`, `capText` 12000자)
3. "다른 심의자들의 답변:" — slot 외 결과를 **심의자 A / 심의자 B**로만 표기 (페르소나명도 노출 금지 — 자기 페르소나에 대한 권위 서열이 생길 수 있다), 각 `capText` 12000자
4. 판정자가 뽑은 쟁점: `agreementAxis` (빈 값이면 이 항목 생략)
5. 지시 (설계서 §5.4 원문 그대로):
   ```
   다른 답변에서 너의 답의 실제 오류를 발견하면 수정하라.
   근거가 유지되면 답을 바꾸지 마라. 동의 자체는 목표가 아니다.
   수정 여부와 무관하게 완결된 최종 답변을 다시 작성하고,
   마지막 줄에 "CONFIDENCE: <0-10 정수>"를 추가하라.
   ```

### 2. 토론 라운드 실행

```go
// RunDebateRound re-runs all successful proposers with debate prompts.
// A proposer that fails in the debate round keeps its round-1 answer
// (losing a member mid-debate must not shrink the deliberation).
func RunDebateRound(ctx context.Context, opts RunProposersOptions, round1 []ProposerResult, agreementAxis string, taskPrompt string) []ProposerResult
```

구현 지침:
- `round1`은 **성공한 결과만** 담겨 들어온다고 가정하라 (orchestrator가 `SuccessfulResults` 적용 후 호출).
- 슬롯별 user prompt를 `BuildDebatePrompt`로 만들어 `opts.UserPrompts`에 채우고 step-04의 `RunProposers`를 재사용한다. **병렬 호출 코드를 다시 짜지 마라.**
- 반환 결과에서 `Err != nil`인 슬롯은 해당 슬롯의 round-1 결과로 대체하고, 라이브 출력에 `  ⚠️ <표시명> 토론 라운드 실패 — 1라운드 답변 유지\n`.
- 라이브 출력 시작 시: `⚖️ 합의 판정: <판정문> — 토론 라운드 진입\n` 출력은 orchestrator 책임이므로 여기서 하지 마라. 이 함수는 개별 proposer 완료 출력(RunProposers 내장)만 낸다.

### 3. 교착 처리 헬퍼

```go
// DeadlockResult renders the final output when the debate round still ends
// split: casting vote — adopt the majority synthesis but attach the dissent
// so the user gets the material for a final call (design §5.4).
func DeadlockResult(v *Verdict) string
```

형식:
```
⚠️ MAGI 교착 (2:1) — 다수안을 채택하되 소수의견을 병기합니다.

<synthesis>

---
📎 소수의견: <dissent>
```
`dissent`가 비어 있으면 소수의견 블록 생략하고 교착 헤더 + synthesis만.

### 4. 테스트 (`debate_test.go`)

1. `BuildDebatePrompt`: 자신의 답 포함 + 타 답변은 "심의자 A/B"로만 표기(페르소나명·모델명 미포함) + 동조 방지 문구 포함
2. `RunDebateRound`: fake `callEndpoint`로 — 전원 성공 시 답변 교체 확인
3. `RunDebateRound`: 한 슬롯 실패 시 round-1 답 유지 확인
4. `DeadlockResult`: dissent 유/무 두 형식

## 하지 말 것

- 토론을 2라운드 이상 돌게 만들지 마라 — 라운드 수 제어는 orchestrator가 하지만, 이 파일의 API도 "1회 실행" 단위로만 설계하라 (설계서 §2.3 ②).
- 토론 프롬프트에 "합의에 도달하라"류 문구 금지 — 동조 압력이 정확도를 낮춘다 (설계서 §2.3).

## 완료 검증

```bash
go build ./...
go test ./internal/magi/
```
커밋 메시지: `feat(magi): debate escalation round with anonymized peers (magi-tasks step-06)`
