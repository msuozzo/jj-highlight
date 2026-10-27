package main

import (
	"strings"

	"github.com/msuozzo/jj-highlight/syntax"
)

// oldMarkerLine reports whether a display line looks like an old-side-only
// marker line: every span removed and the reassembled text opening with a
// marker-shaped run. The old side is never fetched, so this is shape
// detection, not region validation. Callers gate on the new side still
// being conflicted to limit the false-positive surface.
func oldMarkerLine(line *DiffLine) bool {
	if line.NewNo != 0 || len(line.Spans) == 0 {
		return false
	}
	var b strings.Builder
	for _, s := range line.Spans {
		if s.Kind != KOld {
			return false
		}
		b.WriteString(s.Text)
	}
	return syntax.IsConflictMarker([]byte(b.String()))
}
