// jj-highlight is a syntax-highlighting wrapper around jj. It runs one of
// a few supported jj subcommands and colorizes the output with bonsai's
// tree-sitter snapshot:
//
//	jj-highlight diff [...]       highlight a diff
//	jj-highlight interdiff [...]  highlight an interdiff
//	jj-highlight file show [...]  syntax-highlight a file's contents
//	jj-highlight show [...]       highlight a revision's description + diff
//
// Designed for a jj alias:
//
//	[aliases]
//	hi = ["util", "exec", "--", "jj-highlight"]
//
// Using this alias, the commands above read `jj hi diff`, `jj hi show`, etc.
// It can alternatively run as jj's diff formatter (the util diff-formatter
// subcommand, invoked by jj via ui.diff-formatter); see difftool.go.
//
// For the diff-bearing commands jj's red/green foreground signal becomes a
// faded background tint, freeing the foreground for syntax color. Coloring
// a diff line needs the file as parse context, since the same line parses
// differently depending on what surrounds it, so the changed file's
// new-revision contents are fetched from jj, parsed once, and looked up by
// byte offset. The old side is never fetched: removed lines render as a
// flat marker. Files are parsed and rendered as they stream out of jj, so
// the first file paints without waiting for the rest of a large diff.
//
// file show is simpler: it runs the grammar over the raw file contents,
// with no diff structure.
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

func main() {
	log.SetFlags(0)
	// Dispatch to supported commands.
	sub, rest := splitSubcommand(os.Args[1:])
	switch sub {
	case "diff", "show", "interdiff":
		diffMode(sub, rest)
	case "file show":
		fileShowMode(rest)
	case "util diff-formatter":
		// jj diff-formatter entry point (LEFT RIGHT [WIDTH]). See difftool.go.
		diffToolMode(rest)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `jj-highlight: syntax-highlighting wrapper around jj.

Usage:
  jj-highlight diff [ARGS]            highlight a diff
  jj-highlight interdiff [ARGS]       highlight an interdiff
  jj-highlight file show [ARGS]       syntax-highlight a file's contents
  jj-highlight show [ARGS]            highlight a revision's description + diff

Designed for alias config: aliases.hi=['util', 'exec', '--', 'jj-highlight']
`)
}

// splitSubcommand separates the subcommand from all other args.
// Global flags may precede the subcommand, so they are separated and returned
// as part of rest.
func splitSubcommand(args []string) (sub string, rest []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			break // first positional is the subcommand
		}
		rest = append(rest, a)
		if jjGlobalValueFlags[a] && i+1 < len(args) {
			i++
			rest = append(rest, args[i]) // keep the flag's value
		}
		i++
	}
	if i >= len(args) {
		return "", nil // only global flags, no subcommand
	}
	switch args[i] {
	case "diff", "show", "interdiff":
		sub = args[i]
		i++
	case "file":
		if i+1 < len(args) && args[i+1] == "show" {
			sub = "file show"
			i += 2
		} else {
			return "", nil
		}
	case "util":
		if i+1 < len(args) && args[i+1] == "diff-formatter" {
			sub = "util diff-formatter"
			i += 2
		} else {
			return "", nil
		}
	default:
		return "", nil
	}
	rest = append(rest, args[i:]...)
	return sub, rest
}

// diffMode runs `jj <subcmd> --color=always <format> <args>` (subcmd is
// diff, show, or interdiff) and pipes the output through the highlighter.
// Two cases stream jj's output verbatim instead:
//
//   - --color=never, an explicit refusal of color we do not override.
//   - an output we cannot parse into spans (--stat, --summary,
//     --name-only, --types, --template, or an external --tool).
//
// Otherwise it forces the format itself, --color-words by default (so the
// user's ui.diff-format config cannot silently break the parser) or --git
// if asked.
func diffMode(subcmd string, jjArgs []string) {
	p := parseArgs(subcmd, jjArgs)
	if p.firstVal("--color") == "never" {
		passthrough([]string{subcmd}, jjArgs)
		return
	}
	if p.unhighlightable() {
		passthrough([]string{subcmd}, jjArgs)
		return
	}

	git := p.has("--git")
	format := "--color-words"
	// --color=debug wraps each span in its semantic label (<<diff ... ::text>>),
	// which the color-words parser uses to classify old/new/context regardless
	// of how the user themed jj's diff colors. The git parser keys off
	// +/-/space prefixes, so it just needs real color.
	color := "--color=debug"
	if git {
		format = "--git"
		color = "--color=always"
	}

	// Only the new revision is needed: removed content renders as a flat
	// marker and is never syntax-highlighted, so the old side is not fetched.
	// The 'new' side differs by command (see parsedArgs.newRev). git also sets
	// the fileset relativity: git paths are workspace-root-relative,
	// color-words paths are cwd-relative (see jjFileset).
	newRev := p.newRev(subcmd)

	cmdArgs := append([]string{subcmd, color, format}, forwardArgs(subcmd, jjArgs)...)
	cmd := exec.Command("jj", cmdArgs...)
	// --color=debug also frames jj's warnings/errors on stderr, so unwrap them
	// the same way (a no-op for the git path's --color=always).
	errOut := &debugFilterWriter{w: os.Stderr}
	cmd.Stderr = errOut
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalf("jj %s pipe: %v", subcmd, err)
	}
	if err := cmd.Start(); err != nil {
		log.Fatalf("jj %s start: %v", subcmd, err)
	}

	renderDiff(pipe, newRev, p.fetchFlags(), git, p.has("--no-pager"))

	werr := cmd.Wait()
	errOut.flush()
	if werr != nil {
		if exitErr, ok := werr.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		log.Fatalf("jj %s: %v", subcmd, werr)
	}
}

// fileShowMode syntax-highlights file contents. jj file show concatenates
// multiple matched files with no delimiter, and the matches can be
// different languages, so the raw blob can't be highlighted with one grammar.
// Instead it enumerates the matching files with jj file list, then
// shows and highlights each independently with its own grammar and
// concatenates the results. A custom -T/--template renders metadata rather
// than raw content, and a missing or empty path set is an error or matches
// nothing, so those fall back to a verbatim passthrough.
func fileShowMode(jjArgs []string) {
	p := parseArgs("file show", jjArgs)
	if p.has("-T", "--template") || len(p.pos) == 0 {
		passthrough([]string{"file", "show"}, jjArgs)
		return
	}
	files, err := jjFileList(jjArgs)
	if err != nil || len(files) == 0 {
		// Could not enumerate, or nothing matched. Run verbatim so jj's own
		// output and exit code reach the user.
		passthrough([]string{"file", "show"}, jjArgs)
		return
	}
	flagArgs := nonPositional("file show", jjArgs)

	themeCh := make(chan Theme, 1)
	go func() { themeCh <- loadTheme() }()

	output, finalize := startPager(p.has("--no-pager"))
	out := bufio.NewWriter(output)
	theme := <-themeCh

	for _, f := range files {
		content := fileShowOne(flagArgs, f)
		h := newHighlighter(f, content)
		renderSource(out, content, h, theme)
		// Flush per file so a slow or quit pager stalls the loop, and thus
		// the per-file jj file show calls, instead of eagerly showing every
		// matched file.
		if err := out.Flush(); err != nil {
			break
		}
	}
	_ = out.Flush()
	_ = finalize()
}

// passthrough runs `jj <jjCmd...> <args>` verbatim, writing jj's stdout
// directly to ours. Used when we cannot or should not transform the output.
func passthrough(jjCmd, jjArgs []string) {
	cmd := exec.Command("jj", append(append([]string(nil), jjCmd...), jjArgs...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		log.Fatalf("jj %s: %v", strings.Join(jjCmd, " "), err)
	}
}
