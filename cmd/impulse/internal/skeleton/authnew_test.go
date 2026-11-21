package skeleton

import (
	"context"
	"strings"
	"testing"

	outletsmembers "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/auth/members"
	outletsstaff "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/outlets/pkg/auth/staff"
	sitesstaff "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/sites/pkg/auth/staff"
	solostaff "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/solo/pkg/auth/staff"
	tenantedstaff "github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/pkg/auth/staff"
)

// TestAuthNew_refusesAMissingCollection pins that a generated auth refuses settings that
// leave the permission collection out, as it refuses each of its other required settings:
// with a sentence that names the auth's own setting and says what to pass. The settings
// are a struct, so leaving the collection out compiles, and without the refusal the
// permission engine's own (access.New) is what a person reads, which names neither the
// setting nor the union an application with several sites passes.
//
// The auths are the candidates', which TestAuthPackages holds to the template, so this
// is the template's New in each of its shapes.
func TestAuthNew_refusesAMissingCollection(t *testing.T) {
	t.Parallel()

	const says = ".Settings.Collection is nil: the permission engine holds every grant to the permission collection the release declares; pass the generated router's Collection(), or where the application has several sites the union of theirs (the configuration's Collection(), over access.UnionCollection)"
	tests := []struct {
		name    string
		auth    string
		refusal func(ctx context.Context) (built bool, err error)
	}{
		{name: "solo staff", auth: "staff", refusal: func(ctx context.Context) (bool, error) {
			a, err := solostaff.New(ctx, nil, &solostaff.Settings{})

			return a != nil, err
		}},
		{name: "tenanted staff", auth: "staff", refusal: func(ctx context.Context) (bool, error) {
			a, err := tenantedstaff.New(ctx, nil, &tenantedstaff.Settings{})

			return a != nil, err
		}},
		{name: "sites staff", auth: "staff", refusal: func(ctx context.Context) (bool, error) {
			a, err := sitesstaff.New(ctx, nil, &sitesstaff.Settings{})

			return a != nil, err
		}},
		{name: "outlets staff", auth: "staff", refusal: func(ctx context.Context) (bool, error) {
			a, err := outletsstaff.New(ctx, nil, &outletsstaff.Settings{})

			return a != nil, err
		}},
		{name: "outlets members", auth: "members", refusal: func(ctx context.Context) (bool, error) {
			a, err := outletsmembers.New(ctx, nil, &outletsmembers.Settings{})

			return a != nil, err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			built, err := tt.refusal(t.Context())
			if want := tt.auth + says; err == nil || !strings.HasSuffix(err.Error(), want) {
				t.Fatalf("New() error = %v, want one ending %q", err, want)
			}
			if built {
				t.Error("New() returned an auth beside the error, want none")
			}
		})
	}
}
