package skeleton

import (
	"io/fs"
	"regexp"
	"testing"

	"github.com/impulseframework/impulse/cmd/impulse/internal/names"
)

// reinvitedRE finds the auth a case of the migrate command's test hands the
// -reinvite-administrator flag under a name the case writes out. The application's own
// auths are named there through their packages (staff.Name).
var reinvitedRE = regexp.MustCompile(`"-reinvite-administrator", "([^"]*)"`)

// TestMigrateTestNamesNoAuthAnApplicationCanGain holds every candidate's test of the
// migrate command to a name no auth can have where it hands -reinvite-administrator an
// auth that invites nobody. impulse add auth makes the auth it adds one the flag takes.
// Under a name an application can give an auth, the case stops being a refusal the day
// the application gains that auth: the command goes on to a database the case never
// made, and waits there for the auth's first policy load until the test's time runs out.
func TestMigrateTestNamesNoAuthAnApplicationCanGain(t *testing.T) {
	t.Parallel()

	const migrateTest = "cmd/deployment/migrate/main_test.go"
	candidates, err := Candidates()
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}
	for _, candidate := range candidates {
		tree, err := FS(candidate.Name)
		if err != nil {
			t.Fatalf("FS(%s) error = %v", candidate.Name, err)
		}
		src, err := fs.ReadFile(tree, migrateTest)
		if err != nil {
			t.Fatalf("%s: reading %s: %v", candidate.Name, migrateTest, err)
		}
		named := reinvitedRE.FindAllStringSubmatch(string(src), -1)
		if len(named) == 0 {
			t.Errorf("%s: %s hands -reinvite-administrator no name it writes out: the case that refuses an auth that invites nobody is gone, or names its auth another way", candidate.Name, migrateTest)
		}
		for _, m := range named {
			if err := names.ValidateAuth(m[1], nil); err == nil {
				t.Errorf("%s: %s hands -reinvite-administrator the name %q as an auth that invites nobody, and impulse add auth %s makes it one that does: take a name no auth can have", candidate.Name, migrateTest, m[1], m[1])
			}
		}
	}
}
