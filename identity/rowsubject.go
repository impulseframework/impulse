package identity

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// RefuseImpersonatorAsRowSubject is RefuseImpersonatorAsSubject for the write of a row
// of a record whose rows decide what a person may do: a record the policy reads as a
// subject value or a subject set (the generator's @subjectValue and @subjectSet), such
// as a clearance, a spending limit or a place in a team. In a session that acts as
// someone else, the real person behind the session writes no row that names them: they
// do not add one, change or remove the one they have, hand it to someone else, or put
// their own account on another row. A person who acts as an administrator would
// otherwise raise their own clearance by the record's ordinary write and keep it after
// the session ends. The generated write of such a record asks it, of every add, change
// and removal, so the record is held to the rule without a line in the application; an
// action an application writes over such a record asks RefuseImpersonatorAsSubject of
// the person it names.
//
// The rule is asked about the row twice. Before the write: the person the stored row
// names, read by the key the operation's path carries, inside the transaction, so a
// change or a removal of the real person's own row is refused, and so is moving it off
// them. After the write: the person the operation's fields name, so a row added for the
// real person is refused, and so is another row moved onto them. userFields are the
// record's columns that hold a person's account id, key is the row's key as the path
// gives it (empty for an add whose id the record generates), and record names the
// record in the refusal ("pilot certification"). A column that is part of the key names
// the same person before the write and after it.
//
// It answers nil outside an impersonated session, without reading anything, and for a
// row that names anyone else or nobody: the same write for another person goes through.
// The caller's grants answer before the rule (resource.PatchSet.Permitted), so a caller
// they refuse gets the same Forbidden as on any record and learns nothing from the
// rule's sentence. The stored row is read by its key alone and is not held to a tenant:
// the read decides only whether to refuse, and the write that follows is held to its
// tenant as ever.
//
// What it cannot see is a row that reaches the person without naming them: a row that
// names a set the person is in (a pass given to their team), the row another record's
// value is read through (the team's own row, which says which division the team is
// in), and a row the database removes with its parent. They are the limits
// RefuseImpersonatorAsSubject names for a group, and a mask that refuses the record's
// writes by name (accesstypes.RefuseWrites) refuses them.
func RefuseImpersonatorAsRowSubject[Resource resource.Resourcer](
	ctx context.Context, txn resource.ReadWriteTransaction, op resource.OperationType, key resource.KeySet,
	patchSet *resource.PatchSet[Resource], record string, userFields ...accesstypes.Field,
) error {
	actor, acting := impersonatorOf(ctx)
	if !acting || len(userFields) == 0 {
		return nil
	}
	if err := patchSet.Permitted(ctx, txn); err != nil {
		return errors.Wrap(err, "resource.PatchSet.Permitted()")
	}

	// A column of the key names the same person before the write and after it. Any
	// other column is read off the stored row for the person named before, and off
	// the operation's fields for the person named after.
	keyed := false
	var columns []accesstypes.Field
	for _, field := range userFields {
		value, inKey := keyValue(key, field)
		switch {
		case !inKey:
			columns = append(columns, field)
		case columnNames(value, actor):
			keyed = true
		}
	}

	stored := false
	if op != resource.OperationCreate && !keyed && len(columns) > 0 {
		var err error
		if stored, err = storedRowNames[Resource](ctx, txn, key, columns, actor); err != nil {
			return err
		}
	}
	written := false
	if op != resource.OperationDelete {
		for _, field := range columns {
			if patchSet.IsSet(field) && columnNames(patchSet.Get(field), actor) {
				written = true
			}
		}
	}

	switch {
	case op == resource.OperationCreate && (keyed || written):
		return impersonatorRefused(ctx, actor, fmt.Sprintf("add your own %s record", record))
	case op == resource.OperationCreate:
		return nil
	case op == resource.OperationDelete && (keyed || stored):
		return impersonatorRefused(ctx, actor, fmt.Sprintf("remove your own %s record", record))
	case keyed || stored:
		return impersonatorRefused(ctx, actor, fmt.Sprintf("change your own %s record", record))
	case written:
		return impersonatorRefused(ctx, actor, fmt.Sprintf("make another %s record your own", record))
	}

	return nil
}

// keyValue answers the value the key holds for the field, and whether the field is part
// of the key.
func keyValue(key resource.KeySet, field accesstypes.Field) (value any, inKey bool) {
	for _, part := range key.Parts() {
		if part.Key == field {
			return part.Value, true
		}
	}

	return nil, false
}

// storedRowNames reports whether the stored row under the key names the account in one
// of the columns: the person the row names before a write, for
// RefuseImpersonatorAsRowSubject. The read is one row by its key, inside the
// transaction, for those columns alone. A row that is not there names nobody: the write
// that follows finds it missing and says so. A change or a removal that carries no key
// is not one the rule can read a row for, and is refused as the fault it is.
func storedRowNames[Resource resource.Resourcer](
	ctx context.Context, txn resource.ReadWriteTransaction, key resource.KeySet, columns []accesstypes.Field, account impulse.UUID,
) (bool, error) {
	if key.Len() == 0 {
		var record Resource

		return false, errors.Newf("identity: no key to read the %s row by, so the person it names cannot be known", record.Resource())
	}

	qSet := resource.NewQuerySet(resource.NewMetadata[Resource]())
	for _, part := range key.Parts() {
		qSet.SetKey(part.Key, part.Value)
	}
	for _, column := range columns {
		qSet.AddField(column)
	}
	row, err := qSet.Read(ctx, txn)
	if err != nil {
		if httpio.HasNotFound(err) {
			return false, nil
		}

		return false, errors.Wrap(err, "resource.QuerySet.Read()")
	}

	data := reflect.Indirect(reflect.ValueOf(row.Data))
	for _, column := range columns {
		value := data.FieldByName(string(column))
		if !value.IsValid() {
			return false, errors.Newf("identity: %s has no field %s to read the person its row names from", data.Type(), column)
		}
		if columnNames(value.Interface(), account) {
			return true, nil
		}
	}

	return false, nil
}

// columnNames reports whether a column that holds a person's account id holds the
// account's. The id is held as text, as the permission store names a person (User), or
// as a UUID; a NULL names nobody, and so does text that is no account's id.
func columnNames(value any, account impulse.UUID) bool {
	v := reflect.ValueOf(value)
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return false
	}

	var text string
	switch id := v.Interface().(type) {
	case impulse.UUID:
		return id == account
	case impulse.NullUUID:
		return id.Valid && id.UUID == account
	case fmt.Stringer:
		text = id.String()
	default:
		if v.Kind() != reflect.String {
			return false
		}
		text = v.String()
	}
	named, ok := UserID(accesstypes.User(text))

	return ok && named == account
}
