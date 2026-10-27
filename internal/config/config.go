package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// DefaultTaskTimeout is the fallback when task_timeout is unset or invalid.
const DefaultTaskTimeout = 4 * time.Hour

type Config struct {
	MaxSheep           int    `mapstructure:"max_sheep"`
	MaxConcurrentTasks int    `mapstructure:"max_concurrent_tasks"`
	DBPath             string `mapstructure:"db_path"`
	LogLevel           string `mapstructure:"log_level"`
	Shell              string `mapstructure:"shell"`
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

// UseTempConfigFile points every config write at path instead of the operator's
// ~/.shepherd/config.yaml. Tests install this before running (see
// internal/server/testmain_test.go): a handler that calls config.Set writes the
// whole viper state with WriteConfigAs, so a test that resets viper would
// otherwise truncate the real config file — auth credentials included.
func UseTempConfigFile(path string) {
	configFile = path
	viper.SetConfigFile(path)
}

// UseTempConfigDir points GetConfigDir — and everything derived from it, like
// daemon/runtime.json — at dir instead of ~/.shepherd, returning the previous
// value so the caller can restore it. Tests that must NOT reach whatever
// daemon happens to run on this machine install this (see the MCP forwarder
// tests): with an empty dir, daemon.ReadRuntime finds no runtime.json and the
// forwarder takes its deterministic "daemon is not running" path.
func UseTempConfigDir(dir string) string {
	prev := configDir
	configDir = dir
	return prev
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

	// 목자(shepherd) 기본 설정 (#593). 워크스페이스는 위 workspace_path를 그대로 쓴다.
	//   shepherd_model             : 목자 작업에 넣을 모델. 비어 있으면 프로바이더
	//                                기본 모델(model_<provider>)을 따른다. embedded는
	//                                엔드포인트 ID를 넣는다.
	//   shepherd_instructions      : 사용자가 덧붙이는 목자 운영 지침. 기본 지침(#595
	//                                예정) 뒤에 붙는다.
	//   shepherd_scope_exclude     : 목자가 관리하지 않을 프로젝트 이름 목록.
	//   shepherd_scope_include_hidden : 숨김 프로젝트(projects.hidden)도 관리할지.
	// 관리 범위는 "숨기지 않은 모든 프로젝트에서 제외 목록을 뺀 것"이다. 새 프로젝트가
	// 등록되면 자동으로 범위에 들어오게 하기 위한 정의다(계산 헬퍼는
	// manager.ScopeProjects).
	// 프로바이더는 키를 새로 만들지 않는다. 매니저 양 행의 sheep.provider가 단일 기준이다.
	viper.SetDefault("shepherd_model", "")
	viper.SetDefault("shepherd_instructions", "")
	viper.SetDefault("shepherd_scope_exclude", []string{})
	viper.SetDefault("shepherd_scope_include_hidden", false)

	// 목자 깨우기 한도 (#597). 0이나 음수는 manager/wake.go의 기본값으로 대체된다.
	//   shepherd_max_review_rounds  : 이슈 하나에 대해 목자가 검토할 수 있는 최대
	//                                 회차. 넘으면 더 깨우지 않고 확인 필요로 남긴다.
	//   shepherd_max_wakeups_per_day: 하루(자정 기준) 목자 review 작업 생성 상한.
	viper.SetDefault("shepherd_max_review_rounds", 3)
	viper.SetDefault("shepherd_max_wakeups_per_day", 30)

	// 확인 필요 항목 임계값 (#599). 0이나 음수는 manager/attention.go의 기본값으로
	// 대체된다.
	//   shepherd_stuck_minutes        : running 작업이 이 시간을 넘으면 오래 실행
	//                                   중인 것으로 본다 (기본 120분).
	//   shepherd_pending_stale_minutes: 프로젝트에서 가장 오래된 pending 작업이 이
	//                                   시간을 넘으면 적체로 본다 (기본 60분).
	viper.SetDefault("shepherd_stuck_minutes", 120)
	viper.SetDefault("shepherd_pending_stale_minutes", 60)

	// 주기 순찰 (#600). 코드로 관리 범위를 점검하고 직전 순찰 이후 새 이상이
	// 있을 때만 목자를 깨운다. 기록은 깨우기 여부와 무관하게 매 순찰 남긴다.
	//   shepherd_patrol_interval : 순찰 주기 ("30m"). "0"이면 끄고, 켜면 최소
	//                              5분으로 맞춘다 (manager/patrol.go).
	//   shepherd_patrol_wake     : 새 이상에서 목자를 깨울지. 끄면 기록만 한다.
	viper.SetDefault("shepherd_patrol_interval", "30m")
	viper.SetDefault("shepherd_patrol_wake", true)

	// 자동 권한 단계와 상한 (#601). 기본값은 2026-10-23 결정대로 "이슈 생성과
	// 실행까지는 자동, 작업 중지와 이슈 삭제는 사용자 확인"이다.
	//   shepherd_autonomy             : report(조회·보고만) / plan(이슈 생성·수정)
	//                                   / execute(생성·실행) / full(작업 중지).
	//                                   알 수 없는 값은 execute로 되돌린다 (manager/autonomy.go).
	//   shepherd_max_active_delegations: 감시 이슈 작업의 동시 pending+running 상한.
	//   shepherd_daily_cost_limit_usd : 하루 비용 상한 (USD). 0이면 제한하지 않는다.
	viper.SetDefault("shepherd_autonomy", "execute")
	viper.SetDefault("shepherd_max_active_delegations", 3)
	viper.SetDefault("shepherd_daily_cost_limit_usd", 0)

	// 목자 보고 알림 (#602). 목자 작업(review/patrol/command)의 완료 알림은
	// 일반 작업 완료 알림과 다른 제목("목자 보고")으로 나간다.
	//   shepherd_notify                        : 목자 보고 알림을 보낼지 (기본 켜짐).
	//   shepherd_notify_quiet_patrol           : 새 이상이 없던 순찰과, 이슈를 하나도
	//                                            바꾸지 않은 검토(판단 가능할 때만)는
	//                                            알리지 않는다 (기본 켜짐).
	//   shepherd_mute_delegated_notifications  : 감시 이슈 작업의 일반 완료 알림을
	//                                            끈다. 목자가 검토해서 보고하므로
	//                                            알림이 중복되기 때문 (기본 꺼짐).
	viper.SetDefault("shepherd_notify", true)
	viper.SetDefault("shepherd_notify_quiet_patrol", true)
	viper.SetDefault("shepherd_mute_delegated_notifications", false)

	// shell: 임베디드 프로바이더의 bash 도구가 명령을 실행할 셸 실행 파일.
	// 빈 값이면 OS별 자동 탐지 (Unix: bash → sh, Windows: Git Bash → pwsh →
	// powershell). 자동 탐지가 빗나갔을 때의 탈출구이므로 값은 실행 파일
	// 경로/이름 하나만 받는다 — 인자("-c" 등)는 셸 종류를 보고 자동으로 붙인다.
	viper.SetDefault("shell", "")

	// provider_enabled_*: 프로바이더 사용유무. false면 프로젝트 화면의 provider
	// 선택지에서 숨겨지고, 해당 프로바이더로의 작업 실행이 차단된다. 기본 모두 켜짐.
	viper.SetDefault("provider_enabled_claude", true)
	viper.SetDefault("provider_enabled_opencode", true)
	viper.SetDefault("provider_enabled_pi", true)
	viper.SetDefault("provider_enabled_grok", true)
	viper.SetDefault("provider_enabled_embedded", true)
	viper.SetDefault("provider_enabled_magi", true)

	// LLM sheep naming (#638). 켜면 프로젝트 등록 시 담당 양 이름을 LLM이 짓는다.
	//   sheep_name_llm_enabled  : 스위치. 꺼지면 이름 풀(names.GetNext)만 쓴다.
	//   sheep_name_llm_provider : claude/opencode/pi/grok/embedded 중 하나.
	//                             magi는 단일 모델이 없고 auto는 실행 시 claude로
	//                             바뀌므로 선택지가 아니다.
	//   sheep_name_llm_model    : 비우면 model_<provider> 기본값. embedded는
	//                             엔드포인트 ID이고, 비우면 embedded_active_id를 쓴다.
	viper.SetDefault("sheep_name_llm_enabled", false)
	viper.SetDefault("sheep_name_llm_provider", "")
	viper.SetDefault("sheep_name_llm_model", "")

	// 프롬프트 주입 설정
	viper.SetDefault("session_reuse", true)
	viper.SetDefault("include_task_history", true)
	viper.SetDefault("include_mcp_guide", true)
	viper.SetDefault("custom_prompt_claude", "")
	viper.SetDefault("custom_prompt_opencode", "")
	viper.SetDefault("custom_prompt_pi", "")
	viper.SetDefault("custom_prompt_grok", "")

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
	viper.SetDefault("model_grok", "")
	viper.SetDefault("grok_path", "") // empty = auto-detect (GROK_PATH env, PATH, common locations)

	// 임베디드 (in-process 로컬 LLM) 프로바이더
	viper.SetDefault("embedded_active_id", "")
	// tool/bash 결과 SSE·히스토리 저장 직전 시크릿 redaction (기본 on).
	// false로 끄면 원문 그대로 흘린다 — 디버깅용. 프로덕션에서는 끄지 말 것.
	viper.SetDefault("embedded_redact_secrets", true)
	// todo_write 도구 + 미완료 todo turn-end nudge (Phase 3-2). 기본 off —
	// 기존 정규식 false-completion 가드와 병행 도입·A/B 검증용. on일 때만
	// 도구 노출·nudge 동작. 가드 3종을 대체하지 않는다.
	viper.SetDefault("embedded_todo_gate", false)

	// 작업 실행 타임아웃 (Claude/OpenCode CLI 한 번 실행에 허용되는 최대 시간).
	// time.ParseDuration 형식 — 예: "4h", "30m", "8h30m".
	// 무제한으로 두려면 "0", "-1", "unlimited", "none", "off" 중 하나로 지정.
	viper.SetDefault("task_timeout", "4h")

	// grok 헤드리스(-p) 실행이 턴 종료 후 백그라운드 작업(bash/monitor, 백그라운드
	// subagent)을 기다리는 최대 시간. grok 자체 기본값은 10분이고, 시간이 다 되면
	// grok이 백그라운드 작업을 멈추고 exit 1로 끝나 long-running 작업이 실패한다 (#619).
	// time.ParseDuration 형식 — 예: "60m", "90s".
	// "off"/"0"/"disable"이면 --no-wait-for-background를 넘겨 첫 턴이 끝나자마자 종료한다.
	// "unlimited"/"none"/"-1"은 task_timeout 직전까지 기다린다 — task_timeout이
	// 설정돼 있으면 그보다 여유를 두고 짧게, task_timeout도 무제한이면 큰 고정값(24h)을
	// --background-wait-timeout으로 넘긴다. 이 키워드는 task_timeout의 무제한 관례와
	// 같다 (#625).
	// 비어 있으면 기본 60분을 쓰되, task_timeout이 설정돼 있으면 그보다 여유를 두고
	// 짧게 줄인다 — 백그라운드 대기 한도가 작업 타임아웃과 충돌하면 grok이 정리할
	// 기회 전에 데몬이 프로세스를 죽인다. GetGrokBackgroundWait 참고.
	viper.SetDefault("grok_background_wait", "")

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

	// PWA Web Push — 설치한 기기에서 작업 완료/실패 푸시. VAPID 키는
	// 첫 구독/상태 조회 때 생성되어 config.yaml 에 저장된다.
	viper.SetDefault("webpush_enabled", true)
	viper.SetDefault("webpush_notify_on_complete", true)
	viper.SetDefault("webpush_notify_on_fail", true)
	viper.SetDefault("webpush_vapid_public", "")
	viper.SetDefault("webpush_vapid_private", "")
	viper.SetDefault("webpush_vapid_subject", "")

	// 작업 체크포인트 — 작업 시작/종료 시 작업 트리를 숨은 git ref로 스냅샷
	viper.SetDefault("task_checkpoint_enabled", true)
	// 체크포인트 ref 보존 기간(일). 0이면 자동 정리를 하지 않는다.
	viper.SetDefault("task_checkpoint_retention_days", 30)

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

// GetFloat reads a float setting (e.g. shepherd_daily_cost_limit_usd).
func GetFloat(key string) float64 {
	return viper.GetFloat64(key)
}

func Set(key string, value interface{}) error {
	viper.Set(key, value)
	return Save()
}

// SetMany sets several keys and writes the config file once, so related
// values (e.g. login credentials) never land on disk half-updated.
func SetMany(values map[string]interface{}) error {
	for key, value := range values {
		viper.Set(key, value)
	}
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

// DefaultGrokBackgroundWait is the fallback for grok_background_wait when the
// key is empty or unparseable. grok's own built-in limit is only 10 minutes,
// which kills any task whose background work (a build, a model download) runs
// longer than that — grok stops the work and exits 1 (#619). 60 minutes covers
// typical long builds while still bounding a stuck monitor(persistent:true).
const DefaultGrokBackgroundWait = 60 * time.Minute

// grokTaskTimeoutHeadroom is how much shorter the grok background wait is kept
// than task_timeout. Without headroom the two deadlines collide: the daemon
// SIGKILLs the process group at task_timeout before grok can stop its
// background work and exit cleanly with its own message.
const grokTaskTimeoutHeadroom = 5 * time.Minute

// grokBackgroundWaitFloor keeps a task_timeout-derived cap usable: a 1m
// task_timeout must not produce a 0s (or negative) background wait.
const grokBackgroundWaitFloor = time.Minute

// GrokBackgroundWaitOff is the special GetGrokBackgroundWait return value
// meaning "do not wait at all" — callers pass --no-wait-for-background.
const GrokBackgroundWaitOff = time.Duration(-1)

// MaxGrokBackgroundWait is what "unlimited"/"none"/"-1" resolves to when
// task_timeout is itself unlimited: a large finite bound we can hand to
// --background-wait-timeout. grok's flag has no "wait forever" value, and
// omitting it falls back to grok's own 10-minute cap — so an explicit large
// number is required. 24h is far above any real task yet stays inside int64
// seconds. When task_timeout is finite, unlimited resolves to just under it
// instead (see GetGrokBackgroundWait).
const MaxGrokBackgroundWait = 24 * time.Hour

// GetGrokBackgroundWait returns how long a headless grok run should wait for
// the background work it started after its latest turn ends, translated into the
// `--background-wait-timeout <secs>` / `--no-wait-for-background` pair.
//
//   - "off", "0", "disable"/"disabled" → GrokBackgroundWaitOff: grok exits as
//     soon as the first turn ends and stopped background work does not affect
//     the exit code (--no-wait-for-background).
//   - "unlimited", "none", "-1" → wait as long as the task may run: just under
//     task_timeout when it is finite (same headroom cap as below), or
//     MaxGrokBackgroundWait when task_timeout is itself unlimited. This matches
//     task_timeout's own "unlimited"/"none"/"-1" convention (#625); the earlier
//     reading of these as "no wait" was inverted.
//   - a time.ParseDuration value ("60m", "90s") → that duration.
//   - empty or unparseable → DefaultGrokBackgroundWait (a typo must not silently
//     drop us back to grok's 10-minute default, mirroring GetTaskTimeout).
//
// A positive result is always capped below task_timeout (see
// grokTaskTimeoutHeadroom) so grok's own background-wait deadline fires before
// the daemon's per-task deadline. task_timeout of "0"/"off" means unlimited, so
// no cap applies. Callers treat GrokBackgroundWaitOff (<= 0) results as "no
// wait"; a max-wait result stays positive so it becomes --background-wait-timeout.
func GetGrokBackgroundWait() time.Duration {
	raw := strings.TrimSpace(viper.GetString("grok_background_wait"))
	normalized := strings.ToLower(raw)

	// "No wait at all": grok drops background work as soon as the turn ends.
	switch normalized {
	case "off", "0", "disable", "disabled":
		return GrokBackgroundWaitOff
	}

	// "Wait as long as the task may run" — same unlimited convention as
	// task_timeout (#625). Resolve to just under task_timeout, falling back to a
	// large fixed bound when task_timeout is unlimited. --background-wait-timeout
	// has no infinite value, so this must stay positive (never omitted).
	if normalized == "unlimited" || normalized == "none" || normalized == "-1" {
		return grokBackgroundWaitCap()
	}

	wait := DefaultGrokBackgroundWait
	if raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			if d <= 0 {
				return GrokBackgroundWaitOff
			}
			wait = d
		}
	}

	if taskTimeout := GetTaskTimeout(); taskTimeout > 0 && wait >= taskTimeout {
		return grokBackgroundWaitCap()
	}
	return wait
}

// grokBackgroundWaitCap returns the "wait just under the task deadline" value:
// task_timeout minus grokTaskTimeoutHeadroom, floored at grokBackgroundWaitFloor
// so a tiny task_timeout still yields a usable wait. When task_timeout is
// unlimited it returns MaxGrokBackgroundWait instead.
func grokBackgroundWaitCap() time.Duration {
	taskTimeout := GetTaskTimeout()
	if taskTimeout <= 0 {
		return MaxGrokBackgroundWait
	}
	if capped := taskTimeout - grokTaskTimeoutHeadroom; capped > grokBackgroundWaitFloor {
		return capped
	}
	return grokBackgroundWaitFloor
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

// IsProviderEnabled reports whether the given provider is enabled for use.
// Disabled providers are hidden from project provider selectors and blocked
// from task execution. The runtime "auto" provider folds into Claude. Unknown
// providers default to enabled (fail-open) so a typo can't silently block work.
func IsProviderEnabled(provider string) bool {
	p := provider
	if p == "auto" {
		p = "claude"
	}
	switch p {
	// grok was missing from this list until #593: the settings page rendered an
	// Off badge for it and PATCH /api/config stored provider_enabled_grok=false,
	// yet execution never checked the flag — so "disabled" was cosmetic.
	case "claude", "opencode", "pi", "grok", "embedded", "magi":
		return GetBool("provider_enabled_" + p)
	default:
		return true
	}
}

func GetConfigPath() string {
	return configFile
}

// ValidateWorkspacePath normalizes a shepherd workspace path and checks that it
// exists as a directory. It expands a leading "~" (and a bare "~"), makes the
// path absolute (filepath.Abs), strips trailing separators (filepath.Clean),
// and returns the normalized path. The caller should store the returned value,
// not the raw input, so `~/code` and `/home/x/code/` all persist identically.
//
// "/" and the user's home directory are allowed but noteworthy: this daemon's
// `home` project already points at the home directory, so rejecting them would
// break existing setups. Callers may warn about them in the UI.
//
// An empty path is valid — it means "not set yet". The shepherd settings UI
// shows a warning for that state instead of an error.
func ValidateWorkspacePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}

	p = expandHome(p)

	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("invalid workspace path: %w", err)
	}
	abs = filepath.Clean(abs)

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("workspace path does not exist: %s", abs)
		}
		return "", fmt.Errorf("cannot check workspace path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace path is not a directory: %s", abs)
	}

	return abs, nil
}

// expandHome expands a leading "~" or "~/..." to the user's home directory.
// A "~user" form is left alone — we do not resolve other users' homes.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~/"))
}

// GetSheepScopeExclude returns the list of project names the shepherd manager
// must NOT manage. Empty names are dropped so a stray blank entry can't match.
func GetSheepScopeExclude() []string {
	raw := viper.GetStringSlice("shepherd_scope_exclude")
	out := make([]string, 0, len(raw))
	for _, name := range raw {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}
	return out
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

// GetShell returns the shell executable that the embedded provider's bash tool
// should run commands with, or "" to let the platform auto-detect one.
//
// It checks: 1) SHEPHERD_SHELL env var, 2) config "shell". Unlike the CLI
// binary getters below there is no PATH lookup or candidate list here — shell
// discovery is OS-specific and lives in internal/embedded (shell_unix.go /
// shell_windows.go). This is only the user's override.
//
// The value is a single executable path or name ("bash", "pwsh",
// `C:\Program Files\Git\bin\bash.exe`); arguments are appended by the caller
// based on the shell's dialect, so a value like "pwsh -NoProfile" is rejected.
func GetShell() string {
	if s := os.Getenv("SHEPHERD_SHELL"); s != "" {
		return s
	}
	return viper.GetString("shell")
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

// GetGrokBinary returns the path to the grok binary.
// It checks: 1) GROK_PATH env var, 2) config "grok_path", 3) PATH
// lookup, 4) common locations (~/.grok/bin/grok, ~/.local/bin/grok).
// Leave grok_path empty to auto-detect. Point it at the real grok
// binary only — interactive wrappers that wait for Enter leak into
// live output and can stall headless runs.
func GetGrokBinary() string {
	// 1. Environment variable
	if p := os.Getenv("GROK_PATH"); p != "" {
		return p
	}

	// 2. Config file
	if p := viper.GetString("grok_path"); p != "" {
		return p
	}

	// 3. PATH lookup
	if p, err := exec.LookPath("grok"); err == nil {
		return p
	}

	// 4. Common locations
	home, _ := os.UserHomeDir()
	var candidates []string
	if runtime.GOOS == "windows" {
		localAppdata := os.Getenv("LOCALAPPDATA")
		candidates = []string{
			filepath.Join(localAppdata, "Programs", "grok", "grok.exe"),
			filepath.Join(home, ".grok", "bin", "grok.exe"),
			filepath.Join(home, "scoop", "shims", "grok.exe"),
		}
	} else {
		candidates = []string{
			filepath.Join(home, ".grok/bin/grok"),
			filepath.Join(home, ".local/bin/grok"),
			"/usr/local/bin/grok",
		}
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	return "grok" // fallback: hope it's in PATH
}

// EmbeddedConfigFile returns the path to the embedded endpoints config file.
func EmbeddedConfigFile() string {
	return filepath.Join(configDir, "embedded.yaml")
}

// LoadEmbeddedConfig loads the embedded endpoint configuration from
// ~/.shepherd/embedded.yaml. Returns an empty config when the file does not
// exist (meaning no embedded endpoints are configured yet).
func LoadEmbeddedConfig() (*EmbeddedConfig, error) {
	path := EmbeddedConfigFile()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &EmbeddedConfig{Endpoints: []EmbeddedEndpoint{}}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read embedded config: %w", err)
	}

	cfg, err := UnmarshalEmbeddedYAML(data)
	if err != nil {
		return nil, fmt.Errorf("parse embedded config: %w", err)
	}
	// Older embedded.yaml files predate the subagent field. Bool zero-value
	// would silently disable every endpoint for spawn_subagents; treat a file
	// that never mentions the key as "all subagent-capable" (BC).
	applySubagentDefaultMigration(cfg, data)
	return cfg, nil
}

// applySubagentDefaultMigration sets Subagent=true on every endpoint when the
// raw YAML never declares a "subagent" key. Once any save writes the field,
// migration no longer applies and explicit false values are respected.
func applySubagentDefaultMigration(cfg *EmbeddedConfig, raw []byte) {
	if cfg == nil || len(cfg.Endpoints) == 0 {
		return
	}
	if embeddedYAMLHasSubagentKey(raw) {
		return
	}
	for i := range cfg.Endpoints {
		cfg.Endpoints[i].Subagent = true
	}
}

// embeddedYAMLHasSubagentKey reports whether raw YAML declares a subagent
// field (with or without underscore). Case-insensitive; ignores values.
func embeddedYAMLHasSubagentKey(raw []byte) bool {
	lower := strings.ToLower(string(raw))
	return strings.Contains(lower, "subagent:") || strings.Contains(lower, "subagent :")
}

// SaveEmbeddedConfig writes the embedded endpoint configuration to disk.
func SaveEmbeddedConfig(cfg *EmbeddedConfig) error {
	data, err := MarshalEmbeddedYAML(cfg)
	if err != nil {
		return fmt.Errorf("marshal embedded config: %w", err)
	}
	return os.WriteFile(EmbeddedConfigFile(), data, 0644)
}

// EmbeddedEndpoint is a single LLM endpoint entry.
type EmbeddedEndpoint struct {
	ID       string `mapstructure:"id"`
	Label    string `mapstructure:"label"`
	BaseURL  string `mapstructure:"base_url"`
	APIKey   string `mapstructure:"api_key"`
	Model    string `mapstructure:"model"`
	Enabled  bool   `mapstructure:"enabled"`
	Thinking bool   `mapstructure:"thinking"`
	// Vision marks the endpoint's model as vision-capable. When true, the
	// embedded agent surfaces image files (e.g. runtime screenshots) to the
	// model as real images via read_file, independent of whether the task had
	// attached files. Older configs lack this field → defaults to false.
	Vision        bool `mapstructure:"vision"`
	MaxIterations int  `mapstructure:"max_iterations"`
	ContextTokens int  `mapstructure:"context_tokens"`
	// MaxConcurrent limits simultaneous LLM calls per endpoint (0 = unlimited).
	// Corresponds to llama.cpp --parallel slot count.
	MaxConcurrent int `mapstructure:"max_concurrent"`
	// Subagent controls whether spawn_subagents may target this endpoint.
	// Only Enabled && Subagent endpoints appear in the subagent catalog and
	// resolve for spawn_subagents endpoint_id. Parent tasks still use any
	// enabled endpoint. Pre-field configs (YAML without "subagent:") are
	// migrated to true on load for backward compatibility.
	Subagent bool `mapstructure:"subagent"`
	// ReasoningBudgetTokens, when > 0, is sent as reasoning_budget_tokens on
	// embedded agent-loop requests (clamped to max_tokens/2). 0 = not sent.
	// Only for servers that accept the field (Strata); others may reject it.
	ReasoningBudgetTokens int `mapstructure:"reasoning_budget_tokens"`
	// HandoffNoThinking sends chat_template_kwargs {"enable_thinking": false}
	// on the context-handoff summary request so reasoning cannot eat it.
	HandoffNoThinking bool `mapstructure:"handoff_no_thinking"`
	// Auth selects the authentication mode. Empty means the static APIKey is
	// sent as a Bearer token (the historical behavior). "copilot" swaps the
	// static key for a short-lived GitHub Copilot API token derived from
	// GitHubToken, refreshing it before expiry.
	Auth string `mapstructure:"auth"`
	// GitHubToken is the GitHub OAuth/PAT token used to exchange a Copilot API
	// token. Only meaningful when Auth == "copilot". Stored in embedded.yaml
	// like APIKey and masked in API responses.
	GitHubToken string `mapstructure:"github_token"`
}

// Embedded auth modes.
const (
	// EmbeddedAuthCopilot makes the embedded client exchange GitHubToken for a
	// short-lived Copilot API token and send the Copilot-specific headers.
	EmbeddedAuthCopilot = "copilot"
)

// EmbeddedConfig is the top-level config for embedded endpoints.
type EmbeddedConfig struct {
	Endpoints []EmbeddedEndpoint `mapstructure:"endpoints"`
	Magi      *MagiConfig        `mapstructure:"magi" yaml:"magi,omitempty"`
}

// UnmarshalEmbeddedYAML parses embedded config from YAML data.
func UnmarshalEmbeddedYAML(data []byte) (*EmbeddedConfig, error) {
	var cfg EmbeddedConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// MarshalEmbeddedYAML serializes embedded config to YAML.
func MarshalEmbeddedYAML(cfg *EmbeddedConfig) ([]byte, error) {
	return yaml.Marshal(cfg)
}

// EmbeddedEndpointJSON is a JSON-friendly representation of an embedded endpoint
// for API responses (avoids yaml-specific types).
type EmbeddedEndpointJSON struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	BaseURL       string `json:"base_url"`
	APIKey        string `json:"api_key"`
	Model         string `json:"model"`
	Enabled       bool   `json:"enabled"`
	Thinking      bool   `json:"thinking"`
	Vision        bool   `json:"vision"`
	MaxIterations int    `json:"max_iterations"`
	ContextTokens int    `json:"context_tokens"`
	MaxConcurrent int    `json:"max_concurrent"`
	Subagent      bool   `json:"subagent"`
	// Every field must also be copied in EndpointsToJSON / EndpointsFromJSON
	// and the server's embeddedEndpointFromJSON, or a Web UI save drops it.
	ReasoningBudgetTokens int    `json:"reasoning_budget_tokens"`
	HandoffNoThinking     bool   `json:"handoff_no_thinking"`
	Auth                  string `json:"auth"`
	GitHubToken           string `json:"github_token"`
}

// EndpointsToJSON converts embedded endpoints to JSON-friendly slice.
func EndpointsToJSON(endpoints []EmbeddedEndpoint) []EmbeddedEndpointJSON {
	result := make([]EmbeddedEndpointJSON, len(endpoints))
	for i, ep := range endpoints {
		result[i] = EmbeddedEndpointJSON{
			ID:            ep.ID,
			Label:         ep.Label,
			BaseURL:       ep.BaseURL,
			APIKey:        ep.APIKey,
			Model:         ep.Model,
			Enabled:       ep.Enabled,
			Thinking:      ep.Thinking,
			Vision:        ep.Vision,
			MaxIterations: ep.MaxIterations,
			ContextTokens: ep.ContextTokens,
			MaxConcurrent: ep.MaxConcurrent,
			Subagent:      ep.Subagent,

			ReasoningBudgetTokens: ep.ReasoningBudgetTokens,
			HandoffNoThinking:     ep.HandoffNoThinking,
			Auth:                  ep.Auth,
			GitHubToken:           ep.GitHubToken,
		}
	}
	return result
}

// EndpointsFromJSON converts JSON endpoints back to embedded endpoints.
func EndpointsFromJSON(jsonEps []EmbeddedEndpointJSON) []EmbeddedEndpoint {
	result := make([]EmbeddedEndpoint, len(jsonEps))
	for i, ep := range jsonEps {
		result[i] = EmbeddedEndpoint{
			ID:            ep.ID,
			Label:         ep.Label,
			BaseURL:       ep.BaseURL,
			APIKey:        ep.APIKey,
			Model:         ep.Model,
			Enabled:       ep.Enabled,
			Thinking:      ep.Thinking,
			Vision:        ep.Vision,
			MaxIterations: ep.MaxIterations,
			ContextTokens: ep.ContextTokens,
			MaxConcurrent: ep.MaxConcurrent,
			Subagent:      ep.Subagent,

			ReasoningBudgetTokens: ep.ReasoningBudgetTokens,
			HandoffNoThinking:     ep.HandoffNoThinking,
			Auth:                  ep.Auth,
			GitHubToken:           ep.GitHubToken,
		}
	}
	return result
}

// GetEmbeddedEndpointByID returns the embedded endpoint with the given ID,
// or nil when it does not exist or is disabled. Exact id match only — for
// agent-facing refs that may use label/model, use ResolveEmbeddedEndpoint.
func GetEmbeddedEndpointByID(id string) (*EmbeddedEndpoint, error) {
	if id == "" {
		return nil, nil
	}

	cfg, err := LoadEmbeddedConfig()
	if err != nil {
		return nil, err
	}

	for _, ep := range cfg.Endpoints {
		if ep.ID == id && ep.Enabled {
			return &ep, nil
		}
	}

	return nil, nil
}

// ResolveEmbeddedEndpoint resolves an agent-facing endpoint reference.
// Match order among enabled endpoints:
//  1. exact id
//  2. unique label (case-sensitive)
//  3. unique model name
//
// Ambiguous label/model matches are treated as not found (return nil) so the
// caller surfaces FormatUnknownEndpointError with the full id catalog.
// Returns (nil, nil) when ref is empty or no unique match exists.
func ResolveEmbeddedEndpoint(ref string) (*EmbeddedEndpoint, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	eps, err := ListEnabledEndpoints()
	if err != nil {
		return nil, err
	}
	return resolveFromEnabled(ref, eps), nil
}

// resolveFromEnabled is the pure matcher used by ResolveEmbeddedEndpoint and tests.
func resolveFromEnabled(ref string, eps []EmbeddedEndpoint) *EmbeddedEndpoint {
	if ref == "" {
		return nil
	}
	// 1) exact id
	for i := range eps {
		if eps[i].ID == ref {
			ep := eps[i]
			return &ep
		}
	}
	// 2) unique label
	var labelHit *EmbeddedEndpoint
	for i := range eps {
		label := eps[i].Label
		if label == "" {
			continue
		}
		if label != ref {
			continue
		}
		if labelHit != nil {
			labelHit = nil // ambiguous
			break
		}
		ep := eps[i]
		labelHit = &ep
	}
	if labelHit != nil {
		return labelHit
	}
	// 3) unique model
	var modelHit *EmbeddedEndpoint
	for i := range eps {
		if eps[i].Model != ref {
			continue
		}
		if modelHit != nil {
			return nil // ambiguous
		}
		ep := eps[i]
		modelHit = &ep
	}
	return modelHit
}

// ListEnabledEndpoints returns a copy of all enabled embedded endpoints.
// Disabled entries are omitted. Secrets (APIKey) are present on the struct —
// callers that surface this to models/CLI must not print APIKey.
func ListEnabledEndpoints() ([]EmbeddedEndpoint, error) {
	cfg, err := LoadEmbeddedConfig()
	if err != nil {
		return nil, err
	}
	out := make([]EmbeddedEndpoint, 0, len(cfg.Endpoints))
	for _, ep := range cfg.Endpoints {
		if ep.Enabled {
			out = append(out, ep)
		}
	}
	return out, nil
}

// EnabledEndpointIDs returns the IDs of all enabled embedded endpoints.
func EnabledEndpointIDs() ([]string, error) {
	eps, err := ListEnabledEndpoints()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(eps))
	for _, ep := range eps {
		ids = append(ids, ep.ID)
	}
	return ids, nil
}

// ListSubagentEndpoints returns enabled endpoints that allow spawn_subagents
// targeting (Enabled && Subagent). Only embedded endpoints can be subagents;
// this list is the sole catalog for endpoint_id selection.
func ListSubagentEndpoints() ([]EmbeddedEndpoint, error) {
	cfg, err := LoadEmbeddedConfig()
	if err != nil {
		return nil, err
	}
	out := make([]EmbeddedEndpoint, 0, len(cfg.Endpoints))
	for _, ep := range cfg.Endpoints {
		if ep.Enabled && ep.Subagent {
			out = append(out, ep)
		}
	}
	return out, nil
}

// SubagentEndpointIDs returns IDs of endpoints allowed as spawn_subagents targets.
func SubagentEndpointIDs() ([]string, error) {
	eps, err := ListSubagentEndpoints()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(eps))
	for _, ep := range eps {
		ids = append(ids, ep.ID)
	}
	return ids, nil
}

// ResolveSubagentEndpoint resolves endpoint_id for spawn_subagents among
// Enabled && Subagent endpoints only (same match order as ResolveEmbeddedEndpoint:
// exact id → unique label → unique model).
// Returns (nil, nil) when ref is empty or no unique match among subagent-capable
// endpoints. An endpoint that exists but has subagent:false is treated as not found
// for this path (agents should retry with a listed id).
func ResolveSubagentEndpoint(ref string) (*EmbeddedEndpoint, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	eps, err := ListSubagentEndpoints()
	if err != nil {
		return nil, err
	}
	return resolveFromEnabled(ref, eps), nil
}

// FormatEndpointCatalog formats endpoints for agent prompts / error messages.
// It never includes API keys or base URLs (base URLs can leak LAN topology;
// IDs/labels/models are enough for spawn_subagents endpoint_id selection).
//
// Example line:
//
//   - id="agents-a1-4b" label="agents-a1-4b" model="agents-a1-4b" max_concurrent=8
func FormatEndpointCatalog(endpoints []EmbeddedEndpoint) string {
	if len(endpoints) == 0 {
		return "(no enabled endpoints)"
	}
	var sb strings.Builder
	for i, ep := range endpoints {
		if i > 0 {
			sb.WriteByte('\n')
		}
		label := ep.Label
		if label == "" {
			label = ep.ID
		}
		fmt.Fprintf(&sb, "- id=%q label=%q model=%q max_concurrent=%d",
			ep.ID, label, ep.Model, ep.MaxConcurrent)
	}
	return sb.String()
}

// FormatEnabledEndpointCatalog loads enabled endpoints and formats them.
// On load error it returns a short diagnostic string (never panics).
// Prefer FormatSubagentEndpointCatalog for spawn_subagents prompts.
func FormatEnabledEndpointCatalog() string {
	eps, err := ListEnabledEndpoints()
	if err != nil {
		return fmt.Sprintf("(failed to load endpoints: %v)", err)
	}
	return FormatEndpointCatalog(eps)
}

// FormatSubagentEndpointCatalog loads subagent-capable endpoints and formats them.
// On load error it returns a short diagnostic string (never panics).
func FormatSubagentEndpointCatalog() string {
	eps, err := ListSubagentEndpoints()
	if err != nil {
		return fmt.Sprintf("(failed to load endpoints: %v)", err)
	}
	if len(eps) == 0 {
		return "(no subagent-capable endpoints; enable Subagent on an embedded endpoint in Settings → Embedded)"
	}
	return FormatEndpointCatalog(eps)
}

// FormatUnknownEndpointError builds a clear error when endpoint_id resolution fails.
// Includes available IDs so agents can retry without inventing systemd names/ports.
func FormatUnknownEndpointError(endpointID string) string {
	ids, err := EnabledEndpointIDs()
	if err != nil {
		return FormatUnknownEndpointErrorWithIDs(endpointID, nil) +
			fmt.Sprintf(" (could not list available: %v)", err)
	}
	return FormatUnknownEndpointErrorWithIDs(endpointID, ids)
}

// FormatUnknownEndpointErrorWithIDs is the pure form of FormatUnknownEndpointError
// (testable without loading embedded.yaml).
func FormatUnknownEndpointErrorWithIDs(endpointID string, ids []string) string {
	if len(ids) == 0 {
		return fmt.Sprintf("embedded endpoint %q not found (no enabled endpoints configured in embedded.yaml; run: shepherd endpoints list)",
			endpointID)
	}
	return fmt.Sprintf("embedded endpoint %q not found; available endpoint ids: %s (use exact id from shepherd endpoints list / embedded.yaml, not systemd unit/port; label/model accepted only when unique)",
		endpointID, strings.Join(ids, ", "))
}

// FormatUnknownSubagentEndpointError builds a clear error when spawn_subagents
// endpoint_id resolution fails among subagent-capable endpoints only.
func FormatUnknownSubagentEndpointError(endpointID string) string {
	ids, err := SubagentEndpointIDs()
	if err != nil {
		return FormatUnknownSubagentEndpointErrorWithIDs(endpointID, nil) +
			fmt.Sprintf(" (could not list available: %v)", err)
	}
	return FormatUnknownSubagentEndpointErrorWithIDs(endpointID, ids)
}

// FormatUnknownSubagentEndpointErrorWithIDs is the pure form of
// FormatUnknownSubagentEndpointError (testable without loading embedded.yaml).
func FormatUnknownSubagentEndpointErrorWithIDs(endpointID string, ids []string) string {
	if len(ids) == 0 {
		return fmt.Sprintf("subagent endpoint %q not found (no subagent-capable endpoints; enable Subagent on an embedded endpoint in Settings → Embedded, then run: shepherd endpoints list)",
			endpointID)
	}
	return fmt.Sprintf("subagent endpoint %q not found or not subagent-enabled; available subagent endpoint ids: %s (use exact id from shepherd endpoints list; only endpoints with subagent:true are allowed; label/model accepted only when unique)",
		endpointID, strings.Join(ids, ", "))
}

// GetActiveEmbeddedEndpoint returns the currently active embedded endpoint,
// or nil when the active ID is not set or the endpoint is disabled.
func GetActiveEmbeddedEndpoint() (*EmbeddedEndpoint, error) {
	activeID := viper.GetString("embedded_active_id")
	if activeID == "" {
		return nil, nil
	}

	cfg, err := LoadEmbeddedConfig()
	if err != nil {
		return nil, err
	}

	for _, ep := range cfg.Endpoints {
		if ep.ID == activeID && ep.Enabled {
			return &ep, nil
		}
	}

	return nil, nil
}

// SetActiveEmbeddedEndpoint sets the active embedded endpoint ID.
func SetActiveEmbeddedEndpoint(id string) error {
	viper.Set("embedded_active_id", id)
	return viper.WriteConfigAs(configFile)
}
