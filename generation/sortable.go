package generation

import (
	"go/token"
	"go/types"
)

// The types the server orders by whatever their kind, by the package each is declared
// in and its name: a moment, a calendar date and a decimal.
var orderedNamedTypes = map[string]struct{}{
	"time.Time":                             {},
	"cloud.google.com/go/civil.Date":        {},
	"github.com/shopspring/decimal.Decimal": {},
}

// ordersType reports whether the server orders a list by a field of the type, so the
// generated metadata can say which columns take a sort and a table offers no sort the
// server would refuse. It is the query decoder's rule (sortableType in the resource
// package) read off the declared type instead of the running one: text, numbers,
// booleans, moments, dates, decimals, a type that marshals to text and back (a UUID),
// and a pointer to or a Null wrapper of any of them order; a slice, a map, a plain
// struct and a plain array do not. The two rules are held to one fixture
// (testdata/sortfixture), which each package's test reads.
func ordersType(t types.Type) bool {
	t = nullableBase(t)
	if named, ok := t.(*types.Named); ok && named.Obj().Pkg() != nil {
		if _, ordered := orderedNamedTypes[named.Obj().Pkg().Path()+"."+named.Obj().Name()]; ordered {
			return true
		}
	}
	switch underlying := t.Underlying().(type) {
	case *types.Basic:
		switch underlying.Kind() {
		case types.String, types.Bool,
			types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64,
			types.Float32, types.Float64:
			return true
		default:
			return false
		}
	case *types.Struct, *types.Array:
		// A UUID and its kin: marshaled to text, so ordered as text.
		return types.Implements(types.NewPointer(t), textUnmarshaler) && types.Implements(t, textMarshaler)
	default:
		return false
	}
}

// nullableBase returns the type a pointer points at and a Null wrapper holds: the
// wrapper is a struct with a boolean Valid field, and what it holds is its first other
// field. Any other type is returned as it is.
func nullableBase(t types.Type) types.Type {
	t = types.Unalias(t)
	for {
		pointer, ok := t.Underlying().(*types.Pointer)
		if !ok {
			break
		}
		t = types.Unalias(pointer.Elem())
	}
	wrapper, ok := t.Underlying().(*types.Struct)
	if !ok {
		return t
	}
	valid, _, _ := types.LookupFieldOrMethod(t, false, nil, validField)
	if field, isField := valid.(*types.Var); !isField || !isBoolean(field.Type()) {
		return t
	}
	for field := range wrapper.Fields() {
		if field.Name() != validField {
			return nullableBase(field.Type())
		}
	}

	return t
}

// isBoolean reports whether a type's underlying type is bool.
func isBoolean(t types.Type) bool {
	basic, ok := t.Underlying().(*types.Basic)

	return ok && basic.Kind() == types.Bool
}

// textMarshaler and textUnmarshaler are encoding.TextMarshaler and
// encoding.TextUnmarshaler, built here so a type is asked for them without the encoding
// package having been loaded beside it.
var (
	textMarshaler   = textInterface("MarshalText", nil, []types.Type{types.NewSlice(types.Typ[types.Byte]), types.Universe.Lookup("error").Type()})
	textUnmarshaler = textInterface("UnmarshalText", []types.Type{types.NewSlice(types.Typ[types.Byte])}, []types.Type{types.Universe.Lookup("error").Type()})
)

// textInterface builds an interface of one method with the given parameter and result types.
func textInterface(method string, params, results []types.Type) *types.Interface {
	signature := types.NewSignatureType(nil, nil, nil, tuple(params), tuple(results), false)
	iface := types.NewInterfaceType([]*types.Func{types.NewFunc(token.NoPos, nil, method, signature)}, nil)
	iface.Complete()

	return iface
}

// tuple builds the unnamed parameters or results of a signature from their types.
func tuple(of []types.Type) *types.Tuple {
	vars := make([]*types.Var, 0, len(of))
	for _, t := range of {
		vars = append(vars, types.NewVar(token.NoPos, nil, "", t))
	}

	return types.NewTuple(vars...)
}
