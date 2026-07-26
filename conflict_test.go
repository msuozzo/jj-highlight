package main

import (
	"bytes"
	"strings"
	"testing"

	bonsaigo "github.com/msuozzo/bonsai/bonsai-go"
	bonsaimarkdown "github.com/msuozzo/bonsai/bonsai-markdown"
)

func TestMarkerRun(t *testing.T) {
	cases := []struct {
		line string
		c    byte
		n    int
	}{
		{"<<<<<<<", '<', 7},
		{"<<<<<<< conflict 1 of 1", '<', 7},
		{">>>>>>> conflict 1 of 1 ends", '>', 7},
		{"%%%%%%%\tdiff", '%', 7},
		{`\\\\\\\        to: abc`, '\\', 7},
		{"<<<<<<<<<<<<<<< long", '<', 15},
		{"=======", '=', 7},
		{"||||||| base", '|', 7},
		{"<<<<<<<", '<', 7},
		{"<<<<<<<x", 0, 0},    // no whitespace after the run
		{"<<<<<<a", 0, 0},     // run broken by a non-marker char
		{"code < 7", 0, 0},    // runs must start the line
		{"< 7 items", '<', 1}, // a leading run, just short (callers check length)
		{"", 0, 0},
		{"plain line", 0, 0},
	}
	for _, c := range cases {
		gc, gn := markerRun([]byte(c.line))
		if gc != c.c || gn != c.n {
			t.Errorf("markerRun(%q) = (%q, %d), want (%q, %d)", c.line, gc, gn, c.c, c.n)
		}
	}
}

// summarizeLines renders scanConflicts' classification compactly for
// asserts: "c" outside-conflict lines, "m" marker/label, digits for a
// body line's canonical version, "*" for prefixed lines.
func summarizeLines(lines []conflictLine) string {
	var b strings.Builder
	for i, cl := range lines {
		if i > 0 {
			b.WriteByte(' ')
		}
		switch cl.kind {
		case kindOutside:
			b.WriteByte('c')
		case kindMarker:
			b.WriteByte('m')
		default:
			b.WriteByte(byte('0' + cl.canonical))
		}
		if cl.prefixed {
			b.WriteByte('*')
		}
	}
	return b.String()
}

func TestScanConflicts(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		summary  string // "" means no conflicts detected
		versions int
	}{
		{
			name: "diff style",
			src: "before\n" +
				"<<<<<<< conflict 1 of 1\n" +
				"%%%%%%% diff from: aaa \"base\"\n" +
				`\\\\\\\        to: bbb "side1"` + "\n" +
				"-old\n" +
				"+new\n" +
				" shared\n" +
				"+++++++ ccc \"side2\"\n" +
				"side2 body\n" +
				">>>>>>> conflict 1 of 1 ends\n" +
				"after\n",
			summary:  "c m m m 0* 1* 1* m 2 m c",
			versions: 3,
		},
		{
			name: "snapshot style",
			src: "<<<<<<< conflict 1 of 1\n" +
				"+++++++ side1\n" +
				"s1\n" +
				"------- base\n" +
				"b\n" +
				"+++++++ side2\n" +
				"s2\n" +
				">>>>>>> ends\n",
			summary:  "m m 0 m 1 m 2 m",
			versions: 3,
		},
		{
			name: "git style",
			src: "ctx\n" +
				"<<<<<<< side1\n" +
				"s1\n" +
				"||||||| base\n" +
				"b\n" +
				"=======\n" +
				"s2\n" +
				">>>>>>> side2\n",
			summary:  "c m 0 m 1 m 2 m",
			versions: 3,
		},
		{
			name: "three sided diff style",
			src: "<<<<<<< conflict 1 of 1\n" +
				"%%%%%%% diff 1\n" +
				"-o\n" +
				"+a\n" +
				"+++++++ snap\n" +
				"s\n" +
				"%%%%%%% diff 2\n" +
				"-o\n" +
				"+b\n" +
				">>>>>>> ends\n",
			summary:  "m m 0* 1* m 2 m 3* 4* m",
			versions: 5,
		},
		{
			name: "lengthened markers keep short runs as content",
			src: "<<<<<<<<<<<<<<< conflict 1 of 1\n" +
				"+++++++++++++++ side1\n" +
				"<<<<<<< looks like a marker but is content\n" +
				"+++++++++++++++ side2\n" +
				"=======\n" +
				">>>>>>>>>>>>>>> ends\n",
			summary:  "m m 0 m 1 m",
			versions: 2,
		},
		{
			name:    "unclosed opener is content",
			src:     "<<<<<<< not a conflict\ncode here\n",
			summary: "",
		},
		{
			// Found by TestConflictScanAgainstJJ: the quoted 8-char opener
			// used to validate as a git-style region (the real closer
			// satisfied its >= length check, a '=' content run its
			// separator check) and swallow the real region. Marker runs
			// must match the opener's length exactly.
			name: "short quoted opener before a real region stays content",
			src: "<<<<<<<< quoted marker\n" +
				"<<<<<<<<<<< conflict 1 of 1\n" +
				"+++++++++++ side1\n" +
				"========= junk\n" +
				"----------- base\n" +
				"b\n" +
				"+++++++++++ side2\n" +
				"s2\n" +
				">>>>>>>>>>> ends\n",
			summary:  "c m m 0 m 1 m 2 m",
			versions: 3,
		},
		{
			name: "git-shaped region without separator is content",
			src: "<<<<<<< quoted marker\n" +
				"some prose about conflicts\n" +
				">>>>>>> quoted marker\n",
			summary: "",
		},
		{
			name:    "no markers at all",
			src:     "package main\n\nfunc main() {}\n",
			summary: "",
		},
		{
			name: "second region after invalid opener",
			src: "<<<<<<< unclosed\n" +
				"<<<<<<< conflict 1 of 1\n" +
				"%%%%%%% diff\n" +
				"-o\n" +
				"+n\n" +
				">>>>>>> ends\n",
			summary:  "c m m 0* 1* m",
			versions: 2,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines, versions := scanConflicts([]byte(c.src))
			if c.summary == "" {
				if lines != nil {
					t.Fatalf("scanConflicts = %q, want no conflicts", summarizeLines(lines))
				}
				return
			}
			if lines == nil {
				t.Fatalf("scanConflicts found no conflicts, want %q", c.summary)
			}
			if got := summarizeLines(lines); got != c.summary {
				t.Errorf("summary = %q, want %q", got, c.summary)
			}
			if versions != c.versions {
				t.Errorf("versions = %d, want %d", versions, c.versions)
			}
		})
	}
}

// materializedGo is real `jj file show` output for a conflicted Go file in
// the default "diff" marker style.
var materializedGo = []byte(`package main

import "fmt"

func greet(name string) {
<<<<<<< conflict 1 of 1
%%%%%%% diff from: utvowqsv 09280ee6 "base"
\\\\\\\        to: omvonsqx 7286b3e2 "side1"
-	fmt.Println("Hello, " + name)
+	fmt.Println("Howdy, " + name)
+++++++ pkqyukxq 1ac44a86 "side2"
	fmt.Println("Hello there, " + name + "!")
>>>>>>> conflict 1 of 1 ends
}

func main() {
	greet("world")
}
`)

func TestConflictTokensGo(t *testing.T) {
	lines, versions := scanConflicts(materializedGo)
	if lines == nil {
		t.Fatal("scanConflicts found no conflicts")
	}
	toks := collectConflictTokens(bonsaigo.NewParser(), materializedGo, lines, versions)
	if len(toks) == 0 {
		t.Fatal("no tokens produced")
	}

	classOf := func(substr string) tokenClass {
		off := bytes.Index(materializedGo, []byte(substr))
		if off < 0 {
			t.Fatalf("substring %q not found", substr)
		}
		return classAt(uint32(off), toks)
	}
	cases := []struct {
		substr string
		want   tokenClass
	}{
		// Context outside the conflict.
		{`"fmt"`, cString},
		{"package", cKeyword},
		{`"world"`, cString},
		// The base (-) line, the side (+) line, and the snapshot side all
		// carry syntax color.
		{`"Hello, "`, cString},
		{`"Howdy, "`, cString},
		{`"Hello there, "`, cString},
		{`"!"`, cString},
		// Markers, labels, and diff prefixes are conflict apparatus. The
		// commit ids and descriptions on label lines must NOT leak through
		// as number/string tokens.
		{"<<<<<<<", cConflict},
		{"%%%%%%%", cConflict},
		{`\\\\\\\`, cConflict},
		{"+++++++ pkqyukxq", cConflict},
		{">>>>>>>", cConflict},
		{"09280ee6", cConflict},
		{`"side2"`, cConflict},
		{"-\tfmt", cConflict}, // the 1-byte "-" prefix
		{"+\tfmt", cConflict}, // the 1-byte "+" prefix
	}
	for _, c := range cases {
		if got := classOf(c.substr); got != c.want {
			t.Errorf("class at %q = %d, want %d", c.substr, got, c.want)
		}
	}

	// The bytes just past the diff prefixes are code again, not apparatus.
	for _, substr := range []string{"-\tfmt", "+\tfmt"} {
		off := bytes.Index(materializedGo, []byte(substr))
		if got := classAt(uint32(off)+1, toks); got == cConflict {
			t.Errorf("byte after %q prefix still cConflict", substr)
		}
	}
}

func TestConflictTokensDoNotOverlap(t *testing.T) {
	lines, versions := scanConflicts(materializedGo)
	toks := collectConflictTokens(bonsaigo.NewParser(), materializedGo, lines, versions)
	for i := 1; i < len(toks); i++ {
		if toks[i].start < toks[i-1].end {
			t.Fatalf("tokens overlap: [%d,%d) then [%d,%d)",
				toks[i-1].start, toks[i-1].end, toks[i].start, toks[i].end)
		}
	}
}

// materializedMd puts a markdown heading and a fenced code block on diff
// "+" lines. Their block-container tokens include the line's leading
// whitespace, i.e. they start at the very byte the 1-byte prefix token
// occupies (regression: the overlap broke classAt's binary search, so the
// prefix stole the heading color and the rest of the line went bare).
var materializedMd = []byte("intro\n" +
	"<<<<<<< conflict 1 of 1\n" +
	"%%%%%%% diff from: aaa \"base\"\n" +
	"\\\\\\\\\\\\\\        to: bbb \"side1\"\n" +
	"+# HEAD\n" +
	"+\n" +
	"+plain prose\n" +
	"+```\n" +
	"+code()\n" +
	"+```\n" +
	"+++++++ ccc \"side2\"\n" +
	"# Plan\n" +
	">>>>>>> conflict 1 of 1 ends\n" +
	"outro\n")

func TestConflictTokensMarkdown(t *testing.T) {
	lines, versions := scanConflicts(materializedMd)
	if lines == nil {
		t.Fatal("scanConflicts found no conflicts")
	}
	toks := collectConflictTokens(bonsaimarkdown.NewFullParser(), materializedMd, lines, versions)
	for i := 1; i < len(toks); i++ {
		if toks[i].start < toks[i-1].end {
			t.Fatalf("tokens overlap: [%d,%d) then [%d,%d)",
				toks[i-1].start, toks[i-1].end, toks[i].start, toks[i].end)
		}
	}
	cases := []struct {
		substr string
		at     uint32 // offset into the substring to probe
		want   tokenClass
	}{
		{"+# HEAD", 0, cConflict}, // the prefix byte stays apparatus...
		{"+# HEAD", 1, cKeyword},  // ...and the heading keeps its color
		{"+# HEAD", 3, cKeyword},
		{"+```", 0, cConflict},
		{"+```", 1, cString}, // fenced code block spans its prefixed lines
		{"+code()", 1, cString},
		{"# Plan", 0, cKeyword}, // snapshot side, no prefixes involved
	}
	for _, c := range cases {
		off := uint32(bytes.Index(materializedMd, []byte(c.substr)))
		if got := classAt(off+c.at, toks); got != c.want {
			t.Errorf("class at %q+%d = %d, want %d", c.substr, c.at, got, c.want)
		}
	}
}

func TestParseHeaderConflicts(t *testing.T) {
	cases := []struct {
		header  string
		path    string
		removed bool
	}{
		{"Created conflict in main.go:", "main.go", false},
		{"Modified conflict in src/app.py:", "src/app.py", false},
		{"Resolved conflict in main.go:", "main.go", false},
		{"Added conflict new.go:", "new.go", false},
		{"Removed conflict gone.go:", "gone.go", true},
		{"Modified conflict in dst.go (src.go => dst.go):", "dst.go", false},
		// The pre-existing forms still parse.
		{"Modified regular file main.go:", "main.go", false},
		{"Removed executable file run.sh:", "run.sh", true},
	}
	for _, c := range cases {
		fd := parseHeader(c.header)
		if fd == nil {
			t.Errorf("parseHeader(%q) = nil", c.header)
			continue
		}
		if fd.Path != c.path || fd.Removed != c.removed {
			t.Errorf("parseHeader(%q) = {Path: %q, Removed: %v}, want {%q, %v}",
				c.header, fd.Path, fd.Removed, c.path, c.removed)
		}
	}
	for _, bad := range []string{
		"Modified conflict in main.go", // no trailing colon
		"conflict in main.go:",
		"Something else entirely:",
	} {
		if fd := parseHeader(bad); fd != nil {
			t.Errorf("parseHeader(%q) = %+v, want nil", bad, fd)
		}
	}
}
