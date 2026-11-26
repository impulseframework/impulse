package resource

import (
	"context"
	"fmt"
	"iter"
	"maps"
	"reflect"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// NamedRowSources are the views and the computed lists the rows a write names are asked
// of, by resource: a column picked from a view (NamedRow.View) or from a computed list
// (NamedRow.Computed) is set only to a row the writer's list of it shows, and the write
// reads that row from the view's query, or asks the list's own function for it, which it
// is handed here. The generated handlers hand the application's to every decoder
// (WithNamedRowSources); a caller a decode answers carries them on to a write a body
// arms (PatchSet.Enforce), and holds a method's picked fields to them (HoldPicked).
type NamedRowSources map[accesstypes.Resource]NamedRowSource

// NamedRowSource is one view or one computed list a named row is asked of. Its zero
// value is no source.
type NamedRowSource struct {
	// view is the view whose query the named row is read from; nil for a computed list.
	view View
	// computed asks a computed list whether the caller's list of it shows a row
	// (ComputedRows); nil for a view.
	computed func(ctx context.Context, ask *computedAsk) (bool, error)
}

// computedAsk is what a computed list is asked with: the caller of the write or of the
// method, the scope the caller's List on the list was asked in and what it answered, the
// reader the caller's facts are read through (the write's transaction, or the client a
// method's request is held with), and the key of the row named as spelled (columnText).
type computedAsk struct {
	caller   *Caller
	scope    accesstypes.Scope
	decision accesstypes.Decision
	reader   namedRowReader
	spelled  string
}

// ViewRows is the source of a view's rows: a named row is read from the view's query
// (Subquery), as every statement over the view reads it.
func ViewRows(view View) NamedRowSource {
	return NamedRowSource{view: view}
}

// ComputedRows is the source of a computed list's rows: a row a write names is asked of
// the list's own function, list, run as the caller's list of it runs. The function is
// handed a query set of the list's resource in the scope the caller's List was asked in,
// for the caller, with no filter, no sort, no page and no fields, and the caller stamped
// on the context (WithCaller). The rows it yields are read in order, each judged by the
// condition the caller's List carries, as the list's own handler judges it, until one the
// caller is shown whose key, the struct field key, spells the value the write sends
// (columnText). key is the Go field of the row struct that holds the list's one key: a
// field the struct does not have panics, as the Must constructors do, since generated
// code calls it where the decoders are built.
func ComputedRows[Resource Resourcer](key accesstypes.Field, list func(ctx context.Context, q *QuerySet[Resource]) iter.Seq2[*Resource, error]) NamedRowSource {
	rowType := reflect.TypeFor[Resource]()
	field, ok := structField(rowType, string(key))
	if !ok {
		panic(fmt.Sprintf("resource.ComputedRows: %s has no field %s", rowType, key))
	}

	return NamedRowSource{computed: func(ctx context.Context, ask *computedAsk) (bool, error) {
		q := NewQuerySet(NewMetadata[Resource]())
		q.env = ask.caller.Env
		q.collection = ask.caller.collection
		q.namedRowSources = ask.caller.namedRowSources
		q.scope = ask.scope
		q.requiredPermission = accesstypes.List
		q.userPermissions = ask.caller.Permissions
		q.dbType = ask.reader.DBType()
		if ask.decision.IsConditional() {
			expr, err := conditionalExpr(q.Resource(), ask.decision)
			if err != nil {
				return false, err
			}
			if err := q.judgeRowsBy(ctx, &rowConditions{open: true, resource: expr}, q.Resource(), q.userPermissions, ask.reader); err != nil {
				return false, err
			}
		}

		for row, err := range list(WithCaller(ctx, q.Caller()), q) {
			if err != nil {
				return false, err
			}
			if row == nil {
				continue
			}
			shown, _, err := q.judgeRow(row)
			if err != nil {
				return false, err
			}
			if !shown {
				continue
			}
			spelled, err := columnText(reflect.ValueOf(row).Elem().FieldByIndex(field.Index).Interface())
			if err != nil {
				return false, errors.Wrapf(err, "the key %s of a row of %s", key, q.Resource())
			}
			if spelled == ask.spelled {
				return true, nil
			}
		}

		return false, nil
	}}
}

// MergeNamedRowSources returns one map holding every entry of its arguments, a later
// argument's entry replacing an earlier one's. It is never nil.
func MergeNamedRowSources(sets ...NamedRowSources) NamedRowSources {
	merged := make(NamedRowSources)
	for _, set := range sets {
		maps.Copy(merged, set)
	}

	return merged
}

// viewItem renders the view a column is picked from as the FROM item the named row is
// read from: the view's query under its resource's name, as viewFromItem writes it for
// every statement over the view, with the WITH clause that leads the statement and the
// query's own parameters joined to params. A parameter the statement already binds is
// refused, as a gate's read of a view's row refuses it (targetRow). A caller handed no
// source of the view is an error for the log: the application did not hand the decoder
// its sources.
func (a *listedAsk) viewItem(row *NamedRow, params map[string]any) (withClause, item string, err error) {
	source := a.caller.namedRowSources[row.Resource]
	if source.view == nil {
		return "", "", a.noSource(row)
	}
	withClause, item, viewParams, err := viewFromItem(row.Resource, source.view)
	if err != nil {
		return "", "", errors.Wrapf(err, "the row %s names", a.naming())
	}
	for name, value := range viewParams {
		if _, ok := params[name]; ok {
			return "", "", errors.Newf("named parameter collision: %s query and the read of the row %s names both contain named parameter %q", row.Resource, a.naming(), name)
		}
		params[name] = value
	}
	if withClause != "" {
		withClause += "\n"
	}

	return withClause, item, nil
}

// listsComputedRow asks the computed list a column is picked from whether the caller's
// list of it shows the row named: the list's own function, run as the caller's list runs
// it (ComputedRows), in the scope the caller's List on the list was asked in, with the
// decision it answered. An error the function yields is the error of the write, or of the
// method. A caller handed no source of the list, or a view's, is an error for the log.
func (a *listedAsk) listsComputedRow(ctx context.Context, n namedValue, decision accesstypes.Decision) (bool, error) {
	source := a.caller.namedRowSources[n.row.Resource]
	if source.computed == nil {
		return false, a.noSource(n.row)
	}
	listed, err := source.computed(ctx, &computedAsk{caller: a.caller, scope: n.row.grantScope(a.caller.Scope), decision: decision, reader: a.reader, spelled: n.spelled})
	if err != nil {
		return false, errors.Wrapf(err, "asking the computed list %s for the row %s names", n.row.Resource, a.naming())
	}

	return listed, nil
}

// noSource says a write, or a method's request, names a row of a view, or of a computed
// list, it was handed no source of.
func (a *listedAsk) noSource(row *NamedRow) error {
	owner, carrier, source := "resource", "write", "the view"
	if a.method {
		owner, carrier = "method", "request"
	}
	if row.Computed {
		source = "the computed list"
	}

	return errors.Newf("%s %s field %s names a row of %s %s, and the %s was handed no source of that name: hand the application's named-row sources to the decoder (WithNamedRowSources), which the generated handlers do; regenerate them", owner, a.owner, row.Field, source, row.Resource, carrier)
}
