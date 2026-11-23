package identity

import (
	"context"
	"fmt"
	"maps"
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
	"github.com/impulseframework/impulse/session/sessioninfo"
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

	return fakeDatabase{}.ExecuteFunc(t.Context(), f)
}

// inTransactionKey marks the context a transaction's function runs under.
type inTransactionKey struct{}

// fakeDatabase runs a function in a transaction as the application's database does (a
// resource.Executor), and marks the context the function runs under as the Spanner
// client marks it. A read on another client under such a context is one the database
// refuses ("Cloud Spanner does not support nested transactions"), and the permission
// engine's listings read on the engine's own client, so the fake engine refuses a
// listing asked under the mark (fakeUserManager.Roles).
type fakeDatabase struct{}

func (fakeDatabase) ExecuteFunc(ctx context.Context, f func(ctx context.Context, txn resource.ReadWriteTransaction) error) error {
	marked := context.WithValue(ctx, inTransactionKey{}, true)
	if err := resource.NewMockClient(stubTxn{}, nil, nil).ExecuteFunc(marked, f); err != nil {
		return errors.Wrap(err, "resource.MockClient.ExecuteFunc()")
	}

	return nil
}

// fakeStore is an auth's tables in memory: the committed rows the reads answer from, and
// a log of the writes the rules buffered, in order.
type fakeStore struct {
	// sessions are the live sessions of each user, memberships the committed rows, each
	// held under the id of its person's account (User), and roles the rows of the roles
	// table, the custom roles.
	sessions    map[impulse.UUID][]impulse.UUID
	memberships map[access.MembershipKey]bool
	roles       map[access.RoleKey]bool
	// usernames are the users that exist. A session row exists and stands unless ended
	// names it (it has ended) or missing does (no row holds the id). records are the
	// impersonation records by session, true while one stands, and heldBy the sessions
	// whose standing record names a username as its local actor.
	usernames map[impulse.UUID]string
	// disabled are the users that exist and cannot sign in.
	disabled map[impulse.UUID]bool
	// emails are the accounts of the users who have confirmed an address, by address.
	emails  map[string]impulse.UUID
	ended   map[impulse.UUID]bool
	missing map[impulse.UUID]bool
	records map[impulse.UUID]bool
	heldBy  map[string][]impulse.UUID
	// actsAsRole are the held records whose session acts as a role, and devices the
	// unrevoked trusted devices of each user.
	actsAsRole map[impulse.UUID]bool
	devices    map[impulse.UUID][]impulse.UUID
	// links are the mail links by id, and invitations the unconsumed invitations into
	// each tenant.
	links       map[impulse.UUID]*MailLink
	invitations map[accesstypes.Domain][]impulse.UUID
	// invited are the unconsumed invitations that make a tenant.
	invited []InvitedTenantRow
	// passwords are the users that exist for the password rule, true where one holds a
	// password; identities the user each identity links to; tokens the tokens by id,
	// and families the unrevoked tokens of each family.
	passwords  map[impulse.UUID]bool
	identities map[impulse.UUID]impulse.UUID
	// cameThrough is the method and the connection an identity came through, for the
	// identities a test says it of; every other came through google's lakeside.
	cameThrough map[impulse.UUID]Link
	tokens      map[impulse.UUID]*Token
	families    map[impulse.UUID][]impulse.UUID
	// methodRows are the rows of the sign-in methods table, by the method's name, and
	// openedFor the audience the last row written was opened for, nil for none.
	methodRows map[string]SignInMethodRow
	openedFor  *string
	// settingsRow is the row of the sign-in settings table, nil for none.
	settingsRow *SignInSettingsRow
	// fakeGroupTables are the groups of people, their members and the roles they hold
	// (groups_test.go).
	fakeGroupTables
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
	if change.Name != nil {
		name := "<none>"
		if change.Name.Name != nil {
			name = *change.Name.Name
		}
		line += " name=" + name
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

func (f *fakeStore) UserHasPassword(_ context.Context, _ resource.ReadOnlyTransaction, id impulse.UUID) (has, found bool, err error) {
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

func (f *fakeStore) Identities(_ context.Context, _ resource.ReadOnlyTransaction, userID impulse.UUID) ([]Link, error) {
	if err := f.fail("Identities"); err != nil {
		return nil, err
	}
	var links []Link
	for id, linked := range f.identities {
		if linked != userID {
			continue
		}
		link := Link{ID: id, Method: sessioninfo.MethodGoogle, Connection: "lakeside"}
		if through, said := f.cameThrough[id]; said {
			link.Method, link.Connection = through.Method, through.Connection
		}
		links = append(links, link)
	}
	slices.SortFunc(links, func(a, b Link) int { return strings.Compare(a.ID.String(), b.ID.String()) })

	return links, nil
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

func (f *fakeStore) UserDisabled(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) (disabled, found bool, err error) {
	if err := f.fail("UserDisabled"); err != nil {
		return false, false, err
	}
	_, found = f.usernames[id]

	return f.disabled[id], found, nil
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

func (f *fakeStore) UserByVerifiedEmail(_ context.Context, _ resource.ReadWriteTransaction, email string) (userID impulse.UUID, found bool, err error) {
	if err := f.fail("UserByVerifiedEmail"); err != nil {
		return impulse.NilUUID, false, err
	}
	userID, found = f.emails[email]

	return userID, found, nil
}

func (f *fakeStore) ConfirmedEmail(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) (email string, found bool, err error) {
	if err := f.fail("ConfirmedEmail"); err != nil {
		return "", false, err
	}
	for address, holder := range f.emails {
		if holder == id {
			return address, true, nil
		}
	}

	return "", false, nil
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

func (f *fakeStore) RemovePassword(_ context.Context, _ resource.ReadWriteTransaction, id impulse.UUID) error {
	if err := f.fail("RemovePassword"); err != nil {
		return err
	}
	f.writes = append(f.writes, "remove password "+id.String())

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
	if row.InvitedByUserID.Valid {
		invitedBy = row.InvitedByUserID.String()
	}
	line := "insert invitation " + row.ID.String() + " " + row.Email + " tenant=" + row.TenantID + " roles=" + strings.Join(row.RoleNames, ",") + " by=" + invitedBy
	if row.EveryTenant {
		line += " every-tenant"
	}
	if row.Name != "" {
		line += " name=" + row.Name
	}
	if row.NewTenant != nil {
		line += " new-tenant=" + row.NewTenant.Name + " directory-only=" + boolText(row.NewTenant.DirectoryOnly) + " details=" + string(row.NewTenant.Details)
	}
	f.writes = append(f.writes, line)

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

func (f *fakeStore) SignInMethodRows(context.Context, resource.ReadOnlyTransaction) ([]SignInMethodRow, error) {
	if err := f.fail("SignInMethodRows"); err != nil {
		return nil, err
	}
	rows := make([]SignInMethodRow, 0, len(f.methodRows))
	for _, name := range slices.Sorted(maps.Keys(f.methodRows)) {
		rows = append(rows, f.methodRows[name])
	}

	return rows, nil
}

func (f *fakeStore) SetSignInMethod(_ context.Context, _ resource.ReadWriteTransaction, row *SignInMethodRow) error {
	if err := f.fail("SetSignInMethod"); err != nil {
		return err
	}
	role := "<none>"
	if row.DefaultRole != nil {
		role = *row.DefaultRole
	}
	f.writes = append(f.writes, fmt.Sprintf("set sign-in method %s open=%t role=%s", row.Name, row.OpenSignUp, role))
	f.openedFor = row.OpenedFor

	return nil
}

func (f *fakeStore) SetSelfServiceReset(_ context.Context, _ resource.ReadWriteTransaction, name string, on bool) error {
	if err := f.fail("SetSelfServiceReset"); err != nil {
		return err
	}
	f.writes = append(f.writes, fmt.Sprintf("set self-service reset of %s on=%t", name, on))

	return nil
}

func (f *fakeStore) SignInSettingsRow(context.Context, resource.ReadOnlyTransaction) (*SignInSettingsRow, error) {
	if err := f.fail("SignInSettingsRow"); err != nil {
		return nil, err
	}

	return f.settingsRow, nil
}

func (f *fakeStore) SetRequireSecondFactor(_ context.Context, _ resource.ReadWriteTransaction, on bool) error {
	if err := f.fail("SetRequireSecondFactor"); err != nil {
		return err
	}
	f.writes = append(f.writes, fmt.Sprintf("set require second factor on=%t", on))

	return nil
}

func (f *fakeStore) SetRememberBrowserDays(_ context.Context, _ resource.ReadWriteTransaction, days int64) error {
	if err := f.fail("SetRememberBrowserDays"); err != nil {
		return err
	}
	f.writes = append(f.writes, fmt.Sprintf("set remember browser days=%d", days))

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

func (f *fakeStore) Memberships(_ context.Context, _ resource.ReadWriteTransaction, userID impulse.UUID) ([]access.MembershipKey, error) {
	if err := f.fail("Memberships"); err != nil {
		return nil, err
	}
	var keys []access.MembershipKey
	for key, held := range f.memberships {
		if held && key.User == User(userID) {
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

// InsertMembership writes the row unless the membership is held, as the generated
// store's create-unless-there does.
func (f *fakeStore) InsertMembership(_ context.Context, _ resource.ReadWriteTransaction, key *access.MembershipKey) (bool, error) {
	if err := f.fail("InsertMembership"); err != nil {
		return false, err
	}
	if f.memberships[*key] {
		return false, nil
	}
	f.writes = append(f.writes, "insert membership "+membershipText(key))

	return true, nil
}

func (f *fakeStore) DeleteMembership(_ context.Context, _ resource.ReadWriteTransaction, key *access.MembershipKey) error {
	if err := f.fail("DeleteMembership"); err != nil {
		return err
	}
	f.writes = append(f.writes, "delete membership "+membershipText(key))

	return nil
}

func (f *fakeStore) TenantMemberships(_ context.Context, _ resource.ReadWriteTransaction, domain accesstypes.Domain) ([]access.MembershipKey, error) {
	if err := f.fail("TenantMemberships"); err != nil {
		return nil, err
	}
	at := access.NewMembershipKey(accesstypes.DomainPolicyScope(domain), "", "")
	var keys []access.MembershipKey
	for key, held := range f.memberships {
		if held && key.Kind == at.Kind && key.Axis == at.Axis && key.Domain == at.Domain {
			keys = append(keys, key)
		}
	}
	slices.SortFunc(keys, func(a, b access.MembershipKey) int { return strings.Compare(membershipText(&a), membershipText(&b)) })

	return keys, nil
}

// DeleteTenantRoles logs one write for the tenant's custom roles and their grants, and
// answers how many rows of the roles table the tenant holds.
func (f *fakeStore) DeleteTenantRoles(_ context.Context, _ resource.ReadWriteTransaction, domain accesstypes.Domain) (int, error) {
	if err := f.fail("DeleteTenantRoles"); err != nil {
		return 0, err
	}
	at := access.NewMembershipKey(accesstypes.DomainPolicyScope(domain), "", "")
	roles := 0
	for key, held := range f.roles {
		if held && key.Kind == at.Kind && key.Axis == at.Axis && key.Domain == at.Domain {
			roles++
		}
	}
	if roles > 0 {
		f.writes = append(f.writes, "delete the custom roles of "+string(domain))
	}

	return roles, nil
}

func (f *fakeStore) TenantInvitations(_ context.Context, _ resource.ReadWriteTransaction, domain accesstypes.Domain) ([]impulse.UUID, error) {
	if err := f.fail("TenantInvitations"); err != nil {
		return nil, err
	}

	return f.invitations[domain], nil
}

func (f *fakeStore) InvitedTenants(context.Context, resource.ReadOnlyTransaction) ([]InvitedTenantRow, error) {
	if err := f.fail("InvitedTenants"); err != nil {
		return nil, err
	}

	return f.invited, nil
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
	// tenantAdministrators are the roles the engine answers who administers a tenant by,
	// none unless a test names them.
	tenantAdministrators []accesstypes.Role
}

// TenantAdministratorRoles answers the roles a test named as a tenant's administrators'.
func (e *fakeEngine) TenantAdministratorRoles() []accesstypes.Role {
	return e.tenantAdministrators
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
			return access.MembershipKey{}, httpio.NewNotFoundMessageWithError(errors.Newf("role %q does not exist", role), "The role "+roleInWords(role)+" does not exist.")
		}
	}
	if user == "" {
		return access.MembershipKey{}, httpio.NewBadRequestMessage("user cannot be empty string")
	}

	return access.NewMembershipKey(scope, user, role), nil
}

// Roles lists the roles of the scope as the library does, each described by its name.
// The library reads the custom ones on the engine's own client, which the database
// refuses under a transaction's context (fakeDatabase), so a listing asked inside a
// transaction fails here as it does against a database.
func (u *fakeUserManager) Roles(ctx context.Context, scope accesstypes.PolicyScope) ([]access.RoleInfo, error) {
	if ctx.Value(inTransactionKey{}) != nil {
		return nil, errors.New("the roles of a scope were listed inside a transaction: the engine reads them on a client of its own, which the database refuses under a transaction's context")
	}
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
	// methods are the sign-in methods the Auth registers.
	methods []session.RegisteredMethod
	// unoffered are the sign-in methods the Auth's login page offers no registration of,
	// and untaken the connections no registration takes: an identity of either signs
	// nobody in. Every other identity signs in through the registration named for its
	// method.
	unoffered map[sessioninfo.AuthMethod]bool
	untaken   map[string]bool
	// named are the names the registrations give a connection, and labels the labels of
	// the buttons a person signs in to a method's directories with.
	named  map[string]string
	labels map[sessioninfo.AuthMethod]string
	// tenantNames are what the application calls its tenants, by key.
	tenantNames map[accesstypes.Domain]string
}

func (s fakeSessions) DirectoryName(method sessioninfo.AuthMethod, connection string) (name, label string) {
	return s.named[connection], s.labels[method]
}

func (s fakeSessions) TenantName(tenant accesstypes.Domain) string {
	return s.tenantNames[tenant]
}

// asked records a message the rules asked the session library to mail, and answers the
// mailbox's failure; a fake with no mailbox mails nothing.
func (s fakeSessions) asked(what string) error {
	if s.mail == nil {
		return nil
	}
	if s.mail.err != nil {
		return s.mail.err
	}
	s.mail.asked = append(s.mail.asked, what)

	return nil
}

// askerText says an asker as the fake's record keeps it.
func askerText(by session.AskedBy) string {
	switch {
	case !by.Administrator:
		return "the person"
	case by.Tenant == "":
		return "the application"
	default:
		return "tenant " + by.Tenant
	}
}

func (s fakeSessions) RequestAddressChange(_ context.Context, userID impulse.UUID, email string, by session.AskedBy) error {
	return s.asked("address change of " + userID.String() + " to " + email + " asked by " + askerText(by))
}

func (s fakeSessions) SendPasswordReset(_ context.Context, userID impulse.UUID, by session.AskedBy) error {
	return s.asked("password reset of " + userID.String() + " asked by " + askerText(by))
}

func (s fakeSessions) EndPasswordResetLinks(_ context.Context, userID impulse.UUID) error {
	return s.asked("end the password reset links of " + userID.String())
}

func (s fakeSessions) NoticeFactorReset(_ context.Context, userID impulse.UUID) error {
	return s.asked("factor reset notice to " + userID.String())
}

func (s fakeSessions) NoticeRemovedFromTenant(_ context.Context, to, tenant string, accountDeleted bool) error {
	return s.asked("removal notice to " + to + " from " + tenant + " deleted=" + boolText(accountDeleted))
}

func (s fakeSessions) SignInMethods() []session.RegisteredMethod {
	return s.methods
}

func (s fakeSessions) SignsInThrough(method sessioninfo.AuthMethod, connection string) []string {
	if s.unoffered[method] || s.untaken[connection] {
		return nil
	}

	return []string{string(method)}
}

func (s fakeSessions) PrepareInvitation(_ context.Context, req *session.InvitationRequest) (session.PreparedInvitation, error) {
	if s.mail == nil {
		return nil, errors.New("the fake was given no mailbox, so it prepares no invitation")
	}
	if !strings.Contains(req.Email, "@") {
		return nil, httpio.NewBadRequestMessage("the address is not an email address")
	}

	return &fakePrepared{
		mail: s.mail,
		row: session.InvitationRow{
			ID: s.mail.id, TokenHash: []byte("hash"), Email: req.Email, Name: strings.TrimSpace(req.Name), TenantID: req.TenantID, EveryTenant: req.EveryTenant,
			RoleNames: req.RoleNames, InvitedByUserID: req.InvitedByUserID, ExpiresAt: time.Time{}, NewTenant: req.NewTenant,
		},
	}, nil
}

// fakeMail is the mailbox of the fake session library: the id its invitations take,
// the addresses mailed, the invitations ended after a failed send, and the failure
// a send answers.
type fakeMail struct {
	id    impulse.UUID
	sent  []string
	ended []impulse.UUID
	err   error
	// asked are the messages about an account the rules asked for, in order: the link
	// that confirms a new address, the reset link, and the notices.
	asked []string
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

// Send mails the link as the session library does: a failed send ends the
// invitation and is the error.
func (p *fakePrepared) Send(context.Context) error {
	if p.mail.err != nil {
		p.mail.ended = append(p.mail.ended, p.row.ID)

		return p.mail.err
	}
	p.mail.sent = append(p.mail.sent, p.row.Email)

	return nil
}

// managedAuth is the auth the tests' managers manage, and otherAuth another one over the
// same database, whose outlet serves the management of the first.
const (
	managedAuth = "members"
	otherAuth   = "staff"
)

// newManager builds a manager over the store and an engine holding the roles.
func newManager(t *testing.T, store Store, roles map[accesstypes.PolicyScope][]accesstypes.Role, opts ...Option) (*Manager, *fakeEngine) {
	t.Helper()
	engine := &fakeEngine{roles: roles}
	m, err := New(managedAuth, store, engine, fakeSessions{}, opts...)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return m, engine
}

// TestNew pins what a manager is built from.
func TestNew(t *testing.T) {
	t.Parallel()

	if _, err := New("", &fakeStore{}, &fakeEngine{}, fakeSessions{}); err == nil {
		t.Error("New(no auth name) error = nil, want a refusal")
	}
	if _, err := New(managedAuth, nil, &fakeEngine{}, fakeSessions{}); err == nil {
		t.Error("New(nil store) error = nil, want a refusal")
	}
	if _, err := New(managedAuth, &fakeStore{}, nil, fakeSessions{}); err == nil {
		t.Error("New(nil engine) error = nil, want a refusal")
	}
	if _, err := New(managedAuth, &fakeStore{}, &fakeEngine{}, nil); err == nil {
		t.Error("New(nil session API) error = nil, want a refusal")
	}
}
