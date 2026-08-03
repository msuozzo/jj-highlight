package main

import (
	"slices"
	"sort"
	"strings"

	"github.com/msuozzo/bonsai"
)

// Conflict-aware highlighting for jj's materialized conflict markers.
//
// A conflict "region" spans a <<<<<<< opener through its >>>>>>> closer,
// the structural lines jj writes inside one are "markers", and a region's
// "versions" are its candidate resolutions, one per side and one per base
// (so a 2-sided conflict has three).
//
// A marker line is a run of one character from markerChars, at a uniform
// per-file length of at least minMarkerLen, followed by an optional label
// (see jj's lib/src/conflicts.rs). jj's marker styles are:
//
//   - "diff" and "diff-experimental": %%%%%%% diff sections interleaved
//     with +++++++ side snapshots
//   - "snapshot": +++++++ side and ------- base snapshots only
//   - "git": Git's diff3 markers (<<<<<<< ||||||| ======= >>>>>>>)
//
// Parsing materialized text directly yields an ERROR-recovered tree full
// of junk tokens, so instead:
//
//  1. Scan: classify every line and split each region into its versions
//     (a %%%%%%% diff section holds two, any other section body one).
//  2. Blank: for each version, copy the file and overwrite every byte the
//     version does not include with a space, so the copy parses as a
//     plausible resolution at unchanged byte offsets.
//  3. Filter: parse each copy and keep only the tokens starting on lines
//     that prefer that version's parse, so no line is highlighted twice.
//  4. Recolor: marker lines and the 1-byte diff prefixes get the fixed
//     cConflict class.
//
// The cost is one full-file parse per version (3 for a 2-sided diff-style
// conflict). Conflicted files are rare and parses are cheap.

// minMarkerLen mirrors jj's MIN_CONFLICT_MARKER_LEN. jj lengthens all of a
// file's markers uniformly past any marker-like content lines, so runs
// shorter than the opener's length are always content.
const minMarkerLen = 7

const markerChars = `<>+-%\|=`

func isMarkerByte(b byte) bool {
	return strings.IndexByte(markerChars, b) >= 0
}

// markerRun reports the marker character and run length if the line begins
// with a marker-shaped run: one marker char repeated, then end-of-line or
// ASCII whitespace (mirrors jj's parse_conflict_marker_any_len).
func markerRun(line []byte) (byte, int) {
	if len(line) == 0 || !isMarkerByte(line[0]) {
		return 0, 0
	}
	c := line[0]
	n := 1
	for n < len(line) && line[n] == c {
		n++
	}
	if n < len(line) {
		switch line[n] {
		case ' ', '\t', '\r':
		default:
			return 0, 0
		}
	}
	return c, n
}

type lineSpan struct{ start, end int } // byte offsets, end exclusive of '\n'

func splitLineSpans(src []byte) []lineSpan {
	var spans []lineSpan
	start := 0
	for i := 0; i <= len(src); i++ {
		if i == len(src) || src[i] == '\n' {
			if i == len(src) && start == i {
				break
			}
			spans = append(spans, lineSpan{start, i})
			start = i + 1
		}
	}
	return spans
}

type lineKind int

const (
	kindOutside lineKind = iota // outside any conflict: visible everywhere, canonical version 0
	kindMarker                  // marker/label line: blanked everywhere, canonical -1, fixed cConflict color
	kindBody                    // conflict body line: governed by visibleIn/canonical
)

// conflictLine classifies one line of the materialized file. visibleIn
// says which versions' copies contain the line's text. canonical says
// which version's parse to prefer, since a line can be parsed by several.
// For example, in "diff" style (one 2-sided conflict, so 3 versions):
//
//	line  content                       kind     visibleIn  canonical  prefixed
//	0     func f() {                    outside  all        0          no
//	1     <<<<<<< Conflict 1 of 1       marker   none       -          no
//	2     %%%%%%% Changes from base     marker   none       -          no
//	3     -x := 1                       body     {0}        0          yes
//	4     +x := 2                       body     {1}        1          yes
//	5      y := x                       body     {0,1}      1          yes
//	6     +++++++ Contents of side #2   marker   none       -          no
//	7     x := 3                        body     {2}        2          no
//	8     >>>>>>> Conflict 1 of 1 ends  marker   none       -          no
//	9     }                             outside  all        0          no
//
// Version 0's copy keeps lines 0, 3, 5, 9 (every other byte spaced out)
// and parses as the base resolution. Version 1 keeps 0, 4, 5, 9 and
// version 2 keeps 0, 7, 9. Line 5 is parsed by versions 0 and 1, and
// version 1's parse is preferred. Lines 0 and 9 prefer version 0's.
type conflictLine struct {
	span      lineSpan
	kind      lineKind
	visibleIn []int // kindBody: versions whose copies keep this line unblanked
	canonical int   // which version's parse to prefer, or -1 for marker lines
	prefixed  bool  // line carries a 1-byte diff prefix (-/+/space) to blank
}

// visibleTo reports whether version v's copy keeps this line's text.
func (cl *conflictLine) visibleTo(v int) bool {
	switch cl.kind {
	case kindOutside:
		return true
	case kindMarker:
		return false
	}
	return slices.Contains(cl.visibleIn, v)
}

// scanConflicts classifies the materialized file's lines and returns the
// maximum version count across regions, which is the number of blanked
// parses needed (copy v selects version v of every region at once). It
// returns (nil, 0) if the content contains no conflict regions.
//
// Detection is deliberately conservative. A <<<<<<< line only opens a
// region if a matching >>>>>>> closer follows, and a git-style region must
// also contain a ======= separator (real git-style conflicts always do).
// jj writes every marker line in a file with one uniform run length,
// lengthened past any marker-shaped content, so a region's closer and
// section markers must match the opener's length exactly and runs of any
// other length are content. An opener that fails validation is ordinary
// content, so files that merely quote marker-like lines are left alone.
// That includes a short quoted opener sitting before a real conflict,
// which must not swallow the real region's longer markers.
func scanConflicts(src []byte) (lines []conflictLine, maxVersions int) {
	spans := splitLineSpans(src)
	lines = make([]conflictLine, len(spans))
	for i := range lines {
		lines[i] = conflictLine{span: spans[i], kind: kindOutside, canonical: 0}
	}

	found := false
	for i := 0; i < len(spans); {
		c, n := markerRun(src[spans[i].start:spans[i].end])
		if c != '<' || n < minMarkerLen {
			i++
			continue
		}
		end, versions, ok := classifyRegion(src, spans, lines, i, n)
		if !ok {
			i++ // unmatched or implausible opener: treat as content
			continue
		}
		found = true
		if versions > maxVersions {
			maxVersions = versions
		}
		i = end + 1
	}
	if !found {
		return nil, 0
	}
	if maxVersions == 0 {
		maxVersions = 1
	}
	return lines, maxVersions
}

// classifyRegion validates the region opened by the <<<<<<< line at index
// start (whose marker run length is markerLen) and, if valid, classifies its
// lines into out. It returns the index of the >>>>>>> closer line.
func classifyRegion(src []byte, spans []lineSpan, out []conflictLine, start, markerLen int) (end, versions int, ok bool) {
	// Validation pass: find the closer, determine the style, and for a
	// git-style region require its ======= separator.
	line := func(i int) []byte { return src[spans[i].start:spans[i].end] }
	jjStyle := false
	if start+1 < len(spans) {
		c, n := markerRun(line(start + 1))
		jjStyle = n == markerLen && (c == '%' || c == '-' || c == '+')
	}
	closer, sawSep := -1, false
	for j := start + 1; j < len(spans); j++ {
		c, n := markerRun(line(j))
		if n != markerLen {
			continue
		}
		if c == '>' {
			closer = j
			break
		}
		if c == '=' {
			sawSep = true
		}
	}
	if closer < 0 || (!jjStyle && !sawSep) {
		return 0, 0, false
	}

	// Classification pass.
	out[start] = conflictLine{span: spans[start], kind: kindMarker, canonical: -1}
	out[closer] = conflictLine{span: spans[closer], kind: kindMarker, canonical: -1}

	nextVersion := 0
	// Current section: snapshot/git bodies are a single version. A %%%%%%%
	// diff section is a (remove, add) version pair sharing its context lines.
	remVersion, addVersion := -1, -1
	diff := false
	if !jjStyle {
		addVersion = nextVersion // git: content before |||||||/======= is side 1
		nextVersion++
	}
	for i := start + 1; i < closer; i++ {
		l := line(i)
		c, n := markerRun(l)
		if n == markerLen {
			meta := conflictLine{span: spans[i], kind: kindMarker, canonical: -1}
			switch c {
			case '%': // diff from a base to the next side: two versions
				remVersion, addVersion, diff = nextVersion, nextVersion+1, true
				nextVersion += 2
				out[i] = meta
				continue
			case '+': // snapshot of the next side
				remVersion, addVersion, diff = -1, nextVersion, false
				nextVersion++
				out[i] = meta
				continue
			case '-', '|': // snapshot of a base (jj -------, git |||||||)
				remVersion, addVersion, diff = nextVersion, -1, false
				nextVersion++
				out[i] = meta
				continue
			case '=': // git separator: side 2 follows
				remVersion, addVersion, diff = -1, nextVersion, false
				nextVersion++
				out[i] = meta
				continue
			case '\\': // label continuation line
				out[i] = meta
				continue
			}
			// A same-length run of another marker char can't be produced by
			// jj inside a region, so fall through and treat it as a body line.
		}
		cl := conflictLine{span: spans[i], kind: kindBody}
		switch {
		case diff:
			// jj's rule (parse_jj_style_conflict_hunk): "-" lines belong to
			// the base, "+" lines to the side, " "-prefixed lines to both. A
			// bare newline also counts for both (editors strip " \n").
			cl.prefixed = len(l) > 0
			switch {
			case len(l) > 0 && l[0] == '-':
				cl.visibleIn, cl.canonical = []int{remVersion}, remVersion
			case len(l) > 0 && l[0] == '+':
				cl.visibleIn, cl.canonical = []int{addVersion}, addVersion
			default:
				cl.visibleIn, cl.canonical = []int{remVersion, addVersion}, addVersion
			}
		case addVersion >= 0:
			cl.visibleIn, cl.canonical = []int{addVersion}, addVersion
		case remVersion >= 0:
			cl.visibleIn, cl.canonical = []int{remVersion}, remVersion
		default: // body before any section marker in a jj-style region
			cl.kind, cl.canonical = kindMarker, -1
		}
		out[i] = cl
	}
	return closer, nextVersion, true
}

// collectConflictTokens is the conflicted-file counterpart of
// collectTokens, with the same output contract: one sorted,
// non-overlapping token list covering the whole file. It runs the Blank,
// Filter, and Recolor steps: parse one blanked copy per version, keep
// each line's tokens from its canonical version's parse, then add fixed
// cConflict tokens for marker lines and diff prefixes.
func collectConflictTokens(parser *bonsai.Parser, src []byte, lines []conflictLine, maxVersions int) []token {
	var toks []token
	for v := range maxVersions {
		blanked := make([]byte, len(src))
		copy(blanked, src)
		for li := range lines {
			cl := &lines[li]
			if !cl.visibleTo(v) {
				for j := cl.span.start; j < cl.span.end; j++ {
					blanked[j] = ' '
				}
			} else if cl.prefixed {
				blanked[cl.span.start] = ' '
			}
		}
		root, err := parser.Parse(blanked)
		if err != nil {
			continue
		}
		// Blanking only erases lines this version does not see, and those
		// tokens get filtered out below, so comment bodies that survive are
		// still intact for marker detection.
		for _, tk := range collectTokens(root, blanked) {
			li := lineIndexAt(lines, tk.start)
			if li < 0 || lines[li].canonical != v {
				continue
			}
			toks = appendContentToken(toks, clipToCanonical(tk, lines, li, v), lines, li)
		}
	}
	// Fixed conflict-apparatus color for marker/label lines and the 1-byte
	// diff prefixes.
	for _, cl := range lines {
		if cl.kind == kindMarker && cl.span.end > cl.span.start {
			toks = append(toks, token{uint32(cl.span.start), uint32(cl.span.end), cConflict})
		}
		if cl.prefixed {
			toks = append(toks, token{uint32(cl.span.start), uint32(cl.span.start) + 1, cConflict})
		}
	}
	sort.Slice(toks, func(i, j int) bool { return toks[i].start < toks[j].start })
	return toks
}

// lineIndexAt returns the index of the line containing byte offset off (a
// line's trailing newline counts as part of it), or -1 past the last line.
func lineIndexAt(lines []conflictLine, off uint32) int {
	lo, hi := 0, len(lines)
	for lo < hi {
		mid := (lo + hi) / 2
		if uint32(lines[mid].span.end)+1 <= off {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(lines) {
		return lo
	}
	return -1
}

// appendContentToken appends tk to toks, carved around the 1-byte diff
// prefixes of any prefixed lines it covers (li is tk's starting line). The
// prefix bytes carry their own cConflict tokens and classAt needs the
// merged list sorted and non-overlapping, so content tokens must not also
// cover them. Overlap is real: markdown block containers (an atx_heading,
// a fenced_code_block) include the line's leading whitespace, so their
// tokens start exactly at the blanked prefix byte.
func appendContentToken(toks []token, tk token, lines []conflictLine, li int) []token {
	for ; li < len(lines) && uint32(lines[li].span.start) < tk.end; li++ {
		if !lines[li].prefixed {
			continue
		}
		s := uint32(lines[li].span.start)
		if s < tk.start {
			continue // token starts past this line's prefix
		}
		if s > tk.start {
			toks = append(toks, token{tk.start, s, tk.class})
		}
		tk.start = s + 1
	}
	if tk.start < tk.end {
		toks = append(toks, tk)
	}
	return toks
}

// clipToCanonical truncates a token at the first subsequent line whose
// canonical version is not v, so a token recovered across a blanked or
// shared region cannot bleed color into lines another version is
// responsible for.
func clipToCanonical(tk token, lines []conflictLine, li, v int) token {
	for ; li < len(lines); li++ {
		if lines[li].canonical != v {
			if end := uint32(lines[li].span.start); tk.end > end {
				tk.end = end
			}
			return tk
		}
		if uint32(lines[li].span.end)+1 >= tk.end {
			return tk
		}
	}
	return tk
}
