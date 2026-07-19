package gh

import (
	"fmt"
	"os"
	"strings"
)

// Markers for the auto-managed stack section in a PR body.
// Edit freely above/below; content between markers is rewritten on create/update.
const (
	StackSectionStart = "<!-- git-stack -->"
	StackSectionEnd   = "<!-- /git-stack -->"
)

// UpsertStackSection puts stack markdown between markers at the top of the body.
// If markers already exist, only the section between them is replaced.
// Otherwise the section is prepended.
func UpsertStackSection(body, inner string) string {
	inner = strings.TrimSpace(inner)
	section := StackSectionStart + "\n" + inner + "\n" + StackSectionEnd

	start := strings.Index(body, StackSectionStart)
	end := strings.Index(body, StackSectionEnd)
	if start >= 0 && end >= start {
		end += len(StackSectionEnd)
		// Drop a single trailing newline after the end marker so we don't stack blanks.
		rest := body[end:]
		rest = strings.TrimPrefix(rest, "\n")
		prefix := body[:start]
		// Keep any user content before the section (unusual if we always prepend).
		prefix = strings.TrimRight(prefix, "\n")
		if prefix == "" {
			if rest == "" {
				return section + "\n"
			}
			return section + "\n\n" + strings.TrimLeft(rest, "\n")
		}
		if rest == "" {
			return prefix + "\n\n" + section + "\n"
		}
		return prefix + "\n\n" + section + "\n\n" + strings.TrimLeft(rest, "\n")
	}

	body = strings.TrimSpace(body)
	if body == "" {
		return section + "\n"
	}
	return section + "\n\n" + body
}

// FormatStackMarkdown builds the inner stack section (no markers) for a branch list.
// branches should be ordered base → tip (typically trunk first). current is marked.
func FormatStackMarkdown(prs map[string]PRInfo, branches []string, current string) string {
	if len(branches) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## PR Stack\n")
	for _, name := range branches {
		if name == "" {
			continue
		}
		line := formatStackItem(name, prs[name], name == current)
		b.WriteString("\n")
		b.WriteString(line)
	}
	b.WriteString("\n")
	return b.String()
}

func formatStackItem(name string, info PRInfo, current bool) string {
	var item string
	switch {
	case info.URL != "" && info.Number > 0:
		item = fmt.Sprintf("[#%d](%s) `%s`", info.Number, info.URL, name)
	case info.URL != "":
		item = fmt.Sprintf("[%s](%s)", name, info.URL)
	default:
		item = fmt.Sprintf("`%s`", name)
	}
	if current {
		return fmt.Sprintf("- **%s** ←", item)
	}
	return "- " + item
}

// updatePRBodyStack fetches the PR body, upserts the stack section, and edits the PR.
func (c *Client) updatePRBodyStack(branch string, stackMarkdown string) error {
	if strings.TrimSpace(stackMarkdown) == "" {
		return nil
	}
	body, err := c.run("pr", "view", branch, "--json", "body", "--jq", ".body // \"\"")
	if err != nil {
		return err
	}
	// gh/jq may return the literal null string in some versions.
	if body == "null" {
		body = ""
	}
	newBody := UpsertStackSection(body, stackMarkdown)

	f, err := os.CreateTemp("", "git-stack-pr-body-*.md")
	if err != nil {
		return err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.WriteString(newBody); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_, err = c.run("pr", "edit", branch, "--body-file", path)
	return err
}
