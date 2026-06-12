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
