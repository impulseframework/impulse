package shape

import (
	"testing"

	"github.com/impulseframework/impulse/app"
)

// TestReading_judgeTenants names the shape of an auth in an application with tenants,
// from the three things the code says: whether a person belongs to one tenant, whether
// the tenants can have directories, and whether a method of the auth goes through an
// organization's directory with the application giving the roles. Where the tenants
// have no directories the shape is the invite-only one whatever the auth's methods are,
// said in one sentence that a method through a directory extends and never changes.
func TestReading_judgeTenants(t *testing.T) {
	t.Parallel()

	var (
		password = app.Method{Kind: app.MethodPassword, Name: "password"}
		google   = app.Method{Kind: app.MethodGoogle, Name: "google", Authority: app.AuthorityApplication}
		entra    = app.Method{Kind: app.MethodAzure, Name: "entra", Authority: app.AuthorityApplication}
		okta     = app.Method{Kind: app.MethodOIDC, Name: "okta", Authority: app.AuthorityApplication}
		persona  = app.Method{Kind: app.MethodPersona, Name: "persona", Authority: app.AuthorityApplication}
	)
	const (
		none     = "The tenants have no directories (the tenant record declares none), so a tenant's people come by invitation alone"
		declared = "The tenants can have directories, but no method of this auth goes through an organization's directory, so its people come by invitation alone; a directory method whose roles the application gives (impulse add method) makes it "
		chosen   = " Whether a tenant takes a directory, and whether that directory's people get an account without an invitation, is each tenant's choice, made while the application runs on the tenant's Sign-in page."
	)
	tests := []struct {
		name        string
		directories bool
		oneTenant   bool
		methods     []app.Method
		wantShape   string
		wantSays    string
	}{
		{name: "no directories, a password alone", methods: []app.Method{password}, wantShape: "global-users-invite-only", wantSays: none + "."},
		{name: "no directories, one tenant, a password alone", oneTenant: true, methods: []app.Method{password}, wantShape: "tenant-owned-users-invite-only", wantSays: none + "."},
		{
			name: "no directories, a Google method", methods: []app.Method{password, google},
			wantShape: "global-users-invite-only", wantSays: none + ", and the google method is a way for an invited person to sign in and nothing more.",
		},
		{
			name: "no directories, one tenant, a Google method", oneTenant: true, methods: []app.Method{password, google},
			wantShape: "tenant-owned-users-invite-only", wantSays: none + ", and the google method is a way for an invited person to sign in and nothing more.",
		},
		{
			name: "no directories, three directory methods", oneTenant: true, methods: []app.Method{google, entra, okta},
			wantShape: "tenant-owned-users-invite-only", wantSays: none + ", and the google, entra and okta methods are each a way for an invited person to sign in and nothing more.",
		},
		{name: "no directories, the persona method is no organization's", methods: []app.Method{password, persona}, wantShape: "global-users-invite-only", wantSays: none + "."},
		{
			name: "directories, a password alone", directories: true, methods: []app.Method{password},
			wantShape: "global-users-invite-only", wantSays: declared + "8 Global users, each tenant chooses.",
		},
		{
			name: "directories, one tenant, a password alone", directories: true, oneTenant: true, methods: []app.Method{password},
			wantShape: "tenant-owned-users-invite-only", wantSays: declared + "6 Tenant-owned users, by directory.",
		},
		{
			name: "directories, a Google method", directories: true, methods: []app.Method{password, google},
			wantShape: "global-users-each-tenant-chooses", wantSays: "A tenant's people can come through its organization's directory (the google method)." + chosen,
		},
		{
			name: "directories, one tenant, a Google method", directories: true, oneTenant: true, methods: []app.Method{password, google},
			wantShape: "tenant-owned-users-by-directory", wantSays: "A tenant's people can come through its organization's directory (the google method)." + chosen,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reading := &Reading{Tenants: &app.TenantRecord{Name: "Tenant"}}
			if tt.directories {
				reading.Directories = &app.DirectoryRecord{Name: "TenantDirectory"}
			}
			auth := &Auth{Name: "staff"}
			if tt.oneTenant {
				auth.OneTenant = "pkg/auth/staff/staff.go:400"
			}
			reading.judgeTenants(auth, tt.methods)
			if auth.Shape == nil || auth.Shape.Key != tt.wantShape {
				t.Fatalf("judgeTenants() named %v, want %s", auth.Shape, tt.wantShape)
			}
			if auth.Says != tt.wantSays {
				t.Errorf("judgeTenants() says %q, want %q", auth.Says, tt.wantSays)
			}
		})
	}
}
