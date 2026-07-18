package worker

import "testing"

func TestIsStaleSessionError(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		// Real message from #7626 / #7630
		{
			name: "no conversation found with session ID",
			msg:  "Claude Code execution failed: No conversation found with session ID: 019f6af0-abcd-1234",
			want: true,
		},
		{
			name: "no conversation found (short)",
			msg:  "No conversation found",
			want: true,
		},
		{
			name: "resume requires valid session id",
			msg:  "error: --resume requires a valid session id",
			want: true,
		},
		{
			name: "does not match any session",
			msg:  "Session abc does not match any session",
			want: true,
		},
		// Case insensitivity
		{
			name: "uppercase conversation message",
			msg:  "NO CONVERSATION FOUND WITH SESSION ID: XYZ",
			want: true,
		},
		// Non-stale errors must not match
		{
			name: "generic execution failure",
			msg:  "Claude Code execution failed: exit status 1",
			want: false,
		},
		{
			name: "timeout",
			msg:  "execution timeout (exceeded 10m0s)",
			want: false,
		},
		{
			name: "rate limit",
			msg:  "rate limit exceeded, try again later",
			want: false,
		},
		{
			name: "empty",
			msg:  "",
			want: false,
		},
		{
			name: "unrelated session wording",
			msg:  "saved session successfully",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStaleSessionError(tt.msg); got != tt.want {
				t.Errorf("isStaleSessionError(%q) = %v, want %v", tt.msg, got, tt.want)
			}
		})
	}
}
