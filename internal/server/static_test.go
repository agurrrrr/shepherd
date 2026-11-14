package server

import (
	"os"
	"testing"
)

// TestWebDistEmbedPlaceholder guards issue #840.
//
// static.go declares `//go:embed all:web_dist`. go:embed is a compile-time
// directive, so if that directory holds no files the whole package fails to
// build with "pattern all:web_dist: no matching files found". web_dist is
// gitignored build output, so on a fresh clone it does not exist at all. One
// tracked placeholder file (web_dist/.gitkeep) is what keeps `go build ./...`
// and `go test ./...` working without a prior Web UI build.
//
// Commit c2952a8 deleted that placeholder and broke every clean clone. This test
// fails loudly if it disappears again, instead of leaving the failure to be found
// by someone cloning the repository.
//
// The compile error alone is not enough of a guard: CI and `make build` copy
// web/build into web_dist first, so in those environments the embed always has
// files and a deleted .gitkeep stays invisible. The test runs against the tree as
// checked out, which is the only place the clean-clone case is observable.
func TestWebDistEmbedPlaceholder(t *testing.T) {
	const placeholder = "web_dist/.gitkeep"

	info, err := os.Stat(placeholder)
	if err != nil {
		t.Fatalf("%s is missing: a clean clone cannot compile //go:embed all:web_dist (issue #840). Restore the file.", placeholder)
	}
	if info.IsDir() {
		t.Fatalf("%s must be a file, not a directory", placeholder)
	}
}

// TestWebDistFSContract checks what WebDistFS returns in the two valid states:
// no Web UI build (placeholder only) and a built Web UI. It must never return a
// non-nil filesystem that cannot serve index.html, because server.New mounts the
// result as the SPA root.
func TestWebDistFSContract(t *testing.T) {
	sub := WebDistFS()
	if sub == nil {
		// Development checkout without a Web UI build: API-only serving.
		return
	}

	f, err := sub.Open("index.html")
	if err != nil {
		t.Fatalf("WebDistFS returned a filesystem without index.html: %v", err)
	}
	f.Close()
}
