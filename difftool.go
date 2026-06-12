package main

// diffToolMode is the entry point for using jj-highlight as jj's diff
// formatter, an alternative to the wrapper subcommands. It is invoked as
// `jj-highlight util diff-formatter LEFT RIGHT [WIDTH]` -- tucked under util
// because jj calls it, not a person. Wire it as the default diff for every jj
// command with:
//
//	[ui]
//	diff-formatter = ["jj-highlight", "util", "diff-formatter", "$left", "$right", "$width"]
//
// jj hands a diff formatter two materialized trees -- left/ (old) and right/
// (new), changed files only, at repo-relative paths -- and no revisions. jj's
// color-words renderer needs revisions in a repo, which the interface threw
// away. To recover byte-exact color-words we reconstruct a repo: init a
// throwaway jj repo, commit left/ as the parent, put right/ in the working
// copy, and run real `jj diff --color-words`. We then re-color that output
// with the wrapper pipeline (parseDiff + renderFile), reading new-side
// contents straight off right/ on disk.
//
// The cost of this "ephemeral repo" approach is per-invocation: init +
// snapshotting both trees into the store before any diff byte is produced
// (~200ms), and the throwaway repo only inherits global+user jj config, not
// the source repo's repo-local config. The wrapper has neither cost, so it
// stays the preferred path for interactive diffing.

import (
	"bufio"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

// ephemeralConfig is forwarded to every jj call against the throwaway repo.
var ephemeralConfig = []string{
	"--config", "user.name=jj-highlight",
	"--config", "user.email=jj-highlight@localhost",
}

// diffToolMode implements the jj diff-formatter contract (argv LEFT RIGHT
// [WIDTH]) by reconstructing a throwaway repo so real jj can render
// color-words, then re-coloring that output with syntax highlighting.
func diffToolMode(args []string) {
	if len(args) < 2 {
		usage()
		os.Exit(2)
	}
	leftDir, err1 := filepath.Abs(args[0])
	rightDir, err2 := filepath.Abs(args[1])
	if err1 != nil || err2 != nil {
		log.Fatalf("difftool: resolve paths: %v %v", err1, err2)
	}

	repo, err := os.MkdirTemp("", "jjhl-difftool-")
	if err != nil {
		log.Fatalf("difftool: mktemp: %v", err)
	}
	defer os.RemoveAll(repo)

	buildEphemeralRepo(repo, leftDir, rightDir)

	// Force builtin color-words (and guard against recursing into a configured
	// ui.diff-formatter). cwd = repo root, so color-words paths are
	// repo-relative, matching right/'s layout. The diff snapshots the right
	// working copy, so it needs the raised file-size limit too (see
	// ephemeralConfig).
	cmd := exec.Command("jj", append(append([]string{}, ephemeralConfig...),
		"diff", "-r", "@", "--color-words", "--color=always",
		"--config", `ui.diff-formatter=":color-words"`)...)
	cmd.Dir = repo
	cmd.Stderr = os.Stderr
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("difftool: jj diff pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatalf("difftool: jj diff start: %v", err)
	}

	theme := loadTheme()
	out := bufio.NewWriter(os.Stdout)

	// Stream a file at a time so the first paints without waiting for the rest.
	files := make(chan *FileDiff, 2)
	go func() {
		parseDiff(pipe, func(f *FileDiff) { files <- f })
		close(files)
	}()

	for f := range files {
		// New-side contents come from right/ on disk (no jj file show needed);
		// removed files have no new side. Path is repo-relative == right-relative.
		var newSrc []byte
		if !f.Removed && f.Path != "" {
			newSrc = readFile(filepath.Join(rightDir, f.Path))
		}
		h := newHighlighter(f.Path, newSrc)
		renderFile(out, f, h, theme)
		if err := out.Flush(); err != nil {
			break
		}
	}

	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		log.Printf("difftool: jj diff: %v", err)
	}
}

// buildEphemeralRepo creates a jj repo in dir whose parent commit holds the
// left tree and whose working copy holds the right tree, so `jj diff -r @`
// renders left->right. The user identity is forced via config so the commit
// succeeds regardless of the ambient jj config.
func buildEphemeralRepo(dir, leftDir, rightDir string) {
	jj := func(args ...string) {
		full := append(append([]string{}, ephemeralConfig...), args...)
		cmd := exec.Command("jj", full...)
		cmd.Dir = dir
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			log.Fatalf("difftool: jj %v: %v", args, err)
		}
	}

	jj("git", "init", "--quiet", ".")
	copyTree(leftDir, dir)               // working copy := left
	jj("commit", "--quiet", "-m", "old") // snapshot left into parent; new empty @
	clearWorkingCopy(dir)                // working copy := right
	copyTree(rightDir, dir)
	// The diff itself (run by the caller) snapshots the right working copy.
}

// copyTree copies every file under src into dst at the same relative path,
// creating directories as needed. A missing src (e.g. an all-added or
// all-deleted commit has no left or right side) is a no-op.
func copyTree(src, dst string) {
	filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return nil
		}
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer in.Close()
		w, err := os.Create(out)
		if err != nil {
			return nil
		}
		defer w.Close()
		io.Copy(w, in)
		return nil
	})
}

// clearWorkingCopy removes every entry in dir except the .jj store, so the
// working copy can be repopulated with a different tree.
func clearWorkingCopy(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() == ".jj" {
			continue
		}
		os.RemoveAll(filepath.Join(dir, e.Name()))
	}
}

func readFile(path string) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return b
}
