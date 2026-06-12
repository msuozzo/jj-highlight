package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"

	"github.com/msuozzo/bonsai"
)

// --- token classification --------------------------------------------------

type tokenClass int

const (
	cNone tokenClass = iota
	cComment
	cString
	cNumber
	cKeyword
	cType
	cField
	cEmphasis // markdown *italic* and _italic_
	cStrong   // markdown **bold** and __bold__
)

// classifyContainer covers whole-range node types where we want the
// color applied across the entire span, rather than per leaf. For
// strings and comments that means quotes and `//` / `/*` markers stay
// in the same color. For markdown that means headings and code blocks
// read as solid blocks of color. Returning non-cNone tells the
// collector to emit one token and skip recursion into children.
func classifyContainer(typ string, named bool) tokenClass {
	if !named {
		return cNone
	}
	switch typ {
	// Strings.
	case "string", "string_literal", "raw_string_literal",
		"interpreted_string_literal", "byte_string_literal",
		"rune_literal", "concatenated_string":
		return cString
	// Comments.
	case "comment", "line_comment", "block_comment":
		return cComment
	// Markdown block-grammar containers.
	case "atx_heading", "setext_heading":
		return cKeyword
	case "fenced_code_block", "indented_code_block":
		return cString
	case "block_quote":
		return cComment
	case "link_reference_definition":
		return cType
	// Markdown GFM tables. Header row gets keyword color to stand out
	// over body rows. The `|---|---|` delimiter row reads as structural
	// noise, so it gets comment color. Body rows stay default.
	case "pipe_table_header":
		return cKeyword
	case "pipe_table_delimiter_row":
		return cComment
	// Markdown inline-grammar containers. The inline parser fires
	// inside `inline` leaves of the block tree (see collectTokens),
	// so these only apply when we have a working inline parser.
	case "code_span":
		return cString
	case "inline_link", "image", "link",
		"shortcut_link", "full_reference_link", "collapsed_reference_link",
		"uri_autolink", "email_autolink":
		return cField
	case "emphasis":
		return cEmphasis
	case "strong_emphasis":
		return cStrong
	// YAML quoted and block scalars. plain_scalar (unquoted) is
	// deliberately left alone since it covers both keys and values.
	case "double_quote_scalar", "single_quote_scalar", "block_scalar":
		return cString
	// Dockerfile containers. JSON strings within instructions
	// (e.g. CMD ["./app"]) get the string color. `expansion` is the
	// ${VAR} / $VAR form and reads as a field reference.
	case "json_string":
		return cString
	case "expansion":
		return cField
	// Go template field references (.Name, .User.Name). Whole-range
	// classification keeps the leading `.` colored alongside the
	// identifier. Action keywords (if, range, define, end) come
	// through the lowercase-keyword path automatically.
	case "field":
		return cField
	// Bash. simple_expansion is `$VAR`. command_name is the verb at the
	// start of a command (echo, cat, grep). The `string` container
	// already covers double-quoted strings via the strings group above.
	case "simple_expansion":
		return cField
	case "command_name":
		return cType
	}
	if strings.HasSuffix(typ, "_string") || strings.HasSuffix(typ, "_string_literal") {
		return cString
	}
	return cNone
}

// classify maps a leaf tree-sitter node to a token class. One
// classifier handles every grammar: we lump synonyms across tree-
// sitter-go / -python / -markdown / -yaml / -bash / -dockerfile /
// -gotemplate node-type names, and treat any all-letter anonymous
// token as a keyword. The all-letter check covers both lowercase
// (Go's `func`, Python's `def`) and uppercase (Dockerfile's `FROM`,
// `RUN`) without per-language enumeration.
func classify(typ string, named bool) tokenClass {
	if !named {
		if len(typ) >= 2 && isAllAlpha(typ) {
			return cKeyword
		}
		return cNone
	}
	switch typ {
	case "true", "false", "none", "nil":
		return cNumber
	case "number":
		return cNumber
	case "type_identifier", "package_identifier", "primitive_type":
		return cType
	case "field_identifier":
		return cField
	// Markdown leaves. The block grammar emits these as standalone
	// tokens that wouldn't be reached by the container path.
	case "thematic_break",
		"list_marker_minus", "list_marker_plus", "list_marker_star",
		"list_marker_dot", "list_marker_parenthesis":
		return cKeyword
	case "info_string", "language":
		return cType
	// YAML leaves. Quoted-string scalars are handled as containers
	// above. Unquoted text (the inner `string_scalar` leaves that the
	// block grammar emits for both keys and values) is left default
	// so structure stays visible against the noise. Only the typed
	// scalars that read like literals get a color.
	case "integer_scalar", "float_scalar", "boolean_scalar", "null_scalar":
		return cNumber
	case "anchor_name", "alias_name":
		return cField
	// Dockerfile leaves. Image names / aliases read like types,
	// paths and unquoted strings like literals, and ports like
	// numbers. Variable references get the field color so they pop.
	case "image_name", "image_alias":
		return cType
	case "path", "unquoted_string":
		return cString
	case "expose_port":
		return cNumber
	case "variable":
		return cField
	// Bash leaves. variable_name is the LHS of `NAME=value`, the
	// content inside ${...}, etc. test_operator covers `-f`, `-d`,
	// `==`, etc. inside [[ ... ]]. Bare `word` and `string_content`
	// stay default so command arguments aren't recolored.
	case "variable_name":
		return cField
	case "test_operator":
		return cKeyword
	}
	if strings.Contains(typ, "integer") || strings.Contains(typ, "int_literal") ||
		strings.Contains(typ, "float_literal") || typ == "float" || typ == "number" {
		return cNumber
	}
	return cNone
}

func isAllAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

// --- highlighter -----------------------------------------------------------

type token struct {
	start, end uint32
	class      tokenClass
}

type Highlighter struct {
	parser   *bonsai.Parser
	newToks  []token
	newLines []int // 1-based, newLines[k] = byte offset of line k's first byte
}

// newHighlighter parses the file's contents for syntax context.
// Only new source gets syntax highlights (see emitRemoved).
func newHighlighter(path string, newSrc []byte) *Highlighter {
	h := &Highlighter{}
	ctor := parserConstructor(path)
	if ctor == nil {
		return h
	}
	h.parser = ctor()
	if newSrc != nil {
		if root, err := h.parser.Parse(newSrc); err == nil {
			h.newToks = collectTokens(root)
		}
		h.newLines = lineOffsets(newSrc)
	}
	return h
}

// collectTokens walks the parse tree and produces a sorted, non-
// overlapping list of (byte range, class) tokens. Injection (e.g.
// markdown's block-and-inline split) has already been resolved by
// bonsai, so this function sees one unified tree.
func collectTokens(root *bonsai.Node) []token {
	var toks []token
	var visit func(n *bonsai.Node)
	visit = func(n *bonsai.Node) {
		// YAML plain_scalar: the block grammar uses it for both keys
		// and values (and for sequence elements, top-level scalars,
		// etc.). The parent flow_node carries Field == "key" only for
		// the key half of a block_mapping_pair. Anything else is a
		// value and reads like a string.
		if n.Named && n.Type == "plain_scalar" && (n.Parent == nil || n.Parent.Field != "key") {
			toks = append(toks, token{n.StartByte, n.EndByte, cString})
			return
		}
		// Whole-range container? Emit once for the full node and skip
		// descent. This is how strings keep their quotes colored and
		// block comments stay solid across multiple lines.
		if c := classifyContainer(n.Type, n.Named); c != cNone {
			toks = append(toks, token{n.StartByte, n.EndByte, c})
			return
		}
		if len(n.Children) == 0 {
			if c := classify(n.Type, n.Named); c != cNone {
				toks = append(toks, token{n.StartByte, n.EndByte, c})
			}
			return
		}
		for _, ch := range n.Children {
			visit(ch)
		}
	}
	visit(root)
	sort.Slice(toks, func(i, j int) bool { return toks[i].start < toks[j].start })
	return toks
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

func classAt(off uint32, toks []token) tokenClass {
	lo, hi := 0, len(toks)
	for lo < hi {
		mid := (lo + hi) / 2
		if toks[mid].end <= off {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(toks) && toks[lo].start <= off && off < toks[lo].end {
		return toks[lo].class
	}
	return cNone
}

// --- rendering -------------------------------------------------------------

func renderFile(w io.Writer, f *FileDiff, h *Highlighter, t Theme) {
	// A headerless preamble (jj show's description) has no Header line to
	// print, so just emit its passed-through Raw lines.
	if f.Header != "" {
		fmt.Fprintln(w, f.Header)
	}
	for _, line := range f.Lines {
		renderLine(w, line, h, t)
	}
}

func renderLine(w io.Writer, line *DiffLine, h *Highlighter, t Theme) {
	if line.Raw != "" {
		fmt.Fprintln(w, line.Raw)
		return
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

	// The new-side line numbers index the fetched new-revision file, since
	// diffMode fetches the same revision jj diffed. Removed lines are not
	// highlighted, so only the new side is needed.
	newBase := lineBase(h.newLines, line.NewNo)

	var newCol int
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
// Composability note: wrap should set BG only and per-class styles
// should set FG only. A per-class Reset emits \x1b[39m which doesn't
// disturb the BG. If wrap also set FG the wrap-FG would be lost at
// the first per-class transition. The default theme honors this.
// Custom themes mixing FG+BG on the Added key may show artifacts.
func emitHighlighted(w io.Writer, text string, toks []token, base int, t Theme, wrap Style) {
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
	cur := tokenClass(-1)
	var curStyle Style
	for i := range len(text) {
		cls := classAt(uint32(base+i), toks)
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
