package checkpoint

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	gitRun(t, dir, "config", "user.name", "test")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	// Git for Windows ships core.autocrlf=true in its system config, which makes
	// `git apply` write CRLF and breaks the byte-exact content checks below.
	gitRun(t, dir, "config", "core.autocrlf", "false")
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitRaw(t, dir, nil, args...)
	if err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(out)
}

func gitRaw(t *testing.T, dir string, stdin []byte, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"LC_ALL=C",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func commitAll(t *testing.T, dir, message string) string {
	t.Helper()
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", message)
	return gitRun(t, dir, "rev-parse", "HEAD")
}

func changesByPath(t *testing.T, changes []FileChange) map[string]FileChange {
	t.Helper()
	m := make(map[string]FileChange, len(changes))
	for _, c := range changes {
		m[c.Path] = c
	}
	return m
}

func TestProbeAndCommitExists(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "sub/f.txt", "x\n")
	head := commitAll(t, repo, "init")

	top, prefix, err := Probe(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if top == "" || prefix != "" {
		t.Fatalf("Probe(repo) = %q, %q; want non-empty top and empty prefix", top, prefix)
	}

	subTop, subPrefix, err := Probe(ctx, filepath.Join(repo, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if subTop != top || subPrefix != "sub" {
		t.Fatalf("Probe(sub) = %q, %q; want %q, %q", subTop, subPrefix, top, "sub")
	}

	if _, _, err := Probe(ctx, t.TempDir()); !errors.Is(err, ErrNotGitRepo) {
		t.Fatalf("Probe(non-repo) error = %v; want ErrNotGitRepo", err)
	}

	if !CommitExists(ctx, repo, head) {
		t.Fatal("CommitExists(head) = false; want true")
	}
	if CommitExists(ctx, repo, strings.Repeat("0", 40)) {
		t.Fatal("CommitExists(zeros) = true; want false")
	}
}

func TestChangesCoverage(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "f.txt", "one\n")
	writeFile(t, repo, "del.txt", "bye\n")
	writeFile(t, repo, "bin.dat", "old\x00binary")
	writeFile(t, repo, ".gitignore", "ignored.txt\n")
	commitAll(t, repo, "init")

	start, err := Take(ctx, repo, "", "start")
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, repo, "f.txt", "one\ntwo\n")
	writeFile(t, repo, "new.txt", "new\n")
	if err := os.Remove(filepath.Join(repo, "del.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "bin.dat", "new\x00binary")
	writeFile(t, repo, "ignored.txt", "should not appear\n")

	end, err := Take(ctx, repo, "", "end")
	if err != nil {
		t.Fatal(err)
	}

	changes, err := Changes(ctx, repo, start.Commit, end.Commit)
	if err != nil {
		t.Fatal(err)
	}
	got := changesByPath(t, changes)

	if c := got["f.txt"]; c.Status != "modified" || c.Additions != 1 || c.Deletions != 0 || c.Binary {
		t.Errorf("f.txt = %+v; want modified +1 -0 text", c)
	}
	if c := got["new.txt"]; c.Status != "added" || c.Additions != 1 {
		t.Errorf("new.txt = %+v; want added +1", c)
	}
	if c := got["del.txt"]; c.Status != "deleted" || c.Deletions != 1 {
		t.Errorf("del.txt = %+v; want deleted -1", c)
	}
	if c := got["bin.dat"]; !c.Binary {
		t.Errorf("bin.dat = %+v; want binary", c)
	}
	if _, ok := got["ignored.txt"]; ok {
		t.Error("ignored.txt appears in changes; want it excluded")
	}
	if _, ok := got[".gitignore"]; ok {
		t.Error(".gitignore appears in changes; want it unchanged")
	}

	diff, err := FileDiff(ctx, repo, start.Commit, end.Commit, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "diff --git a/f.txt b/f.txt") || !strings.Contains(diff, "+two") {
		t.Fatalf("FileDiff(f.txt) = %q; want added line", diff)
	}

	patch, truncated, err := Patch(ctx, repo, start.Commit, end.Commit, 0)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Fatal("Patch truncated with maxBytes=0")
	}
	if !strings.Contains(patch, "GIT binary patch") {
		t.Fatalf("Patch missing binary section:\n%s", patch)
	}
}

func TestSnapshotLeavesRealIndexAndStatusAlone(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "staged.txt", "one\n")
	writeFile(t, repo, "unstaged.txt", "one\n")
	commitAll(t, repo, "init")

	writeFile(t, repo, "staged.txt", "one\ntwo\n")
	gitRun(t, repo, "add", "staged.txt")
	writeFile(t, repo, "unstaged.txt", "one\nthree\n")
	writeFile(t, repo, "untracked.txt", "new\n")

	indexPath := strings.TrimSpace(gitRun(t, repo, "rev-parse", "--path-format=absolute", "--git-path", "index"))
	beforeCached := gitRun(t, repo, "diff", "--cached", "--name-status")
	beforeStatus := gitRun(t, repo, "status", "--porcelain")
	beforeIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Take(ctx, repo, TaskRef(1, "start"), "start"); err != nil {
		t.Fatal(err)
	}

	afterCached := gitRun(t, repo, "diff", "--cached", "--name-status")
	afterStatus := gitRun(t, repo, "status", "--porcelain")
	afterIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	if beforeCached != afterCached {
		t.Errorf("cached diff changed: %q -> %q", beforeCached, afterCached)
	}
	if beforeStatus != afterStatus {
		t.Errorf("status changed:\n%q\n->\n%q", beforeStatus, afterStatus)
	}
	if !bytes.Equal(beforeIndex, afterIndex) {
		t.Error("real .git/index bytes changed after Take")
	}
}

func TestSnapshotWithoutCommits(t *testing.T) {
	ctx := context.Background()
	requireGit(t)
	repo := t.TempDir()
	gitRun(t, repo, "init", "-q", "-b", "main")
	gitRun(t, repo, "config", "core.autocrlf", "false")
	writeFile(t, repo, "f.txt", "one\n")

	ref := TaskRef(7, "start")
	snap, err := Take(ctx, repo, ref, "start")
	if err != nil {
		t.Fatal(err)
	}
	if snap.Head != "" {
		t.Fatalf("Head = %q; want empty for a repository without commits", snap.Head)
	}
	if snap.Commit == "" || snap.Tree == "" {
		t.Fatal("snapshot commit/tree is empty")
	}
	parents := strings.Fields(gitRun(t, repo, "rev-list", "--parents", "-n", "1", snap.Commit))
	if len(parents) != 1 {
		t.Fatalf("snapshot has parents: %v; want a parentless commit", parents)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "index")); !os.IsNotExist(err) {
		t.Fatalf("real .git/index exists after Take (err=%v); want none", err)
	}
	if got := gitRun(t, repo, "rev-parse", ref); got != snap.Commit {
		t.Fatalf("ref points to %q; want %q", got, snap.Commit)
	}
}

func TestSubdirectoryProjectIsScoped(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "sub/inside.txt", "a\n")
	writeFile(t, repo, "outside.txt", "o\n")
	commitAll(t, repo, "init")
	sub := filepath.Join(repo, "sub")

	start, err := Take(ctx, sub, "", "start")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "sub/inside.txt", "a\nb\n")
	writeFile(t, repo, "outside.txt", "o\nx\n")
	end, err := Take(ctx, sub, "", "end")
	if err != nil {
		t.Fatal(err)
	}

	changes, err := Changes(ctx, sub, start.Commit, end.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "sub/inside.txt" {
		t.Fatalf("Changes(sub) = %+v; want only sub/inside.txt", changes)
	}

	result, err := Transplant(ctx, sub, end.Commit, start.Commit, "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed {
		t.Fatal("Transplant Changed = false; want true")
	}
	if got := readFile(t, repo, "sub/inside.txt"); got != "a\n" {
		t.Fatalf("sub/inside.txt = %q; want %q", got, "a\n")
	}
	if got := readFile(t, repo, "outside.txt"); got != "o\nx\n" {
		t.Fatalf("outside.txt = %q; want later change preserved", got)
	}
}

func TestCommitBetweenSnapshotsIsIncluded(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "f.txt", "one\n")
	commitAll(t, repo, "init")

	start, err := Take(ctx, repo, "", "start")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "f.txt", "one\ntwo\n")
	taskHead := commitAll(t, repo, "task commit")
	writeFile(t, repo, "g.txt", "new\n")
	end, err := Take(ctx, repo, "", "end")
	if err != nil {
		t.Fatal(err)
	}

	if start.Head == end.Head {
		t.Fatalf("start.Head == end.Head == %q; want different heads", start.Head)
	}
	if end.Head != taskHead {
		t.Fatalf("end.Head = %q; want task commit %q", end.Head, taskHead)
	}
	if got := gitRun(t, repo, "rev-parse", start.Commit+"^"); got != start.Head {
		t.Fatalf("start snapshot parent = %q; want %q", got, start.Head)
	}
	if got := gitRun(t, repo, "rev-parse", end.Commit+"^"); got != end.Head {
		t.Fatalf("end snapshot parent = %q; want %q", got, end.Head)
	}

	changes, err := Changes(ctx, repo, start.Commit, end.Commit)
	if err != nil {
		t.Fatal(err)
	}
	got := changesByPath(t, changes)
	if c := got["f.txt"]; c.Status != "modified" || c.Additions != 1 {
		t.Errorf("f.txt = %+v; want committed change included", c)
	}
	if c := got["g.txt"]; c.Status != "added" {
		t.Errorf("g.txt = %+v; want added", c)
	}
}

func TestTransplantPreservesLaterEdits(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "f.txt", "one\n")
	commitAll(t, repo, "init")

	start, err := Take(ctx, repo, "", "start")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "f.txt", "one\ntwo\n")
	end, err := Take(ctx, repo, "", "end")
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, repo, "f.txt", "zero\none\ntwo\n")

	// A plain reverse patch must fail here; the 3-way transplant must not.
	rawDiff, err := gitRaw(t, repo, nil, "diff", "--binary", start.Commit, end.Commit, "--", "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitRaw(t, repo, []byte(rawDiff), "apply", "-R", "--check", "-"); err == nil {
		t.Error("git apply -R unexpectedly succeeded on a user-edited file")
	}

	result, err := Transplant(ctx, repo, end.Commit, start.Commit,
		TaskRef(11, "pre-revert"), TaskRef(11, "reverted"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Post == nil || len(result.Files) != 1 {
		t.Fatalf("Transplant result = %+v; want changed with one file", result)
	}
	if got := readFile(t, repo, "f.txt"); got != "zero\none\n" {
		t.Fatalf("f.txt = %q; want %q", got, "zero\none\n")
	}
	for _, ref := range []string{TaskRef(11, "pre-revert"), TaskRef(11, "reverted")} {
		if !CommitExists(ctx, repo, strings.TrimSpace(gitRun(t, repo, "rev-parse", ref))) {
			t.Errorf("ref %s does not point to a commit", ref)
		}
	}
}

func TestTransplantConflictLeavesFilesAlone(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "f.txt", "one\n")
	commitAll(t, repo, "init")

	start, err := Take(ctx, repo, "", "start")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "f.txt", "two\n")
	end, err := Take(ctx, repo, "", "end")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "f.txt", "three\n")

	_, err = Transplant(ctx, repo, end.Commit, start.Commit, "", "", false)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Transplant error = %v; want *ConflictError", err)
	}
	if len(conflict.Paths) != 1 || conflict.Paths[0] != "f.txt" {
		t.Fatalf("conflict paths = %v; want [f.txt]", conflict.Paths)
	}
	if got := readFile(t, repo, "f.txt"); got != "three\n" {
		t.Fatalf("f.txt = %q; want user content untouched", got)
	}
}

func TestTransplantDryRunDoesNotTouchFiles(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "f.txt", "one\n")
	commitAll(t, repo, "init")

	start, err := Take(ctx, repo, "", "start")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "f.txt", "one\ntwo\n")
	end, err := Take(ctx, repo, "", "end")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "f.txt", "zero\none\ntwo\n")

	result, err := Transplant(ctx, repo, end.Commit, start.Commit, "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Post != nil {
		t.Fatalf("dry-run result = %+v; want Changed=true, Post=nil", result)
	}
	if len(result.Files) != 1 || result.Files[0].Path != "f.txt" {
		t.Fatalf("dry-run files = %+v; want f.txt", result.Files)
	}
	if got := readFile(t, repo, "f.txt"); got != "zero\none\ntwo\n" {
		t.Fatalf("f.txt = %q; dry run must not modify it", got)
	}
}

func TestTakeRejectsOversizedWorkingTree(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "small.txt", "ok\n")
	commitAll(t, repo, "init")

	big := filepath.Join(repo, "big.dat")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxSnapshotFileSize + 1); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ref := TaskRef(3, "start")
	if _, err := Take(ctx, repo, ref, "too big"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Take error = %v; want ErrTooLarge", err)
	}
	if _, err := gitRaw(t, repo, nil, "rev-parse", "--verify", "-q", ref); err == nil {
		t.Fatal("ref was created despite ErrTooLarge")
	}

	commonDir := strings.TrimSpace(gitRun(t, repo, "rev-parse", "--path-format=absolute", "--git-common-dir"))
	matches, err := filepath.Glob(filepath.Join(commonDir, "shepherd-ckpt-index-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary index files left behind: %v", matches)
	}
}

func TestDiffIgnoresUserDiffConfig(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "f.txt", "one\n")
	commitAll(t, repo, "init")
	gitRun(t, repo, "config", "diff.noprefix", "true")
	gitRun(t, repo, "config", "color.ui", "always")

	start, err := Take(ctx, repo, "", "start")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "f.txt", "one\ntwo\n")
	end, err := Take(ctx, repo, "", "end")
	if err != nil {
		t.Fatal(err)
	}

	changes, err := Changes(ctx, repo, start.Commit, end.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "f.txt" || changes[0].Additions != 1 {
		t.Fatalf("Changes = %+v; want f.txt +1", changes)
	}
	diff, err := FileDiff(ctx, repo, start.Commit, end.Commit, "f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "diff --git a/f.txt b/f.txt") {
		t.Fatalf("FileDiff header = %q; want a/ b/ prefixes", diff)
	}
	if strings.Contains(diff, "\x1b[") {
		t.Fatal("FileDiff contains ANSI color escapes")
	}
}

func TestTaskRefLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "f.txt", "one\n")
	commitAll(t, repo, "init")

	for _, ref := range []string{TaskRef(1, "start"), TaskRef(1, "end"), TaskRef(2, "start")} {
		if _, err := Take(ctx, repo, ref, "snapshot"); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, repo, "update-ref", "refs/shepherd/other/keep", "HEAD")

	refs, err := ListTaskRefs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs[1]) != 2 || len(refs[2]) != 1 {
		t.Fatalf("ListTaskRefs = %v; want task 1 with 2 refs and task 2 with 1", refs)
	}
	for _, list := range refs {
		for _, ref := range list {
			if !strings.HasPrefix(ref, "refs/shepherd/tasks/") {
				t.Errorf("ListTaskRefs returned outside refs/shepherd/tasks: %s", ref)
			}
		}
	}

	if err := DeleteTaskRefs(ctx, repo, 1); err != nil {
		t.Fatal(err)
	}
	refs, err = ListTaskRefs(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := refs[1]; ok {
		t.Fatalf("task 1 refs still present: %v", refs[1])
	}
	if len(refs[2]) != 1 {
		t.Fatalf("task 2 refs = %v; want one preserved", refs[2])
	}
	if _, err := gitRaw(t, repo, nil, "rev-parse", "--verify", "-q", "refs/shepherd/other/keep"); err != nil {
		t.Fatal("refs/shepherd/other/keep was deleted; want preserved")
	}
}

func TestPatchTruncation(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "f.txt", "one\n")
	commitAll(t, repo, "init")
	start, err := Take(ctx, repo, "", "start")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, repo, "f.txt", strings.Repeat("line\n", 200))
	end, err := Take(ctx, repo, "", "end")
	if err != nil {
		t.Fatal(err)
	}

	patch, truncated, err := Patch(ctx, repo, start.Commit, end.Commit, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(patch) != 40 {
		t.Fatalf("truncated=%v len=%d; want true/40", truncated, len(patch))
	}
}

func TestRenamedFileDiff(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	writeFile(t, repo, "old.txt", "same\n")
	commitAll(t, repo, "init")
	start, err := Take(ctx, repo, "", "start")
	if err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "mv", "old.txt", "new.txt")
	end, err := Take(ctx, repo, "", "end")
	if err != nil {
		t.Fatal(err)
	}

	changes, err := Changes(ctx, repo, start.Commit, end.Commit)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("Changes = %+v; want one rename", changes)
	}
	if changes[0].Status != "renamed" || changes[0].Path != "new.txt" || changes[0].OldPath != "old.txt" {
		t.Fatalf("rename = %+v; want new.txt from old.txt", changes[0])
	}

	diff, err := FileDiff(ctx, repo, start.Commit, end.Commit, "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "rename from old.txt") || !strings.Contains(diff, "rename to new.txt") {
		t.Fatalf("FileDiff(new.txt) = %q; want rename headers", diff)
	}
}

func TestResolveRefAndDeleteRefs(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "f.txt", "one\n")
	gitRun(t, dir, "add", "f.txt")
	gitRun(t, dir, "commit", "-qm", "initial")

	ctx := context.Background()
	snap, err := Take(ctx, dir, TaskRef(9, "start"), "start")
	if err != nil {
		t.Fatalf("Take: %v", err)
	}

	got, ok := ResolveRef(ctx, dir, TaskRef(9, "start"))
	if !ok || got != snap.Commit {
		t.Fatalf("ResolveRef = %q, %v; want %q", got, ok, snap.Commit)
	}
	if _, ok := ResolveRef(ctx, dir, TaskRef(9, "missing")); ok {
		t.Error("ResolveRef reported a missing ref")
	}

	// Only task refs may be deleted.
	if err := DeleteRefs(ctx, dir, "refs/heads/main", TaskRef(9, "start")); err != nil {
		t.Fatalf("DeleteRefs: %v", err)
	}
	if _, ok := ResolveRef(ctx, dir, TaskRef(9, "start")); ok {
		t.Error("task ref was not deleted")
	}
	if got := gitRun(t, dir, "rev-parse", "refs/heads/main"); got == "" {
		t.Error("branch was deleted")
	}
}
