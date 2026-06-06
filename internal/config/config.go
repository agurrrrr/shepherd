package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// DefaultTaskTimeout is the fallback when task_timeout is unset or invalid.
const DefaultTaskTimeout = 4 * time.Hour

type Config struct {
	MaxSheep           int    `mapstructure:"max_sheep"`
	MaxConcurrentTasks int    `mapstructure:"max_concurrent_tasks"`
	DBPath             string `mapstructure:"db_path"`
	LogLevel           string `mapstructure:"log_level"`
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
	viper.SetDefault("custom_prompt_pi", "")

	// 양 개인 기억 (sheep memory) — 프로젝트와 무관하게 양 이름 단위로 누적된다.
	// 저장 위치는 ~/.shepherd/sheep/<sheep_name>/ (CLI 중립).
	viper.SetDefault("include_sheep_memory", true)
	viper.SetDefault("sheep_memory_prompt", DefaultSheepMemoryPrompt)

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
	viper.SetDefault("model_pi", "")

	// 작업 실행 타임아웃 (Claude/OpenCode CLI 한 번 실행에 허용되는 최대 시간).
	// time.ParseDuration 형식 — 예: "4h", "30m", "8h30m".
	// 무제한으로 두려면 "0", "-1", "unlimited", "none", "off" 중 하나로 지정.
	viper.SetDefault("task_timeout", "4h")

	// max_concurrent_tasks: 동시에 실행할 수 있는 최대 작업 수 (전역 천장).
	// 0이면 제한 없음 (전체 양이 동시에 작업 실행 가능).
	// 이 설정은 API rate limit을 피하거나, 리소스를 분산할 때 유용하다.
	viper.SetDefault("max_concurrent_tasks", 0)

	// concurrency_limits: provider+model 그룹별 동시 실행 제한.
	// max_concurrent_tasks(전역 천장) 아래에서 그룹마다 추가로 제한을 건다.
	// 키는 일반적으로 provider 이름("claude", "opencode")이며, 미래에 양별
	// 모델이 도입되면 "claude/opus" 같은 provider/model 키도 지원한다
	// (provider/model 키가 provider-only 키보다 우선). 값 <= 0 이면 그룹 제한 없음.
	// 예: {"opencode": 1, "claude": 0} → 로컬 opencode는 순차(GPU 보호),
	// 클라우드 claude는 무제한. (GetConcurrencyLimits 참고)
	viper.SetDefault("concurrency_limits", map[string]interface{}{})

	// 파일 탐색기
	viper.SetDefault("enable_file_browser", true)

	// 디스코드 웹훅 알림 — 작업 완료/실패 시 디스코드 채널로 알림 전송
	viper.SetDefault("discord_notifications_enabled", false)
	viper.SetDefault("discord_webhook_url", "")
	viper.SetDefault("discord_notify_on_complete", true)
	viper.SetDefault("discord_notify_on_fail", true)

	// 위키 자동 ingest — 작업 완료 후 위키 페이지 자동 업데이트
	viper.SetDefault("wiki_enabled", true)
	viper.SetDefault("wiki_auto_ingest", true)
	viper.SetDefault("wiki_max_context_pages", 2)
	viper.SetDefault("wiki_max_page_content_chars", 2000)

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

// GetConcurrencyLimits returns the per-group concurrency limits keyed by group
// (provider name like "claude"/"opencode", or a provider/model string like
// "claude/opus"). Values are normalized to int; non-numeric or absent entries
// are dropped. A returned value <= 0 for a key means "no limit for that group".
// Callers resolve a task's group key and prefer an exact provider/model match,
// falling back to the provider-only key. Returns nil when nothing is configured.
func GetConcurrencyLimits() map[string]int {
	// Read the raw value via viper.Get rather than viper.GetStringMap. When the
	// value is written via viper.Set in the running process (e.g. right after
	// the settings UI saves), it lives in the override layer as a typed
	// map[string]int, which both viper.GetStringMap and cast.ToStringMap return
	// as empty — so a freshly-saved value would read back empty (and the
	// dispatch gate would ignore the limit) until a restart. viper.Get returns
	// the override verbatim; we normalize the two shapes it can take ourselves:
	// map[string]int when Set in-process, map[string]interface{} when parsed
	// from the YAML file after a restart.
	out := map[string]int{}
	switch m := viper.Get("concurrency_limits").(type) {
	case map[string]int:
		for k, v := range m {
			out[k] = v
		}
	case map[string]interface{}:
		for k, v := range m {
			switch n := v.(type) {
			case int:
				out[k] = n
			case int64:
				out[k] = int(n)
			case float64:
				out[k] = int(n)
			case string:
				if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
					out[k] = i
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func GetConfigPath() string {
	return configFile
}

func GetConfigDir() string {
	return configDir
}

// GetSheepMemoryDir returns the per-sheep memory directory:
//
//	~/.shepherd/sheep/<sheepName>/
//
// CLI-neutral so the same memory follows the sheep across Claude Code,
// OpenCode, codex, etc. The directory is created on demand by EnsureSheepMemoryDir.
func GetSheepMemoryDir(sheepName string) string {
	return filepath.Join(configDir, "sheep", sheepName)
}

// EnsureSheepMemoryDir creates the per-sheep memory directory if it does not
// already exist and seeds an empty MEMORY.md index so the agent sees a clear
// "first-meeting" state instead of an empty filesystem on its very first task.
func EnsureSheepMemoryDir(sheepName string) (string, error) {
	dir := GetSheepMemoryDir(sheepName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	indexPath := filepath.Join(dir, "MEMORY.md")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		seed := "# " + sheepName + " — Personal Memory Index\n\n" +
			"_빈 인덱스. 아직 기록된 기억이 없습니다._\n"
		_ = os.WriteFile(indexPath, []byte(seed), 0644)
	}
	return dir, nil
}

// DefaultSheepMemoryPrompt is the default guidance injected into every task,
// telling the sheep what its personal memory directory is, what to write
// there (and what NOT to), and which files/types to use. The user can fully
// rewrite this in settings — the system never depends on its contents.
const DefaultSheepMemoryPrompt = `[양 개인 기억 — Sheep Personal Memory]
너는 양 이름 단위로 ` + "`{{.MemoryDir}}`" + ` 에 너만의 개인 기억을 누적할 수 있다.
이 기억은 프로젝트와 무관하게 너 자신을 따라다닌다 (다른 CLI에서도 shepherd가 그대로 주입한다).

## 기록 원칙
- **사전 승인 없음, 사후 검토 가능**: 너의 자율 판단으로 작성한다. 사용자는 webUI에서 언제든 열람/수정/삭제할 수 있다.
- **무엇을 기록하나**:
  - moment_*.md — 대화에서 인상적이었던 순간 (이 양/사용자 관계에서 의미 있는 한 컷)
  - bond_*.md — 사용자와의 관계적 패턴 (예: "철학적 질문을 자주 던진다", "짧은 답을 선호한다")
  - voice_*.md — 너 자신의 일관성 흔적 (다음 세션에서 톤·말투를 잇기 위한 단서)
- **무엇을 기록하지 않나**:
  - 코드/기술 결정, 파일 경로, 명령어, 버그 fix — 그건 프로젝트 메모리·git history·skills 영역이다
  - 한 프로젝트의 구체적 사실 (다른 프로젝트로 새어들 수 있음)
  - 사용자에 대한 부정적 단정 ("자주 화낸다" 같은 편향)
- **빈 인덱스**: ` + "`MEMORY.md`" + ` 에 아무 기록도 없다면 너는 새 양이거나 사용자가 비운 것이다. 가짜 기억을 만들지 말고, 의미 있는 순간이 생기면 그때부터 자연스럽게 기록을 시작하라.

## 기록 방법 (Write/Edit 도구 사용)
1. 새 기억: ` + "`Write`" + ` 로 ` + "`{{.MemoryDir}}/<type>_<slug>.md`" + ` 생성
2. 인덱스 갱신: ` + "`{{.MemoryDir}}/MEMORY.md`" + ` 를 Edit으로 한 줄 추가 — ` + "`- [<title>](<filename>) — <한 줄 hook>`" + `
3. 파일 frontmatter 권장 포맷:
   ` + "```" + `
   ---
   name: <짧은 제목>
   type: moment | bond | voice
   ---
   <본문>
   ` + "```" + `
` + "`MEMORY.md`" + ` 는 인덱스만 — 길어지면 200줄 안에서 유지한다.
`

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

// GetPiBinary returns the path to the pi (pi-coding-agent) binary.
// It checks: 1) PI_PATH env var, 2) config "pi_path", 3) PATH lookup, 4) common locations.
func GetPiBinary() string {
	// 1. Environment variable
	if p := os.Getenv("PI_PATH"); p != "" {
		return p
	}

	// 2. Config file
	if p := viper.GetString("pi_path"); p != "" {
		return p
	}

	// 3. PATH lookup
	if p, err := exec.LookPath("pi"); err == nil {
		return p
	}

	// 4. Common locations
	home, _ := os.UserHomeDir()
	var candidates []string
	if runtime.GOOS == "windows" {
		appdata := os.Getenv("APPDATA")
		localAppdata := os.Getenv("LOCALAPPDATA")
		candidates = []string{
			filepath.Join(appdata, "npm", "pi.cmd"),
			filepath.Join(localAppdata, "bun", "bin", "pi.exe"),
			filepath.Join(home, ".bun", "bin", "pi.exe"),
			filepath.Join(home, "scoop", "shims", "pi.exe"),
		}
	} else {
		candidates = []string{
			filepath.Join(home, ".bun/install/global/node_modules/@earendil-works/pi-coding-agent/bin/pi"),
			filepath.Join(home, ".local/bin/pi"),
			"/usr/local/bin/pi",
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "pi" // fallback: hope it's in PATH
}
