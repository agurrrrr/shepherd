# /done - 이슈 완료 처리

이슈를 완료(Done) 상태로 변경합니다.

## 사용법
```
/done <이슈ID> [메시지]
```

## 예시
- `/done BORI-123` - BORI-123 이슈를 Done으로 변경
- `/done BORI-123 "구현 완료"` - 메시지와 함께 완료 처리

## 실행
mcp__atsel-mcp__change_state 도구를 사용하여 이슈 상태를 변경하세요.
- issue_id: 이슈 ID (필수)
- state: "Done" (고정)

메시지가 있으면 mcp__atsel-mcp__add_comment로 코멘트도 추가하세요.
