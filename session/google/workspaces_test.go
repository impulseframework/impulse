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
		{name: "a domain written with the Kelvin sign is another domain than the ASCII one Unicode lowercases it to", workspaces: Workspaces(Workspace("lakeside", "lakeside.edu"), Workspace("other", "la\u212Aeside.edu"))},
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
		{name: "a domain written with the Kelvin sign, which Unicode lowercases to k, is not the named Workspace's", workspaces: listed, hd: "la\u212Aeside.edu"},
		{name: "any Workspace takes a domain written with the Kelvin sign as it was written, its ASCII letters lowercased", workspaces: AnyWorkspace(), hd: "La\u212Aeside.EDU", wantConnection: "la\u212Aeside.edu", wantAdmitted: true},
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
		{name: "a domain written with the Kelvin sign is another domain than the named Workspace's, and any Workspace's connection", workspaces: Workspaces(Workspace("lakeside", "lakeside.edu"), AnyWorkspace()), connection: "la\u212Aeside.edu", want: true},
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

// TestWorkspaceSet_audienceKey pins what a sign-in method's row is compared by: the
// Workspaces a registration admits, each by its domains, and the kind of account it
// admits outside them, never the names the application gives its Workspaces. A domain
// is one Workspace's alone, so a registration that admits other people never reads as
// one that an administrator opened, and one that admits the same people under another
// name, or with a domain written in capitals, reads as the same.
func TestWorkspaceSet_audienceKey(t *testing.T) {
	t.Parallel()

	lakeside := Workspace("lakeside", "lakeside.edu")

	tests := []struct {
		name        string
		opened, now WorkspaceSet
		same        bool
	}{
		{name: "a second Workspace", opened: lakeside, now: Workspaces(lakeside, Workspace("riverside", "riverside.edu"))},
		{name: "another Workspace under the first one's name", opened: lakeside, now: Workspace("lakeside", "riverside.edu")},
		{name: "a second domain of the same Workspace", opened: lakeside, now: Workspace("lakeside", "lakeside.edu", "lakeside.org")},
		{name: "any Workspace beside the named one", opened: lakeside, now: Workspaces(lakeside, AnyWorkspace())},
		{name: "any Google account in place of any Workspace", opened: AnyWorkspace(), now: AnyGoogleAccount()},
		{name: "the same Workspace under another name", opened: lakeside, now: Workspace("Lakeside University", "lakeside.edu"), same: true},
		{name: "the same domain in capitals", opened: lakeside, now: Workspace("lakeside", "Lakeside.EDU"), same: true},
		{name: "the same Workspaces in another order", opened: Workspaces(lakeside, Workspace("riverside", "riverside.edu")), now: Workspaces(Workspace("riverside", "riverside.edu"), lakeside), same: true},
		{name: "the same Workspace declared to enforce a second factor", opened: lakeside, now: lakeside.Attesting(), same: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, set := range []WorkspaceSet{tt.opened, tt.now} {
				if err := set.validate(); err != nil {
					t.Fatalf("validate() error = %v, want a registration the slot accepts", err)
				}
			}
			opened, now := tt.opened.audience().Key(), tt.now.audience().Key()
			if got := opened == now; got != tt.same {
				t.Errorf("the keys of the two registrations are equal = %v, want %v (%s, %s)", got, tt.same, opened, now)
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
		{name: "a domain written with the Kelvin sign, which Unicode lowercases to k, is not the named Workspace's", workspaces: listed, domain: "la\u212Aeside.edu"},
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

// TestWorkspaceSet_nameOf pins what a sentence calls the organization behind a link's
// connection: the application's name for a named Workspace, the domain of one admitted
// as any Workspace, and nothing for an account in no Workspace or a connection the set
// does not admit.
func TestWorkspaceSet_nameOf(t *testing.T) {
	t.Parallel()

	one := Workspace("lakeside", "lakeside.edu")

	tests := []struct {
		name       string
		workspaces WorkspaceSet
		connection string
		want       string
	}{
		{name: "a named Workspace is its name", workspaces: one, connection: "lakeside", want: "lakeside"},
		{name: "a Workspace admitted as any Workspace is its domain", workspaces: AnyWorkspace(), connection: "riverside.edu", want: "riverside.edu"},
		{name: "a named Workspace beside any Workspace keeps its name", workspaces: Workspaces(one, AnyWorkspace()), connection: "lakeside", want: "lakeside"},
		{name: "an account in no Workspace is no organization", workspaces: Workspaces(one, AnyGoogleAccount()), connection: AnyAccountConnection},
		{name: "a Workspace the set does not admit", workspaces: one, connection: "riverside.edu"},
		{name: "no connection", workspaces: one, connection: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.workspaces.nameOf(tt.connection); got != tt.want {
				t.Errorf("nameOf(%q) = %q, want %q", tt.connection, got, tt.want)
			}
		})
	}
}

// TestVouchesFor pins where Google's word for an address is kept: for a Workspace
// account's address and for an address of Google's own mail, which is told by an ASCII
// domain alone. A domain written with a letter from outside ASCII that Unicode
// lowercases to an ASCII one (the dotted capital I, U+0130, to i) is not Gmail's, and
// Google's word for an address there confirms nothing.
func TestVouchesFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		emailVerified bool
		email         string
		hostedDomain  string
		want          bool
	}{
		{name: "a Gmail address Google verified", emailVerified: true, email: "pat@gmail.com", want: true},
		{name: "a Gmail address whatever its case", emailVerified: true, email: "Pat@GMail.COM", want: true},
		{name: "the other domain of Google's own mail", emailVerified: true, email: "pat@googlemail.com", want: true},
		{name: "a Workspace account's address", emailVerified: true, email: "pat@lakeside.edu", hostedDomain: "lakeside.edu", want: true},
		{name: "an address Google did not verify", email: "pat@gmail.com"},
		{name: "an address from anywhere else", emailVerified: true, email: "pat@elsewhere.example"},
		{name: "a domain written with the dotted capital I is not Gmail's", emailVerified: true, email: "pat@gma\u0130l.com"},
		{name: "a domain written with the dotted capital I is not the other domain of Google's mail", emailVerified: true, email: "pat@googlema\u0130l.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := vouchesFor(tt.emailVerified, tt.email, tt.hostedDomain); got != tt.want {
				t.Errorf("vouchesFor(%v, %q, %q) = %v, want %v", tt.emailVerified, tt.email, tt.hostedDomain, got, tt.want)
			}
		})
	}
}

// TestWorkspaceSet_audience_keepsADomainAsWritten holds the identity a named domain's
// accounts are known by to the domain as it was written, its ASCII letters lowercased:
// a domain written with the Kelvin sign (U+212A), which Unicode lowercases to k, is
// another domain than the ASCII one and never shares its identity.
func TestWorkspaceSet_audience_keepsADomainAsWritten(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		domain string
		want   string
	}{
		{name: "an ASCII domain in capitals", domain: "Lakeside.EDU", want: identityWorkspaceDomain + "lakeside.edu"},
		{name: "a domain written with the Kelvin sign", domain: "La\u212Aeside.EDU", want: identityWorkspaceDomain + "la\u212Aeside.edu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Workspace("lakeside", tt.domain).audience().Identities
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("audience().Identities = %q, want [%q]", got, tt.want)
			}
		})
	}
}
