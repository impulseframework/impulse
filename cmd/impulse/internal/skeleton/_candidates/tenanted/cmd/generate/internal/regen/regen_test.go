//go:build unix

package regen

import (
	"crypto/sha256"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestAcquire(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, root string)
	}{
		{
			name: "the second holder waits for the first to release",
			run: func(t *testing.T, root string) {
				t.Helper()

				releaseFirst, err := acquire(root)
				if err != nil {
					t.Fatalf("acquire() error = %v", err)
				}
				held := make(chan error, 1)
				go func() {
					releaseSecond, err := acquire(root)
					if err == nil {
						err = releaseSecond()
					}
					held <- err
				}()
				select {
				case err := <-held:
					t.Fatalf("the second acquire() returned (%v) while the first held the lock", err)
				case <-time.After(300 * time.Millisecond):
				}
				if err := releaseFirst(); err != nil {
					t.Fatalf("release() error = %v", err)
				}
				select {
				case err := <-held:
					if err != nil {
						t.Fatalf("the second acquire() error = %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("the second acquire() never returned after the first release")
				}
			},
		},
		{
			name: "a released lock is taken again at once",
			run: func(t *testing.T, root string) {
				t.Helper()

				release, err := acquire(root)
				if err != nil {
					t.Fatalf("acquire() error = %v", err)
				}
				if err := release(); err != nil {
					t.Fatalf("release() error = %v", err)
				}
				again := make(chan error, 1)
				go func() {
					release, err := acquire(root)
					if err == nil {
						err = release()
					}
					again <- err
				}()
				select {
				case err := <-again:
					if err != nil {
						t.Fatalf("acquire() again error = %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("acquire() after a release never returned")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The lock is named for the root, so each case has its own.
			tt.run(t, t.TempDir())
		})
	}
}

// writeTree writes each file under root by its path relative to root, making the
// directories it needs.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for path, content := range files {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("os.MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
	}
}

// keysOf returns the map's paths, sorted.
func keysOf(sums map[string][32]byte) []string {
	keys := make([]string, 0, len(sums))
	for path := range sums {
		keys = append(keys, path)
	}
	slices.Sort(keys)

	return keys
}

func TestHashGenerated(t *testing.T) {
	t.Parallel()

	headed := generatedHeader + "\n\npackage auths\n"
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"app/zz_gen_routes.go":                 "package app\n",
		"web/src/app/zz_gen_types.ts":          "export type Name = string;\n",
		"pkg/auths/handlers.go":                headed,
		"app/main.go":                          "package app\n",
		"web/node_modules/lib/zz_gen_lib.go":   headed,
		"web/.angular/cache/zz_gen_cache.ts":   "export {};\n",
		"web/dist/app/handlers.go":             headed,
		"web/node_modules/lib/plain/routes.go": headed,
	})

	got, err := hashGenerated(root)
	if err != nil {
		t.Fatalf("hashGenerated() error = %v", err)
	}
	want := []string{"app/zz_gen_routes.go", "pkg/auths/handlers.go", "web/src/app/zz_gen_types.ts"}
	if keys := keysOf(got); !slices.Equal(keys, want) {
		t.Errorf("hashGenerated() paths = %v, want %v", keys, want)
	}
	if got["pkg/auths/handlers.go"] != sha256.Sum256([]byte(headed)) {
		t.Errorf("hashGenerated()[pkg/auths/handlers.go] is not the hash of the file's content")
	}
}

func TestChangedFiles(t *testing.T) {
	t.Parallel()

	sum := func(content string) [32]byte {
		return sha256.Sum256([]byte(content))
	}
	before := map[string][32]byte{"a/changed.go": sum("1"), "a/same.go": sum("2"), "b/gone.go": sum("3")}

	tests := []struct {
		name  string
		after map[string][32]byte
		want  []string
	}{
		{
			name:  "a changed file, a new one, one that is gone and one unchanged",
			after: map[string][32]byte{"a/changed.go": sum("1, again"), "a/same.go": sum("2"), "a/new.go": sum("4")},
			want:  []string{"a/changed.go", "a/new.go", "b/gone.go (removed)"},
		},
		{
			name:  "nothing changed",
			after: map[string][32]byte{"a/changed.go": sum("1"), "a/same.go": sum("2"), "b/gone.go": sum("3")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := changedFiles(before, tt.after); !slices.Equal(got, tt.want) {
				t.Errorf("changedFiles() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExclusive_reportsAChangedGeneratedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"app/zz_gen_routes.go": "package app\n",
		"app/main.go":          "package app\n",
	})

	finish, err := exclusive(root)
	if err != nil {
		t.Fatalf("exclusive() error = %v", err)
	}
	writeTree(t, root, map[string]string{
		"app/zz_gen_routes.go": "package app\n\n// a line the generator does not write\n",
		"app/main.go":          "package app\n\n// a person's change\n",
	})
	changed, err := finish()
	if err != nil {
		t.Fatalf("finish() error = %v", err)
	}
	if want := []string{"app/zz_gen_routes.go"}; !slices.Equal(changed, want) {
		t.Errorf("finish() with a generated file rewritten = %v, want %v", changed, want)
	}

	// The lock is free once finish returns: exclusive takes it again at once.
	again := make(chan error, 1)
	var finishAgain func() ([]string, error)
	go func() {
		var err error
		finishAgain, err = exclusive(root)
		again <- err
	}()
	select {
	case err := <-again:
		if err != nil {
			t.Fatalf("exclusive() again error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("exclusive() after finish never returned: finish did not release the lock")
	}
	changed, err = finishAgain()
	if err != nil {
		t.Fatalf("finish() again error = %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("finish() with nothing rewritten = %v, want nothing", changed)
	}
}
