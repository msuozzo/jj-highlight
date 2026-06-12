package main

import (
	"bufio"
	"bytes"
	"io"
	"strconv"
	"strings"
)

// diffParser reads a diff from r and calls onFile once per file, as soon
// as that file's block is complete. It returns any read/scan error.
type diffParser func(r io.Reader, onFile func(*FileDiff)) error

type FileDiff struct {
	Removed bool // a deleted file: no new side to fetch or highlight
	Path    string
	Header  string      // raw header with original ANSI, preserved for display
	Lines   []*DiffLine // diff body
}

type DiffLine struct {
	// Raw is non-empty for any line not recognized as a body line (hunk
	// ellipsis, blank separator, binary-file note, format metadata). When
	// set, the other fields are ignored.
	Raw string

	NewNo  int     // 1-based new-side line number (0 if absent on the new side)
	Gutter string  // gutter prefix to re-emit ("  12    12: " for jj, "+"/"-"/" " for git)
	Spans  []*Span // content split by side

	// GutterAccent picks the gutter Style: KContext (zero) is the neutral
	// theme.Gutter, KNew is theme.Added, KOld is theme.Removed. The git
	// parser sets it to colorize the +/-/space prefixes. The color-words
	// parser leaves it zero.
	GutterAccent Kind
}

type Kind int

const (
	KContext Kind = iota // unchanged: present in both old and new
	KOld                 // removed: present only in old (jj renders red)
	KNew                 // added: present only in new (jj renders green)
)

type Span struct {
	Text string
	Kind Kind
}

// --color=debug parsing --------------------------------------------------
//
// We run color-words with --color=debug, which wraps every styled span as
// <<label::text>> where the label is the hunks role ("diff color_words removed
// token", "added token", bare "diff color_words" for context,
// "... line_number", "... header"). These labels are theme-independent.
//
// We walk those units and builds a DiffLine directly. The non-diff lines (e.g.
// jj show's <<show commit::...>> preamble) are handled separately, unwrapping
// the framing and retaining jj's assigned color.

// parseColorWordsLine builds a DiffLine from one --color=debug color-words
// body line. Each <<diff ... ::text>> unit's label indicates the side
// ("removed" -> KOld, "added" -> KNew, else context) while the new-side line
// number comes from the "added line_number" unit. Returns nil for a line that
// is not a gutter body line (a header, or a hunk/elision marker), so the
// caller passes it through verbatim.
func parseColorWordsLine(raw string) *DiffLine {
	type unit struct {
		text string
		kind Kind
	}
	var units []unit
	var plain strings.Builder
	newNo := 0
	for start := 0; ; {
		_, textStart, label, ok := findDiffLabelOpener(raw, start)
		if !ok {
			break
		}
		end := len(raw)
		if ns, _, _, more := findDiffLabelOpener(raw, textStart); more {
			end = ns
		}
		// Content may itself contain ">>"/"::"/"<<"; strip only the trailing
		// SGR and the single frame-closing ">>" at the very end.
		text := strings.TrimSuffix(trimTrailingSGR(raw[textStart:end]), ">>")
		if strings.Contains(label, "added") && strings.Contains(label, "line_number") {
			newNo, _ = strconv.Atoi(strings.TrimSpace(text))
		}
		units = append(units, unit{text, kindForLabel(label)})
		plain.WriteString(text)
		start = end
	}

	// The gutter is "<old> <new>: " (each number right-justified, either side
	// blank if absent). content begins after the first ": ". Bail unless the
	// pre-": " text is the all-spaces/digits shape of a gutter.
	p := plain.String()
	cut := strings.Index(p, ": ")
	if cut < 0 || cut > 64 {
		return nil
	}
	for i := range cut {
		if c := p[i]; c != ' ' && (c < '0' || c > '9') {
			return nil
		}
	}

	content := cut + 2
	dl := &DiffLine{NewNo: newNo, Gutter: p[:content]}
	off := 0
	for _, u := range units {
		next := off + len(u.text)
		if next > content {
			t := u.text
			if off < content {
				t = t[content-off:] // drop the gutter prefix of a straddling unit
			}
			if t != "" {
				dl.Spans = append(dl.Spans, &Span{Text: t, Kind: u.kind})
			}
		}
		off = next
	}
	return dl
}

// kindForLabel maps a jj diff label to a span side. Line-number labels also
// contain "removed"/"added", but those units are in the gutter, never the
// content, so this is only ever asked about content units.
func kindForLabel(label string) Kind {
	switch {
	case strings.Contains(label, "removed"):
		return KOld
	case strings.Contains(label, "added"):
		return KNew
	default:
		return KContext
	}
}

// debugFilterWriter unwraps jj's --color=debug framing from a byte stream,
// line by line. It is used for the diff command's stderr so warnings and
// errors render as text, not <<label::…>> markup.
type debugFilterWriter struct {
	w   io.Writer
	buf []byte
}

func (d *debugFilterWriter) Write(p []byte) (int, error) {
	d.buf = append(d.buf, p...)
	for {
		i := bytes.IndexByte(d.buf, '\n')
		if i < 0 {
			break
		}
		if _, err := io.WriteString(d.w, stripDebugFrame(string(d.buf[:i]))+"\n"); err != nil {
			return len(p), err
		}
		d.buf = d.buf[i+1:]
	}
	return len(p), nil
}

// flush emits any buffered partial final line (a warning without a newline).
func (d *debugFilterWriter) flush() {
	if len(d.buf) > 0 {
		io.WriteString(d.w, stripDebugFrame(string(d.buf)))
		d.buf = nil
	}
}

// stripDebugFrame removes the <<label::…>> framing from a non-diff line (jj
// show's commit metadata and description), preserving jj's own SGR so the
// preamble stays colored as it would under --color=always.
func stripDebugFrame(raw string) string {
	var b strings.Builder
	i := 0
	for i < len(raw) {
		start, textStart, _, ok := findLabelOpener(raw, i)
		if !ok {
			b.WriteString(raw[i:])
			break
		}
		end := len(raw)
		if ns, _, _, ok2 := findLabelOpener(raw, textStart); ok2 {
			end = ns
		}
		seg := raw[textStart:end] // text + ">>" + trailing SGR
		trimmed := trimTrailingSGR(seg)
		b.WriteString(raw[i:start]) // SGR before the opener
		b.WriteString(strings.TrimSuffix(trimmed, ">>"))
		b.WriteString(seg[len(trimmed):]) // the trailing SGR, kept
		i = end
	}
	return b.String()
}

// findLabelOpener is findDiffLabelOpener without the "diff" requirement: it
// matches any letter-initial label, used to strip the preamble's framing.
// Requiring a letter start keeps content like "a << b" or "<<that>>" from
// matching.
func findLabelOpener(s string, from int) (start, textStart int, label string, ok bool) {
	for i := from; i+1 < len(s); i++ {
		if s[i] != '<' || s[i+1] != '<' {
			continue
		}
		j := i + 2
		if j >= len(s) || !(s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z') {
			continue
		}
		for ; j+1 < len(s); j++ {
			if s[j] == ':' && s[j+1] == ':' {
				return i, j + 2, s[i+2 : j], true
			}
			if c := s[j]; !(c == ' ' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				break
			}
		}
	}
	return 0, 0, "", false
}

// findDiffLabelOpener returns the next "<<diff…::" opener at or after from: its
// start, the offset just past "::", and the label. The label must start with
// "diff" and contain only label characters, so a stray "<<" or "a << b::c" in
// content is not mistaken for an opener.
func findDiffLabelOpener(s string, from int) (start, textStart int, label string, ok bool) {
	for i := from; i+1 < len(s); i++ {
		if s[i] != '<' || s[i+1] != '<' {
			continue
		}
		for j := i + 2; j+1 < len(s); j++ {
			c := s[j]
			if c == ':' && s[j+1] == ':' {
				if lbl := s[i+2 : j]; strings.HasPrefix(lbl, "diff") {
					return i, j + 2, lbl, true
				}
				break
			}
			if !(c == ' ' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				break
			}
		}
	}
	return 0, 0, "", false
}

// trimTrailingSGR strips trailing CSI SGR sequences (ESC [ … m) from s.
func trimTrailingSGR(s string) string {
	for strings.HasSuffix(s, "m") {
		i := strings.LastIndex(s, "\x1b[")
		if i < 0 {
			break
		}
		allParams := true
		for k := i + 2; k < len(s)-1; k++ {
			if c := s[k]; !(c >= '0' && c <= '9' || c == ';') {
				allParams = false
				break
			}
		}
		if !allParams {
			break
		}
		s = s[:i]
	}
	return s
}

// parseDiff streams jj's color-words output, invoking onFile for each
// file the moment its block is complete (i.e. when the next file's
// header arrives, or at EOF). Emitting incrementally lets the caller
// fetch and render the first file without waiting for the rest of a
// large diff to stream in.
func parseDiff(r io.Reader, onFile func(*FileDiff)) error {
	var current *FileDiff
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		raw := sc.Text()

		// The common case: a body gutter line of the current file. Try it
		// first (and only when we're inside a file) so the bulk of a large
		// diff takes the fast path without de-framing every line.
		if current != nil && current.Header != "" {
			if line := parseColorWordsLine(raw); line != nil {
				current.Lines = append(current.Lines, line)
				continue
			}
		}

		// Otherwise it's a header, jj show's preamble, or a non-gutter body
		// line (hunk/elision marker). Unwrap the debug framing, keeping jj's
		// own color, and pass it through verbatim.
		framed := stripDebugFrame(raw)
		if fd := parseHeader(stripANSI(framed)); fd != nil {
			if current != nil {
				onFile(current)
			}
			fd.Header = framed
			current = fd
			continue
		}
		if current == nil {
			current = &FileDiff{} // headerless preamble (jj show description)
		}
		current.Lines = append(current.Lines, &DiffLine{Raw: framed})
	}
	if current != nil {
		onFile(current)
	}
	return sc.Err()
}

// parseHeader matches the per-file header jj emits. After ANSI stripping,
// it looks like: "Modified regular file src/foo.py:". jj also emits
// "executable file" for files with the exec bit set (shell scripts,
// etc.), which we highlight the same way.
func parseHeader(plain string) *FileDiff {
	if !strings.HasSuffix(plain, ":") {
		return nil
	}
	body := plain[:len(plain)-1]
	for _, op := range []string{"Modified", "Added", "Removed", "Created", "Renamed"} {
		for _, kind := range []string{"regular", "executable"} {
			prefix := op + " " + kind + " file "
			if strings.HasPrefix(body, prefix) {
				return &FileDiff{Removed: op == "Removed", Path: stripRenameSuffix(body[len(prefix):])}
			}
		}
	}
	return nil
}

// stripRenameSuffix removes jj's " (old => new)" rename annotation that
// color-words headers append (e.g. "dst.go (src.go => dst.go)"), leaving
// just the new path so the file-show fetch gets a valid fileset.
func stripRenameSuffix(path string) string {
	if !strings.HasSuffix(path, ")") {
		return path
	}
	if i := strings.LastIndex(path, " ("); i >= 0 && strings.Contains(path[i:], " => ") {
		return path[:i]
	}
	return path
}

// stripANSI returns s with all CSI escape sequences removed.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			end := i + 2
			for end < len(s) && (s[end] < 0x40 || s[end] > 0x7e) {
				end++
			}
			if end < len(s) {
				i = end + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// parseGitDiff parses the --git unified-diff format, emitting each file via
// onFile as its block completes (at the next "diff --git" header, or EOF).
// Each body line becomes a single-span DiffLine keyed by its +/-/space
// prefix, and the gutter is that one character. Metadata lines (diff --git,
// index, ---, +++, @@, and so on) pass through as Raw.
func parseGitDiff(r io.Reader, onFile func(*FileDiff)) error {
	var current *FileDiff
	var newLine int
	var sawHunk bool // true once past this file's first @@ hunk header

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		raw := sc.Text()
		plain := stripANSI(raw)

		switch {
		case strings.HasPrefix(plain, "diff --git"):
			if current != nil {
				onFile(current)
			}
			current = &FileDiff{Header: raw, Path: parseGitFileHeader(plain)}
			sawHunk = false

		case current == nil || current.Header == "":
			// Lines before the first "diff --git" header (e.g. jj show's
			// commit metadata/description). Collect EVERY such line into a
			// headerless preamble so they pass through verbatim.
			if current == nil {
				current = &FileDiff{}
			}
			current.Lines = append(current.Lines, &DiffLine{Raw: raw})

		case strings.HasPrefix(plain, "deleted file mode"):
			current.Removed = true
			current.Lines = append(current.Lines, &DiffLine{Raw: raw})
		case strings.HasPrefix(plain, "new file mode"),
			strings.HasPrefix(plain, "rename "),
			strings.HasPrefix(plain, "copy "),
			strings.HasPrefix(plain, "similarity"),
			strings.HasPrefix(plain, "dissimilarity"),
			strings.HasPrefix(plain, "index "),
			strings.HasPrefix(plain, "old mode"),
			strings.HasPrefix(plain, "new mode"),
			strings.HasPrefix(plain, "Binary"):
			current.Lines = append(current.Lines, &DiffLine{Raw: raw})

		case !sawHunk && (strings.HasPrefix(plain, "--- ") || strings.HasPrefix(plain, "+++ ")):
			// The `--- a/x` / `+++ b/x` file headers, which only appear
			// before the first hunk. After a hunk, a body line whose
			// content starts with "-- "/"++ " (rendered "--- "/"+++ ") must
			// fall through to the +/- cases below, not be eaten as a header.
			current.Lines = append(current.Lines, &DiffLine{Raw: raw})

		case strings.HasPrefix(plain, "@@"):
			sawHunk = true
			if _, nl, ok := parseHunkHeader(plain); ok {
				newLine = nl
			}
			current.Lines = append(current.Lines, &DiffLine{Raw: raw})

		case len(plain) == 0:
			current.Lines = append(current.Lines, &DiffLine{Raw: raw})

		case plain[0] == '+':
			current.Lines = append(current.Lines, &DiffLine{
				NewNo:        newLine,
				Gutter:       "+",
				GutterAccent: KNew,
				Spans:        []*Span{{Text: plain[1:], Kind: KNew}},
			})
			newLine++
		case plain[0] == '-':
			current.Lines = append(current.Lines, &DiffLine{
				Gutter:       "-",
				GutterAccent: KOld,
				Spans:        []*Span{{Text: plain[1:], Kind: KOld}},
			})
		case plain[0] == ' ':
			current.Lines = append(current.Lines, &DiffLine{
				NewNo:  newLine,
				Gutter: " ",
				Spans:  []*Span{{Text: plain[1:], Kind: KContext}},
			})
			newLine++
		case plain[0] == '\\':
			// "\ No newline at end of file" and similar markers.
			current.Lines = append(current.Lines, &DiffLine{Raw: raw})
		default:
			current.Lines = append(current.Lines, &DiffLine{Raw: raw})
		}
	}
	if current != nil {
		onFile(current)
	}
	return sc.Err()
}

// parseGitFileHeader pulls the new-side path out of a "diff --git a/X b/Y"
// header. We use the b/ side because that's the file's name after the
// change (matters for renames).
func parseGitFileHeader(line string) string {
	for p := range strings.FieldsSeq(line) {
		if rest, ok := strings.CutPrefix(p, "b/"); ok {
			return rest
		}
	}
	return ""
}

// parseHunkHeader extracts the starting old/new line numbers from a
// "@@ -L,N +L,N @@" header. ",N" is optional (defaults to 1) so we
// handle both forms.
func parseHunkHeader(line string) (oldLine, newLine int, ok bool) {
	parts := strings.Fields(line)
	if len(parts) < 3 || parts[0] != "@@" {
		return 0, 0, false
	}
	parseLN := func(s, prefix string) int {
		s = strings.TrimPrefix(s, prefix)
		if c := strings.Index(s, ","); c > 0 {
			s = s[:c]
		}
		n, _ := strconv.Atoi(s)
		return n
	}
	oldLine = parseLN(parts[1], "-")
	newLine = parseLN(parts[2], "+")
	return oldLine, newLine, true
}
