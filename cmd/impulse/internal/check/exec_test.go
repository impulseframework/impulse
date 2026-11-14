package check

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/impulseframework/impulse/app"
)

var errExit = errors.New("exit status 1")

func TestRegen(t *testing.T) {
	t.Parallel()

	const generate = "go generate ./..."

	// write puts content at a path under root, creating directories as needed.
	write := func(t *testing.T, root, rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name         string
		skipGenerate bool
		// program is the generator program the application holds, when the case needs one.
		program     string
		generateErr error
		generateOut string
		// generate stands in for what go generate does to the tree.
		generate    func(t *testing.T, root string)
		wantStatus  Status
		wantSummary string
		wantDetails []string
		wantCalls   []string
	}{
		{
			name:         "skipped",
			skipGenerate: true,
			wantStatus:   Skip, wantSummary: "--skip-generate",
		},
		{
			name:        "generate fails",
			generateOut: "emulator not running\n",
			generateErr: errExit,
			wantStatus:  Fail, wantSummary: "go generate ./... failed",
			wantDetails: []string{"emulator not running"},
			wantCalls:   []string{generate},
		},
		{
			name: "changed, written, and removed generated files are drift; other files are not",
			generate: func(t *testing.T, root string) {
				t.Helper()
				write(t, root, "app/zz_gen_beacons.go", "package app // regenerated\n")
				write(t, root, "gui/src/app/core/service/zz_gen_api.ts", "export {};\n")
				if err := os.Remove(filepath.Join(root, "pkg", "router", "zz_gen_stale.go")); err != nil {
					t.Fatal(err)
				}
				write(t, root, "README.md", "rewritten by hand, not generated\n")
			},
			wantStatus:  Fail,
			wantSummary: "3 generated file(s) changed when regenerated; commit the regenerated files if the change is intentional",
			wantDetails: []string{
				"app/zz_gen_beacons.go (changed)",
				"gui/src/app/core/service/zz_gen_api.ts (written)",
				"pkg/router/zz_gen_stale.go (removed)",
			},
			wantCalls: []string{generate},
		},
		{
			name:       "clean: regeneration rewrites identical content",
			generate:   func(t *testing.T, root string) { t.Helper(); write(t, root, "app/zz_gen_beacons.go", "package app\n") },
			wantStatus: Pass, wantSummary: "regeneration reproduces the generated files on disk",
			wantCalls: []string{generate},
		},
		{
			name: "clean, with the schema warnings the programs printed",
			generateOut: "2026/09/22 10:00:00 Finished Resource generation in 1s\n" +
				"Warning: Beacon lists in StationId, Name order with no index leading with those columns, so every page sorts the tenant's partition; wanted: CREATE INDEX BeaconsByStationIdName ON Beacons(StationId, Name)\n" +
				"2026/09/22 10:00:01 Finished Typescript generation in 1s\n" +
				"Warning: Lens resolves its tenant through BeaconId, Beacons.StationId, so its lists scan all of Lenses, every tenant, and no index on Lenses changes that; a table listed at volume carries the tenant key on the row\n",
			wantStatus:  Pass,
			wantSummary: "regeneration reproduces the generated files on disk; 2 schema warning(s)",
			wantDetails: []string{
				"Warning: Beacon lists in StationId, Name order with no index leading with those columns, so every page sorts the tenant's partition; wanted: CREATE INDEX BeaconsByStationIdName ON Beacons(StationId, Name)",
				"Warning: Lens resolves its tenant through BeaconId, Beacons.StationId, so its lists scan all of Lenses, every tenant, and no index on Lenses changes that; a table listed at volume carries the tenant key on the row",
			},
			wantCalls: []string{generate},
		},
		{
			name:        "a warning line in a failed run stays in the tail",
			generateOut: "Warning: Beacon lists in StationId, Name order with no index\ngeneration: boom\n",
			generateErr: errExit,
			wantStatus:  Fail, wantSummary: "go generate ./... failed",
			wantDetails: []string{"Warning: Beacon lists in StationId, Name order with no index", "generation: boom"},
			wantCalls:   []string{generate},
		},
		{
			// The GenerateIdentity tree is the generator's whole: its files carry plain
			// names and are held like any generated file, while a file of the same name
			// outside the tree is the application's own.
			name:    "every file under a tree a generator owns is generated, whatever its name",
			program: "package main\n\nimport \"github.com/impulseframework/impulse/generation\"\n\nfunc main() {\n\t_, _ = generation.NewResourceGenerator(nil, \"pkg/resources\", nil,\n\t\tgeneration.GenerateHandlers(\"app\"),\n\t\tgeneration.GenerateIdentity(\"pkg/identity\"),\n\t)\n}\n",
			generate: func(t *testing.T, root string) {
				t.Helper()
				write(t, root, "pkg/identity/staffidentity/client.go", "package staffidentity // regenerated\n")
				write(t, root, "pkg/identity/internal/staffresources/staff_sessions.go", "package staffresources\n")
				if err := os.RemoveAll(filepath.Join(root, "pkg", "identity", "partnershandlers")); err != nil {
					t.Fatal(err)
				}
				write(t, root, "pkg/config/client.go", "package config // edited by hand\n")
			},
			wantStatus:  Fail,
			wantSummary: "3 generated file(s) changed when regenerated; commit the regenerated files if the change is intentional",
			wantDetails: []string{
				"pkg/identity/internal/staffresources/staff_sessions.go (written)",
				"pkg/identity/partnershandlers/handlers.go (removed)",
				"pkg/identity/staffidentity/client.go (changed)",
			},
			wantCalls: []string{generate},
		},
		{
			name: "generated files under node_modules are not the application's",
			generate: func(t *testing.T, root string) {
				t.Helper()
				write(t, root, "gui/node_modules/dep/zz_gen_thing.js", "changed\n")
			},
			wantStatus: Pass, wantSummary: "regeneration reproduces the generated files on disk",
			wantCalls: []string{generate},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			write(t, root, "app/zz_gen_beacons.go", "package app\n")
			write(t, root, "pkg/router/zz_gen_stale.go", "package router\n")
			write(t, root, "README.md", "hand-written\n")
			write(t, root, "gui/node_modules/dep/zz_gen_thing.js", "vendored\n")
			write(t, root, "pkg/identity/staffidentity/client.go", "package staffidentity\n")
			write(t, root, "pkg/identity/partnershandlers/handlers.go", "package partnershandlers\n")
			write(t, root, "pkg/config/client.go", "package config\n")
			application := &app.App{Root: root}
			if tt.program != "" {
				g, err := app.ParseGenerator("cmd/generate/main.go", []byte(tt.program))
				if err != nil || g == nil {
					t.Fatalf("app.ParseGenerator() = %v, error = %v, want the case's program read", g, err)
				}
				application.Generators = []*app.Generator{g}
			}

			answers := map[string]fakeAnswer{}
			if !tt.skipGenerate {
				answer := fakeAnswer{out: tt.generateOut, err: tt.generateErr}
				if tt.generate != nil {
					answer.run = func() { tt.generate(t, root) }
				}
				answers[generate] = answer
			}
			exec := &fakeExec{t: t, answers: answers}
			got := regen{}.Run(context.Background(), &Env{App: application, Exec: exec, SkipGenerate: tt.skipGenerate})
			want := Result{Name: regen{}.Name(), Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantCalls, exec.calls); diff != "" {
				t.Errorf("commands mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGoworkOff(t *testing.T) {
	t.Parallel()

	const (
		build = "go build ./..."
		vet   = "go vet ./..."
	)

	tests := []struct {
		name        string
		answers     map[string]fakeAnswer
		wantStatus  Status
		wantSummary string
		wantDetails []string
		wantCalls   []string
	}{
		{
			name:       "build fails",
			answers:    map[string]fakeAnswer{build: {out: "pkg/x: cannot find module\n", err: errExit}},
			wantStatus: Fail, wantSummary: "GOWORK=off go build ./... failed",
			wantDetails: []string{"pkg/x: cannot find module"},
			wantCalls:   []string{build},
		},
		{
			name:       "vet fails",
			answers:    map[string]fakeAnswer{build: {}, vet: {out: "vet: unreachable code\n", err: errExit}},
			wantStatus: Fail, wantSummary: "GOWORK=off go vet ./... failed",
			wantDetails: []string{"vet: unreachable code"},
			wantCalls:   []string{build, vet},
		},
		{
			name:       "clean",
			answers:    map[string]fakeAnswer{build: {}, vet: {}},
			wantStatus: Pass, wantSummary: "GOWORK=off go build and go vet succeed",
			wantCalls: []string{build, vet},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exec := &fakeExec{t: t, answers: tt.answers}
			got := goworkOff{}.Run(context.Background(), &Env{App: &app.App{Root: "/app"}, Exec: exec})
			want := Result{Name: goworkOff{}.Name(), Status: tt.wantStatus, Summary: tt.wantSummary, Details: tt.wantDetails}
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("Run() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantCalls, exec.calls); diff != "" {
				t.Errorf("commands mismatch (-want +got):\n%s", diff)
			}
			for i, env := range exec.envs {
				if diff := cmp.Diff([]string{"GOWORK=off"}, env); diff != "" {
					t.Errorf("command %d environment mismatch (-want +got):\n%s", i, diff)
				}
			}
		})
	}
}
