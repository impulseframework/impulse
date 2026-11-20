package identity

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// fakeGroupTables are the group tables of the fake store: the committed groups by id,
// the people in each and the roles each holds. The zero value holds none.
type fakeGroupTables struct {
	groups       map[impulse.UUID]*Group
	groupMembers map[access.GroupMemberKey]bool
	groupRoles   map[access.GroupRoleKey]bool
	// nextGroupID is the id InsertGroup answers.
	nextGroupID impulse.UUID
	// The periods of the committed rows that carry one (periods_test.go): a
	// membership's, a place's in a group and a group's role's.
	membershipPeriods map[access.MembershipKey]accesstypes.Period
	placePeriods      map[access.GroupMemberKey]accesstypes.Period
	rolePeriods       map[access.GroupRoleKey]accesstypes.Period
	// membershipReasons are the reasons of the committed memberships given with one
	// (reasons_test.go).
	membershipReasons map[access.MembershipKey]string
	// tenantPeople are the people of each tenant: the rows of the tenant's own list of
	// the auth's people.
	tenantPeople map[accesstypes.Domain][]impulse.UUID
	// databaseNow is the database's clock, which says what has ended; the wall clock
	// when nil.
	databaseNow func() time.Time
}

func groupRoleText(key *access.GroupRoleKey) string {
	return string(key.Group) + " " + key.Kind + "/" + key.Domain + " " + string(key.Role)
}

func (f *fakeStore) InsertGroup(_ context.Context, _ resource.ReadWriteTransaction, group *access.GroupRow) (impulse.UUID, error) {
	if err := f.fail("InsertGroup"); err != nil {
		return impulse.NilUUID, err
	}
	f.writes = append(f.writes, "insert group "+group.Kind+"/"+group.Domain+" "+group.Name+": "+group.Description)

	return f.nextGroupID, nil
}

func (f *fakeStore) Group(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) (*Group, bool, error) {
	if err := f.fail("Group"); err != nil {
		return nil, false, err
	}
	group, found := f.groups[id]

	return group, found, nil
}

func (f *fakeStore) GroupNamed(_ context.Context, _ resource.ReadWriteTransaction, place *access.GroupRow) (bool, error) {
	if err := f.fail("GroupNamed"); err != nil {
		return false, err
	}
	for _, group := range f.groups {
		if group.Kind == place.Kind && group.Axis == place.Axis && group.Domain == place.Domain && group.Name == place.Name {
			return true, nil
		}
	}

	return false, nil
}

func (f *fakeStore) DescribeGroup(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID, description string) error {
	if err := f.fail("DescribeGroup"); err != nil {
		return err
	}
	f.writes = append(f.writes, "describe group "+id.String()+": "+description)

	return nil
}

func (f *fakeStore) DeleteGroup(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) error {
	if err := f.fail("DeleteGroup"); err != nil {
		return err
	}
	f.writes = append(f.writes, "delete group "+id.String())

	return nil
}

func (f *fakeStore) TenantGroups(_ context.Context, _ resource.ReadWriteTransaction, domain accesstypes.Domain) ([]impulse.UUID, error) {
	if err := f.fail("TenantGroups"); err != nil {
		return nil, err
	}
	var ids []impulse.UUID
	for id, group := range f.groups {
		if group.Kind == "domain" && group.Domain == string(domain) {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b impulse.UUID) int { return strings.Compare(a.String(), b.String()) })

	return ids, nil
}

func (f *fakeStore) GroupMembers(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) ([]accesstypes.User, error) {
	if err := f.fail("GroupMembers"); err != nil {
		return nil, err
	}
	var users []accesstypes.User
	for key, in := range f.groupMembers {
		if in && key.Group == GroupID(id) {
			users = append(users, key.User)
		}
	}
	slices.Sort(users)

	return users, nil
}

func (f *fakeStore) HasGroupMember(_ context.Context, _ resource.ReadWriteTransaction, key *access.GroupMemberKey) (bool, error) {
	if err := f.fail("HasGroupMember"); err != nil {
		return false, err
	}

	return f.groupMembers[*key], nil
}

func (f *fakeStore) InsertGroupMember(_ context.Context, _ resource.ReadWriteTransaction, key *access.GroupMemberKey) (bool, error) {
	if err := f.fail("InsertGroupMember"); err != nil {
		return false, err
	}
	if f.groupMembers[*key] {
		return false, nil
	}
	f.writes = append(f.writes, "insert group member "+string(key.Group)+" "+string(key.User))

	return true, nil
}

func (f *fakeStore) DeleteGroupMember(_ context.Context, _ resource.ReadWriteTransaction, key *access.GroupMemberKey) error {
	if err := f.fail("DeleteGroupMember"); err != nil {
		return err
	}
	f.writes = append(f.writes, "delete group member "+string(key.Group)+" "+string(key.User))

	return nil
}

func (f *fakeStore) UserGroups(_ context.Context, _ resource.ReadWriteTransaction, userID impulse.UUID) ([]GroupPlace, error) {
	if err := f.fail("UserGroups"); err != nil {
		return nil, err
	}
	var places []GroupPlace
	for id, group := range f.groups {
		if f.groupMembers[access.GroupMemberKey{Group: GroupID(id), User: User(userID)}] {
			places = append(places, GroupPlace{ID: id, Kind: group.Kind, Axis: group.Axis, Domain: group.Domain})
		}
	}
	slices.SortFunc(places, func(a, b GroupPlace) int { return strings.Compare(a.ID.String(), b.ID.String()) })

	return places, nil
}

func (f *fakeStore) GroupRoles(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) ([]access.GroupRoleKey, error) {
	if err := f.fail("GroupRoles"); err != nil {
		return nil, err
	}
	var keys []access.GroupRoleKey
	for key, held := range f.groupRoles {
		if held && key.Group == GroupID(id) {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b access.GroupRoleKey) int { return strings.Compare(groupRoleText(&a), groupRoleText(&b)) })

	return keys, nil
}

func (f *fakeStore) HasGroupRole(_ context.Context, _ resource.ReadWriteTransaction, key *access.GroupRoleKey) (bool, error) {
	if err := f.fail("HasGroupRole"); err != nil {
		return false, err
	}

	return f.groupRoles[*key], nil
}

func (f *fakeStore) InsertGroupRole(_ context.Context, _ resource.ReadWriteTransaction, key *access.GroupRoleKey) (bool, error) {
	if err := f.fail("InsertGroupRole"); err != nil {
		return false, err
	}
	if f.groupRoles[*key] {
		return false, nil
	}
	f.writes = append(f.writes, "insert group role "+groupRoleText(key))

	return true, nil
}

func (f *fakeStore) DeleteGroupRole(_ context.Context, _ resource.ReadWriteTransaction, key *access.GroupRoleKey) error {
	if err := f.fail("DeleteGroupRole"); err != nil {
		return err
	}
	f.writes = append(f.writes, "delete group role "+groupRoleText(key))

	return nil
}

func (f *fakeStore) TenantGroupRoles(_ context.Context, _ resource.ReadWriteTransaction, domain accesstypes.Domain) ([]access.GroupRoleKey, error) {
	if err := f.fail("TenantGroupRoles"); err != nil {
		return nil, err
	}
	var keys []access.GroupRoleKey
	for key, held := range f.groupRoles {
		if held && key.Kind == "domain" && key.Domain == string(domain) {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b access.GroupRoleKey) int { return strings.Compare(groupRoleText(&a), groupRoleText(&b)) })

	return keys, nil
}

func (f *fakeStore) GroupsHolding(_ context.Context, _ resource.ReadWriteTransaction, key *access.RoleKey) ([]impulse.UUID, error) {
	if err := f.fail("GroupsHolding"); err != nil {
		return nil, err
	}
	var ids []impulse.UUID
	for held, is := range f.groupRoles {
		if is && held.Kind == key.Kind && held.Axis == key.Axis && held.Domain == key.Domain && held.Role == key.Role {
			ids = append(ids, mustUUID(string(held.Group)))
		}
	}
	slices.SortFunc(ids, func(a, b impulse.UUID) int { return strings.Compare(a.String(), b.String()) })

	return ids, nil
}

func (f *fakeStore) TenantPerson(_ context.Context, _ resource.ReadWriteTransaction, domain accesstypes.Domain, userID impulse.UUID) (bool, error) {
	if err := f.fail("TenantPerson"); err != nil {
		return false, err
	}

	return slices.Contains(f.tenantPeople[domain], userID), nil
}

// GroupRole answers the rule on a group's role as the library does: the Super
// Administrator role refused, a tenant's group held to its tenant, and the role one the
// role file or the roles table holds there.
func (u *fakeUserManager) GroupRole(ctx context.Context, group accesstypes.Group, kept, scope accesstypes.PolicyScope, role accesstypes.Role, held access.RoleReader) (access.GroupRoleKey, error) {
	if role == accesstypes.SuperAdministrator {
		return access.GroupRoleKey{}, httpio.NewBadRequestMessage("the Super Administrator role is given to a person, never to a group")
	}
	if _, inTenant := kept.Domain(); inTenant && scope != kept {
		return access.GroupRoleKey{}, httpio.NewBadRequestMessage("a group kept in one tenant holds roles in that tenant alone")
	}
	key := access.NewGroupRoleKey(group, scope, role)
	if !slices.Contains(u.engine.roles[scope], role) {
		custom, err := held(ctx, access.RoleKey{Kind: key.Kind, Axis: key.Axis, Domain: key.Domain, Role: role})
		if err != nil {
			return access.GroupRoleKey{}, err
		}
		if !custom {
			return access.GroupRoleKey{}, httpio.NewNotFoundMessagef("role %q is not a valid role. Please check that the role exists.", role)
		}
	}

	return key, nil
}

var (
	// nightID is a group kept in alpha, staffID one kept for the whole application, and
	// dayID one kept in beta; newGroupID is the id a created group takes.
	nightID    = mustUUID("0190b000-0000-7000-8000-000000000001")
	staffID    = mustUUID("0190b000-0000-7000-8000-000000000002")
	dayID      = mustUUID("0190b000-0000-7000-8000-000000000003")
	newGroupID = mustUUID("0190b000-0000-7000-8000-000000000009")
)

// groupStore is a fake store holding the three groups, with dana an account of the auth.
func groupStore() *fakeStore {
	return &fakeStore{
		memberships: map[access.MembershipKey]bool{},
		roles:       map[access.RoleKey]bool{},
		usernames:   map[impulse.UUID]string{userID: "dana", otherUser: "gale"},
		fakeGroupTables: fakeGroupTables{
			groups: map[impulse.UUID]*Group{
				nightID: {Kind: "domain", Domain: "alpha", Name: "Night shift"},
				staffID: {Kind: "global", Name: "Staff"},
				dayID:   {Kind: "domain", Domain: "beta", Name: "Day shift"},
			},
			groupMembers: map[access.GroupMemberKey]bool{},
			groupRoles:   map[access.GroupRoleKey]bool{},
			nextGroupID:  newGroupID,
		},
	}
}

func memberKey(group, user impulse.UUID) access.GroupMemberKey {
	return access.GroupMemberKey{Group: GroupID(group), User: User(user)}
}

// TestManager_CreateGroup pins a group's creation: the permission store's rules on where
// it is kept and what it is called, a name taken where it is kept refused, the row
// buffered, and nobody's permissions changed by it.
func TestManager_CreateGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		scope       accesstypes.PolicyScope
		group       string
		description string
		wantErr     func(error) bool
		wantWrites  []string
	}{
		{
			name:  "a group kept in a tenant",
			scope: alphaScope, group: " Dispatch ", description: "Who sends the crews out.",
			wantWrites: []string{"insert group domain/alpha Dispatch: Who sends the crews out."},
		},
		{
			name:  "a group kept for the whole application",
			scope: globalScope, group: "Support", description: "The support desk.",
			wantWrites: []string{"insert group global/ Support: The support desk."},
		},
		{
			name:  "the name of a group kept in another tenant is free",
			scope: alphaScope, group: "Day shift", description: "Alpha's own.",
			wantWrites: []string{"insert group domain/alpha Day shift: Alpha's own."},
		},
		{name: "a name taken where the group is kept", scope: alphaScope, group: "Night shift", description: "Again.", wantErr: httpio.HasConflict},
		{name: "kept in every tenant", scope: everyScope, group: "Everyone", description: "Everybody.", wantErr: httpio.HasBadRequest},
		{name: "no name", scope: globalScope, group: " ", description: "Nobody.", wantErr: httpio.HasBadRequest},
		{name: "no description", scope: globalScope, group: "Support", description: "", wantErr: httpio.HasBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			m, engine := newManager(t, store, engineRoles)

			var id impulse.UUID
			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				var err error
				id, err = m.CreateGroup(ctx, txn, tt.scope, tt.group, tt.description)

				return err
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("CreateGroup() error = %v, want the refusal", err)
				}
			} else if err != nil || id != newGroupID {
				t.Fatalf("CreateGroup() = (%s, %v), want the new group's id", id, err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != 0 {
				t.Errorf("the engine was told %d time(s): an empty group changes nobody's permissions", engine.changed)
			}
		})
	}
}

// TestManager_DeleteGroup pins that a group goes once it is emptied: a group with a
// member or a role is refused as a conflict that says what to do, and writes nothing;
// an emptied one is deleted and the engine told once the transaction has committed.
func TestManager_DeleteGroup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		group       impulse.UUID
		members     []access.GroupMemberKey
		roles       []access.GroupRoleKey
		wantErr     func(error) bool
		wantWrites  []string
		wantChanged int
	}{
		{name: "an empty group is deleted", group: nightID, wantWrites: []string{"delete group " + nightID.String()}, wantChanged: 1},
		{name: "a group with a member is refused", group: nightID, members: []access.GroupMemberKey{memberKey(nightID, userID)}, wantErr: httpio.HasConflict},
		{name: "a group with a role is refused", group: nightID, roles: []access.GroupRoleKey{access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")}, wantErr: httpio.HasConflict},
		{name: "another group's member does not hold this one", group: nightID, members: []access.GroupMemberKey{memberKey(staffID, userID)}, wantWrites: []string{"delete group " + nightID.String()}, wantChanged: 1},
		{name: "a group that does not exist", group: newGroupID, wantErr: httpio.HasNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			for _, key := range tt.members {
				store.groupMembers[key] = true
			}
			for _, key := range tt.roles {
				store.groupRoles[key] = true
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.DeleteGroup(ctx, txn, tt.group)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("DeleteGroup() error = %v, want the refusal", err)
				}
				if httpio.HasConflict(err) && !strings.Contains(err.Error(), "remove its members and take its roles away") {
					t.Errorf("DeleteGroup() error = %v, want it to say what to do", err)
				}
			} else if err != nil {
				t.Fatalf("DeleteGroup() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// TestManager_AddGroupMember pins a person's place in a group: the row buffered under
// the group's id and the id of the person's account, a person who is in the group left
// as they are, a group that does not exist refused, and the engine told once the
// transaction has committed.
func TestManager_AddGroupMember(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		group       impulse.UUID
		user        impulse.UUID
		in          []access.GroupMemberKey
		wantErr     func(error) bool
		wantWrites  []string
		wantChanged int
	}{
		{name: "a person is put in a tenant's group", group: nightID, user: userID, wantWrites: []string{"insert group member " + nightID.String() + " " + userID.String()}, wantChanged: 1},
		{name: "a person is put in a group of the whole application", group: staffID, user: userID, wantWrites: []string{"insert group member " + staffID.String() + " " + userID.String()}, wantChanged: 1},
		{name: "a person who is in the group is left as they are", group: nightID, user: userID, in: []access.GroupMemberKey{memberKey(nightID, userID)}},
		{name: "a group that does not exist", group: newGroupID, user: userID, wantErr: httpio.HasNotFound},
		{name: "nobody", group: nightID, user: impulse.NilUUID, wantErr: httpio.HasBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			for _, key := range tt.in {
				store.groupMembers[key] = true
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.AddGroupMember(ctx, txn, tt.group, tt.user)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("AddGroupMember() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("AddGroupMember() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// TestManager_RemoveGroupMember pins a person's removal from a group, and the rule on an
// account nothing admits: a person left with no role of their own and no place in a
// group is deleted with the removal, and one who holds either stays.
func TestManager_RemoveGroupMember(t *testing.T) {
	t.Parallel()

	removal := "delete group member " + nightID.String() + " " + userID.String()
	tests := []struct {
		name        string
		in          []access.GroupMemberKey
		held        []access.MembershipKey
		wantWrites  []string
		wantChanged int
	}{
		{name: "a person who is not in the group is no error"},
		{
			name:        "the last thing a person held: the account goes with the removal",
			in:          []access.GroupMemberKey{memberKey(nightID, userID)},
			wantWrites:  []string{"delete user " + userID.String(), removal},
			wantChanged: 1,
		},
		{
			name:       "a person in another group stays",
			in:         []access.GroupMemberKey{memberKey(nightID, userID), memberKey(staffID, userID)},
			wantWrites: []string{removal}, wantChanged: 1,
		},
		{
			name:       "a person with a role of their own stays",
			in:         []access.GroupMemberKey{memberKey(nightID, userID)},
			held:       []access.MembershipKey{access.NewMembershipKey(betaScope, dana, "Reader")},
			wantWrites: []string{removal}, wantChanged: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			for _, key := range tt.in {
				store.groupMembers[key] = true
			}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.RemoveGroupMember(ctx, txn, nightID, userID)
			})
			if err != nil {
				t.Fatalf("RemoveGroupMember() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// TestManager_RemoveRole_keepsAnAccountInAGroup pins that a place in a group keeps an
// account as a role does: the person's last role of their own goes, and the account
// stays while they are in a group.
func TestManager_RemoveRole_keepsAnAccountInAGroup(t *testing.T) {
	t.Parallel()

	reader := access.NewMembershipKey(alphaScope, dana, "Reader")
	for _, inGroup := range []bool{true, false} {
		store := groupStore()
		store.memberships[reader] = true
		if inGroup {
			store.groupMembers[memberKey(staffID, userID)] = true
		}
		m, _ := newManager(t, store, engineRoles)

		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.RemoveRole(ctx, txn, alphaScope, userID, "Reader")
		})
		if err != nil {
			t.Fatalf("RemoveRole() error = %v", err)
		}
		deleted := slices.Contains(store.writes, "delete user "+userID.String())
		if deleted == inGroup {
			t.Errorf("in a group = %v: account deleted = %v, want an account kept exactly while its person is in a group (writes %v)", inGroup, deleted, store.writes)
		}
	}
}

// TestManager_GiveGroupRole pins a group's role: the permission engine's rule first (no
// Super Administrator, a tenant's group in its tenant alone, a role that exists there),
// a role the group holds left as it is, the row buffered, and the engine told once the
// transaction has committed.
func TestManager_GiveGroupRole(t *testing.T) {
	t.Parallel()

	night, staff := GroupID(nightID), GroupID(staffID)
	tests := []struct {
		name        string
		group       impulse.UUID
		scope       accesstypes.PolicyScope
		role        accesstypes.Role
		held        []access.GroupRoleKey
		custom      []access.RoleKey
		wantErr     func(error) bool
		wantWrites  []string
		wantChanged int
	}{
		{name: "a tenant's group, a role in its tenant", group: nightID, scope: alphaScope, role: "Reader", wantWrites: []string{"insert group role " + string(night) + " domain/alpha Reader"}, wantChanged: 1},
		{name: "a group of the whole application, a global role", group: staffID, scope: globalScope, role: "Steward", wantWrites: []string{"insert group role " + string(staff) + " global/ Steward"}, wantChanged: 1},
		{name: "a group of the whole application, a role in every tenant", group: staffID, scope: everyScope, role: "Reader", wantWrites: []string{"insert group role " + string(staff) + " every/ Reader"}, wantChanged: 1},
		{name: "a group of the whole application, a role in one tenant", group: staffID, scope: alphaScope, role: "Reader", wantWrites: []string{"insert group role " + string(staff) + " domain/alpha Reader"}, wantChanged: 1},
		{
			name: "a custom role is read from the roles table in the transaction", group: nightID, scope: alphaScope, role: "Auditor",
			custom:     []access.RoleKey{{Kind: "domain", Domain: "alpha", Role: "Auditor"}},
			wantWrites: []string{"insert group role " + string(night) + " domain/alpha Auditor"}, wantChanged: 1,
		},
		{name: "a role the group holds is left as it is", group: nightID, scope: alphaScope, role: "Reader", held: []access.GroupRoleKey{access.NewGroupRoleKey(night, alphaScope, "Reader")}},
		{name: "a tenant's group, another tenant", group: nightID, scope: betaScope, role: "Reader", wantErr: httpio.HasBadRequest},
		{name: "a tenant's group, every tenant", group: nightID, scope: everyScope, role: "Reader", wantErr: httpio.HasBadRequest},
		{name: "a tenant's group, the whole application", group: nightID, scope: globalScope, role: "Steward", wantErr: httpio.HasBadRequest},
		{name: "the Super Administrator role", group: staffID, scope: globalScope, role: accesstypes.SuperAdministrator, wantErr: httpio.HasBadRequest},
		{name: "a role that does not exist there", group: staffID, scope: globalScope, role: "Reader", wantErr: httpio.HasNotFound},
		{name: "a group that does not exist", group: newGroupID, scope: globalScope, role: "Steward", wantErr: httpio.HasNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			for _, key := range tt.held {
				store.groupRoles[key] = true
			}
			for _, key := range tt.custom {
				store.roles[key] = true
			}
			m, engine := newManager(t, store, engineRoles)

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return m.GiveGroupRole(ctx, txn, tt.group, tt.scope, tt.role)
			})
			if tt.wantErr != nil {
				if err == nil || !tt.wantErr(err) {
					t.Fatalf("GiveGroupRole() error = %v, want the refusal", err)
				}
			} else if err != nil {
				t.Fatalf("GiveGroupRole() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
			if engine.changed != tt.wantChanged {
				t.Errorf("the engine was told %d time(s), want %d", engine.changed, tt.wantChanged)
			}
		})
	}
}

// TestManager_TakeGroupRole pins the removal of a group's role: the row goes and the
// engine is told; a role the group does not hold is no error and tells nobody.
func TestManager_TakeGroupRole(t *testing.T) {
	t.Parallel()

	key := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")
	for _, held := range []bool{true, false} {
		store := groupStore()
		store.groupRoles[key] = held
		m, engine := newManager(t, store, engineRoles)

		err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return m.TakeGroupRole(ctx, txn, nightID, alphaScope, "Reader")
		})
		if err != nil {
			t.Fatalf("TakeGroupRole() error = %v", err)
		}
		var want []string
		wantChanged := 0
		if held {
			want, wantChanged = []string{"delete group role " + groupRoleText(&key)}, 1
		}
		if diff := cmp.Diff(want, store.writes); diff != "" {
			t.Errorf("held = %v: buffered writes (-want +got):\n%s", held, diff)
		}
		if engine.changed != wantChanged {
			t.Errorf("held = %v: the engine was told %d time(s), want %d", held, engine.changed, wantChanged)
		}
	}
}

// TestManager_oneTenantPerPerson_groups pins that one tenant per person counts a place
// in a tenant's group as a role in that tenant: a person in one tenant's group is given
// no role and no place in another tenant, a person who holds a role in one tenant no
// place in another's group, and a place in a group kept for the whole application makes
// a person the application's own, who is given a role anywhere.
func TestManager_oneTenantPerPerson_groups(t *testing.T) {
	t.Parallel()

	refusedElsewhere := func(err error) bool {
		var refusal *sessioninfo.LoginRefusal

		return httpio.HasConflict(err) && errors.As(err, &refusal)
	}
	tests := []struct {
		name    string
		in      []access.GroupMemberKey
		held    []access.MembershipKey
		act     func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		refused bool
	}{
		{
			name: "a person in alpha's group is given no role in beta",
			in:   []access.GroupMemberKey{memberKey(nightID, userID)},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AssignRole(ctx, txn, betaScope, userID, "Reader")
			},
			refused: true,
		},
		{
			name: "a person in alpha's group is given a role in alpha",
			in:   []access.GroupMemberKey{memberKey(nightID, userID)},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AssignRole(ctx, txn, alphaScope, userID, "Reader")
			},
		},
		{
			name: "a person in alpha's group is given no place in beta's group",
			in:   []access.GroupMemberKey{memberKey(nightID, userID)},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AddGroupMember(ctx, txn, dayID, userID)
			},
			refused: true,
		},
		{
			name: "a person with a role in alpha is given no place in beta's group",
			held: []access.MembershipKey{access.NewMembershipKey(alphaScope, dana, "Reader")},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AddGroupMember(ctx, txn, dayID, userID)
			},
			refused: true,
		},
		{
			name: "a person in a group of the whole application is given a place in any tenant's group",
			in:   []access.GroupMemberKey{memberKey(staffID, userID), memberKey(nightID, userID)},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AddGroupMember(ctx, txn, dayID, userID)
			},
		},
		{
			name: "a person with a role in alpha is given a place in a group of the whole application",
			held: []access.MembershipKey{access.NewMembershipKey(alphaScope, dana, "Reader")},
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AddGroupMember(ctx, txn, staffID, userID)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := groupStore()
			for _, key := range tt.in {
				store.groupMembers[key] = true
			}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			roles := map[accesstypes.PolicyScope][]accesstypes.Role{alphaScope: {"Reader"}, betaScope: {"Reader"}}
			m, _ := newManager(t, store, roles, WithOneTenantPerPerson())

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.act(ctx, txn, m)
			})
			if tt.refused {
				if err == nil || !refusedElsewhere(err) {
					t.Fatalf("error = %v, want the refusal of a second tenant", err)
				}
				if len(store.writes) != 0 {
					t.Errorf("buffered writes = %v, want none", store.writes)
				}
			} else if err != nil {
				t.Fatalf("error = %v, want the act through", err)
			}
		})
	}
}

// TestManager_DeleteUser_removesGroupPlaces pins that an account that goes leaves no
// place in a group behind, and that the engine is told for a person who held nothing but
// such a place.
func TestManager_DeleteUser_removesGroupPlaces(t *testing.T) {
	t.Parallel()

	store := groupStore()
	store.groupMembers[memberKey(nightID, userID)] = true
	store.groupMembers[memberKey(staffID, userID)] = true
	store.groupMembers[memberKey(staffID, otherUser)] = true
	m, engine := newManager(t, store, engineRoles)

	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.DeleteUser(ctx, txn, userID)
	})
	if err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
	want := []string{
		"delete user " + userID.String(),
		"delete group member " + nightID.String() + " " + userID.String(),
		"delete group member " + staffID.String() + " " + userID.String(),
	}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told %d time(s), want once", engine.changed)
	}
}

// TestManager_ClearTenant_groups pins what a tenant that goes takes of groups: its own
// groups with their members and roles, and the roles a group of the whole application
// holds in it. A person who held nothing but a place in the tenant's group goes with it,
// and one who holds anything else stays.
func TestManager_ClearTenant_groups(t *testing.T) {
	t.Parallel()

	night, staff := GroupID(nightID), GroupID(staffID)
	nightReader := access.NewGroupRoleKey(night, alphaScope, "Reader")
	staffInAlpha := access.NewGroupRoleKey(staff, alphaScope, "Reader")
	staffInBeta := access.NewGroupRoleKey(staff, betaScope, "Reader")

	store := groupStore()
	// dana is in alpha's group alone; gale is in alpha's group and in the staff.
	store.groupMembers[memberKey(nightID, userID)] = true
	store.groupMembers[memberKey(nightID, otherUser)] = true
	store.groupMembers[memberKey(staffID, otherUser)] = true
	for _, key := range []access.GroupRoleKey{nightReader, staffInAlpha, staffInBeta} {
		store.groupRoles[key] = true
	}
	m, engine := newManager(t, store, engineRoles)

	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.ClearTenant(ctx, txn, "alpha")
	})
	if err != nil {
		t.Fatalf("ClearTenant() error = %v", err)
	}
	want := []string{
		"delete group role " + groupRoleText(&nightReader),
		"delete group role " + groupRoleText(&staffInAlpha),
		"delete group member " + nightID.String() + " " + userID.String(),
		"delete group member " + nightID.String() + " " + otherUser.String(),
		"delete group " + nightID.String(),
		"delete user " + userID.String(),
	}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
	if engine.changed != 1 {
		t.Errorf("the engine was told %d time(s), want once", engine.changed)
	}
}

// TestTenant_groups pins what makes a group method a tenant's: it reaches the groups
// kept in the tenant and no other, a group kept elsewhere being not found whoever asks,
// and it gives a role in the tenant alone.
func TestTenant_groups(t *testing.T) {
	t.Parallel()

	acts := map[string]func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant, group impulse.UUID) error{
		"describe": func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant, group impulse.UUID) error {
			return tenant.DescribeGroup(ctx, txn, group, "Who works after dark.")
		},
		"delete": func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant, group impulse.UUID) error {
			return tenant.DeleteGroup(ctx, txn, group)
		},
		"add a member": func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant, group impulse.UUID) error {
			return tenant.AddGroupMember(ctx, txn, group, userID)
		},
		"remove a member": func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant, group impulse.UUID) error {
			return tenant.RemoveGroupMember(ctx, txn, group, userID)
		},
		"give a role": func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant, group impulse.UUID) error {
			return tenant.GiveGroupRole(ctx, txn, group, "Reader")
		},
		"take a role": func(ctx context.Context, txn resource.ReadWriteTransaction, tenant *Tenant, group impulse.UUID) error {
			return tenant.TakeGroupRole(ctx, txn, group, "Reader")
		},
	}
	for name, act := range acts {
		for _, group := range []impulse.UUID{nightID, staffID, dayID, newGroupID} {
			store := groupStore()
			store.tenantPeople = map[accesstypes.Domain][]impulse.UUID{"alpha": {userID}}
			m, _ := newManager(t, store, engineRoles)
			tenant := &Tenant{manager: m, domain: "alpha"}

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return act(ctx, txn, tenant, group)
			})
			if group == nightID {
				if err != nil {
					t.Errorf("%s, the tenant's own group: error = %v", name, err)
				}

				continue
			}
			if err == nil || !httpio.HasNotFound(err) || len(store.writes) != 0 {
				t.Errorf("%s, group %s kept elsewhere: error = %v, writes = %v; want not found and nothing written", name, group, err, store.writes)
			}
		}
	}

	store := groupStore()
	m, _ := newManager(t, store, engineRoles)
	tenant := &Tenant{manager: m, domain: "alpha"}
	err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if _, err := tenant.CreateGroup(ctx, txn, "Dispatch", "Who sends the crews out."); err != nil {
			return err
		}

		return tenant.GiveGroupRole(ctx, txn, nightID, "Reader")
	})
	if err != nil {
		t.Fatalf("Tenant.CreateGroup(), GiveGroupRole() error = %v", err)
	}
	want := []string{
		"insert group domain/alpha Dispatch: Who sends the crews out.",
		"insert group role " + string(GroupID(nightID)) + " domain/alpha Reader",
	}
	if diff := cmp.Diff(want, store.writes); diff != "" {
		t.Errorf("buffered writes (-want +got):\n%s", diff)
	}
}

// TestManager_groups_noWayToTheSuperAdministratorRole pins that a group is no way around
// the rules of the Super Administrator role. The role is refused for a group whoever
// asks, a super administrator among them. And where a row saying a group holds it stands
// in the table all the same, written past the rules, a person in that group is no super
// administrator: they give the role to nobody, an administrator who is none acts on how
// they sign in as on anybody's, and putting a person in the group ends nothing of
// theirs, since no role comes with the place that only its holder gives.
func TestManager_groups_noWayToTheSuperAdministratorRole(t *testing.T) {
	t.Parallel()

	staffSuper := access.NewGroupRoleKey(GroupID(staffID), globalScope, accesstypes.SuperAdministrator)
	// dana is a super administrator. erin is a steward and in the staff group, which the
	// row written past the rules says holds the role. frank is an account that holds
	// nothing.
	newStore := func() *fakeStore {
		store := groupStore()
		store.usernames[frankID] = "frank"
		store.disabled = map[impulse.UUID]bool{}
		store.memberships[danaSuper] = true
		store.memberships[erinSteward] = true
		store.groupRoles[staffSuper] = true
		store.groupMembers[memberKey(staffID, otherUser)] = true

		return store
	}
	as := func(user accesstypes.User) func(context.Context) context.Context {
		return func(ctx context.Context) context.Context { return asCallerThrough(ctx, managedAuth, user) }
	}
	tests := []struct {
		name string
		ctx  func(context.Context) context.Context
		// clean leaves the row written past the rules out of the store.
		clean   bool
		act     func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		wantErr func(error) bool
		// wantSays is a part of the sentence a refusal reads.
		wantSays   string
		wantWrites []string
	}{
		{
			name: "a super administrator gives the role to no group", ctx: as(dana), clean: true,
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.GiveGroupRole(ctx, txn, staffID, globalScope, accesstypes.SuperAdministrator)
			},
			wantErr: httpio.HasBadRequest, wantSays: "never to a group",
		},
		{
			name: "a person in a group that a row says holds the role makes nobody a super administrator", ctx: as(erin),
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AssignRole(ctx, txn, globalScope, frankID, accesstypes.SuperAdministrator)
			},
			wantErr: httpio.HasForbidden, wantSays: "Only a super administrator gives the Super Administrator role",
		},
		{
			name: "nor changes how a super administrator signs in", ctx: as(erin),
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RenameUser(ctx, txn, userID, "dana.r")
			},
			wantErr: httpio.HasForbidden, wantSays: "Only a super administrator renames a super administrator",
		},
		{
			name: "and is nobody a super administrator alone acts on", ctx: as(User(frankID)),
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.RenameUser(ctx, txn, otherUser, "gale.r")
			},
			wantWrites: []string{"update user " + otherUser.String() + " username=gale.r"},
		},
		{
			name: "a place in the group ends nothing the person holds", ctx: as(dana),
			act: func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
				return m.AddGroupMember(ctx, txn, staffID, frankID)
			},
			wantWrites: []string{"insert group member " + staffID.String() + " " + frankID.String()},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := newStore()
			if tt.clean {
				delete(store.groupRoles, staffSuper)
			}
			m, _ := newManager(t, store, superRoles, WithRoleGrants(&fakeGrants{}))

			err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return tt.act(tt.ctx(ctx), txn, m)
			})
			switch {
			case tt.wantErr == nil && err != nil:
				t.Fatalf("error = %v, want the act done", err)
			case tt.wantErr != nil && (err == nil || !tt.wantErr(err) || !strings.Contains(err.Error(), tt.wantSays)):
				t.Fatalf("error = %v, want the refusal that says %q", err, tt.wantSays)
			}
			if diff := cmp.Diff(tt.wantWrites, store.writes); diff != "" {
				t.Errorf("buffered writes (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_groups_removalNotice pins the notice of a removal from a tenant where an
// administrator takes a person's last role in it through a group: by taking the person
// out of the group, or by taking the role from the group. It is mailed after the
// commit, to the address the person confirmed, and only when nothing else of theirs
// reaches the tenant.
func TestManager_groups_removalNotice(t *testing.T) {
	t.Parallel()

	reader := access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Reader")
	takeOut := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.RemoveGroupMember(ctx, txn, nightID, userID)
	}
	takeRole := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.TakeGroupRole(ctx, txn, nightID, alphaScope, "Reader")
	}
	// takeOwn takes the Clerk role the person holds of their own in the tenant.
	takeOwn := func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error {
		return m.RemoveRole(ctx, txn, alphaScope, userID, "Clerk")
	}
	// A second group of the tenant, for the cases where the person stands in two.
	eveningID := mustUUID("0190b000-0000-7000-8000-000000000004")
	over := accesstypes.Period{Start: time.Time{}, End: time.Now().Add(-time.Hour)}
	ownClerk := access.NewMembershipKey(alphaScope, dana, "Clerk")
	everywhere := access.NewMembershipKey(accesstypes.EveryDomainPolicyScope(), dana, "Clerk")
	tests := []struct {
		name   string
		act    func(ctx context.Context, txn resource.ReadWriteTransaction, m *Manager) error
		caller accesstypes.User
		// held are the person's own memberships, and roles the roles the group holds
		// beside Reader in alpha.
		held  []access.MembershipKey
		roles []access.GroupRoleKey
		// alsoIn is a second group the person stands in, the nil id for none, and its
		// are the roles that group holds.
		alsoIn impulse.UUID
		its    []access.GroupRoleKey
		// placeOver and roleOver say that the person's place in the group, or the
		// group's Reader role, is of a period that is over.
		placeOver, roleOver bool
		// unconfirmed is a person who has confirmed no address, and both a group where
		// the administrator who acts stands beside the person, with an address of their
		// own.
		unconfirmed, both bool
		wantAsked         []string
	}{
		{
			name: "taken out of the group that gave their one role in the tenant, with nothing else anywhere: the account goes too", act: takeOut, caller: erin,
			wantAsked: []string{"removal notice to dana@example.com from alpha deleted=true"},
		},
		{
			name: "taken out of the group, with a role of their own in another tenant: the account stays", act: takeOut, caller: erin,
			held:      []access.MembershipKey{access.NewMembershipKey(betaScope, dana, "Reader")},
			wantAsked: []string{"removal notice to dana@example.com from alpha deleted=false"},
		},
		{
			name: "taken out of the group while a role of their own holds them in the tenant is no removal from it", act: takeOut, caller: erin,
			held: []access.MembershipKey{access.NewMembershipKey(alphaScope, dana, "Clerk")},
		},
		{name: "a person who takes themselves out of the group is told nothing", act: takeOut, caller: dana, held: []access.MembershipKey{access.NewMembershipKey(betaScope, dana, "Reader")}},
		{
			name: "the group's one role in the tenant is taken: everyone in the group it held there is told", act: takeRole, caller: erin,
			wantAsked: []string{"removal notice to dana@example.com from alpha deleted=false"},
		},
		{
			name: "the group keeps another role in the tenant: taking one is no removal from it", act: takeRole, caller: erin,
			roles: []access.GroupRoleKey{access.NewGroupRoleKey(GroupID(nightID), alphaScope, "Clerk")},
		},
		{
			name: "the group's role is taken while a role of their own holds the person in the tenant", act: takeRole, caller: erin,
			held: []access.MembershipKey{access.NewMembershipKey(alphaScope, dana, "Clerk")},
		},
		{
			name: "taken out of the group while another group holds them in the tenant is no removal from it", act: takeOut, caller: erin,
			alsoIn: eveningID, its: []access.GroupRoleKey{access.NewGroupRoleKey(GroupID(eveningID), alphaScope, "Clerk")},
		},
		{
			name: "the group's role is taken while another group holds the person in the tenant", act: takeRole, caller: erin,
			alsoIn: eveningID, its: []access.GroupRoleKey{access.NewGroupRoleKey(GroupID(eveningID), alphaScope, "Clerk")},
		},
		{
			name: "a group of another tenant that holds them there does not keep them in this one", act: takeOut, caller: erin,
			alsoIn: dayID, its: []access.GroupRoleKey{access.NewGroupRoleKey(GroupID(dayID), betaScope, "Clerk")},
			wantAsked: []string{"removal notice to dana@example.com from alpha deleted=false"},
		},
		{
			name: "taken out of the group while a role held in every tenant reaches this one", act: takeOut, caller: erin,
			held: []access.MembershipKey{everywhere},
		},
		{
			name: "the group's role is taken while a role held in every tenant reaches this one", act: takeRole, caller: erin,
			held: []access.MembershipKey{everywhere},
		},
		{
			name: "a place whose period is over gave the person nothing: taking it is no removal from the tenant", act: takeOut, caller: erin,
			placeOver: true,
		},
		{
			name: "a role of the group whose period is over gave nobody anything: taking it is no removal", act: takeRole, caller: erin,
			roleOver: true,
		},
		{name: "taken out of the group with no confirmed address: nothing is mailed", act: takeOut, caller: erin, unconfirmed: true},
		{name: "the group's role is taken from a person with no confirmed address: nothing is mailed", act: takeRole, caller: erin, unconfirmed: true},
		{
			name: "the administrator who takes the group's role stands in the group too: they are told nothing, the others are", act: takeRole, caller: erin,
			both:      true,
			wantAsked: []string{"removal notice to dana@example.com from alpha deleted=false"},
		},
		{
			name: "a role of their own is taken while the group holds them in the tenant: no removal from it", act: takeOwn, caller: erin,
			held: []access.MembershipKey{ownClerk},
		},
		{
			name: "a role of their own is taken and their place in the group is over: the group no longer holds them, and its row keeps the account", act: takeOwn, caller: erin,
			held:      []access.MembershipKey{ownClerk},
			placeOver: true,
			wantAsked: []string{"removal notice to dana@example.com from alpha deleted=false"},
		},
		{
			name: "a role of their own is taken and the group's role is over: the group gives nothing in the tenant", act: takeOwn, caller: erin,
			held:      []access.MembershipKey{ownClerk},
			roleOver:  true,
			wantAsked: []string{"removal notice to dana@example.com from alpha deleted=false"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := groupStore()
			store.emails = map[string]impulse.UUID{"dana@example.com": userID}
			store.groupMembers[memberKey(nightID, userID)] = true
			store.groupRoles[reader] = true
			for _, key := range tt.roles {
				store.groupRoles[key] = true
			}
			for _, key := range tt.held {
				store.memberships[key] = true
			}
			store.groups[eveningID] = &Group{Kind: "domain", Domain: "alpha", Name: "Evening shift"}
			if tt.alsoIn != impulse.NilUUID {
				store.groupMembers[memberKey(tt.alsoIn, userID)] = true
				for _, key := range tt.its {
					store.groupRoles[key] = true
				}
			}
			if tt.placeOver {
				store.placePeriods = map[access.GroupMemberKey]accesstypes.Period{memberKey(nightID, userID): over}
			}
			if tt.roleOver {
				store.rolePeriods = map[access.GroupRoleKey]accesstypes.Period{reader: over}
			}
			if tt.unconfirmed {
				store.emails = nil
			}
			if tt.both {
				store.emails["gale@example.com"] = otherUser
				store.groupMembers[memberKey(nightID, otherUser)] = true
			}
			mail := &fakeMail{}
			m, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, fakeSessions{mail: mail}, administrators, WithRoleGrants(&fakeGrants{}))
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				err := tt.act(asCaller(ctx, tt.caller), txn, m)
				if len(mail.asked) != 0 {
					t.Errorf("before the commit the session library was asked %v, want nothing", mail.asked)
				}

				return err
			}); err != nil {
				t.Fatalf("error = %v", err)
			}
			if diff := cmp.Diff(tt.wantAsked, mail.asked); diff != "" {
				t.Errorf("notices after the commit (-want +got):\n%s", diff)
			}
		})
	}
}

// TestManager_TakeGroupRole_ofNoTenant pins what the notice of a removal costs where
// none can be owed: a role a group holds for the whole application is no tenant's, so
// taking it reads nobody in the group for a notice, and tells nobody.
func TestManager_TakeGroupRole_ofNoTenant(t *testing.T) {
	t.Parallel()

	store := groupStore()
	store.emails = map[string]impulse.UUID{"dana@example.com": userID}
	store.groupMembers[memberKey(staffID, userID)] = true
	store.groupRoles[access.NewGroupRoleKey(GroupID(staffID), globalScope, "Reader")] = true
	// What a notice reads of each person in the group fails here: the removal passes
	// only when nobody is read.
	store.failOn = "UserPeriods"
	mail := &fakeMail{}
	m, err := New(managedAuth, store, &fakeEngine{roles: engineRoles}, fakeSessions{mail: mail}, administrators, WithRoleGrants(&fakeGrants{}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := inTransaction(t, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return m.TakeGroupRole(asCaller(ctx, erin), txn, staffID, globalScope, "Reader")
	}); err != nil {
		t.Fatalf("TakeGroupRole() of a role held for the whole application error = %v, want nil with nobody read for a notice", err)
	}
	if len(mail.asked) != 0 {
		t.Errorf("the session library was asked %v, want nothing: the role is no tenant's", mail.asked)
	}
}
