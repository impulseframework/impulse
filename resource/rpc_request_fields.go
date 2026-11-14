package resource

import (
	"fmt"
	"maps"
	"math"
	"reflect"
	"time"

	"cloud.google.com/go/civil"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// A grant on a method may test the method's own request fields.
//
// A method declares the fields a condition may read with a bare @attribute(name) on
// the field; the generator registers them as the method resource's own attributes, the
// Column of each being the request struct's Go field. A grant's condition reads one as
// new.<name>. Both decoders settle those atoms in Go as soon as the request is decoded:
// a method with no target is then allowed or refused at decode, and a method with a
// target carries what is left (the part that reads the row) to its gate.
//
// A list field is judged once per element: every element has to pass. A list with no
// element is no value, and no value never permits. A method declares at most one list
// field this way (the generator refuses a second), so a condition never reads two.

// settleRequestFields settles every new.<name> atom of expr against the decoded
// request and returns what is left of it.
func settleRequestFields(collection *GeneratedCollection, method accesstypes.Resource, expr condition.Expr, request any) (condition.Expr, error) {
	names := condition.PostImageBindings(expr)
	if len(names) == 0 {
		return expr, nil
	}
	if collection == nil {
		return nil, errors.Newf("method %s: a condition reads request fields (%v) and the decoder holds no collection to resolve them", method, names)
	}
	bindings, _ := collection.Bindings(collection.Scope(method), method)

	value := reflect.Indirect(reflect.ValueOf(request))
	scalars := make(map[string]any, len(names))
	var (
		listName string
		list     []any
	)
	for _, name := range names {
		var field string
		for _, attr := range bindings.Attributes {
			if attr.Name == name {
				field = attr.Column
			}
		}
		if field == "" {
			return nil, errors.Newf("method %s: a condition reads new.%s, which the method does not declare as a request field", method, name)
		}
		// field is the Go field the generator recorded for the name in the method's own
		// declarations: the condition's name selects among those, and a name the method
		// does not declare was refused above, so nothing taken from the request or from
		// a role file reaches this lookup as a field name.
		// nosemgrep: go.lang.security.audit.unsafe-reflect-by-name.unsafe-reflect-by-name
		fieldValue := value.FieldByName(field)
		if !fieldValue.IsValid() {
			return nil, errors.Newf("method %s: request field %s (new.%s) is not a field of %s", method, field, name, value.Type())
		}
		if fieldValue.Kind() == reflect.Slice && fieldValue.Type().Elem().Kind() != reflect.Uint8 {
			if listName != "" {
				// The generator refuses a second list field on a method, so this is a
				// collection built by hand.
				return nil, errors.Newf("method %s: a condition reads two list fields (new.%s and new.%s); a method declares one list field for conditions", method, listName, name)
			}
			listName = name
			for i := range fieldValue.Len() {
				element, err := storageValue(fieldValue.Index(i))
				if err != nil {
					return nil, errors.Wrapf(err, "method %s: new.%s", method, name)
				}
				list = append(list, element)
			}

			continue
		}
		scalar, err := storageValue(fieldValue)
		if err != nil {
			return nil, errors.Wrapf(err, "method %s: new.%s", method, name)
		}
		scalars[name] = scalar
	}

	if listName == "" || len(list) == 0 {
		if listName != "" {
			scalars[listName] = nil
		}
		settled, err := condition.SettlePostImage(expr, scalars)
		if err != nil {
			return nil, errors.Wrapf(err, "method %s", method)
		}

		return settled, nil
	}
	settled := make([]condition.Expr, 0, len(list))
	for _, element := range list {
		post := maps.Clone(scalars)
		post[listName] = element
		one, err := condition.SettlePostImage(expr, post)
		if err != nil {
			return nil, errors.Wrapf(err, "method %s", method)
		}
		settled = append(settled, one)
	}

	return condition.AllOf(settled), nil
}

// storageValue converts a decoded request field to the value the condition
// evaluator compares: a string, an int64, a float64, a bool, an instant or a
// date. A nil pointer is no value. A type that prints itself (an id) is its text.
func storageValue(v reflect.Value) (any, error) {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil, nil
		}
		v = v.Elem()
	}
	switch value := v.Interface().(type) {
	case time.Time:
		return value.UTC(), nil
	case civil.Date:
		return condition.Date{Year: value.Year, Month: value.Month, Day: value.Day}, nil
	}
	switch {
	case v.Kind() == reflect.String:
		return v.String(), nil
	case v.CanInt():
		return v.Int(), nil
	case v.CanUint():
		unsigned := v.Uint()
		if unsigned > math.MaxInt64 {
			return nil, errors.Newf("a request field holds %d, which is beyond what a condition compares (a 64-bit signed integer)", unsigned)
		}

		return int64(unsigned), nil
	case v.CanFloat():
		return v.Float(), nil
	case v.Kind() == reflect.Bool:
		return v.Bool(), nil
	}
	if stringer, ok := v.Interface().(fmt.Stringer); ok {
		return stringer.String(), nil
	}

	return nil, errors.Newf("a request field of type %s has no comparison type", v.Type())
}
