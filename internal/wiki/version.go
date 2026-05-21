package wiki

import (
	"context"
	"fmt"
	"sort"

	"github.com/agurrrrr/shepherd/ent"
	"github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/ent/wikipageversion"
	"github.com/agurrrrr/shepherd/internal/db"
)

// SavePageVersion saves a snapshot of a wiki page's content as a version record.
func SavePageVersion(projectName, pageSlug, content, summary, author string) (*ent.WikiPageVersion, error) {
	ctx := context.Background()
	client := db.Client()

	p, err := findProjectByName(projectName)
	if err != nil {
		return nil, err
	}

	if summary == "" {
		summary = "내용 변경"
	}

	v, err := client.WikiPageVersion.Create().
		SetPageSlug(pageSlug).
		SetContent(content).
		SetSummary(summary).
		SetAuthor(author).
		SetProject(p).
		Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to save wiki page version: %w", err)
	}

	return v, nil
}

// PageVersionHistory returns the version history for a wiki page, ordered by creation time descending.
func PageVersionHistory(projectName, pageSlug string) ([]*ent.WikiPageVersion, error) {
	ctx := context.Background()
	client := db.Client()

	versions, err := client.WikiPageVersion.Query().
		Where(
			wikipageversion.HasProjectWith(project.Name(projectName)),
			wikipageversion.PageSlug(pageSlug),
		).
		WithProject().
		Order(ent.Desc(wikipageversion.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to query wiki page versions: %w", err)
	}

	return versions, nil
}

// PageVersionHistoryFormatted returns the version history as a formatted string for display.
func PageVersionHistoryFormatted(projectName, pageSlug string) (string, error) {
	versions, err := PageVersionHistory(projectName, pageSlug)
	if err != nil {
		return "", err
	}

	if len(versions) == 0 {
		return fmt.Sprintf("페이지 %q 에 대한 버전 히스토리가 없습니다.\n", pageSlug), nil
	}

	// Sort by created_at ascending for display (oldest first)
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].CreatedAt.Before(versions[j].CreatedAt)
	})

	var result string
	result += fmt.Sprintf("pages/%s 변경 이력\n", pageSlug)
	result += "──────────────────────────────\n"
	for i, v := range versions {
		result += fmt.Sprintf("%d. %s — %s\n",
			i+1,
			v.CreatedAt.Format("2006-01-02 15:04"),
			v.Summary,
		)
		if v.Author != "" {
			result += fmt.Sprintf("   by %s\n", v.Author)
		}
	}

	return result, nil
}
