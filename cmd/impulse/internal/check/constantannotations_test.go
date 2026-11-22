package check

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

func TestConstantAnnotations(t *testing.T) {
	t.Parallel()

	const program = `package main

import (
	"context"

	"github.com/impulseframework/impulse/generation"
)

func main() {
	_, _ = generation.NewResourceGenerator(context.Background(), "pkg/resources", []string{"file://schema"},
		generation.GenerateHandlers("app"),
		generation.WithRPC("pkg/rpc"),
	)
}
`
	const (
		resources  = "package resources\n\nimport \"github.com/impulseframework/impulse/accesstypes\"\n\n// @manualAddResource(Read)\nconst Ledgers accesstypes.Resource = \"Ledgers\"\n\n// @manualAddPermission\nconst Override accesstypes.Permission = \"Override\"\n"
		rpcMethod  = "package rpc\n\nimport \"github.com/impulseframework/impulse/accesstypes\"\n\n// @manualAddResource(Execute)\nconst PingBeacon accesstypes.Resource = \"PingBeacon\"\n"
		route      = "package app\n\nimport \"github.com/impulseframework/impulse/accesstypes\"\n\n// @manualAddPermission\nconst Release accesstypes.Permission = \"Release\"\n"
		registers  = "pkg/rpc/beacon.go:6: constant PingBeacon carries @manualAddResource, which generation reads only on a constant of the resources package (pkg/resources), so here it registers nothing: declare the constant there"
		declares   = "app/routes.go:6: constant Release carries @manualAddPermission, which generation reads only on a constant of the resources package (pkg/resources), so here it declares nothing: declare the constant there"
		failing    = " annotation(s) on a constant outside the resources package"
		passing    = "no @manualAddResource or @manualAddPermission on a constant outside the resources package"
		proseInApp = "package app\n\n// shipsLog serves the log by hand. Its resource is declared in the resources package:\n// @manualAddResource(Read) stands on that constant.\nfunc shipsLog() {}\n"
	)

	tests := []struct {
		name      string
		noProgram bool
		files     map[string]string
		want      Result
	}{
		{
			name:  "annotated constants in the resources package alone pass",
			files: map[string]string{"pkg/resources/manual.go": resources},
			want:  Result{Name: constantAnnotations{}.Name(), Status: Pass, Summary: passing},
		},
		{
			name:  "a registration beside an RPC method fails",
			files: map[string]string{"pkg/resources/manual.go": resources, "pkg/rpc/beacon.go": rpcMethod},
			want:  Result{Name: constantAnnotations{}.Name(), Status: Fail, Summary: "1" + failing, Details: []string{registers}},
		},
		{
			name:  "a declaration beside a hand-written route fails",
			files: map[string]string{"app/routes.go": route},
			want:  Result{Name: constantAnnotations{}.Name(), Status: Fail, Summary: "1" + failing, Details: []string{declares}},
		},
		{
			name:  "both at once are counted, in the scan's order",
			files: map[string]string{"pkg/rpc/beacon.go": rpcMethod, "app/routes.go": route},
			want:  Result{Name: constantAnnotations{}.Name(), Status: Fail, Summary: "2" + failing, Details: []string{declares, registers}},
		},
		{
			name:  "a function's comment of prose passes",
			files: map[string]string{"app/log.go": proseInApp},
			want:  Result{Name: constantAnnotations{}.Name(), Status: Pass, Summary: passing},
		},
		{
			name:      "no generator program skips",
			noProgram: true,
			files:     map[string]string{"app/routes.go": route},
			want:      Result{Name: constantAnnotations{}.Name(), Status: Skip, Summary: "no generator program"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			write := func(rel, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", "module example.com/annotations\n\ngo 1.26.6\n")
			if !tt.noProgram {
				write("cmd/generate/main.go", program)
			}
			for rel, content := range tt.files {
				write(rel, content)
			}

			a, err := app.Discover(root)
			if err != nil {
				t.Fatalf("app.Discover() error = %v", err)
			}
			got := constantAnnotations{}.Run(context.Background(), &Env{App: a})
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
