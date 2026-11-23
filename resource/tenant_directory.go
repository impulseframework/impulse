package resource

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/spxscan"
)

// errTenantDirectoryUnsupportedDatabase is the answer on a database the rules' reads
// are not implemented for: the statements render for Postgres, the reads do not yet.
var errTenantDirectoryUnsupportedDatabase = errors.New("resource: a tenant's directory rules are read on Spanner only")

// TenantDirectoryRules holds a tenant's directory to its rules at every write of the
// directories record (@tenantDirectory):
//
//   - a tenant has one directory;
//   - a directory, its method and its connection together, is one tenant's;
//   - a personal account (any Google account, a Microsoft personal account) is no
//     organization's directory, so it is no tenant's;
//   - a directory is not removed while the tenant is directory only, since nobody
//     could then enter the tenant.
//
// The schema holds the first two as well, with a unique index on the directories
// record's tenant column and one on its method and connection: the rules read what is
// committed, so two writes that race, or that one transaction buffers together, are
// refused by the index at the commit. The rules refuse first, each with a sentence the
// person can act on, where the index can only say that a unique value exists.
//
// A directory is never typed in. It is set from a sign-in: FromSession answers the
// directory the caller is signed in through, held to the rules, which the directories
// record's generated create stamps on the row, and Set holds a directory a sign-in
// names to the rules, for a write made inside that sign-in. The generated
// New<Record>DirectoryRules supplies the tables and the word for one tenant; an
// application names none of them.
type TenantDirectoryRules struct {
	table     string
	keyColumn string
	columns   *TenantDeclarationColumns
	word      string
	// namers say what the application's sign-in registrations call a directory, for
	// the sentences that name one (NamedBy).
	namers []DirectoryNamer
	// passwords answer, one for each auth, whether the person behind the request holds
	// a password (PasswordsBy): the directory-only switch is turned on by a person who
	// has one.
	passwords []CallerPassword
	// secondFactors answer, one for each auth, whether its sign-in settings ask everyone
	// for a second factor now (SecondFactorsBy): a tenant vouches for its directory's own
	// second factor only where the application asks somebody for one.
	secondFactors []AuthSecondFactor
	// people are, one for each auth, the readers of the people a tenant seats, the ones
	// its directory-only switch keeps out and the ones who keep the administrators' way
	// in, with the tenant's lists of them (CountedBy), for the read of the switch and the
	// warning that turns it on.
	people []DirectoryPeople
}

// NewTenantDirectoryRules builds the rules over the tenant record's table and key
// column and its declaration's columns. word is what the refusals call one tenant
// ("tenant", "sector"), as the application's people say it.
func NewTenantDirectoryRules(table, keyColumn string, columns *TenantDeclarationColumns, word string) *TenantDirectoryRules {
	if word == "" {
		word = "tenant"
	}

	return &TenantDirectoryRules{table: table, keyColumn: keyColumn, columns: columns, word: word}
}

// NamedBy answers the rules with the namers of the application's auths, which say what
// each auth's sign-in registrations call a directory: every sentence of the rules that
// names a directory then says it in words a person knows (DirectoryWords), the name a
// registration gives it or the label of its button, and never its connection, which
// for a Microsoft Entra tenant is an id. The generated frames of a tenant's own methods
// pass the namer of each auth's generated identity client. Rules nobody names read
// what a directory alone says.
func (r *TenantDirectoryRules) NamedBy(namers ...DirectoryNamer) *TenantDirectoryRules {
	named := *r
	named.namers = namers

	return &named
}

// CallerPassword answers, for one auth, whether the person behind the request holds a
// password in it, in the caller's transaction. applies is false where the auth asks
// nothing of the request: its caller is another auth's person, or the auth has no
// password method, so nobody of it could have a password. The generated identity
// client of each auth supplies it (CallerPassword); an application names none.
type CallerPassword func(ctx context.Context, txn ReadWriteTransaction) (has, applies bool, err error)

// PasswordsBy answers the rules with what each of the application's auths says of the
// caller's password: a tenant is made directory only by a person who has a password,
// since a tenant's own administrator enters a directory-only tenant with a password and
// a second factor (signin.TenantDeclaration.AdmitsAdministrator), and that is the way
// back in if the directory has a problem. The generated frames of a tenant's own
// methods pass the reader of each auth's generated identity client. Where no auth's
// answer applies, as in an application whose people have no passwords, the rule stands
// down and nothing is said of it; rules built without readers ask nothing.
func (r *TenantDirectoryRules) PasswordsBy(readers ...CallerPassword) *TenantDirectoryRules {
	asked := *r
	asked.passwords = readers

	return &asked
}

// SeatedCount counts the people of one auth in tenant's own list of its people, in the
// caller's transaction: who holds a seat in the tenant itself or in every tenant, and who
// signs in through the tenant's directory, one row a person, the rows DirectoryOutsiders
// reads. The generated identity client of each auth whose people a tenant seats supplies
// it (SeatedCount); an application names none.
type SeatedCount func(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (int, error)

// DirectoryCounter is one auth's part of the count the read of a tenant's directory-only
// switch says (DirectoryOnlyState.People): how many people the tenant's list holds, and
// who of them the switch keeps out, read by the same reader the warning that turns the
// switch on asks, so the read and the warning always say the same number. A nil reader
// counts nobody.
type DirectoryCounter struct {
	Seated    SeatedCount
	Outsiders DirectoryOutsiders
}

// CountedBy answers the rules with the application's auths whose people a tenant seats,
// one entry for each: the readers of whom the directory-only switch keeps out and of who
// keeps the administrators' way in, how many people the tenant's list holds, and the
// tenant's lists that decide whether a caller is told who they are (DirectoryPeople). The
// read of a tenant's sign-in settings (SignInSettings) says the counts from them before
// anyone turns it on, and the warning that turns it on (DirectoryOnlyWarning) reads the
// same entries, so the two always say the same number. The generated frames of a tenant's
// own methods pass each auth's generated identity client's readers. Rules built without
// entries count nobody: the read says no number, and the warning says who will lose
// access without one.
func (r *TenantDirectoryRules) CountedBy(people ...DirectoryPeople) *TenantDirectoryRules {
	counted := *r
	counted.people = people

	return &counted
}

// directoryWords says a directory as the middle of one of the rules' sentences names
// it (DirectoryWords), and directoryName with its kind before a name (DirectoryName).
func (r *TenantDirectoryRules) directoryWords(directory signin.Directory) string {
	return DirectoryWords(directory, r.namers...)
}

func (r *TenantDirectoryRules) directoryName(directory signin.Directory) string {
	return DirectoryName(directory, r.namers...)
}

// SessionDirectory answers the directory the request's session was established
// through: what "make the directory I am signed in through this tenant's directory"
// names. A session that came through no directory (a password, a session the
// application started itself, an impersonation) is refused with a Conflict that says
// how to sign in instead; a personal account is answered as it is, for the rules to
// refuse it by name (TenantDirectoryRules.Set).
func SessionDirectory(ctx context.Context) (signin.Directory, error) {
	established, ok := sessioninfo.DataFromCtx(ctx).EstablishedBy()
	if ok && established.Method.ThroughDirectory() {
		return signin.Directory{Method: established.Method, Connection: established.Connection}, nil
	}
	how := "This session was not signed in through an organization's directory."
	if ok && established.Method == sessioninfo.MethodPassword {
		how = "You are signed in with a password."
	}

	return signin.Directory{}, httpio.NewConflictMessagef("%s A directory is set by signing in through it: sign in with your organization's account (Google Workspace or Microsoft Entra), then make it the directory.", how)
}

// FromSession answers the directory the caller is signed in through, held to the
// rules for tenant: the directory the directories record's generated create stamps on
// the row, so no request types a method or a connection. Every refusal is a Conflict:
// SessionDirectory's and Set's.
func (r *TenantDirectoryRules) FromSession(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (signin.Directory, error) {
	directory, err := SessionDirectory(ctx)
	if err != nil {
		return signin.Directory{}, err
	}
	if err := r.Set(ctx, txn, tenant, directory); err != nil {
		return signin.Directory{}, err
	}

	return directory, nil
}

// Set holds directory to the rules as tenant's directory, in the caller's transaction,
// and writes nothing: the caller buffers the row once Set answers nil. It refuses, each
// as a Conflict with a sentence the person can act on, a directory that is no
// organization's (a personal account, or no directory at all), a tenant that has a
// directory already, and a directory another tenant has. The last says the
// organization already has a tenant, which a rule of one tenant per directory cannot
// keep from being learned; it does not say which.
func (r *TenantDirectoryRules) Set(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain, directory signin.Directory) error {
	if !r.columns.declared() {
		return errors.New("resource: a tenant's directory rules need the directories record's columns: build them with the generated New<Record>DirectoryRules")
	}
	switch {
	case directory.PersonalAccounts():
		return httpio.NewConflictMessagef("%s is not an organization's directory, so it cannot be a %s's directory. Sign in with your organization's account (Google Workspace or Microsoft Entra) and try again.", personalAccountsName(directory), r.word)
	case !directory.Organization():
		return httpio.NewConflictMessagef("A %s's directory is an organization's directory: a Google Workspace or a Microsoft Entra tenant, as a sign-in through it names it.", r.word)
	}

	held, err := r.held(ctx, txn, tenant, directory)
	if err != nil {
		return err
	}
	for _, row := range held {
		rowDirectory := signin.Directory{Method: sessioninfo.AuthMethod(row.Method), Connection: row.Connection}
		switch {
		case accesstypes.Domain(row.Domain) == tenant && rowDirectory == directory:
			return httpio.NewConflictMessagef("%s is already this %s's directory.", startSentence(r.directoryName(directory)), r.word)
		case accesstypes.Domain(row.Domain) == tenant:
			return httpio.NewConflictMessagef("This %s already has a directory, %s. A %s has one directory: remove it first to set another.", r.word, r.directoryName(rowDirectory), r.word)
		}
	}
	if len(held) > 0 {
		return httpio.NewConflictMessagef("%s already has a %s here, and a directory belongs to one %s. Ask that %s's administrator to invite you.", startSentence(r.directoryName(directory)), r.word, r.word, r.word)
	}

	return nil
}

// Remove holds the removal of tenant's directory to its rule, in the caller's
// transaction, and writes nothing: a tenant that is directory only keeps its
// directory, since its people enter through it alone and nobody could enter the tenant
// without it. The refusal is a Conflict that says to turn directory only off first. A
// request that turns the tenant directory only and removes its directory is refused the
// same way, though the switch it turns on is not yet committed: the transaction notes
// the writes the rules pass (directoryWrites). A tenant record with no directory-only
// column removes freely.
func (r *TenantDirectoryRules) Remove(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) error {
	if !r.columns.declared() || r.columns.DirectoryOnly == "" {
		return nil
	}
	statements, err := tenantDirectoryStatements(txn.DBType(), r.table, r.keyColumn, r.columns)
	if err != nil {
		return err
	}
	if txn.DBType() != SpannerDBType {
		return errTenantDirectoryUnsupportedDatabase
	}
	var rows []struct {
		DirectoryOnly bool `spanner:"DirectoryOnly"`
	}
	statement := spanner.Statement{SQL: statements.DirectoryOnly, Params: map[string]any{domainParamName: string(tenant)}}
	if err := spxscan.Select(ctx, txn.SpannerReadOnlyTransaction(), &rows, statement); err != nil {
		return errors.Wrap(err, "spxscan.Select()")
	}
	for _, row := range rows {
		if row.DirectoryOnly {
			return httpio.NewConflictMessagef("This %s is directory only: its people sign in through its directory alone, so nobody could enter it without one. Turn directory only off first, then remove the directory.", r.word)
		}
	}
	writes := notedDirectoryWrites(txn)
	if writes.turnsDirectoryOnly(tenant) {
		return httpio.NewConflictMessagef("This same request turns this %[1]s directory only, so it cannot also remove its directory: nobody could enter the %[1]s. Keep the directory, or leave directory only off.", r.word)
	}
	writes.noteRemoved(tenant)

	return nil
}

// heldDirectory is a row of the directories record the rules read: the tenant that
// has it, and the directory.
type heldDirectory struct {
	Domain     string `spanner:"Domain"`
	Method     string `spanner:"Method"`
	Connection string `spanner:"Connection"`
}

// held reads the rows that decide whether directory may become tenant's: the
// directory tenant has, and the row of whichever tenant has directory, as committed.
func (r *TenantDirectoryRules) held(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain, directory signin.Directory) ([]heldDirectory, error) {
	statements, err := tenantDirectoryStatements(txn.DBType(), r.table, r.keyColumn, r.columns)
	if err != nil {
		return nil, err
	}
	if txn.DBType() != SpannerDBType {
		return nil, errTenantDirectoryUnsupportedDatabase
	}
	var rows []heldDirectory
	statement := spanner.Statement{
		SQL:    statements.Held,
		Params: map[string]any{domainParamName: string(tenant), "method": string(directory.Method), "connection": directory.Connection},
	}
	if err := spxscan.Select(ctx, txn.SpannerReadOnlyTransaction(), &rows, statement); err != nil {
		return nil, errors.Wrap(err, "spxscan.Select()")
	}

	return rows, nil
}

// directoryRuleStatements are the statements the rules read with. Held reads the
// directories record's rows that are the tenant's or carry the directory, taking the
// tenant, the method and the connection (@domain, @method and @connection on Spanner;
// $1, $2 and $3 on Postgres), the tenant's own row first. DirectoryOnly reads the
// tenant's directory-only flag, taking the tenant; it is empty where the record
// carries no such column.
type directoryRuleStatements struct {
	Held          string
	DirectoryOnly string
}

// tenantDirectoryStatements renders the rules' statements for the database type, each
// identifier quoted the way the roster's statements quote them (backticks on Spanner,
// double quotes on Postgres).
func tenantDirectoryStatements(dbType DBType, table, keyColumn string, columns *TenantDeclarationColumns) (directoryRuleStatements, error) {
	var g *sqlGenerator
	var text, domainParam, methodParam, connectionParam string
	switch dbType {
	case SpannerDBType:
		g, text, domainParam, methodParam, connectionParam = newSQLGenerator(Spanner), columnTypeString, "@"+domainParamName, "@method", "@connection"
	case PostgresDBType:
		g, text, domainParam, methodParam, connectionParam = newSQLGenerator(PostgreSQL), "TEXT", "$1", "$2", "$3"
	default:
		return directoryRuleStatements{}, errors.Newf("resource: a tenant's directory rules: unsupported database type %q", dbType)
	}
	quote := func(column string) string {
		if dbType == PostgresDBType {
			return g.quoteIdentifier(column)
		}

		return column
	}
	tenant := fmt.Sprintf("CAST(%s AS %s)", g.quoteIdentifier(columns.TenantColumn), text)
	method, connection := g.quoteIdentifier(columns.MethodColumn), g.quoteIdentifier(columns.ConnectionColumn)
	out := directoryRuleStatements{
		Held: fmt.Sprintf("SELECT %s AS %s, %s AS %s, %s AS %s FROM %s WHERE %s = %s OR (%s = %s AND %s = %s) ORDER BY CASE WHEN %s = %s THEN 0 ELSE 1 END, %s",
			tenant, quote("Domain"), method, quote("Method"), connection, quote("Connection"), g.quoteIdentifier(columns.DirectoriesTable),
			tenant, domainParam, method, methodParam, connection, connectionParam, tenant, domainParam, tenant),
	}
	if columns.DirectoryOnly != "" {
		out.DirectoryOnly = fmt.Sprintf("SELECT %s AS %s FROM %s WHERE CAST(%s AS %s) = %s",
			g.quoteIdentifier(columns.DirectoryOnly), quote("DirectoryOnly"), g.quoteIdentifier(table), g.quoteIdentifier(keyColumn), text, domainParam)
	}

	return out, nil
}

// personalAccountsName is what a sentence calls one of the two values that stand for
// everyone who is in no organization.
func personalAccountsName(directory signin.Directory) string {
	if directory.Method == sessioninfo.MethodAzure {
		return "A personal Microsoft account"
	}

	return "A personal Google account"
}
