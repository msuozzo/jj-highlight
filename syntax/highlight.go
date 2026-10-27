package syntax

import (
	"sort"

	"github.com/msuozzo/bonsai"
)

// Token assigns a class to the source bytes [Start, End).
type Token struct {
	Start, End uint32
	Class      Class
}

// Highlight parses src as the language path names and returns its tokens,
// sorted and non-overlapping. conflicted reports that src holds jj conflict
// markers, whose sides are parsed separately. Unsupported paths and failed
// parses yield no tokens.
func Highlight(path string, src []byte) (toks []Token, conflicted bool) {
	ctor := parserConstructor(path)
	if ctor == nil {
		return nil, false
	}
	parser := ctor()
	// Materialized conflicts would wreck a direct parse (ERROR recovery
	// plus junk tokens from the marker lines), so conflicted content is
	// tokenized side-by-side instead. See conflict.go.
	if lines, versions := scanConflicts(src); lines != nil {
		return collectConflictTokens(parser, src, lines, versions), true
	}
	root, err := parser.Parse(src)
	if err != nil {
		return nil, false
	}
	return collectTokens(root, src), false
}

// collectTokens walks the parse tree and produces a sorted, non-
// overlapping list of (byte range, class) tokens. Injection (e.g.
// markdown's block-and-inline split) has already been resolved by
// bonsai, so this function sees one unified tree.
func collectTokens(root *bonsai.Node, src []byte) []Token {
	var toks []Token
	var visit func(n *bonsai.Node)
	visit = func(n *bonsai.Node) {
		if c := classifyContext(n); c != None {
			toks = append(toks, Token{n.StartByte, n.EndByte, c})
			return
		}
		// Whole-range container? Emit once for the full node and skip
		// descent. This is how strings keep their quotes colored and
		// block comments stay solid across multiple lines.
		if c := classifyContainer(n.Kind, n.Named); c != None {
			// A comment opening with TODO, FIXME, and friends splits so the
			// label colors apart from the message. See comment.go.
			if c == Comment {
				toks = append(toks, splitCommentMarkers(src, n.StartByte, n.EndByte)...)
				return
			}
			toks = append(toks, Token{n.StartByte, n.EndByte, c})
			return
		}
		if len(n.Children) == 0 {
			if c := classify(n.Kind, n.Named); c != None {
				toks = append(toks, Token{n.StartByte, n.EndByte, c})
			}
			return
		}
		for _, ch := range n.Children {
			visit(ch)
		}
	}
	visit(root)
	sort.Slice(toks, func(i, j int) bool { return toks[i].Start < toks[j].Start })
	return toks
}

// ClassAt returns the class of the token covering off.
func ClassAt(off uint32, toks []Token) Class {
	lo, hi := 0, len(toks)
	for lo < hi {
		mid := (lo + hi) / 2
		if toks[mid].End <= off {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(toks) && toks[lo].Start <= off && off < toks[lo].End {
		return toks[lo].Class
	}
	return None
}
