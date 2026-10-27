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
	QueueStatusFmt string // format string with %d placeholders

	// Status
	StatusIdle       string
	StatusWorking    string
	StatusError      string
	StatusCompleted  string
	StatusPending    string
	StatusInProgress string

	// Dashboard / Detail view
	ErrorPrefix       string // "에러: " / "Error: "
	NoProjects        string // 프로젝트가 없습니다...
	Stats             string // 📊 통계
	WorkingFmt        string // "작업중: %d"
	CompletedFmt      string // "완료: %d"
	IdleFmt           string // "대기: %d"
	ShepherdHelp      string // 목자에게 직접 명령을 내릴 수 있습니다.
	Examples          string // 사용 예시:
	RememberTitle     string // 기억하기:
	RememberHelp      string // '기억해: ...' 형식으로 명령하면...
	SavedMemories     string // 📝 저장된 기억:
	NoMemories        string // 저장된 기억이 없습니다
	InputHint         string // Enter 또는 / 를 눌러 명령을 입력하세요
	ShepherdTaskList  string // 📋 목자 작업 목록
	NoTasks           string // 수행한 작업이 없습니다
	GiveCommand       string // 목자에게 명령을 내려보세요
	StartCommand      string // 명령을 입력하여 작업을 시작하세요
	TaskListKeys      string // ↑/↓: 선택  Enter: 상세 보기
	StatusLabel       string // 상태:
	ProviderLabel     string // 프로바이더:
	SessionLabel      string // 세션:
	NoOutput          string // 출력 없음
	TaskListTitle     string // 작업 목록
	TaskListTitleFmt  string // "📋 %s 작업 목록"
	TaskNotFound      string // 작업을 찾을 수 없습니다
	NoWorkingProjects string // 작업 중인 프로젝트가 없습니다...
	SwitchToDashboard string // [Tab] 대시보드로 전환
	EmptyState        string // 양이 없습니다...
	ProjectListTitle  string // 📋 프로젝트
	ShepherdSidebar   string // 목자
	ShepherdManager   string // 👤 목자 (매니저)
	MemoryFileHeader  string // # Shepherd 기억...
	TaskDetailFmt     string // "📝 작업 #%d 상세"
	StartedFmt        string // "시작: %s"
	CompletedTimeFmt  string // "완료: %s (소요: %s)"
	PromptLabel       string // 프롬프트:
	OutputLabel       string // 출력:
	TaskDetailKeys    string // PgUp/PgDn: 스크롤  ESC/Enter: 목록으로

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
	CLISheepNameSourceFmt    string // "   name source: %s\n"
	CLISheepNameFallbackFmt  string // "   LLM naming fell back: %s\n"
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
	CLITaskProcessKilledFmt  string // "🛑 %d running task process(es) killed"

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

	// Push/Discord notifications (#665)
	Push struct {
		TaskComplete       string // 작업 완료 / Task completed
		TaskFailed         string // 작업 실패 / Task failed
		TestBody           string // 테스트 알림 본문
		ShepherdReport     string // 목자 보고
		ReportReview       string // 목자 보고 · 검토
		ReportPatrol       string // 목자 보고 · 순찰
		ReportCommand      string // 목자 보고 · 지시
		RequestTitleFmt    string // "Shepherd · 목자 요청 #%d"
		RequestAwaitingFmt string // "%s 승인/거절 대기 — %s"
		CostCapTitle       string // "Shepherd · 하루 비용 상한 도달"
		CostCapBodyFmt     string // cost-cap push body ($%.2f/$%.2f)
	}

	// Patrol skip reasons — stored codes are language-neutral; the API
	// renders them with these labels (#665 §3).
	Patrol struct {
		WakeDisabled     string // wake_disabled
		PatrolBusy       string // patrol_busy
		DailyCap         string // daily_cap
		CostCap          string // cost_cap
		CollectionFailed string // collection_failed
		EnqueueFailed    string // enqueue_failed
	}

	// Needs-attention panel items (#665 §2)
	Attention struct {
		FailedTaskTitleFmt      string // "작업 #%d 실패"
		NoFailureRecorded       string // 실패 원인이 기록되지 않았습니다
		LongRunningTitleFmt     string // "작업 #%d가 %s 넘게 실행 중"
		NoPrompt                string // 프롬프트 없음
		RunningOnSheepFmt       string // "%s 양에서 실행 중 — %s"
		ManagerLabel            string // 목자
		CircuitOpenTitleFmt     string // "%s 양의 서킷 브레이커가 켜졌습니다"
		CircuitOpenMessageFmt   string // 연속 실패 %d회 — ...
		BacklogTitleFmt         string // "%s 프로젝트에 pending 작업이 %d개"
		BacklogMessageFmt       string // 가장 오래된 작업 #%d가 %s 넘게 대기를 기다립니다.
		BacklogDrainingFmt      string // 담당 양 %s이(가) 지금 #%d를 실행 중이므로...
		BacklogDrainingStartFmt string // " 실행 시작 시각은 %s입니다."
		BacklogSheepIdleFmt     string // 담당 양 %s은(는) 지금 대기 상태(idle)이므로...
		BacklogDeadzoneFmt      string // 목자 배정 shepherd_kind 빈 pending %d개 ...
		ReviewLimitTitleFmt     string // "이슈 #%d가 검토 회차 상한에 닿았습니다"
		ReviewLimitMessageFmt   string // "회차 %d/%d — ..."
		UserStoppedTitleFmt     string // "이슈 #%d의 작업 #%d를 사용자가 중지했습니다"
		UserStoppedMessage      string // 사용자가 직접 중지한 작업입니다...
		ProviderDisabledFmt     string // 목자의 프로바이더 '%s'가 설정에서 꺼져 있습니다.
		WorkspaceInvalid        string // 워크스페이스가 지정되지 않았거나 유효하지 않습니다.
		ShepherdBlockedTitleFmt string // "목자 작업 #%d가 pending에 머물러 있습니다"
		ShepherdBlockedMessage  string // reason suffix: " 설정의 목자 탭을 확인하세요."
		CostCapTitleFmt         string // "하루 비용 상한에 닿았습니다 ($%.2f/$%.2f)"
		CostCapMessage          string // 오늘 합산 비용이 상한을 넘었습니다...
		MinutesFmt              string // "%d분"
		HoursFmt                string // "%d시간"
		DaysFmt                 string // "%d일"
	}

	// User-confirmation request results (#665 §2). Codes ride in the API
	// response; these labels render them.
	Requests struct {
		RejectedDefault  string // 사용자가 거절했습니다.
		StopFailedFmt    string // "작업 중지 실패: %v"
		CancelFailedFmt  string // "작업 대기 취소 실패: %v"
		CancelDoneFmt    string // "작업 #%d 대기 취소 완료"
		StopDoneFmt      string // "작업 #%d 중지 완료"
		ExecuteFailedFmt string // "이슈 실행 실패: %v"
		ExecuteDoneFmt   string // "이슈 #%d 실행 적재 완료 (task %v)"
		DeleteFailedFmt  string // "이슈 삭제 실패: %v"
		DeleteDoneFmt    string // "이슈 #%d 삭제 완료"
		OtherApproved    string // 승인 기록만 남겼습니다 (실행할 조치 없음).
		UnknownKindFmt   string // "알 수 없는 kind %q — 조치 없음"
		KindStopTask     string // 작업 중지
		KindExecuteIssue string // 이슈 실행
		KindDeleteIssue  string // 이슈 삭제
		KindOther        string // 기타
		NoTarget         string // 없음
		NoReason         string // 사유 없음
	}

	// Task revert: REST errors and CLI output (#665 §4)
	Revert struct {
		NotFinished             string
		AlreadyReverted         string
		NoCheckpoint            string
		SnapshotPruned          string
		SaveResultFailedFmt     string // "되돌리기 결과를 저장하지 못했습니다: %v"
		NotReverted             string
		MissingRevertSnapshot   string
		RevertSnapshotPruned    string
		RevertedSnapshotMissing string
		UndoSaveFailedFmt       string // "되돌리기 취소 결과를 저장하지 못했습니다: %v"
		NotGitRepo              string
		RunningCheckFailedFmt   string // "실행 중인 작업을 확인하지 못했습니다: %v"
		OtherTaskRunningFmt     string // "같은 저장소에서 작업 #%d이(가) 실행 중입니다..."
		Conflict                string
		GenericFailFmt          string // "되돌리지 못했습니다: %v"
		UndoDoneFmt             string // "✅ Task #%d 되돌리기 취소 완료"
		DoneFmt                 string // "✅ Task #%d 되돌리기 완료"
		PreviewHeaderFmt        string // "🔍 Task #%d 되돌리기 미리보기 (파일은 바뀌지 않았습니다)"
		UncommittedNote         string // ℹ️ 변경은 커밋하지 않은 상태로...
		UndoUncommittedNote     string // ℹ️ 변경은 커밋되지 않은 상태로... (undo wording)
		NoFilesToChange         string // 바뀔 파일이 없습니다.
		Cancelled               string // 취소했습니다.
		FilesReturnHeader       string // 다음 파일이 원래 상태로 돌아갑니다:
		NoChangedFiles          string // 변경되는 파일이 없습니다.
		ConfirmPrompt           string // "되돌릴까요? [y/N]: "
		ConflictFilesHeader     string // 충돌한 파일:
	}

	// Server REST response strings (#665 §5)
	Server struct {
		PromptInjected       string // 프롬프트가 주입되었습니다
		ProviderDefaultModel string // 프로바이더 기본값 (model option label)
	}

	// Task changes panel extras (#665 §5)
	Changes struct {
		LiveWarning string // 작업이 끝난 뒤의 변경도 섞여 있습니다
	}

	// Issue status labels shown to users (#665 §5). LLM prompts keep their
	// own fixed wording.
	IssueStatus struct {
		Todo       string // 작업전
		InProgress string // 작업중
		Testing    string // 테스트
		Failed     string // 실패
		Done       string // 성공
	}

	// Wiki CLI output (#665 §5)
	Wiki struct {
		NoHistoryFmt     string // "페이지 %q 에 대한 버전 히스토리가 없습니다."
		VersionHeaderFmt string // "# %s (버전 %d)"
		HistoryTitleFmt  string // "pages/%s 변경 이력"
		InitCreated      string // 초기 생성 (version summary default)
		ContentChanged   string // 내용 변경 (version summary default)
		LintOrphanFmt    string // 페이지 %q 은 다른 페이지에서 참조되지 않습니다
		LintStaleFmt     string // 페이지 %q 가 %d일 동안 업데이트되지 않았습니다
		LintSimilarFmt   string // 페이지 %q 과 %q 의 내용이 %.0f%% 유사합니다...
		LintDriftFmt     string // 페이지 %q 는 auto_generated=true 이지만...
		InvalidLinksFmt  string // ⚠️ 페이지에 유효하지 않은 링크가 있습니다: %s
	}

	// Provider display names (#665 §5)
	Provider struct {
		Embedded string // 임베디드 / embedded
	}
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

	QueueStatusFmt: "대기: %d, 진행중: %d, 완료: %d, 실패: %d, 중단: %d",

	StatusIdle:       "대기",
	StatusWorking:    "작업중",
	StatusError:      "에러",
	StatusCompleted:  "완료",
	StatusPending:    "대기",
	StatusInProgress: "진행 중",

	ErrorPrefix:       "에러: ",
	NoProjects:        "프로젝트가 없습니다.\n\n목자를 선택하여 프로젝트를 등록하세요.",
	Stats:             "📊 통계",
	WorkingFmt:        "작업중: %d",
	CompletedFmt:      "완료: %d",
	IdleFmt:           "대기: %d",
	ShepherdHelp:      "목자에게 직접 명령을 내릴 수 있습니다.",
	Examples:          "사용 예시:",
	RememberTitle:     "기억하기:",
	RememberHelp:      "'기억해: ...' 형식으로 명령하면\n    해당 내용을 저장하여 이후 작업에 참고합니다.",
	SavedMemories:     "📝 저장된 기억:",
	NoMemories:        "저장된 기억이 없습니다",
	InputHint:         "Enter 또는 / 를 눌러 명령을 입력하세요",
	ShepherdTaskList:  "📋 목자 작업 목록",
	NoTasks:           "수행한 작업이 없습니다",
	GiveCommand:       "목자에게 명령을 내려보세요",
	StartCommand:      "명령을 입력하여 작업을 시작하세요",
	TaskListKeys:      "↑/↓: 선택  Enter: 상세 보기",
	StatusLabel:       "상태: ",
	ProviderLabel:     "프로바이더: ",
	SessionLabel:      "세션: ",
	NoOutput:          "출력 없음",
	TaskListTitle:     "작업 목록",
	TaskListTitleFmt:  "📋 %s 작업 목록",
	TaskNotFound:      "작업을 찾을 수 없습니다",
	NoWorkingProjects: "작업 중인 프로젝트가 없습니다.\n\n명령을 입력하여 작업을 시작하세요.\n작업이 시작되면 여기에 실시간 출력이 표시됩니다.",
	SwitchToDashboard: "[Tab] 대시보드로 전환",
	EmptyState:        "양이 없습니다.\n\n먼저 양을 생성하고 프로젝트에 배정하세요:\n  shepherd spawn        # 양 생성\n  shepherd project add  # 프로젝트 추가\n  shepherd project assign  # 양 배정",
	ProjectListTitle:  "📋 프로젝트",
	ShepherdSidebar:   "목자",
	ShepherdManager:   "👤 목자 (매니저)",
	MemoryFileHeader:  "# Shepherd 기억\n\n이 파일에 저장된 내용은 작업 시 참고됩니다.\n\n",
	TaskDetailFmt:     "📝 작업 #%d 상세",
	StartedFmt:        "시작: %s",
	CompletedTimeFmt:  "완료: %s (소요: %s)",
	PromptLabel:       "프롬프트:",
	OutputLabel:       "출력:",
	TaskDetailKeys:    "PgUp/PgDn: 스크롤  ESC/Enter: 목록으로",

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
	CLISheepNameSourceFmt:    "   이름 출처: %s\n",
	CLISheepNameFallbackFmt:  "   LLM 작명 폴백: %s\n",
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
	CLITaskProcessKilledFmt:  "🛑 실행 중이던 작업 프로세스 %d개 종료됨\n",

	CLIFlagSheepName:      "양 이름 지정",
	CLIFlagRecallAll:      "모든 양 종료",
	CLIFlagProjectDesc:    "프로젝트 설명",
	CLIFlagLogLimit:       "표시할 로그 수",
	CLIFlagBrowserSheep:   "양 이름 (기본: 목자)",
	CLIFlagBrowserPage:    "페이지 이름",
	CLIFlagBrowserHead:    "헤드리스 모드",
	CLIFlagBrowserWait:    "대기할 선택자",
	CLIFlagBrowserCapture: "캡처할 요소 선택자",

	Push: struct {
		TaskComplete       string
		TaskFailed         string
		TestBody           string
		ShepherdReport     string
		ReportReview       string
		ReportPatrol       string
		ReportCommand      string
		RequestTitleFmt    string
		RequestAwaitingFmt string
		CostCapTitle       string
		CostCapBodyFmt     string
	}{
		TaskComplete:       "작업 완료",
		TaskFailed:         "작업 실패",
		TestBody:           "테스트 알림입니다. 작업이 끝나면 이런 식으로 알려 드립니다.",
		ShepherdReport:     "목자 보고",
		ReportReview:       "목자 보고 · 검토",
		ReportPatrol:       "목자 보고 · 순찰",
		ReportCommand:      "목자 보고 · 지시",
		RequestTitleFmt:    "Shepherd · 목자 요청 #%d",
		RequestAwaitingFmt: "%s 승인/거절 대기 — %s",
		CostCapTitle:       "Shepherd · 하루 비용 상한 도달",
		CostCapBodyFmt:     "오늘 목자+감시 작업 비용 $%.2f이 상한 $%.2f에 닿았습니다. 자동 깨우기와 위임이 멈춥니다.",
	},

	Patrol: struct {
		WakeDisabled     string
		PatrolBusy       string
		DailyCap         string
		CostCap          string
		CollectionFailed string
		EnqueueFailed    string
	}{
		WakeDisabled:     "깨우기 꺼짐",
		PatrolBusy:       "순찰 작업 진행 중",
		DailyCap:         "하루 깨우기 상한 도달",
		CostCap:          "하루 비용 상한 도달",
		CollectionFailed: "점검 실패",
		EnqueueFailed:    "목자 작업 적재 실패",
	},

	Attention: struct {
		FailedTaskTitleFmt      string
		NoFailureRecorded       string
		LongRunningTitleFmt     string
		NoPrompt                string
		RunningOnSheepFmt       string
		ManagerLabel            string
		CircuitOpenTitleFmt     string
		CircuitOpenMessageFmt   string
		BacklogTitleFmt         string
		BacklogMessageFmt       string
		BacklogDrainingFmt      string
		BacklogDrainingStartFmt string
		BacklogSheepIdleFmt     string
		BacklogDeadzoneFmt      string
		ReviewLimitTitleFmt     string
		ReviewLimitMessageFmt   string
		UserStoppedTitleFmt     string
		UserStoppedMessage      string
		ProviderDisabledFmt     string
		WorkspaceInvalid        string
		ShepherdBlockedTitleFmt string
		ShepherdBlockedMessage  string
		CostCapTitleFmt         string
		CostCapMessage          string
		MinutesFmt              string
		HoursFmt                string
		DaysFmt                 string
	}{
		FailedTaskTitleFmt:      "작업 #%d 실패",
		NoFailureRecorded:       "실패 원인이 기록되지 않았습니다",
		LongRunningTitleFmt:     "작업 #%d가 %s 넘게 실행 중",
		NoPrompt:                "프롬프트 없음",
		RunningOnSheepFmt:       "%s 양에서 실행 중 — %s",
		ManagerLabel:            "목자",
		CircuitOpenTitleFmt:     "%s 양의 서킷 브레이커가 켜졌습니다",
		CircuitOpenMessageFmt:   "연속 실패 %d회 — 대기 작업이 멈췄습니다. 재시도 전에 서킷을 초기화하세요.",
		BacklogTitleFmt:         "%s 프로젝트에 pending 작업이 %d개",
		BacklogMessageFmt:       "가장 오래된 작업 #%d가 %s 넘게 대기를 기다립니다.",
		BacklogDrainingFmt:      " 담당 양 %s이(가) 지금 #%d를 실행 중이므로, 이 pending은 직렬 큐에서 순서를 기다리는 것일 수 있습니다.",
		BacklogDrainingStartFmt: " 실행 시작 시각은 %s입니다.",
		BacklogSheepIdleFmt:     " 담당 양 %s은(는) 지금 대기 상태(idle)이므로 실제 정체일 가능성이 있습니다.",
		BacklogDeadzoneFmt:      " 같은 프로젝트에 목자(shepherd) 양에게 배정되었고 shepherd_kind이 빈 pending이 %d개 있습니다. #629 수정이 배포되기 전의 데몬에서는 이런 작업이 실행되지 않고 고착됩니다.",
		ReviewLimitTitleFmt:     "이슈 #%d가 검토 회차 상한에 닿았습니다",
		ReviewLimitMessageFmt:   "회차 %d/%d — 회차를 초기화한 뒤 다시 적재하거나 이슈를 마감하세요.",
		UserStoppedTitleFmt:     "이슈 #%d의 작업 #%d를 사용자가 중지했습니다",
		UserStoppedMessage:      "사용자가 직접 중지한 작업입니다. 감시 이슈로 남아 있지만 목자는 깨어나지 않습니다.",
		ProviderDisabledFmt:     "목자의 프로바이더 '%s'가 설정에서 꺼져 있습니다.",
		WorkspaceInvalid:        "워크스페이스가 지정되지 않았거나 유효하지 않습니다.",
		ShepherdBlockedTitleFmt: "목자 작업 #%d가 pending에 머물러 있습니다",
		ShepherdBlockedMessage:  " 설정의 목자 탭을 확인하세요.",
		CostCapTitleFmt:         "하루 비용 상한에 닿았습니다 ($%.2f/$%.2f)",
		CostCapMessage:          "목자 작업과 감시 이슈 작업의 오늘 합산 비용이 상한을 넘었습니다. 자동 깨우기와 목자의 이슈 적재가 거부됩니다. 자정이 지나면 자동으로 풀립니다.",
		MinutesFmt:              "%d분",
		HoursFmt:                "%d시간",
		DaysFmt:                 "%d일",
	},

	Requests: struct {
		RejectedDefault  string
		StopFailedFmt    string
		CancelFailedFmt  string
		CancelDoneFmt    string
		StopDoneFmt      string
		ExecuteFailedFmt string
		ExecuteDoneFmt   string
		DeleteFailedFmt  string
		DeleteDoneFmt    string
		OtherApproved    string
		UnknownKindFmt   string
		KindStopTask     string
		KindExecuteIssue string
		KindDeleteIssue  string
		KindOther        string
		NoTarget         string
		NoReason         string
	}{
		RejectedDefault:  "사용자가 거절했습니다.",
		StopFailedFmt:    "작업 중지 실패: %v",
		CancelFailedFmt:  "작업 대기 취소 실패: %v",
		CancelDoneFmt:    "작업 #%d 대기 취소 완료",
		StopDoneFmt:      "작업 #%d 중지 완료",
		ExecuteFailedFmt: "이슈 실행 실패: %v",
		ExecuteDoneFmt:   "이슈 #%d 실행 적재 완료 (task %v)",
		DeleteFailedFmt:  "이슈 삭제 실패: %v",
		DeleteDoneFmt:    "이슈 #%d 삭제 완료",
		OtherApproved:    "승인 기록만 남겼습니다 (실행할 조치 없음).",
		UnknownKindFmt:   "알 수 없는 kind %q — 조치 없음",
		KindStopTask:     "작업 중지",
		KindExecuteIssue: "이슈 실행",
		KindDeleteIssue:  "이슈 삭제",
		KindOther:        "기타",
		NoTarget:         "없음",
		NoReason:         "사유 없음",
	},

	Revert: struct {
		NotFinished             string
		AlreadyReverted         string
		NoCheckpoint            string
		SnapshotPruned          string
		SaveResultFailedFmt     string
		NotReverted             string
		MissingRevertSnapshot   string
		RevertSnapshotPruned    string
		RevertedSnapshotMissing string
		UndoSaveFailedFmt       string
		NotGitRepo              string
		RunningCheckFailedFmt   string
		OtherTaskRunningFmt     string
		Conflict                string
		GenericFailFmt          string
		UndoDoneFmt             string
		DoneFmt                 string
		PreviewHeaderFmt        string
		UncommittedNote         string
		UndoUncommittedNote     string
		NoFilesToChange         string
		Cancelled               string
		FilesReturnHeader       string
		NoChangedFiles          string
		ConfirmPrompt           string
		ConflictFilesHeader     string
	}{
		NotFinished:             "작업이 완료·실패·중단 상태일 때만 되돌릴 수 있습니다",
		AlreadyReverted:         "이미 되돌린 작업입니다. 되돌리기 취소를 먼저 실행하세요",
		NoCheckpoint:            "체크포인트가 없어 되돌릴 수 없습니다",
		SnapshotPruned:          "체크포인트 스냅샷을 찾을 수 없습니다",
		SaveResultFailedFmt:     "되돌리기 결과를 저장하지 못했습니다: %v",
		NotReverted:             "되돌린 작업이 아닙니다",
		MissingRevertSnapshot:   "되돌리기 스냅샷 정보가 없습니다",
		RevertSnapshotPruned:    "되돌리기 직전 스냅샷을 찾을 수 없습니다",
		RevertedSnapshotMissing: "되돌리기 스냅샷을 찾을 수 없습니다. 스냅샷이 정리되었을 수 있습니다",
		UndoSaveFailedFmt:       "되돌리기 취소 결과를 저장하지 못했습니다: %v",
		NotGitRepo:              "git 저장소가 아니라서 되돌릴 수 없습니다",
		RunningCheckFailedFmt:   "실행 중인 작업을 확인하지 못했습니다: %v",
		OtherTaskRunningFmt:     "같은 저장소에서 작업 #%d이(가) 실행 중입니다. 그 작업이 끝난 뒤에 되돌려 주세요",
		Conflict:                "되돌리는 중 충돌이 발생했습니다. 작업 트리는 바뀌지 않았습니다",
		GenericFailFmt:          "되돌리지 못했습니다: %v",
		UndoDoneFmt:             "✅ Task #%d 되돌리기 취소 완료",
		DoneFmt:                 "✅ Task #%d 되돌리기 완료",
		PreviewHeaderFmt:        "🔍 Task #%d 되돌리기 미리보기 (파일은 바뀌지 않았습니다)",
		UncommittedNote:         "ℹ️ 변경은 커밋하지 않은 상태로 작업 트리에 남아 있습니다.",
		UndoUncommittedNote:     "ℹ️ 변경은 커밋되지 않은 상태로 작업 트리에 남아 있습니다.",
		NoFilesToChange:         "바뀔 파일이 없습니다.",
		Cancelled:               "취소했습니다.",
		FilesReturnHeader:       "다음 파일이 원래 상태로 돌아갑니다:",
		NoChangedFiles:          "변경되는 파일이 없습니다.",
		ConfirmPrompt:           "되돌릴까요? [y/N]: ",
		ConflictFilesHeader:     "충돌한 파일:",
	},

	Server: struct {
		PromptInjected       string
		ProviderDefaultModel string
	}{
		PromptInjected:       "프롬프트가 주입되었습니다",
		ProviderDefaultModel: "프로바이더 기본값",
	},

	Changes: struct {
		LiveWarning string
	}{
		LiveWarning: "작업이 끝난 뒤의 변경도 섞여 있습니다",
	},

	IssueStatus: struct {
		Todo       string
		InProgress string
		Testing    string
		Failed     string
		Done       string
	}{
		Todo:       "작업전",
		InProgress: "작업중",
		Testing:    "테스트",
		Failed:     "실패",
		Done:       "성공",
	},

	Wiki: struct {
		NoHistoryFmt     string
		VersionHeaderFmt string
		HistoryTitleFmt  string
		InitCreated      string
		ContentChanged   string
		LintOrphanFmt    string
		LintStaleFmt     string
		LintSimilarFmt   string
		LintDriftFmt     string
		InvalidLinksFmt  string
	}{
		NoHistoryFmt:     "페이지 %q 에 대한 버전 히스토리가 없습니다.",
		VersionHeaderFmt: "# %s (버전 %d)",
		HistoryTitleFmt:  "pages/%s 변경 이력",
		InitCreated:      "초기 생성",
		ContentChanged:   "내용 변경",
		LintOrphanFmt:    "페이지 %q 은 다른 페이지에서 참조되지 않습니다",
		LintStaleFmt:     "페이지 %q 가 %d일 동안 업데이트되지 않았습니다",
		LintSimilarFmt:   "페이지 %q 과 %q 의 내용이 %.0f%% 유사합니다 (복사/붙여넣기 확인)",
		LintDriftFmt:     "페이지 %q 는 auto_generated=true 이지만 파일 시스템의 내용과 DB 내용이 다릅니다",
		InvalidLinksFmt:  "⚠️ 페이지에 유효하지 않은 링크가 있습니다: %s",
	},

	Provider: struct {
		Embedded string
	}{
		Embedded: "임베디드",
	},
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

	QueueStatusFmt: "Pending: %d, Running: %d, Completed: %d, Failed: %d, Stopped: %d",

	StatusIdle:       "Idle",
	StatusWorking:    "Working",
	StatusError:      "Error",
	StatusCompleted:  "Completed",
	StatusPending:    "Pending",
	StatusInProgress: "In Progress",

	ErrorPrefix:       "Error: ",
	NoProjects:        "No projects registered.\n\nSelect Shepherd to register a project.",
	Stats:             "📊 Stats",
	WorkingFmt:        "Working: %d",
	CompletedFmt:      "Done: %d",
	IdleFmt:           "Idle: %d",
	ShepherdHelp:      "You can give commands directly to Shepherd.",
	Examples:          "Examples:",
	RememberTitle:     "Remember:",
	RememberHelp:      "Use 'remember: ...' to save notes\n    for future task reference.",
	SavedMemories:     "📝 Saved Memories:",
	NoMemories:        "No saved memories",
	InputHint:         "Press Enter or / to enter a command",
	ShepherdTaskList:  "📋 Shepherd Task List",
	NoTasks:           "No tasks performed yet",
	GiveCommand:       "Give Shepherd a command to get started",
	StartCommand:      "Enter a command to start a task",
	TaskListKeys:      "↑/↓: Select  Enter: View details",
	StatusLabel:       "Status: ",
	ProviderLabel:     "Provider: ",
	SessionLabel:      "Session: ",
	NoOutput:          "No output",
	TaskListTitle:     "Task List",
	TaskListTitleFmt:  "📋 %s Task List",
	TaskNotFound:      "Task not found",
	NoWorkingProjects: "No projects currently working.\n\nEnter a command to start a task.\nReal-time output will be displayed here once a task starts.",
	SwitchToDashboard: "[Tab] Switch to dashboard",
	EmptyState:        "No sheep available.\n\nCreate sheep and assign them to projects first:\n  shepherd spawn        # Create sheep\n  shepherd project add  # Add project\n  shepherd project assign  # Assign sheep",
	ProjectListTitle:  "📋 Projects",
	ShepherdSidebar:   "Shepherd",
	ShepherdManager:   "👤 Shepherd (Manager)",
	MemoryFileHeader:  "# Shepherd Memories\n\nContent saved here will be referenced during tasks.\n\n",
	TaskDetailFmt:     "📝 Task #%d Details",
	StartedFmt:        "Started: %s",
	CompletedTimeFmt:  "Completed: %s (Duration: %s)",
	PromptLabel:       "Prompt:",
	OutputLabel:       "Output:",
	TaskDetailKeys:    "PgUp/PgDn: Scroll  ESC/Enter: Back to list",

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
	CLISheepNameSourceFmt:    "   name source: %s\n",
	CLISheepNameFallbackFmt:  "   LLM naming fell back: %s\n",
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
	CLITaskProcessKilledFmt:  "🛑 %d running task process(es) killed\n",

	CLIFlagSheepName:      "Specify sheep name",
	CLIFlagRecallAll:      "Terminate all sheep",
	CLIFlagProjectDesc:    "Project description",
	CLIFlagLogLimit:       "Number of logs to display",
	CLIFlagBrowserSheep:   "Sheep name (default: shepherd)",
	CLIFlagBrowserPage:    "Page name",
	CLIFlagBrowserHead:    "Headless mode",
	CLIFlagBrowserWait:    "Selector to wait for",
	CLIFlagBrowserCapture: "Element selector to capture",

	Push: struct {
		TaskComplete       string
		TaskFailed         string
		TestBody           string
		ShepherdReport     string
		ReportReview       string
		ReportPatrol       string
		ReportCommand      string
		RequestTitleFmt    string
		RequestAwaitingFmt string
		CostCapTitle       string
		CostCapBodyFmt     string
	}{
		TaskComplete:       "Task completed",
		TaskFailed:         "Task failed",
		TestBody:           "This is a test notification. Finished tasks will be announced like this.",
		ShepherdReport:     "Shepherd report",
		ReportReview:       "Shepherd report · review",
		ReportPatrol:       "Shepherd report · patrol",
		ReportCommand:      "Shepherd report · command",
		RequestTitleFmt:    "Shepherd · shepherd request #%d",
		RequestAwaitingFmt: "%s awaiting approval/rejection — %s",
		CostCapTitle:       "Shepherd · daily cost cap reached",
		CostCapBodyFmt:     "Today's shepherd+watched task spend $%.2f reached the cap $%.2f. Automatic waking and delegation stop.",
	},

	Patrol: struct {
		WakeDisabled     string
		PatrolBusy       string
		DailyCap         string
		CostCap          string
		CollectionFailed string
		EnqueueFailed    string
	}{
		WakeDisabled:     "wake disabled",
		PatrolBusy:       "patrol task in progress",
		DailyCap:         "daily wake cap reached",
		CostCap:          "daily cost cap reached",
		CollectionFailed: "collection failed",
		EnqueueFailed:    "shepherd task enqueue failed",
	},

	Attention: struct {
		FailedTaskTitleFmt      string
		NoFailureRecorded       string
		LongRunningTitleFmt     string
		NoPrompt                string
		RunningOnSheepFmt       string
		ManagerLabel            string
		CircuitOpenTitleFmt     string
		CircuitOpenMessageFmt   string
		BacklogTitleFmt         string
		BacklogMessageFmt       string
		BacklogDrainingFmt      string
		BacklogDrainingStartFmt string
		BacklogSheepIdleFmt     string
		BacklogDeadzoneFmt      string
		ReviewLimitTitleFmt     string
		ReviewLimitMessageFmt   string
		UserStoppedTitleFmt     string
		UserStoppedMessage      string
		ProviderDisabledFmt     string
		WorkspaceInvalid        string
		ShepherdBlockedTitleFmt string
		ShepherdBlockedMessage  string
		CostCapTitleFmt         string
		CostCapMessage          string
		MinutesFmt              string
		HoursFmt                string
		DaysFmt                 string
	}{
		FailedTaskTitleFmt:      "Task #%d failed",
		NoFailureRecorded:       "No failure cause was recorded",
		LongRunningTitleFmt:     "Task #%d has been running for over %s",
		NoPrompt:                "No prompt",
		RunningOnSheepFmt:       "Running on sheep %s — %s",
		ManagerLabel:            "Shepherd",
		CircuitOpenTitleFmt:     "%s's circuit breaker tripped",
		CircuitOpenMessageFmt:   "%d consecutive failures — pending tasks stopped. Reset the circuit before retrying.",
		BacklogTitleFmt:         "Project %s has %d pending tasks",
		BacklogMessageFmt:       "The oldest task #%d has been waiting in pending for over %s.",
		BacklogDrainingFmt:      " Assigned sheep %s is running task #%d now, so this pending may just be waiting its turn in the serial queue.",
		BacklogDrainingStartFmt: " Its run started at %s.",
		BacklogSheepIdleFmt:     " Assigned sheep %s is idle now, so this may be a real stall.",
		BacklogDeadzoneFmt:      " The same project has %d pending task(s) assigned to the shepherd sheep with an empty shepherd_kind. Daemons before the #629 fix never run those and they stay stuck.",
		ReviewLimitTitleFmt:     "Issue #%d reached the review-round cap",
		ReviewLimitMessageFmt:   "Round %d/%d — reset the rounds and re-enqueue, or close the issue.",
		UserStoppedTitleFmt:     "Issue #%d: the user stopped task #%d",
		UserStoppedMessage:      "The user stopped this task directly. The issue stays watched, but the shepherd does not wake.",
		ProviderDisabledFmt:     "The shepherd's provider '%s' is disabled in settings.",
		WorkspaceInvalid:        "The workspace is unset or invalid.",
		ShepherdBlockedTitleFmt: "Shepherd task #%d is stuck in pending",
		ShepherdBlockedMessage:  " Check the Shepherd tab in Settings.",
		CostCapTitleFmt:         "Daily cost cap reached ($%.2f/$%.2f)",
		CostCapMessage:          "Today's combined spend of shepherd and watched-issue tasks passed the cap. Automatic waking and shepherd issue enqueue are refused. The cap lifts after midnight.",
		MinutesFmt:              "%dm",
		HoursFmt:                "%dh",
		DaysFmt:                 "%dd",
	},

	Requests: struct {
		RejectedDefault  string
		StopFailedFmt    string
		CancelFailedFmt  string
		CancelDoneFmt    string
		StopDoneFmt      string
		ExecuteFailedFmt string
		ExecuteDoneFmt   string
		DeleteFailedFmt  string
		DeleteDoneFmt    string
		OtherApproved    string
		UnknownKindFmt   string
		KindStopTask     string
		KindExecuteIssue string
		KindDeleteIssue  string
		KindOther        string
		NoTarget         string
		NoReason         string
	}{
		RejectedDefault:  "The user rejected the request.",
		StopFailedFmt:    "failed to stop task: %v",
		CancelFailedFmt:  "failed to cancel pending task: %v",
		CancelDoneFmt:    "pending task #%d cancelled",
		StopDoneFmt:      "task #%d stopped",
		ExecuteFailedFmt: "issue execute failed: %v",
		ExecuteDoneFmt:   "issue #%d enqueued (task %v)",
		DeleteFailedFmt:  "issue delete failed: %v",
		DeleteDoneFmt:    "issue #%d deleted",
		OtherApproved:    "approval recorded only (no action to run).",
		UnknownKindFmt:   "unknown kind %q — no action",
		KindStopTask:     "stop task",
		KindExecuteIssue: "execute issue",
		KindDeleteIssue:  "delete issue",
		KindOther:        "other",
		NoTarget:         "none",
		NoReason:         "no reason given",
	},

	Revert: struct {
		NotFinished             string
		AlreadyReverted         string
		NoCheckpoint            string
		SnapshotPruned          string
		SaveResultFailedFmt     string
		NotReverted             string
		MissingRevertSnapshot   string
		RevertSnapshotPruned    string
		RevertedSnapshotMissing string
		UndoSaveFailedFmt       string
		NotGitRepo              string
		RunningCheckFailedFmt   string
		OtherTaskRunningFmt     string
		Conflict                string
		GenericFailFmt          string
		UndoDoneFmt             string
		DoneFmt                 string
		PreviewHeaderFmt        string
		UncommittedNote         string
		UndoUncommittedNote     string
		NoFilesToChange         string
		Cancelled               string
		FilesReturnHeader       string
		NoChangedFiles          string
		ConfirmPrompt           string
		ConflictFilesHeader     string
	}{
		NotFinished:             "only completed, failed, or stopped tasks can be reverted",
		AlreadyReverted:         "already reverted; run undo first",
		NoCheckpoint:            "no checkpoint, cannot revert",
		SnapshotPruned:          "checkpoint snapshot not found",
		SaveResultFailedFmt:     "failed to save revert result: %v",
		NotReverted:             "task was not reverted",
		MissingRevertSnapshot:   "missing revert snapshot",
		RevertSnapshotPruned:    "pre-revert snapshot not found",
		RevertedSnapshotMissing: "revert snapshot not found; it may have been pruned",
		UndoSaveFailedFmt:       "failed to save undo result: %v",
		NotGitRepo:              "not a git repository, cannot revert",
		RunningCheckFailedFmt:   "failed to check running tasks: %v",
		OtherTaskRunningFmt:     "task #%d is running in the same repository; revert after it finishes",
		Conflict:                "the revert hit conflicts; the working tree was not changed",
		GenericFailFmt:          "revert failed: %v",
		UndoDoneFmt:             "✅ Task #%d revert undone",
		DoneFmt:                 "✅ Task #%d reverted",
		PreviewHeaderFmt:        "🔍 Task #%d revert preview (no files were changed)",
		UncommittedNote:         "ℹ️ The changes stay in the working tree, uncommitted.",
		UndoUncommittedNote:     "ℹ️ The changes stay in the working tree, uncommitted.",
		NoFilesToChange:         "No files would change.",
		Cancelled:               "Cancelled.",
		FilesReturnHeader:       "These files return to their original state:",
		NoChangedFiles:          "No files changed.",
		ConfirmPrompt:           "Revert? [y/N]: ",
		ConflictFilesHeader:     "Conflicting files:",
	},

	Server: struct {
		PromptInjected       string
		ProviderDefaultModel string
	}{
		PromptInjected:       "The prompt was injected",
		ProviderDefaultModel: "provider default",
	},

	Changes: struct {
		LiveWarning string
	}{
		LiveWarning: "changes made after the task finished are mixed in",
	},

	IssueStatus: struct {
		Todo       string
		InProgress string
		Testing    string
		Failed     string
		Done       string
	}{
		Todo:       "to do",
		InProgress: "in progress",
		Testing:    "testing",
		Failed:     "failed",
		Done:       "done",
	},

	Wiki: struct {
		NoHistoryFmt     string
		VersionHeaderFmt string
		HistoryTitleFmt  string
		InitCreated      string
		ContentChanged   string
		LintOrphanFmt    string
		LintStaleFmt     string
		LintSimilarFmt   string
		LintDriftFmt     string
		InvalidLinksFmt  string
	}{
		NoHistoryFmt:     "No version history for page %q.",
		VersionHeaderFmt: "# %s (version %d)",
		HistoryTitleFmt:  "pages/%s change history",
		InitCreated:      "initial creation",
		ContentChanged:   "content change",
		LintOrphanFmt:    "page %q is not referenced by any other page",
		LintStaleFmt:     "page %q has not been updated for %d days",
		LintSimilarFmt:   "pages %q and %q are %.0f%% similar (check for copy/paste)",
		LintDriftFmt:     "page %q has auto_generated=true but its file-system content differs from the DB content",
		InvalidLinksFmt:  "⚠️ The page has invalid links: %s",
	},

	Provider: struct {
		Embedded string
	}{
		Embedded: "embedded",
	},
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
