package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/viper"
)

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
