@rem Windows integration test for the #1476 bug-fix batch (B1/B2/B3/B4).
@rem Run from the repo root in a cmd or Git Bash shell:  scripts\windows_bugfix_integration.bat
@echo off
setlocal EnableDelayedExpansion

set "PASS=0"
set "FAIL=0"

echo ============================================================
echo  Shepherd Windows bug-fix integration tests (#1476)
echo ============================================================
echo.

where go >nul 2>nul
if errorlevel 1 (
  echo [SKIP] go not on PATH - set it first:  set "PATH=C:\Program Files\Go\bin;%%PATH%%"
  exit /b 1
)

REM Build the CLI once so serve/stop tests run the fixed binary.
echo [BUILD] go build -o build\shepherd-test.exe ./cmd/shepherd
go build -o build\shepherd-test.exe ./cmd/shepherd
if errorlevel 1 (
  echo [FAIL] build failed
  exit /b 1
)
echo [OK]   build succeeded
echo.

REM ---------------------------------------------------------------- B1
echo [TEST] B1 - mixed CP949/UTF-8 decode unit tests
go test ./internal/embedded/ -run "TestDecodeShellOutput|TestDecodeCP949Run" -count=1 >nul 2>&1
if errorlevel 1 (
  echo [FAIL] B1 decode tests
  set /a FAIL+=1
) else (
  echo [PASS] B1 decode tests
  set /a PASS+=1
)

REM ---------------------------------------------------------------- B3
echo [TEST] B3 - procutil KillTree unit + tree-kill integration
go test ./internal/procutil/ -count=1 >nul 2>&1
if errorlevel 1 (
  echo [FAIL] B3 procutil tests
  set /a FAIL+=1
) else (
  echo [PASS] B3 procutil tests
  set /a PASS+=1
)

REM ---------------------------------------------------------------- B4 + B2
REM Start the daemon in background, verify the log file is written (B4),
REM then stop it and verify graceful shutdown ran cleanup (B2).

REM Isolate config/state in a temp HOME so we don't touch the real daemon,
REM and use a unique port so we never collide with the daemon running these
REM tests (killing that one would kill the test harness itself).
set "TESTHOME=%TEMP%\shepherd-it-%RANDOM%%RANDOM%"
mkdir "%TESTHOME%" 2>nul
set "HOME=%TESTHOME%"
set "USERPROFILE=%TESTHOME%"
REM Pre-write a config pointing at an uncommon port for this isolated run.
mkdir "%TESTHOME%\.shepherd" 2>nul
(
echo server_host: "127.0.0.1"
echo server_port: 18585
) > "%TESTHOME%\.shepherd\config.yaml"

echo [TEST] B4 - serve -d writes daemon.log and pops no console
start "" /b "%CD%\build\shepherd-test.exe" serve -d >"%TESTHOME%\serve-out.txt" 2>&1
REM give it time to bind + write runtime.json + log (ping used as a sleep; MSYS 'timeout' is not cmd's)
ping -n 7 127.0.0.1 >nul

if exist "%TESTHOME%\.shepherd\logs\daemon.log" (
  echo [PASS] B4 daemon.log created
  set /a PASS+=1
) else (
  echo [FAIL] B4 daemon.log missing under %TESTHOME%\.shepherd\logs
  set /a FAIL+=1
)

REM B2: stop must trigger graceful cleanup. The daemon-stop command is
REM `serve stop` (bare `stop` is the task-cancel command — a mistake here
REM silently runs the wrong CLI path and the daemon never shuts down).
REM We detect graceful shutdown by the daemon removing its runtime.json (only
REM done in the graceful path; TerminateProcess skips it).
echo [TEST] B2 - shepherd serve stop runs graceful shutdown (runtime.json removed)
"%CD%\build\shepherd-test.exe" serve stop >nul 2>&1
ping -n 5 127.0.0.1 >nul
if exist "%TESTHOME%\.shepherd\runtime.json" (
  echo [FAIL] B2 runtime.json still present - graceful cleanup did NOT run
  set /a FAIL+=1
) else (
  echo [PASS] B2 runtime.json removed by graceful shutdown
  set /a PASS+=1
)

echo.
echo ============================================================
echo  RESULT: %PASS% passed, %FAIL% failed
echo ============================================================

REM cleanup
rmdir /s /q "%TESTHOME%" 2>nul
if %FAIL% gtr 0 exit /b 1
exit /b 0
