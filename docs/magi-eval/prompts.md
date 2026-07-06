# MAGI A/B 측정 — 재현 프롬프트 세트

> 과거 실패·저품질 태스크에서 발췌한 자문형 프롬프트.
> Phase 1 MAGI는 도구 없이 자문형으로 동작하므로, 에이전틱 실패 케이스를 자문형으로 변형해 수록한다.

---

## P1: 접근성 서비스 선언 자문 (#7000 계열)

**원본 태스크**: #7000 (compass-app, failed)
**원본 요청**: "플레이 콘솔에 접근성 서비스 관련 작성해야해 다음 확인해서 textarea 나 input 이나 선택해야하는걸 알려줘."

**변형 프롬프트 (자문형)**:
> 안드로이드 앱에서 AccessibilityService를 사용 중인데 `isAccessibilityTool="true"`로 설정되어 있습니다. 이 앱은 자녀 보호(parental control) 앱입니다. Google Play Console의 접근성 도구 선언 양식에서 "핵심 목적" textarea에 무엇을 적어야 할지, 그리고 `isAccessibilityTool` 속성을 false로 바꾸는 것이 나은지 true로 유지하고 양식을 작성하는 것이 나은지 판단해주세요.

**채점 기준**:
- `isAccessibilityTool="false"` 변경을 권장하는지 (정답: false 권장)
- 자녀 보호 앱이 접근성 도구가 아님을 명시하는지
- Play 정책 위반 리스크를 언급하는지
- 구체적인 코드 수정 위치를 제시하는지

---

## P2: 키보드 IME 창 플래그 디버깅 자문 (#6944/#6945 계열)

**원본 태스크**: #6944, #6945 (ds-keyboard, failed — 추론 루프)
**원본 요청**: "키보드가 나왔다가 사라진다. 브라우저 주소창을 탭하면 제대로 나오는데 input을 탭하니 나오다 사라진다."

**변형 프롬프트 (자문형)**:
> Android IME(InputMethodService)에서 `FLAG_NOT_TOUCH_MODAL`과 `FLAG_WATCH_OUTSIDE_TOUCH`를 설정했더니, 일반 input 필드를 탭할 때 키보드가 나타났다가 즉시 사라지는 문제가 발생합니다. 브라우저 주소창에서는 정상 작동합니다. 원인과 해결 방안을 설명해주세요.

**채점 기준**:
- `FLAG_NOT_TOUCH_MODAL`이 외부 터치를 통과시켜 input 포커스를 잃게 만드는 메커니즘을 설명하는지
- `onFinishInputView()` 호출 시점과 원인을 짚는지
- 브라우저 주소창이 다르게 동작하는 이유를 언급하는지
- 플래그 제거 또는 조정이라는 구체적 해결책을 제시하는지

---

## P3: 임베디드 빌드 검증 게이트 오탐 (#7000 후속)

**원본 태스크**: #7000 결과에서 파생 (embedded 빌드 검증 게이트 false positive)
**원본 맥락**: embedded 프로바이더의 build-verification gate가 advisory answer를 실패로 오판

**변형 프롬프트 (자문형)**:
> shepherd의 embedded 프로바이더 실행 루프에서 모델이 "advisory" 답변(코드 수정 없이 조언만 제공)을 내놓았을 때, build-verification 게이트가 이를 false positive로 실패 처리하는 문제가 있습니다. advisory 응답과 실제 코드 수정 시도를 구분하는 기준을 설계해주세요.

**채점 기준**:
- advisory 응답의 특징(파일 수정 없음, 조언성 텍스트)을 식별하는지
- build-verification 게이트가 트리거되는 조건을 정확히 설명하는지
- false positive 방지를 위한 분기 로직(예: FilesModified가 비어 있으면 게이트 스킵)을 제안하는지
- 기존 `IncompleteReason` 필드의 활용 방안을 언급하는지

---

## P4: 일반 코드 리뷰 자문

**목적**: 단순 기술 질문에서 MAGI와 단일 모델의 품질 차이를 측정하기 위한 베이스라인

**프롬프트**:
> Go에서 `context.Context`를 함수 시그니처에 넘기는 관례에 대해 설명하고, 다음 중 어떤 것이 관례에 어긋나는지 판단해주세요:
> 1. `func DoSomething(ctx context.Context, name string) error`
> 2. `func DoSomething(name string, ctx context.Context) error`
> 3. `func DoSomething(ctx context.Context) (string, error)`
> 4. `type Service struct { ctx context.Context }`

**채점 기준**:
- ctx가 첫 번째 매개변수여야 한다는 관례를 명시하는지
- #2가 위반이라고 지적하는지
- struct에 ctx를 저장하는 안티패턴(#4)을 경고하는지
- context 패키지의 공식 가이드라인을 인용하는지

---

## P5: 에러 처리 아키텍처 자문

**목적**: 설계 수준의 질문에서 합의 시스템이 더 균형 잡힌 답을 내는지 확인

**프롬프트**:
> 마이크로서비스에서 분산 트랜잭션 대신 eventual consistency를 선택할 때, 보상 트랜잭션(compensating transaction) 패턴과 saga 패턴의 차이를 설명하고, 각각 어떤 상황에 적합한지 예를 들어주세요.

**채점 기준**:
- 보상 트랜잭션과 saga의 정확한 정의 차이를 제시하는지
- 각 패턴의 적용 시나리오 예시를 드는지
- 장단점을 균형 있게 비교하는지
- 실무 적용 시 주의점을 언급하는지
