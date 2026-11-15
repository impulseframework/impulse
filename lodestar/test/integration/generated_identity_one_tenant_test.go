//go:build skipAuth

package integration

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/identity"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestGeneratedIdentityOneTenantPerPerson holds the rule of one tenant per person
// (identity.WithOneTenantPerPerson) over the crew's real tables. Lodestar itself lets a
// person hold roles in several sectors, so the suite builds a second identity client
// over the served stack's engine and session manager, with the option, and drives the
// generated methods and the two sign-in hooks through it in transactions of its own:
// a role in a second sector is refused whichever way it would be given, a rename onto a
// name that holds a role in another sector among them, the refusal never says which
// sector holds the person, nothing of a refused act is written or mailed, an address
// counts for an invitation once its holder has confirmed it, and the crew's own
// people, who hold a role for the whole fleet or in every sector, are outside the rule
// and keep their last such role while they hold roles in two sectors.
//
// Demonstrates: auth.generated-identity.
func TestGeneratedIdentityOneTenantPerPerson(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServed(ctx, t)

	strict, err := crewidentity.NewClient(s.access, s.crew.Session().API(), identity.WithOneTenantPerPerson())
	if err != nil {
		t.Fatalf("crewidentity.NewClient() with one tenant per person: %v", err)
	}
	client := resource.NewSpannerClient(s.db.Client)
	run := func(f func(ctx context.Context, txn resource.ReadWriteTransaction) error) error {
		return client.ExecuteFunc(ctx, f)
	}
	// refused fails unless err is the rule's refusal: a Conflict that carries the login
	// refusal code and names neither sector.
	refused := func(t *testing.T, what string, err error) {
		t.Helper()

		var refusal *sessioninfo.LoginRefusal
		if !httpio.HasConflict(err) || !errors.As(err, &refusal) || refusal.Code() != sessioninfo.RefusedOtherTenant {
			t.Fatalf("%s: error = %v, want a Conflict carrying the login refusal code %s", what, err, sessioninfo.RefusedOtherTenant)
		}
		message := strings.ToLower(httpio.Message(err))
		for _, sector := range []string{anvil, bastion, cinder} {
			if strings.Contains(message, sector) {
				t.Errorf("%s: the refusal %q names the sector %s", what, httpio.Message(err), sector)
			}
		}
	}
	mailedTo := func(address string) int {
		n := 0
		for _, message := range s.mail.Messages() {
			if message.To == address {
				n++
			}
		}

		return n
	}
	anvilSector, bastionSector := anvil, bastion
	suffix := identitySuffix()

	// odell is a person of Anvil, with an address an administrator typed.
	odell, odellAddress := "odell-"+suffix, "odell-"+suffix+"@example.com"
	var odellID impulse.UUID
	if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
		created, err := (&crewidentity.CreateCrewUser{Username: odell, Email: &odellAddress, Roles: []string{"Pilot"}, Domain: &anvilSector}).Execute(ctx, txn, strict)
		if err != nil {
			return err
		}
		odellID = created.ID

		return nil
	}); err != nil {
		t.Fatalf("creating a person in a sector: %v", err)
	}
	inAnvil := []string{"domain/anvil Pilot"}

	t.Run("a role in a second sector is refused, and one more in the person's own is given", func(t *testing.T) {
		err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return (&crewidentity.AssignCrewRole{Username: odell, Role: "Pilot", Domain: &bastionSector}).Execute(ctx, txn, strict)
		})
		refused(t, "a role in Bastion for a person of Anvil", err)
		if held := crewHolds(t, s.db, odell); !slices.Equal(held, inAnvil) {
			t.Errorf("after the refusal the person holds %v, want %v", held, inAnvil)
		}

		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return (&crewidentity.AssignCrewRole{Username: odell, Role: "Cadet", Domain: &anvilSector}).Execute(ctx, txn, strict)
		}); err != nil {
			t.Fatalf("one more role in the person's own sector: %v", err)
		}
		inAnvil = []string{"domain/anvil Cadet", "domain/anvil Pilot"}
		if held := crewHolds(t, s.db, odell); !slices.Equal(held, inAnvil) {
			t.Errorf("the person holds %v, want %v", held, inAnvil)
		}
	})

	t.Run("an account created in another sector under a name that holds a role is refused before anything is written", func(t *testing.T) {
		before := countRowsOf(ctx, t, s, "CrewSessionUsers", "Username", odell)
		err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := (&crewidentity.CreateCrewUser{Username: odell, Roles: []string{"Pilot"}, Domain: &bastionSector}).Execute(ctx, txn, strict)

			return err
		})
		refused(t, "an account in Bastion under the name of a person of Anvil", err)
		if after := countRowsOf(ctx, t, s, "CrewSessionUsers", "Username", odell); after != before {
			t.Errorf("the refused create left %d account(s) under the name, want %d", after, before)
		}
	})

	t.Run("an address nobody confirmed holds nothing against an invitation, which another sector sends", func(t *testing.T) {
		// piper is a person of Anvil whose address an administrator typed and nobody
		// confirmed: it says nothing of who reads its mail, so Anvil cannot keep Bastion
		// from inviting an address by writing it on one of its own accounts. Whoever
		// accepts is held to the rule then.
		piper, piperAddress := "piper-"+suffix, "piper-"+suffix+"@example.com"
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := (&crewidentity.CreateCrewUser{Username: piper, Email: &piperAddress, Roles: []string{"Pilot"}, Domain: &anvilSector}).Execute(ctx, txn, strict)

			return err
		}); err != nil {
			t.Fatalf("creating a person in a sector: %v", err)
		}
		var invited *crewidentity.InviteCrewUserResult
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			var err error
			invited, err = (&crewidentity.InviteCrewUser{Email: piperAddress, Roles: []string{"Pilot"}, Domain: &bastionSector}).Execute(ctx, txn, strict)

			return err
		}); err != nil {
			t.Fatalf("an invitation into Bastion for an address a person of Anvil holds unconfirmed: %v", err)
		}
		if !invited.Sent || mailedTo(piperAddress) != 1 {
			t.Errorf("the invitation: sent = %v with %d message(s), want it mailed once", invited.Sent, mailedTo(piperAddress))
		}
	})

	t.Run("an invitation into another sector is refused when it is sent, and one into the person's own goes", func(t *testing.T) {
		// odell confirms the address, as following the confirmation's link does.
		if _, err := s.db.Apply(ctx, []*spanner.Mutation{spanner.UpdateMap("CrewSessionUsers", map[string]any{"Id": odellID, "EmailVerifiedAt": time.Now()})}); err != nil {
			t.Fatalf("confirming the person's address: %v", err)
		}
		linksBefore := countRowsOf(ctx, t, s, "CrewSessionMailLinks", "Email", odellAddress)
		err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := (&crewidentity.InviteCrewUser{Email: odellAddress, Roles: []string{"Pilot"}, Domain: &bastionSector}).Execute(ctx, txn, strict)

			return err
		})
		refused(t, "an invitation into Bastion for the address of a person of Anvil", err)
		if !strings.Contains(httpio.Message(err), "an account already holds the address "+odellAddress) {
			t.Errorf("the refusal %q does not tell the sender the address has an account", httpio.Message(err))
		}
		if after := countRowsOf(ctx, t, s, "CrewSessionMailLinks", "Email", odellAddress); after != linksBefore {
			t.Errorf("the refused invitation left %d link(s) for the address, want %d", after, linksBefore)
		}
		if n := mailedTo(odellAddress); n != 0 {
			t.Errorf("the refused invitation mailed the address %d time(s)", n)
		}

		// The address is compared as the database compares one: its case does not hide the account.
		err = run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := (&crewidentity.InviteCrewUser{Email: strings.ToUpper(odellAddress), Roles: []string{"Pilot"}, Domain: &bastionSector}).Execute(ctx, txn, strict)

			return err
		})
		refused(t, "the same invitation with the address in upper case", err)

		var sameSector *crewidentity.InviteCrewUserResult
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			var err error
			sameSector, err = (&crewidentity.InviteCrewUser{Email: odellAddress, Roles: []string{"Dispatcher"}, Domain: &anvilSector}).Execute(ctx, txn, strict)

			return err
		}); err != nil {
			t.Fatalf("an invitation into the person's own sector: %v", err)
		}
		if !sameSector.Sent || mailedTo(odellAddress) != 1 {
			t.Errorf("the invitation into the person's own sector: sent = %v with %d message(s), want it mailed once", sameSector.Sent, mailedTo(odellAddress))
		}

		// Into the fleet as a whole, and for an address nobody holds, the rule has nothing to say.
		stranger := "stranger-" + suffix + "@example.com"
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			if _, err := (&crewidentity.InviteCrewUser{Email: odellAddress, Roles: []string{"ClientBrowser"}}).Execute(ctx, txn, strict); err != nil {
				return err
			}
			_, err := (&crewidentity.InviteCrewUser{Email: stranger, Roles: []string{"Pilot"}, Domain: &bastionSector}).Execute(ctx, txn, strict)

			return err
		}); err != nil {
			t.Fatalf("an invitation into the fleet for a person of a sector, and one into a sector for a stranger: %v", err)
		}
	})

	t.Run("a rename onto a name that holds a role in another sector is refused, and one onto a name of the person's own sector goes", func(t *testing.T) {
		// A role given to a name before it has an account stands until an account takes
		// the name: a rename is one way to take it, and it is held to the rule.
		elsewhere, here := "ghost-"+suffix, "odell-renamed-"+suffix
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			if err := (&crewidentity.AssignCrewRole{Username: elsewhere, Role: "Pilot", Domain: &bastionSector}).Execute(ctx, txn, strict); err != nil {
				return err
			}

			return (&crewidentity.AssignCrewRole{Username: here, Role: "Dispatcher", Domain: &anvilSector}).Execute(ctx, txn, strict)
		}); err != nil {
			t.Fatalf("giving roles to two names that have no account yet: %v", err)
		}
		before := crewHolds(t, s.db, odell)

		err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return (&crewidentity.RenameCrewUser{UserID: odellID, Username: elsewhere}).Execute(ctx, txn, strict)
		})
		refused(t, "a rename of a person of Anvil onto a name that holds a role in Bastion", err)
		if held := crewHolds(t, s.db, odell); !slices.Equal(held, before) {
			t.Errorf("after the refused rename the person holds %v, want %v", held, before)
		}
		if held := crewHolds(t, s.db, elsewhere); !slices.Equal(held, []string{"domain/bastion Pilot"}) {
			t.Errorf("after the refused rename the name holds %v, want its one role in Bastion", held)
		}
		if n := countRowsOf(ctx, t, s, "CrewSessionUsers", "Username", odell); n != 1 {
			t.Errorf("after the refused rename %d account(s) hold the old name, want the one", n)
		}

		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return (&crewidentity.RenameCrewUser{UserID: odellID, Username: here}).Execute(ctx, txn, strict)
		}); err != nil {
			t.Fatalf("a rename onto a name that holds a role in the person's own sector: %v", err)
		}
		want := append(slices.Clone(before), "domain/anvil Dispatcher")
		slices.Sort(want)
		if held := crewHolds(t, s.db, here); !slices.Equal(held, want) {
			t.Errorf("after the rename the person holds %v, want %v", held, want)
		}
		odell = here
	})

	t.Run("the crew's own people are outside the rule, and their last role outside the sectors stays while they hold roles in two", func(t *testing.T) {
		// staff holds a role for the whole fleet, as an operator does: a role in one sector
		// and then in another is given, and so is an invitation into a third.
		staff, staffAddress := "staff-"+suffix, "staff-"+suffix+"@example.com"
		cinderSector := cinder
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := (&crewidentity.CreateCrewUser{Username: staff, Email: &staffAddress, Roles: []string{"ClientBrowser"}}).Execute(ctx, txn, strict)

			return err
		}); err != nil {
			t.Fatalf("creating a person of the crew's own: %v", err)
		}
		for _, sector := range []*string{&bastionSector, &anvilSector} {
			if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
				return (&crewidentity.AssignCrewRole{Username: staff, Role: "Cadet", Domain: sector}).Execute(ctx, txn, strict)
			}); err != nil {
				t.Fatalf("a role in %s for a person who holds a role for the whole fleet: %v", *sector, err)
			}
		}
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := (&crewidentity.InviteCrewUser{Email: staffAddress, Roles: []string{"Pilot"}, Domain: &cinderSector}).Execute(ctx, txn, strict)

			return err
		}); err != nil {
			t.Fatalf("an invitation into a third sector for the address of a person who holds a role for the whole fleet: %v", err)
		}
		inBoth := []string{"domain/anvil Cadet", "domain/bastion Cadet", "global/ ClientBrowser"}
		if held := crewHolds(t, s.db, staff); !slices.Equal(held, inBoth) {
			t.Fatalf("the person holds %v, want %v", held, inBoth)
		}

		// The role for the whole fleet is what lets them hold roles in two sectors: it stays.
		err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return (&crewidentity.RemoveCrewRole{Username: staff, Role: "ClientBrowser"}).Execute(ctx, txn, strict)
		})
		if !httpio.HasConflict(err) || !strings.Contains(httpio.Message(err), "holds roles in more than one tenant") {
			t.Fatalf("taking the only role outside the sectors from a person of two sectors: error = %v, want a Conflict that says so", err)
		}
		for _, sector := range []string{anvil, bastion} {
			if strings.Contains(strings.ToLower(httpio.Message(err)), sector) {
				t.Errorf("the refusal %q names the sector %s", httpio.Message(err), sector)
			}
		}
		if held := crewHolds(t, s.db, staff); !slices.Equal(held, inBoth) {
			t.Errorf("after the refusal the person holds %v, want %v", held, inBoth)
		}

		// Down to one sector it goes, and the person is then that sector's: a role in
		// another is refused.
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return (&crewidentity.RemoveCrewRole{Username: staff, Role: "Cadet", Domain: &anvilSector}).Execute(ctx, txn, strict)
		}); err != nil {
			t.Fatalf("taking the role in Anvil: %v", err)
		}
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return (&crewidentity.RemoveCrewRole{Username: staff, Role: "ClientBrowser"}).Execute(ctx, txn, strict)
		}); err != nil {
			t.Fatalf("taking the role for the whole fleet from a person of one sector: %v", err)
		}
		err = run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return (&crewidentity.AssignCrewRole{Username: staff, Role: "Cadet", Domain: &anvilSector}).Execute(ctx, txn, strict)
		})
		refused(t, "a role in Anvil for a person who now belongs to Bastion alone", err)

		// A role for the whole fleet, and one in every sector, are given to a person of a sector.
		pilot := "pilot-" + suffix
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			_, err := (&crewidentity.CreateCrewUser{Username: pilot, Roles: []string{"Pilot"}, Domain: &anvilSector}).Execute(ctx, txn, strict)

			return err
		}); err != nil {
			t.Fatalf("creating a person of Anvil: %v", err)
		}
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			if err := (&crewidentity.AssignCrewRole{Username: pilot, Role: "ClientBrowser"}).Execute(ctx, txn, strict); err != nil {
				return err
			}

			return (&crewidentity.AssignCrewRole{Username: pilot, Role: "Pilot", EveryDomain: true}).Execute(ctx, txn, strict)
		}); err != nil {
			t.Fatalf("a role for the whole fleet and one in every sector, for a person of a sector: %v", err)
		}
	})

	t.Run("the roles of one sign-in are judged together, in the transaction the session library hands its hooks", func(t *testing.T) {
		newcomer := "newcomer-" + suffix
		seat := []signin.DirectorySeat{{Tenant: accesstypes.Domain(anvil), Role: "Cadet"}}
		invitation := func(sector, role string) *sessioninfo.Invitation {
			return &sessioninfo.Invitation{Email: newcomer + "@example.com", Tenant: sector, RoleNames: []string{role}}
		}
		// signIn runs the two hooks as a sign-in does: the directory's default role, then
		// the accepted invitation, in one transaction, and their committed functions
		// once it has committed.
		signIn := func(inv *sessioninfo.Invitation) error {
			var committed []func(context.Context)
			_, err := s.db.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
				committed = committed[:0]
				seated, err := strict.DirectorySeated()(ctx, tx, impulse.NilUUID, newcomer, seat)
				if err != nil {
					return err
				}
				accepted, err := strict.InvitationAccepted()(ctx, tx, impulse.NilUUID, newcomer, inv)
				if err != nil {
					return err
				}
				committed = append(committed, seated, accepted)

				return nil
			})
			if err != nil {
				return err
			}
			for _, f := range committed {
				f(ctx)
			}

			return nil
		}

		refused(t, "a stranger from Anvil's directory who carries Bastion's invitation", signIn(invitation(bastion, "Pilot")))
		if held := crewHolds(t, s.db, newcomer); len(held) != 0 {
			t.Errorf("the refused sign-in left the person holding %v, want nothing", held)
		}

		if err := signIn(invitation(anvil, "Pilot")); err != nil {
			t.Fatalf("a stranger from Anvil's directory who carries Anvil's invitation: %v", err)
		}
		want := []string{"domain/anvil Cadet", "domain/anvil Pilot"}
		if held := crewHolds(t, s.db, newcomer); !slices.Equal(held, want) {
			t.Errorf("after the sign-in the person holds %v, want %v", held, want)
		}

		// A later invitation into another sector is refused at its acceptance.
		_, err := s.db.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
			_, err := strict.InvitationAccepted()(ctx, tx, impulse.NilUUID, newcomer, invitation(bastion, "Pilot"))

			return err
		})
		refused(t, "Bastion's invitation accepted by a person who belongs to Anvil", err)
		if held := crewHolds(t, s.db, newcomer); !slices.Equal(held, want) {
			t.Errorf("after the refused acceptance the person holds %v, want %v", held, want)
		}
	})

	t.Run("without the option a person is given a role in a second sector, as Lodestar does", func(t *testing.T) {
		if err := run(func(ctx context.Context, txn resource.ReadWriteTransaction) error {
			return (&crewidentity.AssignCrewRole{Username: odell, Role: "Pilot", Domain: &bastionSector}).Execute(ctx, txn, s.crew.Identity())
		}); err != nil {
			t.Fatalf("a role in a second sector through Lodestar's own client: %v", err)
		}
		if held := crewHolds(t, s.db, odell); !slices.Contains(held, "domain/bastion Pilot") {
			t.Errorf("the person holds %v, want the role in Bastion among them", held)
		}
	})
}
