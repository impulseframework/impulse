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
	{name: "impulse new", cmd: newNew, args: []string{"harbor", "--module", "example.com/harbor", "--auth", "staff"}, knows: "tenant-owned-users-invite-only, tenant-owned-users-by-directory, global-users-invite-only, global-users-each-tenant-chooses"},
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
// settles whether a person belongs to one tenant: the two tenant-owned shapes hold a
// person to one, and the transition the creation composes carries it.
func Test_settleShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		shape    string
		wantOne  bool
		wantSays string
	}{
		{shape: "tenant-owned-users-invite-only", wantOne: true, wantSays: "A tenant's people come by invitation alone"},
		{shape: "tenant-owned-users-by-directory", wantOne: true, wantSays: "impulse add method staff google --slot any-workspace --authority application"},
		{shape: "global-users-invite-only", wantSays: "A tenant's people come by invitation alone"},
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
			transitions, err := nf.opts.transitions()
			if err != nil {
				t.Fatal(err)
			}
			want := []transition{transition_.Tenancy{Table: "Tenants", OneTenantPerPerson: tt.wantOne}}
			if diff := cmp.Diff(want, transitions); diff != "" {
				t.Errorf("the composed transitions mismatch (-want +got):\n%s", diff)
			}
			if says := strings.Join(start.TenantsSay([]string{"staff"}, nil), " "); !strings.Contains(says, tt.wantSays) {
				t.Errorf("the starting point says %q, want it to say %q", says, tt.wantSays)
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
	if start, err := nf.opts.settleShape(cmd); err != nil || start != nil || !nf.opts.oneTenant {
		t.Errorf("settleShape() = %v, %v with oneTenant %v; want no shape, no error and one tenant per person", start, err, nf.opts.oneTenant)
	}
	if got, want := nf.opts.describe(), "tenancy (Tenants, one tenant per person)"; got != want {
		t.Errorf("describe() = %q, want %q", got, want)
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
// is taken: the invite-only ones are the application as it is, and the other two say
// which impulse add method adds the directory method a tenant's people come through. On
// an application that already registers a method through an organization's directory,
// an invite-only shape is refused, naming the method, since with tenants the
// application would be the shape beside it.
func Test_tenancyStartingPoint(t *testing.T) {
	t.Parallel()

	solo, withGoogle := renderedDir(t, skeleton.Base), renderedDir(t, transition_.TenancyReferenceCandidate)
	tests := []struct {
		shape    string
		wantSays string
		// refused is the refusal on the application with the Google method.
		refused string
	}{
		{
			shape: "tenant-owned-users-invite-only", wantSays: "A directory method added later with the application giving the roles (impulse add method) makes the application 6 Tenant-owned users, by directory.",
			refused: "--shape tenant-owned-users-invite-only: in Tenant-owned users, invite only a tenant's people come by invitation alone, and the application registers the staff auth's google method (pkg/auth/staff/google.go:N), through which a tenant's people could come by their organization's directory, so with tenants it would be 6 Tenant-owned users, by directory. Take --shape tenant-owned-users-by-directory, or run the command without --shape",
		},
		{shape: "tenant-owned-users-by-directory", wantSays: "impulse add method staff azure --slot any --authority application for Microsoft Entra. Until then impulse audit reads the application as 5 Tenant-owned users, invite only."},
		{
			shape: "global-users-invite-only", wantSays: "makes the application 8 Global users, each tenant chooses.",
			refused: "--shape global-users-invite-only: in Global users, invite only a tenant's people come by invitation alone, and the application registers the staff auth's google method (pkg/auth/staff/google.go:N), through which a tenant's people could come by their organization's directory, so with tenants it would be 8 Global users, each tenant chooses. Take --shape global-users-each-tenant-chooses, or run the command without --shape",
		},
		{shape: "global-users-each-tenant-chooses", wantSays: "Until then impulse audit reads the application as 7 Global users, invite only."},
	}
	for _, tt := range tests {
		t.Run(tt.shape, func(t *testing.T) {
			t.Parallel()

			s := tenantsShape(t, tt.shape)
			start, err := tenancyStartingPoint(s, solo)
			if err != nil {
				t.Fatalf("tenancyStartingPoint() error = %v", err)
			}
			if says := strings.Join(start.says, " "); start.shape != *s || !strings.Contains(says, tt.wantSays) {
				t.Errorf("the starting point is %s and says %q, want %s saying %q", start.shape, says, s, tt.wantSays)
			}

			start, err = tenancyStartingPoint(s, withGoogle)
			if tt.refused != "" {
				if err == nil || positionRE.ReplaceAllString(errors.Cause(err).Error(), ".go:N") != tt.refused {
					t.Fatalf("tenancyStartingPoint() over a directory method error = %v, want %q", err, tt.refused)
				}

				return
			}
			if err != nil {
				t.Fatalf("tenancyStartingPoint() over a directory method error = %v", err)
			}
			if want, says := "A tenant's people can come through its organization's directory, by the staff auth's google method (pkg/auth/staff/google.go:N)", positionRE.ReplaceAllString(strings.Join(start.says, " "), ".go:N"); !strings.Contains(says, want) {
				t.Errorf("the starting point says %q, want it to say %q", says, want)
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
