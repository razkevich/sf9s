// Package soql provides just enough SOQL awareness to drive editor
// completion: which object the statement targets, and what kind of token the
// cursor sits on. It deliberately does not parse the full grammar — a wrong
// guess must degrade to "no suggestions", never to a wrong insertion.
package soql

import (
	"strings"
	"unicode"
)

// Clause is the region of a statement the cursor is in.
type Clause int

const (
	// ClauseUnknown means we can't tell — offer nothing.
	ClauseUnknown Clause = iota
	// ClauseSelect is the field list between SELECT and FROM.
	ClauseSelect
	// ClauseFrom is the object position after FROM.
	ClauseFrom
	// ClauseFilter is WHERE / ORDER BY / GROUP BY / HAVING, where field
	// names are also valid.
	ClauseFilter
)

func (c Clause) String() string {
	switch c {
	case ClauseSelect:
		return "select"
	case ClauseFrom:
		return "from"
	case ClauseFilter:
		return "filter"
	default:
		return "unknown"
	}
}

// Context describes the completion site under the cursor.
type Context struct {
	Clause Clause
	// Object is the FROM target of the statement the cursor belongs to
	// ("" when not yet typed).
	Object string
	// Prefix is the partial token being typed, e.g. "Nam" or "Owner.Na".
	Prefix string
	// Start is the byte offset in the input where Prefix begins; replacing
	// input[Start:cursor] with a candidate performs the completion.
	Start int
}

// RelationshipPath splits a dotted prefix into its relationship segments and
// the trailing partial field: "Owner.Manager.Na" → ["Owner","Manager"], "Na".
func (c Context) RelationshipPath() ([]string, string) {
	if i := strings.LastIndex(c.Prefix, "."); i >= 0 {
		return strings.Split(c.Prefix[:i], "."), c.Prefix[i+1:]
	}
	return nil, c.Prefix
}

func isWordByte(b byte) bool {
	return b == '_' || b == '.' || b == '$' ||
		(b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// Analyze inspects a statement and a cursor offset (in bytes) and returns the
// completion context at that point.
func Analyze(input string, cursor int) Context {
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(input) {
		cursor = len(input)
	}

	start := cursor
	for start > 0 && isWordByte(input[start-1]) {
		start--
	}
	ctx := Context{Prefix: input[start:cursor], Start: start}

	head := input[:start]
	if insideLiteral(head) {
		// Typing inside a string literal: nothing here is a field or object.
		return Context{Prefix: ctx.Prefix, Start: ctx.Start}
	}
	ctx.Clause = clauseAt(head)
	ctx.Object = objectFor(input, start)
	return ctx
}

// insideLiteral reports whether the text ends inside an unterminated
// single-quoted literal.
func insideLiteral(head string) bool {
	inLiteral := false
	for i := 0; i < len(head); i++ {
		switch {
		case inLiteral && head[i] == '\\' && i+1 < len(head):
			i++
		case head[i] == '\'':
			inLiteral = !inLiteral
		}
	}
	return inLiteral
}

// clauseAt determines the clause from the keywords preceding the cursor,
// scoped to the innermost unclosed parenthesis so subqueries and function
// arguments resolve on their own terms.
func clauseAt(head string) Clause {
	scope := innermostScope(head)
	last := ClauseUnknown
	for _, kw := range keywordsIn(scope) {
		switch kw {
		case "select":
			last = ClauseSelect
		case "from":
			last = ClauseFrom
		case "where", "and", "or", "not", "order", "group", "having", "by":
			last = ClauseFilter
		case "limit", "offset":
			last = ClauseUnknown
		}
	}
	// A word immediately after FROM's object means we've moved past it.
	if last == ClauseFrom && trailingObjectComplete(scope) {
		return ClauseFilter
	}
	return last
}

// innermostScope returns the text whose keywords govern the cursor: the body
// of an unclosed subquery if we're inside one, otherwise the enclosing
// statement with completed (...) groups removed so a closed subquery can't
// leak its keywords or object outward.
func innermostScope(head string) string {
	if tail, open := unclosedTail(head); open {
		for _, kw := range keywordsIn(tail) {
			if kw == "select" {
				return tail
			}
		}
	}
	return stripBalanced(head)
}

// unclosedTail returns the text after the last unmatched '('.
func unclosedTail(s string) (string, bool) {
	var opens []int
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			opens = append(opens, i)
		case ')':
			if len(opens) > 0 {
				opens = opens[:len(opens)-1]
			}
		}
	}
	if len(opens) == 0 {
		return "", false
	}
	return s[opens[len(opens)-1]+1:], true
}

// stripBalanced replaces every complete (...) group with a space.
func stripBalanced(s string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			if depth == 0 {
				b.WriteByte(' ')
			}
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteByte(s[i])
			}
		}
	}
	return b.String()
}

// cutAtUnmatchedClose truncates s at the first ')' that closes a group opened
// before s began, keeping lookahead inside the cursor's own scope.
func cutAtUnmatchedClose(s string) string {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth == 0 {
				return s[:i]
			}
			depth--
		}
	}
	return s
}

func keywordsIn(s string) []string {
	var out []string
	for _, tok := range tokenize(s) {
		lower := strings.ToLower(tok)
		switch lower {
		case "select", "from", "where", "and", "or", "not",
			"order", "group", "having", "by", "limit", "offset":
			out = append(out, lower)
		}
	}
	return out
}

func tokenize(s string) []string {
	return strings.FieldsFunc(blankLiterals(s), func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == '(' || r == ')' || r == '\''
	})
}

// blankLiterals replaces the contents of single-quoted string literals with
// spaces, so a keyword inside a literal ("imported from Acme") can never be
// read as SOQL. An unterminated literal blanks to end of input, which is
// exactly right: the cursor is inside a string the user is still typing.
func blankLiterals(s string) string {
	out := []byte(s)
	inLiteral := false
	for i := 0; i < len(out); i++ {
		switch {
		case inLiteral && out[i] == '\\' && i+1 < len(out):
			out[i], out[i+1] = ' ', ' ' // escaped char, e.g. \'
			i++
		case out[i] == '\'':
			inLiteral = !inLiteral
		case inLiteral:
			out[i] = ' '
		}
	}
	return string(out)
}

// trailingObjectComplete reports whether the FROM object has been typed and
// followed by whitespace, i.e. the cursor is past it.
func trailingObjectComplete(scope string) bool {
	toks := tokenize(scope)
	for i := len(toks) - 1; i >= 0; i-- {
		if strings.EqualFold(toks[i], "from") {
			// tokens after FROM, and the scope ends with a separator
			return len(toks)-i-1 >= 1
		}
	}
	return false
}

// objectFor finds the FROM object governing the position at offset. The FROM
// clause usually sits after the cursor, since people type SELECT first.
func objectFor(input string, offset int) string {
	scope := innermostScope(input[:offset])
	if obj := fromObject(scope); obj != "" {
		return obj
	}
	rest := stripBalanced(cutAtUnmatchedClose(input[offset:]))
	return fromObject(scope + " " + rest)
}

func fromObject(s string) string {
	toks := tokenize(s)
	for i := 0; i < len(toks); i++ {
		if strings.EqualFold(toks[i], "from") && i+1 < len(toks) {
			candidate := toks[i+1]
			if isIdentifier(candidate) {
				return candidate
			}
		}
	}
	return ""
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isWordByte(s[i]) {
			return false
		}
	}
	r := rune(s[0])
	return !unicode.IsDigit(r) && s[0] != '.'
}

// Candidate is one completion suggestion.
type Candidate struct {
	// Text is inserted in place of the typed prefix.
	Text string
	// Detail is shown next to it (type, label, "object", …).
	Detail string
}

// Filter returns the candidates matching partial, prefix matches first then
// substring matches, each group alphabetical. Matching is case-insensitive.
func Filter(candidates []Candidate, partial string) []Candidate {
	needle := strings.ToLower(partial)
	if needle == "" {
		return candidates
	}
	var prefixed, contained []Candidate
	for _, c := range candidates {
		lower := strings.ToLower(c.Text)
		switch {
		case strings.HasPrefix(lower, needle):
			prefixed = append(prefixed, c)
		case strings.Contains(lower, needle):
			contained = append(contained, c)
		}
	}
	return append(prefixed, contained...)
}
