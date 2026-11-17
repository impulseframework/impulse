package drivertest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/internal/dbtype"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// An invitation may make a tenant instead of seating the person in one: it names no
// tenant and no roles, and carries the tenant its first sign-in makes
// (sessioninfo.NewTenant) as a JSON document on its row. These cases pin, on every
// backend, that the row keeps the document, that the acceptor is handed it with how
// the person signed in, inside the transaction that consumes the invitation, that a
// tenant entered through its directory only is accepted through an organization's
// directory alone, and that an acceptor that refuses leaves nothing behind.

// lakesideHigh is the tenant the invitations of these cases make.
func lakesideHigh(directoryOnly bool) *sessioninfo.NewTenant {
	return &sessioninfo.NewTenant{Name: "Lakeside High", DirectoryOnly: directoryOnly, Details: json.RawMessage(`{"defaultRole":"Teacher","fields":{"Region":"north"}}`)}
}

// insertTenantInvitation inserts an invitation to email that makes tenant.
func insertTenantInvitation(ctx context.Context, t *testing.T, d AuthDriver, email string, tenant *sessioninfo.NewTenant, hash []byte) *dbtype.MailLink {
	t.Helper()

	link := newMailLink(dbtype.MailLinkInvitation, email, hash)
	link.NewTenant = tenant
	if err := d.InsertMailLink(ctx, link); err != nil {
		t.Fatalf("InsertMailLink() error = %v", err)
	}

	return link
}

// personalGoogle is an external identity of a personal Google account, admitted as any
// Google account: the provider vouches for it alone, so it is no organization's.
func personalGoogle(subject, email string) *sessioninfo.Identity {
	return &sessioninfo.Identity{
		Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection, Subject: subject, Email: email, EmailVerified: true, SignInName: email,
	}
}

// tenantMaker is an acceptor that makes the tenant an invitation carries, the way an
// application does: it writes what it was handed to HookRecords under made:<address>,
// through the transaction it is handed, so the tenant is committed with the
// consumption or not at all. While refuse holds an error it answers it after its
// write, which the flow must then never commit.
type tenantMaker struct {
	mu     sync.Mutex
	refuse error
}

func (m *tenantMaker) hook(ctx context.Context, tx HookTx, _ impulse.UUID, username string, inv *sessioninfo.Invitation) (func(ctx context.Context), error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := tx.Record(ctx, "made:"+inv.Email, madeAs(username, inv.NewTenant, inv.SignInMethod, inv.SignInConnection)); err != nil {
		return nil, errors.Wrap(err, "HookTx.Record()")
	}

	return nil, m.refuse
}

// set makes the acceptor refuse with err, or work when err is nil.
func (m *tenantMaker) set(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refuse = err
}

// madeAs is what tenantMaker records: who accepted, the tenant the invitation carried
// and how the person signed in.
func madeAs(username string, tenant *sessioninfo.NewTenant, method sessioninfo.AuthMethod, connection string) string {
	if tenant == nil {
		return fmt.Sprintf("%s made no tenant, signed in by %s through %q", username, method, connection)
	}

	return fmt.Sprintf("%s made %s (directory only %t, details %s), signed in by %s through %q", username, tenant.Name, tenant.DirectoryOnly, tenant.Details, method, connection)
}

// assertNothingMade fails unless the acceptance of link left everything as it was: the
// invitation live and unconsumed, and no tenant recorded for its address.
func assertNothingMade(ctx context.Context, t *testing.T, h *AuthHarness, in *AuthInstance, link *dbtype.MailLink, inv *sessioninfo.Invitation) {
	t.Helper()

	if _, err := in.Driver.MailLinkByToken(ctx, link.TokenHash, time.Now()); err != nil {
		t.Errorf("MailLinkByToken() after the refusal error = %v, want the invitation still live", err)
	}
	if consumedAt := h.MailLinkConsumedAt(ctx, t, in.Raw, link.TokenHash); consumedAt != nil {
		t.Errorf("the invitation's ConsumedAt = %v after the refusal, want nil", consumedAt)
	}
	if value, found := h.Recorded(ctx, t, in.Raw, "made:"+link.Email); found {
		t.Errorf("HookRecords[made:%s] = %q after the refusal, want no tenant made", link.Email, value)
	}
	if inv.Accepted {
		t.Error("Invitation.Accepted = true after the refusal, want false")
	}
}

func testTenantInvitationRoundTrip(ctx context.Context, t *testing.T, h *AuthHarness) {
	in := h.New(ctx, t, Auth, AuthConfig{Auth: true})

	plain := insertTenantInvitation(ctx, t, in.Driver, "pat@lakeside.edu", &sessioninfo.NewTenant{Name: "Lakeside High"}, tokenHash(1))
	full := insertTenantInvitation(ctx, t, in.Driver, "sam@riverside.edu", &sessioninfo.NewTenant{
		Name: "Riverside \"Prep\" & Sons", DirectoryOnly: true, Details: json.RawMessage(`{"defaultRole":"Teacher","fields":{"Region":"north","Established":"2026-01-02"}}`),
	}, tokenHash(2))
	ordinary := insertInvitation(ctx, t, in.Driver, "alex@lakeside.edu", "lakeside", []string{"Teacher"}, tokenHash(3))

	for _, link := range []*dbtype.MailLink{plain, full, ordinary} {
		got, err := in.Driver.MailLinkByToken(ctx, link.TokenHash, time.Now())
		if err != nil {
			t.Fatalf("MailLinkByToken(%s) error = %v", link.Email, err)
		}
		if diff := cmp.Diff(link, got, mailLinkOpts); diff != "" {
			t.Errorf("MailLinkByToken(%s) mismatch (-want +got):\n%s", link.Email, diff)
		}
	}
	if got, err := in.Driver.MailLinkByToken(ctx, ordinary.TokenHash, time.Now()); err != nil || got.NewTenant != nil {
		t.Errorf("MailLinkByToken() of an invitation into a tenant that exists = %+v, %v; want it to make none", got, err)
	}

	live, err := in.Driver.LiveMailLinks(ctx, dbtype.MailLinkInvitation, time.Now())
	if err != nil {
		t.Fatalf("LiveMailLinks() error = %v", err)
	}
	if diff := cmp.Diff([]*dbtype.MailLink{plain, full, ordinary}, live, mailLinkOpts); diff != "" {
		t.Errorf("LiveMailLinks() mismatch (-want +got):\n%s", diff)
	}
}

func testTenantInvitationAccepted(ctx context.Context, t *testing.T, h *AuthHarness) {
	maker := &tenantMaker{}
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, Accept: maker.hook})

	// Through a Google Workspace: the stranger is provisioned by the invitation, and
	// the acceptor is handed the tenant and the Workspace the person came through, in
	// the transaction that makes the account and consumes the invitation.
	const name = "pat.lee@lakeside.edu"
	tenant := lakesideHigh(true)
	link := insertTenantInvitation(ctx, t, in.Driver, name, tenant, tokenHash(1))
	identity := google("sub-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity, Invitation: invitationOf(link)}
	err := signInRequest(ctx, in, req)
	assertProvisioned(ctx, t, in.Driver, err, req, identity, "lakeside")
	if !req.Invitation.Accepted {
		t.Error("Invitation.Accepted = false after the sign-in through a directory, want accepted")
	}
	assertRecorded(ctx, t, h, in.Raw, "made:"+name, madeAs(name, tenant, sessioninfo.MethodGoogle, "lakeside"))
	if consumedAt := h.MailLinkConsumedAt(ctx, t, in.Raw, link.TokenHash); consumedAt == nil {
		t.Error("the invitation's ConsumedAt = nil after the sign-in, want consumed")
	}

	// By password, in the acceptance's own transaction: the acceptor is handed the
	// password method and no connection, so the tenant it makes takes no directory.
	sam := passwordUser(ctx, t, in.Driver, "sam")
	open := lakesideHigh(false)
	samLink := insertTenantInvitation(ctx, t, in.Driver, "sam@riverside.edu", open, tokenHash(2))
	samReq := &sessioninfo.NewSessionRequest{
		Reason: sessioninfo.ReasonLogin, Username: sam.Username, UserID: sam.ID,
		Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: sam.ID.String()}, Invitation: invitationOf(samLink),
	}
	if err := signInRequest(ctx, in, samReq); err != nil {
		t.Fatalf("password sign-in with an invitation that makes a tenant error = %v", err)
	}
	if !samReq.Invitation.Accepted {
		t.Error("Invitation.Accepted = false after the password sign-in, want accepted")
	}
	assertRecorded(ctx, t, h, in.Raw, "made:sam@riverside.edu", madeAs("sam", open, sessioninfo.MethodPassword, ""))

	// By hand, for a person already signed in: the acceptor is handed how the session
	// was established, which the caller notes on the invitation.
	alex := createUser(ctx, t, in.Driver, "alex")
	alexLink := insertTenantInvitation(ctx, t, in.Driver, "alex@hillside.edu", tenant, tokenHash(3))
	byHand := invitationOf(alexLink)
	byHand.SignInMethod, byHand.SignInConnection = sessioninfo.MethodAzure, "tenant-hillside"
	if err := in.Accept(ctx, alex.ID, alex.Username, "", byHand); err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if !byHand.Accepted {
		t.Error("Invitation.Accepted = false after Accept(), want accepted")
	}
	assertRecorded(ctx, t, h, in.Raw, "made:alex@hillside.edu", madeAs("alex", tenant, sessioninfo.MethodAzure, "tenant-hillside"))
}

func testTenantInvitationDirectoryOnly(ctx context.Context, t *testing.T, h *AuthHarness) {
	maker := &tenantMaker{}
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, Accept: maker.hook})
	tenant := lakesideHigh(true)

	// A password sign-in: refused before anything is written, the invitation standing.
	sam := passwordUser(ctx, t, in.Driver, "sam")
	samLink := insertTenantInvitation(ctx, t, in.Driver, "sam@lakeside.edu", tenant, tokenHash(1))
	samReq := &sessioninfo.NewSessionRequest{
		Reason: sessioninfo.ReasonLogin, Username: sam.Username, UserID: sam.ID,
		Identity: &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: sam.ID.String()}, Invitation: invitationOf(samLink),
	}
	assertRefused(t, signInRequest(ctx, in, samReq), sessioninfo.RefusedOrganizationAccountRequired, signin.ErrOrganizationAccountRequired)
	assertNothingMade(ctx, t, h, in, samLink, samReq.Invitation)
	assertEmail(t, "the password user after the refusal", mustUser(ctx, t, in.Driver, sam.ID), nil, false)

	// A stranger with a personal Google account, which is no organization's directory:
	// refused, and no account and no link is made for them.
	const name = "pat.lee@gmail.example"
	link := insertTenantInvitation(ctx, t, in.Driver, name, tenant, tokenHash(2))
	personal := personalGoogle("sub-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: personal, Invitation: invitationOf(link)}
	assertRefused(t, signInRequest(ctx, in, req), sessioninfo.RefusedOrganizationAccountRequired, signin.ErrOrganizationAccountRequired)
	assertNothingMade(ctx, t, h, in, link, req.Invitation)
	assertNoUser(ctx, t, in.Driver, name)
	assertNoLink(ctx, t, in.Driver, personal)

	// By hand, from a session a password established, and from one that records no way
	// it was established: refused the same way.
	for _, method := range []sessioninfo.AuthMethod{sessioninfo.MethodPassword, ""} {
		byHand := invitationOf(samLink)
		byHand.SignInMethod = method
		assertRefused(t, in.Accept(ctx, sam.ID, sam.Username, "", byHand), sessioninfo.RefusedOrganizationAccountRequired, signin.ErrOrganizationAccountRequired)
		assertNothingMade(ctx, t, h, in, samLink, byHand)
	}

	// The invitation stood through all of it: the same person, through their
	// organization's directory, accepts it.
	workspace := google("sub-sam", "sam@lakeside.edu")
	mustLink(ctx, t, in.Driver, sam.ID, workspace)
	again := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: workspace, Invitation: invitationOf(samLink)}
	if err := signInRequest(ctx, in, again); err != nil {
		t.Fatalf("sign-in through the organization's directory error = %v, want the invitation accepted", err)
	}
	if !again.Invitation.Accepted {
		t.Error("Invitation.Accepted = false through the organization's directory, want accepted")
	}
	assertRecorded(ctx, t, h, in.Raw, "made:sam@lakeside.edu", madeAs("sam", tenant, sessioninfo.MethodGoogle, "lakeside"))
}

func testTenantInvitationRefusedByAcceptor(ctx context.Context, t *testing.T, h *AuthHarness) {
	// The application refuses to make the tenant, as it does when the directory the
	// person came through is another tenant's: the refusal is the sign-in's.
	refusal := sessioninfo.NewLoginRefusal(sessioninfo.RefusedOrganizationHasTenant, httpio.NewConflictMessage("Your organization already has a tenant here. Ask its administrator to invite you."))
	maker := &tenantMaker{refuse: refusal}
	in := h.New(ctx, t, AuthWithAppTables, AuthConfig{Auth: true, Accounts: InviteOnly, Accept: maker.hook})

	const name = "pat.lee@lakeside.edu"
	tenant := lakesideHigh(false)
	link := insertTenantInvitation(ctx, t, in.Driver, name, tenant, tokenHash(1))
	identity := google("sub-pat", name)
	req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity, Invitation: invitationOf(link)}

	err := signInRequest(ctx, in, req)

	if got := sessioninfo.LoginRefusalCodeOf(err); got != sessioninfo.RefusedOrganizationHasTenant {
		t.Fatalf("sign-in error = %v (code %q), want the acceptor's refusal %q", err, got, sessioninfo.RefusedOrganizationHasTenant)
	}
	if !httpio.HasConflict(err) {
		t.Errorf("sign-in error = %v, want the acceptor's own answer, a Conflict", err)
	}
	assertNothingMade(ctx, t, h, in, link, req.Invitation)
	assertNoUser(ctx, t, in.Driver, name)
	assertNoLink(ctx, t, in.Driver, identity)

	// A person whose identity is linked already is refused the same way, and keeps the
	// invitation; and so is an acceptance by hand.
	alex := createUser(ctx, t, in.Driver, "alex@lakeside.edu")
	alexIdentity := google("sub-alex", "alex@lakeside.edu")
	mustLink(ctx, t, in.Driver, alex.ID, alexIdentity)
	alexLink := insertTenantInvitation(ctx, t, in.Driver, "alex@lakeside.edu", tenant, tokenHash(2))
	alexReq := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Identity: alexIdentity, Invitation: invitationOf(alexLink)}
	if got := sessioninfo.LoginRefusalCodeOf(signInRequest(ctx, in, alexReq)); got != sessioninfo.RefusedOrganizationHasTenant {
		t.Errorf("sign-in of a linked person: refusal code = %q, want %q", got, sessioninfo.RefusedOrganizationHasTenant)
	}
	assertNothingMade(ctx, t, h, in, alexLink, alexReq.Invitation)
	byHand := invitationOf(alexLink)
	byHand.SignInMethod, byHand.SignInConnection = sessioninfo.MethodGoogle, "lakeside"
	if got := sessioninfo.LoginRefusalCodeOf(in.Accept(ctx, alex.ID, alex.Username, "", byHand)); got != sessioninfo.RefusedOrganizationHasTenant {
		t.Errorf("Accept(): refusal code = %q, want %q", got, sessioninfo.RefusedOrganizationHasTenant)
	}
	assertNothingMade(ctx, t, h, in, alexLink, byHand)

	// The invitation stood: once the application makes the tenant, the stranger's next
	// attempt at the same link is provisioned and the tenant made with the account.
	maker.set(nil)
	retry := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, Username: name, Identity: identity, Invitation: invitationOf(link)}
	err = signInRequest(ctx, in, retry)
	assertProvisioned(ctx, t, in.Driver, err, retry, identity, "lakeside")
	assertRecorded(ctx, t, h, in.Raw, "made:"+name, madeAs(name, tenant, sessioninfo.MethodGoogle, "lakeside"))
}
