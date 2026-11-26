package identity

import (
	"context"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// The fake store's tables of the roles held on one record hold no row: a read answers
// none, and a write is logged as the other writes are.

func (f *fakeStore) SetRecordMembership(_ context.Context, _ resource.ReadWriteTransaction, key *access.RecordMembershipKey, _ accesstypes.Period) error {
	if err := f.fail("SetRecordMembership"); err != nil {
		return err
	}
	f.writes = append(f.writes, "set record membership "+recordMembershipText(key))

	return nil
}

func (f *fakeStore) DeleteRecordMembership(_ context.Context, _ resource.ReadWriteTransaction, key *access.RecordMembershipKey) error {
	if err := f.fail("DeleteRecordMembership"); err != nil {
		return err
	}
	f.writes = append(f.writes, "delete record membership "+recordMembershipText(key))

	return nil
}

func (f *fakeStore) SetRecordGroupRole(_ context.Context, _ resource.ReadWriteTransaction, key *access.RecordGroupRoleKey, _ accesstypes.Period) error {
	if err := f.fail("SetRecordGroupRole"); err != nil {
		return err
	}
	f.writes = append(f.writes, "set record group role "+recordGroupRoleText(key))

	return nil
}

func (f *fakeStore) DeleteRecordGroupRole(_ context.Context, _ resource.ReadWriteTransaction, key *access.RecordGroupRoleKey) error {
	if err := f.fail("DeleteRecordGroupRole"); err != nil {
		return err
	}
	f.writes = append(f.writes, "delete record group role "+recordGroupRoleText(key))

	return nil
}

func (f *fakeStore) RecordRoles(context.Context, resource.ReadWriteTransaction, accesstypes.RecordScope) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {
	if err := f.fail("RecordRoles"); err != nil {
		return nil, nil, err
	}

	return nil, nil, nil
}

func (f *fakeStore) TenantRecordRoles(context.Context, resource.ReadWriteTransaction, accesstypes.Domain) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {
	if err := f.fail("TenantRecordRoles"); err != nil {
		return nil, nil, err
	}

	return nil, nil, nil
}

func (f *fakeStore) RecordMemberships(context.Context, resource.ReadWriteTransaction, impulse.UUID) ([]access.RecordMembershipKey, error) {
	if err := f.fail("RecordMemberships"); err != nil {
		return nil, err
	}

	return nil, nil
}

// recordMembershipText spells a person's role on a record for the fake's log of writes.
func recordMembershipText(key *access.RecordMembershipKey) string {
	return key.Domain + " " + key.Resource + "/" + key.Record + " " + string(key.User) + " " + string(key.Role)
}

// recordGroupRoleText spells a group's role on a record for the fake's log of writes.
func recordGroupRoleText(key *access.RecordGroupRoleKey) string {
	return key.Domain + " " + key.Resource + "/" + key.Record + " " + string(key.Group) + " " + string(key.Role)
}
