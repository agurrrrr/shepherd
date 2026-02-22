package server

import (
	"embed"
	"io/fs"
)

//go:embed all:web_dist
var webDistFS embed.FS

// WebDistFS returns the embedded web UI filesystem.
// Returns nil if the embed directory is empty (e.g., during development without a build).
func WebDistFS() fs.FS {
	sub, err := fs.Sub(webDistFS, "web_dist")
	if err != nil {
		return nil
	}
	// Check if index.html exists to verify the embed has content
	f, err := sub.Open("index.html")
	if err != nil {
		return nil
	}
	f.Close()
	return sub
}
