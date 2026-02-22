# /history - 작업 히스토리 조회

프로젝트의 작업 히스토리를 조회합니다.

## 사용법
```
/history <프로젝트명> [개수]
```

## 예시
- `/history bori-app` - bori-app 프로젝트 히스토리 조회
- `/history shepherd 20` - shepherd 프로젝트 최근 20개 히스토리

## 실행
mcp__shepherd__get_history 도구를 사용하여 작업 히스토리를 조회하세요.
- project_name: 프로젝트 이름 (필수)
- limit: 조회 개수 (선택, 기본 10)

조회 결과를 보기 좋게 정리해서 보여주세요:
- 작업 ID
- 상태 (완료/실패/진행중)
- 요청 내용
- 결과 요약
