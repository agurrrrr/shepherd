package agent

import (
	"time"
)

// Provider AI 에이전트 프로바이더 인터페이스
type Provider interface {
	// Execute 프로그래매틱 실행 (비대화형)
	Execute(workdir, prompt string, opts ExecuteOptions) (*Result, error)

	// ExecuteInteractive 대화형 실행 (스트리밍)
	ExecuteInteractive(workdir, sessionID, prompt string, opts InteractiveOptions) (*Result, error)

	// Name 프로바이더 이름 반환
	Name() string

	// IsAvailable 사용 가능 여부 확인
	IsAvailable() bool
}

// ExecuteOptions 실행 옵션
type ExecuteOptions struct {
	Timeout   time.Duration
	MaxTurns  int
	MCPConfig string
}

// InteractiveOptions 대화형 실행 옵션
type InteractiveOptions struct {
	Timeout  time.Duration
	OnOutput func(text string)
	OnInput  func(prompt string) (string, error)
}

// Result 실행 결과
type Result struct {
	Result        string
	SessionID     string
	FilesModified []string
	Output        []string // 스트리밍 출력 수집
	Error         string
}

// ProviderType 프로바이더 타입
type ProviderType string

const (
	ProviderClaude   ProviderType = "claude"
	ProviderOpencode ProviderType = "opencode"
	ProviderAuto     ProviderType = "auto"

	// Backward compatibility
	ProviderLocal = ProviderOpencode
)

// DefaultExecuteOptions 기본 실행 옵션
func DefaultExecuteOptions() ExecuteOptions {
	return ExecuteOptions{
		Timeout:  5 * time.Minute,
		MaxTurns: 0, // 무제한
	}
}

// DefaultInteractiveOptions 기본 대화형 옵션
func DefaultInteractiveOptions(onOutput func(string), onInput func(string) (string, error)) InteractiveOptions {
	return InteractiveOptions{
		Timeout:  30 * time.Minute,
		OnOutput: onOutput,
		OnInput:  onInput,
	}
}
