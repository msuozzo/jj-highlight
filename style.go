package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Style is one ANSI style: optional FG, BG, and attribute flags.
// FG/BG accept jj's notation: named colors ("red", "bright red",
// "default"), or hex codes ("#rrggbb"). Empty string means "no
// change."
type Style struct {
	FG, BG    string
	Bold      bool
	Dim       bool
	Italic    bool
	Underline bool
	Reverse   bool
}

// Empty reports whether the Style has any non-default attribute.
func (s Style) Empty() bool {
	return s.FG == "" && s.BG == "" && !s.Bold && !s.Dim && !s.Italic && !s.Underline && !s.Reverse
}

// ANSI returns the escape sequence that enables this Style.
func (s Style) ANSI() string {
	var parts []string
	if s.Bold {
		parts = append(parts, "1")
	}
	if s.Dim {
		parts = append(parts, "2")
	}
	if s.Italic {
		parts = append(parts, "3")
	}
	if s.Underline {
		parts = append(parts, "4")
	}
	if s.Reverse {
		parts = append(parts, "7")
	}
	if c := colorANSI(s.FG, true); c != "" {
		parts = append(parts, c)
	}
	if c := colorANSI(s.BG, false); c != "" {
		parts = append(parts, c)
	}
	if len(parts) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

// Reset returns the escape sequence that selectively undoes this
// Style. Only the attributes set are reset, so nested styles compose.
func (s Style) Reset() string {
	var parts []string
	if s.Bold || s.Dim {
		parts = append(parts, "22")
	}
	if s.Italic {
		parts = append(parts, "23")
	}
	if s.Underline {
		parts = append(parts, "24")
	}
	if s.Reverse {
		parts = append(parts, "27")
	}
	if s.FG != "" {
		parts = append(parts, "39")
	}
	if s.BG != "" {
		parts = append(parts, "49")
	}
	if len(parts) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

// colorANSI converts a jj color name or "#rrggbb" hex to an ANSI CSI
// parameter. Returns "" for unknown / empty names.
func colorANSI(name string, fg bool) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return ""
	}
	name = strings.ReplaceAll(name, "_", " ")
	if strings.HasPrefix(name, "#") {
		return hexColorANSI(name, fg)
	}
	if code, ok := basicColors[name]; ok {
		if !fg {
			code += 10
		}
		return strconv.Itoa(code)
	}
	return ""
}

var basicColors = map[string]int{
	"black": 30, "red": 31, "green": 32, "yellow": 33,
	"blue": 34, "magenta": 35, "cyan": 36, "white": 37,
	"default":      39,
	"bright black": 90, "bright red": 91, "bright green": 92, "bright yellow": 93,
	"bright blue": 94, "bright magenta": 95, "bright cyan": 96, "bright white": 97,
}

func hexColorANSI(hex string, fg bool) string {
	if len(hex) != 7 || hex[0] != '#' {
		return ""
	}
	r, e1 := strconv.ParseUint(hex[1:3], 16, 8)
	g, e2 := strconv.ParseUint(hex[3:5], 16, 8)
	b, e3 := strconv.ParseUint(hex[5:7], 16, 8)
	if e1 != nil || e2 != nil || e3 != nil {
		return ""
	}
	if fg {
		return fmt.Sprintf("38;2;%d;%d;%d", r, g, b)
	}
	return fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
}

// Theme is the resolved palette used during render. Defaults come
// from defaultTheme. jj's color config overrides any key we recognize.
type Theme struct {
	Gutter   Style
	Comment  Style
	String   Style
	Number   Style
	Keyword  Style
	Type     Style
	Field    Style
	Emphasis Style // markdown *italic*
	Strong   Style // markdown **bold**
	Added    Style // wraps KNew spans (BG-only by default so the syntax FG shows through)
	Removed  Style // replaces KOld styling entirely
}

func (t Theme) styleFor(c tokenClass) Style {
	switch c {
	case cComment:
		return t.Comment
	case cString:
		return t.String
	case cNumber:
		return t.Number
	case cKeyword:
		return t.Keyword
	case cType:
		return t.Type
	case cField:
		return t.Field
	case cEmphasis:
		return t.Emphasis
	case cStrong:
		return t.Strong
	}
	return Style{}
}

// defaultTheme is the baked-in palette used when jj config is absent
// or doesn't set our keys.
var defaultTheme = Theme{
	Gutter:   Style{FG: "#888888", Dim: true},
	Comment:  Style{FG: "#888888"},
	String:   Style{FG: "#87af87"},
	Number:   Style{FG: "#d7af87"},
	Keyword:  Style{FG: "#d787d7"},
	Type:     Style{FG: "#87afff"},
	Field:    Style{FG: "#afd7d7"},
	Emphasis: Style{Italic: true},
	Strong:   Style{Bold: true},
	Added:    Style{BG: "#1e5a2d"},
	Removed:  Style{FG: "#d75f5f", Underline: true},
}

// loadTheme runs `jj config list --include-defaults colors` and merges
// any keys we recognize into the default palette. Returns the default
// unchanged on any error (jj missing, not in a repo, parse failure).
//
// Config keys we recognize (set in your jj config, e.g.
// `~/.config/jj/config.toml`):
//
//	[colors]
//	"syntax keyword"  = "magenta"
//	"syntax string"   = { fg = "#76b078" }
//	"syntax number"   = "yellow"
//	"syntax comment"  = { fg = "bright black", italic = true }
//	"syntax type"     = "cyan"
//	"syntax field"    = "blue"
//	"syntax emphasis" = { italic = true }
//	"syntax strong"   = { bold = true }
//	"syntax added"    = { bg = "#1e5a2d" }
//	"syntax removed"  = { fg = "red", underline = true }
//	"syntax gutter"   = { fg = "bright black", dim = true }
//
// Both jj's value forms work:
//
//	"syntax keyword" = "bright magenta bold"           # string form
//	"syntax keyword".fg = "bright magenta"             # dotted attrs
//	"syntax keyword".bold = true
func loadTheme() Theme {
	out, err := exec.Command("jj", "config", "list", "--include-defaults", "colors").Output()
	if err != nil {
		return defaultTheme
	}
	return applyOverrides(defaultTheme, parseStyleConfig(string(out)))
}

func applyOverrides(t Theme, m map[string]Style) Theme {
	for k, s := range m {
		switch k {
		case "syntax keyword":
			t.Keyword = s
		case "syntax string":
			t.String = s
		case "syntax number":
			t.Number = s
		case "syntax comment":
			t.Comment = s
		case "syntax type":
			t.Type = s
		case "syntax field":
			t.Field = s
		case "syntax emphasis":
			t.Emphasis = s
		case "syntax strong":
			t.Strong = s
		case "syntax added":
			t.Added = s
		case "syntax removed":
			t.Removed = s
		case "syntax gutter":
			t.Gutter = s
		}
	}
	return t
}

// parseStyleConfig parses output from `jj config list colors`. Lines
// look like one of:
//
//	colors.name = "value"
//	colors."multi word name" = "value"
//	colors.name.attr = "value"
//	colors."multi word name".attr = true
//
// Values are either a string-form ("fg [on bg] [modifiers]") or one
// attribute (fg, bg, bold, underline, italic, dim, reverse). Multiple
// lines for the same key merge into one Style.
func parseStyleConfig(out string) map[string]Style {
	styles := make(map[string]Style)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "colors.") {
			continue
		}
		key, attr, value, ok := parseConfigLine(line)
		if !ok {
			continue
		}
		s := styles[key]
		if attr == "" {
			s = mergeStyleString(s, value)
		} else {
			s = setStyleAttr(s, attr, value)
		}
		styles[key] = s
	}
	return styles
}

// parseConfigLine breaks one "colors.X[.attr] = value" line into its
// parts. Returns ok=false on lines we can't recognize.
func parseConfigLine(line string) (key, attr, value string, ok bool) {
	s := line[len("colors."):]
	// Key: either "quoted multi word" or a bare identifier ending at
	// the next '.', '=', or space.
	if strings.HasPrefix(s, "\"") {
		end := strings.IndexByte(s[1:], '"')
		if end < 0 {
			return
		}
		key = s[1 : 1+end]
		s = s[1+end+1:]
	} else {
		end := strings.IndexAny(s, ".= ")
		if end < 0 {
			return
		}
		key = s[:end]
		s = s[end:]
	}
	s = strings.TrimLeft(s, " ")
	// Optional ".attr"
	if strings.HasPrefix(s, ".") {
		s = s[1:]
		end := strings.IndexAny(s, " =")
		if end < 0 {
			return
		}
		attr = s[:end]
		s = s[end:]
	}
	s = strings.TrimLeft(s, " ")
	if !strings.HasPrefix(s, "=") {
		return
	}
	value = strings.TrimSpace(s[1:])
	ok = true
	return
}

func setStyleAttr(s Style, attr, value string) Style {
	value = strings.Trim(value, "\"")
	switch attr {
	case "fg":
		s.FG = value
	case "bg":
		s.BG = value
	case "bold":
		s.Bold = value == "true"
	case "dim":
		s.Dim = value == "true"
	case "italic":
		s.Italic = value == "true"
	case "underline", "underlined":
		s.Underline = value == "true"
	case "reverse":
		s.Reverse = value == "true"
	}
	return s
}

// mergeStyleString parses jj's string-form style ("red bold", "bright
// red on yellow underline") and merges into s. Handles two-word colors
// ("bright X") and the "on" separator for BG.
func mergeStyleString(s Style, value string) Style {
	value = strings.Trim(value, "\"")
	tokens := strings.Fields(value)
	nextIsBG := false
	assign := func(name string) {
		if nextIsBG {
			s.BG = name
			nextIsBG = false
		} else if s.FG == "" {
			s.FG = name
		} else {
			s.BG = name
		}
	}
	for i := 0; i < len(tokens); i++ {
		tok := strings.ToLower(tokens[i])
		switch tok {
		case "bold":
			s.Bold = true
		case "dim":
			s.Dim = true
		case "italic":
			s.Italic = true
		case "underline", "underlined":
			s.Underline = true
		case "reverse":
			s.Reverse = true
		case "on":
			nextIsBG = true
		case "bright":
			if i+1 < len(tokens) {
				assign("bright " + strings.ToLower(tokens[i+1]))
				i++
			}
		default:
			assign(tok)
		}
	}
	return s
}
