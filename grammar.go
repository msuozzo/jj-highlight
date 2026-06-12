package main

import (
	"path/filepath"
	"strings"

	"github.com/msuozzo/bonsai"
	bonsaibash "github.com/msuozzo/bonsai/bonsai-bash"
	bonsaidockerfile "github.com/msuozzo/bonsai/bonsai-dockerfile"
	bonsaigo "github.com/msuozzo/bonsai/bonsai-go"
	bonsaigotemplate "github.com/msuozzo/bonsai/bonsai-gotemplate"
	bonsaimarkdown "github.com/msuozzo/bonsai/bonsai-markdown"
	bonsaipython "github.com/msuozzo/bonsai/bonsai-python"
	bonsaiyaml "github.com/msuozzo/bonsai/bonsai-yaml"
)

func canHighlight(path string) bool { return parserConstructor(path) != nil }

// parserConstructor returns the bonsai parser constructor for a file, or
// nil if the file's name and extension match no grammar.
func parserConstructor(path string) func() *bonsai.Parser {
	// Filename dispatch first, for files without extensions.
	base := strings.ToLower(filepath.Base(path))
	switch {
	case base == "dockerfile",
		base == "containerfile",
		strings.HasPrefix(base, "dockerfile."):
		return newDockerfileWithBash
	}

	switch strings.ToLower(filepath.Ext(path)) {
	case ".py":
		return bonsaipython.NewParser
	case ".go":
		return bonsaigo.NewParser
	case ".md", ".markdown":
		// Use parser that composes both Markdown grammars,
		return bonsaimarkdown.NewFullParser
	case ".yaml", ".yml":
		return bonsaiyaml.NewParser
	case ".dockerfile":
		return newDockerfileWithBash
	case ".sh", ".bash", ".zsh":
		return bonsaibash.NewParser
	case ".tmpl", ".gotmpl", ".gohtml":
		return bonsaigotemplate.NewParser
	}
	return nil
}

// newDockerfileWithBash returns a Dockerfile parser that also tokenizes the
// shell scripts inside RUN, CMD, and ENTRYPOINT instructions.
func newDockerfileWithBash() *bonsai.Parser {
	return bonsaidockerfile.NewParser().With(bonsai.SubParser{
		Match:  func(n *bonsai.Node) bool { return n.Type == "shell_command" },
		Parser: bonsaibash.NewParser(),
	})
}
