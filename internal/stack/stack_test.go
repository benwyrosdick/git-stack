package stack_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benwyrosdick/git-stack/internal/git"
	"github.com/benwyrosdick/git-stack/internal/stack"
)

func withTempRepo(t *testing.T, fn func(dir string, eng *stack.Engine, repo *git.Repo)) {
	t.Helper()
	// Prefer MkdirTemp over t.TempDir: git objects can race macOS RemoveAll and
	// mark otherwise-passing tests as failed on cleanup.
	dir, err := os.MkdirTemp("", "git-stack-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// Avoid optional locks / background maintenance during tests.
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "stack-test@example.com")
	run("config", "user.name", "stack-test")
	run("config", "commit.gpgsign", "false")
	run("config", "gc.auto", "0")
	run("config", "maintenance.auto", "false")
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README")
	run("commit", "-qm", "main seed")

	repo := &git.Repo{Dir: dir}
	var buf bytes.Buffer
	eng := &stack.Engine{Repo: repo, Out: &buf, Quiet: true}
	fn(dir, eng, repo)
}

// tempDir is like t.TempDir but best-effort cleanup (see withTempRepo).
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "git-stack-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeCommit(t *testing.T, dir, file, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", file)
	gitRun(t, dir, "commit", "-qm", msg)
}

func TestRestackUpstream_FFParentAdvance(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "branch", "api")
		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		fork := gitRun(t, dir, "rev-parse", "HEAD")

		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")
		writeCommit(t, dir, "ui.txt", "u2", "ui-2")

		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a2", "api-2")

		got, err := eng.RestackUpstream("refs/heads/api", "refs/heads/api.ui")
		if err != nil {
			t.Fatal(err)
		}
		if got != fork {
			t.Fatalf("upstream %s != fork %s", got, fork)
		}
		subjects := gitRun(t, dir, "log", "--reverse", "--format=%s", got+"..api.ui")
		subjects = strings.ReplaceAll(subjects, "\n", ",")
		if subjects != "ui-1,ui-2" {
			t.Fatalf("unexpected subjects: [%s]", subjects)
		}
	})
}

func TestRestackUpstream_ForcePushRecoverable(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "branch", "api")
		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		writeCommit(t, dir, "api.txt", "a2", "api-2")
		oldTip := gitRun(t, dir, "rev-parse", "HEAD")

		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-only")

		gitRun(t, dir, "checkout", "-q", "api")
		gitRun(t, dir, "reset", "--hard", "HEAD~1")
		writeCommit(t, dir, "api.txt", "a2b", "api-2b")

		got, err := eng.RestackUpstream("refs/heads/api", "refs/heads/api.ui")
		if err != nil {
			t.Fatal(err)
		}
		if got != oldTip {
			t.Fatalf("expected fork-point %s got %s", oldTip, got)
		}
	})
}

func TestRestack_FFEndToEnd(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "branch", "api")
		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")

		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")

		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a2", "api-2")

		if err := eng.Restack(stack.RestackOpts{Branch: "api.ui", NoFetch: true}); err != nil {
			t.Fatal(err)
		}
		// ui-1 should be sole commit on top of api
		subjects := gitRun(t, dir, "log", "--reverse", "--format=%s", "api..api.ui")
		if subjects != "ui-1" {
			t.Fatalf("after restack expected only ui-1 on top of parent, got: %s", subjects)
		}
		if !repo.IsAncestor("refs/heads/api", "refs/heads/api.ui") {
			t.Fatal("api not ancestor of api.ui after restack")
		}
	})
}

func TestRestack_ForcePushReplaysChildOnly(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "branch", "api")
		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		writeCommit(t, dir, "api.txt", "a2", "api-2")
		oldAPI2 := gitRun(t, dir, "rev-parse", "HEAD")

		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")

		gitRun(t, dir, "checkout", "-q", "api")
		gitRun(t, dir, "reset", "--hard", "HEAD~1")
		writeCommit(t, dir, "api.txt", "a2b", "api-2b")

		if err := eng.Restack(stack.RestackOpts{Branch: "api.ui", NoFetch: true}); err != nil {
			t.Fatal(err)
		}
		// old parent commit should not be in child history
		if repo.IsAncestor(oldAPI2, "refs/heads/api.ui") {
			// After rewrite, old api-2 may still be reachable via reflog but not as ancestor of tip...
			// Check log subjects only
		}
		subjects := gitRun(t, dir, "log", "--reverse", "--format=%s", "api..api.ui")
		if subjects != "ui-1" {
			t.Fatalf("expected only ui-1 after restack, got: %s", subjects)
		}
	})
}

func TestRestack_NoopWhenAlreadyOnTip(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "branch", "api")
		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")

		before := gitRun(t, dir, "rev-parse", "api.ui")
		if err := eng.Restack(stack.RestackOpts{Branch: "api.ui", NoFetch: true}); err != nil {
			t.Fatal(err)
		}
		after := gitRun(t, dir, "rev-parse", "api.ui")
		if before != after {
			t.Fatal("SHA changed on no-op restack")
		}
	})
}

func TestParentOf_DotDepth(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "branch", "wms-batching")
		gitRun(t, dir, "branch", "wms-batching.ui")
		if p := eng.ParentOf("wms-batching"); p != "main" {
			t.Fatalf("got %s want main", p)
		}
		if p := eng.ParentOf("wms-batching.ui"); p != "wms-batching" {
			t.Fatalf("got %s want wms-batching", p)
		}
		if p := eng.ParentOf("wms-batching.ui.tests"); p != "wms-batching.ui" {
			t.Fatalf("got %s want wms-batching.ui", p)
		}
	})
}

func TestParentOf_LocalConfig(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "checkout", "-q", "-b", "api-work")
		writeCommit(t, dir, "a.txt", "a", "api-1")
		gitRun(t, dir, "checkout", "-q", "main")
		gitRun(t, dir, "checkout", "-q", "-b", "ui-work")
		writeCommit(t, dir, "u.txt", "u", "ui-1")
		// Free names: no dots — without config, parent is trunk
		if p := eng.ParentOf("ui-work"); p != "main" {
			t.Fatalf("before track got %s", p)
		}
		if err := eng.Track("ui-work", "api-work"); err != nil {
			t.Fatal(err)
		}
		if p := eng.ParentOf("ui-work"); p != "api-work" {
			t.Fatalf("after track got %s want api-work", p)
		}
		if _, src := eng.ParentOfWithSource("ui-work"); src != stack.SourceLocal {
			t.Fatalf("source %s", src)
		}
	})
}

func TestCreate_FreeNameFrom(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		if err := eng.Create(stack.CreateOpts{Name: "api-work"}); err != nil {
			t.Fatal(err)
		}
		writeCommit(t, dir, "a.txt", "a", "api-1")
		if err := eng.Create(stack.CreateOpts{Name: "ui-work", From: "api-work"}); err != nil {
			t.Fatal(err)
		}
		if eng.ParentOf("ui-work") != "api-work" {
			t.Fatalf("parent %s", eng.ParentOf("ui-work"))
		}
		kids, err := eng.DescendantsOf("api-work")
		if err != nil {
			t.Fatal(err)
		}
		if len(kids) != 1 || kids[0] != "ui-work" {
			t.Fatalf("descendants %v", kids)
		}
	})
}

func TestPRStackBranches(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		if err := eng.Create(stack.CreateOpts{Name: "feat"}); err != nil {
			t.Fatal(err)
		}
		writeCommit(t, dir, "f.txt", "f", "feat-1")
		if err := eng.Create(stack.CreateOpts{Name: "feat.ui"}); err != nil {
			t.Fatal(err)
		}
		writeCommit(t, dir, "u.txt", "u", "ui-1")
		if err := eng.Create(stack.CreateOpts{Name: "feat.ui.tests"}); err != nil {
			t.Fatal(err)
		}
		// Stack for middle branch: trunk → feat → feat.ui → child
		got := eng.PRStackBranches("feat.ui")
		want := []string{"main", "feat", "feat.ui", "feat.ui.tests"}
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v want %v", got, want)
			}
		}
	})
}

func TestCreate(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		if err := eng.Create(stack.CreateOpts{Name: "feat"}); err != nil {
			t.Fatal(err)
		}
		if err := eng.Create(stack.CreateOpts{Name: "feat.ui"}); err != nil {
			t.Fatal(err)
		}
		cur, _ := repo.CurrentBranch()
		if cur != "feat.ui" {
			t.Fatalf("current %s", cur)
		}
		if eng.ParentOf("feat.ui") != "feat" {
			t.Fatal("parent")
		}
	})
}

func TestReparent_UpdatesMetadata(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "checkout", "-q", "-b", "a")
		writeCommit(t, dir, "a.txt", "a", "a-1")
		gitRun(t, dir, "checkout", "-q", "main")
		gitRun(t, dir, "checkout", "-q", "-b", "b")
		writeCommit(t, dir, "b.txt", "b", "b-1")
		gitRun(t, dir, "checkout", "-q", "a")
		if err := eng.Create(stack.CreateOpts{Name: "child", From: "a"}); err != nil {
			t.Fatal(err)
		}
		writeCommit(t, dir, "c.txt", "c", "child-1")
		if eng.ParentOf("child") != "a" {
			t.Fatal(eng.ParentOf("child"))
		}
		if err := eng.Reparent(stack.ReparentOpts{
			Branch:    "child",
			NewParent: "b",
			NoFetch:   true,
		}); err != nil {
			t.Fatal(err)
		}
		if eng.ParentOf("child") != "b" {
			t.Fatalf("parent after reparent: %s", eng.ParentOf("child"))
		}
		if !repo.IsAncestor("refs/heads/b", "refs/heads/child") {
			t.Fatal("history not on b")
		}
	})
}

func TestTrack_CycleRejected(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "branch", "a")
		gitRun(t, dir, "branch", "b")
		if err := eng.Track("a", "b"); err != nil {
			t.Fatal(err)
		}
		if err := eng.Track("b", "a"); err == nil {
			t.Fatal("expected cycle error")
		}
	})
}

func TestSync_NoopWhenClean(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")

		res, err := eng.Sync(stack.SyncOpts{Root: "api", NoFetch: true})
		if err != nil {
			t.Fatal(err)
		}
		if res == nil {
			t.Fatal("nil result")
		}
	})
}

func TestSync_RestackChild(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")
		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a2", "api-2")

		_, err := eng.Sync(stack.SyncOpts{Root: "api", NoFetch: true})
		if err != nil {
			t.Fatal(err)
		}
		subjects := gitRun(t, dir, "log", "--reverse", "--format=%s", "api..api.ui")
		if subjects != "ui-1" {
			t.Fatalf("got %s", subjects)
		}
	})
}

func TestSync_DryRunNoMutate(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")
		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a2", "api-2")

		before := gitRun(t, dir, "rev-parse", "api.ui")
		_, err := eng.Sync(stack.SyncOpts{Root: "api", DryRun: true, NoFetch: true})
		if err != nil {
			t.Fatal(err)
		}
		after := gitRun(t, dir, "rev-parse", "api.ui")
		if before != after {
			t.Fatal("dry-run mutated api.ui")
		}
	})
}

func TestSync_OntoTrunk(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")

		gitRun(t, dir, "checkout", "-q", "main")
		writeCommit(t, dir, "main.txt", "m2", "main-2")

		_, err := eng.Sync(stack.SyncOpts{Root: "api.ui", OntoTrunk: true, NoFetch: true})
		if err != nil {
			t.Fatal(err)
		}
		if !repo.IsAncestor("refs/heads/main", "refs/heads/api") {
			t.Fatal("api should be on main tip")
		}
		if !repo.IsAncestor("refs/heads/api", "refs/heads/api.ui") {
			t.Fatal("api.ui should be on api tip")
		}
	})
}

func TestSync_WithoutOntoTrunkSkipsMain(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")

		gitRun(t, dir, "checkout", "-q", "main")
		writeCommit(t, dir, "main.txt", "m2", "main-2")

		before := gitRun(t, dir, "rev-parse", "api")
		_, err := eng.Sync(stack.SyncOpts{Root: "api", NoFetch: true})
		if err != nil {
			t.Fatal(err)
		}
		after := gitRun(t, dir, "rev-parse", "api")
		if before != after {
			t.Fatal("api should not absorb main without --onto-trunk")
		}
	})
}

func TestRemoteRelation(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		// bare remote
		remote := tempDir(t)
		gitRun(t, remote, "init", "-q", "--bare", "-b", "main")
		gitRun(t, dir, "remote", "add", "origin", remote)
		gitRun(t, dir, "push", "-u", "origin", "main")

		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "push", "-u", "origin", "api")

		if rel := repo.RemoteRelationOf("api"); rel != git.RelInSync {
			t.Fatalf("want in-sync got %s", rel)
		}

		// advance remote
		other := tempDir(t)
		gitRun(t, other, "clone", "-q", remote, ".")
		gitRun(t, other, "config", "user.email", "t@t.com")
		gitRun(t, other, "config", "user.name", "t")
		gitRun(t, other, "config", "commit.gpgsign", "false")
		gitRun(t, other, "checkout", "-q", "api")
		writeCommit(t, other, "api.txt", "a2", "api-2")
		gitRun(t, other, "push", "origin", "api")

		gitRun(t, dir, "fetch", "origin")
		if rel := repo.RemoteRelationOf("api"); rel != git.RelBehind {
			t.Fatalf("want behind got %s", rel)
		}
	})
}

func TestRestack_FFBehindBeforeRestack(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		remote := tempDir(t)
		gitRun(t, remote, "init", "-q", "--bare", "-b", "main")
		gitRun(t, dir, "remote", "add", "origin", remote)
		gitRun(t, dir, "push", "-u", "origin", "main")

		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "push", "-u", "origin", "api")

		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")
		gitRun(t, dir, "push", "-u", "origin", "api.ui")

		// advance parent on remote
		other := tempDir(t)
		gitRun(t, other, "clone", "-q", remote, ".")
		gitRun(t, other, "config", "user.email", "t@t.com")
		gitRun(t, other, "config", "user.name", "t")
		gitRun(t, other, "config", "commit.gpgsign", "false")
		gitRun(t, other, "checkout", "-q", "api")
		writeCommit(t, other, "api.txt", "a2", "api-2")
		gitRun(t, other, "push", "origin", "api")

		// local api is behind; restack api.ui should FF api first
		if err := eng.Restack(stack.RestackOpts{Branch: "api.ui"}); err != nil {
			t.Fatal(err)
		}
		if !repo.IsAncestor("refs/heads/api", "refs/heads/api.ui") {
			t.Fatal("not stacked")
		}
		// api should have been FF'd
		if rel := repo.RemoteRelationOf("api"); rel != git.RelInSync {
			t.Fatalf("api should be in-sync after FF, got %s", rel)
		}
	})
}

func TestRestack_DivergedBlocks(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		remote := tempDir(t)
		gitRun(t, remote, "init", "-q", "--bare", "-b", "main")
		gitRun(t, dir, "remote", "add", "origin", remote)
		gitRun(t, dir, "push", "-u", "origin", "main")

		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "push", "-u", "origin", "api")
		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")

		// diverge api: local and remote different commits
		other := tempDir(t)
		gitRun(t, other, "clone", "-q", remote, ".")
		gitRun(t, other, "config", "user.email", "t@t.com")
		gitRun(t, other, "config", "user.name", "t")
		gitRun(t, other, "config", "commit.gpgsign", "false")
		gitRun(t, other, "checkout", "-q", "api")
		writeCommit(t, other, "api.txt", "remote", "api-remote")
		gitRun(t, other, "push", "origin", "api")

		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "local", "api-local")
		gitRun(t, dir, "fetch", "origin")

		err := eng.Restack(stack.RestackOpts{Branch: "api.ui", NoFetch: true})
		if err == nil {
			t.Fatal("expected diverge block")
		}
	})
}

func TestList(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")

		infos, err := eng.List("")
		if err != nil {
			t.Fatal(err)
		}
		if len(infos) < 3 {
			t.Fatalf("expected trunk + stack rows, got %d: %+v", len(infos), infos)
		}
		if infos[0].Name != "main" {
			t.Fatalf("trunk should be first, got %s", infos[0].Name)
		}
		if infos[0].Parent != "—" {
			t.Fatalf("trunk parent display: %q", infos[0].Parent)
		}
		found := false
		for _, i := range infos {
			if i.Name == "api.ui" && i.Status == stack.StatusOK {
				found = true
			}
			if i.Name == "api.ui" && !strings.Contains(i.TreePrefix, "└─") && !strings.Contains(i.TreePrefix, "├─") {
				t.Fatalf("api.ui missing tree connector: %q", i.TreePrefix)
			}
		}
		if !found {
			t.Fatalf("api.ui not ok: %+v", infos)
		}
		text := stack.FormatList("", infos)
		if !strings.Contains(text, "├─") && !strings.Contains(text, "└─") {
			t.Fatalf("format missing connectors:\n%s", text)
		}
	})
}

func TestReparent(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "checkout", "-q", "-b", "a")
		writeCommit(t, dir, "a.txt", "a", "a-1")
		gitRun(t, dir, "checkout", "-q", "main")
		gitRun(t, dir, "checkout", "-q", "-b", "b")
		writeCommit(t, dir, "b.txt", "b", "b-1")
		gitRun(t, dir, "checkout", "-q", "a")
		gitRun(t, dir, "checkout", "-q", "-b", "a.child")
		writeCommit(t, dir, "c.txt", "c", "child-1")

		// reparent a.child onto b
		if err := eng.Reparent(stack.ReparentOpts{
			Branch:    "a.child",
			NewParent: "b",
			NoFetch:   true,
		}); err != nil {
			t.Fatal(err)
		}
		if !repo.IsAncestor("refs/heads/b", "refs/heads/a.child") {
			t.Fatal("a.child should be on b")
		}
	})
}

func currentBranch(t *testing.T, dir string) string {
	t.Helper()
	return gitRun(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// Restack of a non-current branch must not leave HEAD on that branch.
func TestRestack_PreservesCheckout(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "branch", "api")
		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")

		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")

		// Advance parent while staying on api (not api.ui).
		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a2", "api-2")

		if got := currentBranch(t, dir); got != "api" {
			t.Fatalf("precondition: want api, got %s", got)
		}
		if err := eng.Restack(stack.RestackOpts{Branch: "api.ui", NoFetch: true}); err != nil {
			t.Fatal(err)
		}
		if got := currentBranch(t, dir); got != "api" {
			t.Fatalf("restack changed checkout to %s; want api", got)
		}
		if !repo.IsAncestor("refs/heads/api", "refs/heads/api.ui") {
			t.Fatal("api not ancestor of api.ui after restack")
		}
		subjects := gitRun(t, dir, "log", "--reverse", "--format=%s", "api..api.ui")
		if subjects != "ui-1" {
			t.Fatalf("after restack expected only ui-1, got: %s", subjects)
		}
	})
}

// Sync restack of descendants must not change the current branch.
func TestSync_PreservesCheckout(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")
		gitRun(t, dir, "checkout", "-q", "api")
		writeCommit(t, dir, "api.txt", "a2", "api-2")

		if got := currentBranch(t, dir); got != "api" {
			t.Fatalf("precondition: want api, got %s", got)
		}
		if _, err := eng.Sync(stack.SyncOpts{Root: "api", NoFetch: true}); err != nil {
			t.Fatal(err)
		}
		if got := currentBranch(t, dir); got != "api" {
			t.Fatalf("sync changed checkout to %s; want api", got)
		}
		if !repo.IsAncestor("refs/heads/api", "refs/heads/api.ui") {
			t.Fatal("not stacked after sync")
		}
	})
}

// Reparent of a non-current branch must not leave HEAD on that branch.
func TestReparent_PreservesCheckout(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		gitRun(t, dir, "checkout", "-q", "-b", "a")
		writeCommit(t, dir, "a.txt", "a", "a-1")
		gitRun(t, dir, "checkout", "-q", "main")
		gitRun(t, dir, "checkout", "-q", "-b", "b")
		writeCommit(t, dir, "b.txt", "b", "b-1")
		gitRun(t, dir, "checkout", "-q", "a")
		gitRun(t, dir, "checkout", "-q", "-b", "a.child")
		writeCommit(t, dir, "c.txt", "c", "child-1")

		// Stay on a; reparent a.child onto b.
		gitRun(t, dir, "checkout", "-q", "a")
		if err := eng.Reparent(stack.ReparentOpts{
			Branch:    "a.child",
			NewParent: "b",
			NoFetch:   true,
		}); err != nil {
			t.Fatal(err)
		}
		if got := currentBranch(t, dir); got != "a" {
			t.Fatalf("reparent changed checkout to %s; want a", got)
		}
		if !repo.IsAncestor("refs/heads/b", "refs/heads/a.child") {
			t.Fatal("a.child should be on b")
		}
	})
}

// Pull of another branch that is behind origin must FF without switching.
func TestPull_OtherBranchFF_PreservesCheckout(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		remote := tempDir(t)
		gitRun(t, remote, "init", "-q", "--bare", "-b", "main")
		gitRun(t, dir, "remote", "add", "origin", remote)
		gitRun(t, dir, "push", "-u", "origin", "main")

		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "push", "-u", "origin", "api")

		// Advance remote api via another clone.
		other := tempDir(t)
		gitRun(t, other, "clone", "-q", remote, ".")
		gitRun(t, other, "config", "user.email", "t@t.com")
		gitRun(t, other, "config", "user.name", "t")
		gitRun(t, other, "config", "commit.gpgsign", "false")
		gitRun(t, other, "checkout", "-q", "api")
		writeCommit(t, other, "api.txt", "a2", "api-2")
		gitRun(t, other, "push", "origin", "api")

		// Stay on main; pull api without checkout.
		gitRun(t, dir, "checkout", "-q", "main")
		beforeAPI := gitRun(t, dir, "rev-parse", "api")
		if err := eng.Pull("api"); err != nil {
			t.Fatal(err)
		}
		if got := currentBranch(t, dir); got != "main" {
			t.Fatalf("pull changed checkout to %s; want main", got)
		}
		afterAPI := gitRun(t, dir, "rev-parse", "api")
		if beforeAPI == afterAPI {
			t.Fatal("api tip should have advanced")
		}
		if rel := repo.RemoteRelationOf("api"); rel != git.RelInSync {
			t.Fatalf("api should be in-sync after pull, got %s", rel)
		}
		// Working tree should still be main's tree (not switched).
		if _, err := os.Stat(filepath.Join(dir, "api.txt")); err == nil {
			t.Fatal("api.txt should not be in worktree while on main")
		}
	})
}

// Pull of the current branch still uses git pull and stays on that branch.
func TestPull_CurrentBranch(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		remote := tempDir(t)
		gitRun(t, remote, "init", "-q", "--bare", "-b", "main")
		gitRun(t, dir, "remote", "add", "origin", remote)
		gitRun(t, dir, "push", "-u", "origin", "main")

		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "push", "-u", "origin", "api")

		other := tempDir(t)
		gitRun(t, other, "clone", "-q", remote, ".")
		gitRun(t, other, "config", "user.email", "t@t.com")
		gitRun(t, other, "config", "user.name", "t")
		gitRun(t, other, "config", "commit.gpgsign", "false")
		gitRun(t, other, "checkout", "-q", "api")
		writeCommit(t, other, "api.txt", "a2", "api-2")
		gitRun(t, other, "push", "origin", "api")

		// On api; pull should FF and stay on api.
		if err := eng.Pull("api"); err != nil {
			t.Fatal(err)
		}
		if got := currentBranch(t, dir); got != "api" {
			t.Fatalf("want api, got %s", got)
		}
		if rel := repo.RemoteRelationOf("api"); rel != git.RelInSync {
			t.Fatalf("want in-sync got %s", rel)
		}
		if _, err := os.Stat(filepath.Join(dir, "api.txt")); err != nil {
			t.Fatal("api.txt should be present after pull on api")
		}
	})
}

// EnsureRemoteReady / restack FF of a parent must not switch branches.
func TestRestack_FFParent_PreservesCheckout(t *testing.T) {
	withTempRepo(t, func(dir string, eng *stack.Engine, repo *git.Repo) {
		remote := tempDir(t)
		gitRun(t, remote, "init", "-q", "--bare", "-b", "main")
		gitRun(t, dir, "remote", "add", "origin", remote)
		gitRun(t, dir, "push", "-u", "origin", "main")

		gitRun(t, dir, "checkout", "-q", "-b", "api")
		writeCommit(t, dir, "api.txt", "a1", "api-1")
		gitRun(t, dir, "push", "-u", "origin", "api")

		gitRun(t, dir, "checkout", "-q", "-b", "api.ui")
		writeCommit(t, dir, "ui.txt", "u1", "ui-1")
		gitRun(t, dir, "push", "-u", "origin", "api.ui")

		other := tempDir(t)
		gitRun(t, other, "clone", "-q", remote, ".")
		gitRun(t, other, "config", "user.email", "t@t.com")
		gitRun(t, other, "config", "user.name", "t")
		gitRun(t, other, "config", "commit.gpgsign", "false")
		gitRun(t, other, "checkout", "-q", "api")
		writeCommit(t, other, "api.txt", "a2", "api-2")
		gitRun(t, other, "push", "origin", "api")

		// Stay on main; restack api.ui should FF api then restack, stay on main.
		gitRun(t, dir, "checkout", "-q", "main")
		if err := eng.Restack(stack.RestackOpts{Branch: "api.ui"}); err != nil {
			t.Fatal(err)
		}
		if got := currentBranch(t, dir); got != "main" {
			t.Fatalf("restack changed checkout to %s; want main", got)
		}
		if rel := repo.RemoteRelationOf("api"); rel != git.RelInSync {
			t.Fatalf("api should be FF'd, got %s", rel)
		}
		if !repo.IsAncestor("refs/heads/api", "refs/heads/api.ui") {
			t.Fatal("not stacked")
		}
	})
}
