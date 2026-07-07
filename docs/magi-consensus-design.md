# MAGI 합의 시스템 설계 — 다중 모델 에이전트 합의 프로바이더

> **상태**: Phase 0/1 구현 완료 (2026-07-06)
> **작성일**: 2026-07-06 (작업 #7010)
> **선행 문서**: `docs/embedded-provider-design.md` (임베디드 프로바이더 — 본 설계의 기반)

---

## 1. 배경과 목표

에반게리온의 MAGI 시스템처럼, **서로 다른 인격을 가진 3개의 독립 시스템이 심의하고
표결하는 구조**를 shepherd의 로컬 LLM 실행 경로에 도입한다.

동기는 오마주만이 아니다. 실질적 근거:

- 단일 로컬 모델(20~30B급)의 품질 한계는 임베디드 프로바이더 실패 분석
  (#6944/#6945, #7000)에서 반복 확인됐다. 툴콜 견고화·가드로 *실행 안정성*은
  확보했지만 *판단 품질* 자체는 모델 한 개의 상한에 묶여 있다.
- 하드웨어가 이미 합의에 유리한 형태다: GPU 3대(RTX 3060 ×2, MI50)에 서로 다른
  모델을 물리적으로 분리 배치하면 proposer 3개의 **진짜 병렬 호출**이 된다.
- MoA 계열 연구는 "여러 중형 오픈 모델의 종합이 단일 대형 모델을 능가할 수 있다"를
  반복적으로 보여준다 (§2).

### 목표

- **`magi` 프로바이더 신설**: proposer 3개(교체 가능한 로컬 엔드포인트, 페르소나
  부여) + aggregator 1개(Claude 또는 최강 로컬 모델)의 합의 파이프라인.
- **하이브리드 심의**: 평소엔 1라운드 MoA(빠름), 합의 실패·저신뢰 케이스만 토론
  라운드로 에스컬레이션 (DOWN 방식, §2.4).
- 기존 임베디드 인프라(클라이언트, 툴 레지스트리, 가드, 핸드오프) **최대 재사용**.

### 비목표

- proposer 3개가 각자 툴을 들고 병렬로 *실행*하는 구조 (§3에서 배제 근거).
- 항상-N라운드 토론 (품질 이득 불확실 + 속도 저하, §2.5).
- 임베디드 프로바이더 대체 — magi는 임베디드 위에 얹히는 선택지다.

---

## 2. 논문 리뷰 — 설계 원칙 도출

각 논문에서 이 설계에 직접 반영한 원칙만 추린다.

### 2.1 Mixture-of-Agents (Wang et al. 2024, arXiv:2406.04692)

proposer 여럿이 독립 답변을 내고 aggregator가 종합하는 계층 구조. 오픈소스 중형
모델 조합만으로 AlpacaEval 2.0에서 GPT-4o를 능가했다. 원본은 다층(3~4 layer)
구조지만, 후속 연구(SMoA, arXiv:2411.03284)는 희소화로 비용을 줄여도 품질이
유지됨을 보였다.

→ **반영**: 기본 경로는 proposer 1층 + aggregator 1회의 **1레이어 MoA**.
층을 쌓는 대신 필요할 때만 토론으로 확장한다(§2.4). aggregator를 최강 로컬 모델로
돌리는 완전-로컬 구성도 MoA 원본이 검증한 방식이다.

### 2.2 이기종 앙상블의 다양성 원리

앙상블 일반화 오차는 `평균 편향 + 평균 분산 − 다양성`으로 분해되며, 오류
비상관성(error inconsistency)이 앙상블 이득과 선형 상관을 가진다. 같은 계열
모델을 여러 개 돌리면 오답이 상관되어 합의가 "자신감 있는 오답"으로 수렴한다
(VLM 앙상블에서의 family bias 보고: arXiv:2603.17111).

→ **반영**: proposer 3개는 **모델 계열을 다르게** 하는 것을 강력 권장
(예: Qwen / Llama / Mistral 계열). 페르소나 프롬프트는 다양성 보조 장치일 뿐,
본질적 다양성은 모델 차이에서 온다. 설정 검증 시 동일 모델 3개 구성이면 경고를
출력한다.

### 2.3 Multi-agent Debate (Du et al. 2023, arXiv:2305.14325)

여러 인스턴스가 서로의 답을 보고 수정하는 토론이 사실성·추론을 개선. 단,
후속 검증 연구들이 한계를 밝혔다:

- **동조(conformity/sycophancy)**: 다수가 틀리면 정답자가 오답으로 끌려간다.
  합의 압력이 강할수록 정확도가 떨어지고 라운드만 늘어난다
  (Can LLM Agents Really Debate?, arXiv:2511.07784; Free-MAD, arXiv:2509.11035).
- 강한 단일 모델 + 좋은 프롬프트를 항상 이기지는 못한다.

→ **반영**: ① 1라운드는 반드시 **블라인드**(서로의 답 미노출 — 독립성 확보).
② 토론은 **최대 1라운드**로 제한. ③ 토론 프롬프트에 "근거가 유지되면 답을 바꾸지
마라, 동의 자체는 목표가 아니다"를 명시. ④ 토론 시 상대 답변은 **익명화**
(arXiv:2510.07517 — 정체성 노출이 권위 편향을 유발).

### 2.4 Debate Only When Necessary — DOWN (arXiv:2504.05047)

초기 응답의 confidence가 임계값을 넘으면 토론을 생략하고, 필요한 질의에만 토론을
활성화. 품질을 유지·향상하면서 효율을 최대 6배 개선.

→ **반영**: 이 설계의 **에스컬레이션 게이트의 직접 근거**. 합의 판정(§5.3)에서
`unanimous/majority + 고신뢰`면 즉시 채택, `split 또는 저신뢰`만 토론으로 넘긴다.
"무조건 토론 3라운드" 구조를 배제하는 근거이기도 하다.

### 2.5 Self-consistency 및 다수결 (arXiv:2203.11171, arXiv:2402.05120)

정답 검증이 쉬운 문제(수학, 분류)는 단순 샘플링+다수결로도 크게 개선된다.
반면 서술형·설계형 답변은 "같은 답" 판정이 어려워 aggregator 종합이 필요하다.

→ **반영**: 다수결은 aggregator가 판정 단계에서 내부적으로 수행하되(세 답의 핵심
주장 일치 여부), 최종 산출물은 항상 aggregator의 **종합(synthesis)**이다.

### 2.6 LLM-as-a-Judge 편향 (arXiv:2410.02736 외)

judge/aggregator는 위치 편향(먼저 본 답 선호), 장황함 편향, 자기 선호 편향
(같은 계열 모델의 답을 과대평가)을 가진다. 완화책: 답변 순서 랜덤화, 모델 정체
마스킹, 길이 편향 명시적 억제.

→ **반영**: aggregator 프롬프트에서 ① 세 답변의 **제시 순서를 랜덤화**,
② 어느 엔드포인트/모델의 답인지 감추고 **페르소나명만 표기**, ③ "길이가 아니라
근거의 질로 평가하라" 지시. aggregator가 Claude면 로컬 proposer와 계열이 달라
자기 선호 편향이 구조적으로 완화된다.

---

## 3. 핵심 설계 판단: 에이전트 루프에서 무엇에 합의하는가

논문들은 대부분 **단발 Q→A** 세팅이다. shepherd의 임베디드 프로바이더는 툴콜
에이전트 루프라서 그대로 이식할 수 없다: proposer 3개가 병렬로 `write_file`/`bash`를
실행하면 같은 저장소에서 부작용이 충돌한다. worktree 3개 분리 + diff 3개 중 선택은
가능하지만 로컬 GPU 예산에서 비용이 3배이고 diff 판정이 가장 어려운 문제가 된다.

따라서 **합의 대상을 "부작용 없는 텍스트 산출물"로 한정**한다. 원작 MAGI도
스스로 행동하지 않는다 — 심의·표결하고, 실행은 다른 시스템이 한다. 같은 구조로:

| 합의 지점 | 대상 | 실행 주체 | 단계 |
|-----------|------|----------|------|
| (a) 자문형 태스크 | 응답 전체 | 없음 (텍스트 답변) | Phase 1 |
| (b) 에이전틱 태스크의 플랜 | 실행 계획 | 단일 executor (기존 `embedded.Run`) | Phase 2 |
| (c) 완료 직전 리뷰 | 결과 요약 + 변경 diff | 승인/반려 표결 (2/3) | Phase 3 |

에이전틱 실행 자체는 여전히 단일 모델이다. MAGI는 **판단 품질**(무엇을 할지,
잘 됐는지)을 올리는 장치이고, 툴콜 견고성 문제는 기존 가드가 계속 담당한다.

---

## 4. 아키텍처

```
worker.ExecuteInteractive (interactive.go)
        │  switch s.Provider
        ├── claude / opencode / pi / embedded  (기존)
        └── magi → executeWithMagi (internal/worker/magi.go, 신규)
                        │
                        ▼
                internal/magi (신규 패키지)
                ├── orchestrator.go  # 합의 파이프라인 상태기계
                ├── proposer.go      # embedded.Client 재사용, errgroup 병렬 호출
                ├── aggregator.go    # 합의 판정 + 종합 (JSON 스키마 강제)
                ├── debate.go        # 에스컬레이션 라운드
                ├── persona.go       # MELCHIOR/BALTHASAR/CASPER 기본 프롬프트
                └── types.go         # 설정·판정 wire 타입
                        │
        ┌───────────────┼──────────────────┐
        ▼               ▼                  ▼
  Round 1 병렬      Aggregator         (필요시) Executor
  3× embedded.Client  Claude CLI(-p)     embedded.Run
  (블라인드 제안)      또는 로컬 endpoint  (Phase 2: 합의 플랜 실행)
```

- **worker→magi 단방향 의존**, `SetMagiExecutor` 주입 방식은 기존
  `SetEmbeddedExecutor`(`internal/worker/embedded.go`)와 동일 — import cycle 회피.
- proposer 호출은 `embedded.Client`(chat/completions)를 그대로 쓴다. 신규 HTTP
  코드 없음.
- 프로바이더 등록: `internal/config/config.go`의 provider 스위치
  (`"claude", "opencode", "pi", "embedded"`)에 `"magi"` 추가.
- 동시성: `internal/queue/processor.go`의 `concurrency_limits`는 양 단위로
  동작하므로 magi 태스크 1개가 내부적으로 3개 엔드포인트를 병렬 호출하는 것과
  충돌하지 않는다. 단, **여러 magi 양이 같은 엔드포인트를 공유**하면 로컬 서버에서
  직렬화되므로, proposer의 `endpoint_id`는 서로 다른 물리 서버(GPU)를 가리키는
  구성을 권장하고 설정 화면에 base_url 중복 경고를 둔다.

---

## 5. 합의 파이프라인 (Phase 1: 자문형)

### 5.1 Round 1 — 블라인드 병렬 제안

- proposer 3개를 errgroup으로 동시 호출. 각자 다음을 받는다:
  - 시스템 프롬프트 = `BuildSystemPromptForEmbedded(...)` 공통 컨텍스트
    + **페르소나 블록** (§6)
  - 동일한 유저 프롬프트 (서로의 존재·답변은 노출하지 않음 — 독립성)
  - 답변 끝에 자기보고 신뢰도 요구: `CONFIDENCE: <0-10>` 한 줄
- 개별 proposer 실패(타임아웃/HTTP 에러)는 **2개 이상 성공 시 계속 진행**
  (성공 답변만으로 판정). 1개 이하 성공이면 단일 임베디드 실행으로 폴백하고
  라이브 출력에 경고.
- 타임아웃: proposer별 개별 타임아웃 (기본 120s, 엔드포인트별 오버라이드).
  가장 느린 모델이 전체를 인질로 잡지 않도록 데드라인 도달 시 완료된 답만 수집.

### 5.2 라이브 출력 UX

기존 `OnOutput` 스트림에 심의 과정을 그대로 흘린다 (마기 연출 그대로):

```
🧠 MAGI 심의 개시 — MELCHIOR·BALTHASAR·CASPER
  🔬 MELCHIOR-1 (qwen3-27b) 응답 완료 — 신뢰도 8/10
  🛡 BALTHASAR-2 (llama-3.3-70b-q3) 응답 완료 — 신뢰도 6/10
  🎭 CASPER-3 (mistral-small) 응답 완료 — 신뢰도 9/10
⚖️ 합의 판정: 2:1 분열 — 토론 라운드 진입
  (쟁점: 마이그레이션 순서 — CASPER는 롤백 우선을 주장)
✅ 합의 도달 (3:0) — 종합 응답 작성 중
```

### 5.3 Aggregator 판정

aggregator에게 세 답변을 **순서 랜덤 + 페르소나명만 표기**로 제시하고
JSON 판정을 강제한다:

```json
{
  "verdict": "unanimous | majority | split",
  "agreement_axis": "핵심 결론에서 무엇이 일치/불일치했는지 한 줄",
  "synthesis": "종합 답변 (최종 산출물)",
  "dissent": "소수의견 요약 (있을 때)",
  "confidence": 0-10
}
```

분기:

- `unanimous` 또는 `majority` **이고** `confidence >= threshold`(기본 7)
  → `synthesis` 채택, 종료. (평상시 경로 — 지연은 max(proposer) + aggregator 1회)
- `split` 이거나 저신뢰 → **토론 에스컬레이션** (§5.4)
- JSON 파싱 실패 → 재프롬프트 1회 → 그래도 실패면 세 답변을 페르소나명과 함께
  나란히 붙인 결과로 완료하되 `⚠️ MAGI 판정 실패 — 원문 병기`를 마킹.
  (incomplete 처리하지 않는다 — 답 자체는 존재하므로. #7000 오탐 교훈:
  게이트는 보수적으로.)

### 5.4 토론 라운드 (최대 1회, 설정 가능)

각 proposer에게 재요청:

- 입력: 자신의 이전 답 + **익명화된** 나머지 답변들("다른 심의자 A/B") +
  aggregator가 뽑은 쟁점(`agreement_axis`)
- 지시: "타 답변에서 네 답의 오류를 찾으면 수정하라. 근거가 유지되면 답을
  바꾸지 마라. 동의 자체는 목표가 아니다." (동조 방지, §2.3)
- 수정 답변으로 aggregator 재판정. 이번엔 무조건 종결:
  - 합의 도달 → synthesis 채택
  - 여전히 `split` → **casting vote**: `synthesis`(다수안)를 채택하되 결과
    상단에 `⚠️ MAGI 교착 (2:1)` + 소수의견을 병기해서 사용자가 최종 판단할
    재료를 남긴다. (shepherd 태스크는 비동기라 실행 중 사용자 표결을 기다릴 수
    없다 — 원작의 "사령관 결정" 대신 "결과에 반대 의견 첨부"로 기능을 옮긴다.
    추후 WebUI에 심의 상세 뷰가 생기면 대기-표결 모드를 옵션으로 검토.)

### 5.5 사용량·비용 집계

`ExecuteResult.PromptTokens/CompletionTokens`는 proposer 3 + aggregator +
토론 호출의 usage 합산. `CostUSD`는 Claude aggregator 사용 시에만 발생.
라이브 출력 말미에 "MAGI 심의 비용: N 토큰 (호출 5회)"를 남겨 관측 가능하게 한다.

---

## 6. 페르소나 설계

기본 시스템 프롬프트 블록 (persona.go에 내장, 사용자 오버라이드 가능):

- **MELCHIOR-1 (과학자)** — 기술적 정밀성. 논리 결함·엣지케이스·반례를 우선
  탐색하라. 근거 없는 주장은 하지 마라.
- **BALTHASAR-2 (어머니)** — 보수성과 안전. 리스크·부작용·되돌릴 수 없는 변경을
  우선 경계하라. 확신이 없으면 낮은 신뢰도를 보고하라.
- **CASPER-3 (여성)** — 실용성과 사용자 관점. 실제로 쓰이는 상황을 상상하고,
  더 단순한 해법이 있으면 그것을 주장하라.

주의(§2.2): 페르소나는 관점 다양성의 **보조 장치**다. 오답 비상관성의 본체는
모델 계열 차이이므로, 같은 모델 3개에 페르소나만 달리 붙인 구성은 합의 가치가
크게 떨어진다. 설정 저장 시 3개 proposer의 모델명이 동일하면 경고를 표시한다.

---

## 7. 설정 스키마 (`~/.shepherd/embedded.yaml` 확장)

기존 `endpoints` 목록을 재사용하고 `magi` 섹션을 추가한다:

```yaml
endpoints:
  - id: gpu0-qwen       # RTX 3060 #1
    base_url: http://192.168.x.a:8080
    model: qwen3-27b
    ...
  - id: gpu1-llama      # RTX 3060 #2
    ...
  - id: mi50-mistral    # MI50
    ...

magi:
  enabled: true
  proposers:
    - endpoint_id: gpu0-qwen
      persona: melchior          # melchior | balthasar | casper | custom
    - endpoint_id: gpu1-llama
      persona: balthasar
    - endpoint_id: mi50-mistral
      persona: casper
  aggregator:
    type: claude_cli             # claude_cli | endpoint
    endpoint_id: ""              # type=endpoint일 때 (완전 로컬 구성)
  escalation:
    confidence_threshold: 7      # 이 미만이면 토론
    max_debate_rounds: 1
  proposer_timeout_seconds: 300
  mode: advisory                 # advisory | plan | review (Phase 게이트, §8)
```

- `aggregator.type: claude_cli`는 `claude -p`(print 모드) 서브프로세스 1회 호출.
  네트워크 의존이 생기는 유일한 지점이며, **완전 로컬을 원하면 `endpoint`로 최강
  로컬 모델을 지정**한다 (MoA 원본 방식, §2.1). Claude CLI 실패 시 자동으로
  proposer 중 첫 엔드포인트가 aggregator를 겸직하는 폴백.
- WebUI: Settings > Embedded 탭에 "MAGI 합의" 서브섹션 — proposer 3슬롯
  (엔드포인트 드롭다운 + 페르소나 선택), aggregator 선택, 임계값 슬라이더.
  양(sheep)의 프로바이더 선택지에 `magi` 추가.

---

## 8. 단계별 로드맵

### Phase 0 — 설정·배선 (작음)
config 스키마, provider 등록, WebUI 설정 탭, `SetMagiExecutor` 배선.

### Phase 1 — 자문형 합의 (§5 전체)
툴이 필요 없는 질문/분석/리뷰 태스크에서 MoA+DOWN 파이프라인.
자문형 판별: Round 1에서 proposer들이 툴 없이 답했으므로 별도 판별이 필요 없다 —
**magi 모드 `advisory`에서는 태스크 전체를 무툴 심의로 처리**한다.
(#7000에서 본 것처럼 자문형 태스크는 실제로 존재하는 주요 부류다.)

### Phase 1.5 — 읽기 전용 도구 주입
Phase 1과 Phase 2 사이의 중간 단계. Phase 1의 무툴 심의는 "코드를 직접 확인해야
하는 문제"에서 추측을 결론으로 내는 한계가 있었다 (#7031/#7033 교훈). Phase 1.5는
proposer에게 **읽기 전용 도구**를 주입하여 이 한계를 극복한다:

- **도구 셋**: `read_file`, `grep`, `glob` (네이티브), `get_history`, `get_task_detail`,
  `get_status`, `skill_load`, `wiki_*` (shepherd MCP), 외부 MCP 서버의 읽기 전용 메서드
  (`list_`, `get_`, `read_`, `status` 등 이름 기반 휴리스틱으로 분류)
- **제외 도구**: `write_file`, `edit_file`, `bash` (파일 시스템 변경), `task_start`,
  `task_complete`, `task_error` (태스크 상태 변이), 브라우저 도구 (Chrome 프로필 충돌),
  모든 `_add_`, `_delete_`, `_update_`, `_start`, `_stop` 패턴의 외부 MCP 도구
- **핵심 설계**: 모든 proposer가 동일한 읽기 전용 툴 셋을 공유 (per-proposer가 아님).
  각 proposer는 독립적인 `ToolRegistry`를 생성하여 파일을 병렬로 읽을 수 있음.
  `callEndpoint`가 미니 에이전트 루프로 변경되어 최대 10회 도구 호출 후 최종 답변 도출.
- **게이트 조정**: `minSubstantiveRunes`를 120에서 60으로 낮춤 — 도구 사용 후 간결한
  답변이 임계값에 걸려 실패하는 것을 방지.
- **쓰기 도구 제외 이유**: 3개 모델이 동일한 파일 시스템/클러스터에 동시 쓰기를
  시도하면 충돌이 발생함. Phase 2의 단일 executor 패턴에서만 쓰기를 허용.

### Phase 2 — 플랜 합의 + 단일 executor
에이전틱 태스크: ① orchestrator가 공유 컨텍스트 팩(파일 트리, 관련 파일 발췌)을
만들어 3 proposer에 동일 제공 → ② "실행 계획"을 §5 파이프라인으로 합의 →
③ 합의 플랜을 시스템 프롬프트에 주입해 단일 executor(`embedded.Run`, 지정
엔드포인트)가 실행. 컨텍스트 팩은 read-only 툴을 각자 돌리는 것보다 저렴하고
"동일 입력" 조건(§2.1)도 지킨다.

### Phase 3 — 완료 리뷰 게이트
executor 완료 후 결과 요약 + `git diff` 요약을 3 심의자에게 표결
(승인/반려 + 사유). 2/3 승인 시 완료, 반려 시 반려 사유를 주입해 1회 재작업.
기존 빌드 검증 게이트(#6290, #7000 교훈)와 상보적 — 빌드 게이트는 기계적 검증,
리뷰 게이트는 의미적 검증.

### 측정
과거 실패·저품질 태스크(#6944/#6945/#7000 부류)를 재현 프롬프트 세트로 만들어
단일 임베디드 vs MAGI advisory를 A/B. 지연·토큰 배수도 함께 기록.

---

## 9. 트레이드오프와 리스크

| 항목 | 내용 | 대응 |
|------|------|------|
| 지연 | 병렬이면 평상시 ~1.5–2배 (max(proposer)+aggregator), 토론 시 3–4배 | DOWN 게이트로 토론을 예외 경로화 |
| 토큰 비용 | 호출 4~8회/태스크 | 로컬이라 화폐 비용은 0, Claude aggregator만 유료 |
| 네트워크 의존 | Claude aggregator 사용 시 | `endpoint` 타입으로 완전 로컬 구성 지원 + CLI 실패 시 로컬 폴백 |
| 동조 수렴 | 토론이 정답을 오답으로 뒤집을 수 있음 | 블라인드 1라운드, 토론 1회 제한, 익명화, "동의는 목표가 아니다" 지시 |
| 오답 상관 | 같은 계열 모델 3개면 합의 가치 급락 | 모델 계열 분리 권장 + 설정 시 경고 |
| aggregator 편향 | 위치·장황함·자기선호 | 순서 랜덤화, 정체 마스킹, 평가 기준 명시 |
| VRAM 제약 | 3060 12GB ×2, MI50 32GB | proposer는 12GB에 들어가는 중형 모델 위주, 최강 모델(MI50)을 aggregator 겸직 후보로 |
| 판정 JSON 깨짐 | 로컬 aggregator의 스키마 불복종 | 재프롬프트 1회 → 원문 병기 폴백 (incomplete 아님) |

---

## 10. 참고 문헌

- Wang et al., *Mixture-of-Agents Enhances Large Language Model Capabilities*, arXiv:2406.04692 (2024)
- Li et al., *SMoA: Sparse Mixture-of-Agents*, arXiv:2411.03284 (2024)
- Du et al., *Improving Factuality and Reasoning in LMs through Multiagent Debate*, arXiv:2305.14325 (2023)
- Eo et al., *Debate Only When Necessary (DOWN)*, arXiv:2504.05047 (2025)
- *Can LLM Agents Really Debate? A Controlled Study of MAD in Logical Reasoning*, arXiv:2511.07784 (2025)
- *Free-MAD: Consensus-Free Multi-Agent Debate*, arXiv:2509.11035 (2025)
- *When Identity Skews Debate: Anonymization for Bias-Reduced Multi-Agent Reasoning*, arXiv:2510.07517 (2025)
- Wang et al., *Self-Consistency Improves Chain of Thought Reasoning*, arXiv:2203.11171 (2022)
- Li et al., *More Agents Is All You Need*, arXiv:2402.05120 (2024)
- *Justice or Prejudice? Quantifying Biases in LLM-as-a-Judge*, arXiv:2410.02736 (2024)
- *Hidden Clones: Exposing and Fixing Family Bias in VLM Ensembles*, arXiv:2603.17111 (2026)
