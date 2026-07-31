package stack

import "testing"

func TestOrderAsTree_DedupesSameName(t *testing.T) {
	infos := []BranchInfo{
		{Name: "main", Parent: "—", Depth: 0},
		{Name: "feat/order-create-detail", Parent: "main", Depth: 1, PRNumber: 4473},
		// Duplicate of the child: one without PR (stale), one with PR (fresh).
		{Name: "feat/order-create-detail.index", Parent: "feat/order-create-detail", Depth: 2, PRNumber: 0, ShortSHA: "dbaefc7"},
		{Name: "feat/order-create-detail.index", Parent: "feat/order-create-detail", Depth: 2, PRNumber: 4487, ShortSHA: "dbaefc7"},
	}
	out := OrderAsTree(infos)
	seen := map[string]int{}
	for _, info := range out {
		seen[info.Name]++
	}
	for name, n := range seen {
		if n != 1 {
			t.Fatalf("name %q appears %d times in OrderAsTree output: %+v", name, n, out)
		}
	}
	var child *BranchInfo
	for i := range out {
		if out[i].Name == "feat/order-create-detail.index" {
			child = &out[i]
			break
		}
	}
	if child == nil {
		t.Fatalf("child missing: %+v", out)
	}
	if child.PRNumber != 4487 {
		t.Fatalf("expected higher PR number to win, got %d", child.PRNumber)
	}
	if len(out) != 3 {
		t.Fatalf("expected 3 rows (main, parent, child), got %d: %+v", len(out), out)
	}
}

func TestOrderAsTree_TrimSpaceNames(t *testing.T) {
	infos := []BranchInfo{
		{Name: "main", Parent: "—", Depth: 0},
		{Name: "  feat.a  ", Parent: "main", Depth: 1, PRNumber: 1},
		{Name: "feat.a", Parent: "main", Depth: 1, PRNumber: 2},
	}
	out := OrderAsTree(infos)
	if len(out) != 2 {
		t.Fatalf("expected trim+dedupe to 2 rows, got %d: %+v", len(out), out)
	}
	if out[1].Name != "feat.a" {
		t.Fatalf("name not trimmed: %q", out[1].Name)
	}
	if out[1].PRNumber != 2 {
		t.Fatalf("expected PR 2, got %d", out[1].PRNumber)
	}
}

func TestOrderAsTree_UniqueChildren(t *testing.T) {
	// Same child listed under parent twice with identical PR — still one row.
	infos := []BranchInfo{
		{Name: "main", Parent: "—", Depth: 0},
		{Name: "a", Parent: "main", Depth: 1},
		{Name: "a.b", Parent: "a", Depth: 2, PRNumber: 5},
		{Name: "a.b", Parent: "a", Depth: 2, PRNumber: 5},
	}
	out := OrderAsTree(infos)
	if len(out) != 3 {
		t.Fatalf("expected 3, got %d: %+v", len(out), out)
	}
}
