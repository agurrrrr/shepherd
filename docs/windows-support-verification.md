# Windows 셸 지원 — 실기기 검증 체크리스트

**머지 게이트 문서.** 작업 #7925 MAGI 합의: 아래 핵심 경로(`-EncodedCommand` 프리앰블, `taskkill` 트리 종료, `safePath` 백슬래시 가드)는 **실제 Windows 머신 검증 없이 main 머지 금지**.

| 항목 | 값 |
|------|-----|
| 관련 커밋 | `89d274c` (1/6) · `739f32c` (2/6) · `b90c947` (3/6) · `c1bfb41` (4/6) · `19acf36` (5/6) |
| 선행 조사 | #7923 조사 · #7925 MAGI 판정 · #7926 6단계 분할 · #7927–#7931 구현 |
| 대상 범위 | **임베디드 프로바이더**의 `bash` 도구. CLI 프로바이더(claude/opencode) interactive/ConPTY는 범위 밖(P2). |
| 단위 테스트 한계 | PowerShell 프리앰블·인코딩·.ps1 폴백 테스트는 순수 문자열 조립만 검증한다. **실행 의미론은 이 체크리스트로만 실증**. |

검증 후 이 문서 상단 또는 각 항목에 `PASS` / `FAIL` / 날짜 / 환경 / 검증자 이니셜을 기록하라.

---

## 0. 환경 매트릭스 (최소 3조합)

아래 **최소 3조합**을 모두 통과해야 머지 게이트를 연다. 가능하면 4번째(순정 + pwsh 7)도 권장.

| ID | Git Bash | PowerShell | 기대 자동 탐지 결과 | 용도 |
|----|----------|------------|---------------------|------|
| **M1** | 설치됨 (PATH 또는 기본 경로) | 5.1 및/또는 7 무관 | `…\Git\bin\bash.exe` (또는 PATH의 bash) | 기본 경로. 모델은 POSIX 방언을 계속 씀. |
| **M2** | **없음** (PATH·`Program Files\Git` 모두 제거/미설치) | **pwsh 7** 있음 | `pwsh.exe` | PowerShell 폴백 + 7+ `$PSStyle` 경로 |
| **M3** | **없음** | **powershell 5.1만** (pwsh 없음) | `powershell.exe` | 5.1 프리앰블 가드의 **유일한 실증 환경** |
| M4 (권장) | 없음 | 5.1 + 7 공존, `SHEPHERD_SHELL` 미설정 | `pwsh.exe` (후보 순서상 7 우선) | 자동 탐지 우선순위 확인 |

**설치/제거 팁**

- Git Bash 없음 재현: Git 미설치 또는 `SHEPHERD_SHELL`로 우회하지 말고, PATH에서 `bash`를 빼고 `C:\Program Files\Git` / `(x86)` 경로를 일시 rename.
- WSL이 켜진 머신: PATH에 `C:\Windows\System32\bash.exe`가 잡히면 **Linux 배포판**이 뜬다(프로젝트 경로·FS가 다름). 이 경우 자동 탐지는 "bash 있음"으로 성공하지만 실사용은 깨진다 → §2 오버라이드·§9에서 확인.

**빌드 산출물**

- 검증 대상 바이너리는 해당 커밋에서 빌드한 Windows 바이너리 (`GOOS=windows go build` 또는 CI `windows-latest` 산출물).
- 설정: `shell` 키 비움(자동 탐지)을 기본으로 두고, 오버라이드 항목에서만 설정.

---

## 1. 셸 탐색 검증

**코드:** `internal/embedded/shell_windows.go` (`detectShell`) · `internal/embedded/shell.go` (`resolveShell`, `resolveShellOverride`) · `internal/config/config.go` (`GetShell`, 우선순위 `SHEPHERD_SHELL` > config `shell`)

### 1.1 자동 탐지 (매트릭스별)

| 환경 | 절차 | 기대 결과 |
|------|------|-----------|
| M1 | 임베디드 작업에서 bash 도구로 `echo SHELL_OK` (또는 Git Bash면 `uname -o`) | 성공. 선택된 셸이 Git Bash. |
| M2 | 동일 | 성공. 툴 description에 PowerShell 안내 한 줄이 붙을 수 있음 (`bashToolDescription`, `shell.go`). |
| M3 | 동일 | 성공. 5.1 경로. |
| (옵션) bash/pwsh/powershell **전부** 제거 | bash 도구 1회 | **actionable 에러**: `no supported shell found: install Git for Windows (bash) or PowerShell 7 (pwsh), or set the "shell" config key / SHEPHERD_SHELL …`. `exec: "bash": executable file not found` 원문만 나오면 FAIL. |

### 1.2 오버라이드

| 절차 | 기대 결과 | 실패 시 |
|------|-----------|---------|
| `SHEPHERD_SHELL=C:\Program Files\Git\bin\bash.exe` (공백 경로) | 해당 bash 사용. 인자 거부 없이 수락. | `resolveShellOverride` / `GetShell` |
| config.yaml `shell: pwsh` 또는 절대 경로 | 그 셸로 실행 | 동일 |
| `SHEPHERD_SHELL`과 config 동시 설정 | **env가 이김** | `GetShell` |
| `shell: "pwsh -NoProfile"` (플래그 포함) | 거부/에러 (실행 파일 하나만 허용) | override 검증 |
| 셸 전부 없는 환경 + 오버라이드 없음 | §1.1 actionable 에러 | `detectShell` |

### 1.3 WSL bash 함정 (해당 머신만)

| 절차 | 기대 결과 |
|------|-----------|
| WSL 활성 + Git Bash 미설치, PATH에 System32 `bash.exe`만 있음 | 자동 탐지가 WSL bash를 집을 수 있음. 프로젝트 디렉터리 기준 파일 작업이 깨지면 **알려진 함정**. `SHEPHERD_SHELL`을 Git Bash 경로로 지정해 복구되는지 확인. |

---

## 2. PowerShell 5.1 프리앰블 (`$PSStyle` 버전 가드)

**코드:** `internal/embedded/shell_powershell.go` — `psPreamble` / `psScript()`  
**실증 환경:** **M3 필수** (5.1만). M2(pwsh 7)만 통과해도 이 항목은 PASS가 아니다.

| # | 절차 | 기대 결과 | 실패 시 볼 곳 |
|---|------|-----------|----------------|
| 2.1 | M3에서 bash 도구로 단순 명령: `Write-Output 'ok'` 또는 `echo ok` | 출력에 `ok`, **첫 줄부터 실패하지 않음** | `psPreamble`의 `if ($PSVersionTable.PSVersion.Major -ge 7) { $PSStyle… }` 가드 누락 시 5.1에서 `$null`에 속성 대입 + `$ErrorActionPreference='Stop'` → **모든 명령 즉사** |
| 2.2 | 같은 명령을 M2(pwsh 7)에서 | 성공 (가드 안 쪽이 실행돼도 정상) | `psScript` |
| 2.3 | (리스크) 데몬/서비스처럼 콘솔 없는 프로세스에서 연속 실행 | `[Console]::OutputEncoding = …` 가 **terminating error**가 되지 않는지. 전멸하면 `try{}catch{}` 래핑 후보 (3/6 위키 미해결 항목) | `psPreamble` 2행 |

**이 항목이 머지 게이트의 핵심 하나다.** 단위 테스트는 문자열에 가드가 *들어 있는지만* 보고, 5.1 런타임 동작은 검증하지 않는다.

---

## 3. exit code 정규화

**코드:** `psEpilogue` (`shell_powershell.go`) + `$ErrorActionPreference = 'Stop'`  
**환경:** M2 또는 M3 (PowerShell 경로). Git Bash(M1)는 POSIX exit와 비교용으로만.

| # | 절차 (PowerShell 경로) | 기대 결과 | 실패 시 |
|---|------------------------|-----------|---------|
| 3a | 실패 네이티브: `cmd /c exit 3` | 도구 결과에 non-zero 반영 (`exit 3` 등). 성공으로 보이지 않음. | `$LASTEXITCODE` 전파 epilogue |
| 3b | 실패 cmdlet: `Get-Item 'C:\this\path\does\not\exist-shepherd-verify'` (또는 존재하지 않는 명령let) | **성공으로 오인되지 않음** (Stop + terminating error). | `$ErrorActionPreference='Stop'` 누락 시 exit 0 오인 |
| 3c | 순수 성공 cmdlet: `Get-Date` 또는 `Write-Output 'hi'` | 성공(0). `$LASTEXITCODE`가 `$null`이어도 epilogue null 가드로 정상 종료. | `if ($null -ne $LASTEXITCODE)` |

M1(Git Bash) 참고: `bash -c 'exit 3'` 형태가 non-zero로 보이는지만 스모크.

---

## 4. 긴 명령 `.ps1` 폴백

**코드:** `psEncodedCommandLimit = 30000` (인코딩 **후** 문자 수), `psInvocation`, `writePowerShellScript` · 삭제: `shellProc.release` + `execBash`의 `defer proc.close()`

| # | 절차 | 기대 결과 | 실패 시 |
|---|------|-----------|---------|
| 4.1 | 인코딩 후 임계값을 넘기는 큰 명령 (예: 수 KB~10KB+ 문자열을 파일에 쓰는 heredoc/here-string) | 명령 성공, 대상 파일 내용 일치 | `psInvocation` → `-File` 분기, UTF-8 **BOM** 기록 여부 (5.1은 BOM 없으면 ANSI로 읽어 한글 깨짐) |
| 4.2 | 동일 규모 명령이 **성공**한 뒤 | `%TEMP%`(또는 `os.TempDir`)에 `shepherd-*.ps1` **잔존 없음** | `shellProc.close` / `release`가 성공 경로에서 안 불림 |
| 4.3 | 의도적 실패 명령(큰 스크립트 안에서 throw / exit 1) | 실패 보고 + 임시 `.ps1` 삭제 | 동일 |
| 4.4 | 긴 명령 + 짧은 타임아웃으로 **타임아웃** | 타임아웃 에러 + 임시 `.ps1` 삭제 | cancel 경로에서도 `close()` |

임계값 근거: CreateProcess 커맨드라인 ~32767자, 셸 경로·플래그 여유 → 30000. UTF-16LE+base64 ≈ 2.7× → 원본 약 11KB.

---

## 5. 프로세스 트리 종료 (taskkill)

**코드:** `internal/embedded/process_windows.go` — `killProcessGroup` → `killTreeWithTaskkill` (`taskkill /T /F /PID`) · `newShellCmd`의 `cmd.Cancel` · 폴백 `Process.Kill`  
**P2 아님:** Job Object는 백로그. 지금은 taskkill 검증.

| # | 절차 | 기대 결과 | 실패 시 |
|---|------|-----------|---------|
| 5.1 | 자식을 낳는 명령: `npm install`(느린 패키지) 또는 `go build` 대형 모듈, 또는 PowerShell `Start-Process`/`Start-Sleep` 체인 | 실행 중 작업관리자에 셸+자식 PID 확인 가능 | — |
| 5.2 | 작업 타임아웃 또는 작업 중단(cancel) | 셸 **및 자식**이 사라짐. 고아 `go`/`node`/`Sleep` 없음 | `killProcessGroup` / Cancel이 기본 `Process.Kill`만 쓰는지, taskkill PATH |
| 5.3 | taskkill 실패 시뮬레이션(가능 시) | 최소한 셸 프로세스는 `Process.Kill` 폴백으로 종료 | `killTreeWithTaskkill` 폴백 분기 |

**참고 (taskkill 한계 → P2 근거):** 자식이 새 프로세스 그룹/세션으로 분리되면 `/T`도 놓칠 수 있음. PATH·권한 의존, cancel 레이스. 정석은 Job Object (`CREATE_SUSPENDED` → assign → `KILL_ON_JOB_CLOSE` → resume). `shellProc.cleanup` 자리는 이미 확보됨.

---

## 6. safePath (경로 탈출 가드)

**코드:** `ToolRegistry.safePath` · `isEscapingRel` (`internal/embedded/tools.go`)  
**성격:** 하드 시큐리티 경계가 아니라 **model-mistake guard**의 플랫폼 버그 수정(백슬래시 `filepath.Rel` 결과).

| # | 절차 | 기대 결과 | 실패 시 |
|---|------|-----------|---------|
| 6.1 | `read_file` / `write_file` 경로: `..\..\Windows\System32\drivers\etc\hosts` (또는 프로젝트 밖 `..\..\` 계열) | **차단** (에러). 파일 내용이 읽히거나 쓰이지 않음 | `isEscapingRel` — `rel`의 `\` 미정규화 시 구 버그로 통과 |
| 6.2 | 정상 프로젝트 상대 경로 `README.md` 등 | 허용 | `safePath` |
| 6.3 | 이름 `..foo` (점이 두 개지만 상위 디렉터리 아님) | 허용 (오탐 아님) | `isEscapingRel` — `..` / `../` 접두만 차단 |

단위 테스트: `TestIsEscapingRel` (Linux CI에서도 `..\foo` 케이스 포함 — `filepath.ToSlash` 대신 무조건 `\`→`/` 치환).

---

## 7. 한글 출력 / 인코딩

**코드:** `decodeShellOutput` (`tools.go`) — UTF-8 유효 시 그대로; NUL 있으면 바이너리 취급; 그 외 **EUC-KR/CP949 시도 + round-trip 검증** 통과 시에만 채택. bash stdout/stderr 경계에만 적용.  
**환경:** 한국어 Windows + 시스템 로캘 CP949 가정. Go 툴체인(`go build` 등)은 UTF-8이라 이 폴백 대상이 아님.

| # | 절차 | 기대 결과 | 실패 시 |
|---|------|-----------|---------|
| 7.1 | 네이티브/레거시 도구가 CP949로 한글을 내는 명령 (환경에 맞는 것 선택) | 도구 결과에 한글이 깨지지 않거나, 깨지더라도 **통째로 binary 오판**되지 않음 | `decodeShellOutput`, `isBinary` 계열 |
| 7.2 | `go build` / `go test` 한글 경로·메시지 | 기존과 같이 UTF-8로 정상 (폴백 미적용 경로) | 전역 디코딩이 redact를 건드리지 않았는지 |
| 7.3 | PowerShell 5.1 출력 선두 BOM | 모델에게 보이는 문자열이 BOM 때문에 이상하지 않은지 (`[Text.Encoding]::UTF8`은 .NET Framework에서 BOM 포함 가능) | 프리앰블 / Go 쪽 trim |

---

## 8. 에이전트 실사용 1회 (인수 기준)

**이게 진짜 인수 기준이다.** 개별 단위 항목을 모두 통과해도 이 항목 실패 시 머지 보류.

| # | 절차 | 기대 결과 |
|---|------|-----------|
| 8.1 | 임베디드 프로바이더 양으로 **실제 작업 하나** 실행: 예) 작은 Go 파일 수정 + `go build`/`go test` 검증까지 에이전트가 끝까지 수행 | 완료. bash 도구·read/edit/write가 연쇄적으로 성공. |
| 8.2 | 매트릭스 | **최소 M1 1회 + (M2 또는 M3) 1회**. PowerShell만 있는 환경에서 POSIX 습관(`&&`, `cat`)이 프롬프트 분기(5/6) 덕분에 완화되는지도 관찰. |
| 8.3 | 관찰 포인트 | 빌드 검증 게이트가 bash 호출을 인식하는지(`loop.go` `case "bash":`); 별칭 `shell`/`powershell`을 쓰지 않는지; truncation 시 힌트가 방언에 맞는지(`truncationHint`). |

**코드 힌트 (실사용 실패 시)**

| 증상 | 볼 곳 |
|------|--------|
| 셸 미발견 | `detectShell`, `GetShell` |
| 5.1 전멸 | `psPreamble` `$PSStyle` 가드, Console OutputEncoding |
| 명령은 도는데 문법이 계속 틀림 | 5/6 프롬프트 분기 `ShellUsesPowerShell`, `internal/worker/embedded.go` `shellDialect*` |
| glob이 전 파일 검색 | `compileGlobFilter` / `globToRegexp` |
| 타임아웃 후 고아 프로세스 | `killProcessGroup`, taskkill |
| 프로젝트 밖 읽기/쓰기 | `safePath` / `isEscapingRel` |

---

## 9. 검증 기록 템플릿

```text
날짜:
검증자:
커밋 SHA:
바이너리 출처:

| 항목 | M1 | M2 | M3 | 메모 |
|------|----|----|----|------|
| 1 셸 탐색 |  |  |  |  |
| 2 PS 5.1 프리앰블 | n/a |  |  | M3 필수 |
| 3 exit 정규화 |  |  |  |  |
| 4 .ps1 폴백 |  |  |  |  |
| 5 taskkill 트리 |  |  |  |  |
| 6 safePath |  |  |  |  |
| 7 한글 출력 |  |  |  |  |
| 8 에이전트 실사용 |  |  |  |  |

머지 승인: YES / NO
잔여 이슈:
```

---

## 10. 구현 단계 ↔ 이 체크리스트 매핑

| 단계 | 커밋 | 이 문서 항목 |
|------|------|----------------|
| 1/6 safePath + Windows CI test | `89d274c` | §6, (CI는 단위 회귀) |
| 2/6 shellProc + 탐색 + config | `739f32c` | §1 |
| 3/6 EncodedCommand + 프리앰블 + .ps1 | `b90c947` | §2, §3, §4 |
| 4/6 taskkill | `c1bfb41` | §5 |
| 5/6 프롬프트/truncation/glob/CP949 | `19acf36` | §7, §8 관찰 포인트 |
| 6/6 본 문서 + 위키 + P2 이슈 | (이 문서 커밋) | 머지 게이트 정의 |

---

## 11. 범위 밖 (검증 대상 아님 · 백로그)

- CLI 프로바이더 interactive / ConPTY (`internal/worker/pty_windows.go` → 현재 streaming 폴백)
- Windows Job Object 전환 (taskkill 대체)
- 툴 이름 별칭 `shell`/`powershell` (의도적 미도입 — `loop.go` 가드)
- cmd.exe 자동 폴백 (의도적 미도입)
- `internal/names`, `internal/wiki` 기존 깨진 테스트 수리 (1/6에서 Windows CI에서 패키지 제외만 함)
