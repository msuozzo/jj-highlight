package main

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
		if !canHighlight(name) {
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
		if canHighlight(name) {
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
		want map[string]tokenClass
	}{{
		name: "ruby colors receiverless calls only",
		path: "Gemfile",
		src:  "gem \"rails\"\n@count = 1\nr.add(:label)\nWidgets::CAP\n",
		want: map[string]tokenClass{
			"gem":     cType,   // receiverless call
			"add":     cNone,   // has a receiver
			":label":  cString, // symbol
			"@count":  cField,  // instance variable
			"Widgets": cType,   // constant
		},
	}, {
		// Rust names the inside of `#[...]` an `attribute`, the same
		// node name HCL uses for `name = value`.
		name: "rust attribute does not take the HCL attribute color",
		path: "lib.rs",
		src:  "#[derive(Debug)]\npub struct W {\n    n: u32,\n}\nfn f(&mut self) -> char { 'x' }\n",
		want: map[string]tokenClass{
			"derive": cNone,
			"W":      cType,
			"u32":    cType,
			"mut":    cKeyword,
			"self":   cKeyword,
			"'x'":    cString,
		},
	}, {
		name: "typescript members read as fields",
		path: "app.ts",
		src:  "class R {\n  #items = new Map();\n  add(w: Widget) { this.label = w.name; }\n}\n",
		want: map[string]tokenClass{
			"#items": cField,
			"add":    cField, // method name
			"this":   cKeyword,
			"name":   cField, // member access
			"Widget": cType,
			"Map":    cNone, // plain identifier, not a type position
		},
	}, {
		name: "c preprocessor directives are keywords",
		path: "widget.c",
		src:  "#include <stdio.h>\n#define MAX 128\nstatic int f(void) { char c = 'x'; return 0; }\n",
		want: map[string]tokenClass{
			"#include":  cKeyword,
			"<stdio.h>": cString,
			"#define":   cKeyword,
			"int":       cType,
			"'x'":       cString,
		},
	}, {
		name: "java builtin types and char literals",
		path: "Widget.java",
		src:  "class Sample {\n  static final int MAX = 0xFF;\n  void run() { char c = 'z'; }\n}\n",
		want: map[string]tokenClass{
			"Sample": cType,
			"int":    cType,
			"void":   cType,
			"0xFF":   cNumber,
			"'z'":    cString,
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			h := newHighlighter(tc.path, src)
			if h.newToks == nil {
				t.Fatalf("no tokens for %s", tc.path)
			}
			for sub, want := range tc.want {
				off := bytes.Index(src, []byte(sub))
				if off < 0 {
					t.Fatalf("substring %q not in src", sub)
				}
				if got := classAt(uint32(off), h.newToks); got != want {
					t.Errorf("%q: class %v, want %v", sub, got, want)
				}
			}
		})
	}
}
