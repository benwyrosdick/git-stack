// Package cli implements git-stack subcommands.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/benwyrosdick/git-stack/internal/gh"
	"github.com/benwyrosdick/git-stack/internal/git"
	"github.com/benwyrosdick/git-stack/internal/stack"
	"github.com/benwyrosdick/git-stack/internal/tui"
)

// Version is set via -ldflags at release time.
var Version = "dev"

// Run parses args and executes. args should be os.Args[1:].
func Run(args []string) error {
	if len(args) == 0 {
		return runTUI()
	}
	cmd := args[0]
	rest := args[1:]
	switch cmd {
	case "help", "-h", "--help":
		printHelp(os.Stdout)
		return nil
	case "version", "--version", "-v":
		fmt.Println(Version)
		return nil
	case "parent":
		return cmdParent(rest)
	case "create":
		return cmdCreate(rest)
	case "restack":
		return cmdRestack(rest)
	case "reparent":
		return cmdReparent(rest)
	case "sync":
		return cmdSync(rest)
	case "pr":
		return cmdPR(rest)
	case "ls", "list":
		return cmdLS(rest)
	case "tui":
		return runTUI()
	default:
		return fmt.Errorf("unknown command: %s (try: git-stack help)", cmd)
	}
}

func engine() (*stack.Engine, *git.Repo, error) {
	repo := &git.Repo{}
	if err := repo.RequireRepo(); err != nil {
		return nil, nil, err
	}
	return &stack.Engine{Repo: repo, Out: os.Stderr}, repo, nil
}

func cmdParent(args []string) error {
	eng, _, err := engine()
	if err != nil {
		return err
	}
	branch := ""
	if len(args) > 0 {
		branch = args[0]
	}
	p, err := eng.Parent(branch)
	if err != nil {
		return err
	}
	fmt.Println(p)
	return nil
}

func cmdCreate(args []string) error {
	eng, _, err := engine()
	if err != nil {
		return err
	}
	opts := stack.CreateOpts{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from", "--start", "--parent":
			if i+1 >= len(args) {
				return fmt.Errorf("create: %s requires an argument", args[i])
			}
			opts.From = args[i+1]
			i++
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("create: unknown flag %s", args[i])
			}
			if opts.Name != "" {
				return fmt.Errorf("create: unexpected argument %s", args[i])
			}
			opts.Name = args[i]
		}
	}
	return eng.Create(opts)
}

func cmdRestack(args []string) error {
	eng, _, err := engine()
	if err != nil {
		return err
	}
	opts := stack.RestackOpts{}
	for _, a := range args {
		switch a {
		case "--push":
			opts.Push = true
		case "--onto-trunk":
			opts.OntoTrunk = true
		case "--no-fetch":
			opts.NoFetch = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("restack: unknown flag %s", a)
			}
			if opts.Branch != "" {
				return fmt.Errorf("restack: unexpected argument %s", a)
			}
			opts.Branch = a
		}
	}
	return eng.Restack(opts)
}

func cmdReparent(args []string) error {
	eng, repo, err := engine()
	if err != nil {
		return err
	}
	opts := stack.ReparentOpts{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from":
			if i+1 >= len(args) {
				return fmt.Errorf("reparent: --from requires an argument")
			}
			opts.OldParent = args[i+1]
			i++
		case "--push":
			opts.Push = true
		case "--no-fetch":
			opts.NoFetch = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("reparent: unknown flag %s", args[i])
			}
			if opts.Branch == "" {
				opts.Branch = args[i]
			} else if opts.NewParent == "" {
				opts.NewParent = args[i]
			} else {
				return fmt.Errorf("reparent: unexpected argument %s", args[i])
			}
		}
	}
	if err := eng.Reparent(opts); err != nil {
		return err
	}
	// Best-effort PR retarget (matches bash)
	if gh.Available() {
		_ = gh.RetargetBase(repo, opts.Branch, opts.NewParent)
	}
	return nil
}

func cmdSync(args []string) error {
	eng, _, err := engine()
	if err != nil {
		return err
	}
	opts := stack.SyncOpts{}
	for _, a := range args {
		switch a {
		case "--push":
			opts.Push = true
		case "--onto-trunk":
			opts.OntoTrunk = true
		case "--dry-run":
			opts.DryRun = true
		case "--no-fetch":
			opts.NoFetch = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("sync: unknown flag %s", a)
			}
			if opts.Root != "" {
				return fmt.Errorf("sync: unexpected argument %s", a)
			}
			opts.Root = a
		}
	}
	_, err = eng.Sync(opts)
	return err
}

func cmdPR(args []string) error {
	eng, repo, err := engine()
	if err != nil {
		return err
	}
	opts := gh.PROpts{}
	for _, a := range args {
		switch a {
		case "--draft":
			opts.Draft = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("pr: unknown flag %s", a)
			}
			if opts.Branch != "" {
				return fmt.Errorf("pr: unexpected argument %s", a)
			}
			opts.Branch = a
		}
	}
	url, err := gh.EnsurePR(eng, repo, opts)
	if err != nil {
		return err
	}
	if url != "" {
		fmt.Println(url)
	}
	return nil
}

func cmdLS(args []string) error {
	eng, _, err := engine()
	if err != nil {
		return err
	}
	root := ""
	if len(args) > 0 {
		root = args[0]
	}
	infos, err := eng.List(root)
	if err != nil {
		return err
	}
	fmt.Print(stack.FormatList(root, infos))
	return nil
}

func runTUI() error {
	repo := &git.Repo{}
	if err := repo.RequireRepo(); err != nil {
		return err
	}
	return tui.Run(repo)
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `git-stack — plain-git stacked branch helpers (dot-depth parents)

  (no args)                                Interactive TUI
  parent  [branch]                         Print inferred parent
  create  <name> [--from <start>]          Create branch from inferred/explicit parent
  restack [branch] [--push] [--onto-trunk] Replay branch onto parent (backwards)
          [--no-fetch]
  reparent <branch> <new-parent>           Rebase onto a different parent
           [--from <old-parent>] [--push] [--no-fetch]
  sync    [root] [--push] [--onto-trunk]   Fix root then descendants (forwards)
           [--dry-run] [--no-fetch]
  pr      [branch] [--draft]               Create/retarget PR with correct base (needs gh)
  ls      [root]                           List stack tree under root (or all stacks)
  tui                                      Open interactive TUI
  version                                  Print version

Depth marker is "." because git cannot have both branch "foo" and "foo/bar".

  wms-batching            → parent main
  wms-batching.ui         → parent wms-batching
  wms-batching.ui.tests   → parent wms-batching.ui

Examples:
  git-stack create wms-batching
  git-stack create wms-batching.ui
  git-stack restack
  git-stack restack --onto-trunk
  git-stack sync wms-batching --push
  git-stack sync wms-batching --onto-trunk --dry-run
  git-stack pr
`)
}
