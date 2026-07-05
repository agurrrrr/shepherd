# Shepherd 메모리 누적 개선방향 문서

> 작성일: 2026-07-03  
> 관련 작업: #6887, #6888 (shepherd 크래시), #6890 (panic recovery), #6891 (원인 분석)

---

## 1. 문제 요약

Shepherd가 장시간 실행되는 작업 중 **OOM (Out of Memory) Killer에 의해 강제 종료**되는 현상이 발생했다. 6월 28일 dmesg 로그에 shepherd이 **36GB 메모리**를 사용하다가 kill된 기록이 확인되었으며, 7월 2일에도 동일한 패턴의 크래시가 발생했다.

### 1.1 발견된 두 가지 문제

| # | 문제 | 상태 | 비고 |
|---|------|------|------|
| 1 | **Panic 전파로 인한 크래시** | ✅ 해결 (#6890) | embedded tool dispatch 경로에 recover() 추가 |
| 2 | **출력 데이터 무제한 누적으로 인한 메모리 폭발** | ✅ 해결 (#6914) | 3중 복사 모두 바이트 기반 크기 제한 적용 |

---

## 2. 원인 분석

### 2.1 출력 데이터 3중 복사 구조

현재 shepherd은 하나의 작업 실행 중 **동일한 출력 데이터를 3곳에 중복 저장**한다:

```
claude CLI / opencode / embedded loop
    │
    ├─ (1) outputBuilder (strings.Builder)     ← execute* 함수 내부
    │      용도: 결과 파싱, 에러 메시지, rate limit 감지
    │      위치: interactive.go (4곳), pi.go, pty_unix.go
    │
    ├─ (2) outputLines ([]string)              ← processor.go
    │      용도: DB 저장, summary 생성
    │      위치: internal/queue/processor.go:289
    │
    └─ (3) RunningTask.OutputLines ([]string)  ← interactive.go
           용도: 크래시/중단 시 복구용 출력 저장
           위치: internal/worker/interactive.go:194 (AppendOutput)
```

**데이터 흐름:**
```
CLI stdout → scanner.Scan()
    → outputBuilder.WriteString(line)          // 복사본 #1
    → opts.OnOutput(parsed)                    // 콜백
        → outputLines = append(outputLines...)  // 복사본 #2
        → AppendOutput(sheepName, text)        // 복사본 #3
```

### 2.2 각 저장소의 크기 제한 현황

| 저장소 | 크기 제한 | 최대 크기 | 비고 |
|--------|-----------|-----------|------|
| `outputBuilder` | **없음** | 무제한 | strings.Builder가 자동 확장 |
| `outputLines` | **없음** | 무제한 | slice append로 자동 확장 |
| `RunningTask.OutputLines` | **없음** | 무제한 | slice append로 자동 확장 |
| embedded loop `messages` | `trimMessages()`로 토큰 기반 제한 | ~ContextTokens(32K) | 토큰 수 기준이지 바이트 기준 아님 |
| tool result (`truncateToolResult`) | 8,000자 | 8KB | ✅ 이미 제한됨 |

### 2.3 메모리 폭발 시나리오

offllama 프로젝트에서 양26이 모바일 MCP를 사용한 작업 사례:

1. ADB logcat 출력 → bash 도구 → 수만 줄의 로그
2. 스크린샷 캡처 → base64 인코딩된 이미지 데이터
3. 모바일 UI 요소 덤프 → 대량의 JSON

이 출력들이 claude CLI의 stream-json으로 들어오면서:
- 매 라인마다 `outputBuilder`, `outputLines`, `RunningTask.OutputLines`에 **3번씩 복사**
- 작업이 길어질수록 메모리 선형 증가
- 시스템 가용 메모리 부족 시 OOM Killer 발동

### 2.4 왜 지금까지 발견되지 않았나

- 일반적인 코드 작업(bash 몇 번, 파일 수정)에서는 출력이 수십 KB 수준
- 모바일 MCP + logcat 조합이 처음으로 대량 출력을 유발
- 52GB 시스템 메모리 중 llama-server(13GB) + Gradle(5GB+) 등이 이미 사용 중이라 가용 여유가 적었음

---

## 3. 개선 방향

### 방향 A: outputBuilder에 크기 제한 추가

#### 개요
각 execute* 함수의 `outputBuilder`에 최대 크기를 두고, 초과 시 오래된 데이터를 버린다.

#### 구현 방식
```go
const maxOutputBuilderBytes = 10 * 1024 * 1024 // 10MB

// 스캔 루프 내부
mu.Lock()
if outputBuilder.Len() < maxOutputBuilderBytes {
    outputBuilder.WriteString(line + "\n")
} else if !outputTruncated {
    outputBuilder.WriteString("\n...[output truncated, keeping tail only]...\n")
    outputTruncated = true
}
// 초과 시: 최근 데이터만 유지하기 위해 주기적으로 앞부분 잘라내기
mu.Unlock()
```

또는 더 정교하게: ring buffer 또는 tail-only 버퍼를 사용하여 마지막 N 바이트만 유지.

#### outputBuilder 사용처별 영향 분석

| 사용처 | 전체 출력 필요? | 제한 시 영향 | 대응 |
|--------|----------------|-------------|------|
| `parseStreamOutput(fullOutput)` | 부분 — result 이벤트만 필요 | result 이벤트가 앞쪽에 있으면 유실 위험 | stream-json에서 result는 항상 마지막에 옴 → tail 유지로 안전 |
| `parseOpenCodeOutput(fullOutput)` | 부분 — result 이벤트만 필요 | 동일 | 동일 |
| Rate limit 감지 (`errStr := fullOutput`) | 부분 — 에러 메시지 근처만 필요 | 초반 rate limit 메시지 유실 가능 | 에러 메시지 자체(`err.Error()`)로도 감지 가능 |
| 에러 메시지 (`truncateStr(fullOutput, 500)`) | 아니요 — 마지막 500자만 사용 | 영향 없음 | 영향 없음 |
| PTY 경로: 토큰/비용 추출 | 부분 — 전체에서 패턴 검색 | 초반 패턴 유실 위험 | PTY는 auto_approve=false일 때만 사용, 일반적이지 않음 |

#### 사이드 이펙트 평가

- **낮음**: stream-json/opencode JSON의 result 이벤트는 항상 출력의 마지막에 위치하므로 tail 유지 방식이 안전함
- **주의점**: rate limit 감지 로직이 fullOutput을 검사하는데, 이 경우 err.Error() 문자열도 함께 검사하므로 완전히 우회 불가능하지는 않음
- **PTY 경로**: 토큰 추출을 위해 전체 출력에서 패턴을 찾는데, 이 경로는 auto_approve=false일 때만 사용되므로 실사용 빈도 낮음. 별도 처리 필요 가능

---

### 방향 B: RunningTask.OutputLines 크기 제한

#### 개요
크래시 복구용인 `RunningTask.OutputLines`에 최대 라인 수 또는 바이트 제한을 둔다.

#### 구현 방식
```go
const maxRunningTaskOutputLines = 5000 // 또는 바이트 기반

func AppendOutput(sheepName string, text string) {
    runningTasksMu.RLock()
    task, ok := runningTasks[sheepName]
    runningTasksMu.RUnlock()
    if ok {
        task.outputMu.Lock()
        task.OutputLines = append(task.OutputLines, text)
        // 크기 제한: 최근 N줄만 유지
        if len(task.OutputLines) > maxRunningTaskOutputLines {
            // 앞부분 버리기 (slice 재할당)
            task.OutputLines = task.OutputLines[len(task.OutputLines)-maxRunningTaskOutputLines:]
        }
        task.outputMu.Unlock()
    }
}
```

#### 사용처별 영향 분석

| 사용처 | 전체 출력 필요? | 제한 시 영향 |
|--------|----------------|-------------|
| `StopTask` → 크래시/중단 시 DB 저장 | 아니요 — 요약만 있으면 충분 | 마지막 N줄만 저장됨 |
| `GetRunningTaskOutput` → TUI 표시 | 아니요 — 실시간 모니터링용 | 최근 출력만 표시됨 |

#### 사이드 이펙트 평가

- **매우 낮음**: 이 데이터는 크래시 복구용으로만 사용되며, 전체 출력을 보존할 실용적 이유가 없음
- 주의: slice 앞부분 잘라내기 시 메모리 해제가 GC에 의존하므로, 정기적으로 새 slice 할당으로 교체하는 것이 좋음

---

### 방향 C: processor.go outputLines 크기 제한

#### 개요
DB 저장용 `outputLines []string`에 최대 라인 수를 둔다.

#### 구현 방식
```go
const maxOutputLines = 10000 // 또는 바이트 기반

opts := worker.DefaultInteractiveOptions(
    func(text string) {
        outputLines = append(outputLines, text)
        // 크기 제한: 최근 N줄만 유지
        if len(outputLines) > maxOutputLines {
            outputLines = outputLines[len(outputLines)-maxOutputLines:]
        }
        worker.AppendOutput(sheepName, text)
        if p.OnOutput != nil {
            p.OnOutput(sheepName, projectName, text)
        }
    },
    nil,
)
```

#### 사용처별 영향 분석

| 사용처 | 전체 출력 필요? | 제한 시 영향 |
|--------|----------------|-------------|
| `CompleteTaskWithTokens(taskID, ..., outputLines)` → DB 저장 | 부분 — 웹 UI에서 확인 | 과거 출력 일부를 볼 수 없음 |
| `buildSummaryFromOutput(outputLines)` → summary 생성 | 아니요 — 마지막 5줄만 사용 | 영향 없음 |
| `FailTaskWithOutput(taskID, ..., outputLines)` → 실패 시 DB 저장 | 부분 — 디버깅용 | 동일 |

#### 사이드 이펙트 평가

- **중간**: 웹 UI에서 작업 출력을 확인할 때 과거 부분이 누락됨
- 완화책: head(처음 N줄) + tail(마지막 N줄) 방식으로 양끝을 보존하면 실용성 유지 가능
- DB에 JSON 배열로 저장되므로 저장 크기도 함께 줄어드는 이점

---

### 방향 D: 중복 저장 제거 (outputBuilder ↔ outputLines 통합)

#### 개요
`outputBuilder`(execute* 함수 내부)와 `outputLines`(processor.go)가 사실상 같은 데이터를 저장하므로, 하나로 통합한다.

#### 구현 방식

**옵션 D-1: outputBuilder 제거, outputLines만 유지**
- execute* 함수에서 결과 파싱을 위해 fullOutput이 필요하므로, OnOutput 콜백으로 전달된 데이터를 함수 반환값으로 다시 받아야 함
- ExecuteResult에 Output 필드 추가하여 반환

```go
type ExecuteResult struct {
    Result           string
    FilesModified    []string
    SessionID        string
    CostUSD          float64
    PromptTokens     int64
    CompletionTokens int64
    Incomplete       bool
    IncompleteReason string
    Output           []string // ← 새 필드: execute* 함수에서 수집한 출력
}
```

processor.go에서는 ExecuteResult.Output을 직접 DB 저장에 사용하고, 별도의 outputLines 수집 불필요.

**옵션 D-2: outputLines 제거, outputBuilder만 유지**
- execute* 함수에서 ExecuteResult에 fullOutput(또는 제한된 버전)을 포함하여 반환
- processor.go는 이를 그대로 DB에 저장

#### 사이드 이펙트 평가

- **중간~높음**: 인터페이스 변경으로 여러 파일 수정 필요
- 장점: 메모리 사용량 절반으로 감소
- 주의: RunningTask.OutputLines는 여전히 별도 필요 (크래시 복구용이므로 execute* 함수 반환값을 사용할 수 없음 — 함수가 정상 종료하지 못했을 때 필요)

---

### 방향 E: Embedded loop messages 배열 관리 강화

#### 개설
embedded loop의 `messages []ChatMessage`는 `trimMessages()`로 컨텍스트 토큰을 제한하지만, 다음 경우에 예상보다 많은 메모리를 사용할 수 있다:

1. 이미지(base64)가 포함된 tool result → `appendPendingImages`로 별도 추가됨
2. 대용량 bash 출력 → `truncateToolResult`로 8000자 제한은 있지만 반복 호출 시 누적

#### 현재 안전장치
- `truncateToolResult`: 각 tool result를 8000자로 제한 ✅
- `trimMessages`: 컨텍스트 토큰 기반 메시지 배열 트리밍 ✅
- 이미지는 ContentParts로 관리되며 base64 원본은 toolRegistry에서 보관

#### 추가 개선 여부
- 현재 구조로 충분히 보호되고 있으므로 **추가 변경 불필요**
- 단, 이미지 최적화(리사이즈 등)는 별도 작업(#6688)에서 이미 처리됨

---

## 4. 우선순위 및 권장 구현 순서

| 우선순위 | 방향 | 위험도 | 효과 | 상태 |
|---------|------|--------|------|------|
| 1순위 | **B: RunningTask.OutputLines 제한** | 매우 낮음 | 높음 | ✅ 구현 완료 (#6914) |
| 2순위 | **A: outputBuilder 제한** | 낮음 | 높음 | ✅ 구현 완료 (#6914) |
| 3순위 | **C: processor.go outputLines 제한** | 중간 | 중간 | ✅ 구현 완료 (#6914) |
| 4순위 | **D: 중복 저장 제거** | 중간~높음 | 높음 | 🔶 검토 후 결정 |
| 미적용 | E: embedded messages 강화 | — | — | ❌ 불필요 |

### 권장 근거

1. **B를 1순위로**: RunningTask.OutputLines는 맵에 저장되어 shepherd 프로세스가 살아있는 동안 해제되지 않으므로 가장 위험한 누수源이다. 수정 범위가 좁고 사이드 이펙트가 거의 없다.

2. **A를 2순위로**: outputBuilder는 작업당 독립적으로 생성/소멸하지만, 단일 작업이 장시간 실행되면 그 안에서 폭발한다. 결과 파싱 로직에 영향을 주지 않도록 tail 유지 방식을 써야 한다.

3. **C를 3순위로**: DB 저장 품질과 직결되므로 head+tail 방식으로 보존성을 높여야 한다.

4. **D는 검토 단계**: 인터페이스 변경을 수반하므로 B/A/C 적용 후 잔여 메모리 문제가 있는지 관찰한 후 결정한다.

---

## 5. 구현 시 주의사항

### 5.1 Tail 유지 방식 구현 시 주의점

slice의 앞부분을 잘라낼 때(`s = s[n:]`) 기저 배열은 해제되지 않으므로 실제 메모리 해제를 위해서는 새 slice 할당이 필요하다:

```go
// ❌ 이렇게 하면 메모리가 해제되지 않음
task.OutputLines = task.OutputLines[len(task.OutputLines)-max:]

// ✅ 새 slice 할당으로 GC가 기저 배열 회수 가능
kept := make([]string, max)
copy(kept, task.OutputLines[len(task.OutputLines)-max:])
task.OutputLines = kept
```

### 5.2 동시성 안전

- `outputBuilder`: 이미 mutex(`mu`)로 보호됨 → 제한 로직도 같은 락 내에서 수행
- `RunningTask.OutputLines`: 이미 `outputMu`로 보호됨 → 동일
- `processor.go outputLines`: 단일 고루틴(processTask)에서만 접근 → 별도 락 불필요

### 5.3 크기 기준 선택

| 기준 | 장점 | 단점 |
|------|------|------|
| 라인 수 | 구현 단순 | 한 줄이 매우 길면(예: base64 이미지) 의미 없음 |
| 바이트 수 | 정확한 메모리 통제 | 매번 Len() 호출 필요 |
| 하이브리드 | 두 장점 모두 | 구현 복잡도 증가 |

**권장**: 바이트 수 기준. Go의 strings.Builder와 slice 모두 Len()이 O(1)이므로 성능 부담이 없다.

### 5.4 임계값 설정

| 항목 | 권장 임계값 | 근거 |
|------|-----------|------|
| outputBuilder | 10MB | 결과 파싱에 충분한 tail 확보 + 일반 작업(수십 KB)은 영향 없음 |
| RunningTask.OutputLines | 5MB | 크래시 복구용 요약으로 충분 |
| processor.go outputLines | 20MB (head 5MB + tail 15MB) | DB 저장 + 웹 UI 확인용 |

---

## 6. 테스트 계획

### 6.1 단위 테스트

```go
func TestAppendOutputLimit(t *testing.T) {
    // RunningTask.OutputLines가 임계값 초과 시 앞부분이 잘리는지 확인
    registerRunningTask("test", func() {}, nil)
    for i := 0; i < maxRunningTaskOutputLines+100; i++ {
        AppendOutput("test", fmt.Sprintf("line %d", i))
    }
    _, lines := GetRunningTaskOutput("test")
    if len(lines) > maxRunningTaskOutputLines {
        t.Errorf("output lines not capped: got %d", len(lines))
    }
    // 마지막 줄이 보존되는지 확인
    if lines[len(lines)-1] != fmt.Sprintf("line %d", maxRunningTaskOutputLines+99) {
        t.Errorf("tail not preserved")
    }
}
```

### 6.2 통합 테스트

- 대량 출력을 생성하는 mock 작업 실행 후 shepherd 메모리 사용량 모니터링
- `/proc/<pid>/status`의 VmRSS 추적으로 메모리 안정성 확인

---

## 7. 요약

Shepherd의 OOM 크래시는 두 가지 원인의 조합이었다:

1. **즉각적 원인**: embedded tool dispatch 경로의 panic 전파 → #6890에서 recover()로 해결
2. **근본적 원인**: 출력 데이터의 무제한 3중 복사 → 본 문서에서 다룰 개선 대상

방향 B(RunningTask 제한)와 A(outputBuilder 제한)를 우선 적용하면 사이드 이펙트 없이 가장 위험한 메모리 누수를 차단할 수 있다. 이후 C(outputLines 제한)와 D(중복 제거)를 단계적으로 적용하여 전체 메모리 사용량을 합리적인 수준으로 관리한다.

---

## 8. 구현 이력 (#6914)

### 구현 내역

| 방향 | 구현 내용 | 파일 |
|------|----------|------|
| **B** | `AppendOutput()`에 5MB 바이트 제한 추가, tail 유지 + 마커 삽입 | `internal/worker/interactive.go` |
| **A** | `strings.Builder` → `CappedBuffer` 교체 (10MB 제한), 4개 execute* 함수 | `interactive.go`, `pi.go`, `pty_unix.go` |
| **C** | `outputLines`에 20MB head+tail 제한 추가 | `internal/queue/processor.go` |

### 새로 추가된 파일

| 파일 | 설명 |
|------|------|
| `internal/worker/capped_buffer.go` | 스레드 세이프한 크기 제한 바이트 버퍼 (tail 유지 방식) |
| `internal/worker/capped_buffer_test.go` | CappedBuffer 단위 테스트 (6개 케이스) |
| `internal/worker/append_output_limit_test.go` | AppendOutput 크기 제한 테스트 (2개 케이스) |
| `internal/queue/output_capping_test.go` | head+tail 캡핑 테스트 (3개 케이스) |

### 임계값 요약

| 저장소 | 제한 | 방식 | 근거 |
|--------|------|------|------|
| outputBuilder (CappedBuffer) | 10 MB | tail 유지 | stream-json result 이벤트는 항상 마지막에 위치 |
| RunningTask.OutputLines | 5 MB | tail 유지 + 마커 | 크래시 복구용 요약으로 충분 |
| processor.go outputLines | 20 MB | head(4MB) + tail(16MB) | DB 저장 + 웹 UI 확인용, 양끝 보존 |

### 방향 D (중복 저장 제거) — 보류

인터페이스 변경(`ExecuteResult`에 `Output` 필드 추가 등)을 수반하므로, B/A/C 적용 후 잔여 메모리 문제가 있는지 관찰한 후 결정.
