package main

import "strings"

// Annotation markers inside comments.
//
// A comment opening with TODO, NOTE, FIXME, XXX, HACK, or BUG carries an
// annotation rather than prose, and reads better with that label colored
// apart from its message. Grammars are no help here: tree-sitter emits one
// comment node for the whole run, so the label has to come from scanning the
// comment's own bytes.
//
// collectTokens routes every cComment container through splitCommentMarkers,
// which turns the one token into three (opener, label, remainder). Keying off
// the class rather than a node type means every grammar we dispatch picks
// this up at once, and markdown block quotes (also cComment) come along with
// them.
//
// Matching is narrow on purpose, since a false positive recolors ordinary
// prose:
//
//   - Uppercase only, so the word "note" in a markdown block quote stays
//     prose.
//   - First word of the comment body only, so `// see TODO below` does not
//     match.
//   - Boundary-strict, so TODOs and NOTEbook do not either.
//
// The label extends over an immediately following `(owner)` and `:`, so that
// `TODO(msuozzo):` reads as one unit.

// commentMarkers splits the labels across two classes: TODO and NOTE are
// informational, the rest report something broken.
var commentMarkers = map[string]tokenClass{
	"TODO":  cTodo,
	"NOTE":  cTodo,
	"FIXME": cFixme,
	"XXX":   cFixme,
	"HACK":  cFixme,
	"BUG":   cFixme,
}

// commentOpeners are the bytes a comment's own syntax puts ahead of its
// body, across every grammar we dispatch: `//`, `///`, `//!`, `/*`, `/**`,
// `#`, `#!`, and markdown's `>`.
const commentOpeners = "/#*!>"

// splitCommentMarkers returns the tokens covering the comment occupying
// src[start:end]. A comment whose body opens with a marker splits into three
// (opener, label, remainder), anything else stays the one cComment token.
// Output is ordered and non-overlapping, as classAt requires.
func splitCommentMarkers(src []byte, start, end uint32) []token {
	whole := []token{{start, end, cComment}}
	if start >= end || int(end) > len(src) {
		return whole
	}
	body := start
	for body < end && (src[body] == ' ' || src[body] == '\t' ||
		strings.IndexByte(commentOpeners, src[body]) >= 0) {
		body++
	}
	word := body
	for word < end && src[word] >= 'A' && src[word] <= 'Z' {
		word++
	}
	class, ok := commentMarkers[string(src[body:word])]
	// A word byte just past the match means the label was only a prefix of a
	// longer word (TODOs, NOTEbook). The leading side needs no such check:
	// the skip loop above stopped on an opener or a space, and neither is a
	// word byte.
	if !ok || (word < end && isWordByte(src[word])) {
		return whole
	}
	label := word
	if label < end && src[label] == '(' {
		if past, ok := scanOwner(src, label, end); ok {
			label = past
		}
	}
	if label < end && src[label] == ':' {
		label++
	}
	toks := make([]token, 0, 3)
	if body > start {
		toks = append(toks, token{start, body, cComment})
	}
	toks = append(toks, token{body, label, class})
	if label < end {
		toks = append(toks, token{label, end, cComment})
	}
	return toks
}

// scanOwner returns the offset just past the `(owner)` tag opening at open.
// Requiring it to close on the same line stops an unbalanced `(` inside a
// block comment from swallowing the rest of the comment.
func scanOwner(src []byte, open, end uint32) (uint32, bool) {
	for i := open + 1; i < end; i++ {
		if src[i] == ')' {
			return i + 1, true
		}
		if src[i] == '\n' {
			break
		}
	}
	return 0, false
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
