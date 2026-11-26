package identity

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
)

// fakeRecordTables are the committed rows of the roles held on records, a person's and a
// group's, each with its period. A write is logged as the other writes are and changes no
// row.
type fakeRecordTables struct {
	recordRoles      map[access.RecordMembershipKey]accesstypes.Period
	recordGroupRoles map[access.RecordGroupRoleKey]accesstypes.Period
}

// heldOnProjects are the roles the tests' role file marks for the kind Projects
// (Role.HeldOn): the only kind the fake access library lets a role be held on.
var heldOnProjects = []accesstypes.Role{"ProjectEditor", "ProjectViewer"}

// projects is the kind of record the tests hold roles on.
const projects accesstypes.Resource = "Projects"

// RecordMembership answers the rule on a person's role on a record as the library does,
// in the library's words: a kind the collection marks, a role the role file marks for it.
func (u *fakeUserManager) RecordMembership(_ context.Context, at accesstypes.RecordScope, user accesstypes.User, role accesstypes.Role) (access.RecordMembershipKey, error) {
	if err := refuseFakeRecordRole(at, role); err != nil {
		return access.RecordMembershipKey{}, err
	}

	return access.NewRecordMembershipKey(at, user, role), nil
}

// RecordGroupRole answers the rule on a group's role on a record as the library does: the
// person's rule, and the group kept in the record's tenant.
func (u *fakeUserManager) RecordGroupRole(_ context.Context, group accesstypes.Group, kept accesstypes.PolicyScope, at accesstypes.RecordScope, role accesstypes.Role) (access.RecordGroupRoleKey, error) {
	if err := refuseFakeRecordRole(at, role); err != nil {
		return access.RecordGroupRoleKey{}, err
	}
	if kept != accesstypes.DomainPolicyScope(at.Domain()) {
		return access.RecordGroupRoleKey{}, httpio.NewBadRequestMessage("A role on one project is given to a person or to a group of the project's tenant.")
	}

	return access.NewRecordGroupRoleKey(group, at, role), nil
}

// RolesHeldOn lists the roles the role file marks for the kind.
func (u *fakeUserManager) RolesHeldOn(record accesstypes.Resource) []access.RoleInfo {
	if record != projects {
		return nil
	}
	roles := make([]access.RoleInfo, 0, len(heldOnProjects))
	for _, role := range heldOnProjects {
		roles = append(roles, access.RoleInfo{Name: role})
	}

	return roles
}

// refuseFakeRecordRole is the fake library's rule on a role held on a record.
func refuseFakeRecordRole(at accesstypes.RecordScope, role accesstypes.Role) error {
	if at.Resource() != projects {
		return httpio.NewBadRequestMessagef("No role can be held on one %s. Give the role in the tenant instead.", at.Resource())
	}
	if !slices.Contains(heldOnProjects, role) {
		return httpio.NewBadRequestMessagef("%s is not a role held on a project.", role)
	}

	return nil
}

func (f *fakeStore) SetRecordMembership(_ context.Context, _ resource.ReadWriteTransaction, key *access.RecordMembershipKey, period accesstypes.Period) error {
	if err := f.fail("SetRecordMembership"); err != nil {
		return err
	}
	f.writes = append(f.writes, "set record membership "+recordMembershipText(key)+" "+periodText(period))

	return nil
}

func (f *fakeStore) DeleteRecordMembership(_ context.Context, _ resource.ReadWriteTransaction, key *access.RecordMembershipKey) error {
	if err := f.fail("DeleteRecordMembership"); err != nil {
		return err
	}
	f.writes = append(f.writes, "delete record membership "+recordMembershipText(key))

	return nil
}

func (f *fakeStore) SetRecordGroupRole(_ context.Context, _ resource.ReadWriteTransaction, key *access.RecordGroupRoleKey, period accesstypes.Period) error {
	if err := f.fail("SetRecordGroupRole"); err != nil {
		return err
	}
	f.writes = append(f.writes, "set record group role "+recordGroupRoleText(key)+" "+periodText(period))

	return nil
}

func (f *fakeStore) DeleteRecordGroupRole(_ context.Context, _ resource.ReadWriteTransaction, key *access.RecordGroupRoleKey) error {
	if err := f.fail("DeleteRecordGroupRole"); err != nil {
		return err
	}
	f.writes = append(f.writes, "delete record group role "+recordGroupRoleText(key))

	return nil
}

func (f *fakeStore) RecordRoles(_ context.Context, _ resource.ReadWriteTransaction, at accesstypes.RecordScope) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {
	if err := f.fail("RecordRoles"); err != nil {
		return nil, nil, err
	}
	record := access.NewRecordMembershipKey(at, "", "")

	return f.recordRows(func(axis, domain, resource, id string) bool {
		return axis == record.Axis && domain == record.Domain && resource == record.Resource && id == record.Record
	})
}

func (f *fakeStore) TenantRecordRoles(_ context.Context, _ resource.ReadWriteTransaction, domain accesstypes.Domain) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {
	if err := f.fail("TenantRecordRoles"); err != nil {
		return nil, nil, err
	}

	return f.recordRows(func(axis, held, _, _ string) bool {
		return axis == "" && held == string(domain)
	})
}

func (f *fakeStore) RecordMemberships(_ context.Context, _ resource.ReadWriteTransaction, userID impulse.UUID) ([]access.RecordMembershipKey, error) {
	if err := f.fail("RecordMemberships"); err != nil {
		return nil, err
	}
	now := time.Now()
	var keys []access.RecordMembershipKey
	for key, period := range f.recordRoles {
		if key.User == User(userID) && !period.Ended(now) {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b access.RecordMembershipKey) int { return compareRecordMemberships(&a, &b) })

	return keys, nil
}

// recordRows lists the rows of both tables whose record the function admits, in key order.
func (f *fakeStore) recordRows(admits func(axis, domain, resource, id string) bool) ([]access.RecordMembershipKey, []access.RecordGroupRoleKey, error) {
	var people []access.RecordMembershipKey
	for key := range f.recordRoles {
		if admits(key.Axis, key.Domain, key.Resource, key.Record) {
			people = append(people, key)
		}
	}
	slices.SortFunc(people, func(a, b access.RecordMembershipKey) int { return compareRecordMemberships(&a, &b) })
	var groups []access.RecordGroupRoleKey
	for key := range f.recordGroupRoles {
		if admits(key.Axis, key.Domain, key.Resource, key.Record) {
			groups = append(groups, key)
		}
	}
	slices.SortFunc(groups, func(a, b access.RecordGroupRoleKey) int {
		return cmp.Compare(recordGroupRoleText(&a), recordGroupRoleText(&b))
	})

	return people, groups, nil
}

// compareRecordMemberships orders a person's roles on records by their text.
func compareRecordMemberships(a, b *access.RecordMembershipKey) int {
	return cmp.Compare(recordMembershipText(a), recordMembershipText(b))
}

// recordMembershipText spells a person's role on a record for the fake's log of writes.
func recordMembershipText(key *access.RecordMembershipKey) string {
	return key.Domain + " " + key.Resource + "/" + key.Record + " " + string(key.User) + " " + string(key.Role)
}

// recordGroupRoleText spells a group's role on a record for the fake's log of writes.
func recordGroupRoleText(key *access.RecordGroupRoleKey) string {
	return key.Domain + " " + key.Resource + "/" + key.Record + " " + string(key.Group) + " " + string(key.Role)
}
