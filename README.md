# jj-highlight

Syntax-highlighted output for [`jj`](https://github.com/jj-vcs/jj) commands using tree-sitter derived syntax color from [bonsai](https://github.com/msuozzo/bonsai).

## Install

```sh
go install github.com/msuozzo/jj-highlight@latest
```

## Usage

`jj-highlight` wraps a few jj subcommands and colorizes their output:

```sh
jj-highlight diff [...]       # highlight a diff
jj-highlight show [...]       # a revision's description and diff
jj-highlight file show [...]  # a file's contents
jj-highlight interdiff [...]  # an interdiff
```

Flags parse exactly as in a normal jj command.

The cleanest way to use it is a single jj alias so it can be used as a command group:

```toml
[aliases]
hi = ["util", "exec", "--", "jj-highlight"]
```

```sh
jj hi diff                 # like jj diff, highlighted
jj hi diff -r @-           # any flag the subcommand accepts
jj hi diff --git           # git unified diff style
jj hi show @
jj hi file show src/foo.go
```

> **Note:** with the alias, put global flags after `hi` (`jj hi -R repo diff`). Flags before `hi` (`jj -R repo hi diff`) bind to jj, not jj-highlight.

## How it works

`jj-highlight` augments jj's native file and diff machinery with overlaid syntax highlighting.

For the diff commands, `jj-highlight` runs the jj subcommand e.g. `diff` and pipes the output through the highlighter. So rather than producing diffs itself, it drives jj's own diff renderer and re-colors the result. For each changed file it fetches the file contents, parses them with bonsai, and calculates token classes by file byte offset.

Added and context bytes carry their syntax color, added bytes with a faded green background. Removed bytes carry a fixed red underline and no syntax color.

File reads, parsing, and highlighting are asynchronous, so each paints as soon as its contents are ready.

## Custom themes

Set syntax colors in your jj config. The defaults are:

```toml
[colors]
"syntax keyword"  = "magenta"
"syntax string"   = { fg = "#76b078" }
"syntax number"   = "yellow"
"syntax comment"  = { fg = "bright black", italic = true }
"syntax type"     = "cyan"
"syntax field"    = "blue"
"syntax emphasis" = { italic = true }
"syntax strong"   = { bold = true }
"syntax added"    = { bg = "#1e5a2d" }
"syntax removed"  = { fg = "red", underline = true }
"syntax gutter"   = { fg = "bright black", dim = true }
```

Color values accept jj's notation: named colors (`red`, `bright red`, `default`) or hex (`#rrggbb`). Any key can also carry attribute-only styling (`bold`, `italic`, `dim`, `underline`, `reverse`) with no color, which is how the defaults for `syntax emphasis` and `syntax strong` work.

## Diff formats

| Format                                          | Behavior                                                            |
| ----------------------------------------------- | ------------------------------------------------------------------- |
| `color-words`                                   | Default. Inline old/new spans parsed into per-byte highlighting.    |
| `--git`                                         | Unified diff with `+`/`-`/space prefix, parsed into per-line spans. |
| `--stat`, `--summary`, `--name-only`, `--types` | Stream through unchanged.                                           |
| `-T`/`--template`                               | Custom per-entry output. Streams through unchanged.                 |
| `--tool`                                        | An external diff tool's output. Streams through unchanged.          |
| `--color=never`                                 | Streams through. An explicit refusal of color is respected.         |

## Supported languages

| Language     | Files / extensions                            |
| ------------ | --------------------------------------------- |
| Go           | `.go`                                         |
| Python       | `.py`                                         |
| Markdown     | `.md`, `.markdown`                            |
| YAML         | `.yaml`, `.yml`                               |
| Dockerfile   | `Dockerfile`, `Containerfile`, `*.dockerfile` |
| Bash         | `.sh`, `.bash`, `.zsh`                        |
| Go templates | `.tmpl`, `.gotmpl`, `.gohtml`                 |

Markdown wires up both the block and inline grammars, so structure and inline content (links, code spans, emphasis) are tokenized together. Dockerfile uses a similar composition for shell code inside `RUN`, `CMD`, and `ENTRYPOINT`, so a step like `RUN go build -o myapp ./...` shows the dockerfile keywords and the bash command in their respective colors. Other extensions render with diff colors only.

## Pager

Matches jj's default: `less -FRX`. Override via `$PAGER`. Paging is skipped when `--no-pager` is passed or when stdout is not a TTY.

## Alternative Usage: As a jj diff formatter

`jj-highlight` can also register as jj's diff formatter, so every diff-bearing command (`jj diff`, `jj show`, `jj log -p`, ...) is highlighted with no alias:

```toml
[ui]
diff-formatter = ["jj-highlight", "util", "diff-formatter", "$left", "$right", "$width"]
```

The trade for that native integration is a couple of downsides, both because jj runs a formatter against two bare file trees with no link back to the source repo:

- **~200 ms startup latency per invocation.** Each call builds a throwaway repo to reuse jj diffing machinery.
- **Repo-local config is not applied.** The throwaway repo inherits global and user jj config but NOT the repo-local config. For instance, a custom `[colors]` palette, a raised `snapshot.max-new-file-size`, etc. are silently dropped.

The `jj hi` command group alias has neither downside in speed or correctness so it should be preferred.

## Related projects

- [**difftastic**](https://github.com/Wilfred/difftastic): A structural diff tool that also uses tree-sitter's parsed syntax trees to improve diff clarity. Notably _replaces_ jj's diff look and feel with its own, while jj-highlight keeps jj's diff style while adding syntax color on top.
- [**delta**](https://github.com/dandavison/delta): A syntax-highlighting pager for git-style diffs supporting side-by-side view and line numbers. Notably works on jj's `--git` output but doesn't support jj's default color-words format.

## License

MIT. See [LICENSE](LICENSE).
