# Step 01 — 설정 스키마: `embedded.yaml`에 `magi` 섹션 추가

> 설계서 참조: §7 (설정 스키마), §2.2 (동일 모델 경고), §4 (base_url 중복 경고)
> 선행 단계: 없음

## 목표

`~/.shepherd/embedded.yaml`에 `magi` 섹션을 읽고/쓰고/검증하는 코드를 `internal/config` 패키지에 추가한다. 이 단계에서는 **설정만** 다룬다 — 합의 로직은 만들지 않는다.

## 생성/수정할 파일 (이 외 파일 금지)

- **생성**: `internal/config/magi.go`
- **생성**: `internal/config/magi_test.go`
- **수정**: `internal/config/config.go` — `EmbeddedConfig` 구조체에 필드 1개 추가만

## 작업 내용

### 1. `EmbeddedConfig`에 Magi 필드 추가

`internal/config/config.go`의 `EmbeddedConfig` 구조체(~496행)에 필드를 추가한다:

```go
type EmbeddedConfig struct {
	Endpoints []EmbeddedEndpoint `mapstructure:"endpoints"`
	Magi      *MagiConfig        `mapstructure:"magi" yaml:"magi,omitempty"`
}
```

포인터인 이유: 섹션이 없는 기존 파일과 구분하기 위해 (없으면 nil).

### 2. `internal/config/magi.go` 생성

**중요**: 모든 신규 구조체에 `yaml:"snake_case"` 태그를 명시하라. yaml.v3는 mapstructure 태그를 무시하므로, 태그가 없으면 키가 `endpointid`처럼 붙어버린다. `mapstructure` 태그도 함께 단다 (기존 스타일 유지).

```go
package config

// MagiProposer selects one embedded endpoint as a deliberation member.
type MagiProposer struct {
	EndpointID   string `mapstructure:"endpoint_id" yaml:"endpoint_id"`
	Persona      string `mapstructure:"persona" yaml:"persona"` // melchior | balthasar | casper | custom
	CustomPrompt string `mapstructure:"custom_prompt" yaml:"custom_prompt,omitempty"`
}

// MagiAggregator selects the synthesis/judging backend.
type MagiAggregator struct {
	Type       string `mapstructure:"type" yaml:"type"` // claude_cli | endpoint
	EndpointID string `mapstructure:"endpoint_id" yaml:"endpoint_id,omitempty"`
}

// MagiEscalation controls the debate-escalation gate (design §5.3, §5.4).
type MagiEscalation struct {
	ConfidenceThreshold int `mapstructure:"confidence_threshold" yaml:"confidence_threshold"`
	MaxDebateRounds     int `mapstructure:"max_debate_rounds" yaml:"max_debate_rounds"`
}

// MagiConfig is the magi consensus provider settings (design §7).
type MagiConfig struct {
	Enabled                bool           `mapstructure:"enabled" yaml:"enabled"`
	Proposers              []MagiProposer `mapstructure:"proposers" yaml:"proposers"`
	Aggregator             MagiAggregator `mapstructure:"aggregator" yaml:"aggregator"`
	Escalation             MagiEscalation `mapstructure:"escalation" yaml:"escalation"`
	ProposerTimeoutSeconds int            `mapstructure:"proposer_timeout_seconds" yaml:"proposer_timeout_seconds"`
	Mode                   string         `mapstructure:"mode" yaml:"mode"` // advisory (Phase 1); plan/review reserved
}
```

추가로 다음 함수들을 구현한다:

```go
// ApplyMagiDefaults fills zero-value fields with defaults. Call after load.
func ApplyMagiDefaults(m *MagiConfig)
```
- `ConfidenceThreshold <= 0` → 7, `MaxDebateRounds < 0` → 1 (0은 "토론 안 함" 유효값이므로 음수만 보정), `ProposerTimeoutSeconds <= 0` → 120, `Mode == ""` → `"advisory"`, `Aggregator.Type == ""` → `"claude_cli"`.

```go
// GetMagiConfig loads the magi section from embedded.yaml with defaults applied.
// Returns nil when the section is absent (magi never configured).
func GetMagiConfig() (*MagiConfig, error)
```
- `LoadEmbeddedConfig()` 호출 → `cfg.Magi == nil`이면 `(nil, nil)` → 있으면 `ApplyMagiDefaults` 적용 후 반환.

```go
// SaveMagiConfig writes the magi section, preserving existing endpoints.
func SaveMagiConfig(m *MagiConfig) error
```
- **read-modify-write**: `LoadEmbeddedConfig()`로 전체를 읽고 `cfg.Magi = m`만 바꿔 `SaveEmbeddedConfig(cfg)` — endpoints를 날리면 안 된다.

```go
// ValidateMagiConfig returns hard errors (config unusable) and soft warnings
// (config works but is inadvisable). Pure function for testability.
func ValidateMagiConfig(cfg *EmbeddedConfig) (errs []string, warnings []string)
```
하드 에러 (모두 영문 메시지):
- proposer 수가 3이 아니면: `"magi requires exactly 3 proposers, got N"`
- proposer의 `endpoint_id`가 `cfg.Endpoints`에 없거나 `Enabled: false`면: `"proposer N: endpoint %q not found or disabled"`
- `Persona`가 `melchior|balthasar|casper|custom` 외 값: `"proposer N: unknown persona %q"`
- `persona: custom`인데 `CustomPrompt`가 비면: `"proposer N: custom persona requires custom_prompt"`
- `Aggregator.Type`이 `claude_cli|endpoint` 외 값: `"aggregator: unknown type %q"`
- `Aggregator.Type == "endpoint"`인데 `EndpointID`가 없거나 endpoints에서 못 찾으면: `"aggregator: endpoint %q not found or disabled"`

소프트 경고 (설계서 §2.2, §4):
- proposer 3개의 모델명(엔드포인트의 `Model`)이 모두 같으면: `"all proposers use the same model %q — consensus value degrades sharply with correlated errors (use different model families)"`
- proposer들의 `BaseURL`이 중복되면: `"proposers share base_url %q — requests will serialize on the same server"`
- persona가 중복되면: `"duplicate persona %q across proposers"`

### 3. `internal/config/magi_test.go` 생성

`ValidateMagiConfig`와 `ApplyMagiDefaults`는 순수 함수이므로 viper 초기화 없이 테스트 가능하다. 최소 케이스:

1. 정상 구성 (서로 다른 3 엔드포인트) → errs 0, warnings 0
2. proposer 2개 → 하드 에러
3. 없는 endpoint_id → 하드 에러
4. 동일 model 3개 → 경고 포함
5. base_url 중복 → 경고 포함
6. `UnmarshalEmbeddedYAML` 라운드트립: 설계서 §7 형식의 yaml 문자열(snake_case 키)을 파싱해 `Magi.Proposers[0].EndpointID`가 올바른지 + `MarshalEmbeddedYAML` 후 재파싱해도 같은지
7. magi 섹션 없는 기존 형식 yaml → `Magi == nil` (하위 호환)
8. `ApplyMagiDefaults`의 기본값 채움 (threshold 7, timeout 120, mode advisory, `MaxDebateRounds: 0` 유지 확인)

## 하지 말 것

- viper 키(`config.yaml`)에 magi 설정을 넣지 마라 — magi 설정의 저장소는 `embedded.yaml` 하나다.
- 기존 `EmbeddedEndpoint`/`UnmarshalEmbeddedYAML`/`SaveEmbeddedConfig` 수정 금지.
- 합의 로직·HTTP 호출·프로바이더 등록은 이 단계 범위 밖.

## 완료 검증

```bash
go build ./...
go test ./internal/config/
```
둘 다 성공해야 완료. 커밋 메시지: `feat(magi): add magi config schema to embedded.yaml (magi-tasks step-01)`
