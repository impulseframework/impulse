package storetest

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// The groups the suite keeps: one in tenant1 and one for the whole
// application, each under an id the suite chose as a store's caller does, and a
// third the phase creates and deletes again.
const (
	nightGroup = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-000000000001")
	staffGroup = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-000000000002")
	spareGroup = accesstypes.Group("7f2f6a52-6c63-4a7a-9c1e-000000000003")

	nightName        = "Night shift"
	nightDescription = "The people who work after dark."
	staffName        = "Staff"
	staffDescription = "The application's own people."
)

// equateScopes compares PolicyScope, a comparable struct with unexported
// fields, by ==.
var equateScopes = cmpopts.EquateComparable(accesstypes.PolicyScope{})

// runGroups holds the group tables to their contract: a group is kept in the
// global partition or in one domain and nowhere else; its name is held to one
// group where it is kept and free in every other place; a member and a role
// each need the group's row; inserts are idempotent and deletes of absent rows
// no-ops; a group with a member or a role cannot be deleted; and the listings
// are sorted. It leaves the two groups runReadPolicy expects.
func runGroups(t *testing.T, store access.Store) {
	t.Helper()
	ctx := t.Context()

	night := &policy.GroupRow{Group: policy.Group{ID: nightGroup, Scope: tenant1}, Name: nightName, Description: nightDescription}
	staff := &policy.GroupRow{Group: policy.Group{ID: staffGroup, Scope: globalScope}, Name: staffName, Description: staffDescription}
	for _, group := range []*policy.GroupRow{night, staff} {
		if taken, err := store.InsertGroup(ctx, group); err != nil || taken {
			t.Fatalf("InsertGroup(%s) = (%v, %v), want it written", group.Name, taken, err)
		}
	}

	t.Run("where a group is kept and what its name is held to", func(t *testing.T) {
		everywhere := &policy.GroupRow{Group: policy.Group{ID: spareGroup, Scope: everyDomain}, Name: "Everyone", Description: "Everybody."}
		if _, err := store.InsertGroup(ctx, everywhere); err == nil {
			t.Error("InsertGroup() kept in every domain: want it refused")
		}
		sameID := &policy.GroupRow{Group: policy.Group{ID: nightGroup, Scope: tenant2}, Name: "Day shift", Description: nightDescription}
		if taken, err := store.InsertGroup(ctx, sameID); err != nil || !taken {
			t.Errorf("InsertGroup() under an id that is taken = (%v, %v), want taken", taken, err)
		}
		sameName := &policy.GroupRow{Group: policy.Group{ID: spareGroup, Scope: tenant1}, Name: nightName, Description: nightDescription}
		if taken, err := store.InsertGroup(ctx, sameName); err != nil || !taken {
			t.Errorf("InsertGroup() under a name taken in the same domain = (%v, %v), want taken", taken, err)
		}
		// The same name kept in another domain is another group.
		elsewhere := &policy.GroupRow{Group: policy.Group{ID: spareGroup, Scope: tenant2}, Name: nightName, Description: nightDescription}
		if taken, err := store.InsertGroup(ctx, elsewhere); err != nil || taken {
			t.Fatalf("InsertGroup() under a name taken in another domain = (%v, %v), want it written", taken, err)
		}
		if deleted, err := store.DeleteGroup(ctx, spareGroup); err != nil || !deleted {
			t.Fatalf("DeleteGroup() of an empty group = (%v, %v), want it deleted", deleted, err)
		}
		if deleted, err := store.DeleteGroup(ctx, spareGroup); err != nil || deleted {
			t.Errorf("DeleteGroup() of a group that is gone = (%v, %v), want (false, nil)", deleted, err)
		}
	})

	t.Run("reading and describing a group", func(t *testing.T) {
		got, found, err := store.Group(ctx, nightGroup)
		if err != nil || !found {
			t.Fatalf("Group() = (found %v, %v), want the group", found, err)
		}
		if diff := cmp.Diff(night, got, equateScopes); diff != "" {
			t.Errorf("Group() (-want +got):\n%s", diff)
		}
		if _, found, err := store.Group(ctx, spareGroup); err != nil || found {
			t.Errorf("Group() of no group = (found %v, %v), want not found", found, err)
		}
		const described = "Who works after dark, since the reorganization."
		if found, err := store.SetGroupDescription(ctx, nightGroup, described); err != nil || !found {
			t.Fatalf("SetGroupDescription() = (%v, %v), want the group found", found, err)
		}
		if found, err := store.SetGroupDescription(ctx, spareGroup, described); err != nil || found {
			t.Errorf("SetGroupDescription() of no group = (%v, %v), want (false, nil)", found, err)
		}
		listed, err := store.ListGroups(ctx, tenant1)
		if err != nil {
			t.Fatalf("ListGroups() error = %v", err)
		}
		want := []policy.GroupRow{{Group: night.Group, Name: nightName, Description: described}}
		if diff := cmp.Diff(want, listed, equateScopes); diff != "" {
			t.Errorf("ListGroups(tenant1) (-want +got):\n%s", diff)
		}
		if listed, err := store.ListGroups(ctx, tenant2); err != nil || len(listed) != 0 {
			t.Errorf("ListGroups(tenant2) = (%v, %v), want none: a group is listed where it is kept", listed, err)
		}
		if _, err := store.SetGroupDescription(ctx, nightGroup, nightDescription); err != nil {
			t.Fatalf("SetGroupDescription() error = %v", err)
		}
	})

	t.Run("members", func(t *testing.T) {
		if err := store.InsertGroupMember(ctx, spareGroup, alice); err == nil {
			t.Error("InsertGroupMember() into a group that does not exist: want it refused")
		}
		for _, user := range []accesstypes.User{bob, alice, alice} {
			if err := store.InsertGroupMember(ctx, nightGroup, user); err != nil {
				t.Fatalf("InsertGroupMember(%s) error = %v", user, err)
			}
		}
		for _, group := range []accesstypes.Group{staffGroup, staffGroup} {
			if err := store.InsertGroupMember(ctx, group, carol); err != nil {
				t.Fatalf("InsertGroupMember() error = %v", err)
			}
		}
		if err := store.InsertGroupMember(ctx, staffGroup, alice); err != nil {
			t.Fatalf("InsertGroupMember() error = %v", err)
		}
		members, err := store.ListGroupMembers(ctx, nightGroup)
		if err != nil {
			t.Fatalf("ListGroupMembers() error = %v", err)
		}
		if diff := cmp.Diff([]accesstypes.User{alice, bob}, members); diff != "" {
			t.Errorf("ListGroupMembers(night) (-want +got):\n%s", diff)
		}
		groups, err := store.ListUserGroups(ctx, alice)
		if err != nil {
			t.Fatalf("ListUserGroups() error = %v", err)
		}
		if diff := cmp.Diff([]policy.GroupRow{*night, *staff}, groups, equateScopes); diff != "" {
			t.Errorf("ListUserGroups(alice) (-want +got):\n%s", diff)
		}
		// Removing alice from the staff, twice: the second is a no-op.
		for range 2 {
			if err := store.DeleteGroupMember(ctx, staffGroup, alice); err != nil {
				t.Fatalf("DeleteGroupMember() error = %v", err)
			}
		}
		if groups, err := store.ListUserGroups(ctx, alice); err != nil || len(groups) != 1 || groups[0].ID != nightGroup {
			t.Errorf("ListUserGroups(alice) after leaving the staff = (%v, %v), want the night shift alone", groups, err)
		}
	})

	t.Run("roles", func(t *testing.T) {
		if err := store.InsertGroupRole(ctx, spareGroup, tenant1, viewer); err == nil {
			t.Error("InsertGroupRole() for a group that does not exist: want it refused")
		}
		held := []struct {
			group accesstypes.Group
			scope accesstypes.PolicyScope
			role  accesstypes.Role
		}{
			{nightGroup, tenant1, viewer},
			{nightGroup, tenant1, viewer},
			{staffGroup, everyDomain, chief},
			{staffGroup, tenant2, editor},
			{staffGroup, globalScope, admin},
		}
		for _, h := range held {
			if err := store.InsertGroupRole(ctx, h.group, h.scope, h.role); err != nil {
				t.Fatalf("InsertGroupRole(%s, %s) error = %v", h.scope, h.role, err)
			}
		}
		groupSubject := policy.Subject{Kind: policy.SubjectGroup, Name: string(staffGroup)}
		roles, err := store.ListGroupRoles(ctx, staffGroup)
		if err != nil {
			t.Fatalf("ListGroupRoles() error = %v", err)
		}
		want := []policy.Membership{
			{Scope: tenant2, Member: groupSubject, Role: editor},
			{Scope: everyDomain, Member: groupSubject, Role: chief},
			{Scope: globalScope, Member: groupSubject, Role: admin},
		}
		if diff := cmp.Diff(want, roles, equateScopes); diff != "" {
			t.Errorf("ListGroupRoles(staff) (-want +got):\n%s", diff)
		}
		// Taking the staff's global role away, twice: the second is a no-op.
		for range 2 {
			if err := store.DeleteGroupRole(ctx, staffGroup, globalScope, admin); err != nil {
				t.Fatalf("DeleteGroupRole() error = %v", err)
			}
		}

		through, err := store.ListUserGroupRoles(ctx, carol)
		if err != nil {
			t.Fatalf("ListUserGroupRoles() error = %v", err)
		}
		wantThrough := []policy.GroupRole{
			{Group: staff.Group, Scope: tenant2, Role: editor},
			{Group: staff.Group, Scope: everyDomain, Role: chief},
		}
		if diff := cmp.Diff(wantThrough, through, equateScopes); diff != "" {
			t.Errorf("ListUserGroupRoles(carol) (-want +got):\n%s", diff)
		}
		through, err = store.ListUserGroupRoles(ctx, alice)
		if err != nil {
			t.Fatalf("ListUserGroupRoles() error = %v", err)
		}
		if diff := cmp.Diff([]policy.GroupRole{{Group: night.Group, Scope: tenant1, Role: viewer}}, through, equateScopes); diff != "" {
			t.Errorf("ListUserGroupRoles(alice) (-want +got):\n%s", diff)
		}
	})

	t.Run("a group goes once it is emptied", func(t *testing.T) {
		spare := &policy.GroupRow{Group: policy.Group{ID: spareGroup, Scope: tenant2}, Name: "Spare", Description: "Made to be deleted."}
		if taken, err := store.InsertGroup(ctx, spare); err != nil || taken {
			t.Fatalf("InsertGroup() = (%v, %v), want it written", taken, err)
		}
		if err := store.InsertGroupMember(ctx, spareGroup, bob); err != nil {
			t.Fatalf("InsertGroupMember() error = %v", err)
		}
		if _, err := store.DeleteGroup(ctx, spareGroup); err == nil {
			t.Fatal("DeleteGroup() of a group with a member: want it refused")
		}
		if err := store.DeleteGroupMember(ctx, spareGroup, bob); err != nil {
			t.Fatalf("DeleteGroupMember() error = %v", err)
		}
		if err := store.InsertGroupRole(ctx, spareGroup, tenant2, editor); err != nil {
			t.Fatalf("InsertGroupRole() error = %v", err)
		}
		if _, err := store.DeleteGroup(ctx, spareGroup); err == nil {
			t.Fatal("DeleteGroup() of a group with a role: want it refused")
		}
		if _, found, err := store.Group(ctx, spareGroup); err != nil || !found {
			t.Fatalf("Group() after the refused deletes = (found %v, %v), want the group still there", found, err)
		}
		if err := store.DeleteGroupRole(ctx, spareGroup, tenant2, editor); err != nil {
			t.Fatalf("DeleteGroupRole() error = %v", err)
		}
		if deleted, err := store.DeleteGroup(ctx, spareGroup); err != nil || !deleted {
			t.Errorf("DeleteGroup() of the emptied group = (%v, %v), want it deleted", deleted, err)
		}
	})
}
