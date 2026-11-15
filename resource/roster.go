package resource

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/spxscan"
	"github.com/impulseframework/impulse/spxscan/spxapi"
)

// TenantSignals is the signal channel a TenantRoster follows: what the live service
// offers (resource.Signaler and resource.SignalSubscriber together). Signal tells every
// instance that a kind changed; Subscribe runs onSignal on every signal of the kind after
// the subscription began, on the channel's own goroutine, and stop ends the subscription.
// The roster never opens a listener of its own: it subscribes to the application's one
// watch, for KindTenants.
type TenantSignals interface {
	Signaler
	SignalSubscriber
}

// TenantBackstop is how often a TenantRoster rereads its table whether or not a signal
// arrived: the correctness guard behind the push, so an instance that missed a signal is
// at most this far behind.
const TenantBackstop = 5 * time.Minute

// errTenantRosterUnsupportedDatabase is the answer on a database the roster's read is
// not implemented for: the statement renders for Postgres, the read does not yet.
var errTenantRosterUnsupportedDatabase = errors.New("resource: the tenant roster's read is implemented for Spanner only")

// tenantSet is the roster's copy of the tenants: each key with what the tenant declares
// about who may enter it, nil for a tenant that declares nothing (or when the roster
// reads no declaration). The value behind the atomic pointer, replaced whole and never
// written in place.
type tenantSet map[accesstypes.Domain]*signin.TenantDeclaration

// tenantNames is the roster's copy of each tenant's name, read beside the keys when the
// roster reads names (WithTenantNames); replaced whole with the set, never written in place.
type tenantNames map[accesstypes.Domain]string

// TenantDeclarationColumns names where the tenant record keeps its declaration of who
// may enter it (signin.TenantDeclaration), so the roster reads the declaration beside
// the keys and the sign-in policy reads it in its own transaction
// (NewTenantDeclarationReader): the record's directory-only column, and the directories
// record's table with its tenant, method and connection columns. The generated
// New<Record>Roster and New<Record>DeclarationReader constructors supply them from the
// @directoryOnly and @tenantDirectory declarations; an application names none of them.
type TenantDeclarationColumns struct {
	// DirectoryOnly is the tenant record's BOOL column saying the tenant's people enter
	// through its directories only, so a password does not reach it; empty when the
	// record declares none, and every tenant admits a password.
	DirectoryOnly string
	// DirectoriesTable is the directories record's table, one row per directory a tenant
	// admits its people through: TenantColumn holds the tenant's key, MethodColumn the
	// sign-in method as sessioninfo.AuthMethod spells it (google, azure, workos) and
	// ConnectionColumn the connection the method's identities carry (a Google Workspace
	// by its domain, a Microsoft Entra tenant by its tenant id).
	DirectoriesTable string
	TenantColumn     string
	MethodColumn     string
	ConnectionColumn string
	// DefaultSeatColumn is the directories record's column naming the role a person the
	// directory vouches for is seated with in the tenant at their first sign-in, NULL or
	// empty for none (the tenant admits its people through that directory by invitation
	// only); empty when the record carries no such column, and every directory admits
	// by invitation only. Read by the sign-in's directory reader
	// (NewTenantDirectoryReader), never by the roster.
	DefaultSeatColumn string
}

// declared reports whether the roster reads a declaration: the columns are given and
// name the directories record.
func (c *TenantDeclarationColumns) declared() bool {
	return c != nil && c.DirectoriesTable != ""
}

// TenantRoster is one instance's copy of the tenant record's keys: the set of tenants
// the application serves, which the generated DomainGuard and the consolidated
// dispatcher ask before a tenant-scoped request runs. Start reads the set once and then
// keeps it current until the context ends: on every tenants signal the channel delivers
// and at the backstop interval regardless. The generated write paths of the tenant
// record call Add and Remove after their commit, so the instance that wrote a tenant
// serves it at once, and signal the tenants kind so every other instance reloads.
//
// With WithTenantDeclarations the copy carries what each tenant declares about who may
// enter it beside its key, read in the same snapshot, and Declarations answers the
// generated DomainGuard's and the session's tenant list's question from it
// (TenantDeclarations) with no read and no wait; the generated write paths of the tenant
// record and of the directories record call Reload after their commit, so a declaration
// changed on this instance decides the next request here, and signal the tenants kind
// so every other instance reloads.
//
// The set sits behind an atomic pointer, so Has never waits on a reload or a writer and
// performs no read: a domain the set does not hold is unknown, whatever the table says
// at that moment. A nil roster holds no tenant, so an application that wires none fails
// closed on every tenant-scoped route. It is safe for concurrent use.
type TenantRoster struct {
	client       Client
	table        string
	keyColumn    string
	declarations *TenantDeclarationColumns
	nameColumn   string
	backstop     time.Duration
	signals      TenantSignals

	// writes serializes the writers (Add, Remove, reload), each of which replaces the
	// set whole; readers load the pointer and never take it. names is replaced with
	// the set on a reload, read beside it.
	writes sync.Mutex
	set    atomic.Pointer[tenantSet]
	names  atomic.Pointer[tenantNames]
}

// TenantRosterOption configures a TenantRoster at construction.
type TenantRosterOption func(*TenantRoster)

// WithTenantSignals wires the application's signal channel: Start subscribes the roster
// to the tenants kind on it, so a tenant another instance wrote reaches this one within
// a moment instead of at the backstop. Without it the backstop alone keeps the copy
// current.
func WithTenantSignals(s TenantSignals) TenantRosterOption {
	return func(r *TenantRoster) {
		r.signals = s
	}
}

// WithTenantBackstop sets how often Start rereads the table without a signal;
// TenantBackstop by default. Tests shorten it.
func WithTenantBackstop(d time.Duration) TenantRosterOption {
	return func(r *TenantRoster) {
		r.backstop = d
	}
}

// WithTenantDeclarations has the roster read what each tenant declares about who may
// enter it beside its key: the columns the generated New<Record>Roster constructor
// supplies from the @directoryOnly and @tenantDirectory declarations. Without it every
// tenant's declaration is nil, which declares nothing.
func WithTenantDeclarations(columns *TenantDeclarationColumns) TenantRosterOption {
	return func(r *TenantRoster) {
		r.declarations = columns
	}
}

// NewTenantRoster builds the roster over the tenant record's table, reading the key
// column into the set. The generated New<Record>Roster constructor supplies the table
// and the column from the @tenant declaration; an application calls that one. Nothing
// is read until Start.
// WithTenantNames has the roster read each tenant's name from the record's name column
// (the field the tenant record marks @tenantName) in the snapshot of its keys, so Name
// answers from the copy and the session's tenant list names each tenant
// (WithDomainNames) with no read of its own. The generated New<Record>Roster passes it
// when the record marks a name; an empty column reads none.
func WithTenantNames(column string) TenantRosterOption {
	return func(r *TenantRoster) {
		r.nameColumn = column
	}
}

func NewTenantRoster(client Client, table, keyColumn string, opts ...TenantRosterOption) *TenantRoster {
	r := &TenantRoster{client: client, table: table, keyColumn: keyColumn, backstop: TenantBackstop}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// Start loads the set once, failing when the read fails, so an application never
// starts serving on an empty roster it could not fill, and then keeps it current until
// ctx ends: a goroutine rereads the table on every tenants signal (when a channel is
// wired) and at the backstop interval regardless. A failed reread is logged and the
// copy stays as it was until the next.
func (r *TenantRoster) Start(ctx context.Context) error {
	if r == nil {
		return errors.New("resource: no tenant roster is wired")
	}
	if err := r.Reload(ctx); err != nil {
		return err
	}

	signals := make(chan struct{}, 1)
	stop := func() {}
	if r.signals != nil {
		var err error
		stop, err = r.signals.Subscribe(KindTenants, func() {
			select {
			case signals <- struct{}{}:
			default:
			}
		})
		if err != nil {
			return errors.Wrap(err, "resource.TenantSignals.Subscribe()")
		}
	}
	go r.refresh(ctx, signals, stop)

	return nil
}

// refresh is Start's loop: a reread per signal and per backstop tick, until ctx ends.
func (r *TenantRoster) refresh(ctx context.Context, signals <-chan struct{}, stop func()) {
	defer stop()
	ticker := time.NewTicker(r.backstop)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-signals:
		}
		if err := r.Reload(ctx); err != nil && ctx.Err() == nil {
			logger.FromCtx(ctx).Errorf("tenants: rereading the %s table failed; the roster stays as it was until the next signal or the backstop: %v", r.table, err)
		}
	}
}

// Reload rereads the table, the declarations with it, and replaces the set whole: what
// the tenant record's and the directories record's generated write paths call after
// their commit, so the instance that wrote serves the change at its next request, and
// what Start calls on every tenants signal and backstop tick. A failed read leaves the
// set as it was. A nil roster reads nothing.
func (r *TenantRoster) Reload(ctx context.Context) error {
	if r == nil {
		return errors.New("resource: no tenant roster is wired")
	}
	set, names, err := r.read(ctx)
	if err != nil {
		return err
	}

	r.writes.Lock()
	defer r.writes.Unlock()
	r.set.Store(&set)
	r.names.Store(&names)

	return nil
}

// read runs the roster's statements outside any request transaction, in one read-only
// snapshot, and returns the tenants as a set, with their names when the roster reads
// names (nil otherwise).
func (r *TenantRoster) read(ctx context.Context) (tenantSet, tenantNames, error) {
	dbType := r.client.DBType()
	statements, err := tenantRosterStatements(dbType, r.table, r.keyColumn, r.declarations, false)
	if err != nil {
		return nil, nil, err
	}
	if dbType != SpannerDBType {
		return nil, nil, errTenantRosterUnsupportedDatabase
	}

	txn := r.client.ReadOnlyTransaction()
	defer txn.Close()

	set, err := readTenantSet(ctx, txn.SpannerReadOnlyTransaction(), statements, nil)
	if err != nil {
		return nil, nil, err
	}
	if r.nameColumn == "" {
		return set, nil, nil
	}
	names, err := readTenantNames(ctx, txn.SpannerReadOnlyTransaction(), tenantNamesStatement(r.table, r.keyColumn, r.nameColumn))
	if err != nil {
		return nil, nil, err
	}

	return set, names, nil
}

// tenantNamesStatement renders the roster's read of the names on Spanner: each key as
// text under the name the scan reads, with its name, "" where the column is NULL.
func tenantNamesStatement(table, keyColumn, nameColumn string) string {
	g := newSQLGenerator(Spanner)

	return fmt.Sprintf("SELECT CAST(%s AS %s) AS Domain, COALESCE(CAST(%s AS %s), '') AS Name FROM %s",
		g.quoteIdentifier(keyColumn), columnTypeString, g.quoteIdentifier(nameColumn), columnTypeString, g.quoteIdentifier(table))
}

// readTenantNames runs the names statement through the roster's snapshot.
func readTenantNames(ctx context.Context, txn spxapi.Querier, statement string) (tenantNames, error) {
	var rows []struct {
		Domain string `spanner:"Domain"`
		Name   string `spanner:"Name"`
	}
	if err := spxscan.Select(ctx, txn, &rows, spanner.Statement{SQL: statement}); err != nil {
		return nil, errors.Wrap(err, "spxscan.Select()")
	}
	names := make(tenantNames, len(rows))
	for _, row := range rows {
		names[accesstypes.Domain(row.Domain)] = row.Name
	}

	return names, nil
}

// rosterStatements are the roster's statements: the keys, each with its directory-only
// flag (FALSE where the record declares none), and the directories, empty where the
// roster reads no declaration. Filtered statements take the tenant as @domain.
type rosterStatements struct {
	Keys        string
	Directories string
}

// tenantRosterStatements renders the roster's statements for the database type: the
// key column as text under the name the scan reads, with the directory-only column
// beside it, and the directories record's rows, each identifier quoted the way the
// package's other statements quote them (backticks on Spanner, double quotes on
// Postgres). With filtered the statements read one tenant, the @domain parameter.
func tenantRosterStatements(dbType DBType, table, keyColumn string, columns *TenantDeclarationColumns, filtered bool) (rosterStatements, error) {
	var g *sqlGenerator
	var text, domainParam string
	switch dbType {
	case SpannerDBType:
		g, text, domainParam = newSQLGenerator(Spanner), columnTypeString, "@"+domainParamName
	case PostgresDBType:
		g, text, domainParam = newSQLGenerator(PostgreSQL), "TEXT", "$1"
	default:
		return rosterStatements{}, errors.Newf("resource: tenant roster: unsupported database type %q", dbType)
	}
	quote := func(column string) string {
		if dbType == PostgresDBType {
			return g.quoteIdentifier(column)
		}

		return column
	}
	directoryOnly := "FALSE"
	if columns.declared() && columns.DirectoryOnly != "" {
		directoryOnly = g.quoteIdentifier(columns.DirectoryOnly)
	}
	out := rosterStatements{
		Keys: fmt.Sprintf("SELECT CAST(%s AS %s) AS %s, %s AS %s FROM %s", g.quoteIdentifier(keyColumn), text, quote("Domain"), directoryOnly, quote("DirectoryOnly"), g.quoteIdentifier(table)),
	}
	if filtered {
		out.Keys += fmt.Sprintf(" WHERE CAST(%s AS %s) = %s", g.quoteIdentifier(keyColumn), text, domainParam)
	}
	if !columns.declared() {
		return out, nil
	}
	out.Directories = fmt.Sprintf("SELECT CAST(%s AS %s) AS %s, %s AS %s, %s AS %s FROM %s",
		g.quoteIdentifier(columns.TenantColumn), text, quote("Domain"), g.quoteIdentifier(columns.MethodColumn), quote("Method"), g.quoteIdentifier(columns.ConnectionColumn), quote("Connection"), g.quoteIdentifier(columns.DirectoriesTable))
	if filtered {
		out.Directories += fmt.Sprintf(" WHERE CAST(%s AS %s) = %s", g.quoteIdentifier(columns.TenantColumn), text, domainParam)
	}
	out.Directories += fmt.Sprintf(" ORDER BY %s, %s", g.quoteIdentifier(columns.MethodColumn), g.quoteIdentifier(columns.ConnectionColumn))

	return out, nil
}

// readTenantSet runs the roster's statements through a Spanner transaction and
// returns the tenants as a set: each key with its declaration when the directories
// statement is rendered, nil otherwise. params carry the filtered statements' tenant.
func readTenantSet(ctx context.Context, txn spxapi.Querier, statements rosterStatements, params map[string]any) (tenantSet, error) {
	var rows []struct {
		Domain        string `spanner:"Domain"`
		DirectoryOnly bool   `spanner:"DirectoryOnly"`
	}
	if err := spxscan.Select(ctx, txn, &rows, spanner.Statement{SQL: statements.Keys, Params: params}); err != nil {
		return nil, errors.Wrap(err, "spxscan.Select()")
	}
	declared := statements.Directories != ""
	set := make(tenantSet, len(rows))
	for _, row := range rows {
		var declaration *signin.TenantDeclaration
		if declared {
			declaration = &signin.TenantDeclaration{DirectoryOnly: row.DirectoryOnly}
		}
		set[accesstypes.Domain(row.Domain)] = declaration
	}
	if !declared {
		return set, nil
	}

	var directoryRows []struct {
		Domain     string `spanner:"Domain"`
		Method     string `spanner:"Method"`
		Connection string `spanner:"Connection"`
	}
	if err := spxscan.Select(ctx, txn, &directoryRows, spanner.Statement{SQL: statements.Directories, Params: params}); err != nil {
		return nil, errors.Wrap(err, "spxscan.Select()")
	}
	for _, row := range directoryRows {
		declaration := set[accesstypes.Domain(row.Domain)]
		if declaration == nil {
			// A directory of a tenant the record does not hold declares nothing.
			continue
		}
		declaration.Directories = append(declaration.Directories, signin.Directory{Method: sessioninfo.AuthMethod(row.Method), Connection: row.Connection})
	}

	return set, nil
}

// NewTenantDeclarationReader returns the reader of a tenant's declaration for the
// sign-in policy (signin.TenantPolicy): it reads the tenant record's row and the
// directories record's rows for the tenant in the sign-in's own transaction, on
// Spanner, where the session's transaction is *spanner.ReadWriteTransaction. The
// generated New<Record>DeclarationReader constructor supplies the table and the
// columns; an application calls that one. A tenant the table does not hold declares
// nothing (nil). The reader has no side effects, as a retried transaction needs.
func NewTenantDeclarationReader(table, keyColumn string, columns *TenantDeclarationColumns) signin.DeclarationReader[*spanner.ReadWriteTransaction] {
	return func(ctx context.Context, txn *spanner.ReadWriteTransaction, tenant accesstypes.Domain) (*signin.TenantDeclaration, error) {
		if tenant == "" {
			return nil, nil
		}
		statements, err := tenantRosterStatements(SpannerDBType, table, keyColumn, columns, true)
		if err != nil {
			return nil, err
		}
		set, err := readTenantSet(ctx, txn, statements, map[string]any{domainParamName: string(tenant)})
		if err != nil {
			return nil, err
		}

		return set[tenant], nil
	}
}

// NewTenantDirectoryReader returns the reader of every tenant's declaration of one
// directory, for a first sign-in by a person no user was created for
// (session.Auth.SetDirectoryAdmission, through signin.DirectoryReader): the directories
// record's rows whose method and connection are the directory's, each as its tenant
// with the directory's default seat (DefaultSeatColumn, "" where the record carries no
// such column or the row names none), read in the sign-in's own transaction on
// Spanner. The generated New<Record>DirectoryReader constructor supplies the columns;
// an application calls that one. A directory no tenant declares answers nothing, and
// so does a record that declares no directories. The reader has no side effects, as a
// retried transaction needs.
func NewTenantDirectoryReader(columns *TenantDeclarationColumns) signin.DirectoryReader[*spanner.ReadWriteTransaction] {
	return func(ctx context.Context, txn *spanner.ReadWriteTransaction, directory signin.Directory) ([]signin.DeclaredDirectory, error) {
		if !columns.declared() {
			return nil, nil
		}
		g := newSQLGenerator(Spanner)
		seat := "''"
		if columns.DefaultSeatColumn != "" {
			seat = fmt.Sprintf("COALESCE(%s, '')", g.quoteIdentifier(columns.DefaultSeatColumn))
		}
		statement := spanner.Statement{
			SQL: fmt.Sprintf("SELECT CAST(%s AS %s) AS Domain, %s AS DefaultSeat FROM %s WHERE %s = @method AND %s = @connection ORDER BY %s",
				g.quoteIdentifier(columns.TenantColumn), columnTypeString, seat, g.quoteIdentifier(columns.DirectoriesTable),
				g.quoteIdentifier(columns.MethodColumn), g.quoteIdentifier(columns.ConnectionColumn), g.quoteIdentifier(columns.TenantColumn)),
			Params: map[string]any{"method": string(directory.Method), "connection": directory.Connection},
		}
		var rows []struct {
			Domain      string `spanner:"Domain"`
			DefaultSeat string `spanner:"DefaultSeat"`
		}
		if err := spxscan.Select(ctx, txn, &rows, statement); err != nil {
			return nil, errors.Wrap(err, "spxscan.Select()")
		}
		declared := make([]signin.DeclaredDirectory, 0, len(rows))
		for _, row := range rows {
			declared = append(declared, signin.DeclaredDirectory{Tenant: accesstypes.Domain(row.Domain), DefaultSeat: accesstypes.Role(row.DefaultSeat)})
		}

		return declared, nil
	}
}

// Domains lists the tenants, sorted. It never fails and reads nothing; the signature
// is DomainRoster's, so the roster's Domains is what an application hands
// SessionPermissions. A nil roster lists none.
func (r *TenantRoster) Domains(context.Context) ([]accesstypes.Domain, error) {
	domains := []accesstypes.Domain{}
	if r == nil {
		return domains, nil
	}
	set := r.set.Load()
	if set == nil {
		return domains, nil
	}
	for domain := range *set {
		domains = append(domains, domain)
	}
	slices.Sort(domains)

	return domains, nil
}

// Name answers the tenant's name as the record's name column held it at the last read
// (WithTenantNames), from the copy: no read, no wait. It is "" for a tenant the copy
// names nothing for, for every tenant of a roster that reads no names, and on a nil
// roster. The roster is the DomainNamer the session's tenant list names tenants with.
func (r *TenantRoster) Name(domain accesstypes.Domain) string {
	if r == nil {
		return ""
	}
	names := r.names.Load()
	if names == nil {
		return ""
	}

	return (*names)[domain]
}

// Named answers the tenant that carries the name, as the record's name column held it
// at the last read (WithTenantNames), from the copy: no read, no wait. A tenant's key is
// an id nobody reads, so what names a tenant by hand names it by its name: a
// development bootstrap reads its logins' tenants this way. A name no tenant carries
// and a name more than one tenant carries are errors: the first names nothing, and the
// second does not say which. A nil roster and one that reads no names hold no name.
func (r *TenantRoster) Named(name string) (accesstypes.Domain, error) {
	if r == nil {
		return "", errors.Newf("resource: tenant roster: no tenant is named %q: the application wired no roster", name)
	}
	names := r.names.Load()
	if names == nil {
		return "", errors.Newf("resource: tenant roster: no tenant is named %q: the roster holds no names (WithTenantNames, and a read)", name)
	}
	var found []accesstypes.Domain
	for domain, held := range *names {
		if held == name {
			found = append(found, domain)
		}
	}
	switch len(found) {
	case 0:
		return "", errors.Newf("resource: tenant roster: no tenant is named %q", name)
	case 1:
		return found[0], nil
	default:
		slices.Sort(found)

		return "", errors.Newf("resource: tenant roster: %d tenants are named %q (%v): the name does not say which", len(found), name, found)
	}
}

// Has reports whether the domain is a tenant the roster holds. It loads the set and
// looks the domain up: no read, no wait. A domain the set does not hold is unknown,
// and every domain is unknown to a nil roster.
func (r *TenantRoster) Has(domain accesstypes.Domain) bool {
	if r == nil {
		return false
	}
	set := r.set.Load()
	if set == nil {
		return false
	}
	_, ok := (*set)[domain]

	return ok
}

// Declarations answers what the domain declares about who may enter it, from the copy:
// the TenantDeclarations an application hands SessionPermissions, so the session's
// tenant list and the generated DomainGuard decide with no read and no wait. A tenant
// the set does not hold, a tenant that declares nothing, and every tenant of a roster
// built without WithTenantDeclarations answer nil, which declares nothing. A nil roster
// answers nil too. It never fails.
func (r *TenantRoster) Declarations(_ context.Context, domain accesstypes.Domain) (*signin.TenantDeclaration, error) {
	if r == nil {
		return nil, nil
	}
	set := r.set.Load()
	if set == nil {
		return nil, nil
	}

	return (*set)[domain], nil
}

// Add puts the domain in the set at once, declaring nothing: what a test harness calls
// to hold a tenant the schema has no row for. The generated write paths call Reload
// instead, so the tenant's declaration comes with it. A nil roster takes nothing.
func (r *TenantRoster) Add(domain accesstypes.Domain) {
	if r == nil {
		return
	}
	r.replace(func(set tenantSet) {
		set[domain] = nil
	})
}

// Remove drops the domain from the set at once. A nil roster drops nothing.
func (r *TenantRoster) Remove(domain accesstypes.Domain) {
	if r == nil {
		return
	}
	r.replace(func(set tenantSet) {
		delete(set, domain)
	})
}

// replace copies the set, applies the change to the copy and swaps it in, under the
// writers' lock, so a reader never sees a set being written.
func (r *TenantRoster) replace(change func(tenantSet)) {
	r.writes.Lock()
	defer r.writes.Unlock()

	var current tenantSet
	if set := r.set.Load(); set != nil {
		current = *set
	}
	next := make(tenantSet, len(current)+1)
	for domain, declaration := range current {
		next[domain] = declaration
	}
	change(next)
	r.set.Store(&next)
}
