package signin

import (
	"context"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// fakeTx stands in for a storage's transaction type.
type fakeTx struct{}

// fakeDeclarations stands in for the application's reader of a tenant's declaration:
// declared holds each tenant's declaration (a tenant missing from it declares
// nothing), err fails every read, and asked records the tenants read, in order.
type fakeDeclarations struct {
	declared map[accesstypes.Domain]*TenantDeclaration
	err      error
	asked    []accesstypes.Domain
}

func (f *fakeDeclarations) read(_ context.Context, _ fakeTx, tenant accesstypes.Domain) (*TenantDeclaration, error) {
	f.asked = append(f.asked, tenant)
	if f.err != nil {
		return nil, f.err
	}

	return f.declared[tenant], nil
}

// The identities a sign-in may carry: through the Lakeside Entra tenant, through the
// Lakeside Google Workspace, by password, and the application's own.
var (
	entraIdentity     = &sessioninfo.Identity{Method: sessioninfo.MethodAzure, Connection: "tid-lakeside", Subject: "oid-pat"}
	workspaceIdentity = &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: "lakeside", Subject: "sub-pat"}
	passwordIdentity  = &sessioninfo.Identity{Method: sessioninfo.MethodPassword, Subject: "user-id"}
	ownIdentity       = &sessioninfo.Identity{Method: sessioninfo.MethodApplication, Subject: "user-id"}
	lakesideEntra     = Directory{Method: sessioninfo.MethodAzure, Connection: "tid-lakeside"}
)

func TestTenantDeclaration_Admits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		declaration *TenantDeclaration
		method      sessioninfo.AuthMethod
		connection  string
		want        bool
	}{
		{
			name:   "a tenant that declares nothing admits a password",
			method: sessioninfo.MethodPassword,
			want:   true,
		},
		{
			name:       "a tenant that declares nothing admits a sign-in through any directory",
			method:     sessioninfo.MethodAzure,
			connection: "tid-lakeside",
			want:       true,
		},
		{
			name:       "a tenant that declares nothing admits a sign-in through an OpenID Connect provider",
			method:     sessioninfo.MethodOIDC,
			connection: "https://id.lakeside.example",
			want:       true,
		},
		{
			name:        "an empty declaration admits a sign-in through any directory",
			declaration: &TenantDeclaration{},
			method:      sessioninfo.MethodGoogle,
			connection:  "lakeside",
			want:        true,
		},
		{
			name:        "an empty declaration is a tenant that declares nothing",
			declaration: &TenantDeclaration{},
			method:      sessioninfo.MethodPassword,
			want:        true,
		},
		{
			name:        "a declared directory admits a sign-in through it",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}},
			method:      sessioninfo.MethodAzure,
			connection:  "tid-lakeside",
			want:        true,
		},
		{
			name:        "a tenant with a directory admits a sign-in through another directory unless it is directory only",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}},
			method:      sessioninfo.MethodAzure,
			connection:  "tid-riverside",
			want:        true,
		},
		{
			name:        "a directory-only tenant refuses the same method through another connection: it is not the tenant's directory",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}, DirectoryOnly: true},
			method:      sessioninfo.MethodAzure,
			connection:  "tid-riverside",
			want:        false,
		},
		{
			name:        "a directory-only tenant refuses another method with the same connection: it is not the tenant's directory",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}, DirectoryOnly: true},
			method:      sessioninfo.MethodGoogle,
			connection:  "tid-lakeside",
			want:        false,
		},
		{
			name:        "a directory-only tenant with no directory admits nobody",
			declaration: &TenantDeclaration{DirectoryOnly: true},
			method:      sessioninfo.MethodAzure,
			connection:  "tid-lakeside",
			want:        false,
		},
		{
			name:        "a directory-only tenant admits each directory its declaration carries",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra, {Method: sessioninfo.MethodGoogle, Connection: "lakeside"}}, DirectoryOnly: true},
			method:      sessioninfo.MethodGoogle,
			connection:  "lakeside",
			want:        true,
		},
		{
			name:        "a tenant with a directory admits a password unless it is directory only",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}},
			method:      sessioninfo.MethodPassword,
			want:        true,
		},
		{
			name:        "a directory-only tenant refuses a password",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}, DirectoryOnly: true},
			method:      sessioninfo.MethodPassword,
			want:        false,
		},
		{
			name:        "a directory-only tenant admits its directory",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}, DirectoryOnly: true},
			method:      sessioninfo.MethodAzure,
			connection:  "tid-lakeside",
			want:        true,
		},
		{
			name:        "a directory-only tenant refuses a session the application started itself",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}, DirectoryOnly: true},
			method:      sessioninfo.MethodApplication,
			want:        false,
		},
		{
			name:        "a directory-only tenant refuses a step the application recorded under its own name",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}, DirectoryOnly: true},
			method:      "magic-link",
			connection:  "app",
			want:        false,
		},
		{
			name:        "a directory-only tenant refuses an impersonation",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}, DirectoryOnly: true},
			method:      sessioninfo.MethodImpersonation,
			connection:  "alice@example.com",
			want:        false,
		},
		{
			name:        "a tenant that is not directory only admits an impersonation",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}},
			method:      sessioninfo.MethodImpersonation,
			connection:  "alice@example.com",
			want:        true,
		},
		{
			name:       "any Google account is no directory: a tenant that declares nothing admits it as it admits a password",
			method:     sessioninfo.MethodGoogle,
			connection: GoogleAnyAccountConnection,
			want:       true,
		},
		{
			name:        "a Microsoft personal account is no directory: a tenant with a directory admits it unless it is directory only",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}},
			method:      sessioninfo.MethodAzure,
			connection:  MicrosoftPersonalAccountsConnection,
			want:        true,
		},
		{
			name:        "a directory-only tenant refuses a personal account",
			declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}, DirectoryOnly: true},
			method:      sessioninfo.MethodGoogle,
			connection:  GoogleAnyAccountConnection,
			want:        false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.declaration.Admits(tt.method, tt.connection); got != tt.want {
				t.Errorf("Admits(%q, %q) = %v, want %v", tt.method, tt.connection, got, tt.want)
			}
		})
	}
}

// TestTenantDeclaration_AdmitsAdministrator pins the administrators' way into a
// directory-only tenant: the password, a second factor and the tenant's own
// administrator, all three, and a tenant that is directory only. Take one away and the
// declaration admits nobody by it.
func TestTenantDeclaration_AdmitsAdministrator(t *testing.T) {
	t.Parallel()

	directoryOnly := &TenantDeclaration{Directories: []Directory{lakesideEntra}, DirectoryOnly: true}
	tests := []struct {
		name         string
		declaration  *TenantDeclaration
		method       sessioninfo.AuthMethod
		secondFactor bool
		administers  bool
		want         bool
	}{
		{name: "the tenant's administrator, by password, with a second factor", declaration: directoryOnly, method: sessioninfo.MethodPassword, secondFactor: true, administers: true, want: true},
		{name: "without the second factor", declaration: directoryOnly, method: sessioninfo.MethodPassword, administers: true},
		{name: "a person who does not administer the tenant", declaration: directoryOnly, method: sessioninfo.MethodPassword, secondFactor: true},
		{name: "an administrator who came through another organization's directory", declaration: directoryOnly, method: sessioninfo.MethodAzure, secondFactor: true, administers: true},
		{name: "an administrator who came through Google, a personal account or a Workspace", declaration: directoryOnly, method: sessioninfo.MethodGoogle, secondFactor: true, administers: true},
		{name: "an administrator in a session the application started itself", declaration: directoryOnly, method: sessioninfo.MethodApplication, secondFactor: true, administers: true},
		{name: "an administrator viewed as by someone else", declaration: directoryOnly, method: sessioninfo.MethodImpersonation, secondFactor: true, administers: true},
		{name: "a session that records no establishment", declaration: directoryOnly, secondFactor: true, administers: true},
		{name: "a directory-only tenant with no directory still has its administrators", declaration: &TenantDeclaration{DirectoryOnly: true}, method: sessioninfo.MethodPassword, secondFactor: true, administers: true, want: true},
		{name: "a tenant that is not directory only has no such way in: Admits answers for it", declaration: &TenantDeclaration{Directories: []Directory{lakesideEntra}}, method: sessioninfo.MethodPassword, secondFactor: true, administers: true},
		{name: "a tenant that declares nothing", method: sessioninfo.MethodPassword, secondFactor: true, administers: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.declaration.AdmitsAdministrator(tt.method, tt.secondFactor, tt.administers); got != tt.want {
				t.Errorf("AdmitsAdministrator(%q, %v, %v) = %v, want %v", tt.method, tt.secondFactor, tt.administers, got, tt.want)
			}
		})
	}
}

func TestTenantPolicy(t *testing.T) {
	t.Parallel()

	// patID is the account of the person signing in: what the role store names them by.
	patID := impulse.Must(impulse.UUIDFromString("9a700000-0000-4000-8000-000000000001"))
	global := accesstypes.GlobalPolicyScope()
	every := accesstypes.EveryDomainPolicyScope()
	lakeside := accesstypes.DomainPolicyScope("lakeside")
	riverside := accesstypes.DomainPolicyScope("riverside")
	member := []accesstypes.Role{"Member"}
	directoryOnly := &TenantDeclaration{Directories: []Directory{lakesideEntra}, DirectoryOnly: true}
	open := &TenantDeclaration{Directories: []Directory{lakesideEntra}}

	tests := []struct {
		name string
		// held are the memberships the role store answers; heldErr fails the read.
		held    accesstypes.RoleCollection
		heldErr error
		// administered are the tenants the role store says the person administers;
		// administeredErr fails that read.
		administered    []accesstypes.Domain
		administeredErr error
		// roleNames is what the sign-in method asserted; nil when it synchronizes no
		// roles.
		roleNames []string
		identity  *sessioninfo.Identity
		declared  map[accesstypes.Domain]*TenantDeclaration
		readErr   error
		opts      []TenantPolicyOption
		// wantOutcome, wantCode and wantCause are the decision; wantErr a failed
		// sign-in instead.
		wantOutcome PolicyOutcome
		wantCode    sessioninfo.LoginRefusalCode
		wantCause   error
		wantErr     bool
		// wantRead are the tenants the reader was asked for, in order.
		wantRead []accesstypes.Domain
		// wantFactors are the factors a held sign-in waits for.
		wantFactors []sessioninfo.AuthMethod
	}{
		{
			name:         "the administrator of a directory-only tenant who signs in with a password is held for the mailed code, though every tenant of theirs is directory only",
			held:         accesstypes.RoleCollection{lakeside: {"TenantAdministrator"}},
			administered: []accesstypes.Domain{"lakeside"},
			identity:     passwordIdentity,
			declared:     map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome:  HoldForMFA,
			wantFactors:  []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
			wantRead:     []accesstypes.Domain{"lakeside"},
		},
		{
			name:         "the administrator is held for the code though another tenant of theirs would admit the password: the session reaches the directory-only tenant only with the factor",
			held:         accesstypes.RoleCollection{lakeside: {"TenantAdministrator"}, riverside: member},
			administered: []accesstypes.Domain{"lakeside"},
			identity:     passwordIdentity,
			declared:     map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome:  HoldForMFA,
			wantFactors:  []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
			wantRead:     []accesstypes.Domain{"lakeside"},
		},
		{
			name:         "the administrator who also holds a role for the whole application is held for the code too",
			held:         accesstypes.RoleCollection{global: {"Administrator"}, lakeside: {"TenantAdministrator"}},
			administered: []accesstypes.Domain{"lakeside"},
			identity:     passwordIdentity,
			declared:     map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome:  HoldForMFA,
			wantFactors:  []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
			wantRead:     []accesstypes.Domain{"lakeside"},
		},
		{
			name:         "the administrator of a tenant that is not directory only signs in as anyone does",
			held:         accesstypes.RoleCollection{lakeside: {"TenantAdministrator"}},
			administered: []accesstypes.Domain{"lakeside"},
			identity:     passwordIdentity,
			declared:     map[accesstypes.Domain]*TenantDeclaration{"lakeside": open},
			wantOutcome:  AllowSignIn,
			wantRead:     []accesstypes.Domain{"lakeside", "lakeside"},
		},
		{
			name:         "the administrator of one directory-only tenant among the tenants they administer is held, the tenants read in the store's order until one is directory only",
			held:         accesstypes.RoleCollection{lakeside: {"TenantAdministrator"}, riverside: {"TenantAdministrator"}},
			administered: []accesstypes.Domain{"lakeside", "riverside"},
			identity:     passwordIdentity,
			declared:     map[accesstypes.Domain]*TenantDeclaration{"riverside": {DirectoryOnly: true}},
			wantOutcome:  HoldForMFA,
			wantFactors:  []sessioninfo.AuthMethod{sessioninfo.MethodEmailCode},
			wantRead:     []accesstypes.Domain{"lakeside", "riverside"},
		},
		{
			name:         "an administrator who signs in through another organization's directory is kept out, and nobody asks what they administer",
			held:         accesstypes.RoleCollection{lakeside: {"TenantAdministrator"}},
			administered: []accesstypes.Domain{"lakeside"},
			identity:     workspaceIdentity,
			declared:     map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome:  DenySignIn,
			wantCode:     sessioninfo.RefusedConnectionNotInTenant,
			wantCause:    ErrConnectionNotInTenant,
			wantRead:     []accesstypes.Domain{"lakeside"},
		},
		{
			name:         "an administrator who signs in with a personal account is kept out",
			held:         accesstypes.RoleCollection{lakeside: {"TenantAdministrator"}},
			administered: []accesstypes.Domain{"lakeside"},
			identity:     &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: GoogleAnyAccountConnection, Subject: "sub-pat"},
			declared:     map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome:  DenySignIn,
			wantCode:     sessioninfo.RefusedDirectoryOnlyPersonalAccount,
			wantCause:    ErrDirectoryOnly,
			wantRead:     []accesstypes.Domain{"lakeside"},
		},
		{
			name:         "an administrator in a session the application starts itself is kept out: the way in is the password",
			held:         accesstypes.RoleCollection{lakeside: {"TenantAdministrator"}},
			administered: []accesstypes.Domain{"lakeside"},
			identity:     ownIdentity,
			declared:     map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome:  DenySignIn,
			wantCode:     sessioninfo.RefusedDirectoryOnly,
			wantCause:    ErrDirectoryOnly,
			wantRead:     []accesstypes.Domain{"lakeside"},
		},
		{
			name:        "a person who holds the administrator's role in every tenant administers none of them: the password is admitted by the rule on a role outside any one tenant, with no hold, and the session then reaches no directory-only tenant",
			held:        accesstypes.RoleCollection{every: {"TenantAdministrator"}},
			identity:    passwordIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome: AllowSignIn,
		},
		{
			name:            "a failure to read what the person administers fails the sign-in",
			held:            accesstypes.RoleCollection{lakeside: {"TenantAdministrator"}},
			administeredErr: errors.New("store blip"),
			identity:        passwordIdentity,
			wantErr:         true,
		},
		{
			name:        "a super administrator is admitted by password when every tenant of theirs is directory only, and no declaration is read",
			held:        accesstypes.RoleCollection{global: {accesstypes.SuperAdministrator}, lakeside: member},
			identity:    passwordIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": {DirectoryOnly: true}},
			wantOutcome: AllowSignIn,
		},
		{
			name:        "a super administrator is admitted through a directory no tenant declares",
			held:        accesstypes.RoleCollection{global: {accesstypes.SuperAdministrator}},
			identity:    entraIdentity,
			wantOutcome: AllowSignIn,
		},
		{
			name:        "a global role is a place outside every tenant and admits a sign-in through a directory no tenant declares",
			held:        accesstypes.RoleCollection{global: {"Administrator"}},
			identity:    entraIdentity,
			wantOutcome: AllowSignIn,
		},
		{
			name:        "a role held in every tenant admits a password whatever the one tenant read would say: the policy has no list of the others",
			held:        accesstypes.RoleCollection{every: {"Auditor"}, lakeside: member},
			identity:    passwordIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome: AllowSignIn,
		},
		{
			name:        "the Super Administrator name held in a tenant makes nobody a super administrator",
			held:        accesstypes.RoleCollection{lakeside: {accesstypes.SuperAdministrator}},
			identity:    passwordIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome: DenySignIn,
			wantCode:    sessioninfo.RefusedDirectoryOnly,
			wantCause:   ErrDirectoryOnly,
			wantRead:    []accesstypes.Domain{"lakeside"},
		},
		{
			name:        "a sign-in whose method synchronizes roles is admitted without the store being asked",
			heldErr:     errors.New("the store was asked"),
			roleNames:   []string{"Pilot"},
			identity:    entraIdentity,
			wantOutcome: AllowSignIn,
		},
		{
			name:        "a member of no tenant is refused with no_tenant",
			held:        accesstypes.RoleCollection{},
			identity:    entraIdentity,
			wantOutcome: DenySignIn,
			wantCode:    sessioninfo.RefusedNoTenant,
			wantCause:   ErrNoTenant,
		},
		{
			name:        "a membership with no role is no membership",
			held:        accesstypes.RoleCollection{lakeside: {}, global: {}},
			identity:    passwordIdentity,
			wantOutcome: DenySignIn,
			wantCode:    sessioninfo.RefusedNoTenant,
			wantCause:   ErrNoTenant,
		},
		{
			name:        "a member of no tenant is admitted with AdmitWithoutTenant, by password",
			held:        accesstypes.RoleCollection{},
			identity:    passwordIdentity,
			opts:        []TenantPolicyOption{AdmitWithoutTenant()},
			wantOutcome: AllowSignIn,
		},
		{
			name:        "a member of no tenant is admitted with AdmitWithoutTenant, through a directory no tenant declares",
			held:        accesstypes.RoleCollection{},
			identity:    workspaceIdentity,
			opts:        []TenantPolicyOption{AdmitWithoutTenant()},
			wantOutcome: AllowSignIn,
		},
		{
			name:        "a sign-in through the directory of a directory-only tenant of the person is admitted, whatever the tenants read before it say",
			held:        accesstypes.RoleCollection{riverside: member, lakeside: member},
			identity:    entraIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": {DirectoryOnly: true}, "riverside": directoryOnly},
			wantOutcome: AllowSignIn,
			wantRead:    []accesstypes.Domain{"lakeside", "riverside"},
		},
		{
			name:        "a sign-in through a directory no tenant of the person has is admitted by a tenant that is not directory only: another tenant's switch does not close it",
			held:        accesstypes.RoleCollection{lakeside: member, riverside: member},
			identity:    workspaceIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly, "riverside": open},
			wantOutcome: AllowSignIn,
			wantRead:    []accesstypes.Domain{"lakeside", "riverside"},
		},
		{
			name:        "a sign-in through a directory is refused with connection_not_in_tenant when every tenant of the person is directory only and none has it",
			held:        accesstypes.RoleCollection{lakeside: member, riverside: member},
			identity:    workspaceIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly, "riverside": {DirectoryOnly: true}},
			wantOutcome: DenySignIn,
			wantCode:    sessioninfo.RefusedConnectionNotInTenant,
			wantCause:   ErrConnectionNotInTenant,
			wantRead:    []accesstypes.Domain{"lakeside", "riverside"},
		},
		{
			name:        "a tenant that declares nothing admits a seated person through a directory",
			held:        accesstypes.RoleCollection{lakeside: member},
			identity:    entraIdentity,
			wantOutcome: AllowSignIn,
			wantRead:    []accesstypes.Domain{"lakeside"},
		},
		{
			name:        "a tenant that lists a directory without the switch admits a seated person through another directory",
			held:        accesstypes.RoleCollection{lakeside: member},
			identity:    workspaceIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": open},
			wantOutcome: AllowSignIn,
			wantRead:    []accesstypes.Domain{"lakeside"},
		},
		{
			name:        "a password is admitted when one tenant admits its people other than through its directory",
			held:        accesstypes.RoleCollection{lakeside: member, riverside: member},
			identity:    passwordIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome: AllowSignIn,
			wantRead:    []accesstypes.Domain{"lakeside", "riverside"},
		},
		{
			name:        "a password is refused with directory_only when every tenant of the person is directory only",
			held:        accesstypes.RoleCollection{lakeside: member, riverside: member},
			identity:    passwordIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly, "riverside": {DirectoryOnly: true}},
			wantOutcome: DenySignIn,
			wantCode:    sessioninfo.RefusedDirectoryOnly,
			wantCause:   ErrDirectoryOnly,
			wantRead:    []accesstypes.Domain{"lakeside", "riverside"},
		},
		{
			name:        "a personal Google account is refused with directory_only_personal_account when every tenant of the person is directory only: it is no directory a tenant could declare, and the person used no password",
			held:        accesstypes.RoleCollection{lakeside: member},
			identity:    &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: GoogleAnyAccountConnection, Subject: "sub-pat"},
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome: DenySignIn,
			wantCode:    sessioninfo.RefusedDirectoryOnlyPersonalAccount,
			wantCause:   ErrDirectoryOnly,
			wantRead:    []accesstypes.Domain{"lakeside"},
		},
		{
			name:        "a personal Microsoft account is refused with directory_only_personal_account when every tenant of the person is directory only",
			held:        accesstypes.RoleCollection{lakeside: member, riverside: member},
			identity:    &sessioninfo.Identity{Method: sessioninfo.MethodAzure, Connection: MicrosoftPersonalAccountsConnection, Subject: "oid-pat"},
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly, "riverside": {DirectoryOnly: true}},
			wantOutcome: DenySignIn,
			wantCode:    sessioninfo.RefusedDirectoryOnlyPersonalAccount,
			wantCause:   ErrDirectoryOnly,
			wantRead:    []accesstypes.Domain{"lakeside", "riverside"},
		},
		{
			name:        "a personal account is admitted where a tenant of the person admits a password",
			held:        accesstypes.RoleCollection{lakeside: member},
			identity:    &sessioninfo.Identity{Method: sessioninfo.MethodGoogle, Connection: GoogleAnyAccountConnection, Subject: "sub-pat"},
			declared:    map[accesstypes.Domain]*TenantDeclaration{},
			wantOutcome: AllowSignIn,
			wantRead:    []accesstypes.Domain{"lakeside"},
		},
		{
			name:        "a session the application starts itself is held to directory only like a password",
			held:        accesstypes.RoleCollection{lakeside: member},
			identity:    ownIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantOutcome: DenySignIn,
			wantCode:    sessioninfo.RefusedDirectoryOnly,
			wantCause:   ErrDirectoryOnly,
			wantRead:    []accesstypes.Domain{"lakeside"},
		},
		{
			name:        "a session the application starts itself is admitted by a tenant that is not directory only",
			held:        accesstypes.RoleCollection{lakeside: member},
			identity:    ownIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"lakeside": open},
			wantOutcome: AllowSignIn,
			wantRead:    []accesstypes.Domain{"lakeside"},
		},
		{
			name:        "the reader is asked tenant by tenant, in a fixed order, until one admits",
			held:        accesstypes.RoleCollection{riverside: member, lakeside: member, accesstypes.DomainPolicyScope("hilltop"): member},
			identity:    passwordIdentity,
			declared:    map[accesstypes.Domain]*TenantDeclaration{"hilltop": directoryOnly, "lakeside": directoryOnly},
			wantOutcome: AllowSignIn,
			wantRead:    []accesstypes.Domain{"hilltop", "lakeside", "riverside"},
		},
		{
			name:     "a role store error fails the sign-in",
			heldErr:  errors.New("store blip"),
			identity: passwordIdentity,
			wantErr:  true,
		},
		{
			name:     "a reader error fails the sign-in",
			held:     accesstypes.RoleCollection{lakeside: member},
			identity: passwordIdentity,
			readErr:  errors.New("row blip"),
			wantErr:  true,
			wantRead: []accesstypes.Domain{"lakeside"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			roles := &fakeRoleManager{held: tt.held, heldErr: tt.heldErr, administered: tt.administered, administeredErr: tt.administeredErr}
			reader := &fakeDeclarations{declared: tt.declared, err: tt.readErr}
			policy := TenantPolicy(roles, reader.read, tt.opts...)
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, UserID: patID, Username: "pat@lakeside.edu", Identity: tt.identity, RoleNames: tt.roleNames}

			decision, err := policy(t.Context(), fakeTx{}, req)

			if (err != nil) != tt.wantErr {
				t.Fatalf("TenantPolicy() error = %v, wantErr %v", err, tt.wantErr)
			}
			// The memberships are read under the id of the person's account, once, and
			// never under the sign-in name; a method that synchronizes roles asks nothing.
			wantAsked := []accesstypes.User{accesstypes.User(patID.String())}
			if tt.roleNames != nil {
				wantAsked = nil
			}
			if diff := cmp.Diff(wantAsked, roles.asked, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("users the role store was asked for mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantRead, reader.asked, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("tenants read mismatch (-want +got):\n%s", diff)
			}
			// What a person administers is read for a password sign-in alone, under the
			// id of their account, once the memberships are read, and never for a super
			// administrator or a method that synchronizes roles.
			var wantAdministeredAsked []accesstypes.User
			if tt.roleNames == nil && tt.heldErr == nil && !tt.held.HoldsSuperAdministrator() && tt.identity.Method == sessioninfo.MethodPassword {
				wantAdministeredAsked = []accesstypes.User{accesstypes.User(patID.String())}
			}
			if diff := cmp.Diff(wantAdministeredAsked, roles.administeredAsked, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("users the role store was asked what they administer mismatch (-want +got):\n%s", diff)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.wantFactors, decision.Factors(), cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("Factors() mismatch (-want +got):\n%s", diff)
			}
			if decision.Outcome() != tt.wantOutcome {
				t.Errorf("Outcome() = %v, want %v", decision.Outcome(), tt.wantOutcome)
			}
			if decision.Refusal() != tt.wantCode {
				t.Errorf("Refusal() = %q, want %q", decision.Refusal(), tt.wantCode)
			}
			if tt.wantCause != nil && !errors.Is(decision.Cause(), tt.wantCause) {
				t.Errorf("Cause() = %v, want %v", decision.Cause(), tt.wantCause)
			}
			if tt.wantCause == nil && decision.Cause() != nil {
				t.Errorf("Cause() = %v, want none", decision.Cause())
			}
		})
	}
}

func TestPolicy_Then(t *testing.T) {
	t.Parallel()

	answer := func(decision *Decision, asked *int) Policy[fakeTx] {
		return func(context.Context, fakeTx, *sessioninfo.NewSessionRequest) (*Decision, error) {
			*asked++

			return decision, nil
		}
	}
	fail := func(asked *int) Policy[fakeTx] {
		return func(context.Context, fakeTx, *sessioninfo.NewSessionRequest) (*Decision, error) {
			*asked++

			return nil, errors.New("rule blip")
		}
	}

	tests := []struct {
		name          string
		first         func(asked *int) Policy[fakeTx]
		next          func(asked *int) Policy[fakeTx]
		wantDecision  *Decision
		wantErr       bool
		wantNextAsked bool
	}{
		{
			name:          "the next rule decides after an Allow",
			first:         func(asked *int) Policy[fakeTx] { return answer(Allow(), asked) },
			next:          func(asked *int) Policy[fakeTx] { return answer(Deny("app_denied"), asked) },
			wantDecision:  Deny("app_denied"),
			wantNextAsked: true,
		},
		{
			name:          "an Allow from both allows",
			first:         func(asked *int) Policy[fakeTx] { return answer(Allow(), asked) },
			next:          func(asked *int) Policy[fakeTx] { return answer(Allow(), asked) },
			wantDecision:  Allow(),
			wantNextAsked: true,
		},
		{
			name:         "a Deny is answered as it is and the next rule is not asked",
			first:        func(asked *int) Policy[fakeTx] { return answer(denyBecause("no_tenant", ErrNoTenant), asked) },
			next:         func(asked *int) Policy[fakeTx] { return answer(Allow(), asked) },
			wantDecision: denyBecause("no_tenant", ErrNoTenant),
		},
		{
			name:          "a RequireMFA still asks the next rule, whose refusal stands: a person held for a factor is refused by the rule that refuses them",
			first:         func(asked *int) Policy[fakeTx] { return answer(RequireMFA("email-otp"), asked) },
			next:          func(asked *int) Policy[fakeTx] { return answer(Deny("app_denied"), asked) },
			wantDecision:  Deny("app_denied"),
			wantNextAsked: true,
		},
		{
			name:          "a RequireMFA stands when the next rule allows",
			first:         func(asked *int) Policy[fakeTx] { return answer(RequireMFA("email-otp"), asked) },
			next:          func(asked *int) Policy[fakeTx] { return answer(Allow(), asked) },
			wantDecision:  RequireMFA("email-otp"),
			wantNextAsked: true,
		},
		{
			name:          "a hold of the next rule's own stands over the first rule's: the application names its factor",
			first:         func(asked *int) Policy[fakeTx] { return answer(RequireMFA("email-otp"), asked) },
			next:          func(asked *int) Policy[fakeTx] { return answer(RequireMFA("totp"), asked) },
			wantDecision:  RequireMFA("totp"),
			wantNextAsked: true,
		},
		{
			name:          "no decision of the next rule denies a sign-in the first rule held",
			first:         func(asked *int) Policy[fakeTx] { return answer(RequireMFA("email-otp"), asked) },
			next:          func(asked *int) Policy[fakeTx] { return answer(nil, asked) },
			wantNextAsked: true,
		},
		{
			name:          "an error of the next rule fails a sign-in the first rule held",
			first:         func(asked *int) Policy[fakeTx] { return answer(RequireMFA("email-otp"), asked) },
			next:          fail,
			wantErr:       true,
			wantNextAsked: true,
		},
		{
			name:  "no decision denies and the next rule is not asked",
			first: func(asked *int) Policy[fakeTx] { return answer(nil, asked) },
			next:  func(asked *int) Policy[fakeTx] { return answer(Allow(), asked) },
		},
		{
			name:    "an error of the first rule fails the sign-in and the next rule is not asked",
			first:   fail,
			next:    func(asked *int) Policy[fakeTx] { return answer(Allow(), asked) },
			wantErr: true,
		},
		{
			name:          "an error of the next rule fails the sign-in",
			first:         func(asked *int) Policy[fakeTx] { return answer(Allow(), asked) },
			next:          fail,
			wantErr:       true,
			wantNextAsked: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var firstAsked, nextAsked int
			policy := tt.first(&firstAsked).Then(tt.next(&nextAsked))

			decision, err := policy(t.Context(), fakeTx{}, &sessioninfo.NewSessionRequest{})

			if (err != nil) != tt.wantErr {
				t.Fatalf("Then() error = %v, wantErr %v", err, tt.wantErr)
			}
			if firstAsked != 1 {
				t.Errorf("the first rule was asked %d times, want once", firstAsked)
			}
			if got := nextAsked == 1; got != tt.wantNextAsked {
				t.Errorf("the next rule was asked %d times, want asked %v", nextAsked, tt.wantNextAsked)
			}
			if diff := cmp.Diff(tt.wantDecision, decision, cmp.AllowUnexported(Decision{}), cmpopts.EquateErrors()); diff != "" {
				t.Errorf("decision mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTenantPolicy_Refused(t *testing.T) {
	t.Parallel()

	reader := &fakeDeclarations{}
	tests := []struct {
		name  string
		build func()
	}{
		{
			name:  "no role store",
			build: func() { TenantPolicy[fakeTx](nil, reader.read) },
		},
		{
			name:  "no declaration reader",
			build: func() { TenantPolicy[fakeTx](&fakeRoleManager{}, nil) },
		},
		{
			name:  "Then without a rule",
			build: func() { TenantPolicy(&fakeRoleManager{}, reader.read).Then(nil) },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("want a panic: a wiring mistake is found at startup")
				}
			}()
			tt.build()
		})
	}
}

// passwordDisabledCase is one person PasswordsDisabledByTenants is asked about: their
// memberships, what they administer and what their tenants declare, with the store's
// and the reader's errors.
type passwordDisabledCase struct {
	name            string
	held            accesstypes.RoleCollection
	heldErr         error
	administered    []accesstypes.Domain
	administeredErr error
	declared        map[accesstypes.Domain]*TenantDeclaration
	readErr         error
	// want is the check's answer; wantErr says it fails instead, with the store's or
	// the reader's error.
	want    bool
	wantErr bool
}

// passwordDisabledCases are the people TestPasswordsDisabledByTenants asks about, and
// whose answers TestPasswordsDisabledByTenants_isThePolicysDecision holds to the policy's.
func passwordDisabledCases() []passwordDisabledCase {
	global := accesstypes.GlobalPolicyScope()
	every := accesstypes.EveryDomainPolicyScope()
	lakeside := accesstypes.DomainPolicyScope("lakeside")
	riverside := accesstypes.DomainPolicyScope("riverside")
	member := []accesstypes.Role{"Member"}
	directoryOnly := &TenantDeclaration{Directories: []Directory{lakesideEntra}, DirectoryOnly: true}
	open := &TenantDeclaration{Directories: []Directory{lakesideEntra}}
	bothDirectoryOnly := map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly, "riverside": directoryOnly}

	return []passwordDisabledCase{
		{
			name:     "a member of one tenant, which is directory only",
			held:     accesstypes.RoleCollection{lakeside: member},
			declared: map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			want:     true,
		},
		{
			name:     "a member of two tenants, both directory only",
			held:     accesstypes.RoleCollection{lakeside: member, riverside: member},
			declared: bothDirectoryOnly,
			want:     true,
		},
		{
			name:         "a member of two directory-only tenants who administers one of them keeps the administrators' way in",
			held:         accesstypes.RoleCollection{lakeside: {"TenantAdministrator"}, riverside: member},
			administered: []accesstypes.Domain{"lakeside"},
			declared:     bothDirectoryOnly,
			want:         false,
		},
		{
			name:     "a super administrator",
			held:     accesstypes.RoleCollection{global: {accesstypes.SuperAdministrator}, lakeside: member},
			declared: map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			want:     false,
		},
		{
			name:     "a member of a directory-only tenant and of one that is not",
			held:     accesstypes.RoleCollection{lakeside: member, riverside: member},
			declared: map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly, "riverside": open},
			want:     false,
		},
		{
			name:     "a member of a directory-only tenant who holds a role in every tenant",
			held:     accesstypes.RoleCollection{lakeside: member, every: member},
			declared: map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			want:     false,
		},
		{
			name:     "a member of a directory-only tenant who holds a global role",
			held:     accesstypes.RoleCollection{lakeside: member, global: {"Auditor"}},
			declared: map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			want:     false,
		},
		{
			name: "a member of a tenant that declares nothing",
			held: accesstypes.RoleCollection{lakeside: member},
			want: false,
		},
		{
			name:     "a person who holds no role anywhere",
			held:     accesstypes.RoleCollection{},
			declared: map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			want:     false,
		},
		{
			name:    "the role store fails to list the memberships",
			heldErr: errors.New("roles blip"),
			wantErr: true,
		},
		{
			name:            "the role store fails to list the tenants administered",
			held:            accesstypes.RoleCollection{lakeside: member},
			administeredErr: errors.New("administered blip"),
			declared:        map[accesstypes.Domain]*TenantDeclaration{"lakeside": directoryOnly},
			wantErr:         true,
		},
		{
			name:    "the reader fails",
			held:    accesstypes.RoleCollection{lakeside: member},
			readErr: errors.New("row blip"),
			wantErr: true,
		},
	}
}

func TestPasswordsDisabledByTenants(t *testing.T) {
	t.Parallel()

	patID := impulse.Must(impulse.UUIDFromString("9a700000-0000-4000-8000-000000000001"))
	for _, tt := range passwordDisabledCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			roles := &fakeRoleManager{held: tt.held, heldErr: tt.heldErr, administered: tt.administered, administeredErr: tt.administeredErr}
			reader := &fakeDeclarations{declared: tt.declared, err: tt.readErr}
			check := PasswordsDisabledByTenants(roles, func(ctx context.Context, tenant accesstypes.Domain) (*TenantDeclaration, error) {
				return reader.read(ctx, fakeTx{}, tenant)
			})

			got, err := check(t.Context(), patID)

			// The memberships are read under the id of the person asked about.
			if diff := cmp.Diff([]accesstypes.User{accesstypes.User(patID.String())}, roles.asked); diff != "" {
				t.Errorf("users the role store was asked for mismatch (-want +got):\n%s", diff)
			}
			if tt.wantErr {
				failure := tt.heldErr
				if failure == nil {
					failure = tt.administeredErr
				}
				if failure == nil {
					failure = tt.readErr
				}
				if !errors.Is(err, failure) {
					t.Fatalf("check() error = %v, want one carrying %v", err, failure)
				}
				if got {
					t.Error("check() = true with an error, want false")
				}

				return
			}
			if err != nil {
				t.Fatalf("check() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("check() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPasswordsDisabledByTenants_isThePolicysDecision(t *testing.T) {
	t.Parallel()

	patID := impulse.Must(impulse.UUIDFromString("9a700000-0000-4000-8000-000000000001"))
	for _, tt := range passwordDisabledCases() {
		if tt.wantErr {
			continue
		}
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policyRoles := &fakeRoleManager{held: tt.held, administered: tt.administered}
			policyReader := &fakeDeclarations{declared: tt.declared}
			req := &sessioninfo.NewSessionRequest{Reason: sessioninfo.ReasonLogin, UserID: patID, Username: "pat@lakeside.edu", Identity: passwordIdentity}
			decision, err := TenantPolicy(policyRoles, policyReader.read)(t.Context(), fakeTx{}, req)
			if err != nil {
				t.Fatalf("TenantPolicy() error = %v", err)
			}
			refused := decision.Outcome() == DenySignIn && decision.Refusal() == sessioninfo.RefusedDirectoryOnly

			checkRoles := &fakeRoleManager{held: tt.held, administered: tt.administered}
			checkReader := &fakeDeclarations{declared: tt.declared}
			got, err := PasswordsDisabledByTenants(checkRoles, func(ctx context.Context, tenant accesstypes.Domain) (*TenantDeclaration, error) {
				return checkReader.read(ctx, fakeTx{}, tenant)
			})(t.Context(), patID)
			if err != nil {
				t.Fatalf("check() error = %v", err)
			}

			if got != refused {
				t.Errorf("check() = %v, but the policy refuses the password as directory_only: %v (outcome %v, code %q)", got, refused, decision.Outcome(), decision.Refusal())
			}
			// The two read the same facts, in the same order.
			if diff := cmp.Diff(policyReader.asked, checkReader.asked, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("tenants read mismatch (-policy +check):\n%s", diff)
			}
		})
	}
}

func TestPasswordsDisabledByTenants_Refused(t *testing.T) {
	t.Parallel()

	reader := func(context.Context, accesstypes.Domain) (*TenantDeclaration, error) {
		return nil, nil
	}
	tests := []struct {
		name  string
		build func()
		want  string
	}{
		{
			name:  "no role store",
			build: func() { PasswordsDisabledByTenants(nil, reader) },
			want:  "signin: PasswordsDisabledByTenants was given no role store: pass the access library's user manager",
		},
		{
			name:  "no declaration reader",
			build: func() { PasswordsDisabledByTenants(&fakeRoleManager{}, nil) },
			want:  "signin: PasswordsDisabledByTenants was given no declaration reader: pass a reader of a tenant's declaration that needs no transaction (on Spanner, resource.OutsideSignIn over the sign-in's reader)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if got := recover(); got != tt.want {
					t.Errorf("panic = %v, want %q: a wiring mistake is found at startup", got, tt.want)
				}
			}()
			tt.build()
		})
	}
}
