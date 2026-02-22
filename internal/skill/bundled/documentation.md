---
name: documentation
description: 코드 문서화 시 참고하는 가이드라인
tags: [docs, quality]
scope: global
---
# Documentation

문서 작성 시 다음 가이드라인을 따르세요:

## 코드 주석
- 공개(public) API에는 반드시 문서 주석 작성
- "무엇을(what)"이 아닌 "왜(why)"를 설명
- 명확하지 않은 비즈니스 로직이나 알고리즘에 주석 추가
- TODO/FIXME 주석에는 담당자와 이슈 번호 포함

## README 구조
1. 프로젝트 개요와 목적
2. 설치 및 설정 방법
3. 기본 사용 예시
4. 설정 옵션 설명
5. 기여 가이드라인

## API 문서
- 모든 엔드포인트의 요청/응답 형식 명시
- 에러 코드와 의미 정리
- curl 또는 코드 예시 포함
- 인증 방식 설명

## 변경 이력
- 주요 변경사항은 CHANGELOG에 기록
- 시맨틱 버저닝 준수
- 브레이킹 변경은 명확히 표시
