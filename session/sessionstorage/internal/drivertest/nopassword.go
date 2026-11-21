package drivertest

// An account can hold a password under an Auth that registers no password method: the
// application's API set one, or an earlier release registered the method and a later
// one took it out. Nobody can enter that password, and no route takes its
// confirmation, so a sign-in held for it would wait for ever. Under such an Auth
// (signinflow.Rules.NoPassword) the standard rule and the link decide the account as
// they decide one with no password: joined at its first sign-in through a directory,
// and held to everything an account with no password is held to. A rule of the
// registration's own that asks for the password all the same is refused: the sign-in
// would wait on what nobody can give. The cases run through the real sign-in flow on
// both drivers. They follow the sequence that locked a person out (a password set, the
// password method taken out, a first sign-in through the directory): first with the
// method registered, where the sign-in waits for the password, then without it.

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

func testNoPasswordMethod(_ context.Context, t *testing.T, h *AuthHarness) {
	const name = "ada@lakeside.edu"

	tests := []struct {
		name string
		// noPassword says the Auth registers no password method.
		noPassword bool
		// resolve is the registration's own account rule, given ada; nil for the
		// standard rule alone.
		resolve func(ada *dbtype.SessionUser) *signin.Resolution
		// linked is an identity ada already signs in through; nil for none.
		linked *sessioninfo.Identity
		check  func(ctx context.Context, t *testing.T, d AuthDriver, err error, req *sessioninfo.NewSessionRequest, ada *dbtype.SessionUser)
	}{
		{
			name: "with the password method registered, a first sign-in through the directory waits for the account's password and links nothing",
			check: func(ctx context.Context, t *testing.T, d AuthDriver, err error, _ *sessioninfo.NewSessionRequest, ada *dbtype.SessionUser) {
				t.Helper()
				assertConfirmationPending(ctx, t, d, err, google("sub-ada", name), ada, "lakeside")
			},
		},
		{
			name:       "with no password method, the same sign-in is joined to the account as to one created ahead with no password, and the account keeps its password",
			noPassword: true,
			check: func(ctx context.Context, t *testing.T, d AuthDriver, err error, req *sessioninfo.NewSessionRequest, ada *dbtype.SessionUser) {
				t.Helper()
				assertNewlyLinked(ctx, t, d, err, req, google("sub-ada", name), ada, "lakeside")
				if kept := mustUser(ctx, t, d, ada.ID); kept.PasswordHash == nil || !sameHash(kept.PasswordHash, ada.PasswordHash) {
					t.Error("the account's password changed with the link, want it kept: it is the account's once the method is registered again")
				}
			},
		},
		{
			name:       "with no password method, an account that already signs in through another connection is refused the link on its name, as one with no password is",
			noPassword: true,
			linked:     entra("oid-ada", name),
			check: func(ctx context.Context, t *testing.T, d AuthDriver, err error, _ *sessioninfo.NewSessionRequest, _ *dbtype.SessionUser) {
				t.Helper()
				assertRefused(t, err, sessioninfo.RefusedIdentityRejected, signin.ErrLinkCrossConnection)
				assertNoLink(ctx, t, d, google("sub-ada", name))
			},
		},
		{
			name:       "with no password method, a link the registration's own rule does not vouch for is made, where the password would otherwise be asked for",
			noPassword: true,
			resolve: func(ada *dbtype.SessionUser) *signin.Resolution {
				return signin.Link(ada.ID, false, signin.Tenant("lakeside"))
			},
			check: func(ctx context.Context, t *testing.T, d AuthDriver, err error, req *sessioninfo.NewSessionRequest, ada *dbtype.SessionUser) {
				t.Helper()
				assertNewlyLinked(ctx, t, d, err, req, google("sub-ada", name), ada, "lakeside")
			},
		},
		{
			name:       "with no password method, a rule of the registration's own that asks for the password is refused, since nobody could enter it: nothing waits and nothing is linked",
			noPassword: true,
			resolve: func(ada *dbtype.SessionUser) *signin.Resolution {
				return signin.Confirm(ada.ID, signin.Tenant("lakeside"))
			},
			check: func(ctx context.Context, t *testing.T, d AuthDriver, err error, _ *sessioninfo.NewSessionRequest, _ *dbtype.SessionUser) {
				t.Helper()
				assertRefused(t, err, sessioninfo.RefusedIdentityRejected, signin.ErrConfirmNeedsPasswordMethod)
				assertNoLink(ctx, t, d, google("sub-ada", name))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := t.Context()

			var ada *dbtype.SessionUser
			cfg := AuthConfig{Auth: true, Accounts: InviteOnly, NoPassword: tt.noPassword}
			if tt.resolve != nil {
				cfg.Resolve = func(context.Context, HookTx, *sessioninfo.NewSessionRequest) (*signin.Resolution, error) {
					return tt.resolve(ada), nil
				}
			}
			in := h.New(ctx, t, Auth, cfg)
			ada = passwordUser(ctx, t, in.Driver, name)
			if tt.linked != nil {
				mustLink(ctx, t, in.Driver, ada.ID, tt.linked)
			}
			identity := google("sub-ada", name)
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}

			err := signInRequest(ctx, in, req)

			tt.check(ctx, t, in.Driver, err, req, ada)
		})
	}

	t.Run("with no password method, in an application whose tenants have directories, the account is not joined through another organization's provider on its name: "+
		"the password it holds vouches for nothing, and the sign-in is refused as a stranger's is", func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		// Ada's account holds a password and a role in Riverside. Hilltop's own provider
		// asserts her name.
		w := firstLinkWorld("")
		w.noPassword = true
		in := w.start(ctx, t, h)
		ada := passwordUser(ctx, t, in.Driver, name)
		w.seats.give(ada.ID, "riverside", "Teacher")

		identity := throughProvider("sub-claimed", name, name)
		req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity}
		err := signInRequest(ctx, in, req)

		assertRefused(t, err, sessioninfo.RefusedInvitationOnly, signin.ErrInvitationOnly)
		if !req.UserID.IsNil() || req.User != nil {
			t.Errorf("the refused sign-in names user %v (%+v), want nobody: the refusal must not say the account exists", req.UserID, req.User)
		}
		assertUntouched(ctx, t, w, in.Driver, ada, identity, "riverside")
	})
}
