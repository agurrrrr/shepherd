package project

import (
	"context"
	"os/exec"
	"regexp"
	"strings"

	entProject "github.com/agurrrrr/shepherd/ent/project"
	"github.com/agurrrrr/shepherd/internal/db"
)

var sshRemoteRe = regexp.MustCompile(`^git@([^:]+):(.+?)(?:\.git)?$`)

// GitRepoURL returns a GitHub/GitLab HTTPS URL from a project path, or "".
// It shells out to git, so prefer the cached repo_url field when available.
func GitRepoURL(projectPath string) string {
	cmd := exec.Command("git", "-C", projectPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" {
		return ""
	}
	// SSH format: git@github.com:user/repo.git
	if m := sshRemoteRe.FindStringSubmatch(raw); m != nil {
		return "https://" + m[1] + "/" + m[2]
	}
	// HTTPS format: https://github.com/user/repo.git
	url := strings.TrimSuffix(raw, ".git")
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "http://") {
		return url
	}
	return ""
}

// SetRepoURL persists the cached repo URL for a project by name.
func SetRepoURL(name, url string) error {
	ctx := context.Background()
	_, err := db.Client().Project.Update().
		Where(entProject.Name(name)).
		SetRepoURL(url).
		Save(ctx)
	return err
}

// BackfillRepoURLs caches repo URLs for projects that were registered before
// the field existed (or whose remote changed later). It is meant to run once at
// daemon startup, in the background, so list handlers never shell out to git per
// project on every request.
func BackfillRepoURLs() (int, error) {
	ctx := context.Background()
	client := db.Client()

	projects, err := client.Project.Query().
		Where(entProject.Or(
			entProject.RepoURLEQ(""),
			entProject.RepoURLIsNil(),
		)).
		All(ctx)
	if err != nil {
		return 0, err
	}

	updated := 0
	for _, p := range projects {
		url := GitRepoURL(p.Path)
		if url == "" {
			continue
		}
		if _, err := client.Project.UpdateOneID(p.ID).SetRepoURL(url).Save(ctx); err != nil {
			continue
		}
		updated++
	}

	return updated, nil
}
