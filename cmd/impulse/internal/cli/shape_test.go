package cli

import (
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/shape"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton"
	transition_ "github.com/impulseframework/impulse/cmd/impulse/internal/transition"
)

// The four commands that take --shape, with arguments each is complete with: nothing is
// asked, so a refusal a test meets is the shape's.
var shapeCommands = []struct {
	name string
	cmd  func() *cobra.Command
	args []string
	// knows are the names the command's --shape takes.
	knows string
}{
	{name: "impulse add auth", cmd: newAddAuth, args: []string{"partners", "--method", "google:workspace", "--outlet", "portal"}, knows: "invite-only, directory-members, directory-roles, open-sign-up"},
	{name: "impulse add method", cmd: newAddMethod, args: []string{"staff", "google"}, knows: "invite-only, directory-members, directory-roles, open-sign-up"},
	{name: "impulse new", cmd: newNew, args: []string{"harbor", "--module", "example.com/harbor", "--auth", "staff"}, knows: "directory-roles, tenant-owned-users-invite-only, tenant-owned-users-by-directory, global-users-invite-only, global-users-each-tenant-chooses"},
	{name: "impulse add tenancy", cmd: newAddTenancy, knows: "tenant-owned-users-invite-only, tenant-owned-users-by-directory, global-users-invite-only, global-users-each-tenant-chooses"},
}

// execute runs a command built on its own, under the root's name so its refusals name
// it as a person typed it, and returns the message its error prints.
func execute(t *testing.T, build func() *cobra.Command, args ...string) string {
	t.Helper()

	cmd := build()
	parent := &cobra.Command{Use: "impulse"}
	if cmd.Name() != "new" {
		add := &cobra.Command{Use: "add"}
		parent.AddCommand(add)
		parent = add
	}
	parent.AddCommand(cmd)
	cmd.SetArgs(args)
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	// Execute runs the root with its own arguments, so the command is run directly.
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags() error = %v", err)
	}
	err := cmd.RunE(cmd, cmd.Flags().Args())
	if err == nil {
		t.Fatal("the command ran, want it refused")
	}

	return message(err)
}

// TestShape_unknownName refuses a --shape name a command does not know before anything
// is asked, read or written, with the names the command knows; each command's help
// lists the same names.
func TestShape_unknownName(t *testing.T) {
	t.Parallel()

	for _, tt := range shapeCommands {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := execute(t, tt.cmd, append(tt.args, "--shape", "members-only")...)
			if want := "--shape members-only: " + tt.name + " does not know that shape. It knows " + tt.knows; got != want {
				t.Errorf("the refusal mismatch (-want +got):\n%s", cmp.Diff(want, got))
			}
			cmd := tt.cmd()
			for _, key := range strings.Split(tt.knows, ", ") {
				if usage := cmd.Flags().Lookup(shape.FlagName).Usage; !strings.Contains(usage, key+" (") {
					t.Errorf("the --shape flag's description does not offer %s:\n%s", key, usage)
				}
				if !strings.Contains(cmd.Long, key) {
					t.Errorf("the help does not name %s", key)
				}
			}
		})
	}

	// A shape another command offers is refused the same way, with where it is one.
	got := execute(t, newAddTenancy, "--shape", "open-sign-up")
	if want := "4 Open sign-up is a starting point of impulse add auth and impulse add method"; !strings.Contains(got, want) {
		t.Errorf("the refusal %q lacks %q", got, want)
	}
}

// TestShape_contradictingFlag refuses, on each of the four commands, a flag given beside
// --shape that says the opposite of the shape, before anything is asked, read or
// written: the refusal names both, says what each means, and says what to do.
func TestShape_contradictingFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
		want string
	}{
		{
			name: "add auth: Directory members with the directory's authority", cmd: newAddAuth,
			args: []string{"partners", "--method", "google:workspace", "--outlet", "portal", "--shape", "directory-members", "--authority", "directory"},
			want: "--shape directory-members and --authority directory contradict each other: Directory members is a method limited to the organizations it names, opened to sign-up so that everyone in the organization's directory gets an account, with the application giving the roles, and --authority directory has the directory give the roles to everyone it signs in. Take --shape directory-roles, or leave --authority out",
		},
		{
			name: "add auth: Directory members with the password method alone", cmd: newAddAuth,
			args: []string{"partners", "--outlet", "portal", "--shape", "directory-members"},
			want: "--shape directory-members needs a directory method, and add auth was given the password method alone: Directory members is a method limited to the organizations it names, opened to sign-up so that everyone in the organization's directory gets an account, with the application giving the roles. Name one with --method azure:<registration>, google:<registration> or oidc:<registration>",
		},
		{
			name: "add auth: Open sign-up with an identity provider's method", cmd: newAddAuth,
			args: []string{"partners", "--method", "oidc:okta", "--outlet", "portal", "--shape", "open-sign-up"},
			want: "--shape open-sign-up and an identity provider's method (oidc) contradict each other: Open sign-up is a method anyone can use, opened to sign-up, with the application giving the roles, and an identity provider's registration admits the accounts of one organization's identity provider and nobody else. Take --shape directory-members, or open the password method, a Microsoft method or a Google method",
		},
		{
			name: "add auth: Directory roles with a second method", cmd: newAddAuth,
			args: []string{"members", "--method", "password", "--method", "google:workspace", "--outlet", "portal", "--shape", "directory-roles"},
			want: "--shape directory-roles and 2 methods (--method password, --method google:workspace) contradict each other: in Directory roles the directory of one organization gives the roles, and a method whose directory gives the roles stands alone in its auth, since the directory is then the authority for everyone the auth holds. Name the one directory method, and give another way in an auth of its own",
		},
		{
			name: "add method: Directory members with a slot anyone can use", cmd: newAddMethod,
			args: []string{"staff", "azure", "--shape", "directory-members", "--slot", "any"},
			want: "--shape directory-members and --slot any contradict each other: Directory members is a method limited to the organizations it names, opened to sign-up so that everyone in the organization's directory gets an account, with the application giving the roles, and --slot any admits the accounts of any Microsoft Entra tenant. Take --shape open-sign-up, or --slot one or listed",
		},
		{
			name: "add method: Directory roles with the application's authority", cmd: newAddMethod,
			args: []string{"members", "google", "workspace", "--shape", "directory-roles", "--authority", "application"},
			want: "--shape directory-roles and --authority application contradict each other: in Directory roles the directory of one organization gives the roles, and --authority application has the application give them. Take --shape invite-only, --shape directory-members or --shape open-sign-up, or leave --authority out",
		},
		{
			name: "add method: Directory members with the password method", cmd: newAddMethod,
			args: []string{"staff", "password", "--shape", "directory-members"},
			want: "--shape directory-members and the password method contradict each other: Directory members is a method limited to the organizations it names, opened to sign-up so that everyone in the organization's directory gets an account, with the application giving the roles, and the password method admits anyone with an email address. Take a directory method (azure, google or oidc), or --shape invite-only or --shape open-sign-up",
		},
		{
			name: "new: global users with one tenant per person", cmd: newNew,
			args: []string{"harbor", "--module", "example.com/harbor", "--auth", "staff", "--shape", "global-users-invite-only", "--one-tenant-per-person"},
			want: "--shape global-users-invite-only and --one-tenant-per-person contradict each other: in Global users, invite only a person can be in several tenants, and --one-tenant-per-person holds a person to one tenant. Take --shape tenant-owned-users-invite-only, or leave --one-tenant-per-person out",
		},
		{
			name: "new: a shape with tenants and tenancy turned off", cmd: newNew,
			args: []string{"harbor", "--module", "example.com/harbor", "--auth", "staff", "--shape", "global-users-each-tenant-chooses", "--tenancy=false"},
			want: "--shape global-users-each-tenant-chooses and --tenancy=false contradict each other: Global users, each tenant chooses is an application with tenants, and --tenancy=false leaves them out. Leave --tenancy out, or leave --shape out",
		},
		{
			name: "new: one tenant per person with no tenants", cmd: newNew,
			args: []string{"harbor", "--module", "example.com/harbor", "--auth", "staff", "--one-tenant-per-person"},
			want: "--one-tenant-per-person goes with --tenancy: it says a person belongs to one tenant, and the application is given none",
		},
		{
			name: "new: Directory roles with tenants", cmd: newNew,
			args: []string{"harbor", "--module", "example.com/harbor", "--auth", "staff", "--shape", "directory-roles", "--method", "google", "--organization", "fleet.example", "--tenancy"},
			want: "--shape directory-roles and --tenancy contradict each other: Directory roles is an application without tenants, in which one organization's directory gives every role, and --tenancy is a choice of an application with tenants, which gives its roles itself, tenant by tenant. Leave --tenancy out, or start from a shape with tenants (tenant-owned-users-invite-only, tenant-owned-users-by-directory, global-users-invite-only, global-users-each-tenant-chooses)",
		},
		{
			name: "new: Directory roles with one of the tenants' choices", cmd: newNew,
			args: []string{"harbor", "--module", "example.com/harbor", "--auth", "staff", "--shape", "directory-roles", "--method", "google", "--organization", "fleet.example", "--one-tenant-per-person"},
			want: "--shape directory-roles and --one-tenant-per-person contradict each other: Directory roles is an application without tenants, in which one organization's directory gives every role, and --one-tenant-per-person is a choice of an application with tenants, which gives its roles itself, tenant by tenant. Leave --one-tenant-per-person out, or start from a shape with tenants (tenant-owned-users-invite-only, tenant-owned-users-by-directory, global-users-invite-only, global-users-each-tenant-chooses)",
		},
		{
			name: "new: a directory named with no shape", cmd: newNew,
			args: []string{"harbor", "--module", "example.com/harbor", "--auth", "staff", "--method", "google"},
			want: "--method goes with --shape directory-roles: it names the one directory that gives every role in that shape. Without it the first auth signs in with a password, and a directory method joins it afterwards with impulse add method",
		},
		{
			name: "new: one provider named for tenant-owned users by directory", cmd: newNew,
			args: []string{"harbor", "--module", "example.com/harbor", "--auth", "staff", "--shape", "tenant-owned-users-by-directory", "--method", "google"},
			want: "--shape tenant-owned-users-by-directory takes no --method: the shape starts with a Google method open to any Google Workspace and a Microsoft method open to any Microsoft Entra tenant, and no flag takes one provider or another width. Leave --method out; another width is your change to what the command writes",
		},
		{
			name: "new: an organization named for a shape that signs in with a password", cmd: newNew,
			args: []string{"harbor", "--module", "example.com/harbor", "--auth", "staff", "--shape", "global-users-invite-only", "--organization", "fleet.example"},
			want: "--shape global-users-invite-only takes no --organization: the flag names the one directory of --shape directory-roles, and in Global users, invite only the first auth signs in with a password. Leave --organization out, and add a directory method afterwards with impulse add method",
		},
		{
			name: "add tenancy: tenant-owned users with several tenants per person", cmd: newAddTenancy,
			args: []string{"--shape", "tenant-owned-users-by-directory", "--one-tenant-per-person=false"},
			want: "--shape tenant-owned-users-by-directory and --one-tenant-per-person=false contradict each other: in Tenant-owned users, by directory a person belongs to one tenant, and --one-tenant-per-person=false lets a person be in several. Take --shape global-users-each-tenant-chooses, or leave --one-tenant-per-person out",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := execute(t, tt.cmd, tt.args...); got != tt.want {
				t.Errorf("the refusal mismatch (-want +got):\n%s", cmp.Diff(tt.want, got))
			}
		})
	}
}

// methodsShape is the shape of the name among the ones add auth and add method offer.
func methodsShape(t *testing.T, key string) *shape.Shape {
	t.Helper()

	s, err := shape.Find(key, shape.OfMethods(), "the test")
	if err != nil {
		t.Fatal(err)
	}

	return &s
}

// Test_authMethods_shape fills in the methods add auth starts an auth with from each
// shape it offers: the authority is the shape's and is never asked, Open sign-up takes
// the slot anyone can use, and under Directory members a password method beside the
// directory method is left as it is.
func Test_authMethods_shape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		shape   string
		methods []string
		want    []authpkg.Method
	}{
		{
			name: "invite only", shape: "invite-only", methods: []string{"password", "azure:entra"},
			want: []authpkg.Method{{Kind: app.MethodPassword}, {Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityApplication}},
		},
		{
			name: "directory members", shape: "directory-members", methods: []string{"password", "google:workspace"},
			want: []authpkg.Method{{Kind: app.MethodPassword}, {Kind: app.MethodGoogle, Name: "workspace", Authority: app.AuthorityApplication}},
		},
		{
			name: "directory roles", shape: "directory-roles", methods: []string{"oidc:okta"},
			want: []authpkg.Method{{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityDirectory}},
		},
		{
			name: "open sign-up", shape: "open-sign-up", methods: []string{"password", "google", "azure:entra"},
			want: []authpkg.Method{
				{Kind: app.MethodPassword},
				{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityApplication, Slot: authpkg.SlotAny},
				{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityApplication, Slot: authpkg.SlotAnyPersonal},
			},
		},
		{
			name: "open sign-up with the password method alone, which add auth takes when none is named", shape: "open-sign-up",
			want: []authpkg.Method{{Kind: app.MethodPassword}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// No terminal is at the test's standard input, so an authority that was asked
			// would be refused as required: the shape settles it instead.
			got, err := authMethods(newAddAuth(), tt.methods, "", false, methodsShape(t, tt.shape))
			if err != nil {
				t.Fatalf("authMethods() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("authMethods() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_methodSpec_shape fills in the method add method writes from each shape it
// offers, keeping the choices the flags made where the shape allows them.
func Test_methodSpec_shape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		shape        string
		kind         string
		registration string
		slot         string
		authority    string
		want         authpkg.Method
	}{
		{name: "invite only", shape: "invite-only", kind: "google", slot: authpkg.SlotAny, want: authpkg.Method{Kind: app.MethodGoogle, Name: "google", Slot: authpkg.SlotAny, Authority: app.AuthorityApplication}},
		{name: "invite only, the persona method", shape: "invite-only", kind: "persona", want: authpkg.Method{Kind: app.MethodPersona, Name: "persona", Authority: app.AuthorityApplication}},
		{name: "directory members", shape: "directory-members", kind: "azure", registration: "entra", slot: authpkg.SlotListed, want: authpkg.Method{Kind: app.MethodAzure, Name: "entra", Slot: authpkg.SlotListed, Authority: app.AuthorityApplication}},
		{name: "directory roles", shape: "directory-roles", kind: "google", registration: "workspace", want: authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: app.AuthorityDirectory}},
		{name: "open sign-up, a Microsoft method", shape: "open-sign-up", kind: "azure", want: authpkg.Method{Kind: app.MethodAzure, Name: "azure", Slot: authpkg.SlotAnyPersonal, Authority: app.AuthorityApplication}},
		{name: "open sign-up, the password method", shape: "open-sign-up", kind: "password", want: authpkg.Method{Kind: app.MethodPassword}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := methodSpec(newAddMethod(), tt.kind, tt.registration, tt.slot, tt.authority, "", false, methodsShape(t, tt.shape))
			if err != nil {
				t.Fatalf("methodSpec() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("methodSpec() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// Without a shape a directory method's authority is still asked, and required where
	// nobody is at the terminal.
	if _, err := methodSpec(newAddMethod(), "google", "", "", "", "", false, nil); err == nil || !strings.Contains(err.Error(), "--authority is required for a directory method") {
		t.Errorf("methodSpec() without a shape error = %v, want the authority required", err)
	}
}

// Test_settleShape composes tenancy into impulse new from each shape it offers, and
// settles the tenants' choices: the two tenant-owned shapes hold a person to one
// tenant, the two invite-only shapes leave the tenants without directories, and the
// transition the creation composes carries both.
func Test_settleShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		shape    string
		wantOne  bool
		wantNone bool
		// wantStart is a shape with no password sign-in, whose first auth is written
		// again before the tenants are laid in.
		wantStart bool
		wantSays  string
	}{
		{shape: "tenant-owned-users-invite-only", wantOne: true, wantNone: true, wantSays: "A tenant's people come by invitation alone: the application has no tenant directories"},
		{shape: "tenant-owned-users-by-directory", wantOne: true, wantStart: true, wantSays: "A tenant's people can come through its organization's directory, by the staff auth's google method and the staff auth's azure method: a tenant's administrator, signed in through their organization's directory, makes it the tenant's"},
		{shape: "global-users-invite-only", wantNone: true, wantSays: "impulse audit reads the application as 7 Global users, invite only with it as without it."},
		{shape: "global-users-each-tenant-chooses", wantSays: "Until then impulse audit reads the application as 7 Global users, invite only."},
	}
	for _, tt := range tests {
		t.Run(tt.shape, func(t *testing.T) {
			t.Parallel()

			var nf newFlags
			cmd := &cobra.Command{Use: "new"}
			nf.bind(cmd)
			if err := cmd.ParseFlags([]string{"--module", "example.com/harbor", "--shape", tt.shape}); err != nil {
				t.Fatal(err)
			}
			start, err := nf.opts.settleShape(cmd)
			if err != nil {
				t.Fatalf("settleShape() error = %v", err)
			}
			if start == nil || start.Key != tt.shape {
				t.Fatalf("settleShape() = %v, want the %s shape", start, tt.shape)
			}
			nf.opts.authName = "staff"
			if err := nf.opts.settleStart(cmd, start); err != nil {
				t.Fatalf("settleStart() error = %v", err)
			}
			transitions, err := nf.opts.transitions()
			if err != nil {
				t.Fatal(err)
			}
			want := []transition{transition_.Tenancy{Table: "Tenants", OneTenantPerPerson: tt.wantOne, NoDirectories: tt.wantNone}}
			if tt.wantStart {
				// The first auth's methods come before the tenants, which build on the auth.
				want = append([]transition{transition_.DirectoryStart{
					Auth: "staff", ShapeKey: tt.shape, ShapeName: start.String(),
					Methods: []authpkg.Method{
						{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityApplication, Slot: authpkg.SlotAnyWorkspace},
						{Kind: app.MethodAzure, Name: "azure", Authority: app.AuthorityApplication, Slot: authpkg.SlotAny},
						{Kind: app.MethodPersona, Authority: app.AuthorityApplication},
					},
				}}, want...)
			}
			if diff := cmp.Diff(want, transitions); diff != "" {
				t.Errorf("the composed transitions mismatch (-want +got):\n%s", diff)
			}
			says := strings.Join(nf.opts.startingPoint(start).says, " ")
			if !strings.Contains(says, tt.wantSays) {
				t.Errorf("the starting point says %q, want it to say %q", says, tt.wantSays)
			}
			if noPassword := strings.Contains(says, "The staff auth has no password sign-in"); noPassword != tt.wantStart {
				t.Errorf("the starting point says the auth has no password sign-in: %v, want %v: %q", noPassword, tt.wantStart, says)
			}
			if signIn := nf.opts.developmentSignIn(start); (signIn != nil) != tt.wantStart {
				t.Errorf("developmentSignIn() = %v, want one only where the start has no password sign-in: any other start keeps the base's development login", signIn)
			}
		})
	}

	// Without a shape the flag alone makes the choice, beside --tenancy.
	var nf newFlags
	cmd := &cobra.Command{Use: "new"}
	nf.bind(cmd)
	if err := cmd.ParseFlags([]string{"--module", "example.com/harbor", "--tenancy", "--one-tenant-per-person"}); err != nil {
		t.Fatal(err)
	}
	if start, err := nf.opts.settleShape(cmd); err != nil || start != nil || !nf.opts.tenants.oneTenant || nf.opts.tenants.noDirectories {
		t.Errorf("settleShape() = %v, %v with the tenants' choices %+v; want no shape, no error, one tenant per person and tenant directories", start, err, nf.opts.tenants)
	}
	if got, want := nf.opts.describe(), "tenancy (Tenants, one tenant per person)"; got != want {
		t.Errorf("describe() = %q, want %q", got, want)
	}
}

// Test_settleStart_byDirectory names what impulse new adds where it starts from
// Tenant-owned users, by directory: the first auth's sign-in in the password's place,
// then the tenants, read against the reference application with tenants.
func Test_settleStart_byDirectory(t *testing.T) {
	t.Parallel()

	var nf newFlags
	cmd := &cobra.Command{Use: "new"}
	nf.bind(cmd)
	if err := cmd.ParseFlags([]string{"--module", "example.com/harbor", "--shape", "tenant-owned-users-by-directory"}); err != nil {
		t.Fatal(err)
	}
	start, err := nf.opts.settleShape(cmd)
	if err != nil {
		t.Fatal(err)
	}
	nf.opts.authName = "members"
	if err := nf.opts.settleStart(cmd, start); err != nil {
		t.Fatalf("settleStart() error = %v, want none: the shape asks nothing about a provider", err)
	}
	if got, want := nf.opts.describe(), "the members auth's sign-in in the password's place (google, azure, persona), tenancy (Tenants, one tenant per person)"; got != want {
		t.Errorf("describe() = %q, want %q", got, want)
	}
	if got := nf.opts.reference(); got != transition_.TenancyReferenceCandidate {
		t.Errorf("reference() = %q, want the application with tenants", got)
	}
	// The next steps name the persona sign-in, since the bootstrap seeds no password.
	want := &developmentSignIn{method: "persona", as: "the persona admin"}
	if diff := cmp.Diff(want, nf.opts.developmentSignIn(start), cmp.AllowUnexported(developmentSignIn{})); diff != "" {
		t.Errorf("developmentSignIn() mismatch (-want +got):\n%s", diff)
	}
}

// Test_settleStart_directoryRoles settles the one directory of a new application that
// starts from Directory roles: --method names it and --organization its one
// organization, each required where no person is at the terminal to ask, as a test has
// none. The step composed writes the directory's method alone, the directory giving the
// roles, and nothing is composed beside it; a directory impulse new does not start
// from, an organization its provider does not know one by, and a choice the shape rules
// out are refused before anything is rendered.
func Test_settleStart_directoryRoles(t *testing.T) {
	t.Parallel()

	const tenant = "0f0c7a52-6f0c-4e3b-9d7e-3f6f2f1d9a10"
	tests := []struct {
		name         string
		args         []string
		want         transition_.DirectoryStart
		wantDescribe string
		wantSays     []string
		// wantMethod is the registration the next steps tell a person to sign in
		// through, since the bootstrap seeds no password.
		wantMethod string
		wantErr    string
	}{
		{
			name: "a Google Workspace", args: []string{"--method", "google", "--organization", "fleet.example"},
			want: transition_.DirectoryStart{
				Auth: "staff", ShapeKey: "directory-roles", ShapeName: "3 Directory roles", Organization: "fleet.example",
				Methods: []authpkg.Method{{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityDirectory}},
			},
			wantDescribe: "the staff auth's sign-in in the password's place (google)", wantMethod: "google",
			wantSays: []string{
				"The google method admits the accounts of one Google Workspace and nobody else, and its directory gives the roles",
				"The staff auth has no password sign-in and invites no first administrator",
			},
		},
		{
			name: "a Microsoft Entra tenant under a registration name", args: []string{"--method", "azure:entra", "--organization", tenant},
			want: transition_.DirectoryStart{
				Auth: "staff", ShapeKey: "directory-roles", ShapeName: "3 Directory roles", Organization: tenant,
				Methods: []authpkg.Method{{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityDirectory}},
			},
			wantDescribe: "the staff auth's sign-in in the password's place (azure)", wantMethod: "entra",
			wantSays: []string{
				"The entra method admits the accounts of one Microsoft Entra tenant and nobody else, and its directory gives the roles",
			},
		},
		{
			name:    "no directory named, and nobody to ask",
			wantErr: "--method is required with --shape directory-roles: name the one directory that gives the roles, google (a Google Workspace) or azure (a Microsoft Entra tenant), and its one organization with --organization",
		},
		{
			name: "no organization named, and nobody to ask", args: []string{"--method", "google"},
			wantErr: "--organization is required with --shape directory-roles: name the Google Workspace whose directory gives the roles, by its domain (fleet.example)",
		},
		{
			name: "no tenant named, and nobody to ask", args: []string{"--method", "azure"},
			wantErr: "--organization is required with --shape directory-roles: name the Microsoft Entra tenant whose directory gives the roles, by its tenant id",
		},
		{
			name: "another identity provider's directory", args: []string{"--method", "oidc:okta", "--organization", "fleet.example"},
			wantErr: "--method oidc:okta: impulse new starts Directory roles from a Google Workspace (google) or a Microsoft Entra tenant (azure), with :<registration> to name the registration. Another identity provider's directory is an auth of its own, added with impulse add auth <name> --shape directory-roles --method oidc:<registration>",
		},
		{
			name: "the password method", args: []string{"--method", "password", "--organization", "fleet.example"},
			wantErr: "--method password: impulse new starts Directory roles from a Google Workspace (google) or a Microsoft Entra tenant (azure), with :<registration> to name the registration. Another identity provider's directory is an auth of its own, added with impulse add auth <name> --shape directory-roles --method oidc:<registration>",
		},
		{
			name: "a Workspace named by something that is no domain", args: []string{"--method", "google", "--organization", "Fleet Example"},
			wantErr: "--organization Fleet Example: a Google method's one organization is a Google Workspace, named by its domain in lowercase (fleet.example)",
		},
		{
			name: "a tenant named by its domain", args: []string{"--method", "azure", "--organization", "fleet.example"},
			wantErr: "--organization fleet.example: a Microsoft method's one organization is a Microsoft Entra tenant, named by its tenant id (a GUID, as the Entra admin center shows it)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var nf newFlags
			cmd := &cobra.Command{Use: "new"}
			nf.bind(cmd)
			if err := cmd.ParseFlags(append([]string{"--module", "example.com/harbor", "--shape", "directory-roles"}, tt.args...)); err != nil {
				t.Fatal(err)
			}
			start, err := nf.opts.settleShape(cmd)
			if err != nil {
				t.Fatalf("settleShape() error = %v", err)
			}
			nf.opts.authName = "staff"
			err = nf.opts.settleStart(cmd, start)
			if tt.wantErr != "" {
				if err == nil || errors.Cause(err).Error() != tt.wantErr {
					t.Fatalf("settleStart() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("settleStart() error = %v", err)
			}
			transitions, err := nf.opts.transitions()
			if err != nil {
				t.Fatal(err)
			}
			// The directory's method stands alone, and the application has no tenants.
			if diff := cmp.Diff([]transition{tt.want}, transitions); diff != "" {
				t.Errorf("the composed transitions mismatch (-want +got):\n%s", diff)
			}
			if got := nf.opts.describe(); got != tt.wantDescribe {
				t.Errorf("describe() = %q, want %q", got, tt.wantDescribe)
			}
			if got := nf.opts.reference(); got != transition_.ReferenceCandidate {
				t.Errorf("reference() = %q, want the application that holds the reference directory auth", got)
			}
			says := strings.Join(nf.opts.startingPoint(start).says, " ")
			for _, want := range tt.wantSays {
				if !strings.Contains(says, want) {
					t.Errorf("the starting point says %q, want it to say %q", says, want)
				}
			}
			signIn := &developmentSignIn{method: tt.wantMethod, as: "the person APP_USERNAME names in the environment template, with the roles APP_ROLES names there"}
			if diff := cmp.Diff(signIn, nf.opts.developmentSignIn(start), cmp.AllowUnexported(developmentSignIn{})); diff != "" {
				t.Errorf("developmentSignIn() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_settleShape_tenantDirectories settles whether a tenant can have a directory of
// its own where impulse new is given no shape, or a flag beside one. Without a shape
// the flag makes the choice beside --tenancy, and tenancy alone writes the tenant
// directories as it always has. Beside a shape, a flag that says the opposite is
// refused, naming both and the shape that agrees with the flag, and without --tenancy
// the flag has nothing to say.
func Test_settleShape_tenantDirectories(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		args         []string
		wantNone     bool
		wantDescribe string
		wantErr      string
	}{
		{name: "tenancy alone writes the tenant directories", args: []string{"--tenancy"}, wantDescribe: "tenancy (Tenants)"},
		{name: "tenancy with the flag said as it is", args: []string{"--tenancy", "--tenant-directories"}, wantDescribe: "tenancy (Tenants)"},
		{name: "tenancy without tenant directories", args: []string{"--tenancy", "--tenant-directories=false"}, wantNone: true, wantDescribe: "tenancy (Tenants, no tenant directories)"},
		{
			name: "both of the tenants' choices", args: []string{"--tenancy", "--one-tenant-per-person", "--tenant-directories=false"},
			wantNone: true, wantDescribe: "tenancy (Tenants, one tenant per person, no tenant directories)",
		},
		{name: "an invite-only shape, said twice", args: []string{"--shape", "global-users-invite-only", "--tenant-directories=false"}, wantNone: true, wantDescribe: "tenancy (Tenants, no tenant directories)"},
		{name: "a directory shape, said twice", args: []string{"--shape", "global-users-each-tenant-chooses", "--tenant-directories"}, wantDescribe: "tenancy (Tenants)"},
		{
			name: "an invite-only shape beside tenant directories", args: []string{"--shape", "tenant-owned-users-invite-only", "--tenant-directories"},
			wantErr: "--shape tenant-owned-users-invite-only and --tenant-directories contradict each other: in Tenant-owned users, invite only a tenant's people come by invitation alone and no tenant has a directory, and --tenant-directories lets a tenant have one. Take --shape tenant-owned-users-by-directory, or leave --tenant-directories out",
		},
		{
			name: "a directory shape beside no tenant directories", args: []string{"--shape", "global-users-each-tenant-chooses", "--tenant-directories=false"},
			wantErr: "--shape global-users-each-tenant-chooses and --tenant-directories=false contradict each other: in Global users, each tenant chooses a tenant's people come through the tenant's own directory, and --tenant-directories=false leaves the tenants without directories. Take --shape global-users-invite-only, or leave --tenant-directories out",
		},
		{
			name: "the flag without tenants", args: []string{"--tenant-directories=false"},
			wantErr: "--tenant-directories goes with --tenancy: it says whether a tenant can have a directory of its own, and the application is given no tenants",
		},
		{
			name: "the flag said as it is, without tenants", args: []string{"--tenant-directories"},
			wantErr: "--tenant-directories goes with --tenancy: it says whether a tenant can have a directory of its own, and the application is given no tenants",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var nf newFlags
			cmd := &cobra.Command{Use: "new"}
			nf.bind(cmd)
			if err := cmd.ParseFlags(append([]string{"--module", "example.com/harbor"}, tt.args...)); err != nil {
				t.Fatal(err)
			}
			_, err := nf.opts.settleShape(cmd)
			if tt.wantErr != "" {
				if err == nil || errors.Cause(err).Error() != tt.wantErr {
					t.Fatalf("settleShape() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("settleShape() error = %v", err)
			}
			transitions, err := nf.opts.transitions()
			if err != nil {
				t.Fatal(err)
			}
			if len(transitions) != 1 {
				t.Fatalf("the composed transitions are %v, want the tenancy step alone", transitions)
			}
			tenancy, ok := transitions[0].(transition_.Tenancy)
			if !ok || tenancy.NoDirectories != tt.wantNone {
				t.Errorf("the composed transition is %+v, want the tenancy step with NoDirectories %v", transitions[0], tt.wantNone)
			}
			if got := nf.opts.describe(); got != tt.wantDescribe {
				t.Errorf("describe() = %q, want %q", got, tt.wantDescribe)
			}
		})
	}
}

// Test_tenancyAsked holds add tenancy's own flags to the step they ask for. With no
// flag the step is the one it always was: a person can be in several tenants, and a
// tenant can have a directory. Each flag makes its choice, a shape makes both, and a
// flag that says the opposite of the shape is refused, naming both.
func Test_tenancyAsked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		args      []string
		want      transition_.Tenancy
		wantShape string
		wantErr   string
	}{
		{name: "no flag", want: transition_.Tenancy{Table: "Tenants"}},
		{name: "another table", args: []string{"--tenant-table", "Depots"}, want: transition_.Tenancy{Table: "Depots"}},
		{name: "one tenant per person", args: []string{"--one-tenant-per-person"}, want: transition_.Tenancy{Table: "Tenants", OneTenantPerPerson: true}},
		{name: "tenant directories, said as it is", args: []string{"--tenant-directories"}, want: transition_.Tenancy{Table: "Tenants"}},
		{name: "no tenant directories", args: []string{"--tenant-directories=false"}, want: transition_.Tenancy{Table: "Tenants", NoDirectories: true}},
		{
			name: "shape 5", args: []string{"--shape", "tenant-owned-users-invite-only"}, wantShape: "tenant-owned-users-invite-only",
			want: transition_.Tenancy{Table: "Tenants", OneTenantPerPerson: true, NoDirectories: true},
		},
		{
			name: "shape 6", args: []string{"--shape", "tenant-owned-users-by-directory"}, wantShape: "tenant-owned-users-by-directory",
			want: transition_.Tenancy{Table: "Tenants", OneTenantPerPerson: true},
		},
		{
			name: "shape 7, with its own choice said beside it", args: []string{"--shape", "global-users-invite-only", "--tenant-directories=false", "--tenant-table", "Depots"},
			wantShape: "global-users-invite-only", want: transition_.Tenancy{Table: "Depots", NoDirectories: true},
		},
		{
			name: "shape 8", args: []string{"--shape", "global-users-each-tenant-chooses"}, wantShape: "global-users-each-tenant-chooses",
			want: transition_.Tenancy{Table: "Tenants"},
		},
		{
			name: "an invite-only shape beside tenant directories", args: []string{"--shape", "global-users-invite-only", "--tenant-directories"},
			wantErr: "--shape global-users-invite-only and --tenant-directories contradict each other",
		},
		{
			name: "a directory shape beside no tenant directories", args: []string{"--shape", "tenant-owned-users-by-directory", "--tenant-directories=false"},
			wantErr: "--shape tenant-owned-users-by-directory and --tenant-directories=false contradict each other",
		},
		{
			name: "a shape that holds a person to one tenant beside the flag turned off", args: []string{"--shape", "tenant-owned-users-invite-only", "--one-tenant-per-person=false"},
			wantErr: "--shape tenant-owned-users-invite-only and --one-tenant-per-person=false contradict each other",
		},
		{name: "a shape of an application without tenants", args: []string{"--shape", "invite-only"}, wantErr: "invite-only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := newAddTenancy()
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatal(err)
			}
			got, start, err := tenancyAsked(cmd)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("tenancyAsked() error = %v, want one saying %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("tenancyAsked() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("the step asked for mismatch (-want +got):\n%s", diff)
			}
			key := ""
			if start != nil {
				key = start.Key
			}
			if key != tt.wantShape {
				t.Errorf("the shape started from is %q, want %q", key, tt.wantShape)
			}
		})
	}
}

// positionRE matches the line of a file position, which moves with every edit of the
// skeleton's file it is in: what a starting point says is compared without it.
var positionRE = regexp.MustCompile(`\.go:\d+`)

// renderedDir renders a skeleton candidate into a temporary directory.
func renderedDir(t *testing.T, candidate string) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), candidate)
	if _, err := skeleton.Render(&skeleton.Options{Version: "v0.1.0", WebVersion: "0.1.0", Candidate: candidate, Dir: dir, ModulePath: "example.com/acme/" + candidate}); err != nil {
		t.Fatalf("skeleton.Render() error = %v", err)
	}

	return dir
}

// tenantsShape is the shape of the name among the ones impulse new and add tenancy
// offer.
func tenantsShape(t *testing.T, key string) *shape.Shape {
	t.Helper()

	s, err := shape.Find(key, shape.OfTenants(), "the test")
	if err != nil {
		t.Fatal(err)
	}

	return &s
}

// Test_tenancyStartingPoint reads each shape add tenancy offers against the application
// it is given. On the base skeleton, whose auth signs in with a password, every shape
// is taken: the invite-only ones leave the tenants without directories, and the other
// two say which impulse add method adds the directory method a tenant's people come
// through. On an application that already registers a method through an organization's
// directory every shape is taken too: an invite-only shape names the method as a way
// for an invited person to sign in and nothing more, since its tenants have no
// directories whatever methods the application has, and a directory shape names it as
// what a tenant's people can come through their own directory by.
func Test_tenancyStartingPoint(t *testing.T) {
	t.Parallel()

	solo, withGoogle := renderedDir(t, skeleton.Base), renderedDir(t, transition_.TenancyReferenceCandidate)
	const (
		invited   = "The staff auth's google method (pkg/auth/staff/google.go:N) is a way for an invited person to sign in and nothing more, and impulse audit reads the application as "
		directory = "A tenant's people can come through its organization's directory, by the staff auth's google method (pkg/auth/staff/google.go:N)"
	)
	tests := []struct {
		shape    string
		wantSays string
		// wantGoogle is what the shape says on the application with the Google method.
		wantGoogle string
	}{
		{
			shape:      "tenant-owned-users-invite-only",
			wantSays:   "A Google, Microsoft or other provider's method added with impulse add method, the application giving its roles, is a way for an invited person to sign in and nothing more, and impulse audit reads the application as 5 Tenant-owned users, invite only with it as without it. Going to 6 Tenant-owned users, by directory is the author's act",
			wantGoogle: invited + "5 Tenant-owned users, invite only with it as without it.",
		},
		{
			shape:      "tenant-owned-users-by-directory",
			wantSays:   "A tenant's people can come through its organization's directory, by the staff auth's google method and the staff auth's azure method: a tenant's administrator",
			wantGoogle: directory,
		},
		{
			shape:      "global-users-invite-only",
			wantSays:   "Going to 8 Global users, each tenant chooses is the author's act, laying the tenant directories in",
			wantGoogle: invited + "7 Global users, invite only with it as without it.",
		},
		{
			shape:      "global-users-each-tenant-chooses",
			wantSays:   "Until then impulse audit reads the application as 7 Global users, invite only.",
			wantGoogle: directory,
		},
	}
	for _, tt := range tests {
		t.Run(tt.shape, func(t *testing.T) {
			t.Parallel()

			s := tenantsShape(t, tt.shape)
			start, _, err := tenancyStartingPoint(s, solo)
			if err != nil {
				t.Fatalf("tenancyStartingPoint() error = %v", err)
			}
			if says := strings.Join(start.says, " "); start.shape != *s || !strings.Contains(says, tt.wantSays) {
				t.Errorf("the starting point is %s and says %q, want %s saying %q", start.shape, says, s, tt.wantSays)
			}

			start, _, err = tenancyStartingPoint(s, withGoogle)
			if err != nil {
				t.Fatalf("tenancyStartingPoint() over a directory method error = %v, want none: every shape with tenants takes any sign-in method", err)
			}
			if says := positionRE.ReplaceAllString(strings.Join(start.says, " "), ".go:N"); start.shape != *s || !strings.Contains(says, tt.wantGoogle) {
				t.Errorf("over a directory method the starting point is %s and says %q, want %s saying %q", start.shape, says, s, tt.wantGoogle)
			}
		})
	}
}

// Test_tenancyStartingPoint_byDirectory holds add tenancy to what Tenant-owned users, by
// directory, is: every person of a tenant signs in through the tenant's own directory.
// On an auth that registers neither a Google nor a Microsoft method the command writes
// both, each as impulse add method would, ahead of the tenancy step; an auth that has
// either is left as its author made it. It removes no method: of an auth with the
// password method it says that the method keeps the application outside the shape, and
// names the hand step. No other shape writes a method or says anything of a password.
func Test_tenancyStartingPoint_byDirectory(t *testing.T) {
	t.Parallel()

	solo, withGoogle := renderedDir(t, skeleton.Base), renderedDir(t, transition_.TenancyReferenceCandidate)
	const (
		writes = "The staff auth registered neither a Google nor a Microsoft method, so the command writes both"
		keeps  = "The staff auth keeps its password method, since no command removes a sign-in method, and the method keeps the application outside 6 Tenant-owned users, by directory"
		byHand = "Taking it out is a hand step (remove pkg/auth/staff/password.go and the registerPassword call in pkg/auth/staff/staff.go"
	)
	s := tenantsShape(t, "tenant-owned-users-by-directory")

	start, steps, err := tenancyStartingPoint(s, solo)
	if err != nil {
		t.Fatalf("tenancyStartingPoint() error = %v", err)
	}
	want := []transition{
		&transition_.Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityApplication, Slot: authpkg.SlotAnyWorkspace}},
		&transition_.Method{Auth: "staff", Spec: authpkg.Method{Kind: app.MethodAzure, Name: "azure", Authority: app.AuthorityApplication, Slot: authpkg.SlotAny}},
	}
	if diff := cmp.Diff(want, steps); diff != "" {
		t.Errorf("the steps ahead of the tenancy step mismatch (-want +got):\n%s", diff)
	}
	says := strings.Join(start.says, " ")
	for _, want := range []string{writes, keeps, byHand} {
		if !strings.Contains(says, want) {
			t.Errorf("the starting point says %q, want it to say %q", says, want)
		}
	}
	a, err := app.Discover(solo)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range steps {
		if err := step.Validate(a); err != nil {
			t.Errorf("a step the command composes is refused by the application it is composed for: %v", err)
		}
	}

	// The auth has a Google method: nothing is written, and the password is still named.
	start, steps, err = tenancyStartingPoint(s, withGoogle)
	if err != nil {
		t.Fatalf("tenancyStartingPoint() over a directory method error = %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("over an auth with a Google method the command composes %d step(s), want none: the auth is left as its author made it", len(steps))
	}
	says = strings.Join(start.says, " ")
	if strings.Contains(says, writes) || !strings.Contains(says, keeps) || !strings.Contains(says, byHand) {
		t.Errorf("over an auth with a Google method and the password method the starting point says %q, want the password named with its hand step and no method written", says)
	}

	for _, key := range []string{"tenant-owned-users-invite-only", "global-users-invite-only", "global-users-each-tenant-chooses"} {
		other := tenantsShape(t, key)
		start, steps, err := tenancyStartingPoint(other, solo)
		if err != nil {
			t.Fatalf("%s: tenancyStartingPoint() error = %v", key, err)
		}
		if says := strings.Join(start.says, " "); len(steps) != 0 || strings.Contains(says, "keeps its password method") {
			t.Errorf("%s composes %d step(s) and says %q, want no method written and nothing said of the password: the shape signs in with one", key, len(steps), says)
		}
	}
}

// Test_byDirectorySteps holds what add tenancy does about the methods of each auth it
// starts as Tenant-owned users, by directory, to what the auth registers: the shape's two
// methods are written on an auth whose roles the application gives that registers
// neither a Google nor a Microsoft method, an auth that has either is left as its author
// made it, an auth whose directory gives the roles holds no tenant's people and gets
// neither a method nor a word, and the password method is named wherever it stands. A
// construction outside an auth package is no auth of the application's.
func Test_byDirectorySteps(t *testing.T) {
	t.Parallel()

	s := tenantsShape(t, "tenant-owned-users-by-directory")
	password := app.Method{Kind: app.MethodPassword, Name: "password", Authority: app.AuthorityApplication}
	google := app.Method{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityApplication}
	azure := app.Method{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityApplication}
	directory := app.Method{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityDirectory}
	written := func(auth string) []transition {
		return []transition{
			&transition_.Method{Auth: auth, Spec: authpkg.Method{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityApplication, Slot: authpkg.SlotAnyWorkspace}},
			&transition_.Method{Auth: auth, Spec: authpkg.Method{Kind: app.MethodAzure, Name: "azure", Authority: app.AuthorityApplication, Slot: authpkg.SlotAny}},
		}
	}
	tests := []struct {
		name        string
		auths       []app.Auth
		wantSteps   []transition
		wantWritten []string
		// wantSays are the starts of the lines said, in order.
		wantSays []string
	}{
		{
			name:        "the password alone: both methods written, and the password named",
			auths:       []app.Auth{{File: "pkg/auth/staff/staff.go", Methods: []app.Method{password}}},
			wantSteps:   written("staff"),
			wantWritten: []string{"the staff auth's google method", "the staff auth's azure method"},
			wantSays:    []string{"The staff auth registered neither a Google nor a Microsoft method, so the command writes both", "The staff auth keeps its password method"},
		},
		{
			name:     "a Google method beside the password: nothing written, and the password named",
			auths:    []app.Auth{{File: "pkg/auth/staff/staff.go", Methods: []app.Method{password, google}}},
			wantSays: []string{"The staff auth keeps its password method"},
		},
		{
			name:  "a Microsoft method alone: left as its author made it",
			auths: []app.Auth{{File: "pkg/auth/staff/staff.go", Methods: []app.Method{azure}}},
		},
		{
			name:  "an auth whose directory gives the roles: neither a method nor a word",
			auths: []app.Auth{{File: "pkg/auth/fleet/fleet.go", Methods: []app.Method{directory}}},
		},
		{
			name: "two auths, one whose directory gives the roles: the other's methods alone",
			auths: []app.Auth{
				{File: "pkg/auth/fleet/fleet.go", Methods: []app.Method{directory}},
				{File: "pkg/auth/members/members.go", Methods: []app.Method{password}},
			},
			wantSteps:   written("members"),
			wantWritten: []string{"the members auth's google method", "the members auth's azure method"},
			wantSays:    []string{"The members auth registered neither a Google nor a Microsoft method, so the command writes both", "The members auth keeps its password method"},
		},
		{
			name:  "a construction outside an auth package: no auth of the application's",
			auths: []app.Auth{{File: "main.go", Methods: []app.Method{password}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			steps, wrote, says := byDirectorySteps(s, &app.App{Auths: tt.auths})
			if diff := cmp.Diff(tt.wantSteps, steps); diff != "" {
				t.Errorf("byDirectorySteps() steps mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantWritten, wrote); diff != "" {
				t.Errorf("byDirectorySteps() written mismatch (-want +got):\n%s", diff)
			}
			if len(says) != len(tt.wantSays) {
				t.Fatalf("byDirectorySteps() says %q, want %d line(s) starting %q", says, len(tt.wantSays), tt.wantSays)
			}
			for i, want := range tt.wantSays {
				if !strings.HasPrefix(says[i], want) {
					t.Errorf("byDirectorySteps() says[%d] = %q, want it to start %q", i, says[i], want)
				}
			}
		})
	}
}

// Test_methodStartingPoints reads what a starting point says of the method it wrote
// against the application: without tenants an open method gives each new person a role;
// on the auth whose people create a tenant, a person who signs up creates their own;
// and on another auth of an application with tenants no method is ever opened, which
// the starting point says in place of where Open is chosen.
func Test_methodStartingPoints(t *testing.T) {
	t.Parallel()

	solo, outlets := renderedDir(t, skeleton.Base), renderedDir(t, transition_.ReferenceCandidate)
	google := authpkg.Method{Kind: app.MethodGoogle, Name: "google", Slot: authpkg.SlotAny, Authority: app.AuthorityApplication}
	open := methodsShape(t, "open-sign-up")
	const (
		role    = "and from then anyone can sign up, and gets the role the administrator names."
		creates = "and from then a person who signs up through it holds no tenant and creates their own."
		never   = "It stays invite only in this application, which has tenants: this auth's people create no tenant of their own, so no method of it is opened to sign-up"
	)
	tests := []struct {
		name  string
		start func() (*startingPoint, error)
		want  string
	}{
		{name: "add method on an application without tenants", start: func() (*startingPoint, error) { return methodStartingPoint(open, solo, "staff", &google) }, want: role},
		{name: "add method on the auth whose people create a tenant", start: func() (*startingPoint, error) { return methodStartingPoint(open, outlets, "staff", &google) }, want: creates},
		{name: "add method on another auth of an application with tenants", start: func() (*startingPoint, error) { return methodStartingPoint(open, outlets, "members", &google) }, want: never},
		{name: "add auth on an application without tenants", start: func() (*startingPoint, error) {
			return authStartingPoint(open, solo, app.DefaultOutletName, []authpkg.Method{google})
		}, want: role},
		{name: "add auth bound to the outlet that serves the tenant record", start: func() (*startingPoint, error) {
			return authStartingPoint(open, outlets, app.DefaultOutletName, []authpkg.Method{{Kind: app.MethodPassword}, google})
		}, want: creates},
		{name: "add auth bound to another outlet of an application with tenants", start: func() (*startingPoint, error) {
			return authStartingPoint(open, outlets, "portal", []authpkg.Method{{Kind: app.MethodPassword}, google})
		}, want: never},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			start, err := tt.start()
			if err != nil {
				t.Fatalf("the starting point error = %v", err)
			}
			if !slices.ContainsFunc(start.says, func(line string) bool { return strings.Contains(line, tt.want) }) {
				t.Errorf("the starting point says %q, want a line saying %q", start.says, tt.want)
			}
		})
	}

	// Open sign-up on an auth with no password method, where the application's tenants
	// have directories, is a starting point like any other: the auth's first administrator
	// comes in by an invitation, which ties the person's first sign-in through the
	// directory to the roles however many organizations the method admits.
	if _, err := authStartingPoint(open, outlets, app.DefaultOutletName, []authpkg.Method{google}); err != nil {
		t.Errorf("add auth with a directory method alone under Open sign-up, where tenants have directories: error = %v, want the starting point", err)
	}

	// Under Directory members add auth says the password method beside the directory
	// method is left invite only.
	members := methodsShape(t, "directory-members")
	workspace := authpkg.Method{Kind: app.MethodGoogle, Name: "workspace", Authority: app.AuthorityApplication}
	start, err := authStartingPoint(members, solo, app.DefaultOutletName, []authpkg.Method{{Kind: app.MethodPassword}, workspace})
	if err != nil {
		t.Fatal(err)
	}
	if want := "The password method beside it is left as it is: invite only."; len(start.says) != 2 || start.says[0] != want {
		t.Errorf("the starting point says %q, want the password method's line %q first", start.says, want)
	}
}

// Test_startingPoint_write pins what a command given --shape prints beside its change:
// the shape by number and name with what it filled in.
func Test_startingPoint_write(t *testing.T) {
	t.Parallel()

	start := &startingPoint{shape: *methodsShape(t, "open-sign-up"), says: []string{"The google method admits any Google account, and the application gives the roles."}}
	var out strings.Builder
	start.write(&out)
	if want := "Starting point: 4 Open sign-up.\n  - The google method admits any Google account, and the application gives the roles.\n\n"; out.String() != want {
		t.Errorf("write() mismatch (-want +got):\n%s", cmp.Diff(want, out.String()))
	}
}
