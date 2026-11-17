package signin

import (
	"testing"

	"github.com/impulseframework/impulse/session/sessioninfo"
)

func TestDirectory_Organization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    Directory
		// wantPersonal is PersonalAccounts, wantOrganization is Organization.
		wantPersonal     bool
		wantOrganization bool
	}{
		{name: "a Google Workspace by its domain is an organization's", d: Directory{Method: sessioninfo.MethodGoogle, Connection: "acme.example"}, wantOrganization: true},
		{name: "a Microsoft Entra tenant by its id is an organization's", d: Directory{Method: sessioninfo.MethodAzure, Connection: "6f2c1b7e-3d4a-4c1e-9b2a-1f0e8d7c6b5a"}, wantOrganization: true},
		{name: "a WorkOS connection is an organization's", d: Directory{Method: sessioninfo.MethodWorkOS, Connection: "conn_01"}, wantOrganization: true},
		{name: "any Google account is everyone who is in no Workspace", d: Directory{Method: sessioninfo.MethodGoogle, Connection: GoogleAnyAccountConnection}, wantPersonal: true},
		{name: "Microsoft's consumer tenant is every personal account", d: Directory{Method: sessioninfo.MethodAzure, Connection: MicrosoftPersonalAccountsConnection}, wantPersonal: true},
		{name: "the consumer tenant is the same tenant in capitals", d: Directory{Method: sessioninfo.MethodAzure, Connection: "9188040D-6C67-4C5B-B112-36A304B66DAD"}, wantPersonal: true},
		{name: "the consumer tenant's id under another method names no personal accounts", d: Directory{Method: sessioninfo.MethodWorkOS, Connection: MicrosoftPersonalAccountsConnection}, wantOrganization: true},
		{name: "a password is no directory", d: Directory{Method: sessioninfo.MethodPassword}},
		{name: "an impersonation is no directory", d: Directory{Method: sessioninfo.MethodImpersonation, Connection: "alice@example.com"}},
		{name: "a directory method with no connection names no organization", d: Directory{Method: sessioninfo.MethodGoogle}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.d.PersonalAccounts(); got != tt.wantPersonal {
				t.Errorf("PersonalAccounts() = %v, want %v", got, tt.wantPersonal)
			}
			if got := tt.d.Organization(); got != tt.wantOrganization {
				t.Errorf("Organization() = %v, want %v", got, tt.wantOrganization)
			}
		})
	}
}
