package resource

import (
	"context"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/freshness"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/spxscan"
	"github.com/impulseframework/impulse/tracer"
)

// Feature names a feature flag. An application declares its flags as constants of
// this type in its resources package, each with a doc comment that is the flag's
// description, and gates a resource, a field or an RPC method with @feature(<Constant>).
// The generator collects the declarations into Features(), the deploy step writes them
// into the FeatureFlags table (MigrateFeatures), and the running application reads the
// table into a FeatureSet the gated routes, decoders and the permission digest consult.
//
// A flag's value is its name as the table holds it: lowercase letters, digits and
// underscores, opening with a letter, at most 64 characters.
type Feature string

// FeatureDeclaration is one declared flag: its name, the description the dialog
// shows, and the constant that declared it.
type FeatureDeclaration struct {
	Name        Feature
	Description string
	Constant    string
}

// The names the generated code and the client library mirror.
const (
	// FeatureFlagsResource is the flags' resource: the FeatureFlags table, served as a
	// read-only resource (List, Read) on every session-serving outlet.
	FeatureFlagsResource accesstypes.Resource = "FeatureFlags"
	// FeatureFlagChangesTable is the audit table every flip writes a row into, in the
	// transaction that flips the flag.
	FeatureFlagChangesTable = "FeatureFlagChanges"
	// SetFeatureMethod is the RPC method that flips a flag, gated by Execute.
	SetFeatureMethod accesstypes.Resource = "SetFeature"
	// FeaturesRoute is the route every outlet serves the enabled flags on, to anyone
	// signed in: GET <prefix>/features answers {"enabled": [...]}.
	FeaturesRoute = "features"
	// FeatureFlagsRoute is the FeatureFlags resource's route segment under a session
	// outlet's prefix; the read route appends /{featureFlagName}.
	FeatureFlagsRoute = "feature-flags"
	// SetFeatureRoute is the SetFeature method's route segment under a session outlet's
	// prefix, named by the RPC route rule (the method name in kebab case).
	SetFeatureRoute = "set-feature"
	// FeatureFlagNameParam is the read route's parameter, named as every single-key read
	// route's is: the resource and its key field.
	FeatureFlagNameParam httpio.ParamType = "featureFlagName"
)

// The numbers.
const (
	// FeatureBackstop is how old a followed FeatureSet's copy may be when a request is
	// answered from it, and how often its loop rereads the table whether or not a signal
	// arrived, so an instance that missed a signal, or got no CPU to hear one, is at
	// most this far behind.
	FeatureBackstop = 5 * time.Minute
	// featureReadTimeout bounds one read of the table. A lookup that finds the copy due
	// waits for the read in flight when it came, if there is one, and for at most one
	// read after it, so a database that does not answer holds a lookup for less than
	// twice this before it is answered from the copy as it was.
	featureReadTimeout = 10 * time.Second
	// featureReadPause is how long the set leaves the table alone after a reread of its
	// own failed: the lookups that come inside it are answered from the copy as it was
	// and none of them reads, so a database that refuses at once is read once a pause
	// and not once a lookup, and the failure is logged at that rate. A signal that
	// arrives meanwhile stays recorded, and the first lookup after the pause rereads. A
	// flip of this instance (Reload) ends the pause: a database that has just taken a
	// write is not down, so the next lookup rereads.
	featureReadPause = time.Second
	// FeatureSignalTimeout bounds a flip's signal: past it the failure is logged and
	// the request answers, since the other instances reload at their backstop.
	FeatureSignalTimeout = 2 * time.Second
	// FeatureNameMaxLength is the longest name the table holds.
	FeatureNameMaxLength = 64
)

// featureNamePattern is the shape of a flag's name.
var featureNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ValidFeatureName reports whether name has a flag name's shape.
func ValidFeatureName(name string) bool {
	return featureNamePattern.MatchString(name)
}

// ValidateFeatureDeclarations refuses a declaration whose name is malformed and two
// declarations of one name, each refusal naming the constant.
func ValidateFeatureDeclarations(declared []FeatureDeclaration) error {
	var errs []error
	seen := make(map[Feature]string, len(declared))
	for _, d := range declared {
		if !ValidFeatureName(string(d.Name)) {
			errs = append(errs, errors.Newf("feature flag constant %s: %q is not a feature name; a name is 1 to %d characters of [a-z0-9_] opening with a letter", d.Constant, d.Name, FeatureNameMaxLength))

			continue
		}
		if prior, dup := seen[d.Name]; dup {
			errs = append(errs, errors.Newf("feature flag constant %s declares %q, which constant %s already declares", d.Constant, d.Name, prior))

			continue
		}
		seen[d.Name] = d.Constant
	}
	if len(errs) > 0 {
		return errors.Wrapf(errors.Join(errs...), "encountered %d feature flag declaration errors", len(errs))
	}

	return nil
}

// FeatureFlagsDDL is the CREATE TABLE statements of the FeatureFlags table and its
// audit table, FeatureFlagChanges, for the application database's type. An application
// copies the statements into a schema migration; a Lodestar test pins its migration to
// them.
//
// FeatureFlags holds one row per declared flag: Name (the key), Description, Enabled,
// UpdatedAt (the commit timestamp of the last write) and UpdatedBy (who wrote it: the
// migration process, or the principal that flipped it). FeatureFlagChanges holds one row
// per flip: Name, ChangedAt (the commit timestamp), Enabled (the value written) and
// ChangedBy. The audit rows are never deleted, so a flag's history outlives the flag.
func FeatureFlagsDDL(dbType DBType) []string {
	switch dbType {
	case SpannerDBType:
		return []string{
			`CREATE TABLE FeatureFlags (
  Name STRING(64) NOT NULL,
  Description STRING(MAX) NOT NULL,
  Enabled BOOL NOT NULL,
  UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  UpdatedBy STRING(MAX) NOT NULL,
) PRIMARY KEY (Name)`,
			`CREATE TABLE FeatureFlagChanges (
  Name STRING(64) NOT NULL,
  ChangedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp = true),
  Enabled BOOL NOT NULL,
  ChangedBy STRING(MAX) NOT NULL,
) PRIMARY KEY (Name, ChangedAt)`,
		}
	case PostgresDBType:
		return []string{
			`CREATE TABLE "FeatureFlags" (
  "Name" VARCHAR(64) NOT NULL,
  "Description" TEXT NOT NULL,
  "Enabled" BOOLEAN NOT NULL,
  "UpdatedAt" TIMESTAMPTZ NOT NULL,
  "UpdatedBy" TEXT NOT NULL,
  PRIMARY KEY ("Name")
)`,
			`CREATE TABLE "FeatureFlagChanges" (
  "Name" VARCHAR(64) NOT NULL,
  "ChangedAt" TIMESTAMPTZ NOT NULL,
  "Enabled" BOOLEAN NOT NULL,
  "ChangedBy" TEXT NOT NULL,
  PRIMARY KEY ("Name", "ChangedAt")
)`,
		}
	default:
		return nil
	}
}

// errFeaturesUnsupportedDatabase is the answer on a database the feature flags are not
// implemented for: the DDL exists for Postgres, the reads and writes do not yet.
var errFeaturesUnsupportedDatabase = errors.New("resource: feature flags are implemented for Spanner only")

// The FeatureFlags table's columns, as the writes and the row map name them, and the
// key's wire name.
const (
	flagNameColumn        = "Name"
	flagDescriptionColumn = "Description"
	flagEnabledColumn     = "Enabled"
	flagUpdatedAtColumn   = "UpdatedAt"
	flagUpdatedByColumn   = "UpdatedBy"
	flagChangedAtColumn   = "ChangedAt"
	flagChangedByColumn   = "ChangedBy"
	flagNameField         = "name"
)

// FeatureFlag is a row of the FeatureFlags table, and the resource the FeatureFlags
// routes serve.
type FeatureFlag struct {
	Name        string    `json:"name"        spanner:"Name"`
	Description string    `json:"description" spanner:"Description"`
	Enabled     bool      `json:"enabled"     spanner:"Enabled"`
	UpdatedAt   time.Time `json:"updatedAt"   spanner:"UpdatedAt"`
	UpdatedBy   string    `json:"updatedBy"   spanner:"UpdatedBy"`
}

// Resource names the table.
func (FeatureFlag) Resource() accesstypes.Resource {
	return FeatureFlagsResource
}

// DefaultConfig tracks no changes through the change-tracking table: the flips are
// audited in FeatureFlagChanges, in the same transaction.
func (FeatureFlag) DefaultConfig() Config {
	return Config{}
}

// featureFlagPatch is the metadata a flag's buffered mutation carries.
type featureFlagPatch struct {
	table     accesstypes.Resource
	patchType PatchType
	key       KeySet
}

func (p featureFlagPatch) PatchType() PatchType {
	return p.patchType
}

func (p featureFlagPatch) PrimaryKey() KeySet {
	return p.key
}

func (p featureFlagPatch) Resource() accesstypes.Resource {
	return p.table
}

// readFeatureFlags reads every row of the table, by name.
func readFeatureFlags(ctx context.Context, txn ReadOnlyTransaction, dbType DBType) ([]FeatureFlag, error) {
	if dbType != SpannerDBType {
		return nil, errFeaturesUnsupportedDatabase
	}
	var rows []FeatureFlag
	stmt := spanner.Statement{SQL: "SELECT Name, Description, Enabled, UpdatedAt, UpdatedBy FROM FeatureFlags ORDER BY Name"}
	if err := spxscan.Select(ctx, txn.SpannerReadOnlyTransaction(), &rows, stmt); err != nil {
		return nil, errors.Wrap(err, "spxscan.Select()")
	}

	return rows, nil
}

// MigrateFeatures brings the FeatureFlags table to the declared flags, in one
// transaction: a declared flag the table lacks is inserted disabled, a flag the table
// holds keeps its Enabled and takes the declared description, and a flag no longer
// declared is deleted. The audit rows are untouched. It runs in the deploy step beside
// the role check, before the release takes traffic.
func MigrateFeatures(ctx context.Context, db Client, declared []FeatureDeclaration) error {
	if err := ValidateFeatureDeclarations(declared); err != nil {
		return err
	}
	if db.DBType() != SpannerDBType {
		return errFeaturesUnsupportedDatabase
	}

	eventSource := ProcessEvent("MigrateFeatures")
	if err := db.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
		held, err := readFeatureFlags(ctx, txn, txn.DBType())
		if err != nil {
			return err
		}
		byName := make(map[Feature]FeatureFlag, len(held))
		for _, row := range held {
			byName[Feature(row.Name)] = row
		}

		for _, d := range declared {
			row, ok := byName[d.Name]
			switch {
			case !ok:
				if err := bufferFeatureFlag(txn, CreatePatchType, d.Name, map[string]any{
					flagNameColumn:        string(d.Name),
					flagDescriptionColumn: d.Description,
					flagEnabledColumn:     false,
					flagUpdatedAtColumn:   spanner.CommitTimestamp,
					flagUpdatedByColumn:   eventSource,
				}); err != nil {
					return err
				}
			case row.Description != d.Description:
				if err := bufferFeatureFlag(txn, UpdatePatchType, d.Name, map[string]any{
					flagNameColumn:        string(d.Name),
					flagDescriptionColumn: d.Description,
				}); err != nil {
					return err
				}
			}
			delete(byName, d.Name)
		}

		// What is left in the map is held and not declared.
		undeclared := make([]Feature, 0, len(byName))
		for name := range byName {
			undeclared = append(undeclared, name)
		}
		slices.Sort(undeclared)
		for _, name := range undeclared {
			if err := bufferFeatureFlag(txn, DeletePatchType, name, nil); err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		return errors.Wrap(err, "resource.Client.ExecuteFunc()")
	}

	return nil
}

// bufferFeatureFlag buffers one mutation of the FeatureFlags table.
func bufferFeatureFlag(txn ReadWriteTransaction, patchType PatchType, name Feature, patch map[string]any) error {
	meta := featureFlagPatch{table: FeatureFlagsResource, patchType: patchType, key: KeySet{}.Add(flagNameColumn, string(name))}
	if err := txn.BufferMap(meta, patch); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.BufferMap()")
	}

	return nil
}

// SetFeatureEnabled flips one flag in its own transaction: the row takes the value,
// the commit timestamp and eventSource, and the audit table gains the flip's row. A
// name the table does not hold is a not-found error. SetFeatureHandler flips through
// the same write; a bootstrap or a test flips a flag directly through this.
func SetFeatureEnabled(ctx context.Context, db Client, name Feature, enabled bool, eventSource string) error {
	if db.DBType() != SpannerDBType {
		return errFeaturesUnsupportedDatabase
	}
	if err := db.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
		return bufferFeatureWrite(ctx, txn, name, enabled, eventSource)
	}); err != nil {
		return errors.Wrap(err, "resource.Client.ExecuteFunc()")
	}

	return nil
}

// bufferFeatureWrite buffers a flip into the transaction: the flag's row updated and the
// audit row inserted, both stamped with the commit timestamp. The row is read first,
// so an unknown name is refused as not found before anything is written.
func bufferFeatureWrite(ctx context.Context, txn ReadWriteTransaction, name Feature, enabled bool, eventSource string) error {
	if txn.DBType() != SpannerDBType {
		return errFeaturesUnsupportedDatabase
	}
	if !ValidFeatureName(string(name)) {
		return httpio.NewNotFoundMessagef("feature flag %q does not exist", name)
	}
	var held []struct {
		Name string `spanner:"Name"`
	}
	stmt := spanner.Statement{
		SQL:    "SELECT Name FROM FeatureFlags WHERE Name = @name",
		Params: map[string]any{flagNameField: string(name)},
	}
	if err := spxscan.Select(ctx, txn.SpannerReadOnlyTransaction(), &held, stmt); err != nil {
		return errors.Wrap(err, "spxscan.Select()")
	}
	if len(held) == 0 {
		return httpio.NewNotFoundMessagef("feature flag %q does not exist", name)
	}

	if err := bufferFeatureFlag(txn, UpdatePatchType, name, map[string]any{
		flagNameColumn:      string(name),
		flagEnabledColumn:   enabled,
		flagUpdatedAtColumn: spanner.CommitTimestamp,
		flagUpdatedByColumn: eventSource,
	}); err != nil {
		return err
	}
	audit := featureFlagPatch{
		table:     accesstypes.Resource(FeatureFlagChangesTable),
		patchType: CreatePatchType,
		key:       KeySet{}.Add(flagNameColumn, string(name)).Add(flagChangedAtColumn, spanner.CommitTimestamp),
	}
	if err := txn.BufferMap(audit, map[string]any{
		flagNameColumn:      string(name),
		flagChangedAtColumn: spanner.CommitTimestamp,
		flagEnabledColumn:   enabled,
		flagChangedByColumn: eventSource,
	}); err != nil {
		return errors.Wrap(err, "ReadWriteTransaction.BufferMap()")
	}

	return nil
}

// FeatureSet is one instance's copy of the FeatureFlags table: what the gated routes,
// the decoders and the permission digest consult. LoadFeatures reads it once at start;
// Follow keeps it current until its context ends; Reload rereads it now. A nil
// FeatureSet answers every flag off, so an application that wires none fails closed on
// every gate. It is safe for concurrent use.
//
// Once followed, the freshness is the request's own. A lookup that finds the copy older
// than the backstop, or behind a features signal this instance received, rereads the
// table before it answers, and one reread serves every request waiting at that moment; a
// reread that fails is logged and the copy answers as it was. Follow's loop rereads on
// every signal and at the backstop interval too, ahead of the requests, so on an
// instance that is serving a lookup finds the copy fresh and reads nothing. Nothing
// depends on the loop: an instance that got no CPU between requests answers its next
// one from a read no older than the backstop.
type FeatureSet struct {
	db       Client
	backstop time.Duration

	// fresh decides, for every lookup, whether the table is reread before the lookup
	// answers: from Follow until its context ends, when the copy is past the backstop
	// or behind a signal. Nil in a set LoadFeatures did not build, which answers from
	// what it holds.
	fresh *freshness.Gate
	// clock is the set's present: the wall clock outside a test.
	clock func() time.Time
	// logf logs a reread that failed: through the context's logger outside a test.
	logf func(ctx context.Context, format string, v ...any)
	// noLoop is a test's stand-in for an instance that gets no CPU between requests:
	// Follow starts no loop, so nothing but a lookup rereads the table.
	noLoop bool

	mu    sync.RWMutex
	flags map[Feature]FeatureFlag
}

// FeatureOption configures a FeatureSet at load.
type FeatureOption func(*FeatureSet)

// WithFeatureBackstop sets how old a followed copy may be when a request is answered
// from it, which is also how often Follow's loop rereads the table without a signal;
// FeatureBackstop by default. Tests shorten it.
func WithFeatureBackstop(d time.Duration) FeatureOption {
	return func(s *FeatureSet) {
		s.backstop = d
	}
}

// LoadFeatures reads the FeatureFlags table into a FeatureSet. The copy stays as read
// until Reload, and is kept current from Follow.
func LoadFeatures(ctx context.Context, db Client, opts ...FeatureOption) (*FeatureSet, error) {
	if db.DBType() != SpannerDBType {
		return nil, errFeaturesUnsupportedDatabase
	}
	s := newFeatureSet(db, opts...)
	if err := s.Reload(ctx); err != nil {
		return nil, err
	}

	return s, nil
}

// newFeatureSet builds a FeatureSet over the database that holds no flag and has read
// nothing.
func newFeatureSet(db Client, opts ...FeatureOption) *FeatureSet {
	s := &FeatureSet{db: db, backstop: FeatureBackstop, clock: time.Now, logf: logErrorf, flags: make(map[Feature]FeatureFlag)}
	for _, opt := range opts {
		opt(s)
	}
	s.fresh = freshness.New(s.load, s.backstop, featureReadTimeout, featureReadPause, s.now, s.rereadFailed)

	return s
}

// now is the set's present, read through clock at every call: a test sets the clock
// after the set is loaded.
func (s *FeatureSet) now() time.Time {
	return s.clock()
}

// rereadFailed logs a reread the set made itself that failed: a lookup's or the loop's.
// ctx is the one Follow was given.
func (s *FeatureSet) rereadFailed(ctx context.Context, err error) {
	s.logf(ctx, "feature flags: rereading the table failed; the copy stays as it was, and the first request after %s rereads it: %v", featureReadPause, err)
}

// Reload rereads the table now: what a flip's handler calls after its commit, so the
// instance that flipped answers from the flip at once. It reads under ctx, the
// caller's, so it is called after a commit and never inside a transaction. A failed
// read leaves the copy as it was and the change recorded: on a set that is followed,
// the next lookup rereads the table under the set's own context, in the pause after a
// reread of the set's own failed too, since the database has taken a write since. So a
// caller whose context had ended does not leave the instance that flipped behind its
// own flip until the backstop. A nil FeatureSet, and one LoadFeatures did not build,
// has nothing to reread and says so.
func (s *FeatureSet) Reload(ctx context.Context) error {
	if s == nil || s.fresh == nil {
		return errors.New("no feature set is loaded")
	}
	if err := s.fresh.Read(ctx); err != nil {
		return errors.Wrap(err, "freshness.Gate.Read()")
	}

	return nil
}

// load is the set's one read of its table, made by fresh, one at a time: it reads the
// flags and replaces the copy whole.
func (s *FeatureSet) load(ctx context.Context) error {
	txn := s.db.ReadOnlyTransaction()
	defer txn.Close()

	rows, err := readFeatureFlags(ctx, txn, s.db.DBType())
	if err != nil {
		return err
	}
	flags := make(map[Feature]FeatureFlag, len(rows))
	for _, row := range rows {
		flags[Feature(row.Name)] = row
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.flags = flags

	return nil
}

// freshen is what every lookup does first: it rereads the table when the copy is past
// the backstop or behind a signal, in a set that is followed. A reread that failed was
// logged, and the copy as it was answers.
func (s *FeatureSet) freshen() {
	if s.fresh != nil {
		_ = s.fresh.Freshen()
	}
}

// Enabled reports whether the flag is on. A flag the table does not hold, and every
// flag of a nil FeatureSet, is off.
func (s *FeatureSet) Enabled(name Feature) bool {
	if s == nil {
		return false
	}
	s.freshen()
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.flags[name].Enabled
}

// Flag returns the flag's row and whether the table holds it.
func (s *FeatureSet) Flag(name Feature) (FeatureFlag, bool) {
	if s == nil {
		return FeatureFlag{}, false
	}
	s.freshen()
	s.mu.RLock()
	defer s.mu.RUnlock()
	flag, ok := s.flags[name]

	return flag, ok
}

// EnabledNames lists the flags that are on, sorted: the features route's answer.
func (s *FeatureSet) EnabledNames() []string {
	names := make([]string, 0)
	if s == nil {
		return names
	}
	s.freshen()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for name, flag := range s.flags {
		if flag.Enabled {
			names = append(names, string(name))
		}
	}
	slices.Sort(names)

	return names
}

// Follow keeps the copy current until ctx ends. From here a lookup that finds the copy
// past the backstop, or behind a signal of KindFeatures, rereads the table before it
// answers, under ctx and never under a request's context; and a goroutine does the same
// ahead of the requests, on every signal the subscriber delivers and at the backstop
// interval regardless. A signal is recorded the moment it arrives, before any reread is
// tried, so a flip that was announced to this instance waits for no goroutine: the old
// copy answers only while the table cannot be read. The subscriber is the application's
// live service, which every application wires; nil is refused. A failed reread is
// logged and the copy stays as it was; the table is left alone for a moment
// (featureReadPause), and the first request after that tries again, or the next one
// after a flip of this instance.
func (s *FeatureSet) Follow(ctx context.Context, signals SignalSubscriber) error {
	if s == nil || s.fresh == nil {
		return errors.New("no feature set is loaded")
	}
	if signals == nil {
		return errors.New("resource.FeatureSet.Follow: a signal subscriber is required; the application wires its live service")
	}
	wake := make(chan struct{}, 1)
	stop, err := signals.Subscribe(KindFeatures, func() {
		s.fresh.Nudge()
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	if err != nil {
		return errors.Wrap(err, "SignalSubscriber.Subscribe()")
	}
	s.fresh.Keep(ctx)
	if s.noLoop {
		context.AfterFunc(ctx, stop)

		return nil
	}
	go s.follow(ctx, wake, stop)

	return nil
}

// follow is Follow's loop, an accelerator and nothing more: on every signal and every
// backstop tick it asks for the reread a lookup would make, sooner, until ctx ends.
func (s *FeatureSet) follow(ctx context.Context, signals <-chan struct{}, stop func()) {
	defer stop()
	ticker := time.NewTicker(s.backstop)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick()
		case <-signals:
			// The subscription recorded the signal as it arrived. A failure of the
			// reread is logged where it happens (rereadFailed).
			s.freshen()
		}
	}
}

// tick is the loop's backstop tick: it rereads the table whatever the copy's age. The
// tick asks for the reread as a signal does, since the copy the loop read one backstop
// ago is not yet past the backstop when the tick fires: left to the bound alone the
// loop would reread on every other tick, and the request that found the backstop ended
// would reread on the rest.
func (s *FeatureSet) tick() {
	s.fresh.Nudge()
	s.freshen()
}

// FeatureGates is what the generated code declares gated, keyed the way the permission
// digest keys its entries: a resource or RPC method by its name, a field by
// "<Resource>.<field>". A gated-off resource takes its fields with it.
type FeatureGates map[accesstypes.Resource]Feature

// Off reports whether the digest key is behind a flag that is off: its own flag, or
// its resource's flag for a field key.
func (g FeatureGates) Off(features *FeatureSet, key accesstypes.Resource) bool {
	if feature, ok := g[key]; ok && !features.Enabled(feature) {
		return true
	}
	if res, _, isField := strings.Cut(string(key), "."); isField {
		if feature, ok := g[accesstypes.Resource(res)]; ok && !features.Enabled(feature) {
			return true
		}
	}

	return false
}

// filter drops every entry of the digest that is behind a flag that is off.
func (g FeatureGates) filter(features *FeatureSet, digest accesstypes.PermissionDigest) {
	if len(g) == 0 {
		return
	}
	for key := range digest {
		if g.Off(features, key) {
			delete(digest, key)
		}
	}
}

// FeatureGuard is the middleware the generated routes wrap a gated resource's and a
// gated method's routes in: while the flag is off the route answers exactly as the
// outlet's not-found handler does, so a gated-off route is indistinguishable from one
// that does not exist.
func FeatureGuard(features *FeatureSet, feature Feature) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !features.Enabled(feature) {
				FeatureNotFound(w)

				return
			}
			next(w, r)
		}
	}
}

// FeatureNotFound answers as the generated router's not-found handler does under every
// outlet prefix: the one answer a gated-off route gives.
func FeatureNotFound(w http.ResponseWriter) {
	http.Error(w, "Not Found", http.StatusNotFound)
}

// FeatureApp is the application surface the feature flag handlers draw on: the decoder
// seam, the database client, the cursor key the flags' list pages with, and the
// FeatureSet the application loaded at start.
type FeatureApp interface {
	DecoderAccessor
	ResourceClient() Client
	CursorKey() *CursorKey
	FeatureSet() *FeatureSet
}

// FeaturesResponse is the features route's answer: the names of the flags that are on.
type FeaturesResponse struct {
	Enabled []string `json:"enabled"`
}

// FeaturesHandler serves the enabled flags to anyone the outlet let through: no
// permission beyond being signed in, since a page needs to know which gated surfaces
// to show before it holds any grant on them. The generated router registers it at
// GET <prefix>/features on every outlet, the API-key outlets included.
func FeaturesHandler(features *FeatureSet) http.HandlerFunc {
	return httpio.Log(func(w http.ResponseWriter, _ *http.Request) error {
		return httpio.NewEncoder(w).Ok(FeaturesResponse{Enabled: features.EnabledNames()})
	})
}

// featureFlagRequest is the FeatureFlags routes' request struct: the row's wire shape,
// the name as the key.
type featureFlagRequest struct {
	Name        string    `json:"name"        index:"true" perm:"-"`
	Description string    `json:"description"`
	Enabled     bool      `json:"enabled"`
	UpdatedAt   time.Time `json:"updatedAt"`
	UpdatedBy   string    `json:"updatedBy"`
}

// FeatureFlagFieldTags are the FeatureFlags routes' field registrations, the way the
// generator registers a generated resource's: what a List or Read grant on FeatureFlags
// may name. The generator registers them into every application's collection.
func FeatureFlagFieldTags() []FieldTags {
	t := reflect.TypeFor[featureFlagRequest]()
	fields := make([]FieldTags, 0, t.NumField())
	for field := range t.Fields() {
		fields = append(fields, FieldTagsFromStructTag(accesstypes.Field(field.Name), field.Tag))
	}

	return fields
}

// FeatureFlagsQueryKeys are the wire names a FeatureFlags list sorts and filters by: the
// name alone, which is the key.
func FeatureFlagsQueryKeys() (order, keys []accesstypes.Tag) {
	return []accesstypes.Tag{flagNameField}, nil
}

// featureFlagsOrder is the list's declared order: by name.
var featureFlagsOrder = Paging{Order: []SortField{{Field: flagNameColumn, Direction: SortAscending}}}

// FeatureFlagsHandler serves the flags as a list, to a List grant on FeatureFlags: the
// FeatureAdministrator's dialog reads it. The generated router registers it at
// GET <prefix>/feature-flags on every session-serving outlet.
func FeatureFlagsHandler(a FeatureApp, collection *GeneratedCollection) http.HandlerFunc {
	decoder := MustNewQueryDecoder[FeatureFlag, featureFlagRequest](collection, accesstypes.List).
		WithCursorKey(a.CursorKey()).
		WithPaging(featureFlagsOrder)

	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		querySet, err := decoder.Decode(r, a.UserPermissions(r), accesstypes.GlobalScope())
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		page := querySet.Page()
		if err := page.Count(ctx, a.ResourceClient()); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		resp := make([]map[string]any, 0)
		for row, err := range querySet.List(ctx, a.ResourceClient()) {
			if err != nil {
				return httpio.NewEncoder(w).ClientMessage(ctx, err)
			}
			if !page.Add(row) {
				break
			}
			resp = append(resp, featureFlagMap(querySet.Fields(), row))
		}
		if page.Reversed() {
			slices.Reverse(resp)
		}
		if err := page.WriteHeaders(w, r); err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(resp)
	})
}

// FeatureFlagHandler serves one flag, to a Read grant on FeatureFlags. The generated
// router registers it at GET <prefix>/feature-flags/{featureFlagName} on every
// session-serving outlet.
func FeatureFlagHandler(a FeatureApp, collection *GeneratedCollection) http.HandlerFunc {
	decoder := MustNewQueryDecoder[FeatureFlag, featureFlagRequest](collection, accesstypes.Read)

	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		name := httpio.Param[string](r, FeatureFlagNameParam)
		querySet, err := decoder.Decode(r, a.UserPermissions(r), accesstypes.GlobalScope())
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		querySet.SetKey(flagNameColumn, name)

		row, err := querySet.Read(ctx, a.ResourceClient())
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}

		return httpio.NewEncoder(w).Ok(featureFlagMap(querySet.Fields(), row))
	})
}

// featureFlagMap renders a flag's row the way the generated handlers render theirs: the
// requested fields by wire name, a masked cell left out, the capability envelope when
// the request asked for one.
func featureFlagMap(fields []accesstypes.Field, row *Row[FeatureFlag]) map[string]any {
	rmap := make(map[string]any, len(fields))
	for _, field := range fields {
		switch field {
		case flagNameColumn:
			if !row.Masked("name") {
				rmap["name"] = row.Data.Name
			}
		case flagDescriptionColumn:
			if !row.Masked("description") {
				rmap["description"] = row.Data.Description
			}
		case flagEnabledColumn:
			if !row.Masked("enabled") {
				rmap["enabled"] = row.Data.Enabled
			}
		case flagUpdatedAtColumn:
			if !row.Masked("updatedAt") {
				rmap["updatedAt"] = row.Data.UpdatedAt
			}
		case flagUpdatedByColumn:
			if !row.Masked("updatedBy") {
				rmap["updatedBy"] = row.Data.UpdatedBy
			}
		}
	}
	if capabilities := row.Capabilities(); capabilities != nil {
		rmap[CapabilitiesProperty] = capabilities
	}

	return rmap
}

// SetFeatureRequest is the SetFeature method's body: the flag and the value to write.
type SetFeatureRequest struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// SetFeatureResult is the SetFeature method's answer: the flag as the table holds it
// after the write, read back from the reloaded copy.
type SetFeatureResult struct {
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// SetFeatureHandler flips a flag, to an Execute grant on SetFeature: the row takes the
// value with the commit timestamp and the principal, the audit table gains the flip's
// row in the same transaction, this instance's copy reloads after the commit, then
// KindFeatures is signaled, before the answer (bounded by FeatureSignalTimeout; a
// failure is logged, since the other instances reload at their backstop), and the
// answer is the flag from the reloaded copy. The reload comes first so that this
// instance answers from the flip while the signal is being sent, which waits for the
// channel; the signal does not run under the request's context, so a request that ends
// once the flip is committed still tells the other instances. An unknown name is 404. A
// dry run (X-Dry-Run: true)
// runs the frame and rolls back, as every transaction-form method does. The signaler is
// the application's live service, which every application wires; the handler refuses
// to be built without one. The generated router registers it at
// POST <prefix>/set-feature on every session-serving outlet.
func SetFeatureHandler(a FeatureApp, collection *GeneratedCollection, signals Signaler) http.HandlerFunc {
	if signals == nil {
		panic("resource.SetFeatureHandler: a signaler is required; the application wires its live service")
	}
	decoder := MustNewRPCDecoder[SetFeatureRequest](a, SetFeatureMethod, accesstypes.Execute).WithCollection(collection)

	return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		ctx, span := tracer.Start(r.Context())
		defer span.End()

		req, err := decoder.Decode(r, accesstypes.GlobalScope())
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		features := a.FeatureSet()
		if features == nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.New("resource.SetFeatureHandler: the application wires no FeatureSet"))
		}

		name := Feature(req.Name)
		eventSource := UserEvent(ctx)
		// SetFeature declares no warning: a request that asks for one is refused as a
		// method's frame refuses it, and never sets the flag.
		asksWarning, err := AsksWarning(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if asksWarning {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewBadRequestMessagef("SetFeature declares no warning: send the call without the %s header", WarningHeader))
		}
		dryRun, err := IsDryRun(r)
		if err != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, err)
		}
		if err := a.ResourceClient().ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
			if err := bufferFeatureWrite(ctx, txn, name, req.Enabled, eventSource); err != nil {
				return err
			}
			if dryRun {
				return ErrDryRun
			}

			return nil
		}); err != nil {
			if dryRun && DryRunRolledBack(err) {
				return httpio.NewEncoder(w).Ok(nil)
			}

			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(err, "resource.Client.ExecuteFunc()"))
		}

		// This instance first: the flip is on its copy, or recorded for its next lookup
		// to read, before the other instances are told, so a request that comes while the
		// signal is being sent is answered from the flip. The signal is sent whatever
		// became of the reload, and of the request while it reloaded.
		reloadErr := features.Reload(ctx)
		signalFeatures(ctx, signals)
		if reloadErr != nil {
			return httpio.NewEncoder(w).ClientMessage(ctx, errors.Wrap(reloadErr, "resource.FeatureSet.Reload()"))
		}
		flag, ok := features.Flag(name)
		if !ok {
			return httpio.NewEncoder(w).ClientMessage(ctx, httpio.NewNotFoundMessagef("feature flag %q does not exist", name))
		}

		return httpio.NewEncoder(w).Ok(SetFeatureResult{Name: flag.Name, Enabled: flag.Enabled, UpdatedAt: flag.UpdatedAt})
	})
}

// signalFeatures signals the other instances that the flags changed, within the
// timeout; a failure is logged and never fails the request. The flip is committed by
// now, so the signal is sent whatever has become of the request: it keeps the request's
// values and runs under the timeout alone, not under the request's context. A request
// that ended while this instance reloaded (the page that flipped is gone) would
// otherwise leave every other instance on the old flag until its backstop. The signal
// is sent inside the handler's call, and nothing is left running when it returns.
func signalFeatures(ctx context.Context, signals Signaler) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), FeatureSignalTimeout)
	defer cancel()
	if err := signals.Signal(ctx, KindFeatures); err != nil {
		logger.FromCtx(ctx).Errorf("feature flags: signaling the change failed; the other instances reload at their backstop: %v", err)
	}
}
