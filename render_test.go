package main

import (
	"bytes"
	"strings"
	"testing"
)

// Old-side-only marker lines take the Conflict style while the new side
// is still conflicted. Removed content, resolved conflicts, and files
// that were never conflicted keep the plain Removed style.
func TestOldSideConflictMarkersStyledAsConflict(t *testing.T) {
	oldOnly := func(spans ...*Span) *DiffLine {
		return &DiffLine{Gutter: "   5     : ", Spans: spans}
	}
	lines := []*DiffLine{
		oldOnly(&Span{Text: `+++++++ xxvxtqpr f2db07e2 "side1"`, Kind: KOld}),
		// One label line word-split into several removed spans.
		oldOnly(
			&Span{Text: `\\\\\\\        to: `, Kind: KOld},
			&Span{Text: "qtxnwkqs", Kind: KOld},
			&Span{Text: ` "A"`, Kind: KOld},
		),
		oldOnly(&Span{Text: "\tx := 100", Kind: KOld}), // content, not a marker
	}
	f := &FileDiff{Path: "x.go", Header: "Modified conflict in x.go:", Lines: lines}
	stillConflicted := []byte("<<<<<<<\n+++++++ s\nx := 2\n>>>>>>>\n")
	var buf bytes.Buffer
	renderFile(&buf, f, newHighlighter("x.go", stillConflicted), defaultTheme)
	got := strings.Split(buf.String(), "\n")
	conflict := defaultTheme.ConflictRemoved.ANSI() + defaultTheme.Conflict.ANSI()
	for i, want := range []bool{true, true, false} {
		if strings.Contains(got[i+1], conflict) != want {
			t.Errorf("line %d conflict-styled = %v, want %v: %q", i, !want, want, got[i+1])
		}
	}

	for path, hdr := range map[string]string{
		"x.go": "Resolved conflict in x.go:",
		"x.md": "Modified regular file x.md:",
	} {
		f = &FileDiff{Path: path, Header: hdr, Lines: lines[:1]}
		buf.Reset()
		renderFile(&buf, f, newHighlighter(path, nil), defaultTheme)
		if strings.Contains(buf.String(), defaultTheme.Conflict.ANSI()) {
			t.Errorf("%q should keep the removed style: %q", hdr, buf.String())
		}
	}
}

// jj's color-words output can split one new line across consecutive display
// lines: when a removed span crosses an old-line boundary, the next display
// line repeats the same new-side number and continues the new line where the
// previous one left off. Token lookups on the continuation line must account
// for the new-side bytes already consumed, or every highlight lands shifted
// toward line start (e.g. a string's opening quote rendered unstyled).
func TestRenderFileCarriesNewColumnAcrossSplitLines(t *testing.T) {
	src := []byte("package p\n\nfunc f() string {\n\treturn \"docker ps\"\n}\n")
	h := newHighlighter("x.go", src)
	if h.newToks == nil {
		t.Fatal("expected parse tokens for Go source")
	}
	// Two display lines both labeled new line 4: the matched "\treturn "
	// landed on the first alongside removed old text, and the added string
	// literal continues on the second at new-line column 8.
	f := &FileDiff{Path: "x.go", Header: "Modified regular file x.go:", Lines: []*DiffLine{
		{NewNo: 4, Gutter: " 3    4: ", Spans: []*Span{
			{Text: "\treturn ", Kind: KContext},
			{Text: "old(", Kind: KOld},
		}},
		{NewNo: 4, Gutter: " 4    4: ", Spans: []*Span{
			{Text: "\told", Kind: KOld},
			{Text: `"docker ps"`, Kind: KNew},
		}},
	}}
	var buf bytes.Buffer
	renderFile(&buf, f, h, defaultTheme)
	lines := strings.Split(buf.String(), "\n")
	want := defaultTheme.String.ANSI() + `"docker ps"`
	if !strings.Contains(lines[2], want) {
		t.Errorf("continuation line should apply the string style from the opening quote:\ngot  %q\nwant substring %q", lines[2], want)
	}
}
