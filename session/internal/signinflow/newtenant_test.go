package signinflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// These cases hold the flow to what it promises an invitation that makes a tenant
// (sessioninfo.Invitation.NewTenant): the acceptor is told what the invitation carries
// and how the person signed in, on every path that runs it; an invitation whose tenant
// is entered through its directory only is accepted through an organization's
// directory alone, and a sign-in by password or with a personal account is refused
// before anything is read or written; and an acceptor that refuses the acceptance
// leaves nothing behind, the invitation standing.

// handed is what the acceptor of these cases was given.
type handed struct {
	Username   string
	Tenant     *sessioninfo.NewTenant
	Method     sessioninfo.AuthMethod
	Connection string
}

// tenantAcceptor is an acceptor that records what it was handed, buffers a grant in
// the transaction as an application's writes its tenant, and answers refuse, nil for
// none.
type tenantAcceptor struct {
	store  *memStore
	got    []handed
	refuse error
}

func (a *tenantAcceptor) accept(_ context.Context, tx *memTx, _ impulse.UUID, username string, inv *sessioninfo.Invitation) (func(ctx context.Context), error) {
	a.got = append(a.got, handed{Username: username, Tenant: inv.NewTenant, Method: inv.SignInMethod, Connection: inv.SignInConnection})
	tx.buffer(func() { a.store.granted[username] = append(a.store.granted[username], "made "+inv.NewTenant.Name) })

	return nil, a.refuse
}

// inviteTenant stores a live invitation to email that makes the tenant Lakeside High,
// and answers it as a sign-in carries it.
func (s *memStore) inviteTenant(email string, directoryOnly bool) *sessioninfo.Invitation {
	inv := s.invite(email, "")
	inv.NewTenant = &sessioninfo.NewTenant{Name: "Lakeside High", DirectoryOnly: directoryOnly, Details: json.RawMessage(`{"region":"north"}`)}

	return inv
}

// through is a first sign-in by the invited stranger through method and connection.
func through(method sessioninfo.AuthMethod, connection string, inv *sessioninfo.Invitation) *sessioninfo.NewSessionRequest {
	return &sessioninfo.NewSessionRequest{
		Reason:     sessioninfo.ReasonLogin,
		Identity:   &sessioninfo.Identity{Method: method, Connection: connection, Subject: "sub-pat", Email: stranger, SignInName: stranger},
		Invitation: inv,
	}
}

func TestSignIn_anInvitationThatMakesATenant(t *testing.T) {
	t.Parallel()

	lakeside := &sessioninfo.NewTenant{Name: "Lakeside High", Details: json.RawMessage(`{"region":"north"}`)}
	lakesideOnly := &sessioninfo.NewTenant{Name: "Lakeside High", DirectoryOnly: true, Details: json.RawMessage(`{"region":"north"}`)}

	tests := []struct {
		name          string
		directoryOnly bool
		method        sessioninfo.AuthMethod
		connection    string
		wantRefused   bool
		want          handed
	}{
		{
			name:   "through a Google Workspace: the acceptor is handed the tenant and the Workspace the person came through",
			method: sessioninfo.MethodGoogle, connection: "lakeside.edu",
			want: handed{Username: stranger, Tenant: lakeside, Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu"},
		},
		{
			name:   "with a personal Google account, where the tenant is not directory only: the acceptor is handed the account's connection, which is no organization's",
			method: sessioninfo.MethodGoogle, connection: signin.GoogleAnyAccountConnection,
			want: handed{Username: stranger, Tenant: lakeside, Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection},
		},
		{
			name:          "directory only, through a Microsoft Entra tenant: accepted",
			directoryOnly: true, method: sessioninfo.MethodAzure, connection: "tenant-1",
			want: handed{Username: stranger, Tenant: lakesideOnly, Method: sessioninfo.MethodAzure, Connection: "tenant-1"},
		},
		{
			name:          "directory only, with a personal Google account: refused, no account made and the invitation standing",
			directoryOnly: true, method: sessioninfo.MethodGoogle, connection: signin.GoogleAnyAccountConnection, wantRefused: true,
		},
		{
			name:          "directory only, with a Microsoft personal account: refused, no account made and the invitation standing",
			directoryOnly: true, method: sessioninfo.MethodAzure, connection: signin.MicrosoftPersonalAccountsConnection, wantRefused: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			d := &memDriver{store: store}
			acceptor := &tenantAcceptor{store: store}
			inv := store.inviteTenant(stranger, tt.directoryOnly)
			req := through(tt.method, tt.connection, inv)

			err := signInWith(t, d, req, Rules[*memTx]{Accept: acceptor.accept})

			if tt.wantRefused {
				assertOrganizationAccountRequired(t, err)
				if len(store.users) != 0 || len(store.links) != 0 || len(store.granted) != 0 || store.sessions != 0 || len(store.steps) != 0 {
					t.Errorf("the store holds %d user(s), %d link(s), grants %v, %d session(s) after steps %v; want nothing read or written", len(store.users), len(store.links), store.granted, store.sessions, store.steps)
				}
				if !store.invitations[inv.ID] || inv.Accepted || len(acceptor.got) != 0 {
					t.Errorf("live = %t, Accepted = %t, acceptor calls = %d; want the invitation standing and the acceptor never asked", store.invitations[inv.ID], inv.Accepted, len(acceptor.got))
				}

				return
			}
			if err != nil {
				t.Fatalf("SignIn() error = %v", err)
			}
			if diff := cmp.Diff([]handed{tt.want}, acceptor.got); diff != "" {
				t.Errorf("the acceptor was handed (-want +got):\n%s", diff)
			}
			if !inv.Accepted || store.invitations[inv.ID] || store.sessions != 1 {
				t.Errorf("Accepted = %t, live = %t, sessions = %d; want the invitation accepted and a session", inv.Accepted, store.invitations[inv.ID], store.sessions)
			}
			if diff := cmp.Diff(map[string][]string{stranger: {"made Lakeside High"}}, store.granted); diff != "" {
				t.Errorf("granted (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("a person whose identity is linked already: the acceptor is handed how they signed in", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		d := &memDriver{store: store}
		acceptor := &tenantAcceptor{store: store}
		id := impulse.Must(impulse.NewUUID())
		store.users[id] = &memUser{username: stranger}
		store.links[linkKey(sessioninfo.MethodGoogle, "lakeside.edu", "sub-pat")] = id
		inv := store.inviteTenant(stranger, true)

		if err := signInWith(t, d, through(sessioninfo.MethodGoogle, "lakeside.edu", inv), Rules[*memTx]{Accept: acceptor.accept}); err != nil {
			t.Fatalf("SignIn() error = %v", err)
		}
		want := []handed{{Username: stranger, Tenant: lakesideOnly, Method: sessioninfo.MethodGoogle, Connection: "lakeside.edu"}}
		if diff := cmp.Diff(want, acceptor.got); diff != "" {
			t.Errorf("the acceptor was handed (-want +got):\n%s", diff)
		}
	})

	passwordRequest := func(id impulse.UUID, inv *sessioninfo.Invitation) *sessioninfo.NewSessionRequest {
		return &sessioninfo.NewSessionRequest{
			Reason: sessioninfo.ReasonLogin, Username: "sam", UserID: id,
			Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: id.String()}, Invitation: inv,
		}
	}

	t.Run("by password: the acceptor is handed the tenant and the password method, with no connection", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		d := &memDriver{store: store}
		acceptor := &tenantAcceptor{store: store}
		id := impulse.Must(impulse.NewUUID())
		store.users[id] = &memUser{username: "sam"}
		inv := store.inviteTenant("sam@lakeside.edu", false)

		if err := signInWith(t, d, passwordRequest(id, inv), Rules[*memTx]{Accept: acceptor.accept}); err != nil {
			t.Fatalf("SignIn() error = %v", err)
		}
		want := []handed{{Username: "sam", Tenant: lakeside, Method: sessioninfo.MethodPassword}}
		if diff := cmp.Diff(want, acceptor.got); diff != "" {
			t.Errorf("the acceptor was handed (-want +got):\n%s", diff)
		}
		if !inv.Accepted || store.sessions != 1 {
			t.Errorf("Accepted = %t, sessions = %d; want the invitation accepted and a session", inv.Accepted, store.sessions)
		}
	})

	t.Run("directory only, by password: refused before anything is read or written, the invitation standing", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		d := &memDriver{store: store}
		acceptor := &tenantAcceptor{store: store}
		id := impulse.Must(impulse.NewUUID())
		store.users[id] = &memUser{username: "sam"}
		inv := store.inviteTenant("sam@lakeside.edu", true)

		err := signInWith(t, d, passwordRequest(id, inv), Rules[*memTx]{Accept: acceptor.accept})

		assertOrganizationAccountRequired(t, err)
		if !store.invitations[inv.ID] || inv.Accepted || len(acceptor.got) != 0 || store.sessions != 0 || len(store.granted) != 0 || len(store.steps) != 0 {
			t.Errorf("live = %t, Accepted = %t, acceptor calls = %d, sessions = %d, granted = %v, steps = %v; want nothing read or written", store.invitations[inv.ID], inv.Accepted, len(acceptor.got), store.sessions, store.granted, store.steps)
		}
	})

	t.Run("an acceptor that refuses the acceptance ends the sign-in on its refusal: no account, no tenant, and the invitation stands", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		d := &memDriver{store: store}
		refusal := sessioninfo.NewLoginRefusal(sessioninfo.RefusedOrganizationHasTenant, httpio.NewConflictMessage("Your organization already has a tenant here."))
		acceptor := &tenantAcceptor{store: store, refuse: refusal}
		inv := store.inviteTenant(stranger, false)

		err := signInWith(t, d, through(sessioninfo.MethodGoogle, "lakeside.edu", inv), Rules[*memTx]{Accept: acceptor.accept})

		var refused *sessioninfo.LoginRefusal
		if !errors.As(err, &refused) || refused.Code() != sessioninfo.RefusedOrganizationHasTenant {
			t.Fatalf("SignIn() error = %v, want the acceptor's refusal %s", err, sessioninfo.RefusedOrganizationHasTenant)
		}
		if len(store.users) != 0 || len(store.links) != 0 || len(store.granted) != 0 || store.sessions != 0 {
			t.Errorf("the store holds %d user(s), %d link(s), grants %v and %d session(s), want nothing committed", len(store.users), len(store.links), store.granted, store.sessions)
		}
		if !store.invitations[inv.ID] || inv.Accepted {
			t.Errorf("live = %t, Accepted = %t; want the invitation standing", store.invitations[inv.ID], inv.Accepted)
		}
	})
}

func TestAcceptInvitation_anInvitationThatMakesATenant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		directoryOnly bool
		method        sessioninfo.AuthMethod
		connection    string
		wantRefused   bool
	}{
		{name: "a session established by password accepts a tenant that is not directory only", method: sessioninfo.MethodPassword},
		{name: "a session established through a Workspace accepts a tenant that is directory only", directoryOnly: true, method: sessioninfo.MethodGoogle, connection: "lakeside.edu"},
		{name: "a session established by password is refused a tenant that is directory only", directoryOnly: true, method: sessioninfo.MethodPassword, wantRefused: true},
		{name: "a session established with a personal account is refused a tenant that is directory only", directoryOnly: true, method: sessioninfo.MethodGoogle, connection: signin.GoogleAnyAccountConnection, wantRefused: true},
		{name: "a session that records no way it was established is refused a tenant that is directory only", directoryOnly: true, wantRefused: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newMemStore()
			d := &memDriver{store: store}
			acceptor := &tenantAcceptor{store: store}
			id := impulse.Must(impulse.NewUUID())
			store.users[id] = &memUser{username: "sam"}
			inv := store.inviteTenant("sam@lakeside.edu", tt.directoryOnly)
			inv.SignInMethod, inv.SignInConnection = tt.method, tt.connection

			err := AcceptInvitation(t.Context(), d, id, "sam", "", inv, acceptor.accept)

			if tt.wantRefused {
				assertOrganizationAccountRequired(t, err)
				if !store.invitations[inv.ID] || inv.Accepted || len(acceptor.got) != 0 || len(store.granted) != 0 || len(store.steps) != 0 {
					t.Errorf("live = %t, Accepted = %t, acceptor calls = %d, granted = %v, steps = %v; want nothing read or written", store.invitations[inv.ID], inv.Accepted, len(acceptor.got), store.granted, store.steps)
				}

				return
			}
			if err != nil {
				t.Fatalf("AcceptInvitation() error = %v", err)
			}
			want := []handed{{Username: "sam", Tenant: inv.NewTenant, Method: tt.method, Connection: tt.connection}}
			if diff := cmp.Diff(want, acceptor.got); diff != "" {
				t.Errorf("the acceptor was handed (-want +got):\n%s", diff)
			}
			if !inv.Accepted || store.invitations[inv.ID] {
				t.Errorf("Accepted = %t, live = %t; want the invitation accepted", inv.Accepted, store.invitations[inv.ID])
			}
		})
	}
}

// assertOrganizationAccountRequired fails unless err is the refusal of an acceptance
// that needs an organization's directory, in the words a person acts on.
func assertOrganizationAccountRequired(t *testing.T, err error) {
	t.Helper()

	var refused *sessioninfo.LoginRefusal
	if !errors.As(err, &refused) || refused.Code() != sessioninfo.RefusedOrganizationAccountRequired {
		t.Fatalf("error = %v, want the refusal %s", err, sessioninfo.RefusedOrganizationAccountRequired)
	}
	if !errors.Is(err, signin.ErrOrganizationAccountRequired) {
		t.Errorf("error = %v, want signin.ErrOrganizationAccountRequired as its cause", err)
	}
	if got := httpio.Message(err); got != OrganizationAccountMessage {
		t.Errorf("message = %q, want %q", got, OrganizationAccountMessage)
	}
}
