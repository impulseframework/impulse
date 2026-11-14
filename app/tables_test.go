package app

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/session"
)

// TestDefaultTablesAreTheLibrarys holds the table names the scan assumes for an Auth
// constructed without a table-name option to the session library's own, so the two
// cannot drift apart.
func TestDefaultTablesAreTheLibrarys(t *testing.T) {
	t.Parallel()

	got := session.Tables{
		Sessions: DefaultSessionTable, Users: DefaultUserTable, Identities: DefaultIdentitiesTable,
		PendingSignIns: DefaultPendingTable, SignInAttempts: DefaultAttemptsTable, MailLinks: DefaultMailLinksTable,
		FactorCodes: DefaultFactorCodesTable, TrustedDevices: DefaultTrustedDevicesTable,
		Tokens: DefaultTokensTable, DeviceCodes: DefaultDeviceCodesTable,
		AuthEvents: DefaultAuthEventsTable, Impersonations: DefaultImpersonationTable,
	}
	if diff := cmp.Diff(session.DefaultTables(), got); diff != "" {
		t.Errorf("the scan's default table names differ from session.DefaultTables() (-library +scan):\n%s", diff)
	}
}
