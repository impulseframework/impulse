package resource

import (
	"context"
	"strings"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	initiator "github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
	"google.golang.org/grpc/codes"
)

// stationRules are the directory rules over the roster tests' fixture, as a generated
// New<Record>DirectoryRules builds them.
func stationRules() *TenantDirectoryRules {
	return NewTenantDirectoryRules(stationsTable, stationsKey, stationColumns, "station")
}

// insertStationDirectory buffers a station's directory row the way a write path that
// passed the rules commits it.
func insertStationDirectory(txn ReadWriteTransaction, id string, station accesstypes.Domain, directory signin.Directory) error {
	rw, ok := txn.(*SpannerReadWriteTransaction)
	if !ok {
		panic("the fixture writes through the Spanner transaction")
	}

	return rw.txn.BufferWrite([]*spanner.Mutation{
		spanner.Insert("StationDirectories", []string{"Id", "StationId", "Method", "Connection"}, []any{id, string(station), string(directory.Method), directory.Connection}),
	})
}

// TestTenantDirectoryRules_spanner proves the rules of a tenant's directory at the
// write, over the emulator: a second directory for a tenant and a directory another
// tenant has are each refused with a sentence that says what to do, a personal account
// and a session that came through no directory are refused as no directory, the
// directory of a tenant that is directory only is not removed, and the schema's two
// unique indexes refuse each of the first two on their own, for a write that never
// asked the rules.
func TestTenantDirectoryRules_spanner(t *testing.T) {
	t.Parallel()

	workspace := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "frontier.example"}
	entra := signin.Directory{Method: sessioninfo.MethodAzure, Connection: "6f2c1b7e-0000-4000-8000-000000000001"}
	other := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "elsewhere.example"}
	db, client := tenantDatabase(t, "directory-rules", "alpha", "beta", "gamma")
	// alpha has the Workspace and is directory only; beta has the Entra tenant; gamma
	// has no directory.
	declareStation(t, db, "alpha", true, workspace)
	declareStation(t, db, "beta", false, entra)
	rules := stationRules()

	set := func(ctx context.Context, station accesstypes.Domain, directory signin.Directory) error {
		return client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
			return rules.Set(ctx, txn, station, directory)
		})
	}

	setTests := []struct {
		name      string
		station   accesstypes.Domain
		directory signin.Directory
		// wantRefusal is the Conflict's sentence; empty where the directory may be set.
		wantRefusal string
	}{
		{name: "a station with no directory takes a directory no station has", station: "gamma", directory: other},
		{
			name: "a second directory for a station is refused, naming the one it has", station: "beta", directory: other,
			wantRefusal: "This station already has a directory, Microsoft Entra tenant 6f2c1b7e-0000-4000-8000-000000000001. A station has one directory: remove it first to set another.",
		},
		{
			name: "the directory a station has already is refused as its own", station: "beta", directory: entra,
			wantRefusal: "Microsoft Entra tenant 6f2c1b7e-0000-4000-8000-000000000001 is already this station's directory.",
		},
		{
			name: "a directory another station has is refused, saying the organization has a station", station: "gamma", directory: workspace,
			wantRefusal: "Google Workspace frontier.example already has a station here, and a directory belongs to one station. Ask that station's administrator to invite you.",
		},
		{
			name: "any Google account is refused as no organization's directory", station: "gamma",
			directory:   signin.Directory{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection},
			wantRefusal: "A personal Google account is not an organization's directory, so it cannot be a station's directory. Sign in with your organization's account (Google Workspace or Microsoft Entra) and try again.",
		},
		{
			name: "Microsoft's personal accounts are refused as no organization's directory", station: "gamma",
			directory:   signin.Directory{Method: sessioninfo.MethodAzure, Connection: signin.MicrosoftPersonalAccountsConnection},
			wantRefusal: "A personal Microsoft account is not an organization's directory, so it cannot be a station's directory. Sign in with your organization's account (Google Workspace or Microsoft Entra) and try again.",
		},
		{
			name: "a password is no directory", station: "gamma", directory: signin.Directory{Method: sessioninfo.MethodPassword},
			wantRefusal: "A station's directory is an organization's directory: a Google Workspace or a Microsoft Entra tenant, as a sign-in through it names it.",
		},
	}
	for _, tt := range setTests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := set(t.Context(), tt.station, tt.directory)
			if tt.wantRefusal == "" {
				if err != nil {
					t.Fatalf("Set() error = %v, want none", err)
				}

				return
			}
			if !httpio.HasConflict(err) || httpio.Message(err) != tt.wantRefusal {
				t.Fatalf("Set() error = %v, want the Conflict %q", err, tt.wantRefusal)
			}
		})
	}

	t.Run("the session's directory is held to the rules: an administrator signed in through a Workspace sets it, one signed in with a password is refused", func(t *testing.T) {
		t.Parallel()

		fromSession := func(ctx context.Context, station accesstypes.Domain) (signin.Directory, error) {
			var got signin.Directory
			err := client.ExecuteFunc(ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				var err error
				got, err = rules.FromSession(ctx, txn, station)

				return err
			})

			return got, err
		}
		through := establishedBy(sessioninfo.AuthEvent{Method: other.Method, Connection: other.Connection})
		if got, err := fromSession(through, "gamma"); err != nil || got != other {
			t.Errorf("FromSession() through a Workspace = %+v, %v, want %+v", got, err, other)
		}
		password := establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword})
		if _, err := fromSession(password, "gamma"); !httpio.HasConflict(err) || !strings.HasPrefix(httpio.Message(err), "You are signed in with a password.") {
			t.Errorf("FromSession() with a password error = %v, want the Conflict that says to sign in through the directory", err)
		}
		taken := establishedBy(sessioninfo.AuthEvent{Method: workspace.Method, Connection: workspace.Connection})
		if _, err := fromSession(taken, "gamma"); !httpio.HasConflict(err) || !strings.Contains(httpio.Message(err), "already has a station here") {
			t.Errorf("FromSession() through a Workspace another station has error = %v, want the Conflict that says so", err)
		}
	})

	t.Run("the directory of a station that is directory only is not removed, and another station's is", func(t *testing.T) {
		t.Parallel()

		remove := func(station accesstypes.Domain) error {
			return client.ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
				return rules.Remove(ctx, txn, station)
			})
		}
		want := "This station is directory only: its people sign in through its directory alone, so nobody could enter it without one. Turn directory only off first, then remove the directory."
		if err := remove("alpha"); !httpio.HasConflict(err) || httpio.Message(err) != want {
			t.Errorf("Remove() of a directory-only station's directory error = %v, want the Conflict %q", err, want)
		}
		if err := remove("beta"); err != nil {
			t.Errorf("Remove() of the directory of a station that admits passwords error = %v, want none", err)
		}
		noFlag := NewTenantDirectoryRules(stationsTable, stationsKey, &TenantDeclarationColumns{DirectoriesTable: "StationDirectories", TenantColumn: "StationId", MethodColumn: "Method", ConnectionColumn: "Connection"}, "station")
		if err := client.ExecuteFunc(t.Context(), func(ctx context.Context, txn ReadWriteTransaction) error {
			return noFlag.Remove(ctx, txn, "alpha")
		}); err != nil {
			t.Errorf("Remove() on a record with no directory-only column error = %v, want none", err)
		}
	})
}

// TestTenantDirectoryRules_schema proves the schema's half of the two rules on the
// emulator, for a write that never asked the rules: the unique index on the tenant
// refuses a second directory for a station, and the unique index on the method and
// the connection refuses a directory another station has, each at the commit.
func TestTenantDirectoryRules_schema(t *testing.T) {
	t.Parallel()

	workspace := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "frontier.example"}
	other := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "elsewhere.example"}

	tests := []struct {
		name      string
		station   accesstypes.Domain
		directory signin.Directory
	}{
		{name: "a second directory for a station", station: "alpha", directory: other},
		{name: "a directory another station has", station: "beta", directory: workspace},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db, client := tenantDatabase(t, "directory-schema-"+string(rune('a'+i)), "alpha", "beta")
			declareStation(t, db, "alpha", false, workspace)
			err := client.ExecuteFunc(t.Context(), func(_ context.Context, txn ReadWriteTransaction) error {
				return insertStationDirectory(txn, "second", tt.station, tt.directory)
			})
			if spanner.ErrCode(err) != codes.AlreadyExists && !httpio.HasConflict(err) {
				t.Fatalf("the commit error = %v, want the unique index to refuse the row", err)
			}
			if rows := countStationDirectories(t, db); rows != 1 {
				t.Errorf("StationDirectories holds %d row(s) after the refused write, want the 1 it held", rows)
			}
		})
	}
}

// countStationDirectories counts the directories record's rows, as committed.
func countStationDirectories(t *testing.T, db *initiator.SpannerDB) int64 {
	t.Helper()

	iter := db.Client.Single().Query(t.Context(), spanner.Statement{SQL: "SELECT COUNT(*) FROM StationDirectories"})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		t.Fatalf("spanner.RowIterator.Next() error = %v", err)
	}
	var n int64
	if err := row.Column(0, &n); err != nil {
		t.Fatalf("spanner.Row.Column() error = %v", err)
	}

	return n
}
