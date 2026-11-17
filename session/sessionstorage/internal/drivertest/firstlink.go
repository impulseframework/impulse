package drivertest

// An account can exist ahead of its first sign-in: an administrator creates a person
// and gives them a role before they have ever signed in, with no password where the
// person is to sign in through a directory. Such an account has no password and no
// link, so nothing but its name ties it to whoever signs in next under that name, and
// a directory asserts the names it likes: an organization's own OpenID Connect provider
// is held to no domain. In an application whose tenants have directories, the standard
// account rule matches the account by the sign-in name and the link is then made only
// when something vouches for it: the sign-in came through the directory of the one
// tenant the account belongs to, holding a role there and nothing anywhere else, or it
// carries the account's own invitation. Any other
// sign-in under the name is refused as a person no account was created for is. A
// registration that declares seeded users declares the provider's names the
// application's own, as a deployment's own provider is for the accounts it seeds, and
// links the account on the name alone. The cases run through the real sign-in flow on
// both drivers, one for each way an account exists ahead of a sign-in.

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// hilltopProvider is Hilltop's directory in the first-link cases: the organization's own
// OpenID Connect provider, which asserts whatever sign-in name and address it likes.
var hilltopProvider = signin.Directory{Method: sessioninfo.MethodOIDC, Connection: "id.hilltop.example"}

// throughProvider is an identity Hilltop's provider asserts: a subject of its own
// choosing under signInName, with email as the address it claims, unverified.
func throughProvider(subject, signInName, email string) *sessioninfo.Identity {
	return &sessioninfo.Identity{Method: hilltopProvider.Method, Connection: hilltopProvider.Connection, Subject: subject, SignInName: signInName, Email: email}
}

// firstLinkWorld is the application of the first-link cases: Riverside sets nothing
// and admits everyone it seats, and Hilltop's directory is its own provider, with the
// default seat the case names ("" for none: Hilltop then admits a stranger by
// invitation only).
func firstLinkWorld(hilltopSeat accesstypes.Role) *doorWorld {
	seats := map[accesstypes.Domain]accesstypes.Role{}
	if hilltopSeat != "" {
		seats["hilltop"] = hilltopSeat
	}

	return newDoorWorld(map[accesstypes.Domain]*signin.TenantDeclaration{"hilltop": {Directories: []signin.Directory{hilltopProvider}}}, seats)
}

// assertUntouched fails unless the account stands as it was created: no link under
// identity, and seated in want and nowhere else.
func assertUntouched(ctx context.Context, t *testing.T, w *doorWorld, d AuthDriver, user *dbtype.SessionUser, identity *sessioninfo.Identity, want ...accesstypes.Domain) {
	t.Helper()

	assertNoLink(ctx, t, d, identity)
	links, err := d.IdentitiesByUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("IdentitiesByUser() error = %v", err)
	}
	if len(links) != 0 {
		t.Errorf("the account holds %d links after the refused sign-in, want none", len(links))
	}
	if diff := cmp.Diff(want, w.seats.tenants(user.ID)); diff != "" {
		t.Errorf("tenants that seat the account after the refused sign-in (-want +got):\n%s", diff)
	}
}

func testFirstLinkByName(_ context.Context, t *testing.T, h *AuthHarness) {
	const name = "dana@riverside.example"

	// An administrator's create that gives no password: the account has neither a
	// password nor a link, and holds the role it was created with, in Riverside.
	createdAhead := func(ctx context.Context, t *testing.T, w *doorWorld, in *AuthInstance) *dbtype.SessionUser {
		t.Helper()

		user := createUser(ctx, t, in.Driver, name)
		if user.PasswordHash != nil {
			t.Fatal("the account created with no password holds one")
		}
		w.seats.give(user.ID, "riverside", "Teacher")

		return user
	}

	for _, hilltopSeat := range []accesstypes.Role{"", "Member"} {
		seatName := "that admits a stranger by invitation only"
		if hilltopSeat != "" {
			seatName = "that seats a stranger with its default role"
		}
		t.Run("an account created ahead with no password is not linked through another organization's own provider "+seatName, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			w := firstLinkWorld(hilltopSeat)
			in := w.start(ctx, t, h)
			dana := createdAhead(ctx, t, w, in)

			// Hilltop's provider asserts Dana's name. Dana holds no role in Hilltop, so
			// Hilltop's directory vouches for nothing, and the sign-in is refused as a
			// person no account was created for is, by Hilltop's own rule for one.
			identity := throughProvider("sub-claimed", name, name)
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
			err := signInRequest(ctx, in, req)
			assertRefused(t, err, sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
			if !req.UserID.IsNil() || req.User != nil {
				t.Errorf("the refused sign-in names user %v (%+v), want nobody: the refusal must not say the account exists", req.UserID, req.User)
			}
			if message := httpio.Message(err); strings.Contains(message, "account") || strings.Contains(message, name) {
				t.Errorf("the refusal says %q, want words that say nothing of an account", message)
			}
			assertUntouched(ctx, t, w, in.Driver, dana, identity, "riverside")

			// The same refusal, in the same words, as for a name no account was created
			// under, where the tenant admits a stranger by invitation only.
			if hilltopSeat == "" {
				stranger := throughProvider("sub-stranger", "nobody@riverside.example", "nobody@riverside.example")
				strangerErr := signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "nobody@riverside.example", Identity: stranger})
				assertRefused(t, strangerErr, sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
				if httpio.Message(strangerErr) != httpio.Message(err) {
					t.Errorf("the refusal of the account's name says %q and a stranger's %q, want the same words", httpio.Message(err), httpio.Message(strangerErr))
				}
			}
		})
	}

	t.Run("an account created ahead with no password is not linked through a directory no tenant has", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := firstLinkWorld("")
		in := w.start(ctx, t, h)
		dana := createdAhead(ctx, t, w, in)

		// A Workspace the deployment registered and no tenant has made its directory.
		identity := google("google-claimed", name)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
		err := signInRequest(ctx, in, req)
		assertRefused(t, err, sessioninfo.RefusedNotInvited, signin.ErrNotInvited)
		if !req.UserID.IsNil() || req.User != nil {
			t.Errorf("the refused sign-in names user %v (%+v), want nobody", req.UserID, req.User)
		}
		assertUntouched(ctx, t, w, in.Driver, dana, identity, "riverside")
	})

	t.Run("an account created ahead with no password is linked through the directory of the one tenant it belongs to, and of no tenant when it holds anything anywhere else, whatever that tenant's administrator gives it", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := firstLinkWorld("Member")
		in := w.start(ctx, t, h)
		refused := func(username, subject string) {
			t.Helper()

			claimed := throughProvider(subject, username, username)
			err := signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: username, Identity: claimed})
			assertRefused(t, err, sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
			assertNoLink(ctx, t, in.Driver, claimed)
		}

		// Staff of the application, created ahead with a role in every tenant: Hilltop's
		// provider vouches for the people who are Hilltop's alone, and this is not one.
		staff := createUser(ctx, t, in.Driver, "staff@operator.example")
		w.seats.mu.Lock()
		w.seats.held[accesstypes.User(staff.ID.String())] = accesstypes.RoleCollection{accesstypes.EveryDomainPolicyScope(): {"Support"}}
		w.seats.mu.Unlock()
		refused("staff@operator.example", "sub-staff")
		// Hilltop's administrator lists everyone a role in every tenant seats there, and
		// gives the account a role in Hilltop itself. It is no more Hilltop's alone than
		// before: the link would hand Hilltop's provider the role held in every tenant.
		w.seats.give(staff.ID, "hilltop", "Teacher")
		refused("staff@operator.example", "sub-staff")

		// A person created ahead in two tenants is neither tenant's alone: Hilltop's
		// provider would be handed the seat in Riverside.
		both := createUser(ctx, t, in.Driver, "frank@hilltop.example")
		w.seats.give(both.ID, "hilltop", "Teacher")
		w.seats.give(both.ID, "riverside", "Teacher")
		refused("frank@hilltop.example", "sub-frank")

		// Hilltop's own person, created ahead with a role in Hilltop and nothing else:
		// the first sign-in through Hilltop's directory makes the link.
		const hilltopName = "erin@hilltop.example"
		erin := createUser(ctx, t, in.Driver, hilltopName)
		w.seats.give(erin.ID, "hilltop", "Teacher")
		identity := throughProvider("sub-erin", hilltopName, hilltopName)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: hilltopName, Identity: identity}
		err := signInRequest(ctx, in, req)
		assertNewlyLinked(ctx, t, in.Driver, err, req, identity, erin, hilltopProvider.Connection)
		assertReached(t, w, erin.ID, identity, "hilltop")
	})

	t.Run("an account created ahead with no password is linked by a sign-in that carries its own invitation, and by no other invitation", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := firstLinkWorld("")
		in := w.start(ctx, t, h)
		dana := createdAhead(ctx, t, w, in)

		// An invitation mailed to another address is not Dana's: Hilltop's provider
		// asserts Dana's name beside the address its own invitation was mailed to.
		const other = "mallory@hilltop.example"
		foreign := insertInvitation(ctx, t, in.Driver, other, "hilltop", []string{"Member"}, tokenHash(1))
		claimed := throughProvider("sub-claimed", name, other)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: claimed, Invitation: invitationOf(foreign)}
		assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
		if req.Invitation.Accepted {
			t.Error("the refused sign-in accepted the invitation it carried")
		}
		assertUntouched(ctx, t, w, in.Driver, dana, claimed, "riverside")

		// Dana's own invitation that is no longer live admits nothing: it alone would
		// have vouched for the link.
		dead := insertInvitation(ctx, t, in.Driver, name, "hilltop", []string{"Member"}, tokenHash(2))
		if err := in.Driver.ConsumeMailLink(ctx, dead.TokenHash, time.Now()); err != nil {
			t.Fatalf("ConsumeMailLink() error = %v", err)
		}
		own := throughProvider("sub-dana", name, name)
		req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: own, Invitation: invitationOf(dead)}
		assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedInvitationExpired, signin.ErrInvitationExpired)
		assertUntouched(ctx, t, w, in.Driver, dana, own, "riverside")

		// A disabled account accepts no invitation, so its own, live, vouches for
		// nothing: the link an enabled account would get is not made and the invitation
		// is not spent.
		const benched = "gus@riverside.example"
		gus, err := in.Driver.CreateUser(ctx, &dbtype.InsertSessionUser{Username: benched, Disabled: true}, nil)
		if err != nil {
			t.Fatalf("CreateUser(%q) error = %v", benched, err)
		}
		w.seats.give(gus.ID, "riverside", "Teacher")
		spare := insertInvitation(ctx, t, in.Driver, benched, "hilltop", []string{"Member"}, tokenHash(4))
		gusIdentity := throughProvider("sub-gus", benched, benched)
		req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: benched, Identity: gusIdentity, Invitation: invitationOf(spare)}
		assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
		assertUntouched(ctx, t, w, in.Driver, gus, gusIdentity, "riverside")
		if consumedAt := h.MailLinkConsumedAt(ctx, t, in.Raw, spare.TokenHash); consumedAt != nil {
			t.Errorf("the disabled account's invitation was consumed at %v by the refused sign-in, want it left standing", consumedAt)
		}

		// Dana's own invitation, live: the person holds the link mailed to the account's
		// address, so the identity is linked and the invitation's role given with it.
		live := insertInvitation(ctx, t, in.Driver, name, "hilltop", []string{"Member"}, tokenHash(3))
		req = &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: own, Invitation: invitationOf(live)}
		err = signInRequest(ctx, in, req)
		assertNewlyLinked(ctx, t, in.Driver, err, req, own, dana, hilltopProvider.Connection)
		if !req.Invitation.Accepted {
			t.Error("Invitation.Accepted = false after the sign-in that carried the account's own invitation")
		}
		if diff := cmp.Diff([]accesstypes.Domain{"hilltop", "riverside"}, w.seats.tenants(dana.ID)); diff != "" {
			t.Errorf("tenants that seat the account after its invitation (-want +got):\n%s", diff)
		}
	})

	t.Run("a registration that declares seeded users takes its provider's names for the application's own and links an account created ahead on the name alone, where a registration that declares nothing refuses it; a stranger is refused under both", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		for _, tt := range []struct {
			seeded bool
			linked bool
		}{
			{seeded: false},
			{seeded: true, linked: true},
		} {
			w := firstLinkWorld("")
			w.accounts, w.seeded = InviteOnly, tt.seeded
			in := w.start(ctx, t, h)
			// The account a seed made: no password, no link, and no role in the tenant
			// whose directory the provider is.
			dana := createdAhead(ctx, t, w, in)

			identity := throughProvider("sub-dana", name, name)
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
			err := signInRequest(ctx, in, req)
			if tt.linked {
				assertNewlyLinked(ctx, t, in.Driver, err, req, identity, dana, hilltopProvider.Connection)
				assertReached(t, w, dana.ID, identity, "riverside")
			} else {
				assertRefused(t, err, sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
				assertUntouched(ctx, t, w, in.Driver, dana, identity, "riverside")
			}

			// A name no account was created under is a stranger's either way: through a
			// directory no tenant has, the method, which is invite only, refuses it.
			stranger := google("google-stranger", "nobody@riverside.example")
			strangerErr := signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: "nobody@riverside.example", Identity: stranger})
			assertRefused(t, strangerErr, sessioninfo.RefusedNotInvited, signin.ErrNotInvited)
			assertNoUser(ctx, t, in.Driver, "nobody@riverside.example")
		}
	})

	t.Run("a registration's own rule that links the account on its own evidence is not held to the rule on a name, and its answer of Standard is", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		// The application's rule knows the person by evidence of its own (here, the
		// provider's subject) and names the account itself: the link is the rule's.
		w := firstLinkWorld("")
		var known atomic.Pointer[dbtype.SessionUser]
		w.resolve = func(_ context.Context, _ HookTx, req *sessioninfo.NewSessionRequest) (*signin.Resolution, error) {
			if user := known.Load(); user != nil && req.Identity.Subject == "sub-known" {
				return signin.Link(user.ID, false, signin.Tenant(req.Identity.Connection)), nil
			}

			return signin.Standard(), nil
		}
		in := w.start(ctx, t, h)
		dana := createdAhead(ctx, t, w, in)
		known.Store(dana)

		// Another subject under Dana's name is left to the standard rule, which links
		// nothing on the name.
		claimed := throughProvider("sub-claimed", name, name)
		claimedReq := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: claimed}
		assertRefused(t, signInRequest(ctx, in, claimedReq), sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
		assertUntouched(ctx, t, w, in.Driver, dana, claimed, "riverside")

		identity := throughProvider("sub-known", name, name)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
		err := signInRequest(ctx, in, req)
		assertNewlyLinked(ctx, t, in.Driver, err, req, identity, dana, hilltopProvider.Connection)
	})

	t.Run("an account created ahead with a password keeps the confirmation it has", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := firstLinkWorld("Member")
		in := w.start(ctx, t, h)

		// An administrator's create with a temporary password, and the first
		// administrator a deployment seeds, both hold a password: the link waits for it.
		dana := passwordUser(ctx, t, in.Driver, name)
		w.seats.give(dana.ID, "riverside", "Teacher")
		identity := throughProvider("sub-claimed", name, name)
		err := signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity})
		assertConfirmationPending(ctx, t, in.Driver, err, identity, dana, hilltopProvider.Connection)
	})

	t.Run("an invitation that was not accepted is no account: a sign-in under the invited name is a stranger's", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		w := firstLinkWorld("")
		in := w.start(ctx, t, h)

		insertInvitation(ctx, t, in.Driver, name, "riverside", []string{"Teacher"}, tokenHash(1))
		if _, err := in.Driver.UserByUserName(ctx, name); !httpio.HasNotFound(err) {
			t.Fatalf("UserByUserName(%q) error = %v, want NotFound: an invitation creates no account until it is accepted", name, err)
		}
		// Hilltop's provider asserts the invited name without the link the invitation
		// mailed: nothing is there to link to, and Hilltop admits a stranger by
		// invitation only.
		identity := throughProvider("sub-claimed", name, name)
		err := signInRequest(ctx, in, &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity})
		assertRefused(t, err, sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
		assertNoLink(ctx, t, in.Driver, identity)
		if _, err := in.Driver.UserByUserName(ctx, name); !httpio.HasNotFound(err) {
			t.Errorf("UserByUserName(%q) error = %v, want NotFound: the refused sign-in creates no account", name, err)
		}
	})

	t.Run("an application whose tenants have no directories links an account created ahead under its name, as before", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: Open})
		dana := createUser(ctx, t, in.Driver, name)
		identity := google("google-dana", name)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
		err := signInRequest(ctx, in, req)
		assertNewlyLinked(ctx, t, in.Driver, err, req, identity, dana, "lakeside")
	})
}
