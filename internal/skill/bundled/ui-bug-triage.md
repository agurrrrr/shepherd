---
name: ui-bug-triage
description: 스크린샷 기반 UI 버그 분석 및 수정 가이드
tags: [ui, debugging, svelte]
scope: global
---
# UI Bug Triage

스크린샷이나 버그 리포트를 기반으로 UI 문제를 분석할 때 다음 절차를 따르세요:

## 1단계: 문제 식별
- 스크린샷에서 시각적 문제 정확히 특정 (레이아웃 깨짐, 정렬, 색상, 여백 등)
- 재현 조건 파악: 특정 화면 크기, 브라우저, 데이터 상태
- 브라우저 콘솔 에러 확인 (JavaScript 에러, 네트워크 에러)

## 2단계: 원인 분석
- CSS 문제: DevTools로 computed styles 확인, 상속/우선순위 문제
- 컴포넌트 문제: props 전달, 상태 관리, 조건부 렌더링
- 데이터 문제: API 응답 형식, null/undefined 처리, 빈 배열

## 3단계: 수정 접근법

### CSS/레이아웃
- Flexbox/Grid 정렬 문제 → justify, align 속성 확인
- 오버플로우 → overflow, text-overflow, word-break
- 반응형 → media query 브레이크포인트 확인
- 간격 → padding, margin, gap 값 확인

### Svelte 컴포넌트
- 반응성 문제 → `$:` 반응형 선언문 확인
- 조건부 렌더링 → `{#if}` 블록 조건 검증
- 이벤트 핸들링 → 이벤트 바인딩과 전파 확인
- SSE/실시간 업데이트 → EventSource 연결 상태 확인

### 모바일 최적화
- 터치 타겟 크기 최소 44px
- 가로 스크롤 발생 여부
- 폰트 크기 가독성
- 네비게이션 접근성

## 4단계: 검증
- 수정 후 문제가 발생한 동일 조건에서 확인
- 다른 화면 크기에서 사이드 이펙트 없는지 확인
- 관련 컴포넌트에 영향이 없는지 확인
