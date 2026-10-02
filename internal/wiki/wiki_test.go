package wiki

import (
	"testing"
	"time"

	"github.com/agurrrrr/shepherd/ent"
)

// Test_sortPagesByCreated_최근작성순은 작성 시각 기준 내림차순을 보장하고,
// 같은 작성 시각에는 slug 오름차순으로 결정적인 순서를 만들어야 한다.
func Test_sortPagesByCreated_최근작성순_생성시간기준_내림차순(t *testing.T) {
	base := time.Date(2026, 1, 30, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		pages  []*ent.WikiPage
		want   []string
	}{
		{
			name: "작성 순 최근 우선",
			pages: []*ent.WikiPage{
				{Slug: "old", CreatedAt: base.Add(-48 * time.Hour)},
				{Slug: "mid", CreatedAt: base.Add(-24 * time.Hour)},
				{Slug: "new", CreatedAt: base},
			},
			want: []string{"new", "mid", "old"},
		},
		{
			name: "갱신 시각은 정렬에 영향을 주지 않는다",
			pages: []*ent.WikiPage{
				{Slug: "created-later", CreatedAt: base, UpdatedAt: base.Add(-72 * time.Hour)},
				{Slug: "created-earlier", CreatedAt: base.Add(-10 * time.Hour), UpdatedAt: base.Add(-1 * time.Hour)},
			},
			want: []string{"created-later", "created-earlier"},
		},
		{
			name: "같은 작성 시각에는 slug 오름차순",
			pages: []*ent.WikiPage{
				{Slug: "b-same", CreatedAt: base},
				{Slug: "a-same", CreatedAt: base},
				{Slug: "c-same", CreatedAt: base},
			},
			want: []string{"a-same", "b-same", "c-same"},
		},
		{
			name:  "빈 슬라이스",
			pages: nil,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sortPagesByCreated(tt.pages)

			got := make([]string, len(tt.pages))
			for i, p := range tt.pages {
				got[i] = p.Slug
			}

			if len(got) != len(tt.want) {
				t.Fatalf("sortPagesByCreated() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("sortPagesByCreated() = %v, want %v", got, tt.want)
					return
				}
			}
		})
	}
}
