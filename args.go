package main

import "strings"

// jjGlobalValueFlags are jj's global flags that take a separate value.
//   - Global flags: May appear either before the subcommand or among a
//     command's args.
//   - Value flags: Have a value provided either as a "=" joined suffix or as
//     the following arg.
var jjGlobalValueFlags = map[string]bool{
	"-R": true, "--repository": true,
	"--at-operation": true, "--at-op": true,
	"--color":       true,
	"--config":      true,
	"--config-file": true,
}

// cmdValueFlags lists, per command, the flags that take a separate value.
//   - Command flags: Must appear among a command's args.
//   - Value flags: Have a value provided either as a "=" joined suffix or as
//     the following arg.
var cmdValueFlags = map[string]map[string]bool{
	"diff": {
		"-r": true, "--revisions": true,
		"-f": true, "--from": true,
		"-t": true, "--to": true,
		"-T": true, "--template": true,
		"--tool": true, "--context": true,
	},
	"show": {
		"-T": true, "--template": true,
		"--tool": true, "--context": true,
	},
	"interdiff": {
		"-f": true, "--from": true,
		"-t": true, "--to": true,
		"--tool": true, "--context": true,
	},
	"file show": {
		"-r": true, "--revision": true,
		"-T": true, "--template": true,
	},
}

func takesValue(subcmd, flag string) bool {
	return jjGlobalValueFlags[flag] || cmdValueFlags[subcmd][flag]
}

// argToken is one parsed CLI token.
type argToken struct {
	raw  []string // original arg string, with the (optional) value string
	name string   // flag name ("-r", "--color"), empty for positional or end
	val  string   // flag value (from "=", a separate token, or an attached short)
	pos  bool     // a positional argument
	end  bool     // the "--" end-of-options marker
}

// tokenize emulates the jj CLI arg grammar.
// It splits args into argTokens, parsing out the different flag types and
// differentiating positional args
func tokenize(subcmd string, args []string) []argToken {
	var toks []argToken
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			toks = append(toks, argToken{raw: []string{a}, end: true})
			for _, p := range args[i+1:] {
				toks = append(toks, argToken{raw: []string{p}, pos: true})
			}
			return toks
		case !strings.HasPrefix(a, "-") || a == "-":
			toks = append(toks, argToken{raw: []string{a}, pos: true})
		case strings.Contains(a, "="):
			k, v, _ := strings.Cut(a, "=")
			toks = append(toks, argToken{raw: []string{a}, name: k, val: v})
		case takesValue(subcmd, a) && i+1 < len(args):
			toks = append(toks, argToken{raw: []string{a, args[i+1]}, name: a, val: args[i+1]})
			i++
		case len(a) > 2 && a[1] != '-' && takesValue(subcmd, a[:2]):
			toks = append(toks, argToken{raw: []string{a}, name: a[:2], val: a[2:]})
		default:
			toks = append(toks, argToken{raw: []string{a}, name: a})
		}
	}
	return toks
}

// parsedArgs indexes a token stream for the queries the modes make.
type parsedArgs struct {
	val  map[string]string // value-flag spelling to its value
	seen map[string]bool   // any flag spelling present
	pos  []string          // positionals (everything after "--" is positional)
}

func parseArgs(subcmd string, args []string) parsedArgs {
	p := parsedArgs{val: map[string]string{}, seen: map[string]bool{}}
	for _, t := range tokenize(subcmd, args) {
		switch {
		case t.pos:
			p.pos = append(p.pos, t.raw[0])
		case t.end:
		default:
			p.seen[t.name] = true
			if t.val != "" {
				p.val[t.name] = t.val
			}
		}
	}
	return p
}

// firstVal returns the value of the first present flag among names, or "".
func (p parsedArgs) firstVal(names ...string) string {
	for _, n := range names {
		if v, ok := p.val[n]; ok {
			return v
		}
	}
	return ""
}

// has reports whether any of names is present.
func (p parsedArgs) has(names ...string) bool {
	for _, n := range names {
		if p.seen[n] {
			return true
		}
	}
	return false
}

// positional returns the first positional argument, or "".
func (p parsedArgs) positional() string {
	if len(p.pos) > 0 {
		return p.pos[0]
	}
	return ""
}

// repo returns the -R/--repository value, or "".
func (p parsedArgs) repo() string { return p.firstVal("-R", "--repository") }

// fetchFlags returns the global flags that the per-file `jj file show`
// fetch must carry so its contents match the displayed diff.
func (p parsedArgs) fetchFlags() []string {
	var out []string
	if v := p.repo(); v != "" {
		out = append(out, "-R", v)
	}
	if v := p.firstVal("--at-operation", "--at-op"); v != "" {
		out = append(out, "--at-operation", v)
	}
	if v := p.firstVal("--config"); v != "" {
		out = append(out, "--config", v)
	}
	if v := p.firstVal("--config-file"); v != "" {
		out = append(out, "--config-file", v)
	}
	return out
}

// newRev returns the revision whose file contents supply syntax context.
// `jj show` takes a positional revset while `jj diff` and `jj interdiff` take
// the new side from --to/-t, then diff's -r/--revisions, else @.
func (p parsedArgs) newRev(subcmd string) string {
	if subcmd == "show" {
		if r := p.positional(); r != "" {
			return r
		}
		return "@"
	}
	if to := p.firstVal("-t", "--to"); to != "" {
		return to
	}
	if r := p.firstVal("-r", "--revisions"); r != "" {
		return r
	}
	return "@"
}

// unhighlightable reports whether the args select an output mode we cannot
// parse into spans, so diffMode streams jj's output verbatim.
func (p parsedArgs) unhighlightable() bool {
	return p.has(
		"--stat", "-s", "--summary", "--name-only", "--types", // summaries
		"--tool",           // output not known in advance
		"-T", "--template", // explicitly customized output
	)
}

// forwardArgs returns args without the color and format flags we override.
func forwardArgs(subcmd string, args []string) []string {
	drop := map[string]bool{"--color": true, "--git": true, "--color-words": true}
	var out []string
	for _, t := range tokenize(subcmd, args) {
		if t.name != "" && drop[t.name] {
			continue
		}
		out = append(out, t.raw...)
	}
	return out
}

// nonPositional returns args with positionals and the "--" marker removed.
func nonPositional(subcmd string, args []string) []string {
	var out []string
	for _, t := range tokenize(subcmd, args) {
		if t.pos || t.end {
			continue
		}
		out = append(out, t.raw...)
	}
	return out
}
