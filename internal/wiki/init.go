package wiki

import (
	"fmt"
	"os"

	"github.com/agurrrrr/shepherd/ent/wikipage"
)

// InitPage defines a default wiki page template.
type InitPage struct {
	Slug     string
	Title    string
	Category wikipage.Category
	Content  string
}

// defaultPages defines the initial wiki pages created for a new project.
var defaultPages = []InitPage{
	{
		Slug:     "architecture",
		Title:    "Architecture",
		Category: wikipage.CategoryArchitecture,
		Content: `아직 기록된 아키텍처 정보가 없습니다.

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
	},
	{
		Slug:     "patterns",
		Title:    "Code Patterns",
		Category: wikipage.CategoryPatterns,
		Content: `아직 학습된 코드 패턴이 없습니다.

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
	},
	{
		Slug:     "troubleshooting",
		Title:    "Troubleshooting",
		Category: wikipage.CategoryTroubleshooting,
		Content: `아직 기록된 문제 해결 사례가 없습니다.

발생한 문제와 해결 방법을 기록하여 나중에 참조할 수 있게 하세요:

## 빈번한 오류
| 오류 메시지 | 원인 | 해결 방법 |
|------------|------|----------|

## 환경 설정 문제
- 

## 디버깅 팁
- 
`,
	},
	{
		Slug:     "lessons_learned",
		Title:    "Lessons Learned",
		Category: wikipage.CategoryLessons,
		Content: `아직 기록된 교훈이 없습니다.

작업 중 발견한 교훈과 개선 사항을 기록하세요:

## 설계 교훈
- 

## 개발 과정 교훈
- 

## 배포 및 운영 교훈
- 
`,
	},
}

// InitializeWiki creates default wiki pages for a project.
// Pages that already exist are skipped without error.
func InitializeWiki(projectName string) error {
	if projectName == "" {
		return fmt.Errorf("project name is required")
	}

	// Create wiki directory
	wikiDir := WikiDir(projectName)
	if err := os.MkdirAll(wikiDir, 0755); err != nil {
		return fmt.Errorf("failed to create wiki directory: %w", err)
	}

	for _, page := range defaultPages {
		existing, err := findPageByProjectAndSlug(projectName, page.Slug)
		if err != nil {
			continue
		}
		if existing != nil {
			continue // Skip existing pages
		}

		_, err = CreatePage(projectName, page.Slug, page.Title, string(page.Category), page.Content, nil)
		if err != nil {
			// Log but don't fail on individual page creation errors
			continue
		}
	}

	// Generate index
	_ = GenerateIndex(projectName)

	return nil
}

// WikiInitialized checks whether a project has default wiki pages.
func WikiInitialized(projectName string) bool {
	pages, err := ListPages(projectName)
	if err != nil {
		return false
	}
	return len(pages) > 0
}

// InitializeWikiFromFileSystem initializes wiki pages from existing .md files
// in the project's wiki directory, without creating default templates.
func InitializeWikiFromFileSystem(projectName string) error {
	if projectName == "" {
		return fmt.Errorf("project name is required")
	}

	wikiDir := WikiDir(projectName)
	if _, err := os.Stat(wikiDir); os.IsNotExist(err) {
		return nil
	}

	// Sync existing files from disk to DB
	if err := SyncFromDisk(projectName); err != nil {
		return fmt.Errorf("failed to sync wiki from disk: %w", err)
	}

	_ = GenerateIndex(projectName)
	return nil
}
