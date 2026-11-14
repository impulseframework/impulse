package transition

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/mod/modfile"

	"github.com/impulseframework/impulse/app"
)

func TestRequirementChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		before string
		after  string
		want   []string
	}{
		{
			name:   "go.mod stands",
			before: "module m\n\ngo 1.26\n\nrequire github.com/spf13/cobra v1.10.2 // indirect\n",
			after:  "module m\n\ngo 1.26\n\nrequire github.com/spf13/cobra v1.10.2 // indirect\n",
		},
		{
			name:   "a module the options are known to pull in, required anew",
			before: "module m\n\ngo 1.26\n",
			after:  "module m\n\ngo 1.26\n\nrequire github.com/coreos/go-oidc/v3 v3.21.0 // indirect\n",
			want:   []string{"go.mod: requires github.com/coreos/go-oidc/v3 v3.21.0, the OIDC library the session library's directory methods sign in through (go mod tidy)"},
		},
		{
			name:   "a module outside the list, required anew",
			before: "module m\n\ngo 1.26\n",
			after:  "module m\n\ngo 1.26\n\nrequire example.com/lib v1.2.3\n",
			want:   []string{"go.mod: requires example.com/lib v1.2.3 (go mod tidy)"},
		},
		{
			name:   "a requirement made direct",
			before: "module m\n\ngo 1.26\n\nrequire github.com/spf13/cobra v1.10.2 // indirect\n",
			after:  "module m\n\ngo 1.26\n\nrequire github.com/spf13/cobra v1.10.2\n",
			want:   []string{"go.mod: requires github.com/spf13/cobra v1.10.2 directly, the command tree's library (go mod tidy)"},
		},
		{
			name:   "a requirement moved to another version",
			before: "module m\n\ngo 1.26\n\nrequire golang.org/x/oauth2 v0.36.0 // indirect\n",
			after:  "module m\n\ngo 1.26\n\nrequire golang.org/x/oauth2 v0.37.0 // indirect\n",
			want:   []string{"go.mod: requires golang.org/x/oauth2 v0.37.0 in place of v0.36.0 (go mod tidy)"},
		},
		{
			name:   "a requirement dropped, after the ones added",
			before: "module m\n\ngo 1.26\n\nrequire example.com/old v1.0.0\n",
			after:  "module m\n\ngo 1.26\n\nrequire example.com/new v1.0.0\n",
			want: []string{
				"go.mod: requires example.com/new v1.0.0 (go mod tidy)",
				"go.mod: no longer requires example.com/old (go mod tidy)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before, err := modfile.Parse("go.mod", []byte(tt.before), nil)
			if err != nil {
				t.Fatal(err)
			}
			after, err := modfile.Parse("go.mod", []byte(tt.after), nil)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tt.want, requirementChanges(before, after)); diff != "" {
				t.Errorf("requirementChanges() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestGenerateFirst pins the regeneration of a change whose code imports a package the
// generators write: the tolerant tidy, the generators, then the tidy that holds the
// module whole, with what the two tidies changed in go.mod reported once, and each
// failure refusing the transition with the step it failed at.
func TestGenerateFirst(t *testing.T) {
	t.Parallel()

	const lead = "go generate ./... failed; fix the cause and run it:"
	boom := func() error {
		return errors.New("exit status 1")
	}
	tests := []struct {
		name      string
		exec      func(a *app.App) *fakeExec
		wantCalls []string
		wantErr   string
		wantDid   []string
	}{
		{
			name: "the three steps, and the requirement the tolerant tidy added is reported",
			exec: func(a *app.App) *fakeExec {
				return &fakeExec{out: "Warning: a column no resource reads\n", tolerant: tidied(a, "module example.com/acme/beacon\n\ngo 1.26\n\nrequire github.com/coreos/go-oidc/v3 v3.11.0\n")}
			},
			wantCalls: []string{"go mod tidy -e", "go generate ./...", "go mod tidy"},
			wantDid:   []string{"go.mod: requires github.com/coreos/go-oidc/v3 v3.11.0, the OIDC library the session library's directory methods sign in through (go mod tidy)", "ran go generate ./..."},
		},
		{
			name: "a tolerant tidy that fails refuses before the generators run",
			exec: func(*app.App) *fakeExec {
				return &fakeExec{tolerant: boom}
			},
			wantCalls: []string{"go mod tidy -e"},
			wantErr:   "go mod tidy -e failed, so go.mod does not require what the change imports; fix the cause and run it, then go generate ./... and go mod tidy:\ngo: tidy -e: boom",
		},
		{
			name: "generators that fail refuse with the transition's lead",
			exec: func(*app.App) *fakeExec {
				return &fakeExec{err: errors.New("exit status 1")}
			},
			wantCalls: []string{"go mod tidy -e", "go generate ./..."},
			wantErr:   lead + "\ngeneration: boom",
		},
		{
			name: "a module the generators did not make whole is refused",
			exec: func(*app.App) *fakeExec {
				return &fakeExec{tidy: boom}
			},
			wantCalls: []string{"go mod tidy -e", "go generate ./...", "go mod tidy"},
			wantErr:   "go mod tidy failed after the regeneration, so the module is not whole: a package the change imports is neither required nor generated; fix the cause and run it:\ngo: tidy: boom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := beacon(t, authFiles(t))
			exec := tt.exec(a)
			ch := &Change{}
			err := generateFirst(t.Context(), a, exec, ch, lead, "ran go generate ./...")
			if !slices.Equal(exec.calls, tt.wantCalls) {
				t.Errorf("exec calls = %q, want %q", exec.calls, tt.wantCalls)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("generateFirst() error = %v, want %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("generateFirst() error = %v", err)
			}
			if !slices.Equal(ch.Did, tt.wantDid) {
				t.Errorf("Did = %q, want %q", ch.Did, tt.wantDid)
			}
			if want := []string{"a column no resource reads"}; !slices.Equal(ch.Warnings, want) {
				t.Errorf("Warnings = %q, want %q", ch.Warnings, want)
			}
		})
	}
}
