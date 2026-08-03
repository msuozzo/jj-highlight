package main

import (
	"bytes"
	"testing"
)

// A marker splits one comment token into three, so the classes on either
// side of the label have to stay cComment while the label itself changes.
// The near-misses matter as much as the hits: matching is uppercase-only,
// front-of-body only, and boundary-strict.
func TestCommentMarkerClasses(t *testing.T) {
	src := []byte(`package p

// TODO(msuozzo): retry on EPIPE
// FIXME: this is broken
// XXX no colon
// NOTEbook is not a marker
// see TODO below
// todo lowercase is not a marker
/* NOTE: inside a block comment */
const s = "TODO: in a string"
`)
	h := newHighlighter("x.go", src)
	if h.newToks == nil {
		t.Fatal("expected parse tokens for Go source")
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
		{"// TODO", cComment},          // the opener stays comment-colored
		{"TODO(msuozzo)", cTodo},       // marker word
		{"msuozzo", cTodo},             // owner tag is part of the label
		{": retry", cTodo},             // and so is the trailing colon
		{" retry on EPIPE", cComment},  // the message is not
		{"FIXME:", cFixme},             // second class, second color
		{"XXX", cFixme},                // colon is optional
		{" no colon", cComment},        //
		{"NOTEbook", cComment},         // boundary-strict, not a prefix match
		{"TODO below", cComment},       // front-of-body only
		{"todo lowercase", cComment},   // uppercase only
		{"NOTE: inside", cTodo},        // block comments open a body too
		{"TODO: in a string", cString}, // only comments are scanned
	} {
		if got := at(tc.sub); got != tc.want {
			t.Errorf("class at %q = %d, want %d", tc.sub, got, tc.want)
		}
	}
}

// Markdown block quotes classify as comments, so they pick up markers as
// well. This is the reason matching is uppercase-only: prose says "note".
func TestCommentMarkerInMarkdownBlockQuote(t *testing.T) {
	src := []byte("# Title\n\n> NOTE: the quote is a comment\n\nPlease note this is not.\n")
	h := newHighlighter("x.md", src)
	if h.newToks == nil {
		t.Fatal("expected parse tokens for Markdown source")
	}
	at := func(sub string) tokenClass {
		off := bytes.Index(src, []byte(sub))
		if off < 0 {
			t.Fatalf("substring %q not in src", sub)
		}
		return classAt(uint32(off), h.newToks)
	}
	if got := at("NOTE:"); got != cTodo {
		t.Errorf("class at %q = %d, want %d", "NOTE:", got, cTodo)
	}
	if got := at("note this"); got == cTodo {
		t.Errorf("lowercase %q should not be a marker", "note this")
	}
}

// The label's exact extent is the part most likely to drift, and the split
// has to keep covering the whole comment so classAt's binary search stays
// correct.
func TestSplitCommentMarkerExtent(t *testing.T) {
	for _, tc := range []struct {
		comment string
		want    string // bytes the label token covers, "" when nothing splits
		class   tokenClass
	}{
		{"// TODO(msuozzo): x", "TODO(msuozzo):", cTodo},
		{"// TODO: x", "TODO:", cTodo},
		{"// TODO x", "TODO", cTodo},
		{"//TODO(b/12345)", "TODO(b/12345)", cTodo},
		{"/// NOTE: doc comment", "NOTE:", cTodo},
		{"# HACK: python and yaml", "HACK:", cFixme},
		{"> NOTE: markdown quote", "NOTE:", cTodo},
		{"/* BUG(x): y */", "BUG(x):", cFixme},
		{"/*\n * TODO: not the first line\n */", "", cNone},
		{"// TODO(unclosed: x", "TODO", cTodo},
		{"// TODOs are tracked", "", cNone},
		{"// see TODO below", "", cNone},
		{"//", "", cNone},
	} {
		src := []byte(tc.comment)
		toks := splitCommentMarkers(src, 0, uint32(len(src)))
		// Whatever the split, the tokens must tile the comment exactly.
		prev := uint32(0)
		for _, tk := range toks {
			if tk.start != prev || tk.end <= tk.start {
				t.Fatalf("%q: tokens do not tile: %v", tc.comment, toks)
			}
			prev = tk.end
		}
		if prev != uint32(len(src)) {
			t.Fatalf("%q: tokens end at %d, want %d", tc.comment, prev, len(src))
		}
		var got string
		var class tokenClass
		for _, tk := range toks {
			if tk.class != cComment {
				got, class = string(src[tk.start:tk.end]), tk.class
			}
		}
		if got != tc.want || class != tc.class {
			t.Errorf("%q: labeled %q as %d, want %q as %d", tc.comment, got, class, tc.want, tc.class)
		}
	}
}
