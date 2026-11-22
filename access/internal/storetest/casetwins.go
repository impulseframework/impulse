package storetest

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/access/internal/policy"
	"github.com/impulseframework/impulse/accesstypes"
)

// RoleWriter writes a custom role row straight to the store's roles table, with
// no rule: the pair of roles that differ only in case a store written before
// InsertRole refused them may hold, which no method of the store makes. Each
// store's tests hand Run their own.
type RoleWriter func(t *testing.T, scope accesstypes.PolicyScope, role accesstypes.Role, description string)

// The fixtures of the case-twin phase: names no other phase writes, so the
// phase's refusals answer its own rows alone. The phase deletes every role it
// adds.
const (
	zed       = accesstypes.Role("Zed")
	zedUpper  = accesstypes.Role("ZED")
	twinRound = 16

	twinDescription = "Held so that its case twin is refused."
)

// runRoleCaseTwins pins InsertRole's refusal of a name that differs only in
// case from a custom role held where the two are read side by side: in the
// scope itself, between one domain and every domain in either order, and of
// two such names inserted at the same moment, one is held and the other is
// refused. It pins as well what is not refused: the global partition beside a
// domain, two domains, the same exact name in a place that meets, and the
// re-insert of a row that is there.
func runRoleCaseTwins(t *testing.T, store access.Store, writeRole RoleWriter) {
	t.Helper()

	cases := []struct {
		name     string
		held     policy.Role
		scope    accesstypes.PolicyScope
		role     accesstypes.Role
		wantTwin bool
	}{
		{name: "a twin in the scope itself is refused", held: policy.Role{Scope: tenant1, Name: zed}, scope: tenant1, role: zedUpper, wantTwin: true},
		{name: "a role held in every domain refuses its twin in one domain", held: policy.Role{Scope: everyDomain, Name: zed}, scope: tenant1, role: zedUpper, wantTwin: true},
		{name: "a role held in one domain refuses its twin in every domain", held: policy.Role{Scope: tenant1, Name: zed}, scope: everyDomain, role: zedUpper, wantTwin: true},
		{name: "a twin in the global partition of a role held in a domain lands", held: policy.Role{Scope: tenant1, Name: zed}, scope: globalScope, role: zedUpper},
		{name: "a twin in another domain lands", held: policy.Role{Scope: tenant1, Name: zed}, scope: tenant2, role: zedUpper},
		{name: "the same exact name in a place that meets lands", held: policy.Role{Scope: everyDomain, Name: zed}, scope: tenant1, role: zed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			if err := store.InsertRole(ctx, tc.held.Scope, tc.held.Name, twinDescription); err != nil {
				t.Fatalf("InsertRole(%s, %q) error = %v", tc.held.Scope, tc.held.Name, err)
			}
			defer deleteRoles(t, store, tc.held, policy.Role{Scope: tc.scope, Name: tc.role})

			err := store.InsertRole(ctx, tc.scope, tc.role, twinDescription)
			landed := roleExists(t, store, tc.scope, tc.role)
			if !tc.wantTwin {
				if err != nil || !landed {
					t.Errorf("InsertRole(%s, %q) = %v, landed %v; want nil and the row", tc.scope, tc.role, err, landed)
				}

				return
			}
			var twin *policy.CaseTwinError
			if !errors.As(err, &twin) || twin.Held != tc.held {
				t.Errorf("InsertRole(%s, %q) error = %v, want a *policy.CaseTwinError naming %v", tc.scope, tc.role, err, tc.held)
			}
			if landed {
				t.Errorf("InsertRole(%s, %q) wrote the refused row", tc.scope, tc.role)
			}
		})
	}

	t.Run("a re-insert of a role that has a twin from before is the no-op", func(t *testing.T) {
		ctx := t.Context()
		writeRole(t, tenant1, zed, twinDescription)
		writeRole(t, tenant1, zedUpper, twinDescription)
		defer deleteRoles(t, store, policy.Role{Scope: tenant1, Name: zed}, policy.Role{Scope: tenant1, Name: zedUpper})

		if err := store.InsertRole(ctx, tenant1, zed, "A second description, which must not land."); err != nil {
			t.Fatalf("InsertRole() re-insert error = %v, want the no-op", err)
		}
		rows, err := store.ListRoles(ctx, tenant1)
		if err != nil {
			t.Fatalf("ListRoles() error = %v", err)
		}
		for _, row := range rows {
			if row.Name == zed && row.Description != twinDescription {
				t.Errorf("InsertRole() re-insert changed the description to %q", row.Description)
			}
		}
	})

	t.Run("two twins inserted at the same moment", func(t *testing.T) {
		for round := range twinRound {
			first := policy.Role{Scope: tenant1, Name: accesstypes.Role(fmt.Sprintf("Twin%02d", round))}
			second := policy.Role{Scope: tenant1, Name: accesstypes.Role(strings.ToUpper(string(first.Name)))}
			if round%2 == 1 {
				second.Scope = everyDomain
			}
			insertTogether(t, store, first, second)
		}
	})
}

// insertTogether inserts two names that differ only in case, in places that
// meet, from two goroutines released together, and checks that exactly one is
// held and the other refused as its twin. It deletes both.
func insertTogether(t *testing.T, store access.Store, first, second policy.Role) {
	t.Helper()
	ctx := t.Context()
	defer deleteRoles(t, store, first, second)

	roles := [2]policy.Role{first, second}
	var errs [2]error
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, role := range roles {
		wg.Go(func() {
			<-start
			errs[i] = store.InsertRole(ctx, role.Scope, role.Name, twinDescription)
		})
	}
	close(start)
	wg.Wait()

	held := -1
	for i, err := range errs {
		if err == nil {
			if held >= 0 {
				t.Errorf("InsertRole(%v) and InsertRole(%v) at the same moment both answered nil", roles[0], roles[1])
			}
			held = i
		}
	}
	if held < 0 {
		t.Fatalf("InsertRole(%v) and InsertRole(%v) at the same moment both failed: %v; %v", roles[0], roles[1], errs[0], errs[1])
	}
	refused := 1 - held
	var twin *policy.CaseTwinError
	if !errors.As(errs[refused], &twin) || twin.Held != roles[held] {
		t.Errorf("InsertRole(%v) error = %v, want a *policy.CaseTwinError naming %v", roles[refused], errs[refused], roles[held])
	}
	if !roleExists(t, store, roles[held].Scope, roles[held].Name) {
		t.Errorf("InsertRole(%v) answered nil and its row is not there", roles[held])
	}
	if roleExists(t, store, roles[refused].Scope, roles[refused].Name) {
		t.Errorf("InsertRole(%v) was refused and its row is there", roles[refused])
	}
}

// roleExists reports whether the (scope, role) row is there.
func roleExists(t *testing.T, store access.Store, scope accesstypes.PolicyScope, role accesstypes.Role) bool {
	t.Helper()
	exists, err := store.RoleExists(t.Context(), scope, role)
	if err != nil {
		t.Fatalf("RoleExists(%s, %q) error = %v", scope, role, err)
	}

	return exists
}

// deleteRoles deletes the rows a check of the phase wrote, so the phases after
// it read the store as the phases before it left it. A row that is not there
// is no failure.
func deleteRoles(t *testing.T, store access.Store, roles ...policy.Role) {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	for _, role := range roles {
		if _, err := store.DeleteRole(ctx, role.Scope, role.Name); err != nil {
			t.Errorf("DeleteRole(%s, %q) error = %v", role.Scope, role.Name, err)
		}
	}
}
