package wiki

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/internal/config"
)

// LLMCallFunc is the callback for making LLM calls from the wiki package.
// This callback pattern avoids a circular import (wiki -> worker -> wiki).
// The callback is registered by main.go using worker.ExecuteInteractive.
type LLMCallFunc func(prompt string) (string, error)

var (
	llmCallback LLMCallFunc
	callbackMu  sync.RWMutex
)

// SetLLMCallback registers the LLM call function used by TriggerIngest.
// Must be called before any ingest is triggered.
func SetLLMCallback(fn LLMCallFunc) {
	callbackMu.Lock()
	defer callbackMu.Unlock()
	llmCallback = fn
}

// WikiPageContext is a lightweight representation of a wiki page for ingest prompts.
type WikiPageContext struct {
	Slug     string
	Title    string
	Category string
	Tags     []string
	Content  string
}

// toPageContexts converts ent.WikiPage slices to WikiPageContext for prompt building.
func toPageContexts(pages []*ent.WikiPage, maxChars int) []*WikiPageContext {
	var result []*WikiPageContext
	for _, p := range pages {
		content := p.Content
		if maxChars > 0 && len(content) > maxChars {
			content = content[:maxChars] + "..."
		}
		result = append(result, &WikiPageContext{
			Slug:     p.Slug,
			Title:    p.Title,
			Category: string(p.Category),
			Tags:     p.Tags,
			Content:  content,
		})
	}
	return result
}

// IngestAction represents a single wiki page change requested by the LLM.
type IngestAction struct {
	Action   string   `json:"action"` // "create", "update", "delete"
	Slug     string   `json:"slug"`
	Title    string   `json:"title"`
	Category string   `json:"category"`
	Content  string   `json:"content"`
	Tags     []string `json:"tags"`
}

// triggerIngest executes the full auto-ingest pipeline.
// Runs in a background goroutine via TriggerIngest.
func triggerIngest(taskID int, projectName, prompt, summary string, filesModified []string) {
	pages, err := ListPages(projectName)
	if err != nil {
		log.Printf("[wiki-ingest] task #%d project %q: failed to list pages: %v", taskID, projectName, err)
		return
	}

	maxChars := 2000
	if v := config.GetInt("wiki_max_page_content_chars"); v > 0 {
		maxChars = v
	}

	pageCtxs := toPageContexts(pages, maxChars)
	promptText := buildIngestPrompt(prompt, summary, filesModified, pageCtxs)
	actions, err := callLLMForIngest(taskID, projectName, promptText)
	if err != nil {
		log.Printf("[wiki-ingest] task #%d project %q: LLM call failed: %v", taskID, projectName, err)
		return
	}

	if len(actions) == 0 {
		log.Printf("[wiki-ingest] task #%d project %q: no wiki changes needed", taskID, projectName)
		return
	}

	if err := ApplyIngestActions(projectName, actions); err != nil {
		log.Printf("[wiki-ingest] task #%d project %q: apply failed: %v", taskID, projectName, err)
		return
	}

	_ = GenerateIndex(projectName)
	log.Printf("[wiki-ingest] task #%d project %q: applied %d action(s)", taskID, projectName, len(actions))
}

// buildIngestPrompt creates the LLM prompt for wiki updates.
func buildIngestPrompt(taskPrompt, taskSummary string, filesModified []string, existingPages []*WikiPageContext) string {
	var sb strings.Builder

	sb.WriteString("다음 작업 결과를 분석하여 프로젝트 위키를 업데이트하세요.\n\n")
	sb.WriteString(fmt.Sprintf("작업 내용: %s\n", taskPrompt))
	sb.WriteString(fmt.Sprintf("작업 결과: %s\n", taskSummary))

	if len(filesModified) > 0 {
		sb.WriteString(fmt.Sprintf("변경 파일: %s\n", strings.Join(filesModified, ", ")))
	} else {
		sb.WriteString("변경 파일: 없음\n")
	}

	if len(existingPages) > 0 {
		sb.WriteString("\n현재 위키 페이지:\n")
		for _, p := range existingPages {
			sb.WriteString(fmt.Sprintf("- [%s] %s (category: %s, tags: %s)\n", p.Slug, p.Title, p.Category, strings.Join(p.Tags, ", ")))
		}
	} else {
		sb.WriteString("\n현재 위키 페이지: 없음 (새 프로젝트)\n")
	}

	sb.WriteString(`
지식 추출 가이드:
1. 아키텍처 변경 → architecture.md
2. 코드 패턴/컨벤션 학습 → patterns.md
3. 문제 해결 경험 → troubleshooting.md
4. 배포/인프라 변경 → deployment.md
5. 작업 교훈 → lessons_learned.md
6. 새로운 엔티티 → entities/<name>.md

출력 형식 (JSON 배열):
[
  {
    "action": "create|update|delete",
    "slug": "page-slug",
    "title": "Page Title",
    "category": "architecture|patterns|troubleshooting|deployment|lessons|entity|custom",
    "content": "마크다운 내용",
    "tags": ["tag1", "tag2"]
  }
]

변경 없는 페이지는 생략하세요. 새로운 지식이 없으면 빈 배열을 반환하세요.
`)

	return sb.String()
}

// callLLMForIngest calls the LLM via the registered callback.
func callLLMForIngest(taskID int, projectName, prompt string) ([]IngestAction, error) {
	callbackMu.RLock()
	cb := llmCallback
	callbackMu.RUnlock()

	if cb == nil {
		return nil, fmt.Errorf("LLM callback not registered")
	}

	response, err := cb(prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM call failed: %w", err)
	}

	response = strings.TrimSpace(response)
	if response == "" {
		return nil, nil
	}

	// Strip markdown code block fences if present
	if strings.HasPrefix(response, "```") {
		// Find the closing fence
		endIdx := strings.LastIndex(response, "```")
		if endIdx > 0 {
			response = response[3:endIdx]
			// Also strip optional language tag like "json"
			firstNewline := strings.Index(response, "\n")
			if firstNewline > 0 {
				response = response[firstNewline+1:]
			}
		}
	}

	var actions []IngestAction
	if err := json.Unmarshal([]byte(response), &actions); err != nil {
		return nil, fmt.Errorf("LLM response JSON parse failed: %w (response: %.200s)", err, response)
	}

	return actions, nil
}

// ApplyIngestActions applies LLM-generated actions to wiki pages.
func ApplyIngestActions(projectName string, actions []IngestAction) error {
	for _, action := range actions {
		switch strings.ToLower(action.Action) {
		case "create":
			_, err := CreatePage(projectName, action.Slug, action.Title, action.Category, action.Content, action.Tags)
			if err != nil {
				log.Printf("[wiki-ingest] project %q: create %q failed: %v", projectName, action.Slug, err)
			}
		case "update":
			_, err := UpdatePage(projectName, action.Slug, action.Title, action.Content, action.Tags)
			if err != nil {
				log.Printf("[wiki-ingest] project %q: update %q failed: %v", projectName, action.Slug, err)
			}
		case "delete":
			err := DeletePage(projectName, action.Slug)
			if err != nil {
				log.Printf("[wiki-ingest] project %q: delete %q failed: %v", projectName, action.Slug, err)
			}
		default:
			log.Printf("[wiki-ingest] project %q: unknown action %q for slug %q", projectName, action.Action, action.Slug)
		}
	}
	return nil
}

// TriggerIngest triggers the wiki auto-ingest pipeline in a background goroutine.
// Does not block the caller, so task completion time is not affected.
func TriggerIngest(taskID int, projectName, prompt, summary string, filesModified []string) {
	go func() {
		log.Printf("[wiki-ingest] task #%d project %q: starting auto-ingest", taskID, projectName)
		triggerIngest(taskID, projectName, prompt, summary, filesModified)
	}()
}
