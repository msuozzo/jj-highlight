// Package syntax classifies the bytes of a source file by its tree-sitter
// parse, for highlighting.
package syntax

import (
	"strings"

	"github.com/msuozzo/bonsai"
)

// Class is the highlight class of a run of source bytes.
type Class int

const (
	None Class = iota
	Comment
	String
	Number
	Keyword
	Type
	Field
	Emphasis // markdown *italic* and _italic_
	Strong   // markdown **bold** and __bold__
	Conflict // jj conflict markers, labels, and diff prefixes
	Todo     // TODO / NOTE label opening a comment
	Fixme    // FIXME / XXX / HACK / BUG label opening a comment
)

// classifyContainer covers whole-range node kinds where we want the
// color applied across the entire span, rather than per leaf. For
// strings and comments that means quotes and `//` / `/*` markers stay
// in the same color. For markdown that means headings and code blocks
// read as solid blocks of color. Returning non-None tells the
// collector to emit one token and skip recursion into children.
func classifyContainer(kind string, named bool) Class {
	if !named {
		return None
	}
	switch kind {
	// Strings. Character literals are containers rather than leaves so
	// the quotes stay colored with the character (C's inner `character`
	// leaf, Rust's `char_literal`, Java's `character_literal`).
	case "string", "string_literal", "raw_string_literal",
		"interpreted_string_literal", "byte_string_literal",
		"rune_literal", "concatenated_string", "string_lit",
		"char_literal", "character_literal":
		return String
	// Comments.
	case "comment", "line_comment", "block_comment":
		return Comment
	// Markdown block-grammar containers.
	case "atx_heading", "setext_heading":
		return Keyword
	case "fenced_code_block", "indented_code_block":
		return String
	case "block_quote":
		return Comment
	case "link_reference_definition":
		return Type
	// Markdown GFM tables. Header row gets keyword color to stand out
	// over body rows. The `|---|---|` delimiter row reads as structural
	// noise, so it gets comment color. Body rows stay default.
	case "pipe_table_header":
		return Keyword
	case "pipe_table_delimiter_row":
		return Comment
	// Markdown inline-grammar containers. The inline parser fires
	// inside `inline` leaves of the block tree (see collectTokens),
	// so these only apply when we have a working inline parser.
	case "code_span":
		return String
	case "inline_link", "image", "link",
		"shortcut_link", "full_reference_link", "collapsed_reference_link",
		"uri_autolink", "email_autolink":
		return Field
	case "emphasis":
		return Emphasis
	case "strong_emphasis":
		return Strong
	// YAML quoted and block scalars. plain_scalar (unquoted) is
	// deliberately left alone since it covers both keys and values.
	case "double_quote_scalar", "single_quote_scalar", "block_scalar":
		return String
	// Dockerfile containers. JSON strings within instructions
	// (e.g. CMD ["./app"]) get the string color. `expansion` is the
	// ${VAR} / $VAR form and reads as a field reference.
	case "json_string":
		return String
	case "expansion":
		return Field
	// Go template field references (.Name, .User.Name). Whole-range
	// classification keeps the leading `.` colored alongside the
	// identifier. Action keywords (if, range, define, end) come
	// through the lowercase-keyword path automatically.
	case "field":
		return Field
	// Bash. simple_expansion is `$VAR`. command_name is the verb at the
	// start of a command (echo, cat, grep). The `string` container
	// already covers double-quoted strings via the strings group above.
	case "simple_expansion":
		return Field
	case "command_name":
		return Type
	// Terraform/HCL. quoted_template (an interpolated string) is
	// deliberately absent so `${...}` keeps expression coloring. bool_lit
	// wraps an anonymous true/false that would otherwise classify as a
	// keyword.
	case "heredoc_start":
		return String
	case "bool_lit", "null_lit":
		return Number
	// Builtin types in Java and TypeScript. These wrap an anonymous
	// token, so without a container rule they would fall to the
	// all-letter keyword path instead of matching the type color that
	// C and Rust primitives get.
	case "void_type", "integral_type", "floating_point_type",
		"boolean_type", "predefined_type":
		return Type
	}
	if strings.HasSuffix(kind, "_string") || strings.HasSuffix(kind, "_string_literal") {
		return String
	}
	return None
}

// classify maps a leaf tree-sitter node to a token class. One
// classifier handles every grammar: we lump synonyms across tree-
// sitter-go / -python / -markdown / -yaml / -bash / -dockerfile /
// -gotemplate node-kind names, and treat any all-letter anonymous
// token as a keyword. The all-letter check covers both lowercase
// (Go's `func`, Python's `def`) and uppercase (Dockerfile's `FROM`,
// `RUN`) without per-language enumeration.
func classify(kind string, named bool) Class {
	if !named {
		// C spells its preprocessor directives `#include`, `#define`,
		// and so on. Every other grammar's `#` token is punctuation
		// followed by a non-letter, so it still falls through.
		if t := strings.TrimPrefix(kind, "#"); len(t) >= 2 && isAllAlpha(t) {
			return Keyword
		}
		return None
	}
	switch kind {
	case "true", "false", "none", "nil":
		return Number
	case "number":
		return Number
	case "type_identifier", "package_identifier", "primitive_type":
		return Type
	case "field_identifier":
		return Field
	// Markdown leaves. The block grammar emits these as standalone
	// tokens that wouldn't be reached by the container path.
	case "thematic_break",
		"list_marker_minus", "list_marker_plus", "list_marker_star",
		"list_marker_dot", "list_marker_parenthesis":
		return Keyword
	case "info_string", "language":
		return Type
	// YAML leaves. Quoted-string scalars are handled as containers
	// above. Unquoted text (the inner `string_scalar` leaves that the
	// block grammar emits for both keys and values) is left default
	// so structure stays visible against the noise. Only the typed
	// scalars that read like literals get a color.
	case "integer_scalar", "float_scalar", "boolean_scalar", "null_scalar":
		return Number
	case "anchor_name", "alias_name":
		return Field
	// Dockerfile leaves. Image names / aliases read like types,
	// paths and unquoted strings like literals, and ports like
	// numbers. Variable references get the field color so they pop.
	case "image_name", "image_alias":
		return Type
	case "path", "unquoted_string":
		return String
	case "expose_port":
		return Number
	case "variable":
		return Field
	// Bash leaves. variable_name is the LHS of `NAME=value`, the
	// content inside ${...}, etc. test_operator covers `-f`, `-d`,
	// `==`, etc. inside [[ ... ]]. Bare `word` and `string_content`
	// stay default so command arguments aren't recolored.
	case "variable_name":
		return Field
	case "test_operator":
		return Keyword
	// Terraform/HCL leaves, reached inside interpolated strings and
	// heredocs. The `${`/`%{`/`}` frames get field color so embedded
	// expressions pop out of the surrounding string.
	case "template_literal", "quoted_template_start", "quoted_template_end",
		"heredoc_identifier":
		return String
	case "template_interpolation_start", "template_interpolation_end",
		"template_directive_start", "template_directive_end":
		return Field
	case "numeric_lit":
		return Number
	// C, Kotlin, and Groovy spell their numbers this way, which the
	// substring fallback below doesn't reach.
	case "number_literal":
		return Number
	// The receiver keywords parse as named nodes, so the anonymous
	// all-letter keyword path never sees them.
	case "this", "self", "super", "mutable_specifier":
		return Keyword
	// JavaScript / TypeScript / TSX member names, matching Go's
	// field_identifier. Covers member access, class members, object
	// literal keys, and JSX attribute names.
	case "property_identifier", "private_property_identifier",
		"shorthand_property_identifier", "shorthand_property_identifier_pattern":
		return Field
	// Ruby. Constants (which include class and module names) read as
	// types; the sigil variables and hash keys read as fields. A symbol
	// is an interned string literal, so it takes the string color.
	case "constant":
		return Type
	case "instance_variable", "class_variable", "global_variable", "hash_key_symbol":
		return Field
	case "simple_symbol", "delimited_symbol":
		return String
	}
	if strings.Contains(kind, "integer") || strings.Contains(kind, "int_literal") ||
		strings.Contains(kind, "float_literal") || kind == "float" || kind == "number" {
		return Number
	}
	return None
}

func isAllAlpha(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

// classifyContext classifies nodes whose color depends on their position
// in the tree, not the node kind alone. A non-None result means "emit
// one token for the whole range and skip the children." Checked before
// classifyContainer so structural rules win over type-name rules.
func classifyContext(n *bonsai.Node) Class {
	if !n.Named {
		return None
	}
	parentIs := func(kind string) bool { return n.Parent != nil && n.Parent.Kind == kind }
	switch n.Kind {
	// YAML plain_scalar: the block grammar uses it for both keys
	// and values (and for sequence elements, top-level scalars,
	// etc.). The parent flow_node carries Field == "key" only for
	// the key half of a block_mapping_pair. Anything else is a
	// value and reads like a string.
	case "plain_scalar":
		if n.Parent == nil || n.Parent.Field != "key" {
			return String
		}
	// Terraform/HCL uses one identifier node for block types, attribute
	// names, and function names, so position decides the color. The
	// rules below it cover the other grammars that reuse the bare
	// identifier node for a named role.
	case "identifier":
		switch {
		// Java, JavaScript, and Python type declarations, matching Go's
		// type_identifier.
		case n.Field == "name" && (parentIs("class_declaration") ||
			parentIs("class_definition")):
			return Type
		// Ruby is method calls all the way down, so only receiverless
		// ones get the color. That covers DSL verbs (`gem`, `task`,
		// `attr_reader`) while leaving `foo.bar.baz` chains alone.
		case n.Field == "method" && parentIs("call") &&
			n.Parent.ChildByField("receiver") == nil:
			return Type
		}
		if n.Field != "" {
			return None
		}
		switch {
		case parentIs("block"):
			return Keyword
		case parentIs("function_call"):
			return Type
		// Rust names the inside of `#[...]` an `attribute` too, so the
		// HCL rule needs the grandparent to tell them apart. Rust
		// attribute names stay default, like Python decorators.
		case parentIs("attribute"):
			if gp := n.Parent.Parent; gp != nil && gp.Kind == "attribute_item" {
				return None
			}
			return Field
		}
	// HCL block labels: the first names a type (`resource
	// "aws_s3_bucket" "b"`), later ones fall through to plain string.
	case "string_lit":
		if parentIs("block") {
			for _, c := range n.Parent.Children {
				if c.Kind == "string_lit" {
					if c == n {
						return Type
					}
					break
				}
			}
		}
	// HCL object keys ({ env = ... }) match the attribute-name color.
	case "expression":
		if n.Field == "key" && parentIs("object_elem") {
			return Field
		}
	}
	return None
}
