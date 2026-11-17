package genlang

// quotes follows the quoted text of one annotation's arguments, a character at a time.
// The scanner, which looks for the parenthesis that closes the annotation, and the
// argument parser, which splits the arguments on their commas, both ask it which
// characters are text, so the two cannot read a quote differently.
//
// A double quote opens a quoted value and the next one closes it, in every annotation.
// A single quote opens a string and the next one closes it only where the arguments may
// hold an expression of the condition language; anywhere else it is an apostrophe. Each
// kind of quote is text inside the other. A string writes a quote inside it twice, which
// reads here as the string closing and opening again, so it needs no case of its own.
type quotes struct {
	expressions bool
	double      bool
	single      bool
}

// newQuotes returns the quotes of arguments that may hold an expression of the condition
// language, or of arguments that hold none.
func newQuotes(expressions bool) *quotes {
	return &quotes{expressions: expressions}
}

// read takes the next character and reports whether it is quoted text or one of the
// quotes around it: a character the annotation's own structure is not read from.
func (q *quotes) read(c byte) bool {
	switch {
	case c == '"' && !q.single:
		q.double = !q.double

		return true
	case c == '\'' && q.expressions && !q.double:
		q.single = !q.single

		return true
	default:
		return q.double || q.single
	}
}

// unclosed describes the quote still open after the last character read: its name, and
// how the text it quotes is written. ok is false where every quote was closed.
func (q *quotes) unclosed() (name, written string, ok bool) {
	switch {
	case q.double:
		return "double quote", "a quoted value opens with a double quote and closes with the next one; close this annotation's value", true
	case q.single:
		return "single quote", "a string opens with a single quote and closes with the next one; close this annotation's string, and write a quote inside a string twice ('it''s')", true
	default:
		return "", "", false
	}
}
