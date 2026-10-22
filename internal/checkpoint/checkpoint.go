// Package checkpoint captures a project's working tree as a hidden git commit,
// compares two snapshots, and applies a snapshot's changes back to the working
// tree with a 3-way merge. It only uses git plumbing commands and never touches
// the user's index, HEAD, branches, or stash.
package checkpoint

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agurrrrr/shepherd/internal/procutil"
)

const (
	gitTimeout = 30 * time.Second

	maxSnapshotFiles     = 20000
	maxSnapshotFileSize  = 50 << 20
	maxSnapshotTotalSize = 200 << 20

	authorName  = "shepherd"
	authorEmail = "shepherd@localhost"
)

// ErrNotGitRepo is returned when dir is not inside a git working tree.
var ErrNotGitRepo = errors.New("not inside a git working tree")

// ErrTooLarge is returned when the working tree exceeds the snapshot limits.
var ErrTooLarge = errors.New("working tree too large to snapshot")

// Snapshot is a commit that records the working tree at a point in time.
type Snapshot struct {
	Commit string // snapshot commit SHA
	Tree   string
	Head   string // HEAD at snapshot time; empty for a repository without commits
}

// FileChange describes one changed path between two snapshots. Paths are
// relative to the repository top level (for example "sub/f").
type FileChange struct {
	Path      string
	OldPath   string // set for renames
	Status    string // added | modified | deleted | renamed
	Additions int
	Deletions int
	Binary    bool
}

// ConflictError reports paths that could not be merged during Transplant.
type ConflictError struct {
	Paths []string
}

func (e *ConflictError) Error() string {
	if len(e.Paths) == 0 {
		return "checkpoint transplant conflict"
	}
	return "checkpoint transplant conflict: " + strings.Join(e.Paths, ", ")
}

// TransplantResult is the outcome of a transplant.
type TransplantResult struct {
	Pre     *Snapshot
	Post    *Snapshot // nil when nothing changed or on a dry run
	Files   []FileChange
	Changed bool
}

// TaskRef returns the hidden ref name for a task checkpoint kind.
func TaskRef(taskID int, kind string) string {
	return fmt.Sprintf("refs/shepherd/tasks/%d/%s", taskID, kind)
}

// Probe reports whether dir is inside a git working tree. It returns the
// repository top level and dir's prefix within it (empty at the top level).
func Probe(ctx context.Context, dir string) (toplevel, prefix string, err error) {
	out, _, code, err := runGit(ctx, dir, nil, nil, "rev-parse", "--is-inside-work-tree")
	if err != nil || code != 0 || strings.TrimSpace(string(out)) != "true" {
		return "", "", ErrNotGitRepo
	}
	topOut, err := gitOutput(ctx, dir, nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("resolve git top level: %w", err)
	}
	prefixOut, err := gitOutput(ctx, dir, nil, nil, "rev-parse", "--show-prefix")
	if err != nil {
		return "", "", fmt.Errorf("resolve git prefix: %w", err)
	}
	toplevel = strings.TrimSpace(string(topOut))
	prefix = strings.TrimSuffix(strings.TrimSpace(string(prefixOut)), "/")
	return toplevel, prefix, nil
}

// Take snapshots the working tree beneath dir. When ref is non-empty the
// snapshot commit is also recorded at that ref. The user's real index and HEAD
// are left untouched.
func Take(ctx context.Context, dir, ref, message string) (*Snapshot, error) {
	top, _, err := Probe(ctx, dir)
	if err != nil {
		return nil, err
	}
	unlock := lockRepo(top)
	defer unlock()
	return takeLocked(ctx, dir, ref, message)
}

func takeLocked(ctx context.Context, dir, ref, message string) (*Snapshot, error) {
	if _, _, err := Probe(ctx, dir); err != nil {
		return nil, err
	}
	if message == "" {
		message = "shepherd checkpoint"
	}

	commonDirOut, err := gitOutput(ctx, dir, nil, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, fmt.Errorf("resolve git common dir: %w", err)
	}
	commonDir := strings.TrimSpace(string(commonDirOut))

	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return nil, fmt.Errorf("generate temporary index name: %w", err)
	}
	tmpIndex := filepath.Join(commonDir, "shepherd-ckpt-index-"+hex.EncodeToString(suffix[:]))
	defer os.Remove(tmpIndex)
	defer os.Remove(tmpIndex + ".lock")

	// Copy the real index so `add -A` can reuse git's stat cache. A repository
	// without any commit has no index; leave the temporary one absent then.
	idxOut, err := gitOutput(ctx, dir, nil, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return nil, fmt.Errorf("resolve git index path: %w", err)
	}
	realIndex := strings.TrimSpace(string(idxOut))
	if st, statErr := os.Stat(realIndex); statErr == nil {
		if err := copyFilePreserveMtime(realIndex, tmpIndex, st); err != nil {
			return nil, fmt.Errorf("copy git index: %w", err)
		}
	}

	if err := checkUntrackedSize(ctx, dir, tmpIndex); err != nil {
		return nil, err
	}

	if err := addAll(ctx, dir, tmpIndex, nil); err != nil {
		// A nested repository without commits makes `add -A` fail. Exclude such
		// directories from the pathspec and retry once, mirroring what the
		// parent task researched.
		excludes, exErr := nestedRepoExcludes(ctx, dir)
		if exErr == nil && len(excludes) > 0 {
			if retryErr := addAll(ctx, dir, tmpIndex, excludes); retryErr == nil {
				err = nil
			}
		}
		if err != nil {
			return nil, err
		}
	}

	treeOut, err := gitOutput(ctx, dir, []string{"GIT_INDEX_FILE=" + tmpIndex}, nil,
		"-c", "core.fsync=objects,reference", "write-tree")
	if err != nil {
		return nil, fmt.Errorf("write snapshot tree: %w", err)
	}
	tree := strings.TrimSpace(string(treeOut))

	head := ""
	headOut, _, headCode, headErr := runGit(ctx, dir, nil, nil, "rev-parse", "--verify", "-q", "HEAD")
	if headErr == nil && headCode == 0 {
		head = strings.TrimSpace(string(headOut))
	}

	commitArgs := []string{"-c", "core.fsync=objects,reference", "commit-tree", tree}
	if head != "" {
		commitArgs = append(commitArgs, "-p", head)
	}
	commitArgs = append(commitArgs, "-m", message)
	commitOut, err := gitOutput(ctx, dir, nil, nil, commitArgs...)
	if err != nil {
		return nil, fmt.Errorf("create snapshot commit: %w", err)
	}
	commit := strings.TrimSpace(string(commitOut))

	if ref != "" {
		if _, err := gitOutput(ctx, dir, nil, nil,
			"-c", "core.fsync=objects,reference", "update-ref", ref, commit); err != nil {
			return nil, fmt.Errorf("record snapshot ref %s: %w", ref, err)
		}
	}

	return &Snapshot{Commit: commit, Tree: tree, Head: head}, nil
}

// Changes lists the files changed between two snapshots, restricted to dir.
func Changes(ctx context.Context, dir, from, to string) ([]FileChange, error) {
	args := diffArgs("--name-status", "-z", "-M", from, to, "--", ".")
	out, err := gitOutput(ctx, dir, nil, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("list changed files: %w", err)
	}
	changes := parseNameStatus(out)

	args = diffArgs("--numstat", "-z", "-M", from, to, "--", ".")
	numOut, err := gitOutput(ctx, dir, nil, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("count changed lines: %w", err)
	}
	changes = applyNumstat(changes, numOut)
	return changes, nil
}

// FileDiff returns the raw unified diff for one path between two snapshots.
func FileDiff(ctx context.Context, dir, from, to, path string) (string, error) {
	gitPath := filepath.ToSlash(path)
	paths := []string{gitPath}
	if changes, err := Changes(ctx, dir, from, to); err == nil {
		for _, c := range changes {
			if c.Path == gitPath || c.OldPath == gitPath {
				if c.Path != "" {
					paths = appendUnique(paths, c.Path)
				}
				if c.OldPath != "" {
					paths = appendUnique(paths, c.OldPath)
				}
			}
		}
	}

	args := diffArgs("--unified=3", from, to, "--")
	for _, p := range paths {
		args = append(args, ":(top,literal)"+p)
	}
	out, err := gitOutput(ctx, dir, nil, nil, args...)
	if err != nil {
		return "", fmt.Errorf("diff %s: %w", path, err)
	}
	return string(out), nil
}

// Patch returns a binary-safe patch between two snapshots. When maxBytes is
// positive and the patch is larger, it is truncated and truncated is true.
func Patch(ctx context.Context, dir, from, to string, maxBytes int) (patch string, truncated bool, err error) {
	args := diffArgs("--binary", "--full-index", from, to, "--", ".")
	out, err := gitOutput(ctx, dir, nil, nil, args...)
	if err != nil {
		return "", false, fmt.Errorf("build patch: %w", err)
	}
	if maxBytes > 0 && len(out) > maxBytes {
		return string(out[:maxBytes]), true, nil
	}
	return string(out), false, nil
}

// Transplant applies the change from base to target onto the current working
// tree using a 3-way merge, so edits made after the target snapshot survive.
// It does not change the user's index. On conflict it returns *ConflictError
// and leaves the working tree untouched.
func Transplant(ctx context.Context, dir, base, target, preRef, postRef string, dryRun bool) (*TransplantResult, error) {
	top, _, err := Probe(ctx, dir)
	if err != nil {
		return nil, err
	}
	unlock := lockRepo(top)
	defer unlock()

	pre, err := takeLocked(ctx, dir, preRef, "shepherd checkpoint pre-transplant")
	if err != nil {
		return nil, err
	}

	out, stderr, code, runErr := runGit(ctx, dir, nil, nil,
		"merge-tree", "--write-tree", "-z", "--name-only", "--merge-base="+base, pre.Commit, target)
	if runErr != nil {
		return nil, runErr
	}
	resultTree, conflicts := parseMergeTree(out)
	switch code {
	case 0:
	case 1:
		return nil, &ConflictError{Paths: conflicts}
	default:
		return nil, gitCommandError("merge-tree", stderr)
	}

	result := &TransplantResult{Pre: pre}
	if resultTree == pre.Tree {
		return result, nil
	}

	if dryRun {
		files, err := Changes(ctx, dir, pre.Commit, resultTree)
		if err != nil {
			return nil, err
		}
		result.Files = files
		result.Changed = true
		return result, nil
	}

	args := diffArgs("--binary", "--full-index", pre.Commit, resultTree, "--", ".")
	patch, err := gitOutput(ctx, dir, nil, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("build transplant patch: %w", err)
	}
	if _, err := gitOutput(ctx, dir, nil, patch, "apply", "--check", "-"); err != nil {
		return nil, fmt.Errorf("check transplant patch: %w", err)
	}
	if _, err := gitOutput(ctx, dir, nil, patch, "apply", "-"); err != nil {
		return nil, fmt.Errorf("apply transplant patch: %w", err)
	}

	post, err := takeLocked(ctx, dir, postRef, "shepherd checkpoint post-transplant")
	if err != nil {
		return nil, err
	}
	files, err := Changes(ctx, dir, pre.Commit, post.Commit)
	if err != nil {
		return nil, err
	}
	result.Post = post
	result.Files = files
	result.Changed = true
	return result, nil
}

// DeleteTaskRefs deletes all checkpoint refs belonging to the given task IDs.
func DeleteTaskRefs(ctx context.Context, dir string, taskIDs ...int) error {
	if len(taskIDs) == 0 {
		return nil
	}
	refs, err := ListTaskRefs(ctx, dir)
	if err != nil {
		return err
	}
	for _, id := range taskIDs {
		for _, ref := range refs[id] {
			if _, err := gitOutput(ctx, dir, nil, nil, "update-ref", "-d", ref); err != nil {
				return fmt.Errorf("delete checkpoint ref %s: %w", ref, err)
			}
		}
	}
	return nil
}

// ListTaskRefs returns checkpoint refs grouped by task ID. Only refs below
// refs/shepherd/tasks/ are returned.
func ListTaskRefs(ctx context.Context, dir string) (map[int][]string, error) {
	out, err := gitOutput(ctx, dir, nil, nil, "for-each-ref", "--format=%(refname)", "refs/shepherd/tasks/")
	if err != nil {
		return nil, fmt.Errorf("list checkpoint refs: %w", err)
	}
	refs := make(map[int][]string)
	for _, line := range strings.Split(string(out), "\n") {
		ref := strings.TrimSpace(line)
		if !strings.HasPrefix(ref, "refs/shepherd/tasks/") {
			continue
		}
		rest := strings.TrimPrefix(ref, "refs/shepherd/tasks/")
		idPart, _, ok := strings.Cut(rest, "/")
		if !ok {
			continue
		}
		id, convErr := strconv.Atoi(idPart)
		if convErr != nil || id <= 0 {
			continue
		}
		refs[id] = append(refs[id], ref)
	}
	for id := range refs {
		sort.Strings(refs[id])
	}
	return refs, nil
}

// CommitExists reports whether sha resolves to a commit in the repository.
func CommitExists(ctx context.Context, dir, sha string) bool {
	if sha == "" || strings.HasPrefix(sha, "-") {
		return false
	}
	_, _, code, err := runGit(ctx, dir, nil, nil, "rev-parse", "--verify", "-q", sha+"^{commit}")
	return err == nil && code == 0
}

// ResolveRef returns the commit SHA a checkpoint ref points at. It reports
// false when the ref does not exist or is not a commit.
func ResolveRef(ctx context.Context, dir, ref string) (string, bool) {
	if ref == "" || strings.HasPrefix(ref, "-") {
		return "", false
	}
	out, _, code, err := runGit(ctx, dir, nil, nil, "rev-parse", "--verify", "-q", ref+"^{commit}")
	if err != nil || code != 0 {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// DeleteRefs deletes specific checkpoint refs. Only refs below
// refs/shepherd/tasks/ are accepted so a caller bug cannot remove a user's
// branch or tag. Missing refs are ignored.
func DeleteRefs(ctx context.Context, dir string, refs ...string) error {
	for _, ref := range refs {
		if !strings.HasPrefix(ref, "refs/shepherd/tasks/") {
			continue
		}
		if _, err := gitOutput(ctx, dir, nil, nil, "update-ref", "-d", ref); err != nil {
			return fmt.Errorf("delete checkpoint ref %s: %w", ref, err)
		}
	}
	return nil
}

// Parent returns the first parent of commit, or an empty string for a root
// commit. It is used to locate the real HEAD range around a snapshot.
func Parent(ctx context.Context, dir, commit string) string {
	if commit == "" || strings.HasPrefix(commit, "-") {
		return ""
	}
	out, _, code, err := runGit(ctx, dir, nil, nil, "rev-parse", "--verify", "-q", commit+"^")
	if err != nil || code != 0 {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// CommitLog is one commit between two snapshots' parents.
type CommitLog struct {
	Hash    string
	Subject string
}

// CommitsBetween lists commits reachable from to but not from, newest first.
// The result is capped at limit when limit is positive.
func CommitsBetween(ctx context.Context, dir, from, to string, limit int) ([]CommitLog, error) {
	if from == "" || to == "" || strings.HasPrefix(from, "-") || strings.HasPrefix(to, "-") {
		return nil, nil
	}
	args := []string{"log", "--format=%H%x00%s"}
	if limit > 0 {
		args = append(args, fmt.Sprintf("--max-count=%d", limit))
	}
	args = append(args, from+".."+to)
	out, err := gitOutput(ctx, dir, nil, nil, args...)
	if err != nil {
		return nil, fmt.Errorf("list commits during task: %w", err)
	}

	var commits []CommitLog
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x00", 2)
		if len(parts) != 2 {
			continue
		}
		commits = append(commits, CommitLog{Hash: parts[0], Subject: parts[1]})
	}
	return commits, nil
}

// --- git execution ---

func diffArgs(extra ...string) []string {
	args := []string{
		"diff",
		"--no-color",
		"--no-ext-diff",
		"--no-textconv",
		"--src-prefix=a/",
		"--dst-prefix=b/",
	}
	return append(args, extra...)
}

func gitEnv() []string {
	return append(os.Environ(),
		"LC_ALL=C",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME="+authorName,
		"GIT_AUTHOR_EMAIL="+authorEmail,
		"GIT_COMMITTER_NAME="+authorName,
		"GIT_COMMITTER_EMAIL="+authorEmail,
	)
}

func runGit(ctx context.Context, dir string, extraEnv []string, stdin []byte, args ...string) (stdout, stderr []byte, code int, err error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	procutil.HideWindow(cmd)
	cmd.Env = append(gitEnv(), extraEnv...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	stdout, stderr = out.Bytes(), errBuf.Bytes()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code = exitErr.ExitCode()
		} else {
			return stdout, stderr, -1, fmt.Errorf("git %s: %w", firstArg(args), runErr)
		}
	}
	if ctx.Err() == context.DeadlineExceeded {
		return stdout, stderr, code, fmt.Errorf("git %s: timed out after %s", firstArg(args), gitTimeout)
	}
	return stdout, stderr, code, nil
}

func gitOutput(ctx context.Context, dir string, extraEnv []string, stdin []byte, args ...string) ([]byte, error) {
	out, stderr, code, err := runGit(ctx, dir, extraEnv, stdin, args...)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, gitCommandError(firstArg(args), stderr)
	}
	return out, nil
}

func gitCommandError(command string, stderr []byte) error {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		return fmt.Errorf("git %s failed", command)
	}
	return fmt.Errorf("git %s failed: %s", command, msg)
}

func firstArg(args []string) string {
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "-c" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg
	}
	if len(args) > 0 {
		return args[0]
	}
	return "git"
}

// --- index handling ---

func copyFilePreserveMtime(src, dst string, st os.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(dst, st.ModTime(), st.ModTime())
}

func checkUntrackedSize(ctx context.Context, dir, tmpIndex string) error {
	out, err := gitOutput(ctx, dir, []string{"GIT_INDEX_FILE=" + tmpIndex}, nil,
		"ls-files", "--others", "--exclude-standard", "-z", "--", ".")
	if err != nil {
		return fmt.Errorf("list untracked files: %w", err)
	}

	type bigFile struct {
		path string
		size int64
	}
	var (
		count     int
		total     int64
		oversized []bigFile
	)
	for _, p := range splitNul(out) {
		if p == "" {
			continue
		}
		count++
		st, statErr := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
		if statErr != nil {
			continue
		}
		total += st.Size()
		if st.Size() > maxSnapshotFileSize {
			oversized = append(oversized, bigFile{path: p, size: st.Size()})
		}
	}

	if count <= maxSnapshotFiles && total <= maxSnapshotTotalSize && len(oversized) == 0 {
		return nil
	}

	sort.Slice(oversized, func(i, j int) bool {
		if oversized[i].size == oversized[j].size {
			return oversized[i].path < oversized[j].path
		}
		return oversized[i].size > oversized[j].size
	})
	if len(oversized) > 5 {
		oversized = oversized[:5]
	}
	details := make([]string, 0, len(oversized))
	for _, f := range oversized {
		details = append(details, fmt.Sprintf("%s (%d bytes)", f.path, f.size))
	}
	reason := fmt.Sprintf("%d untracked files (limit %d), %d total bytes (limit %d)",
		count, maxSnapshotFiles, total, maxSnapshotTotalSize)
	if len(details) > 0 {
		reason += ", largest above " + strconv.Itoa(maxSnapshotFileSize) + " bytes: " + strings.Join(details, ", ")
	}
	return fmt.Errorf("%w: %s", ErrTooLarge, reason)
}

func addAll(ctx context.Context, dir, tmpIndex string, excludes []string) error {
	args := []string{
		"-c", "core.fsync=objects,reference",
		"-c", "core.fsmonitor=false",
		"-c", "core.splitIndex=false",
		"-c", "splitIndex.sharedIndexExpire=never",
		"add", "-A", "--", ".",
	}
	for _, ex := range excludes {
		args = append(args, ":(exclude,literal)"+ex)
	}
	_, err := gitOutput(ctx, dir, []string{"GIT_INDEX_FILE=" + tmpIndex}, nil, args...)
	if err != nil {
		return fmt.Errorf("stage working tree into snapshot index: %w", err)
	}
	return nil
}

func nestedRepoExcludes(ctx context.Context, dir string) ([]string, error) {
	out, err := gitOutput(ctx, dir, nil, nil,
		"ls-files", "--others", "--exclude-standard", "--directory", "-z", "--", ".")
	if err != nil {
		return nil, err
	}
	var excludes []string
	for _, p := range splitNul(out) {
		p = strings.TrimSuffix(p, "/")
		if p == "" {
			continue
		}
		if _, statErr := os.Stat(filepath.Join(dir, filepath.FromSlash(p), ".git")); statErr == nil {
			excludes = append(excludes, p)
		}
	}
	return excludes, nil
}

// --- parsing ---

func splitNul(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	parts := strings.Split(string(b), "\x00")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func parseNameStatus(out []byte) []FileChange {
	tokens := splitNul(out)
	var changes []FileChange
	for i := 0; i < len(tokens); {
		status := tokens[i]
		i++
		if status == "" {
			continue
		}
		if status[0] == 'R' || status[0] == 'C' {
			if i+1 >= len(tokens) {
				break
			}
			oldPath, newPath := tokens[i], tokens[i+1]
			i += 2
			kind := "renamed"
			if status[0] == 'C' {
				kind = "added"
			}
			changes = append(changes, FileChange{Path: newPath, OldPath: oldPath, Status: kind})
			continue
		}
		if i >= len(tokens) {
			break
		}
		path := tokens[i]
		i++
		changes = append(changes, FileChange{Path: path, Status: statusFor(status[0])})
	}
	return changes
}

func statusFor(letter byte) string {
	switch letter {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "added"
	default:
		return "modified"
	}
}

func applyNumstat(changes []FileChange, out []byte) []FileChange {
	index := make(map[string]int, len(changes))
	for i := range changes {
		index[changes[i].Path] = i
	}
	tokens := splitNul(out)
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "" {
			continue
		}
		parts := strings.SplitN(token, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" {
			// Renames and copies carry the old and new path as separate fields.
			if i+2 >= len(tokens) {
				break
			}
			path = tokens[i+2]
			i += 2
		}
		pos, ok := index[path]
		if !ok {
			changes = append(changes, FileChange{Path: path, Status: "modified"})
			pos = len(changes) - 1
			index[path] = pos
		}
		if parts[0] == "-" || parts[1] == "-" {
			changes[pos].Binary = true
			continue
		}
		changes[pos].Additions, _ = strconv.Atoi(parts[0])
		changes[pos].Deletions, _ = strconv.Atoi(parts[1])
	}
	return changes
}

func parseMergeTree(out []byte) (tree string, conflicts []string) {
	nul := bytes.IndexByte(out, 0)
	if nul < 0 {
		return strings.TrimSpace(string(out)), nil
	}
	tree = string(out[:nul])
	for _, p := range splitNul(out[nul+1:]) {
		if p == "" {
			break
		}
		conflicts = append(conflicts, p)
	}
	return tree, conflicts
}

func appendUnique(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}

// --- repository locks ---

var (
	repoLocksMu sync.Mutex
	repoLocks   = make(map[string]*sync.Mutex)
)

func lockRepo(toplevel string) func() {
	repoLocksMu.Lock()
	mu, ok := repoLocks[toplevel]
	if !ok {
		mu = &sync.Mutex{}
		repoLocks[toplevel] = mu
	}
	repoLocksMu.Unlock()

	mu.Lock()
	return mu.Unlock
}
