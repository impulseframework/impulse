package condition

import (
	"fmt"
)

// A grant on a method may test the method's own request fields. A request field is
// written new.<name>, the form a create's grant uses for the row being written, and is
// known the moment the request is decoded, so every atom that reads one is settled in
// Go, before any row is looked for. What is left reads the target row alone and is
// lowered into the gate's statement as any row condition is.

// PreImageBindings returns the distinct attribute names the expression reads off
// the row as it is: every reference without the new. prefix, a comparison's
// right-side attribute included. Sorted.
func PreImageBindings(e Expr) []string {
	set := map[string]struct{}{}
	walk(e, func(node Expr) {
		ref, ok := atomLeft(node)
		if !ok {
			return
		}
		if n, ok := node.(Comparison); ok {
			if right, ok := n.Right.(Ref); ok {
				set[right.Name] = struct{}{}
			}
		}
		if !ref.PostImage && !ref.IsNow() && !ref.IsTemporal() {
			set[ref.Name] = struct{}{}
		}
	})

	return sortedKeys(set)
}

// PostImageBindings returns the distinct names the expression reads under the
// new. prefix. Sorted.
func PostImageBindings(e Expr) []string {
	set := map[string]struct{}{}
	walk(e, func(node Expr) {
		if ref, ok := atomLeft(node); ok && ref.PostImage {
			set[ref.Name] = struct{}{}
		}
	})

	return sortedKeys(set)
}

// PostImageAgainstLiteralsOnly reports whether every atom that reads new.
// compares against literals alone: no subject, no subject value, no subject set,
// no now, no attribute. Only such an atom can be settled against a decoded request
// alone, so a role file whose Execute grant compares a request field to anything else is
// refused when the roles are deployed.
func PostImageAgainstLiteralsOnly(e Expr) bool {
	ok := true
	walk(e, func(node Expr) {
		switch n := node.(type) {
		case Comparison:
			if !n.Left.PostImage {
				return
			}
			if _, literal := n.Right.(Literal); !literal {
				ok = false
			}
		case In:
			if n.Left.PostImage && n.SubjectSet != "" {
				ok = false
			}
		}
	})

	return ok
}

// atomLeft answers the left reference of an atom (a comparison, a membership
// test, a null test).
func atomLeft(node Expr) (Ref, bool) {
	switch n := node.(type) {
	case Comparison:
		return n.Left, true
	case In:
		return n.Left, true
	case NullTest:
		return n.Left, true
	default:
		return Ref{}, false
	}
}

// SettlePostImage evaluates every atom that reads new.<name> against post, the
// proposed values by name (a nil entry is no value), and returns what is left:
// a constant when every atom read new., otherwise the expression over the row's
// own attributes. An atom that reads no value is unknown, and unknown never
// permits: it settles to the bound that fails (FALSE where it stands under an
// even number of negations, TRUE under an odd number).
func SettlePostImage(e Expr, post map[string]any) (Expr, error) {
	return settle(e, &Image{Post: post}, true)
}

func settle(e Expr, image *Image, positive bool) (Expr, error) {
	switch n := e.(type) {
	case And:
		operands := make([]Expr, 0, len(n.Operands))
		for _, op := range n.Operands {
			settled, err := settle(op, image, positive)
			if err != nil {
				return nil, err
			}
			if t, ok := settled.(Truth); ok {
				if !t.Value {
					return Truth{Value: false}, nil
				}

				continue
			}
			operands = append(operands, settled)
		}

		return rebuildLogic(operands, true), nil
	case Or:
		operands := make([]Expr, 0, len(n.Operands))
		for _, op := range n.Operands {
			settled, err := settle(op, image, positive)
			if err != nil {
				return nil, err
			}
			if t, ok := settled.(Truth); ok {
				if t.Value {
					return Truth{Value: true}, nil
				}

				continue
			}
			operands = append(operands, settled)
		}

		return rebuildLogic(operands, false), nil
	case Not:
		settled, err := settle(n.Operand, image, !positive)
		if err != nil {
			return nil, err
		}
		if t, ok := settled.(Truth); ok {
			return Truth{Value: !t.Value}, nil
		}

		return Not{Operand: settled}, nil
	case Comparison, In, NullTest:
		ref, _ := atomLeft(n)
		if !ref.PostImage {
			return n, nil
		}
		if _, touched := image.Post[ref.Name]; !touched {
			return nil, fmt.Errorf("condition: new.%s names no proposed value in %q", ref.Name, n.String())
		}
		result, err := Evaluate(n, image)
		if err != nil {
			return nil, err
		}
		switch result {
		case True:
			return Truth{Value: true}, nil
		case False:
			return Truth{Value: false}, nil
		default:
			return Truth{Value: !positive}, nil
		}
	default:
		return e, nil
	}
}

// AllOf is the conjunction of settled expressions, simplified: one FALSE makes
// it FALSE, a TRUE drops out, and an expression that appears twice stands once.
func AllOf(operands []Expr) Expr {
	kept := make([]Expr, 0, len(operands))
	seen := map[string]bool{}
	for _, op := range operands {
		if t, ok := op.(Truth); ok {
			if !t.Value {
				return Truth{Value: false}
			}

			continue
		}
		if source := op.String(); !seen[source] {
			seen[source] = true
			kept = append(kept, op)
		}
	}

	return rebuildLogic(kept, true)
}
