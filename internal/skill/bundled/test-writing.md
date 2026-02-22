---
name: test-writing
description: 테스트 작성 시 참고하는 가이드라인
tags: [testing, quality]
scope: global
---
# Test Writing

테스트 작성 시 다음 가이드라인을 따르세요:

## 테스트 구조
- 테스트 이름은 `Test_기능_시나리오_기대결과` 형식으로 작성
- Arrange-Act-Assert (AAA) 패턴 사용
- 테이블 기반 테스트(table-driven tests)로 여러 케이스를 깔끔하게 정리

## 커버리지
- 정상 경로 (happy path) 반드시 포함
- 에러 케이스와 경계값 테스트
- 빈 입력, nil, 최대값 등 엣지 케이스

## 원칙
- 각 테스트는 독립적으로 실행 가능해야 함
- 외부 의존성(DB, API)은 모킹 처리
- 테스트는 결정적(deterministic)이어야 함 — 랜덤이나 시간 의존 금지
- 의미 있는 assertion 메시지 포함

## Go 테스트 패턴
```go
func TestFoo(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {"valid input", "hello", "HELLO", false},
        {"empty input", "", "", true},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := Foo(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("Foo() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if got != tt.want {
                t.Errorf("Foo() = %v, want %v", got, tt.want)
            }
        })
    }
}
```
