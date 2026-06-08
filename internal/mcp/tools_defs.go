package mcp

// ListCoreToolDefs returns the list of core tool definitions (task_*, get_*, skill_load).
// This is shared between handleToolsList() (MCP stdio) and the embedded provider.
func ListCoreToolDefs() []Tool {
	return []Tool{
		{
			Name:        "task_start",
			Description: "작업을 큐에 추가합니다. 추가된 작업은 해당 양이 idle 상태가 되면 자동으로 실행됩니다.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name":   {Type: "string", Description: "양 이름 (생략 시 프로젝트에 할당된 양 자동 사용)"},
					"project_name": {Type: "string", Description: "프로젝트 이름"},
					"prompt":       {Type: "string", Description: "작업 내용 (Claude에게 전달할 프롬프트)"},
				},
				Required: []string{"project_name", "prompt"},
			},
		},
		{
			Name:        "task_complete",
			Description: "작업 완료를 기록합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"task_id":        {Type: "number", Description: "작업 ID"},
					"summary":        {Type: "string", Description: "작업 요약"},
					"files_modified": {Type: "string", Description: "수정된 파일 목록 (쉼표 구분)"},
				},
				Required: []string{"task_id", "summary"},
			},
		},
		{
			Name:        "task_error",
			Description: "작업 에러를 기록합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"task_id": {Type: "number", Description: "작업 ID"},
					"error":   {Type: "string", Description: "에러 메시지"},
				},
				Required: []string{"task_id", "error"},
			},
		},
		{
			Name:        "get_history",
			Description: "프로젝트 작업 히스토리를 조회합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"project_name": {Type: "string", Description: "프로젝트 이름"},
					"limit":        {Type: "number", Description: "조회할 개수 (기본 10)"},
				},
				Required: []string{"project_name"},
			},
		},
		{
			Name:        "get_task_detail",
			Description: "작업 상세 정보(요청 프롬프트, 결과 요약, 에러, 수정 파일, 비용, 타임스탬프, 출력 로그)를 전체 조회합니다. 이전 작업 내용을 정확히 파악해야 할 때 사용하세요.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"task_id": {Type: "number", Description: "작업 ID"},
				},
				Required: []string{"task_id"},
			},
		},
		{
			Name:        "get_status",
			Description: "전체 시스템 상태를 조회합니다",
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]Property{},
			},
		},
		{
			Name:        "skill_load",
			Description: "Load full content of a skill by name. Use this when you need detailed instructions from a project skill.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"skill_name": {Type: "string", Description: "Name of the skill to load"},
				},
				Required: []string{"skill_name"},
			},
		},
	}
}
