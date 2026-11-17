package google

import (
	"strings"
	"testing"

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
		wantErrHas string
	}{
		{name: "one Workspace with role sync", workspaces: one, roleSync: sync},
		{name: "one Workspace without role sync", workspaces: one, roleSync: DisableRoleSync()},
		{name: "listed Workspaces without role sync", workspaces: listed, roleSync: DisableRoleSync()},
		{name: "any Workspace without role sync", workspaces: AnyWorkspace(), roleSync: DisableRoleSync()},
		{name: "any Google account without role sync", workspaces: AnyGoogleAccount(), roleSync: DisableRoleSync()},
		{name: "the slot's own refusals come first", workspaces: Workspace("lakeside"), roleSync: DisableRoleSync(), wantErrHas: "needs at least one domain"},
		{name: "role sync with listed Workspaces is refused", workspaces: listed, roleSync: sync, wantErrHas: "RoleSync: role synchronization reads the groups of one Workspace"},
		{name: "role sync with any Workspace is refused", workspaces: AnyWorkspace(), roleSync: sync, wantErrHas: "RoleSync: role synchronization reads the groups of one Workspace"},
		{name: "role sync with any Google account is refused", workspaces: AnyGoogleAccount(), roleSync: sync, wantErrHas: "RoleSync: role synchronization reads the groups of one Workspace"},
		{name: "role sync with a named Workspace beside any Google account is refused", workspaces: Workspaces(one, AnyGoogleAccount()), roleSync: sync, wantErrHas: "RoleSync: role synchronization reads the groups of one Workspace"},
		{name: "a missing role sync slot is left to the role sync check", workspaces: one, roleSync: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := refuseShape(tt.workspaces, tt.roleSync)

			if (err != nil) != (tt.wantErrHas != "") || (err != nil && !strings.Contains(err.Error(), tt.wantErrHas)) {
				t.Errorf("refuseShape() error = %v, want one containing %q", err, tt.wantErrHas)
			}
		})
	}
}

func TestWorkspaceSet_audience(t *testing.T) {
	t.Parallel()

	one := Workspace("lakeside", "lakeside.edu")

	tests := []struct {
		name       string
		workspaces WorkspaceSet
		// want is whom opening the method to sign-up admits, in the page's words.
		want        string
		wantLimited bool
	}{
		{name: "one Workspace is its domain", workspaces: one, want: "anyone with an account at lakeside.edu", wantLimited: true},
		{name: "a Workspace with two domains names both", workspaces: Workspace("lakeside", "lakeside.edu", "lakeside.org"), want: "anyone with an account at lakeside.edu or lakeside.org", wantLimited: true},
		{name: "listed Workspaces name every domain", workspaces: Workspaces(Workspace("lakeside", "lakeside.edu", "lakeside.org"), Workspace("riverside", "riverside.edu")), want: "anyone with an account at lakeside.edu, lakeside.org or riverside.edu", wantLimited: true},
		{name: "any Workspace", workspaces: AnyWorkspace(), want: "anyone with a Google Workspace account"},
		{name: "any Google account", workspaces: AnyGoogleAccount(), want: "anyone with a Google account"},
		{name: "a named Workspace beside any Google account is as wide as any Google account", workspaces: Workspaces(one, AnyGoogleAccount()), want: "anyone with a Google account"},
		{name: "any Workspace beside any Google account is any Google account", workspaces: Workspaces(AnyWorkspace(), AnyGoogleAccount()), want: "anyone with a Google account"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.workspaces.audience()
			if got.Words() != tt.want || got.Limited() != tt.wantLimited {
				t.Errorf("audience() = %q (limited %v), want %q (limited %v)", got.Words(), got.Limited(), tt.want, tt.wantLimited)
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
