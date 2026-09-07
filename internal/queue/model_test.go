package queue

import "testing"

func TestResolveFollowUpModel(t *testing.T) {
	tests := []struct {
		name           string
		parentOverride string
		usedEndpointID string
		want           string
	}{
		{
			name:           "parent override wins over the endpoint used this run",
			parentOverride: "qwen-coder",
			usedEndpointID: "ep-qwen-27b",
			want:           "qwen-coder",
		},
		{
			name:           "empty parent pins the endpoint that actually served the parent",
			parentOverride: "",
			usedEndpointID: "ep-qwen-27b",
			want:           "ep-qwen-27b",
		},
		{
			name:           "both empty stays empty",
			parentOverride: "",
			usedEndpointID: "",
			want:           "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveFollowUpModel(tt.parentOverride, tt.usedEndpointID)
			if got != tt.want {
				t.Errorf("ResolveFollowUpModel(%q, %q) = %q, want %q",
					tt.parentOverride, tt.usedEndpointID, got, tt.want)
			}
		})
	}
}
