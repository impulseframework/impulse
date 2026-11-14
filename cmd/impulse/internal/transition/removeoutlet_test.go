package transition

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

// outletMembers are resource files on the outlets: one on both, one on portal alone.
var outletMembers = map[string]string{
	"pkg/resources/announcements.go": "package resources\n\n// Announcement is read on both surfaces.\n// @outlet(default, portal)\ntype Announcement struct{}\n",
	"pkg/resources/readings.go":      "package resources\n\n// Reading is the portal's alone.\n// @outlet(portal)\ntype Reading struct{}\n",
	".envrc.template":                "export PORT=8090\n# The portal's built bundle.\n# export APP_PORTAL_DIST=web/dist/portal\n",
}

func TestRemoveOutletValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outlet  string
		wantErr string
	}{
		{name: "a declared outlet", outlet: "portal"},
		{name: "an outlet no program declares", outlet: "kiosk", wantErr: "no generator program declares an outlet named kiosk"},
		{name: "the default outlet", outlet: "default", wantErr: `outlet name "default"`},
		{name: "a name that is not an identifier", outlet: "my-portal", wantErr: `outlet name "my-portal"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := beacon(t, nil)
			if _, err := (Outlet{Name: "portal", Prefix: "portal/api", Sessions: true}).Apply(t.Context(), a, &fakeExec{}); err != nil {
				t.Fatal(err)
			}
			err := RemoveOutlet{Name: tt.outlet}.Validate(mustDiscover(t, a.Root))
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("Validate() error = %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestRemoveOutletRetiresIdentity pins what removing an outlet says where the program
// generates each auth's management: an outlet that was the one declaration of its auth
// leaves that auth's generated packages for the regeneration to remove, and the code
// naming them is named as the agent's; an outlet whose auth another outlet declares
// leaves the management standing and says nothing.
func TestRemoveOutletRetiresIdentity(t *testing.T) {
	t.Parallel()

	withIdentity := strings.Replace(authRouterProgram, "\t\tgeneration.GenerateRouter(),\n", "\t\tgeneration.GenerateRouter(),\n\t\tgeneration.GenerateIdentity(\"pkg/identity\"),\n", 1)
	const line = "no outlet declares the members auth any longer, so the regeneration removes its generated management under pkg/identity (membersidentity, membershandlers and internal/membersresources); remove what names it, which no longer compiles: the client the auth's package builds in New with the hooks it sets from it and its Identity method (or the package, where nothing signs in through the auth any longer), and MembersIdentity() on the Configurer, on the App and on every test configurer"
	tests := []struct {
		name    string
		program string
		want    bool
	}{
		{
			name:    "the outlet was the one declaration of its auth",
			program: strings.Replace(withIdentity, "generation.WithRouterOutlet(\"portal\", \"portal/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/staff\"", "generation.WithRouterOutlet(\"portal\", \"portal/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/members\"", 1),
			want:    true,
		},
		{name: "the default outlet declares the same auth", program: withIdentity},
		{
			name:    "the program generates no management",
			program: strings.Replace(authRouterProgram, "generation.WithRouterOutlet(\"portal\", \"portal/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/staff\"", "generation.WithRouterOutlet(\"portal\", \"portal/api\",\n\t\t\tgeneration.Auth(\"example.com/acme/beacon/pkg/auth/members\"", 1),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := authFiles(t)
			files[programFile] = tt.program
			a := beacon(t, files)
			ch, err := RemoveOutlet{Name: "portal"}.Apply(t.Context(), a, &fakeExec{})
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			got := slices.Contains(ch.Skipped, programFile+": "+line)
			if got != tt.want {
				t.Errorf("Skipped names the retired management = %v, want %v; got:\n%s", got, tt.want, strings.Join(ch.Skipped, "\n"))
			}
		})
	}
}

func TestRemoveOutletApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		add         Outlet
		wantDid     []string
		wantSkipped int
		check       func(t *testing.T, a *app.App)
	}{
		{
			name: "a session outlet, back to the application before it",
			add:  Outlet{Name: "portal", Prefix: "portal/api", Sessions: true},
			wantDid: []string{
				`cmd/generate/resourcegenerator/main.go: removed WithRouterOutlet("portal", ...)`,
				`cmd/generate/resourcegenerator/main.go: removed GenerateTypescript("web/portal/src/app/core/service", ForOutlet("portal"), ...)`,
				"deleted the portal browser project, web/portal, with the portal outlet's generated client",
				"web/angular.json: removed the portal project",
				"web/package.json: removed the portal project's scripts and its part of build, lint, and test",
				"Procfile: removed the portal process; the comments that describe it are still there",
				"pkg/resources/announcements.go: Announcement stay on their other outlets; portal is out of their @outlet lists",
				"ran go generate ./..., which regenerated without the portal outlet",
			},
			wantSkipped: 2, // Reading on the outlet alone, and the environment template's line
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				for rel, want := range map[string]string{"web/angular.json": beaconAngular, "web/package.json": beaconPackage, "Procfile": beaconProcfile} {
					if diff := cmp.Diff(want, read(t, a, rel)); diff != "" {
						t.Errorf("%s is not back to what it was (-want +got):\n%s", rel, diff)
					}
				}
				if _, err := os.Stat(a.Abs("web/portal")); !os.IsNotExist(err) {
					t.Errorf("web/portal still exists (%v)", err)
				}
				var workspace struct{ Projects map[string]any }
				if err := json.Unmarshal([]byte(read(t, a, "web/angular.json")), &workspace); err != nil {
					t.Fatalf("angular.json is not JSON: %v", err)
				}
				if got := read(t, a, "pkg/resources/announcements.go"); !strings.Contains(got, "// @outlet(default)\n") {
					t.Errorf("announcements.go = %q", got)
				}
				if got := read(t, a, "pkg/resources/readings.go"); !strings.Contains(got, "// @outlet(portal)\n") {
					t.Errorf("readings.go = %q: a struct on the outlet alone is the agent's decision", got)
				}
			},
		},
		{
			name: "a machine outlet",
			add:  Outlet{Name: "machines", Prefix: "machines"},
			wantDid: []string{
				`cmd/generate/resourcegenerator/main.go: removed WithRouterOutlet("machines", ...)`,
				"ran go generate ./..., which regenerated without the machines outlet",
			},
			check: func(t *testing.T, a *app.App) {
				t.Helper()
				if diff := cmp.Diff(beaconAngular, read(t, a, "web/angular.json")); diff != "" {
					t.Errorf("angular.json changed:\n%s", diff)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := beacon(t, outletMembers)
			if _, err := tt.add.Apply(t.Context(), a, &fakeExec{}); err != nil {
				t.Fatalf("add: %v", err)
			}
			exec := &fakeExec{}
			ch, err := RemoveOutlet{Name: tt.add.Name}.Apply(t.Context(), mustDiscover(t, a.Root), exec)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantDid, ch.Did); diff != "" {
				t.Errorf("Did diff (-want +got):\n%s", diff)
			}
			if len(ch.Skipped) != tt.wantSkipped {
				t.Errorf("Skipped = %q, want %d", ch.Skipped, tt.wantSkipped)
			}
			if diff := cmp.Diff(beaconProgram, read(t, a, "cmd/generate/resourcegenerator/main.go")); diff != "" {
				t.Errorf("the program is not back to what it was (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(regenerated, exec.calls); diff != "" {
				t.Errorf("exec calls mismatch (-want +got):\n%s", diff)
			}
			p := mustDiscover(t, a.Root).Profile()
			if len(p.OutletNames()) != 0 {
				t.Errorf("outlets still declared: %v", p.OutletNames())
			}
			tt.check(t, a)
		})
	}
}

func TestRemoveOutletTakesTheIntroducingComment(t *testing.T) {
	t.Parallel()

	program := strings.Replace(beaconProgram,
		"\t\tgeneration.GenerateRoutes(\"pkg/router\", \"api\"),\n",
		"\t\tgeneration.GenerateRoutes(\"pkg/router\", \"api\"),\n\t\t// The machines outlet is the machine REST API: structs annotated with @outlet\n\t\t// naming machines are served under /machines.\n\t\tgeneration.WithRouterOutlet(\"machines\", \"machines\"),\n", 1)
	a := beacon(t, map[string]string{"cmd/generate/resourcegenerator/main.go": program})
	if _, err := (RemoveOutlet{Name: "machines"}).Apply(t.Context(), a, &fakeExec{}); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if diff := cmp.Diff(beaconProgram, read(t, a, "cmd/generate/resourcegenerator/main.go")); diff != "" {
		t.Errorf("program diff (-want +got):\n%s", diff)
	}
}
