package cli

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
	"github.com/impulseframework/impulse/cmd/impulse/internal/authpkg"
)

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
