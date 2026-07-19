package tui

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// copyToClipboard writes text to the system clipboard.
// Tries wl-copy, xclip, xsel, pbcopy, then OSC 52 (terminal clipboard).
func copyToClipboard(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("nothing to copy")
	}

	type tool struct {
		name string
		args []string
	}
	candidates := []tool{
		{"wl-copy", nil},
		{"xclip", []string{"-selection", "clipboard"}},
		{"xsel", []string{"--clipboard", "--input"}},
		{"pbcopy", nil},
	}
	for _, t := range candidates {
		if _, err := exec.LookPath(t.name); err != nil {
			continue
		}
		cmd := exec.Command(t.name, t.args...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s: %w", t.name, err)
		}
		return nil
	}

	// OSC 52 — works in many terminals (Kitty, iTerm, Windows Terminal, etc.)
	enc := base64.StdEncoding.EncodeToString([]byte(text))
	seq := fmt.Sprintf("\033]52;c;%s\a", enc)
	if _, err := fmt.Fprint(os.Stderr, seq); err != nil {
		return fmt.Errorf("no clipboard tool (wl-copy/xclip/xsel/pbcopy) and OSC 52 failed: %w", err)
	}
	return nil
}
