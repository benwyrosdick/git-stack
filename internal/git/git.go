// Package git shells out to the system git binary.
package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is a git repository working directory.
type Repo struct {
	// Dir is the working tree path. Empty means current directory.
	Dir string
}

func (r *Repo) git(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	if r.Dir != "" {
		cmd.Dir = r.Dir
	}
	return cmd
}

func (r *Repo) run(args ...string) (string, error) {
	cmd := r.git(args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func (r *Repo) runOK(args ...string) error {
	_, err := r.run(args...)
	return err
}

// RunInteractive runs git with the caller's stdout/stderr (for rebase UX).
func (r *Repo) RunInteractive(args ...string) error {
	cmd := r.git(args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// RequireRepo dies if not inside a git repository.
func (r *Repo) RequireRepo() error {
	_, err := r.run("rev-parse", "--git-dir")
	if err != nil {
		return fmt.Errorf("not a git repository")
	}
	return nil
}

// DefaultBranch returns the trunk branch name (origin/HEAD, else main/master).
func (r *Repo) DefaultBranch() string {
	if ref, err := r.run("symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		return strings.TrimPrefix(ref, "refs/remotes/origin/")
	}
	for _, b := range []string{"main", "master"} {
		if r.RefExists(b) {
			return b
		}
	}
	return "main"
}

// CurrentBranch returns the current branch name (fails on detached HEAD).
func (r *Repo) CurrentBranch() (string, error) {
	b, err := r.run("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("cannot determine current branch")
	}
	if b == "HEAD" {
		return "", fmt.Errorf("detached HEAD; pass an explicit branch")
	}
	return b, nil
}

// ResolveRef prefers local heads, then origin/, then the name as-is.
func (r *Repo) ResolveRef(name string) (string, error) {
	if r.LocalBranchExists(name) {
		return "refs/heads/" + name, nil
	}
	if r.OriginBranchExists(name) {
		return "refs/remotes/origin/" + name, nil
	}
	if _, err := r.run("rev-parse", "--verify", "--quiet", name); err == nil {
		return name, nil
	}
	return "", fmt.Errorf("ref does not exist: %s", name)
}

// RefExists reports whether name resolves.
func (r *Repo) RefExists(name string) bool {
	_, err := r.ResolveRef(name)
	return err == nil
}

// LocalBranchExists checks refs/heads/<name>.
func (r *Repo) LocalBranchExists(name string) bool {
	_, err := r.run("rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// OriginBranchExists checks refs/remotes/origin/<name>.
func (r *Repo) OriginBranchExists(name string) bool {
	_, err := r.run("rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+name)
	return err == nil
}

// RevParse returns the full SHA for a rev.
func (r *Repo) RevParse(rev string) (string, error) {
	return r.run("rev-parse", rev)
}

// ShortSHA returns a short object name.
func (r *Repo) ShortSHA(rev string) (string, error) {
	return r.run("rev-parse", "--short", rev)
}

// IsAncestor reports whether ancestor is an ancestor of descendant.
func (r *Repo) IsAncestor(ancestor, descendant string) bool {
	err := r.runOK("merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}

// MergeBase returns the merge-base of two revs.
func (r *Repo) MergeBase(a, b string) (string, error) {
	return r.run("merge-base", a, b)
}

// MergeBaseForkPoint returns git merge-base --fork-point result, or error.
func (r *Repo) MergeBaseForkPoint(parentRef, branchRef string) (string, error) {
	return r.run("merge-base", "--fork-point", parentRef, branchRef)
}

// RevListCount counts commits in range (exclusive..inclusive style args).
func (r *Repo) RevListCount(revRange string) (int, error) {
	out, err := r.run("rev-list", "--count", revRange)
	if err != nil {
		return 0, err
	}
	var n int
	_, err = fmt.Sscanf(out, "%d", &n)
	return n, err
}

// RevListOneline returns oneline log for a range.
func (r *Repo) RevListOneline(revRange string) (string, error) {
	return r.run("log", "--oneline", revRange)
}

// LocalBranches lists short names under refs/heads/.
func (r *Repo) LocalBranches() ([]string, error) {
	out, err := r.run("for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// StatusPorcelainUno returns porcelain status for tracked files only (-uno).
func (r *Repo) StatusPorcelainUno() (string, error) {
	return r.run("status", "--porcelain=v1", "-uno")
}

// RequireClean fails if tracked/staged changes exist (untracked OK).
func (r *Repo) RequireClean() error {
	out, err := r.StatusPorcelainUno()
	if err != nil {
		return err
	}
	if out != "" {
		return fmt.Errorf("working tree is dirty; commit or stash first")
	}
	return nil
}

// RequireNoRebase fails if a rebase is in progress.
func (r *Repo) RequireNoRebase() error {
	gd, err := r.run("rev-parse", "--git-path", "rebase-merge")
	if err == nil {
		if st, e := os.Stat(filepath.Clean(gd)); e == nil && st.IsDir() {
			return fmt.Errorf("rebase in progress; finish with git rebase --continue or git rebase --abort")
		}
	}
	gd, err = r.run("rev-parse", "--git-path", "rebase-apply")
	if err == nil {
		if st, e := os.Stat(filepath.Clean(gd)); e == nil && st.IsDir() {
			return fmt.Errorf("rebase in progress; finish with git rebase --continue or git rebase --abort")
		}
	}
	return nil
}

// HasOrigin reports whether remote "origin" is configured.
func (r *Repo) HasOrigin() bool {
	return r.runOK("remote", "get-url", "origin") == nil
}

// FetchOrigin runs git fetch origin (no-op if no origin).
func (r *Repo) FetchOrigin() error {
	if !r.HasOrigin() {
		return nil
	}
	return r.runOK("fetch", "origin")
}

// RemoteRelation describes local tip vs origin/<branch>.
type RemoteRelation string

const (
	RelNone     RemoteRelation = "none"
	RelInSync   RemoteRelation = "in-sync"
	RelBehind   RemoteRelation = "behind"
	RelAhead    RemoteRelation = "ahead"
	RelDiverged RemoteRelation = "diverged"
)

// RemoteRelationOf classifies local vs origin for a local branch.
func (r *Repo) RemoteRelationOf(branch string) RemoteRelation {
	if !r.OriginBranchExists(branch) {
		return RelNone
	}
	l, err1 := r.RevParse("refs/heads/" + branch)
	rr, err2 := r.RevParse("refs/remotes/origin/" + branch)
	if err1 != nil || err2 != nil {
		return RelNone
	}
	if l == rr {
		return RelInSync
	}
	if r.IsAncestor(l, rr) {
		return RelBehind
	}
	if r.IsAncestor(rr, l) {
		return RelAhead
	}
	return RelDiverged
}

// FFBranch fast-forwards local branch to origin/<branch>.
func (r *Repo) FFBranch(branch string) error {
	remoteRef := "refs/remotes/origin/" + branch
	cur, _ := r.run("rev-parse", "--abbrev-ref", "HEAD")
	if cur == branch {
		return r.runOK("merge", "--ff-only", remoteRef)
	}
	return r.runOK("branch", "-f", branch, remoteRef)
}

// SwitchCreate creates and checks out a new branch from startRef.
func (r *Repo) SwitchCreate(name, startRef string) error {
	return r.runOK("switch", "-c", name, startRef)
}

// Switch checks out an existing branch.
func (r *Repo) Switch(name string) error {
	return r.runOK("switch", name)
}

// DeleteBranch runs git branch -d (safe; refuses unmerged).
func (r *Repo) DeleteBranch(name string) error {
	return r.runOK("branch", "-d", name)
}

// ForceDeleteBranch runs git branch -D (force local delete).
func (r *Repo) ForceDeleteBranch(name string) error {
	return r.runOK("branch", "-D", name)
}

// RebaseOnto runs: git rebase --onto onto upstream branch
func (r *Repo) RebaseOnto(onto, upstream, branch string) error {
	return r.RunInteractive("rebase", "--onto", onto, upstream, branch)
}

// RebaseOntoQuiet runs rebase without attaching to terminal (for tests).
func (r *Repo) RebaseOntoQuiet(onto, upstream, branch string) error {
	return r.runOK("rebase", "--onto", onto, upstream, branch)
}

// PushForceWithLease pushes branch with --force-with-lease.
func (r *Repo) PushForceWithLease(branch string) error {
	return r.runOK("push", "--force-with-lease", "origin",
		"refs/heads/"+branch+":refs/heads/"+branch)
}

// PushUpstream sets upstream and pushes.
func (r *Repo) PushUpstream(branch string) error {
	return r.runOK("push", "-u", "origin",
		"refs/heads/"+branch+":refs/heads/"+branch)
}

// Config sets a local git config key.
func (r *Repo) Config(key, value string) error {
	return r.runOK("config", key, value)
}

// ConfigGet returns a local config value, or "" if unset.
func (r *Repo) ConfigGet(key string) string {
	out, err := r.run("config", "--get", key)
	if err != nil {
		return ""
	}
	return out
}

// ConfigUnset removes a local config key (no error if missing).
func (r *Repo) ConfigUnset(key string) error {
	cmd := r.git("config", "--unset", key)
	_ = cmd.Run() // exit 5 = not found
	return nil
}

// GitDir returns the absolute path to .git (or common dir).
func (r *Repo) GitDir() (string, error) {
	return r.run("rev-parse", "--git-dir")
}

// StackParentConfigKey is the git config key for an explicit stack parent.
func StackParentConfigKey(branch string) string {
	return "branch." + branch + ".gitstack-parent"
}

// GetStackParent reads branch.<name>.gitstack-parent.
func (r *Repo) GetStackParent(branch string) string {
	return r.ConfigGet(StackParentConfigKey(branch))
}

// SetStackParent writes branch.<name>.gitstack-parent.
func (r *Repo) SetStackParent(branch, parent string) error {
	return r.Config(StackParentConfigKey(branch), parent)
}

// UnsetStackParent clears branch.<name>.gitstack-parent.
func (r *Repo) UnsetStackParent(branch string) error {
	return r.ConfigUnset(StackParentConfigKey(branch))
}
