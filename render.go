package main

import (
	"bufio"
	"fmt"
	"io"
	"log"

	"github.com/msuozzo/jj-highlight/syntax"
)

// --- highlighter -----------------------------------------------------------

type Highlighter struct {
	newToks    []syntax.Token
	newLines   []int // 1-based, newLines[k] = byte offset of line k's first byte
	conflicted bool  // the new side contains materialized conflict regions
}

// newHighlighter parses the file's contents for syntax context.
// Only new source gets syntax highlights (see emitRemoved).
func newHighlighter(path string, newSrc []byte) *Highlighter {
	h := &Highlighter{}
	if newSrc == nil || !syntax.Supported(path) {
		return h
	}
	h.newToks, h.conflicted = syntax.Highlight(path, newSrc)
	h.newLines = lineOffsets(newSrc)
	return h
}

func lineOffsets(src []byte) []int {
	out := []int{0, 0} // index 0 unused, line 1 starts at byte 0
	for i := range src {
		if src[i] == '\n' {
			out = append(out, i+1)
		}
	}
	return out
}

// --- rendering -------------------------------------------------------------

func renderFile(w io.Writer, f *FileDiff, h *Highlighter, t Theme) {
	// A headerless preamble (jj show's description) has no Header line to
	// print, so just emit its passed-through Raw lines.
	if f.Header != "" {
		fmt.Fprintln(w, f.Header)
	}
	// Color-words can split one new line across consecutive display lines:
	// when a removed span crosses an old-line boundary, the next display
	// line repeats the same new-side number and continues that new line
	// where the previous one left off. Carry the consumed new-side column
	// across those lines so token lookups stay aligned with the file.
	lastNo, lastCol := 0, 0
	for _, line := range f.Lines {
		start := 0
		if line.NewNo != 0 && line.NewNo == lastNo {
			start = lastCol
		}
		end := renderLine(w, line, h, t, start)
		if line.NewNo != 0 {
			lastNo, lastCol = line.NewNo, end
		}
	}
}

// renderLine writes one display line. newCol counts the new-side bytes
// consumed so far for line.NewNo (KOld spans exist only on the old side and
// don't advance it). It starts at startCol and the final value is returned
// for renderFile's cross-line carry.
func renderLine(w io.Writer, line *DiffLine, h *Highlighter, t Theme, startCol int) int {
	if line.Raw != "" {
		fmt.Fprintln(w, line.Raw)
		return startCol
	}

	gutterStyle := t.Gutter
	switch line.GutterAccent {
	case KNew:
		gutterStyle = t.Added
	case KOld:
		gutterStyle = t.Removed
	}
	io.WriteString(w, gutterStyle.ANSI())
	io.WriteString(w, line.Gutter)
	io.WriteString(w, gutterStyle.Reset())

	// While the new side is still conflicted, an old-side-only marker line
	// is a relabeled marker, not removed content, so it takes the Conflict
	// style with the ConflictRemoved BG as the removed cue, mirroring the
	// Added BG on new-side markers. When the new side is clean the conflict
	// itself was removed, so its markers keep the plain removed style.
	if h.conflicted && oldMarkerLine(line) {
		io.WriteString(w, t.ConflictRemoved.ANSI())
		io.WriteString(w, t.Conflict.ANSI())
		for _, span := range line.Spans {
			io.WriteString(w, span.Text)
		}
		io.WriteString(w, t.Conflict.Reset())
		io.WriteString(w, t.ConflictRemoved.Reset())
		io.WriteString(w, "\x1b[0m\n")
		return startCol
	}

	// The new-side line numbers index the fetched new-revision file, since
	// diffMode fetches the same revision jj diffed. Removed lines are not
	// highlighted, so only the new side is needed.
	newBase := lineBase(h.newLines, line.NewNo)

	newCol := startCol
	for _, span := range line.Spans {
		switch span.Kind {
		case KOld:
			emitRemoved(w, span.Text, t)
		case KNew:
			base := -1
			if newBase >= 0 {
				base = newBase + newCol
			}
			emitHighlighted(w, span.Text, h.newToks, base, t, t.Added)
			newCol += len(span.Text)
		case KContext:
			base := -1
			if newBase >= 0 {
				base = newBase + newCol
			}
			emitHighlighted(w, span.Text, h.newToks, base, t, Style{})
			newCol += len(span.Text)
		}
	}
	io.WriteString(w, "\x1b[0m\n")
	return newCol
}

// emitRemoved writes old-side bytes with the theme's Removed style and no
// syntax-token lookup. Removed text reads as a uniform marker and demphasizes
// interpretation.
func emitRemoved(w io.Writer, text string, t Theme) {
	io.WriteString(w, t.Removed.ANSI())
	io.WriteString(w, text)
	io.WriteString(w, t.Removed.Reset())
}

// emitHighlighted writes text with per-byte syntax color from the
// tokens at the corresponding file offset, optionally wrapped in a
// wrap Style (used by KNew for the Added background tint). base < 0
// or toks == nil signals "no parse context", and we emit plain text
// with just the wrap applied.
//
// Composability note: wrap should set BG only, and per-class styles should
// leave BG alone (FG and attributes are both fine). A per-class Reset emits
// only \x1b[39m and the attribute resets, none of which disturb the BG. If
// wrap also set FG the wrap-FG would be lost at the first per-class
// transition. The default theme honors this. Custom themes mixing FG+BG on
// the Added key, or setting BG on a per-class key, may show artifacts.
func emitHighlighted(w io.Writer, text string, toks []syntax.Token, base int, t Theme, wrap Style) {
	if !wrap.Empty() {
		io.WriteString(w, wrap.ANSI())
	}
	if toks == nil || base < 0 {
		io.WriteString(w, text)
		if !wrap.Empty() {
			io.WriteString(w, wrap.Reset())
		}
		return
	}
	cur := syntax.Class(-1)
	var curStyle Style
	for i := range len(text) {
		cls := syntax.ClassAt(uint32(base+i), toks)
		if cls != cur {
			if !curStyle.Empty() {
				io.WriteString(w, curStyle.Reset())
			}
			cur = cls
			curStyle = t.styleFor(cls)
			if !curStyle.Empty() {
				io.WriteString(w, curStyle.ANSI())
			}
		}
		w.Write([]byte{text[i]})
	}
	if !curStyle.Empty() {
		io.WriteString(w, curStyle.Reset())
	}
	if !wrap.Empty() {
		io.WriteString(w, wrap.Reset())
	}
}

// lineBase returns the file byte offset of the given line, or -1 if lineNo
// is out of range: parser unavailable, empty file, or 0 for a line absent
// on this side.
func lineBase(lines []int, lineNo int) int {
	if lines == nil || lineNo <= 0 || lineNo >= len(lines) {
		return -1
	}
	return lines[lineNo]
}

// renderDiff reads jj's diff stream, fetches each file's new-revision
// contents for parse context, and writes the highlighted result to the
// pager (or stdout). Files are parsed and rendered as they arrive, so the
// first file paints without waiting for the rest.
func renderDiff(r io.Reader, newRev string, fetchFlags []string, git, noPager bool) {
	themeCh := make(chan Theme, 1)
	go func() { themeCh <- loadTheme() }()

	buf := bufio.NewReader(r)
	// diffMode forces the format, so the matching parser is chosen directly.
	var parse diffParser = parseDiff
	if git {
		parse = parseGitDiff
	}

	fetches := newFetches(newRev, fetchFlags, git)

	// The parser runs in the background and emits each file as its block
	// completes. start kicks the file's fetch, overlapping the rest of the
	// stream, and the file goes to the render loop. stop unblocks the sender
	// when the pager quits early. parseErr joins the goroutine so all reads
	// from jj's pipe finish before the caller calls cmd.Wait.
	//
	// A buffer of two keeps the parser about two files ahead. Under a pager
	// the render loop blocks on the output pipe while the user reads, which
	// stalls the parser and our reads from jj, so jj stops producing content
	// for files the user has not scrolled to. Quitting after the first
	// screen costs about two files of work, not the whole diff.
	files := make(chan *FileDiff, 2)
	stop := make(chan struct{})
	parseErr := make(chan error, 1)
	go func() {
		parseErr <- parse(buf, func(f *FileDiff) {
			// After the pager quits, do not start fetches or block on the
			// channel. Keep reading jj to EOF to drain it. Otherwise an
			// early quit would spawn a jj file show per remaining file.
			select {
			case <-stop:
				return
			default:
			}
			fetches.start(f)
			select {
			case files <- f:
			case <-stop:
			}
		})
		close(files)
	}()

	output, finalize := startPager(noPager)
	out := bufio.NewWriter(output)

	theme := <-themeCh

	quit := false
	for f := range files {
		newSrc := fetches.wait(f.Path)
		h := newHighlighter(f.Path, newSrc)
		renderFile(out, f, h, theme)
		// Flush per file to notice a broken pipe (usually the user quitting
		// the pager) before waiting on the next file's fetch.
		if err := out.Flush(); err != nil {
			quit = true
			break
		}
	}
	if quit {
		// Let the parser stop sending and drain the rest of jj's output so
		// the subprocess can exit and the caller's cmd.Wait does not race an
		// in-flight pipe read.
		close(stop)
		for range files {
		}
	}
	if err := <-parseErr; err != nil && !quit {
		log.Printf("parse: %v", err)
	}

	// Best-effort flush and finalize. After an early quit both return EPIPE
	// or a non-zero status, neither of which is our error.
	_ = out.Flush()
	_ = finalize()
}

// renderSource writes file contents with per-byte syntax color and no diff
// structure. It flushes per line so quitting the pager part-way through a
// large file stops it promptly. h.newToks is nil when the path is not
// highlightable or the parse failed, in which case the text passes through
// uncolored.
func renderSource(w *bufio.Writer, content []byte, h *Highlighter, t Theme) {
	for off := 0; off < len(content); {
		end := off
		for end < len(content) && content[end] != '\n' {
			end++
		}
		if end < len(content) {
			end++ // include the trailing newline in this line
		}
		emitHighlighted(w, string(content[off:end]), h.newToks, off, t, Style{})
		off = end
		if err := w.Flush(); err != nil {
			break
		}
	}
}
