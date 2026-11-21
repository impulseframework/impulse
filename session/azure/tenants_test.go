package azure

import (
	"strings"
	"testing"

	"github.com/impulseframework/impulse/session/mock/mock_signin"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"go.uber.org/mock/gomock"
)

const (
	// tenantLake, tenantRiver and tenantOther are three Entra tenants' ids.
	tenantLake  = "11111111-1111-1111-1111-111111111111"
	tenantRiver = "22222222-2222-2222-2222-222222222222"
	tenantOther = "33333333-3333-3333-3333-333333333333"
	// testAuthority is the Microsoft identity platform the tests' tenants live on.
	testAuthority = "https://login"
)

func TestTenantSet_validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tenants TenantSet
		wantErr string
	}{
		{name: "one tenant", tenants: Tenant(tenantLake)},
		{name: "a named tenant", tenants: NamedTenant(tenantLake, "lakeside")},
		{name: "several tenants", tenants: Tenants(Tenant(tenantLake), NamedTenant(tenantRiver, "riverside"))},
		{name: "any tenant", tenants: AnyTenant()},
		{name: "personal accounts", tenants: PersonalAccounts()},
		{name: "listed tenants beside any tenant and personal accounts", tenants: Tenants(NamedTenant(tenantLake, "lakeside"), AnyTenant(), PersonalAccounts())},
		{name: "a tenant id in upper case is a tenant id", tenants: Tenant(strings.ToUpper(tenantLake))},
		{name: "the zero value admits no one", wantErr: "Tenants is required"},
		{name: "a tenant id that is not a GUID", tenants: Tenant("lakeside"), wantErr: `Tenants: "lakeside" is not a tenant id`},
		{name: "an empty tenant id", tenants: Tenant(""), wantErr: `Tenants: "" is not a tenant id`},
		{name: "the consumer tenant listed", tenants: Tenant(ConsumerTenantID), wantErr: "is Microsoft's consumer tenant, the personal accounts: use azure.PersonalAccounts()"},
		{name: "a tenant listed twice, in another case", tenants: Tenants(Tenant(tenantLake), NamedTenant(strings.ToUpper(tenantLake), "again")), wantErr: "is listed twice"},
		{name: "a blank name", tenants: NamedTenant(tenantLake, "  "), wantErr: "is blank"},
		{name: "a name used twice, in another case", tenants: Tenants(NamedTenant(tenantLake, "lakeside"), NamedTenant(tenantRiver, "Lakeside")), wantErr: `the name "Lakeside" is used twice`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.tenants.validate()
			if (err != nil) != (tt.wantErr != "") {
				t.Fatalf("validate() error = %v, wantErr %q", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("validate() error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestTenantSet_admits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		tenants          TenantSet
		tid              string
		wantOrganization string
		wantOK           bool
		wantCode         sessioninfo.LoginRefusalCode
	}{
		{name: "one tenant admits its tenant under its id", tenants: Tenant(tenantLake), tid: tenantLake, wantOrganization: tenantLake, wantOK: true},
		{name: "a tenant id is matched without regard to case and the organization is the id in lower case", tenants: Tenant(tenantLake), tid: strings.ToUpper(tenantLake), wantOrganization: tenantLake, wantOK: true},
		{name: "a named tenant is admitted under its name", tenants: NamedTenant(tenantLake, "lakeside"), tid: tenantLake, wantOrganization: "lakeside", wantOK: true},
		{name: "one tenant refuses another tenant", tenants: Tenant(tenantLake), tid: tenantRiver, wantCode: sessioninfo.RefusedUnlistedTenant},
		{name: "one tenant refuses a personal account", tenants: Tenant(tenantLake), tid: ConsumerTenantID, wantCode: sessioninfo.RefusedPersonalAccount},
		{name: "listed tenants admit each under its own key", tenants: Tenants(Tenant(tenantLake), NamedTenant(tenantRiver, "riverside")), tid: tenantRiver, wantOrganization: "riverside", wantOK: true},
		{name: "listed tenants refuse one outside the list", tenants: Tenants(Tenant(tenantLake), Tenant(tenantRiver)), tid: tenantOther, wantCode: sessioninfo.RefusedUnlistedTenant},
		{name: "any tenant admits a work or school tenant under its id", tenants: AnyTenant(), tid: tenantOther, wantOrganization: tenantOther, wantOK: true},
		{name: "any tenant refuses a personal account", tenants: AnyTenant(), tid: ConsumerTenantID, wantCode: sessioninfo.RefusedPersonalAccount},
		{name: "personal accounts admit the consumer tenant", tenants: PersonalAccounts(), tid: ConsumerTenantID, wantOrganization: ConsumerTenantID, wantOK: true},
		{name: "personal accounts refuse a work or school tenant", tenants: PersonalAccounts(), tid: tenantLake, wantCode: sessioninfo.RefusedUnlistedTenant},
		{name: "a named tenant beside any tenant keeps its name", tenants: Tenants(NamedTenant(tenantLake, "lakeside"), AnyTenant()), tid: tenantLake, wantOrganization: "lakeside", wantOK: true},
		{name: "any tenant beside personal accounts admits both", tenants: Tenants(AnyTenant(), PersonalAccounts()), tid: ConsumerTenantID, wantOrganization: ConsumerTenantID, wantOK: true},
		{name: "no tenant is admitted without a tid", tenants: AnyTenant(), tid: "", wantCode: sessioninfo.RefusedUnlistedTenant},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			organization, ok := tt.tenants.admits(tt.tid)
			if ok != tt.wantOK || organization != tt.wantOrganization {
				t.Fatalf("admits(%q) = %q, %v; want %q, %v", tt.tid, organization, ok, tt.wantOrganization, tt.wantOK)
			}
			if ok {
				return
			}
			err := tt.tenants.refusal(tt.tid)
			if got := sessioninfo.LoginRefusalCodeOf(err); got != tt.wantCode {
				t.Errorf("refusal(%q) code = %q, want %q: %v", tt.tid, got, tt.wantCode, err)
			}
		})
	}
}

func TestTenantSet_endpoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		tenants       TenantSet
		wantDiscovery string
		wantShared    bool
	}{
		{name: "one tenant is served at the tenant's own endpoint", tenants: Tenant(tenantLake), wantDiscovery: testAuthority + "/" + tenantLake + "/v2.0"},
		{name: "a tenant id is lowercased in its endpoint", tenants: Tenant(strings.ToUpper(tenantLake)), wantDiscovery: testAuthority + "/" + tenantLake + "/v2.0"},
		{name: "several tenants are served at organizations", tenants: Tenants(Tenant(tenantLake), Tenant(tenantRiver)), wantDiscovery: testAuthority + "/organizations/v2.0", wantShared: true},
		{name: "any tenant is served at organizations", tenants: AnyTenant(), wantDiscovery: testAuthority + "/organizations/v2.0", wantShared: true},
		{name: "personal accounts alone are served at consumers", tenants: PersonalAccounts(), wantDiscovery: testAuthority + "/consumers/v2.0", wantShared: true},
		{name: "personal accounts beside any tenant are served at common", tenants: Tenants(AnyTenant(), PersonalAccounts()), wantDiscovery: testAuthority + "/common/v2.0", wantShared: true},
		{name: "personal accounts beside a listed tenant are served at common", tenants: Tenants(Tenant(tenantLake), PersonalAccounts()), wantDiscovery: testAuthority + "/common/v2.0", wantShared: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.tenants.discoveryURL(testAuthority); got != tt.wantDiscovery {
				t.Errorf("discoveryURL() = %q, want %q", got, tt.wantDiscovery)
			}
			if got := tt.tenants.shared(); got != tt.wantShared {
				t.Errorf("shared() = %v, want %v", got, tt.wantShared)
			}
		})
	}
}

func TestTenantSet_issuers(t *testing.T) {
	t.Parallel()

	issuer := func(tid string) string { return testAuthority + "/" + tid + "/v2.0" }
	tests := []struct {
		name         string
		tenants      TenantSet
		iss          string
		wantContains bool
		wantOnly     string
	}{
		{name: "one tenant declares its issuer alone", tenants: Tenant(tenantLake), iss: issuer(tenantLake), wantContains: true, wantOnly: issuer(tenantLake)},
		{name: "one tenant does not contain another tenant's issuer", tenants: Tenant(tenantLake), iss: issuer(tenantRiver), wantOnly: issuer(tenantLake)},
		{name: "listed tenants contain each listed tenant's issuer and name no only issuer", tenants: Tenants(Tenant(tenantLake), Tenant(tenantRiver)), iss: issuer(tenantRiver), wantContains: true},
		{name: "listed tenants do not contain an unlisted tenant's issuer", tenants: Tenants(Tenant(tenantLake), Tenant(tenantRiver)), iss: issuer(tenantOther)},
		{name: "any tenant contains every work or school tenant's issuer under the authority", tenants: AnyTenant(), iss: issuer(tenantOther), wantContains: true},
		{name: "any tenant does not contain the consumer tenant's issuer", tenants: AnyTenant(), iss: issuer(ConsumerTenantID)},
		{name: "personal accounts contain the consumer tenant's issuer", tenants: PersonalAccounts(), iss: issuer(ConsumerTenantID), wantContains: true},
		{name: "an issuer under another authority is never contained", tenants: AnyTenant(), iss: "https://evil/" + tenantLake + "/v2.0"},
		{name: "a shared endpoint's placeholder issuer is never contained", tenants: AnyTenant(), iss: testAuthority + "/{tenantid}/v2.0"},
		{name: "an issuer of another shape is never contained", tenants: AnyTenant(), iss: testAuthority + "/organizations/v2.0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			issuers := tt.tenants.issuers(testAuthority)
			if got := issuers.Contains(tt.iss); got != tt.wantContains {
				t.Errorf("issuers().Contains(%q) = %v, want %v", tt.iss, got, tt.wantContains)
			}
			if only, ok := issuers.Only(); ok != (tt.wantOnly != "") || only != tt.wantOnly {
				t.Errorf("issuers().Only() = %q, %v; want %q", only, ok, tt.wantOnly)
			}
		})
	}
}

func TestTenantSet_simulatedTenantID(t *testing.T) {
	t.Parallel()

	const fallback = "034cdd64-5dd9-4a3b-90fb-c76d3a499433"
	tests := []struct {
		name    string
		tenants TenantSet
		want    string
	}{
		{name: "one tenant simulates it", tenants: Tenant(tenantLake), want: tenantLake},
		{name: "a listed tenant is simulated in lower case", tenants: Tenant(strings.ToUpper(tenantLake)), want: tenantLake},
		{name: "several tenants simulate the first", tenants: Tenants(Tenant(tenantRiver), Tenant(tenantLake)), want: tenantRiver},
		{name: "a listed tenant beside any tenant is simulated", tenants: Tenants(AnyTenant(), Tenant(tenantLake)), want: tenantLake},
		{name: "any tenant simulates the fixed tenant", tenants: AnyTenant(), want: fallback},
		{name: "personal accounts alone simulate the consumer tenant", tenants: PersonalAccounts(), want: ConsumerTenantID},
		{name: "any tenant beside personal accounts simulates the fixed tenant", tenants: Tenants(AnyTenant(), PersonalAccounts()), want: fallback},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.tenants.simulatedTenantID(fallback); got != tt.want {
				t.Errorf("simulatedTenantID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthorityOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		authority string
		want      string
		wantErr   bool
	}{
		{name: "empty is the default", want: DefaultAuthority},
		{name: "a base URL", authority: "https://login.microsoftonline.us", want: "https://login.microsoftonline.us"},
		{name: "a trailing slash is dropped", authority: "https://login.microsoftonline.us/", want: "https://login.microsoftonline.us"},
		{name: "a test server over http", authority: "http://127.0.0.1:8443", want: "http://127.0.0.1:8443"},
		{name: "a path is refused", authority: "https://login.microsoftonline.com/common", wantErr: true},
		{name: "a query is refused", authority: "https://login.microsoftonline.com?x=1", wantErr: true},
		{name: "no scheme is refused", authority: "login.microsoftonline.com", wantErr: true},
		{name: "another scheme is refused", authority: "ftp://login.microsoftonline.com", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := authorityOf(tt.authority)
			if (err != nil) != tt.wantErr {
				t.Fatalf("authorityOf() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "Authority: ") {
				t.Errorf("authorityOf() error = %q, want it to name the field", err.Error())
			}
			if got != tt.want {
				t.Errorf("authorityOf() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRefuseShape(t *testing.T) {
	t.Parallel()

	sync := signin.RoleSync(mock_signin.NewMockUserRoleManager(gomock.NewController(t)))
	off := signin.DisableRoleSync()
	tests := []struct {
		name     string
		tenants  TenantSet
		roleSync signin.RoleSyncConfig
		wantErr  string
	}{
		{name: "one tenant with role sync", tenants: Tenant(tenantLake), roleSync: sync},
		{name: "one tenant without role sync", tenants: Tenant(tenantLake), roleSync: off},
		{name: "several tenants without role sync", tenants: Tenants(Tenant(tenantLake), Tenant(tenantRiver)), roleSync: off},
		{name: "any tenant without role sync", tenants: AnyTenant(), roleSync: off},
		{name: "personal accounts without role sync", tenants: PersonalAccounts(), roleSync: off},
		{name: "a listed tenant beside any tenant", tenants: Tenants(Tenant(tenantLake), AnyTenant()), roleSync: off},
		{name: "the slot's own rules come first", roleSync: sync, wantErr: "Tenants is required"},
		{name: "a missing role sync slot", tenants: Tenant(tenantLake), wantErr: "roleSync is required"},
		{name: "role sync across several tenants", tenants: Tenants(Tenant(tenantLake), Tenant(tenantRiver)), roleSync: sync, wantErr: "RoleSync: role synchronization follows one tenant's application roles"},
		{name: "role sync across any tenant", tenants: AnyTenant(), roleSync: sync, wantErr: "RoleSync: role synchronization follows one tenant's application roles"},
		{name: "role sync with personal accounts", tenants: PersonalAccounts(), roleSync: sync, wantErr: "RoleSync: role synchronization follows one tenant's application roles"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := refuseShape(tt.tenants, tt.roleSync)
			if (err != nil) != (tt.wantErr != "") {
				t.Fatalf("refuseShape() error = %v, wantErr %q", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("refuseShape() error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestTenantSet_audience(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tenants TenantSet
		// want is whom opening the method to sign-up admits, in the page's words.
		want        string
		wantLimited bool
	}{
		{name: "one tenant the application names", tenants: NamedTenant(tenantLake, "Lakeside"), want: "anyone with an account at Lakeside", wantLimited: true},
		{name: "one tenant by its id", tenants: Tenant(tenantLake), want: "anyone with an account at the Microsoft Entra tenant " + tenantLake, wantLimited: true},
		{name: "several tenants", tenants: Tenants(NamedTenant(tenantLake, "Lakeside"), NamedTenant(tenantRiver, "Riverside")), want: "anyone with an account at Lakeside or Riverside", wantLimited: true},
		{name: "any tenant", tenants: AnyTenant(), want: "anyone with a Microsoft work or school account"},
		{name: "personal accounts", tenants: PersonalAccounts(), want: "anyone with a personal Microsoft account"},
		{name: "any tenant and personal accounts", tenants: Tenants(AnyTenant(), PersonalAccounts()), want: "anyone with a Microsoft account"},
		{name: "a listed tenant beside any tenant is as wide as any tenant", tenants: Tenants(NamedTenant(tenantLake, "Lakeside"), AnyTenant()), want: "anyone with a Microsoft work or school account"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.tenants.audience()
			if got.Words() != tt.want || got.Limited() != tt.wantLimited {
				t.Errorf("audience() = %q (limited %v), want %q (limited %v)", got.Words(), got.Limited(), tt.want, tt.wantLimited)
			}
		})
	}
}

// TestTenantSet_audienceKey pins what a sign-in method's row is compared by: the tenants
// a registration admits, each by its tenant id, and the kind of account it admits
// outside them, never the names they are said by. An administrator opens a method to
// sign-up for the people it admits then, and the row counts while the registration
// admits those people and no others. A name is the application's to give, and a second
// tenant can carry the first one's, so a key written from names reads two
// registrations that admit different people as one: the method stays open for a tenant
// nobody opened it for. Every registration here is one the slot's own check accepts.
func TestTenantSet_audienceKey(t *testing.T) {
	t.Parallel()

	const tenantHex = "abcdef01-2345-6789-abcd-ef0123456789"
	labelOfLake := Tenant(tenantLake).audience().Organizations[0]

	tests := []struct {
		name string
		// opened is the registration an administrator opened the method under, and now
		// the registration of a later release.
		opened, now TenantSet
		// same says the two admit the same people, so the row stands.
		same bool
	}{
		{name: "a second tenant listed under the first one's label", opened: Tenant(tenantLake), now: Tenants(Tenant(tenantLake), NamedTenant(tenantRiver, labelOfLake))},
		{name: "another tenant in the first one's place, under its name", opened: NamedTenant(tenantLake, "Lakeside"), now: NamedTenant(tenantRiver, "Lakeside")},
		{name: "a second tenant", opened: Tenant(tenantLake), now: Tenants(Tenant(tenantLake), Tenant(tenantRiver))},
		{name: "a tenant fewer", opened: Tenants(Tenant(tenantLake), Tenant(tenantRiver)), now: Tenant(tenantLake)},
		{name: "any tenant beside the listed one", opened: Tenant(tenantLake), now: Tenants(Tenant(tenantLake), AnyTenant())},
		{name: "personal accounts beside the listed one", opened: Tenant(tenantLake), now: Tenants(Tenant(tenantLake), PersonalAccounts())},
		{name: "any tenant in place of personal accounts", opened: PersonalAccounts(), now: AnyTenant()},
		{name: "the same tenant given a name", opened: Tenant(tenantLake), now: NamedTenant(tenantLake, "Lakeside"), same: true},
		{name: "the same tenant given another name", opened: NamedTenant(tenantLake, "Lakeside"), now: NamedTenant(tenantLake, "Lakeside University"), same: true},
		{name: "the same tenant with its id in capitals", opened: Tenant(tenantHex), now: Tenant(strings.ToUpper(tenantHex)), same: true},
		{name: "the same tenants in another order", opened: Tenants(Tenant(tenantLake), Tenant(tenantRiver)), now: Tenants(Tenant(tenantRiver), Tenant(tenantLake)), same: true},
		{name: "the same tenants, one declared to enforce a second factor", opened: Tenant(tenantLake), now: Tenant(tenantLake).Attesting(), same: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, set := range []TenantSet{tt.opened, tt.now} {
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

func TestTenantSet_admitsAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tenants TenantSet
		domain  string
		want    bool
	}{
		{name: "one tenant admits a work address, whose tenant it cannot tell", tenants: Tenant(tenantLake), domain: "lakeside.edu", want: true},
		{name: "one tenant refuses a personal account at outlook.com", tenants: Tenant(tenantLake), domain: "outlook.com"},
		{name: "one tenant refuses hotmail.com", tenants: Tenant(tenantLake), domain: "hotmail.com"},
		{name: "one tenant refuses live.com", tenants: Tenant(tenantLake), domain: "live.com"},
		{name: "one tenant refuses msn.com", tenants: Tenant(tenantLake), domain: "msn.com"},
		{name: "the personal domains are matched without regard to case", tenants: Tenants(Tenant(tenantLake), AnyTenant()), domain: "Outlook.COM"},
		{name: "a domain written with the Kelvin sign, which Unicode folds to k, is not the personal domain", tenants: Tenants(Tenant(tenantLake), AnyTenant()), domain: "outloo\u212A.com", want: true},
		{name: "any tenant refuses a personal account", tenants: AnyTenant(), domain: "outlook.com"},
		{name: "personal accounts admit a personal account", tenants: PersonalAccounts(), domain: "outlook.com", want: true},
		{name: "personal accounts admit a work address too, whose tenant they cannot tell", tenants: PersonalAccounts(), domain: "lakeside.edu", want: true},
		{name: "listed tenants beside personal accounts admit a personal account", tenants: Tenants(NamedTenant(tenantLake, "lakeside"), PersonalAccounts()), domain: "hotmail.com", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.tenants.admitsAddress(tt.domain); got != tt.want {
				t.Errorf("admitsAddress(%q) = %v, want %v", tt.domain, got, tt.want)
			}
		})
	}
}

// TestTenantSet_nameOf pins what a sentence calls a tenant: the application's name for
// a listed tenant, and nothing for a tenant it knows by an id alone, which is said by
// the registration's label and never by the id.
func TestTenantSet_nameOf(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tenants TenantSet
		tid     string
		want    string
	}{
		{name: "a tenant the application names", tenants: NamedTenant(tenantLake, "Lakeside"), tid: tenantLake, want: "Lakeside"},
		{name: "its id in another case", tenants: NamedTenant(tenantLake, "Lakeside"), tid: strings.ToUpper(tenantLake), want: "Lakeside"},
		{name: "a tenant listed by its id alone has no name", tenants: Tenant(tenantLake), tid: tenantLake},
		{name: "one of several", tenants: Tenants(NamedTenant(tenantLake, "Lakeside"), Tenant(tenantRiver)), tid: tenantLake, want: "Lakeside"},
		{name: "the unnamed one of several", tenants: Tenants(NamedTenant(tenantLake, "Lakeside"), Tenant(tenantRiver)), tid: tenantRiver},
		{name: "a tenant admitted as any tenant has no name", tenants: Tenants(NamedTenant(tenantLake, "Lakeside"), AnyTenant()), tid: tenantRiver},
		{name: "a tenant the set does not admit", tenants: NamedTenant(tenantLake, "Lakeside"), tid: tenantRiver},
		{name: "no tenant", tenants: NamedTenant(tenantLake, "Lakeside"), tid: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.tenants.nameOf(tt.tid); got != tt.want {
				t.Errorf("nameOf(%q) = %q, want %q", tt.tid, got, tt.want)
			}
		})
	}
}
