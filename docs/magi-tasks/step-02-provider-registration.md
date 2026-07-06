# Step 02 — `magi` 프로바이더 등록

> 설계서 참조: §4 (프로바이더 등록)
> 선행 단계: 없음 (step-01과 독립)

## 목표

`magi`를 shepherd의 5번째 프로바이더로 등록한다. 이 단계가 끝나면 양(sheep)의 provider로 `magi`를 저장할 수 있고 검증을 통과하지만, **실행은 아직 안 된다** (배선은 step-08).

## 생성/수정할 파일 (이 외 파일 금지)

- **수정**: `ent/schema/sheep.go`
- **재생성**: `ent/` 하위 생성 코드 (`go generate ./ent` 산출물 — 직접 편집 금지)
- **수정**: `internal/worker/worker.go`
- **수정**: `internal/config/config.go`
- 아래 5번의 grep으로 발견되는 프로바이더 열거 지점 (발견 시 목록을 완료 보고에 기재)

## 작업 내용

### 1. ent enum에 `magi` 추가

`ent/schema/sheep.go` ~30행:

```go
field.Enum("provider").
	Values("claude", "opencode", "pi", "embedded", "auto").
```
→ `Values("claude", "opencode", "pi", "embedded", "magi", "auto")` 로 변경하고 주석도 갱신.

그 다음 반드시 실행:
```bash
go generate ./ent
```
이후 `ent/sheep/sheep.go`에 `ProviderMagi Provider = "magi"` 상수가 생성됐는지 확인하라.

### 2. worker 프로바이더 검증 문자열 갱신

`internal/worker/worker.go`에 같은 검증이 **두 곳** 있다 (~71행, ~322행):

```go
if provider != "claude" && provider != "opencode" && provider != "pi" && provider != "embedded" && provider != "auto" {
	return nil, fmt.Errorf("'%s' is not a valid provider (claude, opencode, pi, embedded, auto)", provider)
}
```
두 곳 모두 `"magi"`를 조건과 에러 메시지에 추가한다. **두 곳 다 고쳤는지 grep으로 확인**:
```bash
grep -n "is not a valid provider" internal/worker/worker.go
```

### 3. `IsProviderEnabled` + 기본값

`internal/config/config.go`:
- ~274행 `case "claude", "opencode", "pi", "embedded":` → `"magi"` 추가
- ~58행 부근 `viper.SetDefault("provider_enabled_embedded", true)` 아래에 `viper.SetDefault("provider_enabled_magi", true)` 추가

### 4. 프로바이더 이모지

`internal/worker/worker.go`의 `ProviderEmoji`(~366행)에 케이스 추가:

```go
case sheep.ProviderMagi:
	return "🧠" // Magi = multi-model consensus
```

### 5. 남은 열거 지점 수색 (누락 방지)

Go 코드에서 프로바이더가 하드코딩으로 열거된 다른 지점을 찾아 **같은 방식으로** `magi`를 추가하라:

```bash
grep -rn '"embedded"' --include="*.go" internal/ cmd/ | grep -v _test | grep -v ent/
```

각 히트 지점을 열어 "프로바이더 목록 열거"인 경우에만 `magi`를 추가한다 (embedded 전용 로직, 예: 엔드포인트 설정 CRUD에는 추가하지 마라). 판단이 애매한 지점은 건드리지 말고 완료 보고에 경로를 적어라.

WebUI(Svelte)의 프로바이더 선택지는 이 단계에서 다루지 않는다 (step-09).

## 하지 말 것

- `ent/` 하위 생성 파일을 손으로 편집하지 마라 — `go generate ./ent`만 사용.
- 임베디드 엔드포인트 CRUD(`handlers_system.go`)나 `GetActiveEmbeddedEndpoint` 계열 로직 수정 금지.
- `interactive.go`의 `switch s.Provider`에 magi 케이스를 넣지 마라 — 그건 step-08이다. (지금 넣으면 executor 미주입으로 컴파일은 되지만 반쪽 상태가 커밋된다.)

## 완료 검증

```bash
go generate ./ent
go build ./...
go test ./internal/worker/ ./internal/config/
grep -rn "ProviderMagi" ent/sheep/sheep.go   # 상수 생성 확인
```
모두 성공해야 완료. 커밋 메시지: `feat(magi): register magi provider enum and validation (magi-tasks step-02)`
