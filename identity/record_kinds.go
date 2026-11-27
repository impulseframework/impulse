package identity

import (
	"context"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/eventsource"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/tracer"
)

// RecordKinds answers what the auth holds on one record as kinds of the record's delete
// (resource.TakenKind): the roles held on it, a person's and a group's, whatever their
// periods. The generated delete of a record a role can be held on hands them to the
// delete, which counts them in its question, asks them before anything goes and takes
// them in its rounds, so no role is left on a record that is gone; an application that
// deletes such a record in code of its own hands them to its delete there. A record the
// delete of a row above it takes is not handed them, and the roles held on it stay in
// their table, giving nothing.
func (m *Manager) RecordKinds(at accesstypes.RecordScope) []resource.TakenKind {
	return []resource.TakenKind{&recordRolesHeld{manager: m, at: at}}
}

// recordRolesHeld is the kind of the roles held on one record. One costs its row of its
// table, with the change record its removal writes.
type recordRolesHeld struct {
	manager *Manager
	at      accesstypes.RecordScope
}

// Words are what the question calls the roles held on the record.
func (*recordRolesHeld) Words() resource.DeleteWords {
	return resource.DeleteWords{One: "role held on it", Many: "roles held on it"}
}

// rolesHeld reads the roles people and groups hold on the record, each in key order.
func (k *recordRolesHeld) rolesHeld(ctx context.Context, txn resource.ReadWriteTransaction) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {
	people, groups, err := k.manager.store.RecordRoles(ctx, txn, k.at)
	if err != nil {
		return nil, nil, errors.Wrap(err, "identity.Store.RecordRoles()")
	}

	return people, groups, nil
}

// Count is the number of roles held on the record.
func (k *recordRolesHeld) Count(ctx context.Context, txn resource.ReadWriteTransaction) (int64, error) {
	people, groups, err := k.rolesHeld(ctx, txn)
	if err != nil {
		return 0, err
	}

	return int64(len(people) + len(groups)), nil
}

// Check asks nothing: the record's delete asks its own permission, and a role held on the
// record goes with it.
func (*recordRolesHeld) Check(context.Context, resource.ReadWriteTransaction) error {
	return nil
}

// Take takes the people's roles, then the groups', while the next one's cost fits the
// budget, and has the permission engine told at the commit where it took any.
func (k *recordRolesHeld) Take(ctx context.Context, txn resource.ReadWriteTransaction, budget int, _ ...eventsource.EventSource) (rows int64, mutations int, more bool, err error) {
	ctx, span := tracer.Start(ctx)
	defer span.End()

	people, groups, err := k.rolesHeld(ctx, txn)
	if err != nil {
		return 0, 0, false, err
	}

	return k.manager.takeRecordRoles(ctx, txn, people, groups, budget)
}

// takeRecordRoles deletes the roles held on records, the people's and then the groups',
// in the order given, while the next one's cost fits the budget and the save has time
// (resource.DeleteSaveOver, asked once a row is taken): one row of its table each. It
// answers the rows it took, what they cost and whether rows are left, and has the
// permission engine told at the commit where it took any.
func (m *Manager) takeRecordRoles(
	ctx context.Context, txn resource.ReadWriteTransaction, people []access.RecordMembershipKey, groups []access.RecordGroupRoleKey, budget int,
) (rows int64, mutations int, more bool, err error) {
	store := m.store
	costs := store.DeleteCosts()
	changed := func(rows int64) error {
		if rows == 0 {
			return nil
		}

		return m.policyChangesAtCommit(txn)
	}
	for i := range people {
		if mutations+costs.RecordMembership > budget || (rows > 0 && resource.DeleteSaveOver(ctx)) {
			return rows, mutations, true, changed(rows)
		}
		if err := store.DeleteRecordMembership(ctx, txn, &people[i]); err != nil {
			return rows, mutations, false, errors.Wrap(err, "identity.Store.DeleteRecordMembership()")
		}
		rows++
		mutations += costs.RecordMembership
	}
	for i := range groups {
		if mutations+costs.RecordGroupRole > budget || (rows > 0 && resource.DeleteSaveOver(ctx)) {
			return rows, mutations, true, changed(rows)
		}
		if err := store.DeleteRecordGroupRole(ctx, txn, &groups[i]); err != nil {
			return rows, mutations, false, errors.Wrap(err, "identity.Store.DeleteRecordGroupRole()")
		}
		rows++
		mutations += costs.RecordGroupRole
	}

	return rows, mutations, false, changed(rows)
}
