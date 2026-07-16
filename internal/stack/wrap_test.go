package stack

import (
	"fmt"
	"strings"
	"testing"
)

func TestWrapRemoteErr_Multiline(t *testing.T) {
	play := `cannot cleanly proceed — branch 'wms-batching' has diverged from origin

  local:  9b913d526  (commits not on origin below)
  origin: 9f59cb973  (commits not local below)

  Commits only local:
    abc def

  Resolve, then re-run:
    git switch wms-batching && git reset --hard origin/wms-batching
`
	err := wrapRemoteErr("fix 'wms-batching' vs origin before restacking", fmt.Errorf("%s", play))
	if err == nil {
		t.Fatal("expected error")
	}
	s := err.Error()
	if !strings.Contains(s, "\n\n") {
		t.Fatalf("expected blank line between summary and playbook:\n%s", s)
	}
	if strings.Count(s, "\n") < 5 {
		t.Fatalf("expected multiline error:\n%s", s)
	}
	if !strings.HasPrefix(s, "fix 'wms-batching'") {
		t.Fatalf("summary first:\n%s", s)
	}
	// Must not collapse playbook body to one line
	if strings.Contains(s, "origin below)   origin:") {
		t.Fatalf("newlines collapsed:\n%s", s)
	}
}
