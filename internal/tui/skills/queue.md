# /queue - 작업 큐에 추가

프로젝트의 작업 큐에 새 작업을 추가합니다.

## 사용법
```
/queue <프로젝트> <프롬프트>
```

## 예시
- `/queue bori-app 로그인 버그 수정해줘`
- `/queue shepherd 테스트 코드 작성해`

## 실행
mcp__shepherd__task_start 도구를 사용하여 작업을 큐에 추가하세요.
- sheep_name: 프로젝트에 배정된 양 이름
- project_name: 프로젝트 이름 (필수)
- prompt: 작업 내용 (필수)

먼저 shepherd project list로 프로젝트와 배정된 양을 확인한 후 작업을 추가하세요.
