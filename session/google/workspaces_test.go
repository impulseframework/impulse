package google

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/session/signin"
	"go.uber.org/mock/gomock"

	"github.com/impulseframework/impulse/session/mock/mock_signin"
)

func TestWorkspaceSet_validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		workspaces WorkspaceSet
		wantErrHas string
	}{
		{name: "the zero value is refused", wantErrHas: "Workspaces is required"},
		{name: "an empty list is refused", workspaces: Workspaces(), wantErrHas: "Workspaces is required"},
		{name: "a Workspace without a name is refused", workspaces: Workspace(" ", "example.com"), wantErrHas: "needs a name"},
		{name: "a Workspace without a domain is refused", workspaces: Workspace("lakeside"), wantErrHas: `Workspace "lakeside" needs at least one domain`},
		{name: "a value that is not a domain name is refused", workspaces: Workspace("lakeside", "https://lakeside.edu"), wantErrHas: `"https://lakeside.edu" is not a domain name`},
		{name: "the any-account hint is not a domain name", workspaces: Workspace("lakeside", "*"), wantErrHas: `"*" is not a domain name`},
		{name: "a blank domain is refused", workspaces: Workspace("lakeside", ""), wantErrHas: `"" is not a domain name`},
		{name: "a name used twice is refused", workspaces: Workspaces(Workspace("lakeside", "lakeside.edu"), Workspace("Lakeside", "lakeside.org")), wantErrHas: `the name "Lakeside" is used twice`},
		{name: "a domain in two Workspaces is refused", workspaces: Workspaces(Workspace("lakeside", "lakeside.edu"), Workspace("riverside", "Lakeside.EDU")), wantErrHas: `the domain "Lakeside.EDU" belongs to two Workspaces, "lakeside" and "riverside"`},
		{name: "one Workspace", workspaces: Workspace("lakeside", "lakeside.edu", "lakeside.org")},
		{name: "listed Workspaces", workspaces: Workspaces(Workspace("lakeside", "lakeside.edu"), Workspace("riverside", "riverside.edu"))},
		{name: "any Workspace", workspaces: AnyWorkspace()},
		{name: "any Google account", workspaces: AnyGoogleAccount()},
		{name: "named Workspaces beside the open entries", workspaces: Workspaces(Workspace("lakeside", "lakeside.edu"), AnyWorkspace(), AnyGoogleAccount())},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.workspaces.validate()

			if (err != nil) != (tt.wantErrHas != "") || (err != nil && !strings.Contains(err.Error(), tt.wantErrHas)) {
				t.Errorf("validate() error = %v, want one containing %q", err, tt.wantErrHas)
			}
		})
	}
}

func TestWorkspaceSet_connectionFor(t *testing.T) {
	t.Parallel()

	listed := Workspaces(Workspace("lakeside", "lakeside.edu", "lakeside.org"), Workspace("riverside", "riverside.edu"))

	tests := []struct {
		name           string
		workspaces     WorkspaceSet
		hd             string
		wantConnection string
		wantAdmitted   bool
	}{
		{name: "a named Workspace's domain selects its name", workspaces: listed, hd: "riverside.edu", wantConnection: "riverside", wantAdmitted: true},
		{name: "a Workspace's second domain selects the same name", workspaces: listed, hd: "lakeside.org", wantConnection: "lakeside", wantAdmitted: true},
		{name: "domains are matched without regard to case", workspaces: listed, hd: "Lakeside.EDU", wantConnection: "lakeside", wantAdmitted: true},
		{name: "an unlisted domain is refused", workspaces: listed, hd: "elsewhere.example"},
		{name: "no hd claim is refused by named Workspaces", workspaces: listed, hd: ""},
		{name: "any Workspace keys an unnamed domain by the domain, lowercased", workspaces: Workspaces(listed, AnyWorkspace()), hd: "Elsewhere.Example", wantConnection: "elsewhere.example", wantAdmitted: true},
		{name: "any Workspace still gives a named Workspace its name", workspaces: Workspaces(listed, AnyWorkspace()), hd: "riverside.edu", wantConnection: "riverside", wantAdmitted: true},
		{name: "any Workspace refuses an account outside every Workspace", workspaces: AnyWorkspace(), hd: ""},
		{name: "any Google account admits an account outside every Workspace under the fixed connection", workspaces: AnyGoogleAccount(), hd: "", wantConnection: AnyAccountConnection, wantAdmitted: true},
		{name: "any Google account admits a Workspace account under the fixed connection", workspaces: AnyGoogleAccount(), hd: "elsewhere.example", wantConnection: AnyAccountConnection, wantAdmitted: true},
		{name: "any Google account beside a named Workspace keeps the name for its members", workspaces: Workspaces(Workspace("lakeside", "lakeside.edu"), AnyGoogleAccount()), hd: "lakeside.edu", wantConnection: "lakeside", wantAdmitted: true},
		{name: "the zero value admits no one", hd: "lakeside.edu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			connection, admitted := tt.workspaces.connectionFor(tt.hd)

			if connection != tt.wantConnection || admitted != tt.wantAdmitted {
				t.Errorf("connectionFor(%q) = %q, %v; want %q, %v", tt.hd, connection, admitted, tt.wantConnection, tt.wantAdmitted)
			}
		})
	}
}

// TestWorkspaceSet_admitsConnection proves which links the set still signs in: the ones
// whose connection it would write for the account today. A named Workspace's link
// stands while the Workspace is named, a link under AnyGoogleAccount while any account
// is admitted, and a link made under AnyWorkspace, which carries the account's domain,
// while any Workspace is admitted and no named Workspace has taken that domain.
func TestWorkspaceSet_admitsConnection(t *testing.T) {
	t.Parallel()

	named := Workspace("fleet", "fleet.example")

	tests := []struct {
		name       string
		workspaces WorkspaceSet
		connection string
		want       bool
	}{
		{name: "a named Workspace's name", workspaces: named, connection: "fleet", want: true},
		{name: "a Workspace the set does not name", workspaces: named, connection: "other"},
		{name: "a named Workspace's domain is no connection of a set that admits it by name", workspaces: named, connection: "fleet.example"},
		{name: "an account outside a Workspace is not admitted by named Workspaces", workspaces: named, connection: AnyAccountConnection},
		{name: "no connection", workspaces: Workspaces(named, AnyWorkspace(), AnyGoogleAccount()), connection: ""},
		{name: "a domain under any Workspace", workspaces: AnyWorkspace(), connection: "lakeside.edu", want: true},
		{name: "an account outside a Workspace is not admitted by any Workspace", workspaces: AnyWorkspace(), connection: AnyAccountConnection},
		{name: "a named Workspace keeps its name beside any Workspace", workspaces: Workspaces(named, AnyWorkspace()), connection: "fleet", want: true},
		{name: "another domain beside a named Workspace, under any Workspace", workspaces: Workspaces(named, AnyWorkspace()), connection: "lakeside.edu", want: true},
		{name: "a domain a named Workspace has taken: its accounts are written under the name now", workspaces: Workspaces(named, AnyWorkspace()), connection: "Fleet.Example"},
		{name: "the fixed connection under any Google account", workspaces: AnyGoogleAccount(), connection: AnyAccountConnection, want: true},
		{name: "a domain is no connection of a set that admits any Google account alone", workspaces: AnyGoogleAccount(), connection: "lakeside.edu"},
		{name: "the zero value admits no link", connection: "fleet"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.workspaces.admitsConnection(tt.connection); got != tt.want {
				t.Errorf("admitsConnection(%q) = %v, want %v", tt.connection, got, tt.want)
			}
		})
	}
}

func TestRefuseShape(t *testing.T) {
	t.Parallel()

	manager := mock_signin.NewMockUserRoleManager(gomock.NewController(t))
	sync := RoleSync(manager, "app-", DirectGroups())
	one := Workspace("lakeside", "lakeside.edu")
	listed := Workspaces(one, Workspace("riverside", "riverside.edu"))

	tests := []struct {
		name       string
		workspaces WorkspaceSet
		roleSync   RoleSyncConfig
		accounts   signin.StandardAccountRule
		rule       bool
		wantErrHas string
	}{
		{name: "one Workspace with role sync and members of the directory", workspaces: one, roleSync: sync, accounts: signin.MembersOfDirectory},
		{name: "one Workspace with invited users only", workspaces: one, roleSync: DisableRoleSync(), accounts: signin.InvitedUsersOnly},
		{name: "listed Workspaces without role sync", workspaces: listed, roleSync: DisableRoleSync(), accounts: signin.MembersOfDirectory},
		{name: "the slot's own refusals come first", workspaces: Workspace("lakeside"), roleSync: DisableRoleSync(), accounts: signin.MembersOfDirectory, wantErrHas: "needs at least one domain"},
		{name: "role sync with listed Workspaces is refused", workspaces: listed, roleSync: sync, accounts: signin.InvitedUsersOnly, wantErrHas: "RoleSync: role synchronization reads the groups of one Workspace"},
		{name: "role sync with any Workspace is refused", workspaces: AnyWorkspace(), roleSync: sync, accounts: signin.InvitedUsersOnly, wantErrHas: "RoleSync: role synchronization reads the groups of one Workspace"},
		{name: "role sync with any Google account is refused", workspaces: AnyGoogleAccount(), roleSync: sync, accounts: signin.InvitedUsersOnly, wantErrHas: "RoleSync: role synchronization reads the groups of one Workspace"},
		{name: "role sync with a named Workspace beside any Google account is refused", workspaces: Workspaces(one, AnyGoogleAccount()), roleSync: sync, accounts: signin.InvitedUsersOnly, wantErrHas: "RoleSync: role synchronization reads the groups of one Workspace"},
		{name: "open sign-up with one Workspace is refused", workspaces: one, roleSync: DisableRoleSync(), accounts: signin.OpenSignUp, wantErrHas: "Accounts: open sign-up is for any Workspace or any Google account"},
		{name: "open sign-up with listed Workspaces is refused", workspaces: listed, roleSync: DisableRoleSync(), accounts: signin.OpenSignUp, wantErrHas: "Accounts: open sign-up is for any Workspace or any Google account"},
		{name: "open sign-up with any Google account", workspaces: AnyGoogleAccount(), roleSync: DisableRoleSync(), accounts: signin.OpenSignUp},
		{name: "open sign-up with any Workspace", workspaces: AnyWorkspace(), roleSync: DisableRoleSync(), accounts: signin.OpenSignUp},
		{name: "members of the directory with any Workspace and no rule is refused", workspaces: AnyWorkspace(), roleSync: DisableRoleSync(), accounts: signin.MembersOfDirectory, wantErrHas: "would sign up the world"},
		{name: "members of the directory with any Google account and no rule is refused", workspaces: AnyGoogleAccount(), roleSync: DisableRoleSync(), accounts: signin.MembersOfDirectory, wantErrHas: "would sign up the world"},
		{name: "members of the directory with any Workspace and a rule of the application's own", workspaces: AnyWorkspace(), roleSync: DisableRoleSync(), accounts: signin.MembersOfDirectory, rule: true},
		{name: "invited users only with any Google account", workspaces: AnyGoogleAccount(), roleSync: DisableRoleSync(), accounts: signin.InvitedUsersOnly},
		{name: "a missing role sync slot is left to the role sync check", workspaces: one, roleSync: nil, accounts: signin.MembersOfDirectory},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := refuseShape(tt.workspaces, tt.roleSync, tt.accounts, !tt.rule)

			if (err != nil) != (tt.wantErrHas != "") || (err != nil && !strings.Contains(err.Error(), tt.wantErrHas)) {
				t.Errorf("refuseShape() error = %v, want one containing %q", err, tt.wantErrHas)
			}
		})
	}
}

func TestWorkspaceSet_admitsAddress(t *testing.T) {
	t.Parallel()

	listed := Workspaces(Workspace("lakeside", "lakeside.edu", "lakeside.org"), Workspace("riverside", "riverside.edu"))

	tests := []struct {
		name       string
		workspaces WorkspaceSet
		domain     string
		want       bool
	}{
		{name: "a named Workspace's domain is admitted", workspaces: listed, domain: "riverside.edu", want: true},
		{name: "a Workspace's second domain is admitted", workspaces: listed, domain: "lakeside.org", want: true},
		{name: "domains are matched without regard to case", workspaces: listed, domain: "Lakeside.EDU", want: true},
		{name: "a domain outside the named Workspaces is refused", workspaces: listed, domain: "elsewhere.example"},
		{name: "a personal account's domain is refused by named Workspaces", workspaces: listed, domain: "gmail.com"},
		{name: "any Workspace admits a domain outside the named ones", workspaces: Workspaces(listed, AnyWorkspace()), domain: "elsewhere.example", want: true},
		{name: "any Workspace refuses gmail.com, an account outside every Workspace", workspaces: AnyWorkspace(), domain: "gmail.com"},
		{name: "any Workspace refuses googlemail.com", workspaces: AnyWorkspace(), domain: "googlemail.com"},
		{name: "the personal domains are matched without regard to case", workspaces: AnyWorkspace(), domain: "GMail.com"},
		{name: "any Google account admits a personal account's domain", workspaces: AnyGoogleAccount(), domain: "gmail.com", want: true},
		{name: "any Google account admits any domain", workspaces: AnyGoogleAccount(), domain: "elsewhere.example", want: true},
		{name: "the zero value admits no one", domain: "lakeside.edu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.workspaces.admitsAddress(tt.domain); got != tt.want {
				t.Errorf("admitsAddress(%q) = %v, want %v", tt.domain, got, tt.want)
			}
		})
	}
}
