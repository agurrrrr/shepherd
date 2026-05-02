package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// DefaultTaskTimeout is the fallback when task_timeout is unset or invalid.
const DefaultTaskTimeout = 4 * time.Hour

type Config struct {
	MaxSheep int    `mapstructure:"max_sheep"`
	DBPath   string `mapstructure:"db_path"`
	LogLevel string `mapstructure:"log_level"`
}

var (
	configDir  string
	configFile string
)

func init() {
	home, _ := os.UserHomeDir()
	configDir = filepath.Join(home, ".shepherd")
	configFile = filepath.Join(configDir, "config.yaml")
}

func Init() error {
	// 설정 디렉토리 생성
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}

	// 기본값 설정
	viper.SetDefault("max_sheep", 12)
	viper.SetDefault("db_path", filepath.Join(configDir, "shepherd.db"))
	viper.SetDefault("log_level", "info")
	viper.SetDefault("auto_approve", true) // 자동 승인 모드 (기본: 켜짐)
	viper.SetDefault("language", "ko")
	viper.SetDefault("default_provider", "claude")
	viper.SetDefault("workspace_path", "")

	// 프롬프트 주입 설정
	viper.SetDefault("session_reuse", true)
	viper.SetDefault("include_task_history", true)
	viper.SetDefault("include_mcp_guide", true)
	viper.SetDefault("custom_prompt_claude", "")
	viper.SetDefault("custom_prompt_opencode", "")

	// OpenCode 프롬프트 단축 여부 — false면 Claude와 동일한 full 시스템 프롬프트 사용
	viper.SetDefault("opencode_compact_prompt", true)

	// OpenCode 'thinking' (reasoning) 모드 기본값. 토글 ON일 때 worker가
	// opencode_thinking_model을 -m 인자로 강제하고, 그 모델은 사용자가
	// opencode config에 만들어둔 thinking-routed provider entry를 가리키도록
	// 설정해야 한다 (baseURL = shepherd thinking proxy).
	viper.SetDefault("opencode_thinking_default", false)

	// OpenCode가 OpenAI 호환 어댑터에서 chat_template_kwargs 같은 비표준
	// body 필드를 떨어뜨려서, shepherd 데몬 안에 작은 reverse proxy를 띄워
	// 요청 body에 enable_thinking을 주입한 뒤 진짜 llama-server로 전달한다.
	// 사용자는 opencode config에 baseURL을 이 proxy(127.0.0.1:port)로 가리키는
	// thinking-routed provider entry를 한 번 만들고, opencode_thinking_model에
	// 그 provider/model 조합 (예: "qwen3.6-thinking/qwen3.6-27b")을 넣어둔다.
	viper.SetDefault("opencode_thinking_proxy_enabled", false)
	viper.SetDefault("opencode_thinking_proxy_port", 8686)
	viper.SetDefault("opencode_thinking_proxy_target", "")
	viper.SetDefault("opencode_thinking_model", "")

	// 전역 모델 선택 (빈 문자열이면 각 CLI의 기본 모델 사용)
	viper.SetDefault("model_claude", "")
	viper.SetDefault("model_opencode", "")

	// 작업 실행 타임아웃 (Claude/OpenCode CLI 한 번 실행에 허용되는 최대 시간).
	// time.ParseDuration 형식 — 예: "4h", "30m", "8h30m".
	// 무제한으로 두려면 "0", "-1", "unlimited", "none", "off" 중 하나로 지정.
	viper.SetDefault("task_timeout", "4h")

	// 파일 탐색기
	viper.SetDefault("enable_file_browser", true)

	// 서버 설정
	viper.SetDefault("server_port", 8585)
	viper.SetDefault("server_host", "0.0.0.0")

	// 인증 설정
	viper.SetDefault("auth_username", "")
	viper.SetDefault("auth_password_hash", "")
	viper.SetDefault("auth_jwt_secret", "")
	viper.SetDefault("auth_access_ttl", "24h")
	viper.SetDefault("auth_refresh_ttl", "168h")

	// 설정 파일 경로
	viper.SetConfigFile(configFile)
	viper.SetConfigType("yaml")

	// 설정 파일 읽기 (없으면 무시)
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			// 파일이 없는 것 외의 에러
			if !os.IsNotExist(err) {
				return err
			}
		}
	}

	return nil
}

func Get(key string) interface{} {
	return viper.Get(key)
}

func GetString(key string) string {
	return viper.GetString(key)
}

func GetInt(key string) int {
	return viper.GetInt(key)
}

func GetBool(key string) bool {
	return viper.GetBool(key)
}

func Set(key string, value interface{}) error {
	viper.Set(key, value)
	return Save()
}

func Save() error {
	return viper.WriteConfigAs(configFile)
}

func Load() (*Config, error) {
	var cfg Config
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetTaskTimeout returns the configured per-task execution timeout.
// A return value of 0 means unlimited — callers should use a context without a
// deadline (still cancellable). Sentinel strings for unlimited: "0", "-1",
// "unlimited", "none", "off", or any non-positive duration. Unparseable values
// fall back to DefaultTaskTimeout so a typo can't accidentally disable timeouts.
func GetTaskTimeout() time.Duration {
	raw := strings.TrimSpace(viper.GetString("task_timeout"))
	if raw == "" {
		return DefaultTaskTimeout
	}
	switch strings.ToLower(raw) {
	case "0", "-1", "unlimited", "none", "off":
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return DefaultTaskTimeout
	}
	if d <= 0 {
		return 0
	}
	return d
}

func GetConfigPath() string {
	return configFile
}

func GetConfigDir() string {
	return configDir
}

// GetOpenCodeBinary returns the path to the opencode binary.
// It checks: 1) OPENCODE_PATH env var, 2) config "opencode_path", 3) PATH lookup, 4) common locations.
func GetOpenCodeBinary() string {
	// 1. Environment variable
	if p := os.Getenv("OPENCODE_PATH"); p != "" {
		return p
	}

	// 2. Config file
	if p := viper.GetString("opencode_path"); p != "" {
		return p
	}

	// 3. PATH lookup
	if p, err := exec.LookPath("opencode"); err == nil {
		return p
	}

	// 4. Common locations
	home, _ := os.UserHomeDir()
	var candidates []string
	if runtime.GOOS == "windows" {
		appdata := os.Getenv("APPDATA")
		localAppdata := os.Getenv("LOCALAPPDATA")
		candidates = []string{
			filepath.Join(appdata, "npm", "opencode.cmd"),
			filepath.Join(localAppdata, "bun", "bin", "opencode.exe"),
			filepath.Join(home, ".bun", "bin", "opencode.exe"),
			filepath.Join(home, "scoop", "shims", "opencode.exe"),
		}
	} else {
		candidates = []string{
			filepath.Join(home, ".bun/install/global/node_modules/opencode-ai/bin/opencode"),
			filepath.Join(home, ".local/bin/opencode"),
			"/usr/local/bin/opencode",
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "opencode" // fallback: hope it's in PATH
}
