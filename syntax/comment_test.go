package syntax

import (
	"bytes"
	"testing"
)

// A marker splits one comment token into three, so the classes on either
// side of the label have to stay Comment while the label itself changes.
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
	toks, _ := Highlight("x.go", src)
	if toks == nil {
		t.Fatal("expected parse tokens for Go source")
	}
	at := func(sub string) Class {
		off := bytes.Index(src, []byte(sub))
		if off < 0 {
			t.Fatalf("substring %q not in src", sub)
		}
		return ClassAt(uint32(off), toks)
	}
	for _, tc := range []struct {
		sub  string
		want Class
	}{
		{"// TODO", Comment},          // the opener stays comment-colored
		{"TODO(msuozzo)", Todo},       // marker word
		{"msuozzo", Todo},             // owner tag is part of the label
		{": retry", Todo},             // and so is the trailing colon
		{" retry on EPIPE", Comment},  // the message is not
		{"FIXME:", Fixme},             // second class, second color
		{"XXX", Fixme},                // colon is optional
		{" no colon", Comment},        //
		{"NOTEbook", Comment},         // boundary-strict, not a prefix match
		{"TODO below", Comment},       // front-of-body only
		{"todo lowercase", Comment},   // uppercase only
		{"NOTE: inside", Todo},        // block comments open a body too
		{"TODO: in a string", String}, // only comments are scanned
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
	toks, _ := Highlight("x.md", src)
	if toks == nil {
		t.Fatal("expected parse tokens for Markdown source")
	}
	at := func(sub string) Class {
		off := bytes.Index(src, []byte(sub))
		if off < 0 {
			t.Fatalf("substring %q not in src", sub)
		}
		return ClassAt(uint32(off), toks)
	}
	if got := at("NOTE:"); got != Todo {
		t.Errorf("class at %q = %d, want %d", "NOTE:", got, Todo)
	}
	if got := at("note this"); got == Todo {
		t.Errorf("lowercase %q should not be a marker", "note this")
	}
}

// The label's exact extent is the part most likely to drift, and the split
// has to keep covering the whole comment so ClassAt's binary search stays
// correct.
func TestSplitCommentMarkerExtent(t *testing.T) {
	for _, tc := range []struct {
		comment string
		want    string // bytes the label token covers, "" when nothing splits
		class   Class
	}{
		{"// TODO(msuozzo): x", "TODO(msuozzo):", Todo},
		{"// TODO: x", "TODO:", Todo},
		{"// TODO x", "TODO", Todo},
		{"//TODO(b/12345)", "TODO(b/12345)", Todo},
		{"/// NOTE: doc comment", "NOTE:", Todo},
		{"# HACK: python and yaml", "HACK:", Fixme},
		{"> NOTE: markdown quote", "NOTE:", Todo},
		{"/* BUG(x): y */", "BUG(x):", Fixme},
		{"/*\n * TODO: not the first line\n */", "", None},
		{"// TODO(unclosed: x", "TODO", Todo},
		{"// TODOs are tracked", "", None},
		{"// see TODO below", "", None},
		{"//", "", None},
	} {
		src := []byte(tc.comment)
		toks := splitCommentMarkers(src, 0, uint32(len(src)))
		// Whatever the split, the tokens must tile the comment exactly.
		prev := uint32(0)
		for _, tk := range toks {
			if tk.Start != prev || tk.End <= tk.Start {
				t.Fatalf("%q: tokens do not tile: %v", tc.comment, toks)
			}
			prev = tk.End
		}
		if prev != uint32(len(src)) {
			t.Fatalf("%q: tokens end at %d, want %d", tc.comment, prev, len(src))
		}
		var got string
		var class Class
		for _, tk := range toks {
			if tk.Class != Comment {
				got, class = string(src[tk.Start:tk.End]), tk.Class
			}
		}
		if got != tc.want || class != tc.class {
			t.Errorf("%q: labeled %q as %d, want %q as %d", tc.comment, got, class, tc.want, tc.class)
		}
	}
}
