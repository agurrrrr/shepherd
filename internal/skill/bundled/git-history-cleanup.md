---
name: git-history-cleanup
description: Git 커밋 히스토리 정리 가이드라인
tags: [git, cleanup]
scope: global
effort: high
maxTurns: 20
disallowedTools: [Edit, Write, NotebookEdit]
---
# Git History Cleanup

Git 히스토리를 정리할 때 다음 가이드라인을 따르세요:

## 사전 준비
- 현재 브랜치 상태 확인 (uncommitted changes 없는지)
- 백업 브랜치 생성: `git branch backup-before-cleanup`
- 리모트와 동기화 상태 확인

## 히스토리 정리 전략

### 단일 초기 커밋으로 스쿼시
오픈소스 공개 등 깨끗한 시작이 필요할 때:
1. orphan 브랜치 생성
2. 모든 파일 추가 후 단일 커밋
3. 기존 브랜치 교체

### 의미 있는 커밋 유지
일반적인 히스토리 정리:
1. interactive rebase로 관련 커밋 squash
2. 커밋 메시지 정리 (conventional commits)
3. fixup 커밋 병합

## 민감정보 제거
- `git log --all -p | grep -i "password\|secret\|token\|api_key"` 로 검색
- BFG Repo-Cleaner 또는 `git filter-repo` 사용
- 제거 후 모든 브랜치와 태그에서 확인

## 주의사항
- force push 전 팀원에게 공지
- 태그가 올바른 커밋을 가리키는지 확인
- CI/CD 파이프라인에 영향이 없는지 검증
- `.git/hooks`가 정상 동작하는지 확인

## 마무리
- `git gc --prune=now` 로 불필요한 객체 정리
- 리모트에 push 후 클론해서 검증
