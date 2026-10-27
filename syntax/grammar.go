package syntax

import (
	"path/filepath"
	"strings"

	"github.com/msuozzo/bonsai"
	bonsaibash "github.com/msuozzo/bonsai/bonsai-bash"
	bonsaic "github.com/msuozzo/bonsai/bonsai-c"
	bonsaidockerfile "github.com/msuozzo/bonsai/bonsai-dockerfile"
	bonsaigo "github.com/msuozzo/bonsai/bonsai-go"
	bonsaigotemplate "github.com/msuozzo/bonsai/bonsai-gotemplate"
	bonsaijava "github.com/msuozzo/bonsai/bonsai-java"
	bonsaijavascript "github.com/msuozzo/bonsai/bonsai-javascript"
	bonsaimarkdown "github.com/msuozzo/bonsai/bonsai-markdown"
	bonsaipython "github.com/msuozzo/bonsai/bonsai-python"
	bonsairuby "github.com/msuozzo/bonsai/bonsai-ruby"
	bonsairust "github.com/msuozzo/bonsai/bonsai-rust"
	bonsaiterraform "github.com/msuozzo/bonsai/bonsai-terraform"
	bonsaitsx "github.com/msuozzo/bonsai/bonsai-tsx"
	bonsaitypescript "github.com/msuozzo/bonsai/bonsai-typescript"
	bonsaiyaml "github.com/msuozzo/bonsai/bonsai-yaml"
)

// Supported reports whether path names a language with a grammar.
func Supported(path string) bool { return parserConstructor(path) != nil }

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
	case base == "gemfile", base == "rakefile", base == "podfile", base == "brewfile":
		return bonsairuby.NewParser
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
	case ".tf", ".tfvars", ".hcl":
		return bonsaiterraform.NewParser
	case ".rs":
		return bonsairust.NewParser
	case ".java":
		return bonsaijava.NewParser
	case ".rb", ".rake", ".gemspec", ".ru":
		return bonsairuby.NewParser
	// The JavaScript grammar covers JSX, so .jsx routes here rather than
	// to the tsx dialect.
	case ".js", ".mjs", ".cjs", ".jsx":
		return bonsaijavascript.NewParser
	case ".ts", ".mts", ".cts":
		return bonsaitypescript.NewParser
	case ".tsx":
		return bonsaitsx.NewParser
	// C only: the grammar rejects enough C++ (templates, namespaces) that
	// .cc/.cpp would parse worse than staying uncolored. .h is claimed
	// anyway since C headers outnumber C++ ones in practice.
	case ".c", ".h":
		return bonsaic.NewParser
	}
	return nil
}

// newDockerfileWithBash returns a Dockerfile parser that also tokenizes the
// shell scripts inside RUN, CMD, and ENTRYPOINT instructions.
func newDockerfileWithBash() *bonsai.Parser {
	return bonsaidockerfile.NewParser().With(bonsai.SubParser{
		Match:  func(n *bonsai.Node) bool { return n.Kind == bonsaidockerfile.KindShellCommand },
		Parser: bonsaibash.NewParser(),
	})
}
