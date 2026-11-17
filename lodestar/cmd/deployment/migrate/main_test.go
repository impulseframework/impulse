package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/lodestar/pkg/resources"
	"github.com/impulseframework/impulse/resource"
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
	commandEnvironment(t)
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
	}{
		{
			name:      "the migrations with the seed apply every schema and data migration",
			args:      []string{"-seed"},
			wantAfter: fmt.Sprintf("schema: version %d\ndata: %s\n", last, seededData(t)),
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
			if !tt.missing {
				db := database(t)
				if tt.prepare != nil {
					tt.prepare(t, db)
				}
				name = path.Base(db.DatabaseName())
			}
			t.Setenv("GOOGLE_CLOUD_SPANNER_DATABASE_NAME", name)

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

// TestExecute_retireSecret runs a retirement of a version of the application secret as
// the deployment runs it, against the emulators: ships are given transponder keys while
// version 1 is the only version, version 2 is added, and -retire-secret
// APP_SECRET_KEY_1 writes every key again under version 2, reports how many, and exits 0;
// every key then names version 2 and opens to what it was set to under version 2 alone;
// a second run writes nothing; retiring the current version fails; and no run prints a
// key.
//
// Demonstrates: secret.rotation.
func TestExecute_retireSecret(t *testing.T) {
	commandEnvironment(t)
	ctx := t.Context()
	db := database(t)
	t.Setenv("GOOGLE_CLOUD_SPANNER_DATABASE_NAME", path.Base(db.DatabaseName()))
	secretVersion := func(version byte) []byte {
		return bytes.Repeat([]byte{version}, 32)
	}
	run := func(wantCode int, args ...string) (stdout, stderr string) {
		t.Helper()

		var out, errs bytes.Buffer
		if code := execute(ctx, args, &out, &errs); code != wantCode {
			t.Fatalf("execute(%v) = %d, want %d; stdout:\n%s\nstderr:\n%s", args, code, wantCode, out.String(), errs.String())
		}

		return out.String(), errs.String()
	}

	// Version 1 is the only version: the release migrates and seeds the demo world, and
	// three of its ships are given keys, which the write path seals under version 1.
	t.Setenv("APP_SECRET_KEY_1", base64.StdEncoding.EncodeToString(secretVersion(1)))
	run(0, "-seed")
	first, err := resource.NewSecretKeys(map[int][]byte{1: secretVersion(1)})
	if err != nil {
		t.Fatalf("resource.NewSecretKeys() error = %v", err)
	}
	keys := map[string]string{
		"70000000-0000-4000-8000-000000000001": "kf-7741-open-sesame",
		"70000000-0000-4000-8000-000000000002": "sm-2020-heave-ho",
		"70000000-0000-4000-8000-000000000003": "ln-3030-lights-out",
	}
	before := resource.NewSpannerClient(db.Client, resource.WithSecretKeys(first))
	for id, key := range keys {
		if err := resources.NewShipUpdatePatch(impulse.Must(impulse.UUIDFromString(id))).SetTransponderKey(&key).Apply(ctx, before); err != nil {
			t.Fatalf("setting the key of ship %s: %v", id, err)
		}
	}
	// named asserts every key's stored bytes name the version and open, on their own
	// ship, to what the key was set to, with the keys of a service handed the versions.
	named := func(version int, held *resource.SecretKeys) {
		t.Helper()

		client := resource.NewSpannerClient(db.Client)
		for id, want := range keys {
			ship, err := resources.NewShipQuery().AddColumns(resources.NewShipColumns().TransponderKey()).
				SetID(impulse.Must(impulse.UUIDFromString(id))).Read(ctx, client)
			if err != nil {
				t.Fatalf("reading ship %s: %v", id, err)
			}
			if ship.Data.TransponderKey == nil || ship.Data.TransponderKey.KeyVersion() != version {
				t.Fatalf("the key of ship %s does not name version %d", id, version)
			}
			if got, err := ship.Data.OpenTransponderKey(held); err != nil || got == nil || *got != want {
				t.Fatalf("the key of ship %s does not open to what it was set to: error %v", id, err)
			}
		}
	}
	named(1, first)

	// The rotation hands the service version 2 beside version 1. Retiring version 2, the
	// current one, fails and writes nothing.
	t.Setenv("APP_SECRET_KEY_2", base64.StdEncoding.EncodeToString(secretVersion(2)))
	second, err := resource.NewSecretKeys(map[int][]byte{2: secretVersion(2)})
	if err != nil {
		t.Fatalf("resource.NewSecretKeys() error = %v", err)
	}
	stdout, stderr := run(exitFailed, "-retire-secret", "APP_SECRET_KEY_2")
	if stdout != "" || !strings.Contains(stderr, "APP_SECRET_KEY_2 is the current version of the application secret") {
		t.Errorf("retiring the current version: stdout %q, stderr %q; want a refusal and nothing written", stdout, stderr)
	}
	named(1, first)

	// Retiring version 1 writes the three keys again under version 2 and says so.
	const retired = "Retired APP_SECRET_KEY_1: no stored value names version 1\n"
	stdout, stderr = run(0, "-retire-secret", "APP_SECRET_KEY_1")
	if want := "Ships.TransponderKey: 3 value(s) written again under version 2\n" + retired; stdout != want || stderr != "" {
		t.Errorf("the retirement: stdout %q, stderr %q; want %q and nothing on stderr", stdout, stderr, want)
	}
	// Every key opens under version 2 alone: version 1 can be disabled.
	named(2, second)

	// A second run finds nothing to write and passes.
	again, stderr := run(0, "-retire-secret", "APP_SECRET_KEY_1")
	if want := "Ships.TransponderKey: 0 value(s) written again under version 2\n" + retired; again != want || stderr != "" {
		t.Errorf("the second retirement: stdout %q, stderr %q; want %q and nothing on stderr", again, stderr, want)
	}
	named(2, second)

	for _, key := range keys {
		if strings.Contains(stdout+again, key) {
			t.Errorf("a retirement printed a key")
		}
	}
}

// commandEnvironment readies what the command reads for every case: the module root,
// where the migrations are read from as they are in the image, and the emulators and the
// data level's settings in the environment.
func commandEnvironment(t *testing.T) {
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
	t.Setenv("FIRESTORE_EMULATOR_HOST", firestoreEmulator.Host())
	// The data level opens the members auth too, whose Google sign-in refuses to start
	// without the Workspace domain logins are restricted to and the prefix of the groups
	// that carry roles; nothing signs in here, so any values serve.
	t.Setenv("APP_MEMBERS_OIDC_CONNECTION", "example")
	t.Setenv("APP_MEMBERS_OIDC_HOSTED_DOMAIN", "example.com")
	t.Setenv("APP_MEMBERS_OIDC_GROUP_PREFIX", "members-")
	t.Setenv("APP_CREW_OIDC_CONNECTION", "example")
	t.Setenv("APP_CREW_OIDC_HOSTED_DOMAIN", "example.com")
	t.Setenv("APP_PUBLIC_URL", "http://lodestar.test")
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
