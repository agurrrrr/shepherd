package i18n

import "sync"

// Language represents a supported language.
type Language string

const (
	LangKo Language = "ko"
	LangEn Language = "en"
)

// Messages contains all translatable UI strings.
type Messages struct {
	// TUI
	ShepherdDisplayName string // Display name for shepherd (목자 / Shepherd)
	TUITitle            string
	Loading             string
	ViewSwitch          string
	Help                string

	// Settings
	SettingsTitle     string
	SettingsLanguage  string
	SettingsProvider  string
	SettingsWorkspace string
	SettingsSaved     string
	SettingsKeys      string
	SettingsNotSet    string

	// Queue/Processor
	QueueStatusFmt       string // format string with %d placeholders
	RateLimitSwitch      string
	RateLimitRequeue     string
	RateLimitRestoreFmt  string // format string: provider name

	// Status
	StatusIdle       string
	StatusWorking    string
	StatusError      string
	StatusCompleted  string
	StatusPending    string
	StatusInProgress string

	// Dashboard / Detail view
	ErrorPrefix        string // "에러: " / "Error: "
	NoProjects         string // 프로젝트가 없습니다...
	Stats              string // 📊 통계
	WorkingFmt         string // "작업중: %d"
	CompletedFmt       string // "완료: %d"
	IdleFmt            string // "대기: %d"
	ShepherdHelp       string // 목자에게 직접 명령을 내릴 수 있습니다.
	Examples           string // 사용 예시:
	RememberTitle      string // 기억하기:
	RememberHelp       string // '기억해: ...' 형식으로 명령하면...
	SavedMemories      string // 📝 저장된 기억:
	NoMemories         string // 저장된 기억이 없습니다
	InputHint          string // Enter 또는 / 를 눌러 명령을 입력하세요
	ShepherdTaskList   string // 📋 목자 작업 목록
	NoTasks            string // 수행한 작업이 없습니다
	GiveCommand        string // 목자에게 명령을 내려보세요
	StartCommand       string // 명령을 입력하여 작업을 시작하세요
	TaskListKeys       string // ↑/↓: 선택  Enter: 상세 보기
	StatusLabel        string // 상태:
	ProviderLabel      string // 프로바이더:
	SessionLabel       string // 세션:
	NoOutput           string // 출력 없음
	TaskListTitle      string // 작업 목록
	TaskListTitleFmt   string // "📋 %s 작업 목록"
	TaskNotFound       string // 작업을 찾을 수 없습니다
	NoWorkingProjects  string // 작업 중인 프로젝트가 없습니다...
	SwitchToDashboard  string // [Tab] 대시보드로 전환
	EmptyState         string // 양이 없습니다...
	ProjectListTitle   string // 📋 프로젝트
	ShepherdSidebar    string // 목자
	ShepherdManager    string // 👤 목자 (매니저)
	MemoryFileHeader   string // # Shepherd 기억...
	TaskDetailFmt      string // "📝 작업 #%d 상세"
	StartedFmt         string // "시작: %s"
	CompletedTimeFmt   string // "완료: %s (소요: %s)"
	PromptLabel        string // 프롬프트:
	OutputLabel        string // 출력:
	TaskDetailKeys     string // PgUp/PgDn: 스크롤  ESC/Enter: 목록으로

	// Examples (for help text)
	Example1 string
	Example2 string
	Example3 string
	Example4 string

	// CLI commands (recover, init, main, shutdown)
	CLIRecoverShort          string // "Recover from abnormal termination"
	CLIRecoverLong           string
	CLIInitShort             string // "Register current directory as a project"
	CLIInitLong              string
	CLITUIErrorFmt           string // "TUI error: %v"
	CLISheepRecoverFailFmt   string // "Failed to recover sheep status: %v"
	CLITaskRecoverFailFmt    string // "Failed to recover task status: %v"
	CLINothingToRecover      string // "Nothing to recover."
	CLISheepRecoveredFmt     string // "🐏 %d sheep status recovered"
	CLITaskRecoveredFmt      string // "📋 %d task(s) status recovered"
	CLIGetCwdFailFmt         string // "Failed to get current directory: %v"
	CLIProjectAddFailFmt     string // "Failed to register project: %v"
	CLIProjectAlreadyFmt     string // "📁 Project '%s' already registered"
	CLIProjectRegisteredFmt  string // "📁 Project '%s' registered (%s)"
	CLISheepListFailFmt      string // "Failed to list sheep: %v"
	CLISheepCreateFailFmt    string // "Failed to create sheep: %v"
	CLISheepCreatedFmt       string // "🐏 %s created"
	CLISheepAssignFailFmt    string // "Failed to assign sheep: %v"
	CLISheepAssignedFmt      string // "🔗 %s assigned to %s"
	CLIInitReady             string // "✅ Ready! You can now request tasks:"
	CLIInitExample           string // "   shepherd \"your task here\""
	CLIConfigInitFailFmt     string // "Failed to initialize config: %v"
	CLIDBInitFailFmt         string // "Failed to initialize database: %v"
	CLIWarnSheepRecoverFmt   string // "Warning: Failed to recover sheep: %v"
	CLISheepRecoveredInfoFmt string // "ℹ️  %d sheep status recovered (previous abnormal termination)"
	CLIWarnTaskRecoverFmt    string // "Warning: Failed to recover tasks: %v"
	CLITaskRecoveredInfoFmt  string // "ℹ️  %d task(s) status recovered (previous abnormal termination)"
	CLISignalReceivedFmt     string // "\n⚠️  Signal received: %v, shutting down..."
	CLISheepCleanedFmt       string // "🐏 %d sheep status cleaned"
	CLITaskInterruptedFmt    string // "📋 %d task(s) interrupted"

	// CLI flag descriptions
	CLIFlagSheepName      string // "Specify sheep name"
	CLIFlagRecallAll      string // "Terminate all sheep"
	CLIFlagProjectDesc    string // "Project description"
	CLIFlagLogLimit       string // "Number of logs to display"
	CLIFlagBrowserSheep   string // "Sheep name (default: shepherd)"
	CLIFlagBrowserPage    string // "Page name"
	CLIFlagBrowserHead    string // "Headless mode"
	CLIFlagBrowserWait    string // "Selector to wait for"
	CLIFlagBrowserCapture string // "Element selector to capture"
}

var (
	current *Messages
	mu      sync.RWMutex
)

var ko = &Messages{
	ShepherdDisplayName: "목자",
	TUITitle:            "🐏 Shepherd TUI",
	Loading:             "로딩 중...",
	ViewSwitch:          "[Tab]뷰전환",
	Help:                "[?]도움말 [q]종료",

	SettingsTitle:     "⚙️  설정",
	SettingsLanguage:  "언어",
	SettingsProvider:  "기본 프로바이더",
	SettingsWorkspace: "워크스페이스 경로",
	SettingsSaved:     "설정이 저장되었습니다",
	SettingsKeys:      "[↑↓]선택 [Enter]변경 [Esc]닫기",
	SettingsNotSet:    "(미설정)",

	QueueStatusFmt:      "대기: %d, 진행중: %d, 완료: %d, 실패: %d, 중단: %d",
	RateLimitSwitch:     "🔄 Rate limit 감지 - 프로바이더를 opencode로 임시 전환\n",
	RateLimitRequeue:    "⏸️ Rate limit - 작업 재큐잉\n",
	RateLimitRestoreFmt: "🔄 Rate limit 해제 - 프로바이더를 %s(으)로 복구\n",

	StatusIdle:       "대기",
	StatusWorking:    "작업중",
	StatusError:      "에러",
	StatusCompleted:  "완료",
	StatusPending:    "대기",
	StatusInProgress: "진행 중",

	ErrorPrefix:        "에러: ",
	NoProjects:         "프로젝트가 없습니다.\n\n목자를 선택하여 프로젝트를 등록하세요.",
	Stats:              "📊 통계",
	WorkingFmt:         "작업중: %d",
	CompletedFmt:       "완료: %d",
	IdleFmt:            "대기: %d",
	ShepherdHelp:       "목자에게 직접 명령을 내릴 수 있습니다.",
	Examples:           "사용 예시:",
	RememberTitle:      "기억하기:",
	RememberHelp:       "'기억해: ...' 형식으로 명령하면\n    해당 내용을 저장하여 이후 작업에 참고합니다.",
	SavedMemories:      "📝 저장된 기억:",
	NoMemories:         "저장된 기억이 없습니다",
	InputHint:          "Enter 또는 / 를 눌러 명령을 입력하세요",
	ShepherdTaskList:   "📋 목자 작업 목록",
	NoTasks:            "수행한 작업이 없습니다",
	GiveCommand:        "목자에게 명령을 내려보세요",
	StartCommand:       "명령을 입력하여 작업을 시작하세요",
	TaskListKeys:       "↑/↓: 선택  Enter: 상세 보기",
	StatusLabel:        "상태: ",
	ProviderLabel:      "프로바이더: ",
	SessionLabel:       "세션: ",
	NoOutput:           "출력 없음",
	TaskListTitle:      "작업 목록",
	TaskListTitleFmt:   "📋 %s 작업 목록",
	TaskNotFound:       "작업을 찾을 수 없습니다",
	NoWorkingProjects:  "작업 중인 프로젝트가 없습니다.\n\n명령을 입력하여 작업을 시작하세요.\n작업이 시작되면 여기에 실시간 출력이 표시됩니다.",
	SwitchToDashboard:  "[Tab] 대시보드로 전환",
	EmptyState:         "양이 없습니다.\n\n먼저 양을 생성하고 프로젝트에 배정하세요:\n  shepherd spawn        # 양 생성\n  shepherd project add  # 프로젝트 추가\n  shepherd project assign  # 양 배정",
	ProjectListTitle:   "📋 프로젝트",
	ShepherdSidebar:    "목자",
	ShepherdManager:    "👤 목자 (매니저)",
	MemoryFileHeader:   "# Shepherd 기억\n\n이 파일에 저장된 내용은 작업 시 참고됩니다.\n\n",
	TaskDetailFmt:      "📝 작업 #%d 상세",
	StartedFmt:         "시작: %s",
	CompletedTimeFmt:   "완료: %s (소요: %s)",
	PromptLabel:        "프롬프트:",
	OutputLabel:        "출력:",
	TaskDetailKeys:     "PgUp/PgDn: 스크롤  ESC/Enter: 목록으로",

	Example1: "프로젝트 등록해줘",
	Example2: "하위 폴더들 프로젝트로 등록해",
	Example3: "프로젝트 목록 보여줘",
	Example4: "배정없는 양들 삭제해",

	CLIRecoverShort:          "비정상 종료 복구",
	CLIRecoverLong:           "이전 비정상 종료로 인해 stuck 상태인 양과 작업을 복구합니다.",
	CLIInitShort:             "현재 디렉토리를 프로젝트로 등록",
	CLIInitLong:              "현재 디렉토리를 프로젝트로 등록하고, 양이 없으면 자동 생성합니다.\n\n예시:\n  shepherd init              # 디렉토리 이름을 프로젝트 이름으로 사용\n  shepherd init my-project   # 프로젝트 이름 지정",
	CLITUIErrorFmt:           "TUI 에러: %v\n",
	CLISheepRecoverFailFmt:   "양 상태 복구 실패: %v\n",
	CLITaskRecoverFailFmt:    "작업 상태 복구 실패: %v\n",
	CLINothingToRecover:      "복구할 항목이 없습니다.",
	CLISheepRecoveredFmt:     "🐏 %d마리 양 상태 복구됨\n",
	CLITaskRecoveredFmt:      "📋 %d개 작업 상태 복구됨\n",
	CLIGetCwdFailFmt:         "현재 디렉토리 확인 실패: %v\n",
	CLIProjectAddFailFmt:     "프로젝트 등록 실패: %v\n",
	CLIProjectAlreadyFmt:     "📁 프로젝트 '%s'가 이미 등록되어 있습니다\n",
	CLIProjectRegisteredFmt:  "📁 프로젝트 '%s' 등록됨 (%s)\n",
	CLISheepListFailFmt:      "양 목록 조회 실패: %v\n",
	CLISheepCreateFailFmt:    "양 생성 실패: %v\n",
	CLISheepCreatedFmt:       "🐏 %s 생성됨\n",
	CLISheepAssignFailFmt:    "양 배정 실패: %v\n",
	CLISheepAssignedFmt:      "🔗 %s에 %s 배정됨\n",
	CLIInitReady:             "✅ 준비 완료! 이제 작업을 요청할 수 있습니다:",
	CLIInitExample:           "   shepherd \"할 일을 입력하세요\"",
	CLIConfigInitFailFmt:     "설정 초기화 실패: %v\n",
	CLIDBInitFailFmt:         "데이터베이스 초기화 실패: %v\n",
	CLIWarnSheepRecoverFmt:   "경고: 양 상태 복구 실패: %v\n",
	CLISheepRecoveredInfoFmt: "ℹ️  %d마리 양 상태 복구됨 (이전 비정상 종료)\n",
	CLIWarnTaskRecoverFmt:    "경고: 작업 상태 복구 실패: %v\n",
	CLITaskRecoveredInfoFmt:  "ℹ️  %d개 작업 상태 복구됨 (이전 비정상 종료)\n",
	CLISignalReceivedFmt:     "\n⚠️  신호 수신: %v, 종료 중...\n",
	CLISheepCleanedFmt:       "🐏 %d마리 양 상태 정리됨\n",
	CLITaskInterruptedFmt:    "📋 %d개 작업 중단됨\n",

	CLIFlagSheepName:      "양 이름 지정",
	CLIFlagRecallAll:      "모든 양 종료",
	CLIFlagProjectDesc:    "프로젝트 설명",
	CLIFlagLogLimit:       "표시할 로그 수",
	CLIFlagBrowserSheep:   "양 이름 (기본: 목자)",
	CLIFlagBrowserPage:    "페이지 이름",
	CLIFlagBrowserHead:    "헤드리스 모드",
	CLIFlagBrowserWait:    "대기할 선택자",
	CLIFlagBrowserCapture: "캡처할 요소 선택자",
}

var en = &Messages{
	ShepherdDisplayName: "Shepherd",
	TUITitle:            "🐏 Shepherd TUI",
	Loading:             "Loading...",
	ViewSwitch:          "[Tab]Switch view",
	Help:                "[?]Help [q]Quit",

	SettingsTitle:     "⚙️  Settings",
	SettingsLanguage:  "Language",
	SettingsProvider:  "Default Provider",
	SettingsWorkspace: "Workspace Path",
	SettingsSaved:     "Settings saved",
	SettingsKeys:      "[↑↓]Select [Enter]Edit [Esc]Close",
	SettingsNotSet:    "(not set)",

	QueueStatusFmt:      "Pending: %d, Running: %d, Completed: %d, Failed: %d, Stopped: %d",
	RateLimitSwitch:     "🔄 Rate limit detected - temporarily switching to opencode\n",
	RateLimitRequeue:    "⏸️ Rate limit - requeueing task\n",
	RateLimitRestoreFmt: "🔄 Rate limit cleared - restoring provider to %s\n",

	StatusIdle:       "Idle",
	StatusWorking:    "Working",
	StatusError:      "Error",
	StatusCompleted:  "Completed",
	StatusPending:    "Pending",
	StatusInProgress: "In Progress",

	ErrorPrefix:        "Error: ",
	NoProjects:         "No projects registered.\n\nSelect Shepherd to register a project.",
	Stats:              "📊 Stats",
	WorkingFmt:         "Working: %d",
	CompletedFmt:       "Done: %d",
	IdleFmt:            "Idle: %d",
	ShepherdHelp:       "You can give commands directly to Shepherd.",
	Examples:           "Examples:",
	RememberTitle:      "Remember:",
	RememberHelp:       "Use 'remember: ...' to save notes\n    for future task reference.",
	SavedMemories:      "📝 Saved Memories:",
	NoMemories:         "No saved memories",
	InputHint:          "Press Enter or / to enter a command",
	ShepherdTaskList:   "📋 Shepherd Task List",
	NoTasks:            "No tasks performed yet",
	GiveCommand:        "Give Shepherd a command to get started",
	StartCommand:       "Enter a command to start a task",
	TaskListKeys:       "↑/↓: Select  Enter: View details",
	StatusLabel:        "Status: ",
	ProviderLabel:      "Provider: ",
	SessionLabel:       "Session: ",
	NoOutput:           "No output",
	TaskListTitle:      "Task List",
	TaskListTitleFmt:   "📋 %s Task List",
	TaskNotFound:       "Task not found",
	NoWorkingProjects:  "No projects currently working.\n\nEnter a command to start a task.\nReal-time output will be displayed here once a task starts.",
	SwitchToDashboard:  "[Tab] Switch to dashboard",
	EmptyState:         "No sheep available.\n\nCreate sheep and assign them to projects first:\n  shepherd spawn        # Create sheep\n  shepherd project add  # Add project\n  shepherd project assign  # Assign sheep",
	ProjectListTitle:   "📋 Projects",
	ShepherdSidebar:    "Shepherd",
	ShepherdManager:    "👤 Shepherd (Manager)",
	MemoryFileHeader:   "# Shepherd Memories\n\nContent saved here will be referenced during tasks.\n\n",
	TaskDetailFmt:      "📝 Task #%d Details",
	StartedFmt:         "Started: %s",
	CompletedTimeFmt:   "Completed: %s (Duration: %s)",
	PromptLabel:        "Prompt:",
	OutputLabel:        "Output:",
	TaskDetailKeys:     "PgUp/PgDn: Scroll  ESC/Enter: Back to list",

	Example1: "Register a project",
	Example2: "Register subfolders as projects",
	Example3: "Show project list",
	Example4: "Delete unassigned sheep",

	CLIRecoverShort:          "Recover from abnormal termination",
	CLIRecoverLong:           "Recovers sheep and tasks stuck due to a previous abnormal termination.",
	CLIInitShort:             "Register current directory as a project",
	CLIInitLong:              "Registers the current directory as a project and auto-creates a sheep if none exist.\n\nExamples:\n  shepherd init              # Use directory name as project name\n  shepherd init my-project   # Specify project name",
	CLITUIErrorFmt:           "TUI error: %v\n",
	CLISheepRecoverFailFmt:   "Failed to recover sheep status: %v\n",
	CLITaskRecoverFailFmt:    "Failed to recover task status: %v\n",
	CLINothingToRecover:      "Nothing to recover.",
	CLISheepRecoveredFmt:     "🐏 %d sheep status recovered\n",
	CLITaskRecoveredFmt:      "📋 %d task(s) status recovered\n",
	CLIGetCwdFailFmt:         "Failed to get current directory: %v\n",
	CLIProjectAddFailFmt:     "Failed to register project: %v\n",
	CLIProjectAlreadyFmt:     "📁 Project '%s' already registered\n",
	CLIProjectRegisteredFmt:  "📁 Project '%s' registered (%s)\n",
	CLISheepListFailFmt:      "Failed to list sheep: %v\n",
	CLISheepCreateFailFmt:    "Failed to create sheep: %v\n",
	CLISheepCreatedFmt:       "🐏 %s created\n",
	CLISheepAssignFailFmt:    "Failed to assign sheep: %v\n",
	CLISheepAssignedFmt:      "🔗 %s assigned to %s\n",
	CLIInitReady:             "✅ Ready! You can now request tasks:",
	CLIInitExample:           "   shepherd \"your task here\"",
	CLIConfigInitFailFmt:     "Failed to initialize config: %v\n",
	CLIDBInitFailFmt:         "Failed to initialize database: %v\n",
	CLIWarnSheepRecoverFmt:   "Warning: Failed to recover sheep: %v\n",
	CLISheepRecoveredInfoFmt: "ℹ️  %d sheep status recovered (previous abnormal termination)\n",
	CLIWarnTaskRecoverFmt:    "Warning: Failed to recover tasks: %v\n",
	CLITaskRecoveredInfoFmt:  "ℹ️  %d task(s) status recovered (previous abnormal termination)\n",
	CLISignalReceivedFmt:     "\n⚠️  Signal received: %v, shutting down...\n",
	CLISheepCleanedFmt:       "🐏 %d sheep status cleaned\n",
	CLITaskInterruptedFmt:    "📋 %d task(s) interrupted\n",

	CLIFlagSheepName:      "Specify sheep name",
	CLIFlagRecallAll:      "Terminate all sheep",
	CLIFlagProjectDesc:    "Project description",
	CLIFlagLogLimit:       "Number of logs to display",
	CLIFlagBrowserSheep:   "Sheep name (default: shepherd)",
	CLIFlagBrowserPage:    "Page name",
	CLIFlagBrowserHead:    "Headless mode",
	CLIFlagBrowserWait:    "Selector to wait for",
	CLIFlagBrowserCapture: "Element selector to capture",
}

// Init initializes the i18n system with the given language.
func Init(lang string) {
	mu.Lock()
	defer mu.Unlock()
	switch Language(lang) {
	case LangEn:
		current = en
	default:
		current = ko
	}
}

// T returns the current language's messages.
// Returns Korean messages if not initialized.
func T() *Messages {
	mu.RLock()
	defer mu.RUnlock()
	if current == nil {
		return ko
	}
	return current
}

// SetLanguage changes the language at runtime.
func SetLanguage(lang string) {
	Init(lang)
}

// CurrentLanguage returns the current language code.
func CurrentLanguage() Language {
	mu.RLock()
	defer mu.RUnlock()
	if current == en {
		return LangEn
	}
	return LangKo
}
