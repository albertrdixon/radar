package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Guard against personal working-notes leaking into the repository.
//
// An effort folder holds one person's scratch — plan.md, notes.md,
// must-finish-later.md, discussions/ — on one machine, under a path nobody else
// has. A comment that points at one is a dangling reference the moment it is
// pushed: unresolvable to every other reader, and a description of the author's
// filesystem rather than of the code.
//
// This is mechanical, which is the point. The same rule was applied by hand
// twice and missed both times, because the reference reads like an ordinary
// filename rather than a path.
func TestNoEffortNotesReferencedFromSource(t *testing.T) {
	// Names that only exist inside an effort folder. Deliberately not "notes" or
	// "plan" alone — those are ordinary words in prose.
	banned := []string{"must-finish-later", "wt-memory", "~/wt/"}

	// The author's own home path, resolved at run time rather than written down:
	// spelling a username into a committed test would put in the repository the
	// exact thing the test exists to keep out. Fixture paths like
	// `/Users/alice/.kube/config` are legitimate and stay legitimate, because
	// they belong to nobody running this.
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(home, "/") && len(home) > 6 {
		banned = append(banned, home)
	}

	// The run-time home only matches on the machine that has it, so a macOS
	// worktree path committed from a laptop would pass CI, where HOME is
	// something else entirely — and CI is the check that actually blocks a
	// merge. Any home-rooted worktree path is a leak whoever it belongs to.
	worktreePath := regexp.MustCompile(`/(?:Users|home)/[^/\s"']+/wt[-/]`)

	root := "../.."
	var offenders []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", ".git", "dist", "vendor", "testdata":
				return filepath.SkipDir
			}
			return nil
		}
		// Markdown counts. A README or a demo-cluster guide is as published as
		// the code beside it, and a path pasted into one leaks exactly the same.
		switch filepath.Ext(path) {
		case ".go", ".ts", ".tsx", ".sh", ".yaml", ".yml", ".md":
		default:
			return nil
		}
		// This file names them in order to ban them.
		if strings.HasSuffix(path, "no_effort_refs_test.go") {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, s := range banned {
			if strings.Contains(string(b), s) {
				offenders = append(offenders, path+" contains "+s)
			}
		}
		if m := worktreePath.FindString(string(b)); m != "" {
			offenders = append(offenders, path+" contains a home-rooted worktree path "+m)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(offenders) > 0 {
		t.Errorf("source must not reference personal working notes or absolute home paths:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
