package condition

import (
	"fmt"
)

// A subject set's definition (the @subjectSet annotation) writes two optional
// parts in this language, both over the columns of the set's own row: who:,
// the tests that make a row the requester's, and where:, the filter that says
// which of those rows count. Both are parsed by Parse, with the grammar every
// condition has, and then held to the shape their part takes: ParseWho and
// ParseWhere are the one statement of those shapes, read by the generator when
// it compiles the annotation and by the resource layer when it loads what the
// generator wrote.
//
// The names in both parts are the names the definition writes for the row's
// columns. Resolving a name onto a column, typing a literal against it, and
// finding the set another set builds on are the callers' work, as they are for
// a grant's condition: this package never sees a schema.

// whoShape says what a who: part is made of, for the refusals.
const whoShape = "each who: test is written Column = subject or Column IN subject.<set>, and several are joined by OR"

// whereShape says what a where: part is made of, for the refusals.
const whereShape = "a where: test compares a column of the row to a literal or to now"

// WhoTest is one test of a subject set's who: part. It ties a column of the
// set's row to the requester: the column holds the requester, or the column
// holds a value of another subject set of the requester's.
type WhoTest struct {
	// Column is the row's column, by the name the definition writes for it.
	Column string

	// Set is the subject set the column is tested for membership in, the
	// `Column IN subject.<Set>` form. It is empty for `Column = subject`.
	Set string
}

// Expr returns the test as the condition it is written as.
func (t WhoTest) Expr() Expr {
	left := Ref{Name: t.Column}
	if t.Set == "" {
		return Comparison{Left: left, Op: Eq, Right: Subject{}}
	}

	return In{Left: left, SubjectSet: t.Set}
}

// WhoExpr returns a who: part as one condition: its tests joined by OR. A row
// is the requester's when the condition is TRUE of it, so the tests read
// through Evaluate and render through String as any condition does. It needs
// at least one test.
func WhoExpr(tests []WhoTest) Expr {
	operands := make([]Expr, 0, len(tests))
	for _, test := range tests {
		operands = append(operands, test.Expr())
	}

	return rebuildLogic(operands, false)
}

// ParseWho compiles a subject set's who: part: one test or several joined by
// OR, each tying a column of the row to the requester, `Column = subject` or
// `Column IN subject.<set>`. Anything else is refused, since a who: that does
// not tie the row to the requester would make the set everyone's rows: a
// comparison to a literal, a negated test, tests joined by AND, a test that
// does not name the requester.
func ParseWho(source string) ([]WhoTest, error) {
	expr, err := Parse(source)
	if err != nil {
		return nil, fmt.Errorf("who: %w; %s", err, whoShape)
	}

	operands := []Expr{expr}
	if or, ok := expr.(Or); ok {
		operands = or.Operands
	}

	tests := make([]WhoTest, 0, len(operands))
	for _, operand := range operands {
		test, ok := whoTest(operand)
		if !ok {
			return nil, fmt.Errorf("who: %q does not tie the row to the requester; %s, and a test over the row's other columns belongs in the where: part", operand.String(), whoShape)
		}
		tests = append(tests, test)
	}

	return tests, nil
}

// whoTest reads one operand of a who: part as a test, ok false for anything
// that is neither of the two forms.
func whoTest(operand Expr) (test WhoTest, ok bool) {
	switch n := operand.(type) {
	case Comparison:
		if _, subject := n.Right.(Subject); !subject || n.Op != Eq || !isColumnRef(n.Left) {
			return WhoTest{}, false
		}

		return WhoTest{Column: n.Left.Name}, true
	case In:
		if n.SubjectSet == "" || n.Negated || !isColumnRef(n.Left) {
			return WhoTest{}, false
		}

		return WhoTest{Column: n.Left.Name, Set: n.SubjectSet}, true
	default:
		return WhoTest{}, false
	}
}

// isColumnRef reports whether a reference reads a column of the row as it is:
// a plain name, not the proposed value, not now and not a function of it.
func isColumnRef(ref Ref) bool {
	return !ref.PostImage && !ref.IsNow() && !ref.IsTemporal()
}

// ParseWhere compiles a subject set's where: part: a condition over the
// columns of the set's own row, compared to literals and to now, with the
// comparisons, IN over a literal list, IS NULL, AND, OR and NOT. It is judged
// as any condition is, so a row counts only where the condition is TRUE.
//
// What a where: cannot say is refused here. It names nothing of the requester
// (subject, subject.<name>): the test that ties a row to the requester is the
// who: part. It reads no proposed value (new.): a set's rows are read as the
// database holds them. And every test reads a column, so now on the left and
// the temporal functions, which the database does not evaluate, are refused.
func ParseWhere(source string) (Expr, error) {
	expr, err := Parse(source)
	if err != nil {
		return nil, fmt.Errorf("where: %w; %s", err, whereShape)
	}

	var refusal error
	refuse := func(format string, args ...any) {
		if refusal == nil {
			refusal = fmt.Errorf("where: "+format, args...)
		}
	}
	checkLeft := func(node Expr, left Ref) {
		switch {
		case left.PostImage:
			refuse("%q reads a proposed value (new.); a set's rows are read as the database holds them, so name the column alone", node.String())
		case left.IsTemporal():
			refuse("%q calls %s, which the database does not evaluate; %s", node.String(), left.Func, whereShape)
		case left.IsNow():
			refuse("%q does not read a column of the row; write the column on the left, as in ExpiresAt > now", node.String())
		}
	}
	walk(expr, func(node Expr) {
		switch n := node.(type) {
		case Comparison:
			checkLeft(n, n.Left)
			switch n.Right.(type) {
			case Subject, SubjectValue:
				refuse("%q names the requester; a where: reads the row's own columns alone, and the test that ties a row to the requester belongs in the who: part", n.String())
			}
		case In:
			checkLeft(n, n.Left)
			if n.SubjectSet != "" {
				refuse("%q names the requester; a where: reads the row's own columns alone, and the test that ties a row to the requester belongs in the who: part", n.String())
			}
		case NullTest:
			checkLeft(n, n.Left)
		}
	})
	if refusal != nil {
		return nil, refusal
	}

	return expr, nil
}

// RenameBindings returns the expression with every attribute name passed
// through rename: the left side of each comparison, membership test and null
// test, and a comparison's right-side attribute. The fact now and the temporal
// functions carry no name and stay as they are. It turns a subject set's parts
// from the names the definition writes into the columns they read, for a
// rendering in the schema's words.
func RenameBindings(e Expr, rename func(name string) string) Expr {
	renamed := func(ref Ref) Ref {
		if ref.IsNow() || ref.IsTemporal() {
			return ref
		}
		ref.Name = rename(ref.Name)

		return ref
	}

	switch n := e.(type) {
	case And:
		operands := make([]Expr, 0, len(n.Operands))
		for _, operand := range n.Operands {
			operands = append(operands, RenameBindings(operand, rename))
		}

		return And{Operands: operands}
	case Or:
		operands := make([]Expr, 0, len(n.Operands))
		for _, operand := range n.Operands {
			operands = append(operands, RenameBindings(operand, rename))
		}

		return Or{Operands: operands}
	case Not:
		return Not{Operand: RenameBindings(n.Operand, rename)}
	case Comparison:
		n.Left = renamed(n.Left)
		if right, ok := n.Right.(Ref); ok {
			n.Right = renamed(right)
		}

		return n
	case In:
		n.Left = renamed(n.Left)

		return n
	case NullTest:
		n.Left = renamed(n.Left)

		return n
	default:
		return e
	}
}
