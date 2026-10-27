package main

import "testing"

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
