package identity

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/words"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
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
// A row that reaches the person without naming them is held to the same rule by the two
// functions beside this one, which the generated write asks as well. A row that names a
// set the person is in (a pass given to their team) is refused by
// RefuseImpersonatorAsSetSubject, and a change to the row another record's value is
// read through (the team's own row, which says which division the team is in) by
// RefuseImpersonatorAsReadThroughSubject. Both hold under every mask, the one that
// keeps the record's writes included: a kind of impersonation that makes the record an
// exception of the class access (accesstypes.RefuseClassExcept) lets a person write it
// for other people, and never for themselves, their own set or the row their own
// standing is read through. What stays a stated limit is the one
// RefuseImpersonatorAsSubject names, a role given to a group the person is in.
//
// A row taken with the record it is under (the record's @deleteCascade) is held to the
// rule as well. The library deletes it through its own kind's delete, armed against the
// person who asked, and the generated code asks this rule and the two beside it about
// the row there, as a removal: a delete that would take the real person's own row, or a
// row of a set they are in, is refused whole, in the sentence the row's own removal is
// refused with, and nothing is deleted. A mask that refuses the row's own delete
// refuses the record's delete too, since each taken row is checked against the caller's
// grants. A row that goes with a group of the library's that it names (@deletedWith) is
// held to the same three questions: the library deletes the group without arming the
// delete, and the row is checked as the request's caller all the same
// (resource.DeleteKind.AsCaller). A delete an application runs trusted, with no caller,
// asks neither: an action that deletes such a record in its own body takes the rows
// under it unasked, and an application closes that by refusing the action by name in
// the kind's mask (accesstypes.RefuseAction).
//
// A row that goes with its tenant is a limit of its own. A tenant's delete takes every
// row under the tenant on the grant to delete the tenant: no grant is asked on a row,
// and the rule is not asked about the person a row names. The generator gives the
// tenant record's delete the class access, so a kind of impersonation whose mask
// refuses that class does not delete a tenant. An application closes it for any other
// kind by refusing the delete in the kind's mask
// (accesstypes.RefusePermission(accesstypes.Delete, tenant)), and by name any action
// that deletes a tenant in its own body (accesstypes.RefuseAction), because an action
// is asked for Execute on its name and not for Delete on the record.
//
// A row that goes with an account it names (@deletedWith) is the same limit. The
// account's delete takes it on the permission to delete the account: no grant is asked
// on the row, and the rule is not asked about the person or the set it names. An
// application closes it for a kind by refusing, in the kind's mask, the methods that
// delete a person, by name (accesstypes.RefuseAction(method)).
//
// A row a foreign key's referential action removes or rewrites is another: no write of
// the row is made, so nothing is asked. impulse check refuses ON DELETE CASCADE on an
// application's tables, wherever the link points. PostgreSQL has referential actions that change the
// row where it stands. ON DELETE SET NULL and ON DELETE SET DEFAULT rewrite the column
// that named the parent when the parent is deleted, which refusing the parent's delete
// under the mask closes
// (accesstypes.RefusePermission(accesstypes.Delete, parent)). ON UPDATE CASCADE, SET
// NULL and SET DEFAULT rewrite it when the parent's key is changed, which refusing the
// parent's update closes (Update on the record, or on the one column the foreign key
// names), with any action that changes the key.
//
// A column that records who made a row is another. A grant may compare a column of a
// record to the requester (bookedBy = subject). Such a column carries no subject
// annotation, so it is not among userFields and the rule does not read it, though the
// row names a person and decides what they may do. While the application stamps the
// column from the session and people cannot write it (an output_only field with a
// default_create_fn), no write moves a row that exists onto the real person, and a row
// made in a session that acts as a person is stamped with the person acted as. A
// session that acts as a role is the stated case: a role is not anyone, so the
// request's User() there is the person who started the session, and a stamp taken from
// it puts their name on a row the role's powers made. What the grants give the row's
// author is theirs from then on, after the session has ended too, though their own
// roles might not have let them make the row. An application that does not want that
// refuses the record's create in the mask of the role kind
// (accesstypes.RefusePermission(accesstypes.Create, record)), or stamps the column with
// nobody, a value that is no account's id, in a session that acts as a role. The
// session's record says it is one:
//
//	imp, ok := sessioninfo.ImpersonationFromCtx(ctx); ok && imp.Principal.IsRole()
//
// The request's principal does not: a session of a person's own that the application's
// principal resolver gave a role is a role to PrincipalFromCtx too, and its User() is
// the row's true author. Where people can write such a column, the application refuses
// the record's writes under the mask.
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

// RefuseImpersonatorAsSetSubject is the rule of RefuseImpersonatorAsRowSubject for a row
// that reaches a person through a set they are in: the row's column holds no account,
// it holds a value the policy tests against one of the person's subject sets (a hangar
// pass given to a squadron, read as given to every person in the squadron). In a
// session that acts as someone else, the real person behind the session writes no such
// row for a set they are in themselves: they do not add one, change or remove one that
// is there, or move another onto their own set. A person acting as an administrator
// gives other people's squadrons a pass, and not their own.
//
// column is the record's column the set is asked about and set is the subject set's
// name, both named by the generated code from the record's own declaration (the who:
// test of a @subjectSet that follows another set). Whether a value is in the person's
// set is asked of the library, which builds the statement from the set's declaration
// (resource.PatchSet.SubjectSetHolds), so the rule and the policy share one definition
// of who is in a set: its who:, its where: and the sets it builds on. The real person
// is the one asked about, never the person acted as. The rule is asked about the row
// twice, as the row rule is: the value the stored row holds, read by its key inside the
// transaction, and the value the write gives the column.
//
// It answers nil outside an impersonated session, without reading anything, and for a
// row whose value is in no set of the real person's. The caller's grants answer first.
// It holds whatever the session's mask keeps, and its questions are asked on Spanner
// and on PostgreSQL alike. On PostgreSQL the two questions are what is proven, against
// real rows: the resource package's PostgreSQL client runs no generated write yet, so
// the rule is proven from the request to the row there when that client does. Where
// the question cannot be asked (a patch decoded with no collection, a transaction with
// no connection to ask on) it answers an error, and the write does not happen.
func RefuseImpersonatorAsSetSubject[Resource resource.Resourcer](
	ctx context.Context, txn resource.ReadWriteTransaction, op resource.OperationType, key resource.KeySet,
	patchSet *resource.PatchSet[Resource], record string, column accesstypes.Field, set string,
) error {
	stored := func() (any, bool, error) {
		return storedColumn[Resource](ctx, txn, key, column)
	}

	return refuseImpersonatorAsSetSubject(ctx, txn, op, key, patchSet, stored, record, column, set)
}

// subjectReach is what the rules for a row that reaches a person without naming them
// ask of the write: the caller's grants, the fields the write sets, and the library's
// two questions about a named person and the application's subject sets. A
// resource.PatchSet answers all of it.
type subjectReach interface {
	Permitted(ctx context.Context, txn resource.ReadWriteTransaction) error
	IsSet(field accesstypes.Field) bool
	Get(field accesstypes.Field) any
	SubjectSetHolds(ctx context.Context, txn resource.ReadWriteTransaction, set string, subject accesstypes.User, value any) (bool, error)
	SubjectReadThrough(ctx context.Context, txn resource.ReadWriteTransaction, name string, field accesstypes.Field, subject accesstypes.User, key resource.KeySet) (bool, error)
}

// refuseImpersonatorAsSetSubject is RefuseImpersonatorAsSetSubject over what it asks of
// the write, with stored reading the value the stored row holds in the column.
func refuseImpersonatorAsSetSubject(
	ctx context.Context, txn resource.ReadWriteTransaction, op resource.OperationType, key resource.KeySet,
	patchSet subjectReach, stored func() (value any, found bool, err error), record string, column accesstypes.Field, set string,
) error {
	actor, acting := impersonatorOf(ctx)
	if !acting {
		return nil
	}
	if err := patchSet.Permitted(ctx, txn); err != nil {
		return errors.Wrap(err, "resource.PatchSet.Permitted()")
	}

	person := User(actor)
	holds := func(value any) (bool, error) {
		if holdsNothing(value) {
			return false, nil
		}
		in, err := patchSet.SubjectSetHolds(ctx, txn, set, person, value)
		if err != nil {
			return false, errors.Wrap(err, "resource.PatchSet.SubjectSetHolds()")
		}

		return in, nil
	}

	before, after, err := heldBeforeAndAfter(op, key, patchSet, stored, column, holds)
	if err != nil {
		return err
	}

	refused := func(act string) error {
		return impersonatorRefusedOf(ctx, actor, fmt.Sprintf(act, record, set), fmt.Sprintf(act, record, inWords(set)))
	}
	switch {
	case op == resource.OperationCreate && after:
		return refused("add a %s record for one of your own %s")
	case op == resource.OperationCreate:
		return nil
	case op == resource.OperationDelete && before:
		return refused("remove a %s record of one of your own %s")
	case op == resource.OperationDelete:
		return nil
	case before:
		return refused("change a %s record of one of your own %s")
	case after:
		return refused("move a %s record to one of your own %s")
	}

	return nil
}

// heldBeforeAndAfter answers the question holds asks of the value the row's column
// holds before the write, and of the value it holds after it. A column of the key holds
// the same value before the write and after it. Any other column is read off the stored
// row for the value before, and off the operation's fields for the value after: an add
// has no row before it, and a removal sets nothing.
func heldBeforeAndAfter(
	op resource.OperationType, key resource.KeySet, patchSet subjectReach,
	stored func() (value any, found bool, err error), column accesstypes.Field, holds func(value any) (bool, error),
) (before, after bool, err error) {
	if value, inKey := keyValue(key, column); inKey {
		if before, err = holds(value); err != nil {
			return false, false, err
		}

		return before, before, nil
	}
	if op != resource.OperationCreate {
		held, found, storedErr := stored()
		if storedErr != nil {
			return false, false, storedErr
		}
		if found {
			if before, err = holds(held); err != nil {
				return false, false, err
			}
		}
	}
	if op != resource.OperationDelete && patchSet.IsSet(column) {
		if after, err = holds(patchSet.Get(column)); err != nil {
			return false, false, err
		}
	}

	return before, after, nil
}

// RefuseImpersonatorAsReadThroughSubject is the rule of RefuseImpersonatorAsRowSubject
// for the row a person's standing is read through: a subject set or a subject value
// whose value is declared as a path (`value: SquadronID.WingID`) is read through a
// column of another record, and a change that sets that column changes what the person
// may do without touching a row that names them. In a session that acts as someone
// else, the real person behind the session does not set that column on the row their
// own set or value is read through: a person acting as an administrator moves other
// people's squadrons to another wing, and not their own.
//
// column is the record's column the fact is read through and fact is the subject set's
// or the subject value's name, both named by the generated code from the declaration
// that reads through this record. Whether a row of the real person's reaches the row
// under key is asked of the library, which builds the statement from the fact's own
// declaration (resource.PatchSet.SubjectReadThrough).
//
// The rule is asked only of a change that sets the column: a change that leaves it
// alone (the squadron's new name) asks nothing and reads nothing, and so does an add,
// which no row of anyone's points at yet. A removal is not this rule's: the rows that
// point at the removed row stop its delete, or go with it through their own delete
// where a record declares that (@deleteCascade), and are asked there the rule for a row
// that names a person or a set. It answers nil outside an impersonated session. The
// caller's grants answer first.
//
// The person refused is told the column by its name in words ("change the wing id of
// the squadron record that decides your own wings"); the line the refusal is logged
// with says it by its own name, WingID.
func RefuseImpersonatorAsReadThroughSubject[Resource resource.Resourcer](
	ctx context.Context, txn resource.ReadWriteTransaction, op resource.OperationType, key resource.KeySet,
	patchSet *resource.PatchSet[Resource], record string, column accesstypes.Field, fact string,
) error {
	return refuseImpersonatorAsReadThroughSubject(ctx, txn, op, key, patchSet, record, column, fact)
}

// refuseImpersonatorAsReadThroughSubject is RefuseImpersonatorAsReadThroughSubject over
// what it asks of the write.
func refuseImpersonatorAsReadThroughSubject(
	ctx context.Context, txn resource.ReadWriteTransaction, op resource.OperationType, key resource.KeySet,
	patchSet subjectReach, record string, column accesstypes.Field, fact string,
) error {
	actor, acting := impersonatorOf(ctx)
	if !acting || op != resource.OperationUpdate || !patchSet.IsSet(column) {
		return nil
	}
	if err := patchSet.Permitted(ctx, txn); err != nil {
		return errors.Wrap(err, "resource.PatchSet.Permitted()")
	}

	reads, err := patchSet.SubjectReadThrough(ctx, txn, fact, column, User(actor), key)
	if err != nil {
		return errors.Wrap(err, "resource.PatchSet.SubjectReadThrough()")
	}
	if reads {
		const act = "change the %s of the %s record that decides your own %s"

		return impersonatorRefusedOf(ctx, actor, fmt.Sprintf(act, column, record, fact), fmt.Sprintf(act, inWords(string(column)), record, inWords(fact)))
	}

	return nil
}

// impersonatorRefusedOf is impersonatorRefused for an act that names a column or a
// subject set: the line the refusal is logged with says the act with the name as the
// code writes it (logged), and the person is told it with the name in words (said).
func impersonatorRefusedOf(ctx context.Context, actor impulse.UUID, logged, said string) error {
	line, _ := sessioninfo.ImpersonatorRefusal(actor, logged)
	logger.FromCtx(ctx).Info(line)
	_, refusal := sessioninfo.ImpersonatorRefusal(actor, said)

	return refusal
}

// inWords spells a column's or a subject set's name as lower-case words, as the
// library's refusals spell a field's: "WingID" is "wing id", "crewGroups" is "crew
// groups".
func inWords(name string) string {
	return words.InWords(name)
}

// holdsNothing reports whether a column's value is no value at all: a NULL is in no
// set, so nothing is asked about it.
func holdsNothing(value any) bool {
	v := reflect.ValueOf(value)
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return true
	}
	if id, ok := v.Interface().(impulse.NullUUID); ok {
		return !id.Valid
	}

	return false
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
	data, found, err := storedRow[Resource](ctx, txn, key, columns)
	if err != nil || !found {
		return false, err
	}
	for _, column := range columns {
		value := storedField(data, column)
		if !value.IsValid() {
			return false, errors.Newf("identity: %s has no field %s to read the person its row names from", data.Type(), column)
		}
		if columnNames(value.Interface(), account) {
			return true, nil
		}
	}

	return false, nil
}

// storedColumn answers the value the stored row under the key holds in the column, for
// RefuseImpersonatorAsSetSubject: the value the row holds before a write. A row that is
// not there holds none, and the write that follows finds it missing and says so.
func storedColumn[Resource resource.Resourcer](
	ctx context.Context, txn resource.ReadWriteTransaction, key resource.KeySet, column accesstypes.Field,
) (value any, found bool, err error) {
	data, found, err := storedRow[Resource](ctx, txn, key, []accesstypes.Field{column})
	if err != nil || !found {
		return nil, false, err
	}
	held := storedField(data, column)
	if !held.IsValid() {
		return nil, false, errors.Newf("identity: %s has no field %s to read the stored value from", data.Type(), column)
	}

	return held.Interface(), true, nil
}

// storedRow reads the stored row under the key, for the columns alone, inside the
// transaction, and answers its fields; found is false for a row that is not there. A
// change or a removal that carries no key is not one a row can be read for, and is
// refused as the fault it is.
func storedRow[Resource resource.Resourcer](
	ctx context.Context, txn resource.ReadWriteTransaction, key resource.KeySet, columns []accesstypes.Field,
) (data reflect.Value, found bool, err error) {
	if key.Len() == 0 {
		var record Resource

		return reflect.Value{}, false, errors.Newf("identity: no key to read the %s row by, so what it holds cannot be known", record.Resource())
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
			return reflect.Value{}, false, nil
		}

		return reflect.Value{}, false, errors.Wrap(err, "resource.QuerySet.Read()")
	}

	return reflect.Indirect(reflect.ValueOf(row.Data)), true, nil
}

// storedField answers the field of the stored row that holds the column, or the zero
// Value where the row's type has no field of that name. The column picks one of the
// fields the type itself declares, a field of an embedded struct among them as a
// selector reaches it, and the field is then read by its place in the type: a name
// reaches nothing of the row but a field its type lists.
func storedField(data reflect.Value, column accesstypes.Field) reflect.Value {
	for _, field := range reflect.VisibleFields(data.Type()) {
		if field.Name == string(column) {
			return data.FieldByIndex(field.Index)
		}
	}

	return reflect.Value{}
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
