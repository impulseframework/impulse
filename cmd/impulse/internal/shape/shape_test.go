package shape

import (
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
)

// TestAll pins the eight shapes by number and name, as the framework names them, and
// the name --shape takes for each: the first four are a sign-in method's, the last four
// the tenants'.
func TestAll(t *testing.T) {
	t.Parallel()

	want := []string{
		"1 Invite only = invite-only",
		"2 Directory members = directory-members",
		"3 Directory roles = directory-roles",
		"4 Open sign-up = open-sign-up",
		"5 Tenant-owned users, invite only = tenant-owned-users-invite-only",
		"6 Tenant-owned users, by directory = tenant-owned-users-by-directory",
		"7 Global users, invite only = global-users-invite-only",
		"8 Global users, each tenant chooses = global-users-each-tenant-chooses",
	}
	var got []string
	for _, s := range All() {
		got = append(got, s.String()+" = "+s.Key)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("All() mismatch (-want +got):\n%s", diff)
	}
	if got, want := Keys(OfMethods()), "invite-only, directory-members, directory-roles, open-sign-up"; got != want {
		t.Errorf("Keys(OfMethods()) = %q, want %q", got, want)
	}
	if got, want := Keys(OfTenants()), "tenant-owned-users-invite-only, tenant-owned-users-by-directory, global-users-invite-only, global-users-each-tenant-chooses"; got != want {
		t.Errorf("Keys(OfTenants()) = %q, want %q", got, want)
	}
	for _, s := range All() {
		if tenants := s.Number >= 5; s.HasTenants() != tenants {
			t.Errorf("%s: HasTenants() = %v, want %v", s, s.HasTenants(), tenants)
		}
		if one := s.Number == 5 || s.Number == 6; s.OneTenantPerPerson() != one {
			t.Errorf("%s: OneTenantPerPerson() = %v, want %v", s, s.OneTenantPerPerson(), one)
		}
		if directories := s.Number == 6 || s.Number == 8; s.TenantDirectories() != directories {
			t.Errorf("%s: TenantDirectories() = %v, want %v", s, s.TenantDirectories(), directories)
		}
	}
}

// TestFind finds a shape by the name --shape takes among the ones a command offers, and
// refuses a name the command does not know with the names it knows; a shape another
// command offers is refused the same way, with where it is a starting point.
func TestFind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		offered []Shape
		command string
		want    string
		wantErr string
	}{
		{name: "a method's shape", key: "directory-members", offered: OfMethods(), command: "impulse add method", want: "2 Directory members"},
		{name: "a tenants' shape", key: "global-users-each-tenant-chooses", offered: OfTenants(), command: "impulse new", want: "8 Global users, each tenant chooses"},
		{name: "a method's shape by its number", key: "4", offered: OfMethods(), command: "impulse add auth", want: "4 Open sign-up"},
		{name: "a tenants' shape by its number", key: "5", offered: OfTenants(), command: "impulse add tenancy", want: "5 Tenant-owned users, invite only"},
		{
			name: "a number no shape has", key: "9", offered: OfTenants(), command: "impulse new",
			wantErr: "--shape 9: impulse new does not know that shape. It knows tenant-owned-users-invite-only, tenant-owned-users-by-directory, global-users-invite-only, global-users-each-tenant-chooses",
		},
		{
			name: "a tenants' shape's number given to a method's command", key: "8", offered: OfMethods(), command: "impulse add method",
			wantErr: "--shape 8: impulse add method does not know that shape. It knows invite-only, directory-members, directory-roles, open-sign-up. 8 Global users, each tenant chooses is a starting point of impulse new and impulse add tenancy, since the tenants' choices tell it apart",
		},
		{
			name: "a name nobody knows", key: "members-only", offered: OfMethods(), command: "impulse add auth",
			wantErr: "--shape members-only: impulse add auth does not know that shape. It knows invite-only, directory-members, directory-roles, open-sign-up",
		},
		{
			name: "a tenants' shape given to a method's command", key: "global-users-invite-only", offered: OfMethods(), command: "impulse add method",
			wantErr: "--shape global-users-invite-only: impulse add method does not know that shape. It knows invite-only, directory-members, directory-roles, open-sign-up. 7 Global users, invite only is a starting point of impulse new and impulse add tenancy, since the tenants' choices tell it apart",
		},
		{
			name: "a method's shape given to a tenants' command", key: "open-sign-up", offered: OfTenants(), command: "impulse add tenancy",
			wantErr: "--shape open-sign-up: impulse add tenancy does not know that shape. It knows tenant-owned-users-invite-only, tenant-owned-users-by-directory, global-users-invite-only, global-users-each-tenant-chooses. 4 Open sign-up is a starting point of impulse add auth and impulse add method, since a sign-in method's choices tell it apart",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Find(tt.key, tt.offered, tt.command)
			if tt.wantErr != "" {
				if err == nil || errors.Cause(err).Error() != tt.wantErr {
					t.Fatalf("Find() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("Find() error = %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("Find() = %s, want %s", got, tt.want)
			}
		})
	}
}

// shapeOf is the shape of the name, for a test.
func shapeOf(t *testing.T, key string) Shape {
	t.Helper()

	s, err := Find(key, All(), "the test")
	if err != nil {
		t.Fatal(err)
	}

	return s
}

// TestShape_Method fills in a sign-in method's choices from each of shapes 1 to 4: who
// gives the roles and how wide the method is open, keeping a choice the command line
// made where the shape allows it. A method the shape cannot be, and a flag that says
// the opposite of the shape, is refused with both named, what each means, and what to
// do.
func TestShape_Method(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		shape         string
		method        authpkg.Method
		wantSlot      string
		wantAuthority string
		wantErr       string
	}{
		// 1 Invite only: the application gives the roles, at any width.
		{name: "invite only, the password method", shape: "invite-only", method: authpkg.Method{Kind: app.MethodPassword}},
		{name: "invite only, a Google method", shape: "invite-only", method: authpkg.Method{Kind: app.MethodGoogle, Name: "google"}, wantAuthority: app.AuthorityApplication},
		{name: "invite only, a Microsoft method of any tenant", shape: "invite-only", method: authpkg.Method{Kind: app.MethodAzure, Name: "azure", Slot: authpkg.SlotAny}, wantSlot: authpkg.SlotAny, wantAuthority: app.AuthorityApplication},
		{name: "invite only, an identity provider", shape: "invite-only", method: authpkg.Method{Kind: app.MethodOIDC, Name: "okta"}, wantAuthority: app.AuthorityApplication},
		{name: "invite only, the persona method", shape: "invite-only", method: authpkg.Method{Kind: app.MethodPersona, Name: "persona"}, wantAuthority: app.AuthorityApplication},
		{
			name: "invite only beside the directory's authority", shape: "invite-only", method: authpkg.Method{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityDirectory},
			wantErr: "--shape invite-only and --authority directory contradict each other: in Invite only the application gives the roles, to the people an administrator invites, and --authority directory has the directory give the roles to everyone it signs in. Take --shape directory-roles, or leave --authority out",
		},

		// 2 Directory members: a method limited to the organizations it names.
		{name: "directory members, a Google method", shape: "directory-members", method: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace"}, wantAuthority: app.AuthorityApplication},
		{name: "directory members, listed tenants", shape: "directory-members", method: authpkg.Method{Kind: app.MethodAzure, Name: "entra", Slot: authpkg.SlotListed, Authority: app.AuthorityApplication}, wantSlot: authpkg.SlotListed, wantAuthority: app.AuthorityApplication},
		{name: "directory members, an identity provider", shape: "directory-members", method: authpkg.Method{Kind: app.MethodOIDC, Name: "okta"}, wantAuthority: app.AuthorityApplication},
		{
			name: "directory members beside a slot anyone can use", shape: "directory-members", method: authpkg.Method{Kind: app.MethodAzure, Name: "entra", Slot: authpkg.SlotAny},
			wantErr: "--shape directory-members and --slot any contradict each other: Directory members is a method limited to the organizations it names, opened to sign-up so that everyone in the organization's directory gets an account, with the application giving the roles, and --slot any admits the accounts of any Microsoft Entra tenant. Take --shape open-sign-up, or --slot one or listed",
		},
		{
			name: "directory members beside the directory's authority", shape: "directory-members", method: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: app.AuthorityDirectory},
			wantErr: "--shape directory-members and --authority directory contradict each other:",
		},
		{
			name: "directory members and the password method", shape: "directory-members", method: authpkg.Method{Kind: app.MethodPassword},
			wantErr: "--shape directory-members and the password method contradict each other: Directory members is a method limited to the organizations it names, opened to sign-up so that everyone in the organization's directory gets an account, with the application giving the roles, and the password method admits anyone with an email address. Take a directory method (azure, google or oidc), or --shape invite-only or --shape open-sign-up",
		},
		{
			name: "directory members and the persona method", shape: "directory-members", method: authpkg.Method{Kind: app.MethodPersona, Name: "persona"},
			wantErr: "--shape directory-members and the persona method contradict each other:",
		},

		// 3 Directory roles: one organization's directory gives the roles.
		{name: "directory roles, a Google method", shape: "directory-roles", method: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace"}, wantAuthority: app.AuthorityDirectory},
		{name: "directory roles, an identity provider", shape: "directory-roles", method: authpkg.Method{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityDirectory}, wantAuthority: app.AuthorityDirectory},
		{name: "directory roles, one tenant said twice", shape: "directory-roles", method: authpkg.Method{Kind: app.MethodAzure, Name: "entra", Slot: authpkg.SlotOne}, wantSlot: authpkg.SlotOne, wantAuthority: app.AuthorityDirectory},
		{
			name: "directory roles beside the application's authority", shape: "directory-roles", method: authpkg.Method{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityApplication},
			wantErr: "--shape directory-roles and --authority application contradict each other: in Directory roles the directory of one organization gives the roles, and --authority application has the application give them. Take --shape invite-only, --shape directory-members or --shape open-sign-up, or leave --authority out",
		},
		{
			name: "directory roles beside several organizations", shape: "directory-roles", method: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Slot: authpkg.SlotListed},
			wantErr: "--shape directory-roles and --slot listed contradict each other: in Directory roles the directory of one organization gives the roles, and --slot listed admits the accounts of the Google Workspaces it lists. Leave --slot out: role synchronization follows one organization's roles",
		},
		{
			name: "directory roles and the password method", shape: "directory-roles", method: authpkg.Method{Kind: app.MethodPassword},
			wantErr: "--shape directory-roles and the password method contradict each other: in Directory roles the directory of one organization gives the roles, and the password method has no directory. Take a directory method (azure, google or oidc)",
		},

		// 4 Open sign-up: a method anyone can use, the widest slot when none is given.
		{name: "open sign-up, the password method", shape: "open-sign-up", method: authpkg.Method{Kind: app.MethodPassword}},
		{name: "open sign-up, a Google method", shape: "open-sign-up", method: authpkg.Method{Kind: app.MethodGoogle, Name: "google"}, wantSlot: authpkg.SlotAny, wantAuthority: app.AuthorityApplication},
		{name: "open sign-up, a Microsoft method", shape: "open-sign-up", method: authpkg.Method{Kind: app.MethodAzure, Name: "azure"}, wantSlot: authpkg.SlotAnyPersonal, wantAuthority: app.AuthorityApplication},
		{name: "open sign-up, personal accounts alone", shape: "open-sign-up", method: authpkg.Method{Kind: app.MethodAzure, Name: "azure", Slot: authpkg.SlotPersonal}, wantSlot: authpkg.SlotPersonal, wantAuthority: app.AuthorityApplication},
		{name: "open sign-up, any Workspace", shape: "open-sign-up", method: authpkg.Method{Kind: app.MethodGoogle, Name: "google", Slot: authpkg.SlotAnyWorkspace}, wantSlot: authpkg.SlotAnyWorkspace, wantAuthority: app.AuthorityApplication},
		{
			name: "open sign-up beside one organization", shape: "open-sign-up", method: authpkg.Method{Kind: app.MethodGoogle, Name: "google", Slot: authpkg.SlotOne},
			wantErr: "--shape open-sign-up and --slot one contradict each other: Open sign-up is a method anyone can use, opened to sign-up, with the application giving the roles, and --slot one admits the accounts of one Google Workspace and nobody else. Take --shape directory-members, or leave --slot out, which takes --slot any",
		},
		{
			name: "open sign-up and an identity provider", shape: "open-sign-up", method: authpkg.Method{Kind: app.MethodOIDC, Name: "okta"},
			wantErr: "--shape open-sign-up and an identity provider's method (oidc) contradict each other: Open sign-up is a method anyone can use, opened to sign-up, with the application giving the roles, and an identity provider's registration admits the accounts of one organization's identity provider and nobody else. Take --shape directory-members, or open the password method, a Microsoft method or a Google method",
		},
		{
			name: "open sign-up beside the directory's authority", shape: "open-sign-up", method: authpkg.Method{Kind: app.MethodAzure, Name: "azure", Authority: app.AuthorityDirectory},
			wantErr: "--shape open-sign-up and --authority directory contradict each other:",
		},

		// A kind impulse does not compose is the command's to refuse.
		{name: "a kind impulse does not compose", shape: "open-sign-up", method: authpkg.Method{Kind: "ldap", Name: "ldap"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := tt.method
			err := shapeOf(t, tt.shape).Method(&m)
			if tt.wantErr != "" {
				if err == nil || !strings.HasPrefix(errors.Cause(err).Error(), tt.wantErr) {
					t.Fatalf("Method() error = %v, want one starting %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("Method() error = %v", err)
			}
			if m.Slot != tt.wantSlot || m.Authority != tt.wantAuthority {
				t.Errorf("Method() left slot %q and authority %q, want %q and %q", m.Slot, m.Authority, tt.wantSlot, tt.wantAuthority)
			}
			// What the shape fills in is a method the library takes.
			if m.Kind != "ldap" {
				if m.Kind == app.MethodPassword {
					m.Name = app.PasswordRegistration
				}
				if err := m.Validate(); err != nil {
					t.Errorf("the method the shape filled in does not validate: %v", err)
				}
			}
		})
	}
}

// TestShape_Says pins what a starting point tells its author about the method it wrote:
// the width and who gives the roles, and that Open is chosen on the Sign-in page while
// the application runs, never written; in an application with tenants, what opening
// does there, or that the auth's methods are never opened.
func TestShape_Says(t *testing.T) {
	t.Parallel()

	workspace := authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: app.AuthorityApplication}
	anyone := authpkg.Method{Kind: app.MethodGoogle, Name: "google", Slot: authpkg.SlotAny, Authority: app.AuthorityApplication}
	tests := []struct {
		name    string
		shape   string
		method  authpkg.Method
		opening Opening
		want    string
	}{
		{
			name: "invite only", shape: "invite-only", method: authpkg.Method{Kind: app.MethodPassword},
			want: "The password method admits anyone with an email address, and the application gives the roles. It starts invite only, as every method does, and Invite only is leaving it so: a person with no account needs an invitation.",
		},
		{
			name: "directory members", shape: "directory-members", method: workspace,
			want: "The workspace method admits the accounts of one Google Workspace and nobody else, and the application gives the roles. It starts invite only: Open is chosen on the application's Sign-in page while the application runs, by whoever holds the grant on it, and from then everyone in the organization's directory gets an account at their first sign-in, with the role the administrator names.",
		},
		{
			name: "directory roles", shape: "directory-roles", method: authpkg.Method{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityDirectory},
			want: "The okta method admits the accounts of one organization's identity provider and nobody else, and its directory gives the roles: everyone the directory signs in becomes a user with the roles it names, so the method has no invite-only or open choice and stands alone in its auth.",
		},
		{
			name: "open sign-up", shape: "open-sign-up", method: anyone,
			want: "The google method admits any Google account, and the application gives the roles. It starts invite only: Open is chosen on the application's Sign-in page while the application runs, by whoever holds the grant on it, and from then anyone can sign up, and gets the role the administrator names.",
		},
		{
			name: "open sign-up on the auth whose people create a tenant", shape: "open-sign-up", method: anyone, opening: OpensToCreateTenant,
			want: "The google method admits any Google account, and the application gives the roles. It starts invite only: Open is chosen on the application's Sign-in page while the application runs, by whoever holds the grant on it, and from then a person who signs up through it holds no tenant and creates their own.",
		},
		{
			name: "directory members on another auth of an application with tenants", shape: "directory-members", method: workspace, opening: NeverOpened,
			want: "The workspace method admits the accounts of one Google Workspace and nobody else, and the application gives the roles. It stays invite only in this application, which has tenants: this auth's people create no tenant of their own, so no method of it is opened to sign-up, and a person gets in by an invitation or through the directory of a tenant that names a default role.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := shapeOf(t, tt.shape).Says(&tt.method, tt.opening); got != tt.want {
				t.Errorf("Says() mismatch (-want +got):\n%s", cmp.Diff(tt.want, got))
			}
		})
	}
}

// TestWidth reads how wide a method is open two ways, from the slot impulse is about to
// write and from what the scan read of a registration, and both say the same of the
// same method: whom it admits, and whether it is limited to the organizations it names.
func TestWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		slot        authpkg.Method
		read        app.Method
		wantWords   string
		wantLimited bool
	}{
		{name: "the password method", slot: authpkg.Method{Kind: app.MethodPassword}, read: app.Method{Kind: app.MethodPassword}, wantWords: "anyone with an email address"},
		{name: "an identity provider", slot: authpkg.Method{Kind: app.MethodOIDC}, read: app.Method{Kind: app.MethodOIDC}, wantWords: "the accounts of one organization's identity provider", wantLimited: true},
		{name: "the persona method", slot: authpkg.Method{Kind: app.MethodPersona}, read: app.Method{Kind: app.MethodPersona}, wantWords: "the personas of the test environments' persona provider", wantLimited: true},
		{name: "one tenant", slot: authpkg.Method{Kind: app.MethodAzure}, read: app.Method{Kind: app.MethodAzure, Tenants: app.TenantsOne}, wantWords: "the accounts of one Microsoft Entra tenant", wantLimited: true},
		{name: "listed tenants", slot: authpkg.Method{Kind: app.MethodAzure, Slot: authpkg.SlotListed}, read: app.Method{Kind: app.MethodAzure, Tenants: app.TenantsListed}, wantWords: "the accounts of the Microsoft Entra tenants it lists", wantLimited: true},
		{name: "any tenant", slot: authpkg.Method{Kind: app.MethodAzure, Slot: authpkg.SlotAny}, read: app.Method{Kind: app.MethodAzure, Tenants: app.TenantsAny}, wantWords: "the accounts of any Microsoft Entra tenant"},
		{name: "personal accounts", slot: authpkg.Method{Kind: app.MethodAzure, Slot: authpkg.SlotPersonal}, read: app.Method{Kind: app.MethodAzure, Tenants: app.TenantsPersonal, PersonalAccounts: true}, wantWords: "Microsoft personal accounts"},
		{name: "one tenant and personal accounts", slot: authpkg.Method{Kind: app.MethodAzure, Slot: authpkg.SlotOnePersonal}, read: app.Method{Kind: app.MethodAzure, Tenants: app.TenantsOne, PersonalAccounts: true}, wantWords: "the accounts of one Microsoft Entra tenant, and Microsoft personal accounts"},
		{name: "any tenant and personal accounts", slot: authpkg.Method{Kind: app.MethodAzure, Slot: authpkg.SlotAnyPersonal}, read: app.Method{Kind: app.MethodAzure, Tenants: app.TenantsAny, PersonalAccounts: true}, wantWords: "the accounts of any Microsoft Entra tenant, and Microsoft personal accounts"},
		{name: "one Workspace", slot: authpkg.Method{Kind: app.MethodGoogle, Slot: authpkg.SlotOne}, read: app.Method{Kind: app.MethodGoogle, Workspaces: app.WorkspacesOne}, wantWords: "the accounts of one Google Workspace", wantLimited: true},
		{name: "listed Workspaces", slot: authpkg.Method{Kind: app.MethodGoogle, Slot: authpkg.SlotListed}, read: app.Method{Kind: app.MethodGoogle, Workspaces: app.WorkspacesListed}, wantWords: "the accounts of the Google Workspaces it lists", wantLimited: true},
		{name: "any Workspace", slot: authpkg.Method{Kind: app.MethodGoogle, Slot: authpkg.SlotAnyWorkspace}, read: app.Method{Kind: app.MethodGoogle, Workspaces: app.WorkspacesAny}, wantWords: "the accounts of any Google Workspace"},
		{name: "any Google account", slot: authpkg.Method{Kind: app.MethodGoogle, Slot: authpkg.SlotAny}, read: app.Method{Kind: app.MethodGoogle, Workspaces: app.WorkspacesAny, GoogleAccounts: true}, wantWords: "any Google account"},
		{name: "one Workspace and any Google account", slot: authpkg.Method{Kind: app.MethodGoogle, Slot: authpkg.SlotMixed}, read: app.Method{Kind: app.MethodGoogle, Workspaces: app.WorkspacesOne, GoogleAccounts: true}, wantWords: "the accounts of one Google Workspace, and any Google account"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			want := Width{Words: tt.wantWords, Limited: tt.wantLimited, Read: true}
			if diff := cmp.Diff(want, SlotWidth(&tt.slot)); diff != "" {
				t.Errorf("SlotWidth() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(want, ReadWidth(&tt.read)); diff != "" {
				t.Errorf("ReadWidth() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// A registration whose slot the scan did not read has no width to say, and neither
	// has any Google account built alone, which is no slot impulse writes but reads as
	// what it is.
	for _, unread := range []app.Method{{Kind: app.MethodAzure}, {Kind: app.MethodGoogle}, {Kind: app.MethodWorkOS}} {
		if got := ReadWidth(&unread); got.Read {
			t.Errorf("ReadWidth(%s with no slot read) = %+v, want it not read", unread.Kind, got)
		}
	}
	alone := app.Method{Kind: app.MethodGoogle, Workspaces: app.WorkspacesAccounts, GoogleAccounts: true}
	if got, want := ReadWidth(&alone), (Width{Words: "any Google account", Limited: false, Read: true}); got != want {
		t.Errorf("ReadWidth(any Google account alone) = %+v, want %+v", got, want)
	}
}

// TestShape_OneTenantChoice settles whether a person belongs to one tenant from the
// shape, and refuses the flag where it says the opposite, naming both and the shape
// that agrees with the flag.
func TestShape_OneTenantChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		shape   string
		given   bool
		value   bool
		want    bool
		wantErr string
	}{
		{name: "tenant-owned users fill it in", shape: "tenant-owned-users-invite-only", want: true},
		{name: "tenant-owned users, by directory, said twice", shape: "tenant-owned-users-by-directory", given: true, value: true, want: true},
		{name: "global users leave it out", shape: "global-users-each-tenant-chooses"},
		{name: "global users, said twice", shape: "global-users-invite-only", given: true},
		{
			name: "global users beside one tenant per person", shape: "global-users-invite-only", given: true, value: true,
			wantErr: "--shape global-users-invite-only and --one-tenant-per-person contradict each other: in Global users, invite only a person can be in several tenants, and --one-tenant-per-person holds a person to one tenant. Take --shape tenant-owned-users-invite-only, or leave --one-tenant-per-person out",
		},
		{
			name: "tenant-owned users beside several", shape: "tenant-owned-users-by-directory", given: true,
			wantErr: "--shape tenant-owned-users-by-directory and --one-tenant-per-person=false contradict each other: in Tenant-owned users, by directory a person belongs to one tenant, and --one-tenant-per-person=false lets a person be in several. Take --shape global-users-each-tenant-chooses, or leave --one-tenant-per-person out",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := shapeOf(t, tt.shape).OneTenantChoice(tt.given, tt.value)
			if tt.wantErr != "" {
				if err == nil || errors.Cause(err).Error() != tt.wantErr {
					t.Fatalf("OneTenantChoice() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("OneTenantChoice() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("OneTenantChoice() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestShape_StandsAlone refuses Directory roles given more than one method, naming the
// shape and the methods: the method whose directory gives the roles is the one method
// of its auth. One method is taken, and the other shapes take any number.
func TestShape_StandsAlone(t *testing.T) {
	t.Parallel()

	two := []string{"--method google:workspace", "--method azure:entra"}
	err := shapeOf(t, "directory-roles").StandsAlone(two)
	if want := "--shape directory-roles and 2 methods (--method google:workspace, --method azure:entra) contradict each other: in Directory roles the directory of one organization gives the roles, and a method whose directory gives the roles stands alone in its auth, since the directory is then the authority for everyone the auth holds. Name the one directory method, and give another way in an auth of its own"; err == nil || errors.Cause(err).Error() != want {
		t.Errorf("StandsAlone() error = %v, want %q", err, want)
	}
	if err := shapeOf(t, "directory-roles").StandsAlone(two[:1]); err != nil {
		t.Errorf("StandsAlone() error = %v, want none for one method", err)
	}
	for _, key := range []string{"invite-only", "directory-members", "open-sign-up"} {
		if err := shapeOf(t, key).StandsAlone(two); err != nil {
			t.Errorf("%s: StandsAlone() error = %v, want none: the shape takes any number of methods", key, err)
		}
	}
}

// TestShape_DirectoriesChoice settles whether a tenant can have a directory of its own
// from the shape, and refuses the flag where it says the opposite, naming both and the
// shape that agrees with the flag. The choice is the application's: nothing here asks
// which sign-in methods it has.
func TestShape_DirectoriesChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		shape   string
		given   bool
		value   bool
		want    bool
		wantErr string
	}{
		{name: "tenant-owned users, invite only, leave the tenants without directories", shape: "tenant-owned-users-invite-only"},
		{name: "global users, invite only, said twice", shape: "global-users-invite-only", given: true},
		{name: "tenant-owned users, by directory, fill it in", shape: "tenant-owned-users-by-directory", want: true},
		{name: "global users, each tenant chooses, said twice", shape: "global-users-each-tenant-chooses", given: true, value: true, want: true},
		{
			name: "invite only beside tenant directories", shape: "tenant-owned-users-invite-only", given: true, value: true,
			wantErr: "--shape tenant-owned-users-invite-only and --tenant-directories contradict each other: in Tenant-owned users, invite only a tenant's people come by invitation alone and no tenant has a directory, and --tenant-directories lets a tenant have one. Take --shape tenant-owned-users-by-directory, or leave --tenant-directories out",
		},
		{
			name: "global users, invite only, beside tenant directories", shape: "global-users-invite-only", given: true, value: true,
			wantErr: "--shape global-users-invite-only and --tenant-directories contradict each other: in Global users, invite only a tenant's people come by invitation alone and no tenant has a directory, and --tenant-directories lets a tenant have one. Take --shape global-users-each-tenant-chooses, or leave --tenant-directories out",
		},
		{
			name: "by directory beside no tenant directories", shape: "tenant-owned-users-by-directory", given: true,
			wantErr: "--shape tenant-owned-users-by-directory and --tenant-directories=false contradict each other: in Tenant-owned users, by directory a tenant's people come through the tenant's own directory, and --tenant-directories=false leaves the tenants without directories. Take --shape tenant-owned-users-invite-only, or leave --tenant-directories out",
		},
		{
			name: "each tenant chooses beside no tenant directories", shape: "global-users-each-tenant-chooses", given: true,
			wantErr: "--shape global-users-each-tenant-chooses and --tenant-directories=false contradict each other: in Global users, each tenant chooses a tenant's people come through the tenant's own directory, and --tenant-directories=false leaves the tenants without directories. Take --shape global-users-invite-only, or leave --tenant-directories out",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := shapeOf(t, tt.shape).DirectoriesChoice(tt.given, tt.value)
			if tt.wantErr != "" {
				if err == nil || errors.Cause(err).Error() != tt.wantErr {
					t.Fatalf("DirectoriesChoice() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("DirectoriesChoice() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("DirectoriesChoice() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestShape_tenants pins what a starting point of shapes 5 to 8 says once the tenants
// are laid in: whether a person belongs to one tenant, how a tenant's people get in,
// and what is still to choose. An invite-only shape takes any sign-in method: its
// tenants have no directories, so a method through an organization's directory is a way
// for an invited person to sign in, and the shape says so by the method's name.
func TestShape_tenants(t *testing.T) {
	t.Parallel()

	google := []string{"the staff auth's google method (pkg/auth/staff/google.go:64)"}
	both := []string{google[0], "the staff auth's entra method (pkg/auth/staff/entra.go:70)"}
	const (
		one      = "A person belongs to one tenant: the identity client of every auth whose roles the application gives is built with identity.WithOneTenantPerPerson(), so a role in a second tenant is refused however it would be given."
		several  = "A person can be in several tenants: each tenant invites them and controls the role it gave, and no identity client is built with identity.WithOneTenantPerPerson()."
		invited  = "A tenant's people come by invitation alone: the application has no tenant directories (the tenant record declares none and carries no directory-only switch), so no tenant takes a directory, none is opened to one, and none is made directory only. "
		authored = "is the author's act, laying the tenant directories in (the directories record and its table, the tenant record's directory-only column, each auth's directory admission), which no impulse add method does by itself."
	)
	tests := []struct {
		name    string
		shape   string
		methods []string
		want    []string
	}{
		{
			name: "tenant-owned users, invite only", shape: "tenant-owned-users-invite-only",
			want: []string{
				one,
				invited + "A Google, Microsoft or other provider's method added with impulse add method, the application giving its roles, is a way for an invited person to sign in and nothing more, and impulse audit reads the application as 5 Tenant-owned users, invite only with it as without it. Going to 6 Tenant-owned users, by directory " + authored,
			},
		},
		{
			name: "tenant-owned users, invite only, with a Google method there", shape: "tenant-owned-users-invite-only", methods: google,
			want: []string{
				one,
				invited + "The staff auth's google method (pkg/auth/staff/google.go:64) is a way for an invited person to sign in and nothing more, and impulse audit reads the application as 5 Tenant-owned users, invite only with it as without it. Going to 6 Tenant-owned users, by directory " + authored,
			},
		},
		{
			name: "global users, invite only, with two directory methods there", shape: "global-users-invite-only", methods: both,
			want: []string{
				several,
				invited + "The staff auth's google method (pkg/auth/staff/google.go:64) and the staff auth's entra method (pkg/auth/staff/entra.go:70) are each a way for an invited person to sign in and nothing more, and impulse audit reads the application as 7 Global users, invite only with them as without them. Going to 8 Global users, each tenant chooses " + authored,
			},
		},
		{
			name: "global users, each tenant chooses, before the directory method is there", shape: "global-users-each-tenant-chooses",
			want: []string{
				"A person can be in several tenants: each tenant invites them and controls the role it gave, and no identity client is built with identity.WithOneTenantPerPerson().",
				"A tenant's people come through its organization's directory, and no sign-in method goes through one yet. Add the one the tenants' organizations use, wide enough for all of them, with the application giving the roles: impulse add method staff google --slot any-workspace --authority application for Google Workspace, or impulse add method staff azure --slot any --authority application for Microsoft Entra. Until then impulse audit reads the application as 7 Global users, invite only. With the method there, a tenant's administrator, signed in through their organization's directory, makes it the tenant's on the tenant's Sign-in page, and chooses there whether its people get an account without an invitation; both are chosen while the application runs.",
			},
		},
		{
			name: "tenant-owned users, by directory, with the method there", shape: "tenant-owned-users-by-directory", methods: google,
			want: []string{
				"A person belongs to one tenant: the identity client of every auth whose roles the application gives is built with identity.WithOneTenantPerPerson(), so a role in a second tenant is refused however it would be given.",
				"A tenant's people can come through its organization's directory, by the staff auth's google method (pkg/auth/staff/google.go:64): a tenant's administrator, signed in through their organization's directory, makes it the tenant's on the tenant's Sign-in page, and chooses there whether its people get an account without an invitation; both are chosen while the application runs.",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if diff := cmp.Diff(tt.want, shapeOf(t, tt.shape).TenantsSay([]string{"staff"}, tt.methods)); diff != "" {
				t.Errorf("TenantsSay() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
