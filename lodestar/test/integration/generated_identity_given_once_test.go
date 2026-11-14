// Demonstrates: OrSkip.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// TestGeneratedIdentityRoleGivenOnce holds the membership write to what a
// create-unless-there promises (the generated store writes the row through the create
// patch's OrSkip): a role given twice in one transaction is written once and the commit
// lands, where two plain creates of the row are refused; and a role the person holds
// already is left exactly as it is, the time it was given included.
func TestGeneratedIdentityRoleGivenOnce(t *testing.T) {
	t.Parallel()
	w := newIdentityWorld(t)
	ctx := sessionContext(t, "governor")
	gina := accesstypes.User("gina-" + identitySuffix())

	// One transaction creates the person and gives the role twice.
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		if _, err := w.createCrewUser(ctx, txn, string(gina), "CrewCommon"); err != nil {
			return err
		}
		if err := w.assignCrewRole(ctx, txn, gina, "Headquarters"); err != nil {
			return err
		}

		return w.assignCrewRole(ctx, txn, gina, "Headquarters")
	}); err != nil {
		t.Fatalf("a role given twice in one transaction: %v", err)
	}
	given := readColumn[time.Time](ctx, t, w.db, "CrewUserRoles", crewMembershipKey("Headquarters", gina), "CreatedAt")
	if given.IsZero() {
		t.Fatal("the membership row carries no time")
	}

	// Given again in a later transaction, the row is left as it is.
	if err := w.inTransaction(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		return w.assignCrewRole(ctx, txn, gina, "Headquarters")
	}); err != nil {
		t.Fatalf("a role given to a person who holds it: %v", err)
	}
	if again := readColumn[time.Time](ctx, t, w.db, "CrewUserRoles", crewMembershipKey("Headquarters", gina), "CreatedAt"); !again.Equal(given) {
		t.Errorf("the membership's time moved from %v to %v: a role held already is left as it is", given, again)
	}
}
