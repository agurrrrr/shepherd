# /state - 이슈 상태 변경

이슈의 상태를 변경합니다.

## 사용법
```
/state <이슈ID> <상태>
```

## 상태 값
- Open: 열림
- 개발 / Develop: 개발 중
- Review: 리뷰
- 테스트 / Test: 테스트
- Done: 완료

## 예시
- `/state BORI-123 개발` - BORI-123을 개발 상태로 변경
- `/state BORI-123 테스트` - BORI-123을 테스트 상태로 변경
- `/state BORI-123 Done` - BORI-123을 완료 상태로 변경

## 실행
mcp__atsel-mcp__change_state 도구를 사용하여 이슈 상태를 변경하세요.
- issue_id: 이슈 ID (필수)
- state: 상태 값 (필수)
