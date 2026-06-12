package main

import (
	"os/exec"
	"strings"
	"sync"
)

// Fetches concurrently runs per-file `jj file show` to get file contents.
type Fetches struct {
	mu         sync.Mutex
	files      map[string]*fileFetch
	newRev     string
	fetchFlags []string
	git        bool
}

type fileFetch struct {
	done chan struct{}
	new  []byte
}

func newFetches(newRev string, fetchFlags []string, git bool) *Fetches {
	return &Fetches{files: make(map[string]*fileFetch), newRev: newRev, fetchFlags: fetchFlags, git: git}
}

// start kicks a background fetch of a file's new-revision contents so it
// overlaps reading the rest of the diff.
// - Removed files and non-highlightable ones are skipped.
// - Safe to call from the parser goroutine while wait runs on another.
// - Idempotent per path.
func (f *Fetches) start(file *FileDiff) {
	if file.Removed || !canHighlight(file.Path) {
		return
	}
	f.mu.Lock()
	if _, ok := f.files[file.Path]; ok {
		f.mu.Unlock()
		return
	}
	ff := &fileFetch{done: make(chan struct{})}
	f.files[file.Path] = ff
	f.mu.Unlock()

	path := file.Path
	go func() {
		defer close(ff.done)
		ff.new = jjFileShow(f.fetchFlags, f.newRev, path, f.git)
	}()
}

// wait blocks for the fetch of path and returns its contents, or nil if the
// path was not started (not highlightable or removed) or the fetch failed.
func (f *Fetches) wait(path string) []byte {
	f.mu.Lock()
	ff, ok := f.files[path]
	f.mu.Unlock()
	if !ok {
		return nil
	}
	<-ff.done
	return ff.new
}

// jjFileShow returns a file's contents at rev for diff parse context,
// forwarding the globals in fetchFlags (-R, --at-operation, --config) so
// the content matches the diff. Its relativity depends on the format
// (color-words is cwd-relative, git is workspace-root-relative), encoded by
// jjFileset.
func jjFileShow(fetchFlags []string, rev, path string, git bool) []byte {
	args := append(append([]string{}, fetchFlags...), "-r", rev, jjFileset(git, path))
	return fetchFile(args...)
}

// fetchFile runs `jj file show <args>` and returns its stdout, or nil on
// any error (jj missing, file absent, bad fileset). A nil result renders
// the file uncolored.
func fetchFile(args ...string) []byte {
	out, err := exec.Command("jj", append([]string{"file", "show"}, args...)...).Output()
	if err != nil {
		return nil
	}
	return out
}

// jjFileList resolves the file-show args' filesets to the matching file
// paths, one per line, cwd-relative, at the same revision and flags.
func jjFileList(jjArgs []string) ([]string, error) {
	out, err := exec.Command("jj", append([]string{"file", "list"}, jjArgs...)...).Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// fileShowOne returns one file's contents for file-show mode, forwarding
// flagArgs (revision, repository, other globals). The cwd-relative path
// from jjFileList is quoted so metacharacters in the name are not parsed as
// fileset operators. nil on any error, which renders uncolored.
func fileShowOne(flagArgs []string, file string) []byte {
	args := append(append([]string{}, flagArgs...), jjFileset(false, file))
	return fetchFile(args...)
}

// jjFileset builds a jj fileset selector for a single file. git diff
// headers report paths relative to the workspace root and color-words
// headers relative to cwd, so the prefix follows the format. The path is
// wrapped in a double-quoted string literal (with backslash and quote
// escaped) so fileset metacharacters in a filename are taken literally rather
// than parsed as operators.
func jjFileset(git bool, path string) string {
	prefix := "cwd:"
	if git {
		prefix = "root:"
	}
	q := strings.ReplaceAll(path, `\`, `\\`)
	q = strings.ReplaceAll(q, `"`, `\"`)
	return prefix + `"` + q + `"`
}
