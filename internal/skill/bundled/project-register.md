---
name: project-register
description: shepherd에 프로젝트를 등록하는 정확한 절차. MCP 도구로는 등록할 수 없고 CLI로만 가능합니다.
tags: [shepherd, setup]
scope: global
effort: low
maxTurns: 5
---
# 프로젝트 등록

shepherd에 프로젝트를 등록하는 방법입니다. **반드시 CLI를 사용**하세요. shepherd MCP 도구에는 등록 기능이 없습니다.

## 등록 방법

```bash
# 방법 1 — 임의 위치 등록
shepherd project add <project-name> <absolute-path>

# 방법 2 — 현재 디렉터리를 등록
cd <project-path>
shepherd init
```

등록 성공 시 양(worker)이 자동 할당됩니다. 양 이름은 풀에서 가져오므로 직접 정할 필요 없습니다.

## 흔한 함정 — 이걸 등록 확인으로 쓰지 마세요

`mcp__shepherd__get_history`로 빈 결과가 나온다고 등록된 것이 **아닙니다**. 미등록 프로젝트에 대해서는 이제 다음과 같은 명시적 에러를 반환합니다:

```
project 'foo' is not registered — register it first with `shepherd project add foo <absolute-path>`
```

이 에러를 보면 등록부터 진행하세요.

## 등록 후 확인

```bash
shepherd project list
```

해당 프로젝트가 목록에 보이고 양이 할당되어 있으면 완료입니다. 또는 `mcp__shepherd__get_status`로도 확인 가능합니다.

## 주의사항

- 경로는 **절대 경로**로 넣으세요 (`~`나 상대 경로 X)
- 프로젝트명은 보통 디렉터리명과 동일하게 — 일관성을 위해
- 이미 등록된 이름으로 다시 add 하면 `already exists` 에러가 납니다
