# /issues - 이슈 목록 조회

이슈 트래커(YouTrack)에서 이슈 목록을 조회합니다.

## 사용법
```
/issues [프로젝트] [쿼리]
```

## 예시
- `/issues` - 전체 이슈 조회
- `/issues BORI` - BORI 프로젝트 이슈 조회
- `/issues BORI "State: 개발"` - BORI 프로젝트의 개발 상태 이슈 조회

## 실행
mcp__atsel-mcp__list_issues 도구를 사용하여 이슈 목록을 조회하세요.
- project: 프로젝트 ID (선택)
- query: YouTrack 쿼리 (선택, 예: "State: Open", "State: 개발")
- max: 최대 조회 개수 (기본 10)

조회 결과를 보기 좋게 정리해서 보여주세요.
