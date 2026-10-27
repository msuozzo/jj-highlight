package syntax

import (
	"bytes"
	"testing"
)

// A file type with no dispatch entry renders entirely uncolored, which
// is easy to ship and hard to notice, so pin every name we claim.
func TestGrammarDispatch(t *testing.T) {
	for _, name := range []string{
		"main.go", "setup.py", "README.md", "notes.markdown",
		"compose.yaml", "compose.yml", "Dockerfile", "Containerfile",
		"Dockerfile.builder", "base.dockerfile", "run.sh", "run.bash",
		"run.zsh", "page.tmpl", "page.gotmpl", "page.gohtml",
		"main.tf", "vars.tfvars", "config.hcl",
		"lib.rs", "Widget.java",
		"app.rb", "tasks.rake", "gem.gemspec", "config.ru",
		"Gemfile", "Rakefile", "Podfile", "Brewfile",
		"app.js", "app.mjs", "app.cjs", "app.jsx",
		"app.ts", "app.mts", "app.cts", "app.tsx",
		"widget.c", "widget.h",
	} {
		if !Supported(name) {
			t.Errorf("no grammar dispatched for %q", name)
		}
	}
	// No C++ grammar, so these stay uncolored rather than being parsed
	// by one that would reject them. Kotlin and Groovy are deliberately
	// absent too, pending their own change.
	for _, name := range []string{
		"widget.cc", "widget.cpp", "notes.txt", "data.json",
		"Widget.kt", "build.gradle", "Jenkinsfile",
	} {
		if Supported(name) {
			t.Errorf("unexpected grammar dispatched for %q", name)
		}
	}
}

// The rules these pin are context-dependent (a bare identifier whose
// color depends on its position) or cross-grammar collisions, both of
// which break silently when another grammar is added.
func TestContextualTokenClasses(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		src  string
		want map[string]Class
	}{{
		name: "ruby colors receiverless calls only",
		path: "Gemfile",
		src:  "gem \"rails\"\n@count = 1\nr.add(:label)\nWidgets::CAP\n",
		want: map[string]Class{
			"gem":     Type,   // receiverless call
			"add":     None,   // has a receiver
			":label":  String, // symbol
			"@count":  Field,  // instance variable
			"Widgets": Type,   // constant
		},
	}, {
		// Rust names the inside of `#[...]` an `attribute`, the same
		// node name HCL uses for `name = value`.
		name: "rust attribute does not take the HCL attribute color",
		path: "lib.rs",
		src:  "#[derive(Debug)]\npub struct W {\n    n: u32,\n}\nfn f(&mut self) -> char { 'x' }\n",
		want: map[string]Class{
			"derive": None,
			"W":      Type,
			"u32":    Type,
			"mut":    Keyword,
			"self":   Keyword,
			"'x'":    String,
		},
	}, {
		name: "typescript members read as fields",
		path: "app.ts",
		src:  "class R {\n  #items = new Map();\n  add(w: Widget) { this.label = w.name; }\n}\n",
		want: map[string]Class{
			"#items": Field,
			"add":    Field, // method name
			"this":   Keyword,
			"name":   Field, // member access
			"Widget": Type,
			"Map":    None, // plain identifier, not a type position
		},
	}, {
		name: "c preprocessor directives are keywords",
		path: "widget.c",
		src:  "#include <stdio.h>\n#define MAX 128\nstatic int f(void) { char c = 'x'; return 0; }\n",
		want: map[string]Class{
			"#include":  Keyword,
			"<stdio.h>": String,
			"#define":   Keyword,
			"int":       Type,
			"'x'":       String,
		},
	}, {
		name: "java builtin types and char literals",
		path: "Widget.java",
		src:  "class Sample {\n  static final int MAX = 0xFF;\n  void run() { char c = 'z'; }\n}\n",
		want: map[string]Class{
			"Sample": Type,
			"int":    Type,
			"void":   Type,
			"0xFF":   Number,
			"'z'":    String,
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			toks, _ := Highlight(tc.path, src)
			if toks == nil {
				t.Fatalf("no tokens for %s", tc.path)
			}
			for sub, want := range tc.want {
				off := bytes.Index(src, []byte(sub))
				if off < 0 {
					t.Fatalf("substring %q not in src", sub)
				}
				if got := ClassAt(uint32(off), toks); got != want {
					t.Errorf("%q: class %v, want %v", sub, got, want)
				}
			}
		})
	}
}

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
	toks, _ := Highlight("main.tf", src)
	if toks == nil {
		t.Fatal("expected parse tokens for Terraform source")
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
		{"resource", Keyword},     // block type
		{`"aws_s3_bucket"`, Type}, // first block label
		{`"b"`, String},           // second block label
		{"bucket =", Field},       // attribute name
		{`"my-`, String},          // interpolated string literal part
		{"${", Field},             // interpolation opener
		{"var.env", None},         // expression inside interpolation
		{"42", Number},            // numeric_lit
		{"true", Number},          // bool_lit
		{"env = join", Field},     // object key
		{"join", Type},            // function call
		{`"a"`, String},           // plain string_lit
	} {
		if got := at(tc.sub); got != tc.want {
			t.Errorf("class at %q = %d, want %d", tc.sub, got, tc.want)
		}
	}
}
