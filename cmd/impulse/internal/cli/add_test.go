package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
	"github.com/impulseframework/impulse/cmd/impulse/internal/check"
	transition_ "github.com/impulseframework/impulse/cmd/impulse/internal/transition"
)

// generalStep is a transition whose account of itself is the same for every
// application.
type generalStep struct{}

// Validate refuses nothing.
func (generalStep) Validate(*app.App) error {
	return nil
}

// Apply changes nothing.
func (generalStep) Apply(context.Context, *app.App, check.Execer) (*transition_.Change, error) {
	return &transition_.Change{}, nil
}

// Meaning is the general account.
func (generalStep) Meaning() string {
	return "the general account"
}

// readingStep is a transition that also writes its account for the application it was
// applied to.
type readingStep struct {
	generalStep
}

// MeaningIn is the account written for the application, which names its root.
func (readingStep) MeaningIn(a *app.App) string {
	return "the account of " + a.Root
}

// Test_meaningOf holds the brief to the account a transition writes for the application
// it was applied to, where the transition writes one: what is left of a change can
// depend on how the application's people sign in.
func Test_meaningOf(t *testing.T) {
	t.Parallel()

	a := &app.App{Root: "/srv/depot"}
	if got, want := meaningOf(generalStep{}, a), "the general account"; got != want {
		t.Errorf("meaningOf() = %q, want %q", got, want)
	}
	if got, want := meaningOf(readingStep{}, a), "the account of /srv/depot"; got != want {
		t.Errorf("meaningOf() = %q, want %q: the transition writes an account for the application", got, want)
	}
	// Tenancy is such a transition.
	if _, ok := transition(transition_.Tenancy{}).(interface{ MeaningIn(a *app.App) string }); !ok {
		t.Error("the tenancy step writes no account for the application it was applied to")
	}
}

// Test_parseMethods reads add auth's --method flags: the password method when none is
// given, a registration named after its kind when a Microsoft, Google or persona method
// gives none, and an identity provider under the name the application gives it, which has
// no default.
func Test_parseMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		flags   []string
		want    []authpkg.Method
		wantErr string
	}{
		{name: "no flag is the password method", want: []authpkg.Method{{Kind: app.MethodPassword}}},
		{name: "a directory named after its kind", flags: []string{"google"}, want: []authpkg.Method{{Kind: app.MethodGoogle, Name: "google"}}},
		{
			name:  "the password method beside an identity provider under its name",
			flags: []string{"password", "oidc:okta"},
			want:  []authpkg.Method{{Kind: app.MethodPassword}, {Kind: app.MethodOIDC, Name: "okta"}},
		},
		{name: "an identity provider without a name", flags: []string{"oidc"}, wantErr: "an identity provider's registration needs a name, oidc:<registration>"},
		{name: "the password method under a name", flags: []string{"password:staff"}, wantErr: "the password method takes no registration name"},
		{name: "a method impulse does not compose", flags: []string{"ldap:directory"}, wantErr: "password, azure:<registration>, google:<registration>, oidc:<registration>, or persona"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseMethods(tt.flags)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseMethods() error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("parseMethods() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("parseMethods() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Test_addMethod_identityProviderName refuses an identity provider added without a
// registration name before anything is asked or read: the name is the application's word
// for the provider, so the kind's own name does not stand in for it.
func Test_addMethod_identityProviderName(t *testing.T) {
	t.Parallel()

	cmd := newAddMethod()
	cmd.SetArgs([]string{"staff", "oidc", "--authority", "application"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.Execute()
	if want := "the oidc method needs a registration name"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("Execute() error = %v, want one containing %q", err, want)
	}
}

// Test_addMethod_label holds --label to the one kind that takes it, before anything is
// asked or read: the words are an organization's identity provider's, a flag given with
// no words is refused, and every other kind is told where its words are changed.
func Test_addMethod_label(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "words for a Google method", args: []string{"staff", "google", "--authority", "application", "--label", "Lakeside"}, want: `the google method is shown by what it is. To show it under other words, chain .Labeled("Lakeside") on its declaration in the generator program`},
		{name: "words for the password method", args: []string{"staff", "password", "--label", "Lakeside"}, want: "the password method is shown by what it is"},
		{name: "words for a method impulse does not compose", args: []string{"staff", "ldap", "--label", "Lakeside"}, want: `"ldap" is not a sign-in method impulse composes`},
		{name: "the flag with no words", args: []string{"staff", "oidc", "company-sso", "--authority", "application", "--label", ""}, want: "--label takes the words people read for the provider"},
		{name: "the flag with spaces alone", args: []string{"staff", "oidc", "company-sso", "--authority", "application", "--label", "  "}, want: "--label takes the words people read for the provider"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := newAddMethod()
			cmd.SetArgs(tt.args)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Execute() error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

// Test_methodSpec_label holds what the method carries from --label: the words given for
// an organization's identity provider, and none where the flag was not given, so the
// declaration is written as it was before the flag existed.
func Test_methodSpec_label(t *testing.T) {
	t.Parallel()

	said := newAddMethod()
	if err := said.Flags().Set("label", "Lakeside ID"); err != nil {
		t.Fatalf("Flags().Set() error = %v", err)
	}
	got, err := methodSpec(said, "oidc", "company-sso", "", "application", "Lakeside ID", false, nil)
	if err != nil {
		t.Fatalf("methodSpec() with words error = %v", err)
	}
	if want := (authpkg.Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityApplication, Label: "Lakeside ID"}); got != want {
		t.Errorf("methodSpec() with words = %+v, want %+v", got, want)
	}
	if want := `generation.OIDCMethod("company-sso").Labeled("Lakeside ID")`; got.Declaration() != want {
		t.Errorf("the declaration with words = %s, want %s", got.Declaration(), want)
	}

	got, err = methodSpec(newAddMethod(), "oidc", "company-sso", "", "application", "", false, nil)
	if err != nil {
		t.Fatalf("methodSpec() with no words error = %v", err)
	}
	if want := (authpkg.Method{Kind: app.MethodOIDC, Name: "company-sso", Authority: app.AuthorityApplication}); got != want {
		t.Errorf("methodSpec() with no words = %+v, want %+v", got, want)
	}
	if want := `generation.OIDCMethod("company-sso")`; got.Declaration() != want {
		t.Errorf("the declaration with no words = %s, want %s: it is written as before", got.Declaration(), want)
	}
}

// Test_add_accountsRetired refuses --accounts on add auth and add method before anything
// is asked, read or written, whatever value it carries, and says where the choice is made
// now: in the running application, for each method, on its Sign-in page. The flag stays
// out of the help.
func Test_add_accountsRetired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
	}{
		{name: "add auth with the rule that was the default", cmd: newAddAuth, args: []string{"partners", "--method", "google:workspace", "--authority", "application", "--outlet", "portal", "--accounts", "members"}},
		{name: "add auth opening the password method", cmd: newAddAuth, args: []string{"partners", "--outlet", "portal", "--accounts", "open"}},
		{name: "add method inviting", cmd: newAddMethod, args: []string{"staff", "google", "--authority", "application", "--accounts", "invited"}},
		{name: "add method with a value nobody defined", cmd: newAddMethod, args: []string{"staff", "password", "--accounts=anyone"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := tt.cmd()
			cmd.SetArgs(tt.args)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			err := cmd.Execute()
			if err == nil {
				t.Fatal("Execute() error = nil, want --accounts refused")
			}
			for _, want := range []string{
				"--accounts is no longer an option",
				"chosen in the running application, for each method",
				"Every method starts invite only",
				"on the application's Sign-in page",
				"Remove --accounts and run the command again",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Execute() error = %q, want it to contain %q", err, want)
				}
			}
			if usage := cmd.UsageString(); strings.Contains(usage, "--accounts") {
				t.Errorf("the usage offers --accounts:\n%s", usage)
			}
		})
	}
}
