package gh

import (
	"strings"
	"testing"
)

func TestUpsertStackSection_Prepend(t *testing.T) {
	got := UpsertStackSection("## Summary\n\nhello", "**Stack**\n\n- `main`")
	if !strings.HasPrefix(got, StackSectionStart) {
		t.Fatalf("expected section at top:\n%s", got)
	}
	if !strings.Contains(got, StackSectionEnd+"\n\n## Summary") {
		t.Fatalf("expected end marker then user body:\n%s", got)
	}
	if !strings.Contains(got, "**Stack**") {
		t.Fatalf("missing stack content:\n%s", got)
	}
}

func TestUpsertStackSection_Replace(t *testing.T) {
	body := StackSectionStart + "\n**Stack**\n\n- old\n" + StackSectionEnd + "\n\nkeep me"
	got := UpsertStackSection(body, "**Stack**\n\n- new")
	if strings.Contains(got, "old") {
		t.Fatalf("old section not replaced:\n%s", got)
	}
	if !strings.Contains(got, "- new") {
		t.Fatalf("new section missing:\n%s", got)
	}
	if !strings.Contains(got, "keep me") {
		t.Fatalf("user body lost:\n%s", got)
	}
	// Exactly one pair of markers
	if strings.Count(got, StackSectionStart) != 1 || strings.Count(got, StackSectionEnd) != 1 {
		t.Fatalf("marker count wrong:\n%s", got)
	}
}

func TestUpsertStackSection_EmptyBody(t *testing.T) {
	got := UpsertStackSection("", "**Stack**\n\n- `x`")
	want := StackSectionStart + "\n**Stack**\n\n- `x`\n" + StackSectionEnd + "\n"
	if got != want {
		t.Fatalf("got:\n%q\nwant:\n%q", got, want)
	}
}

func TestFormatStackMarkdown(t *testing.T) {
	prs := map[string]PRInfo{
		"feat.a": {Number: 10, URL: "https://example.com/10", Head: "feat.a"},
		"feat.b": {Number: 11, URL: "https://example.com/11", Head: "feat.b"},
	}
	got := FormatStackMarkdown(prs, []string{"main", "feat.a", "feat.b"}, "feat.b")
	if !strings.Contains(got, "`main`") {
		t.Fatalf("missing trunk:\n%s", got)
	}
	if !strings.Contains(got, "[#10](https://example.com/10) `feat.a`") {
		t.Fatalf("missing linked PR:\n%s", got)
	}
	if !strings.Contains(got, "**[#11](https://example.com/11) `feat.b`** ←") {
		t.Fatalf("current PR not marked:\n%s", got)
	}
}
