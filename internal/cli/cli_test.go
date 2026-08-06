package cli

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// parent -v must not be stolen by global --version handling.
func TestParentVerbose_NotVersion(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "t@t.com")
	run("config", "user.name", "t")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-qm", "seed")
	run("branch", "api")

	// Run CLI with cwd = temp repo.
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = Run([]string{"parent", "-v", "api"})
	_ = w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	out := strings.TrimSpace(buf.String())
	// Verbose parent should mention source, not look like only a version string.
	if out == "" {
		t.Fatal("empty output")
	}
	if !strings.Contains(out, "main") {
		t.Fatalf("expected parent main in output, got %q", out)
	}
	// Version strings look like 0.1.9-... or dev+...
	if strings.HasPrefix(out, "0.") || strings.HasPrefix(out, "dev") {
		t.Fatalf("looks like version, not parent -v: %q", out)
	}
}
