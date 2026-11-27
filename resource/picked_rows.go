package resource

import (
	"context"
	"reflect"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
)

// A method's request can pick rows as a column does: a field whose picker lists a table, a
// view or a computed list the application lists. The method's generated handler holds each
// such field to the rows its caller can list (HoldPicked), before the body runs, as a write
// holds a column (checkNamedRows): the picker offers only those rows, and the method
// refuses any other as a row that is not there. The question is the one a write asks
// (listedAsk), over the method's caller and the application's client, read outside any
// transaction. A method's request names no row it made itself, and leaves no value as it
// was: there is no pass for either.

// PickedRow is one picked field of a method's request, with what the request holds in it.
type PickedRow struct {
	// Row says what the field names, as a column's declaration says it: Field is the
	// request's field. Stored is never set: a method names no row its own request made.
	Row NamedRow
	// Value is what the request holds in the field: one key, or, for a field that
	// takes several, a slice of keys.
	Value any
}

// HoldPicked holds every row the method's request names in its picked fields to the rows
// the caller can list, and answers the first it cannot as a row that is not there: 404,
// "A record this one refers to does not exist." (referencedNotFound), the method and what
// was looked in kept as the cause, for the log.
//
//   - A value that is a nil pointer, or that columnText spells as empty, is not asked; a
//     slice, but a byte slice, is each of its items, and each key it spells is asked once.
//     With nothing left, nothing is asked, not even a grant.
//   - The caller's List on each kind named is asked once for each scope it is asked in
//     (grantScope over the caller's scope), in the caller's environment.
//   - Then each value, in the order given: a row of a table that belongs to no tenant
//     passes with no read where the List is granted outright; denied is the refusal;
//     otherwise the row is read through db as a write's check reads it (by key, within the
//     caller's tenant, under the grant's condition; a view's from its query), or the
//     computed list is asked through its function, which the caller carries
//     (NamedRowSources).
//
// A read needed on a database other than Spanner is not implemented; a db that holds no
// Spanner reader (a mock's) passes what needs a read, as a write's check does. A row of a
// tenant's kind asked by a caller whose scope names no tenant is generated code out of step
// with the library, which declares no such field: an error, never a pass. A zero caller is
// refused, as Enforce and Check refuse it.
func (c *Caller) HoldPicked(ctx context.Context, db Client, method accesstypes.Resource, picked ...PickedRow) error {
	if c.IsZero() {
		return errNoCaller
	}
	named, err := pickedValues(method, picked)
	if err != nil {
		return err
	}
	if len(named) == 0 {
		return nil
	}
	if _, partitioned := c.Scope.Domain(); !partitioned {
		for _, n := range named {
			if n.row.TenantColumn != "" || n.row.GrantInTenant {
				return errors.Newf("method %s field %s picks a row of %s, a kind of a tenant, in a request of no tenant: the generated code is out of step with the library, which declares no such field; regenerate it", method, n.row.Field, n.row.Resource)
			}
		}
	}
	decisions, err := listDecisions(ctx, c, named)
	if err != nil {
		return err
	}

	ask := &listedAsk{caller: c, reader: db, owner: method, method: true}
	for _, n := range named {
		decision := decisions[n.row.grantScope(c.Scope)][n.row.Resource]
		if decision.IsGranted() && n.row.fromTable() && n.row.TenantColumn == "" {
			continue
		}
		if decision.IsDenied() {
			return referencedNotFound(method, n.row.lookedIn())
		}
		if db.DBType() != SpannerDBType && !n.row.Computed {
			return errors.Newf("%s is not implemented for %s", namedRowsNotImplemented, db.DBType())
		}
		if db.DBType() == SpannerDBType && db.SpannerReadOnlyTransaction() == nil {
			continue
		}
		listed, err := ask.listsRow(ctx, n, decision)
		if err != nil {
			return err
		}
		if !listed {
			return referencedNotFound(method, n.row.lookedIn())
		}
	}

	return nil
}

// pickedValues are the keys the picked fields name, in the order given: a slice's items
// in its own order, a key a field names twice once, and a value spelled as empty left out.
func pickedValues(method accesstypes.Resource, picked []PickedRow) ([]namedValue, error) {
	var named []namedValue
	for i := range picked {
		row := &picked[i].Row
		seen := make(map[string]bool)
		for _, value := range pickedKeys(picked[i].Value) {
			spelled, err := columnText(value)
			if err != nil {
				return nil, errors.Wrapf(err, "method %s field %s", method, row.Field)
			}
			if spelled == "" || seen[spelled] {
				continue
			}
			seen[spelled] = true
			named = append(named, namedValue{row: row, value: value, spelled: spelled})
		}
	}

	return named, nil
}

// pickedKeys is what a picked field holds as keys: a slice's items, but a byte slice's,
// which is one key; a pointer's value; nothing for a nil pointer or a nil value.
func pickedKeys(value any) []any {
	if value == nil {
		return nil
	}
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice || rv.Type().Elem().Kind() == reflect.Uint8 {
		return []any{value}
	}
	keys := make([]any, 0, rv.Len())
	for i := range rv.Len() {
		keys = append(keys, rv.Index(i).Interface())
	}

	return keys
}
