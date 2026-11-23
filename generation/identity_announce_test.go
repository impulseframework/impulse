package generation

import (
	"go/format"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/access"
	"github.com/impulseframework/impulse/session"
)

// TestIdentityAnnounce_client pins what the client of an auth's generated management
// renders for a sign-in's writes to reach the open pages: it hands the identity manager
// the auth's tables by the generated types over them (identity.WithAccountTables), each
// where the schema holds it; it declares AnnounceThrough, which hands the manager the
// application's live service to announce through; and it answers the hook for a sign-in
// that stands (SignedIn) for every auth, one whose directory writes the roles included,
// while the hooks that give a role stay with an auth whose roles the application gives.
func TestIdentityAnnounce_client(t *testing.T) {
	t.Parallel()

	every := []identityTable{identityUsers, identitySessions, identityIdentities, identityMailLinks, identityMemberships}
	announce := "func (c *Client) AnnounceThrough(svc live.Service, derivations *live.Derivations) {"
	signedIn := "func (c *Client) SignedIn(db resource.Client) session.SignedInHook {"
	tables := func(fields ...string) string {
		return "opts = append(opts, identity.WithAccountTables(&identity.AccountTables{ " + strings.Join(fields, " ") + " }))"
	}
	users := "Users: crewresources.CrewSessionUser{}.Resource(),"
	sessions := "Sessions: crewresources.CrewSession{}.Resource(),"
	identities := "Identities: crewresources.CrewSessionIdentity{}.Resource(),"
	memberships := "Memberships: crewresources.CrewUserRole{}.Resource(),"
	mailLinks := "MailLinks: crewresources.CrewSessionMailLink{}.Resource(),"
	tests := []struct {
		name            string
		data            *identitySourceData
		wantContains    []string
		wantNotContains []string
	}{
		{
			name: "an auth whose roles the application gives, over every table a sign-in writes",
			data: announceData(false, every...),
			wantContains: []string{
				tables(users, sessions, identities, memberships, mailLinks),
				announce,
				"c.manager.AnnounceWith(func(ctx context.Context, touched *resource.TouchedRows) { live.Announce(ctx, svc, derivations, touched) })",
				`"github.com/impulseframework/impulse/resource/live"`,
				signedIn,
				"func (c *Client) InvitationAccepted() session.InvitationAcceptedHook[*spanner.ReadWriteTransaction] {",
				"func (c *Client) OwnTenants(db resource.Client) session.OwnTenantsHook {",
			},
		},
		{
			name:         "an auth whose directory writes the roles",
			data:         announceData(true, every...),
			wantContains: []string{tables(users, sessions, identities, memberships, mailLinks), announce, signedIn},
			wantNotContains: []string{
				"func (c *Client) InvitationAccepted()",
				"func (c *Client) DirectorySeated()",
				"func (c *Client) DirectoryRole(",
				"func (c *Client) OwnTenants(",
			},
		},
		{
			name:            "an auth whose schema holds no identities and no mail links",
			data:            announceData(false, identityUsers, identitySessions, identityMemberships),
			wantContains:    []string{tables(users, sessions, memberships), announce, signedIn},
			wantNotContains: []string{"Identities: crewresources", "MailLinks: crewresources"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &client{}
			out, err := c.generateTemplateOutput("identityClientTemplate", identityClientTemplate, tt.data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(out); err != nil {
				t.Fatalf("the rendered client is not Go: %v", err)
			}
			assertContains(t, "the client", squashSpace(string(out)), squashEach(tt.wantContains), squashEach(tt.wantNotContains))
		})
	}
}

// TestIdentityAnnounce_handlers pins that the handlers' New hands the auth's client the
// application's live service and the derivations it was given, for the client to
// announce a sign-in's writes with: the application constructs the handlers, so it
// writes nothing for a sign-in to reach the open pages.
func TestIdentityAnnounce_handlers(t *testing.T) {
	t.Parallel()

	data := announceData(false, identityUsers, identitySessions, identityMemberships)
	data.Package = "crewhandlers"
	c := &client{}
	out, err := c.generateTemplateOutput("identityHandlersTemplate", identityHandlersTemplate, data)
	if err != nil {
		t.Fatalf("generateTemplateOutput() error = %v", err)
	}
	if _, err := format.Source(out); err != nil {
		t.Fatalf("the rendered handlers are not Go: %v", err)
	}
	want := []string{
		"func New(deps Deps, derivations *live.Derivations) *Handlers { deps.CrewIdentity().AnnounceThrough(deps.LiveService(), derivations) return &Handlers{Deps: deps, liveDerivations: derivations} }",
		"// It also hands the auth's client the same service and derivations to announce a // sign-in's writes with (AnnounceThrough).",
	}
	assertContains(t, "the handlers", squashSpace(string(out)), squashEach(want), nil)
}

// TestIdentityAnnounce_signInWays pins that the list of how a person signs in declares
// the tables whose changes change it (@changedBy), since a computed resource has no
// query to read them off: the accounts and the identities for the whole application's
// list, and for a tenant's list those, the memberships and, where the auth keeps groups,
// the groups, which put a person in the tenant's list and give its rows their two facts.
// The pages that hold the list then read it again when a sign-in links an identity or a
// password is removed.
func TestIdentityAnnounce_signInWays(t *testing.T) {
	t.Parallel()

	order := "// @order(Password desc, Through asc)"
	tests := []struct {
		name         string
		tenant       bool
		groups       bool
		wantContains []string
	}{
		{
			name: "the whole application's list",
			wantContains: []string{
				"// @changedBy(CrewSessionUsers, CrewSessionIdentities)\n" + order,
				"// identities of the auth's people. A change to an account or to an\n// identity tells the list's open pages,\n" +
					"// which read it again: the tables are declared (@changedBy), since no query names them.",
			},
		},
		{
			name:   "a tenant's list of an auth that keeps groups",
			tenant: true,
			groups: true,
			wantContains: []string{
				"// @changedBy(CrewSessionUsers, CrewSessionIdentities, CrewUserRoles, CrewGroups, CrewGroupMembers, CrewGroupRoles)\n" + order,
				"// identities of the auth's people. A change to an account or to an\n" +
					"// identity, or to who holds a role where, tells the list's open pages,\n" +
					"// which read it again: the tables are declared (@changedBy), since no query names them.",
			},
		},
		{
			name:         "a tenant's list of an auth that keeps no groups",
			tenant:       true,
			wantContains: []string{"// @changedBy(CrewSessionUsers, CrewSessionIdentities, CrewUserRoles)\n" + order},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tables := []identityTable{identityUsers, identitySessions, identityIdentities, identityMemberships}
			if tt.groups {
				tables = append(tables, identityGroups)
			}
			data := announceData(false, tables...)
			data.password = true
			data.tenant = &identityTenant{name: "Sector", table: "Sectors", key: "Id"}
			data.ForTenant = tt.tenant
			data.Policy = access.Tables{UserRoles: "CrewUserRoles", Groups: "CrewGroups", GroupMembers: "CrewGroupMembers", GroupRoles: "CrewGroupRoles"}
			c := &client{}
			out, err := c.generateTemplateOutput("identitySignInWaysTemplate", identitySignInWaysTemplate, data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(out); err != nil {
				t.Fatalf("the rendered list is not Go: %v", err)
			}
			assertContains(t, "the list of how a person signs in", squashSpace(string(out)), squashEach(tt.wantContains), nil)
		})
	}
}

// announceData is the source data of the crew auth's client over the tables given, its
// roles written by a directory when synced.
func announceData(synced bool, tables ...identityTable) *identitySourceData {
	inst := &identityInstance{auth: "Crew", rolesSynced: synced, tables: map[identityTable]bool{}}
	for _, kind := range tables {
		inst.tables[kind] = true
	}

	return &identitySourceData{
		Source: "Auth", Package: "crewidentity", identityInstance: inst, ResourcesPackage: "crewresources", MethodsPackage: "crewidentity", Outlets: "default",
		Tables: session.Tables{Users: "CrewSessionUsers", Sessions: "CrewSessions", Identities: "CrewSessionIdentities", MailLinks: "CrewSessionMailLinks"},
		Policy: access.Tables{UserRoles: "CrewUserRoles"},
	}
}

// squashSpace runs every stretch of white space in s into one space, so a rendered
// source is read without the alignment gofmt would give it.
func squashSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// squashEach is squashSpace over each of the texts.
func squashEach(texts []string) []string {
	squashed := make([]string, 0, len(texts))
	for _, text := range texts {
		squashed = append(squashed, squashSpace(text))
	}

	return squashed
}
