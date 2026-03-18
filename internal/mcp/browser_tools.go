package mcp

import (
	"fmt"
	"time"

	"github.com/agurrrrr/shepherd/internal/browser"
)

// 브라우저 MCP 도구들

// registerBrowserTools 브라우저 관련 도구 핸들러 등록
func (s *Server) registerBrowserTools() {
	// 세션 관리
	s.tools["browser_session_start"] = handleBrowserSessionStart
	s.tools["browser_session_stop"] = handleBrowserSessionStop
	s.tools["browser_list_pages"] = handleBrowserListPages

	// 페이지 제어
	s.tools["browser_open"] = handleBrowserOpen
	s.tools["browser_close"] = handleBrowserClose
	s.tools["browser_navigate"] = handleBrowserNavigate
	s.tools["browser_reload"] = handleBrowserReload
	s.tools["browser_back"] = handleBrowserBack
	s.tools["browser_forward"] = handleBrowserForward

	// 요소 상호작용
	s.tools["browser_click"] = handleBrowserClick
	s.tools["browser_type"] = handleBrowserType
	s.tools["browser_select"] = handleBrowserSelect
	s.tools["browser_check"] = handleBrowserCheck
	s.tools["browser_hover"] = handleBrowserHover
	s.tools["browser_scroll"] = handleBrowserScroll

	// 정보 추출
	s.tools["browser_get_text"] = handleBrowserGetText
	s.tools["browser_get_html"] = handleBrowserGetHTML
	s.tools["browser_get_attribute"] = handleBrowserGetAttribute
	s.tools["browser_get_url"] = handleBrowserGetURL
	s.tools["browser_get_title"] = handleBrowserGetTitle
	s.tools["browser_eval"] = handleBrowserEval

	// 대기
	s.tools["browser_wait_selector"] = handleBrowserWaitSelector
	s.tools["browser_wait_hidden"] = handleBrowserWaitHidden
	s.tools["browser_wait_load"] = handleBrowserWaitLoad
	s.tools["browser_wait_idle"] = handleBrowserWaitIdle

	// 캡처
	s.tools["browser_screenshot"] = handleBrowserScreenshot
	s.tools["browser_pdf"] = handleBrowserPDF

	// 디버그
	s.tools["browser_console_start"] = handleBrowserConsoleStart
	s.tools["browser_console_messages"] = handleBrowserConsoleMessages
	s.tools["browser_network_start"] = handleBrowserNetworkStart
	s.tools["browser_network_requests"] = handleBrowserNetworkRequests
	s.tools["browser_network_request"] = handleBrowserNetworkRequest
}

// getBrowserToolsList 브라우저 도구 목록 반환
func getBrowserToolsList() []Tool {
	return []Tool{
		// 세션 관리
		{
			Name:        "browser_session_start",
			Description: "브라우저 세션을 시작합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름 (필수)"},
					"headless":   {Type: "boolean", Description: "헤드리스 모드 (기본 true)"},
					"proxy":      {Type: "string", Description: "프록시 URL (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_session_stop",
			Description: "브라우저 세션을 종료합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_list_pages",
			Description: "열린 페이지 목록을 반환합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
				},
				Required: []string{"sheep_name"},
			},
		},
		// 페이지 제어
		{
			Name:        "browser_open",
			Description: "페이지를 열고 URL로 이동합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"url":        {Type: "string", Description: "열 URL"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "url"},
			},
		},
		{
			Name:        "browser_close",
			Description: "페이지를 닫습니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_navigate",
			Description: "URL로 이동합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"url":        {Type: "string", Description: "이동할 URL"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "url"},
			},
		},
		{
			Name:        "browser_reload",
			Description: "페이지를 새로고침합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_back",
			Description: "뒤로 가기",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_forward",
			Description: "앞으로 가기",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		// 요소 상호작용
		{
			Name:        "browser_click",
			Description: "요소를 클릭합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"selector":   {Type: "string", Description: "CSS 선택자"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "selector"},
			},
		},
		{
			Name:        "browser_type",
			Description: "텍스트를 입력합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"selector":   {Type: "string", Description: "CSS 선택자"},
					"text":       {Type: "string", Description: "입력할 텍스트"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "selector", "text"},
			},
		},
		{
			Name:        "browser_select",
			Description: "드롭다운을 선택합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"selector":   {Type: "string", Description: "CSS 선택자"},
					"value":      {Type: "string", Description: "선택할 값"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "selector", "value"},
			},
		},
		{
			Name:        "browser_check",
			Description: "체크박스를 설정합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"selector":   {Type: "string", Description: "CSS 선택자"},
					"checked":    {Type: "boolean", Description: "체크 여부"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "selector", "checked"},
			},
		},
		{
			Name:        "browser_hover",
			Description: "요소에 마우스를 올립니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"selector":   {Type: "string", Description: "CSS 선택자"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "selector"},
			},
		},
		{
			Name:        "browser_scroll",
			Description: "스크롤합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"selector":   {Type: "string", Description: "스크롤할 요소 (선택, 없으면 페이지 전체)"},
					"x":          {Type: "number", Description: "가로 스크롤 양"},
					"y":          {Type: "number", Description: "세로 스크롤 양"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		// 정보 추출
		{
			Name:        "browser_get_text",
			Description: "요소의 텍스트를 가져옵니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"selector":   {Type: "string", Description: "CSS 선택자"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "selector"},
			},
		},
		{
			Name:        "browser_get_html",
			Description: "HTML을 가져옵니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"selector":   {Type: "string", Description: "CSS 선택자 (선택, 없으면 전체)"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_get_attribute",
			Description: "요소의 속성값을 가져옵니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"selector":   {Type: "string", Description: "CSS 선택자"},
					"attribute":  {Type: "string", Description: "속성 이름"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "selector", "attribute"},
			},
		},
		{
			Name:        "browser_get_url",
			Description: "현재 URL을 가져옵니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_get_title",
			Description: "페이지 제목을 가져옵니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_eval",
			Description: "JavaScript를 실행합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"js":         {Type: "string", Description: "실행할 JavaScript 코드"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "js"},
			},
		},
		// 대기
		{
			Name:        "browser_wait_selector",
			Description: "요소가 나타날 때까지 대기합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"selector":   {Type: "string", Description: "CSS 선택자"},
					"timeout":    {Type: "number", Description: "타임아웃 (초, 기본 30)"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "selector"},
			},
		},
		{
			Name:        "browser_wait_hidden",
			Description: "요소가 사라질 때까지 대기합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"selector":   {Type: "string", Description: "CSS 선택자"},
					"timeout":    {Type: "number", Description: "타임아웃 (초, 기본 30)"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name", "selector"},
			},
		},
		{
			Name:        "browser_wait_load",
			Description: "페이지 로드가 완료될 때까지 대기합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_wait_idle",
			Description: "네트워크 요청이 완료될 때까지 대기합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"timeout":    {Type: "number", Description: "타임아웃 (초, 기본 30)"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		// 캡처
		{
			Name:        "browser_screenshot",
			Description: "스크린샷을 캡처합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"path":       {Type: "string", Description: "저장 경로 (선택)"},
					"selector":   {Type: "string", Description: "캡처할 요소 선택자 (선택, 없으면 전체)"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_pdf",
			Description: "PDF를 생성합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"path":       {Type: "string", Description: "저장 경로 (선택)"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		// 디버그
		{
			Name:        "browser_console_start",
			Description: "콘솔 메시지 수집을 시작합니다 (log, warn, error 등)",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_console_messages",
			Description: "수집된 콘솔 메시지를 조회합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"clear":      {Type: "boolean", Description: "조회 후 메시지 삭제 (기본 false)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_network_start",
			Description: "네트워크 요청 모니터링을 시작합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"page_name":  {Type: "string", Description: "페이지 이름 (선택)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_network_requests",
			Description: "수집된 네트워크 요청 목록을 조회합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"clear":      {Type: "boolean", Description: "조회 후 목록 삭제 (기본 false)"},
				},
				Required: []string{"sheep_name"},
			},
		},
		{
			Name:        "browser_network_request",
			Description: "특정 네트워크 요청의 상세 정보를 조회합니다",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]Property{
					"sheep_name": {Type: "string", Description: "양 이름"},
					"request_id": {Type: "string", Description: "요청 ID"},
				},
				Required: []string{"sheep_name", "request_id"},
			},
		},
	}
}

// 핸들러 함수들

func handleBrowserSessionStart(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	headless := true
	if h, ok := args["headless"].(bool); ok {
		headless = h
	}

	proxy, _ := args["proxy"].(string)

	opts := &browser.SessionOptions{
		Headless: headless,
		Proxy:    proxy,
	}

	mgr := browser.GetManager()
	sess, err := mgr.GetOrCreateSession(sheepName, opts)
	if err != nil {
		return "", err
	}

	info := sess.Info()
	return fmt.Sprintf("브라우저 세션 시작됨 (양: %s, 헤드리스: %v)", info.SheepName, info.Headless), nil
}

func handleBrowserSessionStop(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	if err := mgr.CloseSession(sheepName); err != nil {
		return "", err
	}

	return fmt.Sprintf("브라우저 세션 종료됨 (양: %s)", sheepName), nil
}

func handleBrowserListPages(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	pages := sess.ListPages()
	if len(pages) == 0 {
		return "열린 페이지 없음", nil
	}

	result := fmt.Sprintf("열린 페이지 (%d개):\n", len(pages))
	for _, p := range pages {
		defaultMark := ""
		if p.IsDefault {
			defaultMark = " (기본)"
		}
		result += fmt.Sprintf("  - %s%s: %s\n", p.Name, defaultMark, p.URL)
	}
	return result, nil
}

func handleBrowserOpen(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	url, _ := args["url"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" || url == "" {
		return "", fmt.Errorf("sheep_name과 url이 필요합니다")
	}

	mgr := browser.GetManager()
	sess, err := mgr.GetOrCreateSession(sheepName, nil)
	if err != nil {
		return "", err
	}

	page, err := sess.OpenPage(url, pageName)
	if err != nil {
		return "", err
	}

	info, _ := page.Info()
	return fmt.Sprintf("페이지 열림: %s", info.Title), nil
}

func handleBrowserClose(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := sess.ClosePage(pageName); err != nil {
		return "", err
	}

	return "페이지 닫힘", nil
}

func handleBrowserNavigate(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	url, _ := args["url"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" || url == "" {
		return "", fmt.Errorf("sheep_name과 url이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.Navigate(sess, pageName, url); err != nil {
		return "", err
	}

	return fmt.Sprintf("이동 완료: %s", url), nil
}

func handleBrowserReload(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.Reload(sess, pageName); err != nil {
		return "", err
	}

	return "새로고침 완료", nil
}

func handleBrowserBack(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.GoBack(sess, pageName); err != nil {
		return "", err
	}

	return "뒤로 가기 완료", nil
}

func handleBrowserForward(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.GoForward(sess, pageName); err != nil {
		return "", err
	}

	return "앞으로 가기 완료", nil
}

func handleBrowserClick(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	selector, _ := args["selector"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" || selector == "" {
		return "", fmt.Errorf("sheep_name과 selector가 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.Click(sess, pageName, selector); err != nil {
		return "", err
	}

	return fmt.Sprintf("클릭 완료: %s", selector), nil
}

func handleBrowserType(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	selector, _ := args["selector"].(string)
	text, _ := args["text"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" || selector == "" {
		return "", fmt.Errorf("sheep_name과 selector가 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.Type(sess, pageName, selector, text); err != nil {
		return "", err
	}

	return fmt.Sprintf("입력 완료: %s", selector), nil
}

func handleBrowserSelect(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	selector, _ := args["selector"].(string)
	value, _ := args["value"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" || selector == "" || value == "" {
		return "", fmt.Errorf("sheep_name, selector, value가 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.SelectOption(sess, pageName, selector, value); err != nil {
		return "", err
	}

	return fmt.Sprintf("선택 완료: %s = %s", selector, value), nil
}

func handleBrowserCheck(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	selector, _ := args["selector"].(string)
	checked, _ := args["checked"].(bool)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" || selector == "" {
		return "", fmt.Errorf("sheep_name과 selector가 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.SetCheckbox(sess, pageName, selector, checked); err != nil {
		return "", err
	}

	return fmt.Sprintf("체크박스 설정 완료: %s = %v", selector, checked), nil
}

func handleBrowserHover(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	selector, _ := args["selector"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" || selector == "" {
		return "", fmt.Errorf("sheep_name과 selector가 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.Hover(sess, pageName, selector); err != nil {
		return "", err
	}

	return fmt.Sprintf("호버 완료: %s", selector), nil
}

func handleBrowserScroll(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	selector, _ := args["selector"].(string)
	pageName, _ := args["page_name"].(string)

	x := 0.0
	y := 0.0
	if xVal, ok := args["x"].(float64); ok {
		x = xVal
	}
	if yVal, ok := args["y"].(float64); ok {
		y = yVal
	}

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if selector != "" {
		if err := browser.ScrollToElement(sess, pageName, selector); err != nil {
			return "", err
		}
		return fmt.Sprintf("요소로 스크롤 완료: %s", selector), nil
	}

	if err := browser.Scroll(sess, pageName, x, y); err != nil {
		return "", err
	}

	return fmt.Sprintf("스크롤 완료: x=%.0f, y=%.0f", x, y), nil
}

func handleBrowserGetText(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	selector, _ := args["selector"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" || selector == "" {
		return "", fmt.Errorf("sheep_name과 selector가 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	text, err := browser.GetText(sess, pageName, selector)
	if err != nil {
		return "", err
	}

	return text, nil
}

func handleBrowserGetHTML(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	selector, _ := args["selector"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	html, err := browser.GetHTML(sess, pageName, selector)
	if err != nil {
		return "", err
	}

	return html, nil
}

func handleBrowserGetAttribute(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	selector, _ := args["selector"].(string)
	attribute, _ := args["attribute"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" || selector == "" || attribute == "" {
		return "", fmt.Errorf("sheep_name, selector, attribute가 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	val, err := browser.GetAttribute(sess, pageName, selector, attribute)
	if err != nil {
		return "", err
	}

	return val, nil
}

func handleBrowserGetURL(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	url, err := browser.GetURL(sess, pageName)
	if err != nil {
		return "", err
	}

	return url, nil
}

func handleBrowserGetTitle(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	title, err := browser.GetTitle(sess, pageName)
	if err != nil {
		return "", err
	}

	return title, nil
}

func handleBrowserEval(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	js, _ := args["js"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" || js == "" {
		return "", fmt.Errorf("sheep_name과 js가 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	result, err := browser.Eval(sess, pageName, js)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%v", result), nil
}

func handleBrowserWaitSelector(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	selector, _ := args["selector"].(string)
	pageName, _ := args["page_name"].(string)

	timeout := 30 * time.Second
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
	}

	if sheepName == "" || selector == "" {
		return "", fmt.Errorf("sheep_name과 selector가 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.WaitSelector(sess, pageName, selector, timeout); err != nil {
		return "", err
	}

	return fmt.Sprintf("요소 발견: %s", selector), nil
}

func handleBrowserWaitHidden(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	selector, _ := args["selector"].(string)
	pageName, _ := args["page_name"].(string)

	timeout := 30 * time.Second
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
	}

	if sheepName == "" || selector == "" {
		return "", fmt.Errorf("sheep_name과 selector가 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.WaitHidden(sess, pageName, selector, timeout); err != nil {
		return "", err
	}

	return fmt.Sprintf("요소 사라짐: %s", selector), nil
}

func handleBrowserWaitLoad(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.WaitLoad(sess, pageName); err != nil {
		return "", err
	}

	return "페이지 로드 완료", nil
}

func handleBrowserWaitIdle(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	pageName, _ := args["page_name"].(string)

	timeout := 30 * time.Second
	if t, ok := args["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
	}

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	if err := browser.WaitIdle(sess, pageName, timeout); err != nil {
		return "", err
	}

	return "네트워크 요청 완료", nil
}

func handleBrowserScreenshot(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	path, _ := args["path"].(string)
	selector, _ := args["selector"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	data, err := browser.Screenshot(sess, pageName, selector, path)
	if err != nil {
		return "", err
	}

	if path != "" {
		return fmt.Sprintf("스크린샷 저장됨: %s (%d bytes)", path, len(data)), nil
	}
	return fmt.Sprintf("스크린샷 캡처됨 (%d bytes)", len(data)), nil
}

func handleBrowserPDF(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	path, _ := args["path"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	data, err := browser.PDF(sess, pageName, path)
	if err != nil {
		return "", err
	}

	if path != "" {
		return fmt.Sprintf("PDF 저장됨: %s (%d bytes)", path, len(data)), nil
	}
	return fmt.Sprintf("PDF 생성됨 (%d bytes)", len(data)), nil
}

// 디버그 핸들러

func handleBrowserConsoleStart(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	page := sess.GetPage(pageName)
	if page == nil {
		return "", fmt.Errorf("페이지 없음: %s", pageName)
	}

	if err := sess.Debug.StartConsoleCapture(page); err != nil {
		return "", err
	}

	return "콘솔 메시지 수집 시작됨", nil
}

func handleBrowserConsoleMessages(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	clear, _ := args["clear"].(bool)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	messages := sess.Debug.GetConsoleMessages()
	if clear {
		sess.Debug.ClearConsoleMessages()
	}

	if len(messages) == 0 {
		return "콘솔 메시지 없음", nil
	}

	result := fmt.Sprintf("콘솔 메시지 (%d개):\n", len(messages))
	for _, m := range messages {
		result += fmt.Sprintf("  [%s] %s  (%s)\n", m.Type, m.Text, m.Timestamp.Format("15:04:05"))
	}
	return result, nil
}

func handleBrowserNetworkStart(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	pageName, _ := args["page_name"].(string)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	page := sess.GetPage(pageName)
	if page == nil {
		return "", fmt.Errorf("페이지 없음: %s", pageName)
	}

	if err := sess.Debug.StartNetworkCapture(page); err != nil {
		return "", err
	}

	return "네트워크 요청 모니터링 시작됨", nil
}

func handleBrowserNetworkRequests(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	clear, _ := args["clear"].(bool)

	if sheepName == "" {
		return "", fmt.Errorf("sheep_name이 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	requests := sess.Debug.GetNetworkRequests()
	if clear {
		sess.Debug.ClearNetworkRequests()
	}

	if len(requests) == 0 {
		return "네트워크 요청 없음", nil
	}

	result := fmt.Sprintf("네트워크 요청 (%d개):\n", len(requests))
	for _, r := range requests {
		status := ""
		if r.Status > 0 {
			status = fmt.Sprintf(" → %d %s (%.0fms)", r.Status, r.StatusText, r.Duration)
		}
		result += fmt.Sprintf("  [%s] %s %s%s\n", r.RequestID, r.Method, r.URL, status)
	}
	return result, nil
}

func handleBrowserNetworkRequest(args map[string]interface{}) (string, error) {
	sheepName, _ := args["sheep_name"].(string)
	requestID, _ := args["request_id"].(string)

	if sheepName == "" || requestID == "" {
		return "", fmt.Errorf("sheep_name과 request_id가 필요합니다")
	}

	mgr := browser.GetManager()
	sess := mgr.GetSession(sheepName)
	if sess == nil {
		return "", fmt.Errorf("세션 없음: %s", sheepName)
	}

	entry, ok := sess.Debug.GetNetworkRequest(requestID)
	if !ok {
		return "", fmt.Errorf("요청 없음: %s", requestID)
	}

	result := fmt.Sprintf("요청 상세:\n")
	result += fmt.Sprintf("  ID: %s\n", entry.RequestID)
	result += fmt.Sprintf("  Method: %s\n", entry.Method)
	result += fmt.Sprintf("  URL: %s\n", entry.URL)
	result += fmt.Sprintf("  Type: %s\n", entry.Type)
	result += fmt.Sprintf("  Status: %d %s\n", entry.Status, entry.StatusText)
	result += fmt.Sprintf("  MIME: %s\n", entry.MIMEType)
	result += fmt.Sprintf("  Duration: %.0fms\n", entry.Duration)
	result += fmt.Sprintf("  Timestamp: %s\n", entry.Timestamp.Format(time.RFC3339))
	return result, nil
}
