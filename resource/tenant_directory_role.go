package resource

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/words"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"github.com/impulseframework/impulse/spxscan"
)

// A tenant's directory admits a stranger it vouches for in one of two ways, which the
// directories record's default seat column says (@directoryDefaultSeat): with a role
// named there, the tenant is open to the directory's people, and each of them is given
// the role when they sign in through it and hold nothing in the tenant; with none, the tenant is invite only, and
// a person from the directory enters once they are invited. The choice is the tenant's
// own, made by the tenant's administrator with two methods the generator writes in the
// tenant's scope for a directories record that carries the column,
// Open<Record>ToDirectory and Make<Record>InviteOnly. Opening a tenant lets in people
// nobody named, so the method that does it declares a warning: the dialog a page runs it
// through shows what it will do, in the words written here, and holds its button until
// the person has typed OpenToDirectoryWord. The column is output only on the wire: the
// record's own update never writes it.

// OpenToDirectoryWord is the word a person types before a tenant is opened to its
// directory's people (the warning of Open<Record>ToDirectory).
const OpenToDirectoryWord = "open"

// TenantDirectory is a tenant's directory as its row of the directories record holds
// it.
type TenantDirectory struct {
	// Key is the row's key as text, which the generated methods write the row through.
	Key string
	// Directory is the organization the tenant's people sign in through.
	Directory signin.Directory
	// DefaultRole is the role a person is given in the tenant when they sign in through
	// the directory and hold nothing in the tenant, at every sign-in, "" where the tenant
	// admits the directory's people by invitation only.
	DefaultRole accesstypes.Role
	// SecondFactor says the tenant says the directory asks its people for a second
	// factor of its own (@directorySecondFactor); false where the record carries no such
	// column.
	SecondFactor bool
}

// Directory answers tenant's directory as committed, in the caller's transaction: the
// row the two methods that say how the tenant admits the directory's people write. A
// tenant with no directory has nobody to open to and nothing to make invite only, and
// is refused with a Conflict that says to set the directory first.
func (r *TenantDirectoryRules) Directory(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (*TenantDirectory, error) {
	if !r.columns.hasDirectories() {
		return nil, errors.New("resource: a tenant's directory rules need the directories record's columns: build them with the generated New<Record>DirectoryRules")
	}
	if r.columns.DirectoriesKeyColumn == "" || r.columns.DefaultSeatColumn == "" {
		return nil, errors.New("resource: the directories record declares no default seat column (@directoryDefaultSeat), so a tenant's directory has no default role to set")
	}
	directory, err := r.directoryRow(ctx, txn, tenant)
	if err != nil {
		return nil, err
	}
	if directory != nil {
		return directory, nil
	}

	return nil, httpio.NewConflictMessagef("This %[1]s has no directory, so there is nobody to open it to. Set its directory first: sign in through your organization's directory and make it this %[1]s's directory.", r.word)
}

// directoryRow reads tenant's row of the directories record as committed, in the
// caller's transaction: its key, its directory, its default seat where the record
// carries that column, and the tenant's word for the directory's second factor where it
// carries that one. A tenant with no directory is answered nil.
func (r *TenantDirectoryRules) directoryRow(ctx context.Context, txn ReadWriteTransaction, tenant accesstypes.Domain) (*TenantDirectory, error) {
	if r.columns.DirectoriesKeyColumn == "" {
		return nil, errors.New("resource: the directories record's columns name no key column (DirectoriesKeyColumn), so a tenant's directory row cannot be read")
	}
	if txn.DBType() != SpannerDBType {
		return nil, errTenantDirectoryUnsupportedDatabase
	}
	g := newSQLGenerator(Spanner)
	seat, secondFactor := "''", sqlFalse
	if r.columns.DefaultSeatColumn != "" {
		seat = fmt.Sprintf("COALESCE(%s, '')", g.quoteIdentifier(r.columns.DefaultSeatColumn))
	}
	if r.columns.SecondFactorColumn != "" {
		secondFactor = g.quoteIdentifier(r.columns.SecondFactorColumn)
	}
	statement := spanner.Statement{
		SQL: fmt.Sprintf("SELECT CAST(%s AS %s) AS RowKey, %s AS Method, %s AS Connection, %s AS DefaultSeat, %s AS SecondFactor FROM %s WHERE CAST(%s AS %s) = @%s",
			g.quoteIdentifier(r.columns.DirectoriesKeyColumn), columnTypeString, g.quoteIdentifier(r.columns.MethodColumn), g.quoteIdentifier(r.columns.ConnectionColumn),
			seat, secondFactor, g.quoteIdentifier(r.columns.DirectoriesTable),
			g.quoteIdentifier(r.columns.TenantColumn), columnTypeString, domainParamName),
		Params: map[string]any{domainParamName: string(tenant)},
	}
	var rows []struct {
		RowKey       string `spanner:"RowKey"`
		Method       string `spanner:"Method"`
		Connection   string `spanner:"Connection"`
		DefaultSeat  string `spanner:"DefaultSeat"`
		SecondFactor bool   `spanner:"SecondFactor"`
	}
	if err := spxscan.Select(ctx, txn.SpannerReadOnlyTransaction(), &rows, statement); err != nil {
		return nil, errors.Wrap(err, "spxscan.Select()")
	}
	for _, row := range rows {
		return &TenantDirectory{
			Key:          row.RowKey,
			Directory:    signin.Directory{Method: sessioninfo.AuthMethod(row.Method), Connection: row.Connection},
			DefaultRole:  accesstypes.Role(row.DefaultSeat),
			SecondFactor: row.SecondFactor,
		}, nil
	}

	return nil, nil
}

// OpenToDirectoryWarning says what opening a tenant to its directory's people with role
// would do, in the words a person reads before they confirm it, and the words for the
// button that does it. It writes nothing and judges no role: the method that opens the
// tenant holds the role to its rules before it asks. A request that names no role yet
// is answered, since the warning is read while the role is still being picked, and
// says the role is to be chosen. The role is said in words, "Night shift lead" for
// NightShiftLead, as every sentence a person reads names one (words.RoleInWords). The
// directory is said in the words a person knows it by (DirectoryWords, through the
// namers the rules were given), never by its connection.
func (r *TenantDirectoryRules) OpenToDirectoryWarning(directory *TenantDirectory, role accesstypes.Role) *Warning {
	through := r.directoryWords(directory.Directory)
	who := fmt.Sprintf("Anyone who signs in through %s will be able to enter this %s without an invitation", through, r.word)
	consequence := who + ". Choose the role each of them gets."
	if role != "" {
		consequence = fmt.Sprintf("%s and gets the role %s.", who, words.RoleInWords(string(role)))
	}

	return &Warning{
		Consequence: consequence,
		Names:       nil,
		Confirm:     fmt.Sprintf("Open to people who sign in through %s", through),
	}
}

// OpenTakesPeopleNote is the note a generated handler adds to OpenToDirectoryWarning in an
// application that holds a person to one tenant (identity.WithOneTenantPerPerson): the
// tenant's directory owns its people, so a person who signs in through it and belongs to
// another tenant is moved to this one at their next sign-in. The directory is said in the
// words a person knows it by (DirectoryWords).
func (r *TenantDirectoryRules) OpenTakesPeopleNote(directory *TenantDirectory) WarningNote {
	return WarningNote{
		Says: fmt.Sprintf(
			"A person belongs to one %s here. People who sign in through %s and belong to another %s are moved to this one the next time they sign in, and lose everything they hold in the other.",
			r.word, r.directoryWords(directory.Directory), r.word,
		),
	}
}
