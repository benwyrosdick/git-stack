package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/benwyrosdick/git-stack/internal/cli"
)

func main() {
	if err := cli.Run(os.Args[1:]); err != nil {
		// Multiline playbooks (diverge, conflict) print in full — do not collapse.
		msg := strings.TrimRight(err.Error(), "\n")
		fmt.Fprintf(os.Stderr, "stack: %s\n", msg)
		os.Exit(1)
	}
}
