package wiki

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/agurrrrr/shepherd/internal/config"
)

var templateVarRe = regexp.MustCompile(`\{\{(\w+)\}\}`)

// ProcessTemplate replaces template variables in the given template string.
// Supported variables: title, slug, date, content.
func ProcessTemplate(template string, data map[string]string) string {
	result := templateVarRe.ReplaceAllStringFunc(template, func(match string) string {
		key := templateVarRe.FindStringSubmatch(match)[1]
		if val, ok := data[key]; ok {
			return val
		}
		return match
	})
	return result
}

// NewTemplateData returns a map of template variables for page creation.
func NewTemplateData(title, slug string) map[string]string {
	return map[string]string{
		"title":   title,
		"slug":    slug,
		"date":    time.Now().Format("2006-01-02"),
		"content": "",
	}
}

// NewTemplateDataWithContent returns a map of template variables including content.
func NewTemplateDataWithContent(title, slug, content string) map[string]string {
	data := NewTemplateData(title, slug)
	data["content"] = content
	return data
}

// ResolveTemplate looks up a template by name in the following order:
// 1. Project-level .shepherd/templates/ directory
// 2. Global config wiki.templates section
// 3. Built-in default template
//
// Returns the resolved template string.
func ResolveTemplate(projectName, templateName string) string {
	if templateName == "" {
		templateName = "default"
	}

	// 1. Project-level template files
	if tpl := loadProjectTemplate(projectName, templateName); tpl != "" {
		return tpl
	}

	// 2. Config-based template
	if tpl := loadConfigTemplate(templateName); tpl != "" {
		return tpl
	}

	// 3. Built-in default
	return builtinTemplates[templateName]
}

// LoadProjectTemplates scans the project .shepherd/templates/ directory and returns
// all available template names.
func LoadProjectTemplates(projectName string) []string {
	tplDir := ProjectTemplatesDir(projectName)
	if _, err := os.Stat(tplDir); os.IsNotExist(err) {
		return nil
	}

	entries, err := os.ReadDir(tplDir)
	if err != nil {
		return nil
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		base := strings.TrimSuffix(e.Name(), ".md")
		base = strings.TrimSuffix(base, ".tmpl")
		names = append(names, base)
	}
	return names
}

// ProjectTemplatesDir returns the project-level template directory path.
// Exposed as a variable for testability.
var ProjectTemplatesDir = func(projectName string) string {
	return filepath.Join(config.GetConfigDir(), "projects", projectName, "templates")
}

// loadProjectTemplate reads a template file from the project's .shepherd/templates/ directory.
// It tries both .md and .tmpl extensions.
func loadProjectTemplate(projectName, name string) string {
	tplDir := ProjectTemplatesDir(projectName)
	for _, ext := range []string{".md", ".tmpl"} {
		path := filepath.Join(tplDir, name+ext)
		if data, err := os.ReadFile(path); err == nil {
			return string(data)
		}
	}
	return ""
}

// loadConfigTemplate retrieves a template from the global config (wiki.templates section).
func loadConfigTemplate(name string) string {
	key := "wiki.templates." + name
	val := config.Get(key)
	if val == nil {
		return ""
	}
	if s, ok := val.(string); ok {
		return s
	}
	return ""
}

// BuiltinTemplateNames returns the set of built-in template names.
func BuiltinTemplateNames() map[string]bool {
	names := make(map[string]bool, len(builtinTemplates))
	for k := range builtinTemplates {
		names[k] = true
	}
	return names
}

// builtinTemplates holds the default built-in wiki page templates.
var builtinTemplates = map[string]string{
	"default": `# {{title}}

{{content}}`,
	"new_page": `---
title: {{title}}
created: {{date}}
tags: []
---

{{content}}`,
	"architecture": `# {{title}}

아직 기록된 아키텍처 정보가 없습니다.

프로젝트 작업을 수행하면서 아래 항목들을 기록해 주세요:

## 기술 스택
- 언어 / 프레임워크:
- 데이터베이스:
- 외부 서비스:

## 서비스 구조
- 주요 컴포넌트와 역할:

## 데이터 흐름
- 요청 처리 파이프라인:

## 배포 아키텍처
- 인프라 구성:
`,
	"patterns": `# {{title}}

아직 학습된 코드 패턴이 없습니다.

프로젝트에서 반복적으로 나타나는 코드 패턴과 관례를 기록하세요:

## 네이밍 규칙
-

## 디렉토리 구조
-

## 에러 처리 패턴
-

## 테스트 관례
-

## 코드 스타일 가이드
-
`,
	"troubleshooting": `# {{title}}

아직 기록된 문제 해결 사례가 없습니다.

발생한 문제와 해결 방법을 기록하여 나중에 참조할 수 있게 하세요:

## 빈번한 오류
| 오류 메시지 | 원인 | 해결 방법 |
|------------|------|----------|

## 환경 설정 문제
-

## 디버깅 팁
-
`,
	"lessons_learned": `# {{title}}

아직 기록된 교훈이 없습니다.

작업 중 발견한 교훈과 개선 사항을 기록하세요:

## 설계 교훈
-

## 개발 과정 교훈
-

## 배포 및 운영 교훈
-
`,
}
