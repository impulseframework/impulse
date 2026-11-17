package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/pkg/auth/staff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/pkg/config"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/session"
)

// The emulator's own project and instance: db-initiator creates the instance when the
// container starts, and each case creates a database on it. versionsTable is where the
// runner records the schema versions, whose row a case edits to stand for a migration
// file that stopped part way with its progress recorded.
const (
	containerProjectID  = "unit-testing"
	containerInstanceID = "test-instance"
	versionsTable       = "SchemaMigrations"
	migrationsDir       = "schema/migrations"
	seedDir             = "schema/devseed"
)

// serviceName is the service name the migration run's core level reads
// (APP_SERVICE_NAME); any name serves the emulators.
const serviceName = "migrate-test"

// The foothold's settings, as a deployment supplies them: the password the staff auth's
// first administrator is seeded with, the stack's generated secret.
const (
	administratorPassword = "a-generated-password"
)

// The package's emulators: the Spanner container the cases create their databases on,
// and the Firestore emulator the migration run's live service opens. The data level
// requires the live service, and a migration run finds its Firestore database as the
// deployment's job does (APP_FIRESTORE_DATABASE) or as the development stack does
// (FIRESTORE_EMULATOR_HOST), which is how the cases hand it the emulator.
var (
	container         *initiator.SpannerContainer
	firestoreEmulator *initiator.FirestoreContainer
)

// TestMain starts one Spanner emulator and one Firestore emulator for the package; the
// cases publish them in the environment, which is how the deployment's job finds its
// databases. The Firestore emulator runs from the Cloud SDK emulators image the Procfile
// starts, by the SDK version, and without rules, since the live service writes with the
// emulator's owner credential, which rules never apply to.
func TestMain(m *testing.M) {
	ctx := context.Background()

	c, err := initiator.NewSpannerContainer(ctx, "1.5.56")
	if err != nil {
		log.Fatal(err)
	}
	container = c

	f, err := initiator.NewFirestoreContainer(ctx, "562.0.0")
	if err != nil {
		stopSpanner(ctx, c)
		log.Fatal(err)
	}
	firestoreEmulator = f

	exitCode := m.Run()

	stopFirestore(ctx, f)
	stopSpanner(ctx, c)

	os.Exit(exitCode)
}

// stopSpanner terminates the Spanner emulator's container and closes the handle on it.
func stopSpanner(ctx context.Context, c *initiator.SpannerContainer) {
	if err := c.Terminate(ctx); err != nil {
		fmt.Println(err)
	}
	if err := c.Close(); err != nil {
		fmt.Println(err)
	}
}

// stopFirestore terminates the Firestore emulator's container and closes the handle on
// it.
func stopFirestore(ctx context.Context, f *initiator.FirestoreContainer) {
	if err := f.Terminate(ctx); err != nil {
		fmt.Println(err)
	}
	if err := f.Close(); err != nil {
		fmt.Println(err)
	}
}

// TestExecute runs the command's flags against the emulators, each case on a database of
// its own: the migrations with the seed, the report on a fresh, a migrated and a dirty
// database, the forces with the row before and after, the refusals before the database is
// touched, and the exit codes. The command reads its target from the environment, so the
// cases run one at a time, from the module root, where the migrations are read from as
// they are in the image.
func TestExecute(t *testing.T) {
	emulators(t)
	// The foothold's settings, which a case may override.
	t.Setenv("APP_STAFF_ADMINISTRATOR_PASSWORD", administratorPassword)
	last := lastMigration(t)

	tests := []struct {
		name string
		// prepare readies the case's database; nil leaves it fresh.
		prepare func(t *testing.T, db *initiator.SpannerDB)
		// missing points the command at a database that does not exist: a refusal must
		// come before the database is touched, and a run against it fails.
		missing  bool
		args     []string
		wantCode int
		// wantOut is the whole of stdout; wantErr a fragment of stderr.
		wantOut string
		wantErr string
		// wantAfter is what -version reports once the case has run; empty skips the check.
		wantAfter string
		// env overrides the environment for the case, a variable at a time; an empty value
		// is set, not unset.
		env map[string]string
		// after checks the database once the run has returned; nil checks nothing.
		after func(t *testing.T)
	}{
		{
			name:      "the migrations with the seed apply every schema and data migration and seed the first administrator",
			args:      []string{"-seed"},
			wantAfter: fmt.Sprintf("schema: version %d\ndata: %s\n", last, seededData(t)),
			after:     administratorSeeded,
		},
		{
			name:    "a database with a user seeds no administrator",
			prepare: withUser,
			after:   administratorAbsent,
		},
		{
			name:    "a database every user has left seeds the administrator again",
			prepare: seededThenEmptied,
			after:   administratorSeeded,
		},
		{
			name:     "an empty users table with no administrator password is refused",
			env:      map[string]string{"APP_STAFF_ADMINISTRATOR_PASSWORD": ""},
			wantCode: exitFailed,
			wantErr:  "no administrator password was given",
		},
		{
			name:    "the report on a fresh database",
			args:    []string{"-version"},
			wantOut: "schema: no version\ndata: no version\n",
		},
		{
			name:    "the report on a migrated database",
			prepare: migrated,
			args:    []string{"-version"},
			wantOut: fmt.Sprintf("schema: version %d\ndata: no version\n", last),
		},
		{
			name:    "the report on a database dirty with progress",
			prepare: dirty,
			args:    []string{"-version"},
			wantOut: fmt.Sprintf("schema: version %d, dirty: 2 statements applied\ndata: no version\n", last),
		},
		{
			name:      "a force sets the schema version and prints the row before and after",
			prepare:   dirty,
			args:      []string{"-force", strconv.Itoa(last - 1)},
			wantOut:   fmt.Sprintf("schema: version %d, dirty: 2 statements applied\nforced schema to version %d\nschema: version %d\n", last, last-1, last-1),
			wantAfter: fmt.Sprintf("schema: version %d\ndata: no version\n", last-1),
		},
		{
			name:      "a force to -1 leaves no version",
			prepare:   migrated,
			args:      []string{"-force", "-1"},
			wantOut:   fmt.Sprintf("schema: version %d\nforced schema to no version\nschema: no version\n", last),
			wantAfter: "schema: no version\ndata: no version\n",
		},
		{
			name:      "a data force sets the data version",
			args:      []string{"-force-data", "1"},
			wantOut:   "data: no version\nforced data to version 1\ndata: version 1\n",
			wantAfter: "schema: no version\ndata: version 1\n",
		},
		{
			name:      "both forces together, the schema first",
			prepare:   migrated,
			args:      []string{"-force-data", "2", "-force", strconv.Itoa(last - 1)},
			wantOut:   fmt.Sprintf("schema: version %d\nforced schema to version %d\nschema: version %d\ndata: no version\nforced data to version 2\ndata: version 2\n", last, last-1, last-1),
			wantAfter: fmt.Sprintf("schema: version %d\ndata: version 2\n", last-1),
		},
		{
			name:     "a force with the seed is refused before the database is touched",
			missing:  true,
			args:     []string{"-force", "40", "-seed"},
			wantCode: exitRefused,
			wantErr:  "-force and -force-data apply no migration: -seed cannot go with them",
		},
		{
			name:     "a force with the report is refused",
			missing:  true,
			args:     []string{"-force-data", "1", "-version"},
			wantCode: exitRefused,
			wantErr:  "-version reports and -force changes: give one or the other",
		},
		{
			name:     "the report with the seed is refused",
			missing:  true,
			args:     []string{"-version", "-seed"},
			wantCode: exitRefused,
			wantErr:  "-version applies no migration: -seed cannot go with it",
		},
		{
			name:     "a force without a value is refused",
			missing:  true,
			args:     []string{"-force"},
			wantCode: exitRefused,
			wantErr:  "flag needs an argument: -force",
		},
		{
			name:     "a force with a non-integer is refused",
			missing:  true,
			args:     []string{"-force", "forty"},
			wantCode: exitRefused,
			wantErr:  `"forty" is not a version: an integer 0 or above, or -1 for no version`,
		},
		{
			name:     "a force below -1 is refused",
			missing:  true,
			args:     []string{"-force", "-2"},
			wantCode: exitRefused,
			wantErr:  `"-2" is not a version`,
		},
		{
			name:     "a retirement with another flag is refused before the database is touched",
			missing:  true,
			args:     []string{"-retire-secret", "APP_SECRET_KEY_1", "-seed"},
			wantCode: exitRefused,
			wantErr:  "-retire-secret applies no migration and reports no version: it goes with no other flag",
		},
		{
			name:     "a retirement without its variable is refused",
			missing:  true,
			args:     []string{"-retire-secret"},
			wantCode: exitRefused,
			wantErr:  "flag needs an argument: -retire-secret",
		},
		{
			name:    "a retirement of an older version of the application secret finds no value that names it and passes",
			prepare: migrated,
			args:    []string{"-retire-secret", "APP_SECRET_KEY_1"},
			env:     map[string]string{"APP_SECRET_KEY_1": secretVersion(1), "APP_SECRET_KEY_2": secretVersion(2)},
			wantOut: "Retired APP_SECRET_KEY_1: no stored value names version 1\n",
		},
		{
			name:     "a retirement of the current version of the application secret is refused",
			prepare:  migrated,
			args:     []string{"-retire-secret", "APP_SECRET_KEY_2"},
			env:      map[string]string{"APP_SECRET_KEY_1": secretVersion(1), "APP_SECRET_KEY_2": secretVersion(2)},
			wantCode: exitFailed,
			wantErr:  "APP_SECRET_KEY_2 is the current version of the application secret",
		},
		{
			name:     "a retirement of a variable that is not a version of the application secret is refused",
			prepare:  migrated,
			args:     []string{"-retire-secret", "APP_COOKIE_KEY_1"},
			env:      map[string]string{"APP_SECRET_KEY_1": secretVersion(1), "APP_SECRET_KEY_2": secretVersion(2)},
			wantCode: exitFailed,
			wantErr:  "APP_COOKIE_KEY_1 is not a version of the application secret",
		},
		{
			name:     "a stray argument is refused",
			missing:  true,
			args:     []string{"-version", "extra"},
			wantCode: exitRefused,
			wantErr:  `the command takes no argument: "extra"`,
		},
		{
			name:     "a database that does not exist fails the run",
			missing:  true,
			args:     []string{"-version"},
			wantCode: exitFailed,
			wantErr:  "deploy.Versions()",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := "nothing-here"
			var db *initiator.SpannerDB
			if !tt.missing {
				db = database(t)
				name = path.Base(db.DatabaseName())
			}
			t.Setenv("GOOGLE_CLOUD_SPANNER_DATABASE_NAME", name)
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			if tt.prepare != nil {
				tt.prepare(t, db)
			}

			var stdout, stderr bytes.Buffer
			code := execute(t.Context(), tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Fatalf("execute(%v) = %d, want %d; stdout:\n%s\nstderr:\n%s", tt.args, code, tt.wantCode, stdout.String(), stderr.String())
			}
			if stdout.String() != tt.wantOut {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantOut)
			}
			if tt.wantErr != "" && !strings.Contains(stderr.String(), tt.wantErr) {
				t.Errorf("stderr lacks %q:\n%s", tt.wantErr, stderr.String())
			}
			if tt.wantErr == "" && stderr.Len() != 0 {
				t.Errorf("stderr = %q, want none", stderr.String())
			}
			if tt.after != nil {
				tt.after(t)
			}
			if tt.wantAfter == "" {
				return
			}
			var after bytes.Buffer
			if code := execute(t.Context(), []string{"-version"}, &after, &stderr); code != 0 {
				t.Fatalf("execute(-version) after the run = %d; stderr:\n%s", code, stderr.String())
			}
			if after.String() != tt.wantAfter {
				t.Errorf("the report after the run = %q, want %q", after.String(), tt.wantAfter)
			}
		})
	}
}

// TestSeedAdministrator proves the foothold's rule, each case on a migrated database of
// its own, by what the seed reports and what it leaves. The seed runs only while the
// users table is empty, and it is two writes: the administrator's roles first, the Super
// Administrator role among them, which the permission store holds under an id the seed
// derives (session.SeedUserID, the same on every run), then the user, created under that
// id with the password the seed is handed, marked for change: the deployment generated
// it, and the first sign-in changes it. The user's row is what says the seed
// is done: a run that stopped between the two left the table empty, and the next run
// writes the same memberships again, each left as it is, and creates the user. Once the
// table holds a user nothing is seeded, whoever that user is and whatever it holds: a
// user under the administrator's name whom the seed did not create is given no role.
// The data level reads its target from the environment, so the cases run one at a time.
func TestSeedAdministrator(t *testing.T) {
	emulators(t)
	// The seed is handed the password; the setting is the deployment's, which the data
	// level reads as it opens.
	t.Setenv("APP_STAFF_ADMINISTRATOR_PASSWORD", administratorPassword)

	tests := []struct {
		name string
		// prepare readies the users table and the permission store before the run; nil
		// leaves both empty.
		prepare func(t *testing.T, auth *staff.Auth)
		// wantSeeded is what the run reports.
		wantSeeded bool
		// wantAdministrator says the run leaves the first administrator: the one user of
		// the table, under the id the seed derives, which holds the administrator's roles.
		// False, that the run leaves the users it found and no other, none of them holding
		// a role, and the administrator's roles held by nobody.
		wantAdministrator bool
	}{
		{
			name:              "an empty users table: the roles are held under the id the seed derives and the user is created under it",
			wantSeeded:        true,
			wantAdministrator: true,
		},
		{
			name:              "a second run seeds nothing: the administrator keeps the account and the roles the first gave",
			prepare:           seededOnce,
			wantAdministrator: true,
		},
		{
			name:              "a run that stopped after the roles and before the user: the next run creates the user and each role is held once",
			prepare:           stoppedBeforeTheUser,
			wantSeeded:        true,
			wantAdministrator: true,
		},
		{
			name:    "a user under the administrator's name who holds no role, in a table that is not empty: nothing is seeded and the user is given no role",
			prepare: someoneUnderTheName,
		},
		{
			name:    "other users and no user under the administrator's name: nothing is seeded",
			prepare: someoneElse,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := database(t)
			t.Setenv("GOOGLE_CLOUD_SPANNER_DATABASE_NAME", path.Base(db.DatabaseName()))
			migrated(t, db)
			data := openData(t)
			auth := data.Staff()
			if tt.prepare != nil {
				tt.prepare(t, auth)
			}
			before := accounts(t, auth)

			seeded, err := auth.SeedAdministrator(t.Context(), administratorPassword)
			if err != nil {
				t.Fatalf("staff.Auth.SeedAdministrator() error = %v", err)
			}
			if seeded != tt.wantSeeded {
				t.Errorf("staff.Auth.SeedAdministrator() = %v, want %v", seeded, tt.wantSeeded)
			}

			if tt.wantAdministrator {
				administratorAlone(t, data)

				return
			}
			nothingSeeded(t, data, before)
		})
	}
}

// emulators points the test at the package's emulators the way a deployment's job finds
// its databases, in the environment, with the settings the data level reads as it opens,
// and moves the test to the module root, where the migrations are read from as they are
// in the image.
func emulators(t *testing.T) {
	t.Helper()

	ctx := context.Background()
	t.Chdir(filepath.Join("..", "..", ".."))
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "9010/tcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SPANNER_EMULATOR_HOST", host+":"+port.Port())
	t.Setenv("GOOGLE_CLOUD_SPANNER_PROJECT", containerProjectID)
	t.Setenv("GOOGLE_CLOUD_SPANNER_INSTANCE_ID", containerInstanceID)
	// The migration run opens the data level, which reads the core level's service name
	// and opens the live service, required, over the Firestore emulator.
	t.Setenv("APP_SERVICE_NAME", serviceName)
	t.Setenv("APP_PUBLIC_URL", "http://tenanted.test")
	t.Setenv("FIRESTORE_EMULATOR_HOST", firestoreEmulator.Host())
}

// seededOnce is a run before the case's own that seeded the administrator.
func seededOnce(t *testing.T, auth *staff.Auth) {
	t.Helper()

	seeded, err := auth.SeedAdministrator(t.Context(), administratorPassword)
	if err != nil || !seeded {
		t.Fatalf("the first staff.Auth.SeedAdministrator() = %v, %v, want the administrator seeded", seeded, err)
	}
}

// stoppedBeforeTheUser stands for a run that stopped between the seed's two writes: the
// administrator's memberships are held under the id the seed derives, each where its
// roles are held, written as the seed writes them, and the users table is empty.
func stoppedBeforeTheUser(t *testing.T, auth *staff.Auth) {
	t.Helper()

	global, everyDomain := seededRoles()
	manager := auth.Access().UserManager()
	user := accesstypes.User(seedID().String())
	if err := manager.AddUserRoles(t.Context(), accesstypes.GlobalPolicyScope(), user, global...); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s) in the global partition error = %v", user, err)
	}
	if err := manager.AddUserRoles(t.Context(), accesstypes.EveryDomainPolicyScope(), user, everyDomain...); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles(%s) in every domain error = %v", user, err)
	}
}

// someoneUnderTheName gives the auth one user under the first administrator's name whom
// the seed did not create, as a stranger's sign-up or an operator's entry leaves one: an
// account under an id of its own, with a password, that holds no role anywhere.
func someoneUnderTheName(t *testing.T, auth *staff.Auth) {
	t.Helper()

	password := administratorPassword
	if _, err := auth.Session().API().CreateSessionUser(t.Context(), &session.CreateUserRequest{Username: staff.Administrator, Password: &password}); err != nil {
		t.Fatalf("session.AuthAPI.CreateSessionUser(%s) error = %v", staff.Administrator, err)
	}
}

// someoneElse gives the auth one user who is not under the first administrator's name.
func someoneElse(t *testing.T, auth *staff.Auth) {
	t.Helper()

	if _, err := auth.Session().API().CreateSessionUser(t.Context(), &session.CreateUserRequest{Username: "someone"}); err != nil {
		t.Fatalf("session.AuthAPI.CreateSessionUser(someone) error = %v", err)
	}
}

// seededRoles are the roles the seed writes for the first administrator, by where they
// are held: the Super Administrator role, which the framework defines in every auth and
// every first administrator holds, beside the application's own
// (staff.FirstAdministratorRoles).
func seededRoles() (global, everyDomain []accesstypes.Role) {
	global, everyDomain = staff.FirstAdministratorRoles()

	return append([]accesstypes.Role{accesstypes.SuperAdministrator}, global...), everyDomain
}

// seedID is the id the seed creates the first administrator under, which it derives from
// the auth's name and the administrator's: the same on every run.
func seedID() impulse.UUID {
	return session.SeedUserID(staff.Name, staff.Administrator)
}

// accounts are the auth's users: the name of each by the id of its account.
func accounts(t *testing.T, auth *staff.Auth) map[impulse.UUID]string {
	t.Helper()

	users, err := auth.Session().API().Users(t.Context())
	if err != nil {
		t.Fatalf("session.AuthAPI.Users() error = %v", err)
	}
	byID := make(map[impulse.UUID]string, len(users))
	for _, user := range users {
		byID[user.ID] = user.Username
	}

	return byID
}

// administratorAlone checks what the seed leaves: the users table holds the first
// administrator alone, under the id the seed derives, with the password the seed was
// handed marked for change, and the permission store holds the administrator's roles
// under that id.
func administratorAlone(t *testing.T, data *config.DataConfiguration) {
	t.Helper()

	id := seedID()
	if got, want := accounts(t, data.Staff()), map[impulse.UUID]string{id: staff.Administrator}; !maps.Equal(got, want) {
		t.Errorf("the users table after the run = %v, want %v: %s alone, under the id the seed derives", got, want, staff.Administrator)
	} else {
		mustChangePassword(t, data.Staff(), id)
	}
	holdsAdministratorRoles(t, data, id)
}

// mustChangePassword checks the first administrator's row holds a password and the mark
// that it must change (PasswordChangeRequired). The deployment generated the password,
// so the session library serves the person nothing but the change until they have chosen
// one of their own, and the generated one signs nobody in after that.
func mustChangePassword(t *testing.T, auth *staff.Auth, id impulse.UUID) {
	t.Helper()

	user, err := auth.Session().API().User(t.Context(), id)
	if err != nil {
		t.Fatalf("session.AuthAPI.User(%s) error = %v", id, err)
	}
	if user.PasswordHash == nil {
		t.Errorf("%s holds no password, want the one the seed was handed", staff.Administrator)
	}
	if !user.PasswordChangeRequired {
		t.Errorf("%s is not marked to change the password, want the mark: the deployment generated the password, and the first sign-in changes it", staff.Administrator)
	}
}

// nothingSeeded checks what a run that seeds nothing leaves: the users table as the run
// found it, the same accounts under the same names and no other; none of them holding a
// role anywhere, whatever its name; nothing held under the id the seed derives or under
// the administrator's username; and the administrator's roles held by nobody.
func nothingSeeded(t *testing.T, data *config.DataConfiguration, before map[impulse.UUID]string) {
	t.Helper()

	after := accounts(t, data.Staff())
	if !maps.Equal(after, before) {
		t.Errorf("the users table after the run = %v, want %v: the users the run found and no other", after, before)
	}
	for id, name := range after {
		holdsNoRole(t, data, accesstypes.User(id.String()), "the account of "+name+", which the seed did not create")
	}
	holdsNoRole(t, data, accesstypes.User(seedID().String()), "the id the seed derives, which no user is under")
	holdsNoRole(t, data, staff.Administrator, "the administrator's username: a role is held by the account")
	global, everyDomain := seededRoles()
	heldBy(t, data, accesstypes.GlobalPolicyScope(), global)
	heldBy(t, data, accesstypes.EveryDomainPolicyScope(), everyDomain)
}

// holdsAdministratorRoles checks the permission store holds the administrator's roles
// under the id of the account, each where it is held and each held by that account
// alone, nothing anywhere else under the id, and nothing under the administrator's
// username: a membership names its person by the id of their account, and one written
// under a name is held by nobody.
func holdsAdministratorRoles(t *testing.T, data *config.DataConfiguration, id impulse.UUID) {
	t.Helper()

	user := accesstypes.User(id.String())
	roles, err := data.UserManager().UserRoles(t.Context(), user)
	if err != nil {
		t.Fatalf("access.UserManager.UserRoles(%s) error = %v", id, err)
	}
	global, everyDomain := seededRoles()
	if got, want := sorted(roles[accesstypes.GlobalPolicyScope()]), sorted(global); !slices.Equal(got, want) {
		t.Errorf("roles in the global partition = %v, want %v", got, want)
	}
	if got, want := sorted(roles[accesstypes.EveryDomainPolicyScope()]), sorted(everyDomain); !slices.Equal(got, want) {
		t.Errorf("roles in every domain = %v, want %v", got, want)
	}
	delete(roles, accesstypes.GlobalPolicyScope())
	delete(roles, accesstypes.EveryDomainPolicyScope())
	for scope, held := range roles {
		t.Errorf("roles held under the id of the account in %v = %v, want none: the administrator's roles are held in the global partition and in every domain", scope, held)
	}
	heldBy(t, data, accesstypes.GlobalPolicyScope(), global, user)
	heldBy(t, data, accesstypes.EveryDomainPolicyScope(), everyDomain, user)
	holdsNoRole(t, data, staff.Administrator, "the administrator's username: a role is held by the account")
}

// heldBy checks who holds each of the roles where they are held: the users given and no
// other, so a second set of memberships under another id fails it; nobody, when none is
// given.
func heldBy(t *testing.T, data *config.DataConfiguration, scope accesstypes.PolicyScope, roles []accesstypes.Role, want ...accesstypes.User) {
	t.Helper()

	for _, role := range roles {
		got, err := data.UserManager().RoleUsers(t.Context(), scope, role)
		if err != nil {
			t.Fatalf("access.UserManager.RoleUsers(%v, %s) error = %v", scope, role, err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("the users who hold %s (%v) = %v, want %v", role, scope, got, want)
		}
	}
}

// holdsNoRole checks the permission store holds no role under the user anywhere; whose
// says who the user is, for the failure.
func holdsNoRole(t *testing.T, data *config.DataConfiguration, user accesstypes.User, whose string) {
	t.Helper()

	roles, err := data.UserManager().UserRoles(t.Context(), user)
	if err != nil {
		t.Fatalf("access.UserManager.UserRoles(%s) error = %v", user, err)
	}
	for scope, held := range roles {
		if len(held) > 0 {
			t.Errorf("roles held under %s (%s) in %v = %v, want none", user, whose, scope, held)
		}
	}
}

// secretVersion is one version of a test application secret as the deployment hands it
// over: 32 bytes, the version's number repeated, in Base64.
func secretVersion(version byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{version}, 32))
}

// database creates a fresh database for the case and drops it when the case ends.
func database(t *testing.T) *initiator.SpannerDB {
	t.Helper()

	db, err := container.CreateDatabase(t.Context(), t.Name())
	if err != nil {
		t.Fatalf("initiator.SpannerContainer.CreateDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.DropDatabase(context.Background()); err != nil {
			t.Error(err)
		}
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})

	return db
}

// migrated applies every schema migration the tree carries.
func migrated(t *testing.T, db *initiator.SpannerDB) {
	t.Helper()

	if err := db.MigrateUp("file://" + migrationsDir); err != nil {
		t.Fatalf("initiator.SpannerDB.MigrateUp() error = %v", err)
	}
}

// dirty migrates the database and then edits the version row into what the runner leaves
// when the last file stops at its third statement: dirty, two statements applied.
func dirty(t *testing.T, db *initiator.SpannerDB) {
	t.Helper()

	migrated(t, db)
	mutation := spanner.Update(versionsTable, []string{"Version", "Dirty", "Applied", "Checkpoint"}, []any{int64(lastMigration(t)), true, int64(2), "cafe"})
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{mutation}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
}

// withUser migrates the database and gives the staff auth one user, so the foothold has
// nothing to do.
func withUser(t *testing.T, db *initiator.SpannerDB) {
	t.Helper()

	migrated(t, db)
	mutation := spanner.Insert("StaffSessionUsers", []string{"Id", "Username", "CreatedAt"}, []any{"00000000-0000-4000-8000-000000000001", "someone", spanner.CommitTimestamp})
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{mutation}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
}

// seededThenEmptied runs the migration once, which seeds the administrator, then deletes
// the administrator: a users table every user has left. The administrator's memberships
// stay in the permission store, under the id the seed derives, so the next run finds
// them, leaves each as it is and creates the user under that id again.
func seededThenEmptied(t *testing.T, _ *initiator.SpannerDB) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	if code := execute(t.Context(), nil, &stdout, &stderr); code != 0 {
		t.Fatalf("execute() = %d; stderr:\n%s", code, stderr.String())
	}
	data := openData(t)
	api := data.Staff().Session().API()
	id, err := api.ValidateCredentials(t.Context(), staff.Administrator, administratorPassword)
	if err != nil {
		t.Fatalf("session.AuthAPI.ValidateCredentials(%s) error = %v", staff.Administrator, err)
	}
	if err := api.DeleteSessionUser(t.Context(), id); err != nil {
		t.Fatalf("session.AuthAPI.DeleteSessionUser(%s) error = %v", staff.Administrator, err)
	}
}

// openData opens the data level on the case's database, as the migration run does, and
// closes it when the case ends.
func openData(t *testing.T) *config.DataConfiguration {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	data, err := config.NewDataConfiguration(ctx)
	if err != nil {
		cancel()
		t.Fatalf("config.NewDataConfiguration() error = %v", err)
	}
	t.Cleanup(func() {
		data.Close()
		cancel()
	})

	return data
}

// administratorSeeded checks the foothold: the staff auth's administrator signs in with
// the configured password and is the one user of the table, under the id the seed
// derives, which holds the administrator's roles in the permission store.
func administratorSeeded(t *testing.T) {
	t.Helper()

	data := openData(t)
	if _, err := data.Staff().Session().API().ValidateCredentials(t.Context(), staff.Administrator, administratorPassword); err != nil {
		t.Errorf("session.AuthAPI.ValidateCredentials(%s) error = %v", staff.Administrator, err)
	}
	administratorAlone(t, data)
}

// administratorAbsent checks nothing was seeded: no administrator signs in.
func administratorAbsent(t *testing.T) {
	t.Helper()

	data := openData(t)
	if _, err := data.Staff().Session().API().ValidateCredentials(t.Context(), staff.Administrator, administratorPassword); err == nil {
		t.Errorf("session.AuthAPI.ValidateCredentials(%s) succeeded, want a refusal", staff.Administrator)
	}
}

// sorted is a copy of the roles in order, for a comparison that ignores the store's.
func sorted(roles []accesstypes.Role) []accesstypes.Role {
	return slices.Sorted(slices.Values(roles))
}

// lastMigration is the highest version among the tree's schema migrations.
func lastMigration(t *testing.T) int {
	t.Helper()

	last, ok := lastIndex(t, migrationsDir)
	if !ok {
		t.Fatalf("no schema migrations under %s", migrationsDir)
	}

	return last
}

// seededData is what -version reports for the data table once the seed has run: the
// highest version among the seed's files, or no version while the application has no
// seed.
func seededData(t *testing.T) string {
	t.Helper()

	last, ok := lastIndex(t, seedDir)
	if !ok {
		return "no version"
	}

	return "version " + strconv.Itoa(last)
}

// lastIndex is the highest index among a migrations directory's up files; ok is false
// when the directory holds none or does not exist.
func lastIndex(t *testing.T, dir string) (last int, ok bool) {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(dir, "*.up.sql"))
	if err != nil {
		t.Fatalf("filepath.Glob() error = %v", err)
	}
	for _, file := range files {
		index, _, _ := strings.Cut(filepath.Base(file), "_")
		n, err := strconv.Atoi(index)
		if err != nil {
			t.Fatalf("%s does not start with a version: %v", file, err)
		}
		last = max(last, n)
	}

	return last, len(files) > 0
}
