package access

import (
	"strings"
	"testing"
)

// TestValidateRoles_authName pins that a role file is handed over under a name the
// collection's generation knew: the names its sets are labeled for. Under any other
// name no set is the file's own, so the check would look at none of its auth's sets and
// say nothing; the file is refused instead, with the name given and the names known. A
// collection that knows no auth accepts any name, and an empty name is always accepted.
func TestValidateRoles_authName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		auths   []string
		auth    string
		wantErr string
	}{
		{
			name:    "the name of the auth's cookie, where it is not the auth's name",
			auths:   []string{"members", "staff"},
			auth:    "__Host-staff",
			wantErr: `the role file is handed over as the roles of "__Host-staff", and the application's generation knew no auth of that name: the auths it knew are members, staff. Hand over the auth's name as generation knows it, which is the name of the auth's package and the name a @subjectSet says in its auth: part`,
		},
		{
			name:    "the name of an auth the collection's generation did not know",
			auths:   []string{"staff"},
			auth:    "members",
			wantErr: `the role file is handed over as the roles of "members", and the application's generation knew no auth of that name: the auths it knew are staff.`,
		},
		{
			name:    "the auth's name in another case",
			auths:   []string{"members", "staff"},
			auth:    "Staff",
			wantErr: `the role file is handed over as the roles of "Staff"`,
		},
		{name: "a name generation knew", auths: []string{"members", "staff"}, auth: "members"},
		{name: "an empty name", auths: []string{"members", "staff"}},
		{name: "a collection that knows no auth accepts any name", auth: "__Host-staff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := ValidateRoles(setsCollection{auths: tt.auths}, &RoleConfig{Roles: ScopedRoles{Domain: []*Role{readsNoSet()}}}, tt.auth)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateRoles() error = %v, want none", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateRoles() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestNew_authName pins that New holds the name WithDefaultRoles hands over to the same
// rule, and says the name is what it refuses, not the role file.
func TestNew_authName(t *testing.T) {
	t.Parallel()

	collection := setsCollection{auths: []string{"members", "staff"}}
	tests := []struct {
		name    string
		auth    string
		wantErr []string
	}{
		{name: "a name generation knew", auth: "staff"},
		{
			name: "the name of the auth's cookie, where it is not the auth's name",
			auth: "__Host-staff",
			wantErr: []string{
				"access.WithDefaultRoles()",
				`the role file is handed over as the roles of "__Host-staff", and the application's generation knew no auth of that name: the auths it knew are members, staff.`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := New(newFakeStore(), collection, WithDefaultRoles(grammarRoleFile, tt.auth))
			if client != nil {
				t.Cleanup(func() {
					if err := client.Close(); err != nil {
						t.Errorf("Client.Close() error = %v", err)
					}
				})
			}
			if len(tt.wantErr) == 0 {
				if err != nil {
					t.Fatalf("New() error = %v, want none", err)
				}

				return
			}
			for _, want := range tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("New() error = %v, want it to contain %q", err, want)
				}
			}
			if err != nil && strings.Contains(err.Error(), "the role file does not validate") {
				t.Errorf("New() error = %v, want it to refuse the name and say nothing against the role file", err)
			}
		})
	}
}
