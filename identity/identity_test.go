package identity

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/spxscan/spxapi"
)

// stubTxn is the transaction under the Mock client's wrapper: the rules hand it to the
// store and never read it themselves.
type stubTxn struct{}

func (stubTxn) DBType() resource.DBType {
	return resource.SpannerDBType
}

func (stubTxn) SpannerReadOnlyTransaction() spxapi.Querier {
	return nil
}

func (stubTxn) PostgresReadOnlyTransaction() any {
	return nil
}

func (stubTxn) BufferMap(resource.PatchSetMetadata, map[string]any) error {
	return nil
}

func (stubTxn) BufferStruct(resource.PatchSetMetadata) error {
	return nil
}

func (stubTxn) DataChangeEventIndex(accesstypes.Resource, string) int {
	return 0
}

// inTransaction runs f in a transaction of the Mock client, which commits when f returns
// nil, as the Spanner client's does: what f registered for after the commit runs then.
func inTransaction(t *testing.T, f func(ctx context.Context, txn resource.ReadWriteTransaction) error) error {
	t.Helper()

	if err := resource.NewMockClient(stubTxn{}, nil, nil).ExecuteFunc(t.Context(), f); err != nil {
		return errors.Wrap(err, "resource.MockClient.ExecuteFunc()")
	}

	return nil
}

// fakeStore is an auth's tables in memory: the committed rows the reads answer from, and
// a log of the writes the rules buffered, in order.
type fakeStore struct {
	// sessions are the live sessions of each user, memberships the committed rows, and
	// roles the rows of the roles table, the custom roles.
	sessions    map[impulse.UUID][]impulse.UUID
	memberships map[access.MembershipKey]bool
	roles       map[access.RoleKey]bool
	// usernames are the users that exist. A session row exists and stands unless ended
	// names it (it has ended) or missing does (no row holds the id). records are the
	// impersonation records by session, true while one stands, and heldBy the sessions
	// whose standing record names a username as its local actor.
	usernames map[impulse.UUID]string
	ended     map[impulse.UUID]bool
	missing   map[impulse.UUID]bool
	records   map[impulse.UUID]bool
	heldBy    map[string][]impulse.UUID
	// actsAsRole are the held records whose session acts as a role, and devices the
	// unrevoked trusted devices of each user.
	actsAsRole map[impulse.UUID]bool
	devices    map[impulse.UUID][]impulse.UUID
	// links are the mail links by id.
	links map[impulse.UUID]*MailLink
	// passwords are the users that exist for the password rule, true where one holds a
	// password; identities the user each identity links to; tokens the tokens by id,
	// and families the unrevoked tokens of each family.
	passwords  map[impulse.UUID]bool
	identities map[impulse.UUID]impulse.UUID
	tokens     map[impulse.UUID]*Token
	families   map[impulse.UUID][]impulse.UUID
	// nextID is the id InsertUser answers.
	nextID impulse.UUID
	// failOn names a call that fails.
	failOn string

	writes []string
	// reads are the reads of the roles table.
	reads []string
}

var errStore = errors.New("the store fails")

func (f *fakeStore) fail(call string) error {
	if f.failOn == call {
		return errStore
	}

	return nil
}

func (f *fakeStore) InsertUser(_ context.Context, _ resource.ReadWriteTransaction, user *NewUser) (impulse.UUID, error) {
	if err := f.fail("InsertUser"); err != nil {
		return impulse.NilUUID, err
	}
	email := "<none>"
	if user.Email != nil {
		email = *user.Email
	}
	f.writes = append(f.writes, "insert user "+user.Username+" "+email)

	return f.nextID, nil
}

func (f *fakeStore) UpdateUser(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID, change *UserChange) error {
	if err := f.fail("UpdateUser"); err != nil {
		return err
	}
	line := "update user " + id.String()
	if change.Username != nil {
		line += " username=" + *change.Username
	}
	if change.Disabled != nil {
		line += " disabled=" + boolText(*change.Disabled)
	}
	if change.PasswordChangeRequired != nil {
		line += " passwordChangeRequired=" + boolText(*change.PasswordChangeRequired)
	}
	if change.Email != nil {
		address := "<none>"
		if change.Email.Address != nil {
			address = *change.Email.Address
		}
		line += " email=" + address
	}
	f.writes = append(f.writes, line)

	return nil
}

func (f *fakeStore) LiveSessions(_ context.Context, _ resource.ReadWriteTransaction, userID impulse.UUID) ([]impulse.UUID, error) {
	if err := f.fail("LiveSessions"); err != nil {
		return nil, err
	}

	return f.sessions[userID], nil
}

func (f *fakeStore) EndSession(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) error {
	if err := f.fail("EndSession"); err != nil {
		return err
	}
	f.writes = append(f.writes, "end session "+id.String())

	return nil
}

func (f *fakeStore) UserHasPassword(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) (has, found bool, err error) {
	if err := f.fail("UserHasPassword"); err != nil {
		return false, false, err
	}
	has, found = f.passwords[id]

	return has, found, nil
}

func (f *fakeStore) IdentityUser(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) (userID impulse.UUID, found bool, err error) {
	if err := f.fail("IdentityUser"); err != nil {
		return impulse.NilUUID, false, err
	}
	userID, found = f.identities[id]

	return userID, found, nil
}

func (f *fakeStore) Identities(_ context.Context, _ resource.ReadWriteTransaction, userID impulse.UUID) ([]impulse.UUID, error) {
	if err := f.fail("Identities"); err != nil {
		return nil, err
	}
	var ids []impulse.UUID
	for id, linked := range f.identities {
		if linked == userID {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b impulse.UUID) int { return strings.Compare(a.String(), b.String()) })

	return ids, nil
}

func (f *fakeStore) DeleteIdentity(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) error {
	if err := f.fail("DeleteIdentity"); err != nil {
		return err
	}
	f.writes = append(f.writes, "delete identity "+id.String())

	return nil
}

func (f *fakeStore) Token(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) (token *Token, found bool, err error) {
	if err := f.fail("Token"); err != nil {
		return nil, false, err
	}
	token, found = f.tokens[id]

	return token, found, nil
}

func (f *fakeStore) FamilyTokens(_ context.Context, _ resource.ReadWriteTransaction, family impulse.UUID) ([]impulse.UUID, error) {
	if err := f.fail("FamilyTokens"); err != nil {
		return nil, err
	}

	return f.families[family], nil
}

func (f *fakeStore) RevokeToken(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID, reason string) error {
	if err := f.fail("RevokeToken"); err != nil {
		return err
	}
	f.writes = append(f.writes, "revoke token "+id.String()+" "+reason)

	return nil
}

func (f *fakeStore) Username(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) (username string, found bool, err error) {
	if err := f.fail("Username"); err != nil {
		return "", false, err
	}
	username, found = f.usernames[id]

	return username, found, nil
}

func (f *fakeStore) UserID(_ context.Context, _ resource.ReadWriteTransaction, username string) (id impulse.UUID, found bool, err error) {
	if err := f.fail("UserID"); err != nil {
		return impulse.NilUUID, false, err
	}
	for id, held := range f.usernames {
		if strings.EqualFold(held, username) {
			return id, true, nil
		}
	}

	return impulse.NilUUID, false, nil
}

func (f *fakeStore) DeleteUser(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) error {
	if err := f.fail("DeleteUser"); err != nil {
		return err
	}
	f.writes = append(f.writes, "delete user "+id.String())

	return nil
}

func (f *fakeStore) SessionExpired(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) (expired, found bool, err error) {
	if err := f.fail("SessionExpired"); err != nil {
		return false, false, err
	}
	if f.missing[id] {
		return false, false, nil
	}

	return f.ended[id], true, nil
}

func (f *fakeStore) Impersonation(_ context.Context, _ resource.ReadWriteTransaction, sessionID impulse.UUID) (live, found bool, err error) {
	if err := f.fail("Impersonation"); err != nil {
		return false, false, err
	}
	live, found = f.records[sessionID]

	return live, found, nil
}

func (f *fakeStore) ImpersonationsHeldBy(_ context.Context, _ resource.ReadWriteTransaction, username string) ([]HeldImpersonation, error) {
	if err := f.fail("ImpersonationsHeldBy"); err != nil {
		return nil, err
	}
	var held []HeldImpersonation
	for _, id := range f.heldBy[username] {
		held = append(held, HeldImpersonation{SessionID: id, ActsAsRole: f.actsAsRole[id]})
	}

	return held, nil
}

func (f *fakeStore) RenameImpersonationActor(_ context.Context, _ resource.ReadWriteTransaction, sessionID impulse.UUID, username string) error {
	if err := f.fail("RenameImpersonationActor"); err != nil {
		return err
	}
	f.writes = append(f.writes, "rename actor "+sessionID.String()+" "+username)

	return nil
}

func (f *fakeStore) RenameSession(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID, username string) error {
	if err := f.fail("RenameSession"); err != nil {
		return err
	}
	f.writes = append(f.writes, "rename session "+id.String()+" "+username)

	return nil
}

func (f *fakeStore) SetPassword(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID, hash string, changeRequired bool) error {
	if err := f.fail("SetPassword"); err != nil {
		return err
	}
	f.writes = append(f.writes, "set password "+id.String()+" "+hash+" changeRequired="+boolText(changeRequired))

	return nil
}

func (f *fakeStore) ClearAttempts(_ context.Context, _ resource.ReadWriteTransaction, name string) error {
	if err := f.fail("ClearAttempts"); err != nil {
		return err
	}
	f.writes = append(f.writes, "clear attempts "+name)

	return nil
}

func (f *fakeStore) InsertInvitation(_ context.Context, _ resource.ReadWriteTransaction, row *session.InvitationRow) error {
	if err := f.fail("InsertInvitation"); err != nil {
		return err
	}
	invitedBy := "<nobody>"
	if row.InvitedBy.Valid {
		invitedBy = row.InvitedBy.String()
	}
	f.writes = append(f.writes, "insert invitation "+row.ID.String()+" "+row.Email+" tenant="+row.Tenant+" roles="+strings.Join(row.RoleNames, ",")+" by="+invitedBy)

	return nil
}

func (f *fakeStore) MailLink(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) (link *MailLink, found bool, err error) {
	if err := f.fail("MailLink"); err != nil {
		return nil, false, err
	}
	link, found = f.links[id]

	return link, found, nil
}

func (f *fakeStore) ConsumeMailLink(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) error {
	if err := f.fail("ConsumeMailLink"); err != nil {
		return err
	}
	f.writes = append(f.writes, "consume mail link "+id.String())

	return nil
}

func (f *fakeStore) TrustedDevices(_ context.Context, _ resource.ReadWriteTransaction, userID impulse.UUID) ([]impulse.UUID, error) {
	if err := f.fail("TrustedDevices"); err != nil {
		return nil, err
	}

	return f.devices[userID], nil
}

func (f *fakeStore) RevokeTrustedDevice(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) error {
	if err := f.fail("RevokeTrustedDevice"); err != nil {
		return err
	}
	f.writes = append(f.writes, "revoke device "+id.String())

	return nil
}

func (f *fakeStore) EndImpersonation(_ context.Context, _ resource.ReadWriteTransaction, sessionID impulse.UUID, reason string) error {
	if err := f.fail("EndImpersonation"); err != nil {
		return err
	}
	f.writes = append(f.writes, "end impersonation "+sessionID.String()+" "+reason)

	return nil
}

func (f *fakeStore) HasRole(_ context.Context, _ resource.ReadWriteTransaction, key *access.RoleKey) (bool, error) {
	if err := f.fail("HasRole"); err != nil {
		return false, err
	}
	f.reads = append(f.reads, "role "+key.Kind+"/"+key.Domain+" "+string(key.Role))

	return f.roles[*key], nil
}

func (f *fakeStore) HasMembership(_ context.Context, _ resource.ReadWriteTransaction, key *access.MembershipKey) (bool, error) {
	if err := f.fail("HasMembership"); err != nil {
		return false, err
	}

	return f.memberships[*key], nil
}

func (f *fakeStore) Memberships(_ context.Context, _ resource.ReadWriteTransaction, username string) ([]access.MembershipKey, error) {
	if err := f.fail("Memberships"); err != nil {
		return nil, err
	}
	var keys []access.MembershipKey
	for key, held := range f.memberships {
		if held && string(key.User) == username {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b access.MembershipKey) int { return strings.Compare(membershipText(&a), membershipText(&b)) })

	return keys, nil
}

func (f *fakeStore) RoleHolders(_ context.Context, _ resource.ReadWriteTransaction, key *access.RoleKey) ([]accesstypes.User, error) {
	if err := f.fail("RoleHolders"); err != nil {
		return nil, err
	}
	var users []accesstypes.User
	for held, ok := range f.memberships {
		if ok && held.Kind == key.Kind && held.Axis == key.Axis && held.Domain == key.Domain && held.Role == key.Role {
			users = append(users, held.User)
		}
	}
	slices.Sort(users)

	return users, nil
}

func (f *fakeStore) InsertMembership(_ context.Context, _ resource.ReadWriteTransaction, key *access.MembershipKey) error {
	if err := f.fail("InsertMembership"); err != nil {
		return err
	}
	f.writes = append(f.writes, "insert membership "+membershipText(key))

	return nil
}

func (f *fakeStore) DeleteMembership(_ context.Context, _ resource.ReadWriteTransaction, key *access.MembershipKey) error {
	if err := f.fail("DeleteMembership"); err != nil {
		return err
	}
	f.writes = append(f.writes, "delete membership "+membershipText(key))

	return nil
}

func boolText(b bool) string {
	if b {
		return "true"
	}

	return "false"
}

func membershipText(key *access.MembershipKey) string {
	return key.Kind + "/" + key.Domain + " " + string(key.User) + " " + string(key.Role)
}

// fakeEngine is the permission engine the manager is built over: the roles of its role
// file, by scope, and a count of the times it was told the policy changed. Every other method
// of the Controller is the embedded nil interface's, and panics if a rule reaches for it.
type fakeEngine struct {
	access.Controller
	roles   map[accesstypes.PolicyScope][]accesstypes.Role
	changed int
	// rolesErr is the failure the engine answers a listing of a scope's roles with.
	rolesErr error
}

func (e *fakeEngine) UserManager() access.UserManager {
	return &fakeUserManager{engine: e}
}

func (e *fakeEngine) PolicyChanged() {
	e.changed++
}

// fakeUserManager answers the membership rule as the library does: a role of the role
// file is known, any other is read through the caller's reader of the roles table, a
// role neither holds is NotFound, an empty user a BadRequest.
type fakeUserManager struct {
	access.UserManager
	engine *fakeEngine
}

func (u *fakeUserManager) Membership(ctx context.Context, scope accesstypes.PolicyScope, user accesstypes.User, role accesstypes.Role, held access.RoleReader) (access.MembershipKey, error) {
	if !slices.Contains(u.engine.roles[scope], role) {
		key := access.NewMembershipKey(scope, user, role)
		custom, err := held(ctx, access.RoleKey{Kind: key.Kind, Axis: key.Axis, Domain: key.Domain, Role: role})
		if err != nil {
			return access.MembershipKey{}, err
		}
		if !custom {
			return access.MembershipKey{}, httpio.NewNotFoundMessagef("role %q is not a valid role. Please check that the role exists.", role)
		}
	}
	if user == "" {
		return access.MembershipKey{}, httpio.NewBadRequestMessage("user cannot be empty string")
	}

	return access.NewMembershipKey(scope, user, role), nil
}

// Roles lists the roles of the scope as the library does, each described by its name.
func (u *fakeUserManager) Roles(_ context.Context, scope accesstypes.PolicyScope) ([]access.RoleInfo, error) {
	if u.engine.rolesErr != nil {
		return nil, u.engine.rolesErr
	}
	listed := make([]access.RoleInfo, 0, len(u.engine.roles[scope]))
	for _, role := range u.engine.roles[scope] {
		listed = append(listed, access.RoleInfo{Name: role, Description: "the " + string(role) + " role"})
	}

	return listed, nil
}

// fakeSessions is the session library's API as the rules use it: a hash that shows the
// password it was made of, the library's refusal of an empty one, and a mailbox that
// takes prepared invitations.
type fakeSessions struct {
	mail *fakeMail
}

func (fakeSessions) HashPassword(password string) (string, error) {
	if password == "" {
		return "", httpio.NewBadRequestMessage("an empty password is not a password")
	}

	return "hash(" + password + ")", nil
}

func (s fakeSessions) PrepareInvitation(_ context.Context, req *session.InvitationRequest) (session.PreparedInvitation, error) {
	if s.mail == nil {
		return nil, errors.New("the Auth sends no mail: pass WithMail to NewAuth")
	}
	if !strings.Contains(req.Email, "@") {
		return nil, httpio.NewBadRequestMessage("the address is not an email address")
	}

	return &fakePrepared{
		mail: s.mail,
		row:  session.InvitationRow{ID: s.mail.id, TokenHash: []byte("hash"), Email: req.Email, Tenant: req.Tenant, RoleNames: req.RoleNames, InvitedBy: req.InvitedBy, ExpiresAt: time.Time{}},
	}, nil
}

// fakeMail is the mailbox of the fake session library: the id its invitations take,
// the addresses mailed, the invitations withdrawn after a failed send, and the failure
// a send answers.
type fakeMail struct {
	id        impulse.UUID
	sent      []string
	withdrawn []impulse.UUID
	err       error
}

// fakePrepared is a prepared invitation of the fake session library.
type fakePrepared struct {
	mail *fakeMail
	row  session.InvitationRow
}

func (p *fakePrepared) Row() *session.InvitationRow {
	row := p.row

	return &row
}

// Send mails the link as the session library does: a failed send withdraws the
// invitation and is the error.
func (p *fakePrepared) Send(context.Context) error {
	if p.mail.err != nil {
		p.mail.withdrawn = append(p.mail.withdrawn, p.row.ID)

		return p.mail.err
	}
	p.mail.sent = append(p.mail.sent, p.row.Email)

	return nil
}

// newManager builds a manager over the store and an engine holding the roles.
func newManager(t *testing.T, store Store, roles map[accesstypes.PolicyScope][]accesstypes.Role, opts ...Option) (*Manager, *fakeEngine) {
	t.Helper()
	engine := &fakeEngine{roles: roles}
	m, err := New(store, engine, fakeSessions{}, opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m, engine
}

// TestNew pins what a manager is built from.
func TestNew(t *testing.T) {
	t.Parallel()

	if _, err := New(nil, &fakeEngine{}, fakeSessions{}); err == nil {
		t.Error("New(nil store) error = nil, want a refusal")
	}
	if _, err := New(&fakeStore{}, nil, fakeSessions{}); err == nil {
		t.Error("New(nil engine) error = nil, want a refusal")
	}
	if _, err := New(&fakeStore{}, &fakeEngine{}, nil); err == nil {
		t.Error("New(nil session API) error = nil, want a refusal")
	}
}
