package main

import (
	"bytes"
	"strings"
	"testing"
)

// HCL classification is context-dependent (one identifier node, three
// roles), so pin the class assigned at each interesting position.
func TestTerraformTokenClasses(t *testing.T) {
	src := []byte(`resource "aws_s3_bucket" "b" {
  bucket = "my-${var.env}"
  count  = 42
  flag   = true
  tags   = { env = join("-", ["a"]) }
}
`)
	h := newHighlighter("main.tf", src)
	if h.newToks == nil {
		t.Fatal("expected parse tokens for Terraform source")
	}
	at := func(sub string) tokenClass {
		off := bytes.Index(src, []byte(sub))
		if off < 0 {
			t.Fatalf("substring %q not in src", sub)
		}
		return classAt(uint32(off), h.newToks)
	}
	for _, tc := range []struct {
		sub  string
		want tokenClass
	}{
		{"resource", cKeyword},     // block type
		{`"aws_s3_bucket"`, cType}, // first block label
		{`"b"`, cString},           // second block label
		{"bucket =", cField},       // attribute name
		{`"my-`, cString},          // interpolated string literal part
		{"${", cField},             // interpolation opener
		{"var.env", cNone},         // expression inside interpolation
		{"42", cNumber},            // numeric_lit
		{"true", cNumber},          // bool_lit
		{"env = join", cField},     // object key
		{"join", cType},            // function call
		{`"a"`, cString},           // plain string_lit
	} {
		if got := at(tc.sub); got != tc.want {
			t.Errorf("class at %q = %d, want %d", tc.sub, got, tc.want)
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
