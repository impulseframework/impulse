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
	"regexp"
	"strconv"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/pkg/auth/staff"
	"github.com/impulseframework/impulse/cmd/impulse/internal/skeleton/_candidates/tenanted/pkg/config"
	initiator "github.com/impulseframework/impulse/db"
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

// administratorAddress is the foothold's setting, as a deployment supplies it: the email
// address of the staff auth's first administrator, which the placement names.
const administratorAddress = "ada@lakeside.example"

// What the command says when it refuses to invite for want of a mailbox: the refusal,
// whose it is, and the hand step that gives the environment its mailbox. roleHeld is
// what it writes where nobody is invited.
const (
	roleHeld        = "Somebody holds the Super Administrator role in the staff auth: no first administrator invited\n"
	noMailbox       = "the application has no mailbox to send as, so the first administrator's invitation was not sent"
	whoseInvitation = "the staff auth's first administrator (APP_STAFF_ADMINISTRATOR)"
	mailboxHandStep = "give the environment its mailbox before this release, the deployment's hand step for the mailbox: name the mailbox (APP_MAIL_MAILBOX) and add its app password (the secret APP_MAIL_APP_PASSWORD), then run the release again"
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
// its own: the migrations with the seed, the first administrator's invitation where the
// command can decide it without mailing, the report on a fresh, a migrated and a dirty
// database, the forces with the row before and after, the refusals before the database is
// touched, and the exit codes. The command reads its target from the environment, so the
// cases run one at a time, from the module root, where the migrations are read from as
// they are in the image.
//
// No case names a mailbox, so the data level writes its mail to the log, as an
// environment does before the mailbox's hand step: the command then refuses to invite a
// first administrator and says what to give, whether the auth has no account or accounts
// that hold nothing, and a database where somebody holds the Super Administrator role
// needs no invitation, no address and no mailbox. Whatever a case does, no invitation's
// link is in what the command wrote:
// its output, its errors and its log are read for one after every run. The invitation
// itself, mailed, accepted and sent again, is proven where the auth mails to a recorder
// (test/integration).
func TestExecute(t *testing.T) {
	emulators(t)
	// The foothold's setting, which a case may override, and no mailbox, whatever the
	// shell the suite runs in names.
	t.Setenv(staff.AdministratorVariable, administratorAddress)
	t.Setenv("APP_MAIL_MAILBOX", "")
	t.Setenv("APP_MAIL_APP_PASSWORD", "")
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
		// wantOut is the whole of stdout; wantErr a fragment of stderr, and wantAlso
		// further fragments of it.
		wantOut  string
		wantErr  string
		wantAlso []string
		// wantLast stands in for wantOut where the run writes a line of its own for each
		// secret field the application stores before its last line (the retirement of a
		// version of the application secret): stdout ends with it, and every line before
		// it is a field's line. The application as rendered stores no secret field; the
		// case stands as it is once a resource carries one (conditions:"secret").
		wantLast string
		// wantAfter is what -version reports once the case has run; empty skips the check.
		wantAfter string
		// env overrides the environment for the case, a variable at a time; an empty value
		// is set, not unset.
		env map[string]string
		// after checks the database once the run has returned; nil checks nothing.
		after func(t *testing.T)
	}{
		{
			name:      "the migrations with the seed apply every schema and data migration; with no mailbox the first administrator is not invited, and the run says what to give",
			args:      []string{"-seed"},
			wantCode:  exitFailed,
			wantErr:   noMailbox,
			wantAlso:  []string{whoseInvitation, mailboxHandStep},
			wantAfter: fmt.Sprintf("schema: version %d\ndata: %s\n", last, seededData(t)),
			after:     nobodyInvited,
		},
		{
			name:    "a database where somebody holds the Super Administrator role invites nobody, and the run goes to its end with no mailbox",
			prepare: withHolder,
			wantOut: roleHeld,
			after:   nobodyInvited,
		},
		{
			name:    "a database where somebody holds the Super Administrator role needs no address named",
			prepare: withHolder,
			env:     map[string]string{staff.AdministratorVariable: ""},
			wantOut: roleHeld,
			after:   nobodyInvited,
		},
		{
			name:     "an account that holds no role stops no invitation: with no mailbox the first administrator is not invited, and the run says what to give",
			prepare:  withUser,
			wantCode: exitFailed,
			wantErr:  noMailbox,
			wantAlso: []string{whoseInvitation, mailboxHandStep},
			after:    nobodyInvited,
		},
		{
			name:     "an account that holds no role and no address named is refused: nobody administers the auth",
			prepare:  withUser,
			env:      map[string]string{staff.AdministratorVariable: ""},
			wantCode: exitFailed,
			wantErr:  "no first administrator is named: nobody holds the Super Administrator role yet",
			wantAlso: []string{whoseInvitation},
			after:    nobodyInvited,
		},
		{
			name:     "no account and no address named is refused",
			env:      map[string]string{staff.AdministratorVariable: ""},
			wantCode: exitFailed,
			wantErr:  "no first administrator is named",
			wantAlso: []string{whoseInvitation},
			after:    nobodyInvited,
		},
		{
			name:     "no account and a name that is no address is refused: the setting holds an email address, where it held a sign-in name",
			env:      map[string]string{staff.AdministratorVariable: "administrator"},
			wantCode: exitFailed,
			wantErr:  "is not an email address",
			wantAlso: []string{whoseInvitation},
			after:    nobodyInvited,
		},
		{
			name:     "a fresh invitation while the environment has no mailbox is refused, and the run says what to give",
			prepare:  migrated,
			args:     []string{"-reinvite-administrator", staff.Name},
			wantCode: exitFailed,
			wantErr:  noMailbox,
			wantAlso: []string{whoseInvitation, mailboxHandStep},
			after:    nobodyInvited,
		},
		{
			name:     "a fresh invitation once somebody holds the Super Administrator role is refused",
			prepare:  withHolder,
			args:     []string{"-reinvite-administrator", staff.Name},
			wantCode: exitFailed,
			wantErr:  "somebody holds the Super Administrator role, so no first administrator is invited",
			wantAlso: []string{whoseInvitation},
			after:    nobodyInvited,
		},
		{
			name:     "a fresh invitation is not refused for an account that holds no role: with no mailbox the run says what to give",
			prepare:  withUser,
			args:     []string{"-reinvite-administrator", staff.Name},
			wantCode: exitFailed,
			wantErr:  noMailbox,
			wantAlso: []string{whoseInvitation, mailboxHandStep},
			after:    nobodyInvited,
		},
		{
			name:     "a fresh invitation with another flag is refused before the database is touched",
			missing:  true,
			args:     []string{"-reinvite-administrator", staff.Name, "-seed"},
			wantCode: exitRefused,
			wantErr:  "-reinvite-administrator applies no migration and reports no version: it goes with no other flag",
		},
		{
			name:     "a fresh invitation with a retirement is refused before the database is touched",
			missing:  true,
			args:     []string{"-retire-secret", "APP_SECRET_KEY_1", "-reinvite-administrator", staff.Name},
			wantCode: exitRefused,
			wantErr:  "-reinvite-administrator applies no migration and reports no version: it goes with no other flag",
		},
		{
			name:     "a fresh invitation under a name no auth can have, since an auth's name has no hyphen, is refused before the database is touched, whatever auth the application gains",
			missing:  true,
			args:     []string{"-reinvite-administrator", "no-such-auth"},
			wantCode: exitRefused,
			wantErr:  "-reinvite-administrator no-such-auth: no auth of that name invites a first administrator; name one of staff",
		},
		{
			name:     "a fresh invitation without its auth is refused",
			missing:  true,
			args:     []string{"-reinvite-administrator"},
			wantCode: exitRefused,
			wantErr:  "flag needs an argument: -reinvite-administrator",
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
			name:     "a retirement of an older version of the application secret finds no value that names it and passes",
			prepare:  migrated,
			args:     []string{"-retire-secret", "APP_SECRET_KEY_1"},
			env:      map[string]string{"APP_SECRET_KEY_1": secretVersion(1), "APP_SECRET_KEY_2": secretVersion(2)},
			wantLast: "Retired APP_SECRET_KEY_1: no stored value names version 1\n",
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

			// The process's log, where a sender that only logs would write a message, link
			// included: read beside the command's two streams for a link.
			var stdout, stderr, logged bytes.Buffer
			log.SetOutput(&logged)
			t.Cleanup(func() {
				log.SetOutput(os.Stderr)
			})
			code := execute(t.Context(), tt.args, &stdout, &stderr)
			for where, wrote := range map[string]string{"stdout": stdout.String(), "stderr": stderr.String(), "the log": logged.String()} {
				if strings.Contains(wrote, "invitation=") || strings.Contains(wrote, "/login?") {
					t.Errorf("%s holds an invitation's link, which is for the mailed message alone:\n%s", where, wrote)
				}
			}
			if code != tt.wantCode {
				t.Fatalf("execute(%v) = %d, want %d; stdout:\n%s\nstderr:\n%s", tt.args, code, tt.wantCode, stdout.String(), stderr.String())
			}
			switch {
			case tt.wantLast != "":
				if problem := retirementProblem(stdout.String(), tt.wantLast); problem != "" {
					t.Error(problem)
				}
			case stdout.String() != tt.wantOut:
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.wantOut)
			}
			if tt.wantErr != "" && !strings.Contains(stderr.String(), tt.wantErr) {
				t.Errorf("stderr lacks %q:\n%s", tt.wantErr, stderr.String())
			}
			for _, want := range tt.wantAlso {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
				}
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

// someone is the id of the account the cases give the staff auth.
const someone = "00000000-0000-4000-8000-000000000001"

// withUser migrates the database and gives the staff auth one user who holds no role, as
// a person who signed up or a persona is: the auth has an account and nobody who
// administers it, so the first administrator is invited still.
func withUser(t *testing.T, db *initiator.SpannerDB) {
	t.Helper()

	migrated(t, db)
	mutation := spanner.Insert("StaffSessionUsers", []string{"Id", "Username", "CreatedAt"}, []any{someone, "someone", spanner.CommitTimestamp})
	if _, err := db.Apply(t.Context(), []*spanner.Mutation{mutation}); err != nil {
		t.Fatalf("spanner.Client.Apply() error = %v", err)
	}
}

// withHolder migrates the database and gives the staff auth one user who holds the Super
// Administrator role, as the development bootstrap's admin login does and as a persona
// of a cast may: somebody administers the auth, so nobody is invited.
func withHolder(t *testing.T, db *initiator.SpannerDB) {
	t.Helper()

	withUser(t, db)
	manager := openData(t).Staff().Access().UserManager()
	if err := manager.AddUserRoles(t.Context(), accesstypes.GlobalPolicyScope(), accesstypes.User(someone), accesstypes.SuperAdministrator); err != nil {
		t.Fatalf("access.UserManager.AddUserRoles() error = %v", err)
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

// nobodyInvited checks what a run that mailed nothing leaves: no invitation stands for
// the staff auth, so no link exists that anybody could open. A case on a database the
// run never migrated has no table to read and nothing to check.
func nobodyInvited(t *testing.T) {
	t.Helper()

	data := openData(t)
	invitations, err := data.Staff().Session().API().Invitations(t.Context())
	if err != nil {
		t.Fatalf("session.AuthAPI.Invitations() error = %v", err)
	}
	for _, invitation := range invitations {
		t.Errorf("an invitation stands for %s, want none: nothing was mailed", invitation.Email)
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

// rewrittenLine is the line a retirement writes for each secret field the application
// stores, before its last line: the field, and how many of its values were written
// again under the current version of the application secret.
var rewrittenLine = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*\.[A-Za-z][A-Za-z0-9]*: [0-9]+ value\(s\) written again under version [0-9]+$`)

// retirementProblem says what is wrong with what a retirement wrote, or nothing: stdout
// ends with the retirement's last line, and before it stands one line for each secret
// field the application stores, whatever their number (none in the application as
// rendered).
func retirementProblem(stdout, last string) string {
	before, found := strings.CutSuffix(stdout, last)
	if !found {
		return fmt.Sprintf("stdout = %q, want it to end with %q", stdout, last)
	}
	for line := range strings.Lines(before) {
		if !rewrittenLine.MatchString(strings.TrimSuffix(line, "\n")) {
			return fmt.Sprintf("stdout holds the line %q before %q, want only the lines of the secret fields written again", line, last)
		}
	}

	return ""
}

// TestRetirementProblem holds the reading of a retirement's output to what the command
// writes: its last line alone where the application stores no secret field, a line for
// each field before it where it stores some, and nothing else.
func TestRetirementProblem(t *testing.T) {
	t.Parallel()

	const last = "Retired APP_SECRET_KEY_1: no stored value names version 1\n"
	tests := []struct {
		name   string
		stdout string
		want   string
	}{
		{name: "no secret field", stdout: last},
		{name: "one secret field", stdout: "Vaults.Code: 0 value(s) written again under version 2\n" + last},
		{name: "two secret fields, one with values", stdout: "Vaults.Code: 3 value(s) written again under version 2\nKeys.Seed: 0 value(s) written again under version 2\n" + last},
		{name: "a line that is no field's", stdout: "Applying migrations\n" + last, want: `stdout holds the line "Applying migrations\n" before`},
		{name: "the last line missing", stdout: "Vaults.Code: 0 value(s) written again under version 2\n", want: "want it to end with"},
		{name: "something after the last line", stdout: last + "done\n", want: "want it to end with"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := retirementProblem(tt.stdout, last)
			if (tt.want == "") != (got == "") || !strings.Contains(got, tt.want) {
				t.Errorf("retirementProblem() = %q, want one containing %q", got, tt.want)
			}
		})
	}
}
