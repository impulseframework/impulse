package session

import (
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/session/internal/dbtype"
)

// Tables names the tables one Auth keeps, by what each holds. An application that runs
// several Auths in one database keeps each under a prefix of its own (TablesWithPrefix),
// and everything that must agree on the names reads them from here: the Auth's own
// construction, the application's migrations, and the code generator, which declares
// the management resources over the same tables.
type Tables struct {
	// Sessions holds the sessions and Users the people who sign in.
	Sessions string
	Users    string
	// Identities holds the links between a user and the external identities a
	// directory verified for them.
	Identities string
	// PendingSignIns holds the sign-ins that wait for a confirmation or a second
	// factor, and SignInAttempts what the password method's attempt limiter counts.
	PendingSignIns string
	SignInAttempts string
	// MailLinks holds the invitations, address verifications and sign-ups the Auth
	// mails, FactorCodes the sign-in codes it mails, and TrustedDevices the browsers
	// people asked to be remembered after a code.
	MailLinks      string
	FactorCodes    string
	TrustedDevices string
	// Tokens holds the bearer tokens the Auth issues for its machine outlets, and
	// DeviceCodes the device authorizations command-line clients wait on.
	Tokens      string
	DeviceCodes string
	// AuthEvents holds how each session was authenticated, and Impersonations who
	// established a view-as or act-as-role session; the storage attaches both
	// (sessionstorage.NewAuthEventsTable, sessionstorage.NewImpersonationTable).
	AuthEvents     string
	Impersonations string
}

// DefaultTables answers the library's own names, the ones an Auth reads and writes when
// no option names another and the library's schema sets create.
func DefaultTables() Tables {
	return Tables{
		Sessions:       dbtype.SessionsTable,
		Users:          dbtype.UsersTable,
		Identities:     dbtype.IdentitiesTable,
		PendingSignIns: dbtype.PendingSignInsTable,
		SignInAttempts: dbtype.SignInAttemptsTable,
		MailLinks:      dbtype.MailLinksTable,
		FactorCodes:    dbtype.FactorCodesTable,
		TrustedDevices: dbtype.TrustedDevicesTable,
		Tokens:         dbtype.TokensTable,
		DeviceCodes:    dbtype.DeviceCodesTable,
		AuthEvents:     dbtype.AuthEventsTable,
		Impersonations: dbtype.ImpersonationsTable,
	}
}

// TablesWithPrefix answers the names of an Auth kept under prefix: the library's own name
// for each table, led by the prefix (Crew gives CrewSessions, CrewSessionUsers and so
// on). It is the one rule for the names of an Auth that shares its database with another,
// so the Auth's construction and whatever else names its tables cannot spell them two
// ways. A prefix that leaves a name no plain identifier is refused.
func TablesWithPrefix(prefix string) (Tables, error) {
	tables := DefaultTables()
	for _, name := range []*string{
		&tables.Sessions, &tables.Users, &tables.Identities, &tables.PendingSignIns, &tables.SignInAttempts, &tables.MailLinks,
		&tables.FactorCodes, &tables.TrustedDevices, &tables.Tokens, &tables.DeviceCodes, &tables.AuthEvents, &tables.Impersonations,
	} {
		*name = prefix + *name
		if err := dbtype.ValidateTableName(*name); err != nil {
			return Tables{}, errors.Wrapf(err, "table prefix %q", prefix)
		}
	}

	return tables, nil
}
