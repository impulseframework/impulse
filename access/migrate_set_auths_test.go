package access

import (
	"fmt"
	"strings"
	"testing"
)

// TestValidateRoles_authName pins that a role file is handed over under a name the
// collection's generation knew: the names its sets are labeled for. Under any other
// name no set is the file's own, so the check would look at none of its auth's sets and
// say nothing; the file is refused instead, with the name given and the names known. A
// collection that knows no auth accepts any name. An empty name is refused where the
// collection knows several auths, since nothing then says whose roles the file's are,
// and accepted where it knows one or none.
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
		{
			name:    "an empty name, where the collection knows several auths",
			auths:   []string{"members", "staff"},
			wantErr: noAuthsNameOfSeveral,
		},
		{name: "an empty name, where the collection knows one auth", auths: []string{"staff"}},
		{name: "an empty name, where the collection knows no auth"},
		{name: "a collection that knows no auth accepts any name", auth: "__Host-staff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := ValidateRoles(setsCollection{auths: tt.auths}, &RoleConfig{Auth: tt.auth, Roles: ScopedRoles{Domain: []*Role{readsNoSet()}}}, tt.auth)
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

// noAuthsNameOfSeveral is the sentence a role file handed over under an empty name is
// refused with by a collection that knows the auths members and staff.
const noAuthsNameOfSeveral = "the role file is handed over under no auth's name, and the application's generation knew several auths: members, staff. A role is held to the sets of its own auth's people, so hand the file over under its auth's name, which is the name of the auth's package and the name a @subjectSet says in its auth: part"

// TestNew_authName pins that New holds the name WithDefaultRoles hands over to the same
// rule, and says the name is what it refuses, not the role file. Where the collection
// knows several auths, an engine is told whose roles it holds: an empty name is
// refused, and so is an engine opened with no role file. Where it knows one auth or
// none, both open.
func TestNew_authName(t *testing.T) {
	t.Parallel()

	several := []string{"members", "staff"}
	tests := []struct {
		name  string
		auths []string
		auth  string
		// noRoleFile opens the engine with no role file, and so with no name.
		noRoleFile bool
		wantErr    []string
	}{
		{name: "a name generation knew", auths: several, auth: "staff"},
		{
			name:  "the name of the auth's cookie, where it is not the auth's name",
			auths: several,
			auth:  "__Host-staff",
			wantErr: []string{
				"access.WithDefaultRoles()",
				`the role file is handed over as the roles of "__Host-staff", and the application's generation knew no auth of that name: the auths it knew are members, staff.`,
			},
		},
		{
			name:    "an empty name, where the collection knows several auths",
			auths:   several,
			wantErr: []string{"access.WithDefaultRoles()", noAuthsNameOfSeveral},
		},
		{
			name:       "no role file, where the collection knows several auths",
			auths:      several,
			noRoleFile: true,
			wantErr: []string{
				"the collection knows several auths (members, staff), and the engine is handed no role file, so it is told none of them. An engine holds the roles of one auth and holds each role's grants to that auth's sets of people: open it with the auth's role file under the auth's name, access.WithDefaultRoles(file, name)",
			},
		},
		{name: "an empty name, where the collection knows one auth", auths: []string{"staff"}},
		{name: "no role file, where the collection knows one auth", auths: []string{"staff"}, noRoleFile: true},
		{name: "an empty name, where the collection knows no auth"},
		{name: "no role file, where the collection knows no auth", noRoleFile: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var opts []Option
			if !tt.noRoleFile {
				opts = append(opts, WithDefaultRoles(readerRoleFile(tt.auth), tt.auth))
			}
			client, err := New(newFakeStore(), setsCollection{auths: tt.auths}, opts...)
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

// Sentences a role file is refused with where the name it carries (its "auth") and the
// name it is handed over under differ, for a file that says "staff" or nothing and a
// name of "members" or nothing.
const (
	fileAuthOtherName = `the role file says it holds the roles of "staff" (its "auth"), and it is handed over as the roles of "members". An engine holds the roles of one auth, and a role file is handed over under the name it carries: hand the members auth its own role file, or correct the file's "auth" if these are the members auth's roles`
	fileAuthNone      = `the role file does not say whose roles it holds, and it is handed over as the roles of "members". A role file is handed over under the name it carries: add "auth": "members" as the file's first key`
	fileAuthUnnamed   = `the role file says it holds the roles of "staff" (its "auth"), and it is handed over under no auth's name: hand it over under the name it carries, access.WithDefaultRoles(file, "staff")`
)

// fileAuthTests are the pairs of a file's "auth" and the name it is handed over under,
// over a collection that knows the auths members and staff, or only staff where an
// empty name is handed over (several auths refuse an empty name before the file is
// read), and the sentence each pair is refused with.
var fileAuthTests = []struct {
	name     string
	fileAuth string
	auth     string
	wantErr  string
}{
	{name: "a file that names no auth, handed over under no name"},
	{name: "a file handed over under the name it carries", fileAuth: "staff", auth: "staff"},
	{name: "a file handed over under another auth's name", fileAuth: "staff", auth: "members", wantErr: fileAuthOtherName},
	{name: "a file that names no auth, handed over under a name", auth: "members", wantErr: fileAuthNone},
	{name: "a file that names an auth, handed over under no name", fileAuth: "staff", wantErr: fileAuthUnnamed},
}

// fileAuthCollection is the collection a fileAuthTests pair is handed to.
func fileAuthCollection(auth string) setsCollection {
	if auth == "" {
		return setsCollection{auths: []string{"staff"}}
	}

	return setsCollection{auths: []string{"members", "staff"}}
}

// authKey is the first line of a role file in a test that carries auth as its "auth",
// and nothing where auth is empty: the file then names no auth.
func authKey(auth string) string {
	if auth == "" {
		return ""
	}

	return fmt.Sprintf("\n  \"auth\": %q,", auth)
}

// readerRoleFile is a role file over setsCollection whose one domain role reads no set,
// carrying auth as its "auth" where auth is not empty.
func readerRoleFile(auth string) RoleFile {
	return RoleFile(`{` + authKey(auth) + `
  "roles": {
    "domain": [
      {"name": "Clerk", "description": "Reads every widget.", "permissions": {"Read": [{"resource": "Widgets"}]}}
    ]
  }
}`)
}

// TestValidateRoles_fileAuth pins that ValidateRoles takes a configuration only under
// the name it carries (RoleConfig.Auth), and holds a nil configuration to no name.
func TestValidateRoles_fileAuth(t *testing.T) {
	t.Parallel()

	for _, tt := range fileAuthTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			config := &RoleConfig{Auth: tt.fileAuth, Roles: ScopedRoles{Domain: []*Role{readsNoSet()}}}
			_, err := ValidateRoles(fileAuthCollection(tt.auth), config, tt.auth)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateRoles() error = %v, want none", err)
				}

				return
			}
			if err == nil || !strings.Contains(err.Error(), "error="+tt.wantErr) {
				t.Fatalf("ValidateRoles() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
	t.Run("a nil configuration, under a name", func(t *testing.T) {
		t.Parallel()

		if _, err := ValidateRoles(setsCollection{auths: []string{"members", "staff"}}, nil, "members"); err != nil {
			t.Fatalf("ValidateRoles() error = %v, want none", err)
		}
	})
}

// TestNew_fileAuth pins that New takes a role file only under the name it carries, and
// says so as a refusal of the file it was handed (access.WithDefaultRoles()).
func TestNew_fileAuth(t *testing.T) {
	t.Parallel()

	for _, tt := range fileAuthTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client, err := New(newFakeStore(), fileAuthCollection(tt.auth), WithDefaultRoles(readerRoleFile(tt.fileAuth), tt.auth))
			if client != nil {
				t.Cleanup(func() {
					if err := client.Close(); err != nil {
						t.Errorf("Client.Close() error = %v", err)
					}
				})
			}
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("New() error = %v, want none", err)
				}

				return
			}
			for _, want := range []string{"error=" + tt.wantErr, "error=access.WithDefaultRoles()"} {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("New() error = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

// TestNew_fileAuthAfterAuthName pins the order of the two refusals of a name: a name
// generation did not know is refused as that, by the sentence it has had all along,
// before the file's "auth" is held to it.
func TestNew_fileAuthAfterAuthName(t *testing.T) {
	t.Parallel()

	want := `the role file is handed over as the roles of "visitors", and the application's generation knew no auth of that name: the auths it knew are members, staff.`
	_, err := New(newFakeStore(), setsCollection{auths: []string{"members", "staff"}}, WithDefaultRoles(readerRoleFile("staff"), "visitors"))
	if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), `(its "auth")`) {
		t.Fatalf("New() error = %v, want it to contain %q and say nothing of the file's auth", err, want)
	}
	_, err = ValidateRoles(setsCollection{auths: []string{"members", "staff"}}, &RoleConfig{Auth: "staff", Roles: ScopedRoles{Domain: []*Role{readsNoSet()}}}, "visitors")
	if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), `(its "auth")`) {
		t.Fatalf("ValidateRoles() error = %v, want it to contain %q and say nothing of the file's auth", err, want)
	}
}

// TestClient_Auth pins that an engine says the auth whose roles it holds: the name its
// role file carries and was handed over under, and nothing for an engine opened under
// no name or with no file. Its checkers answer the same.
func TestClient_Auth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		auths []string
		opts  []Option
		want  string
	}{
		{name: "a file handed over under its name", auths: []string{"members", "staff"}, opts: []Option{WithDefaultRoles(readerRoleFile("staff"), "staff")}, want: "staff"},
		{name: "a file handed over under no name", auths: []string{"staff"}, opts: []Option{WithDefaultRoles(readerRoleFile(""), "")}},
		{name: "no role file", auths: []string{"staff"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := newPolicyClient(t, newFakeStore(), setsCollection{auths: tt.auths}, tt.opts...)
			if got := client.Auth(); got != tt.want {
				t.Errorf("Client.Auth() = %q, want %q", got, tt.want)
			}
			if got := client.ForUser("ada").Auth(); got != tt.want {
				t.Errorf("Client.ForUser().Auth() = %q, want %q", got, tt.want)
			}
			if got := client.ForRole("Clerk").Auth(); got != tt.want {
				t.Errorf("Client.ForRole().Auth() = %q, want %q", got, tt.want)
			}
		})
	}
}
