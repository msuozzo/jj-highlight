package main

import (
	"bytes"
	"strings"
	"testing"
)

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
