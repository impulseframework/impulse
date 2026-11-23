package resource

import (
	"context"
	"strings"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// TestTenantDirectoryRules_vouch proves the rules of a tenant's word for its directory's
// second factor over the emulator: saying it is refused unless the tenant has a
// directory, the caller is signed in through it and the application asks somebody for a
// second factor, each with a sentence the person can act on; a tenant that vouches
// already is answered as it is; the warning says the consequence in words, or the same
// refusals; taking the word back asks nothing of the caller; and rules over a record
// that carries no column for the word answer an error of the program.
func TestTenantDirectoryRules_vouch(t *testing.T) {
	t.Parallel()

	workspace := signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "frontier.example"}
	entra := signin.Directory{Method: sessioninfo.MethodAzure, Connection: "6f2c1b7e-0000-4000-8000-000000000002"}
	provider := signin.Directory{Method: sessioninfo.MethodOIDC, Connection: "id.delta.example"}
	db, client := tenantDatabase(t, "vouch-rules", "alpha", "beta", "gamma", "delta")
	// alpha has the Workspace; beta has the Entra tenant; gamma has no directory; delta
	// has an organization's own OpenID Connect provider, and vouches for it already.
	declareStation(t, db, "alpha", false, workspace)
	declareStation(t, db, "beta", false, entra)
	declareStation(t, db, "delta", false, provider)
	vouchForStationDirectory(t, db, "delta", 0)

	through := func(directory signin.Directory) context.Context {
		return establishedBy(sessioninfo.AuthEvent{Method: directory.Method, Connection: directory.Connection})
	}
	password := establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodPassword})
	impersonation := establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodImpersonation, Connection: "alice"})
	personal := establishedBy(sessioninfo.AuthEvent{Method: sessioninfo.MethodGoogle, Connection: signin.GoogleAnyAccountConnection})

	// says is an auth's reader that answers its sign-in settings, and failing one that
	// fails wherever it is asked.
	says := func(required, applies bool) AuthSecondFactor {
		return func(context.Context, ReadWriteTransaction) (bool, bool, error) {
			return required, applies, nil
		}
	}
	failing := func(context.Context, ReadWriteTransaction) (bool, bool, error) {
		return false, false, errors.New("the sign-in settings could not be read")
	}
	asks := stationRules().SecondFactorsBy(says(true, true))
	asksNobody := stationRules().SecondFactorsBy(says(false, true))

	const (
		noDirectory = "This station has no directory, so there is no directory to vouch for. Set its directory first: sign in through your organization's directory and make it this station's directory."
		notThrough  = "Only a person who is signed in through this station's directory, your organization's Microsoft directory, can say that it asks its people for a second factor. %s Sign in through your organization's Microsoft directory and try again."
		nobodyAsked = "This application asks nobody for a second factor now, so there is nothing to turn off for people who sign in through your organization's Microsoft directory."
		vouches     = "This station already says that id.delta.example asks its people for a second factor. There is nothing to turn on."
		nothingBack = "This station has no directory, so there is nothing to take back."
		noColumn    = "resource: the directories record declares no second-factor column (@directorySecondFactor), so a tenant has no word to say for its directory"
	)
	betaRow := &TenantDirectory{Key: "beta-0", Directory: entra}
	deltaRow := &TenantDirectory{Key: "delta-0", Directory: provider, SecondFactor: true}

	vouchTests := []struct {
		name    string
		rules   *TenantDirectoryRules
		ctx     context.Context
		station accesstypes.Domain
		want    *TenantDirectory
		// wantRefusal is the Conflict's sentence; empty where the word may be said.
		wantRefusal string
	}{
		{name: "through the station's own directory, the row with its key", rules: asks, ctx: through(entra), station: "beta", want: betaRow},
		{name: "and through a Workspace that is the station's", rules: asks, ctx: through(workspace), station: "alpha", want: &TenantDirectory{Key: "alpha-0", Directory: workspace}},
		{name: "a station with no directory is refused, saying to set one first", rules: asks, ctx: through(entra), station: "gamma", wantRefusal: noDirectory},
		{
			name: "a caller signed in with a password is refused, saying how they signed in", rules: asks, ctx: password, station: "beta",
			wantRefusal: strings.Replace(notThrough, "%s", "You signed in with a password.", 1),
		},
		{
			name: "through another station's directory is refused, naming both", rules: asks, ctx: through(workspace), station: "beta",
			wantRefusal: strings.Replace(notThrough, "%s", "You signed in through Google Workspace frontier.example.", 1),
		},
		{
			name: "a personal account is refused", rules: asks, ctx: personal, station: "beta",
			wantRefusal: strings.Replace(notThrough, "%s", "You signed in with a personal Google account.", 1),
		},
		{
			name: "a session that views as someone else is refused", rules: asks, ctx: impersonation, station: "beta",
			wantRefusal: strings.Replace(notThrough, "%s", "This session is viewing the application as someone else, which is no sign-in through its directory.", 1),
		},
		{name: "an application that asks nobody for a second factor is refused", rules: asksNobody, ctx: through(entra), station: "beta", wantRefusal: nobodyAsked},
		{
			name: "a station that vouches already is answered as it is, by a password session, with nothing more asked", rules: stationRules().SecondFactorsBy(failing), ctx: password, station: "delta",
			want: deltaRow,
		},
		{name: "a reader that does not apply asks nothing", rules: stationRules().SecondFactorsBy(says(false, false)), ctx: through(entra), station: "beta", want: betaRow},
		{
			name: "of two readers, one that asks somebody is enough", rules: stationRules().SecondFactorsBy(says(false, true), says(true, true)), ctx: through(entra), station: "beta",
			want: betaRow,
		},
		{name: "rules built without readers ask nothing", rules: stationRules(), ctx: through(entra), station: "beta", want: betaRow},
	}
	for _, tt := range vouchTests {
		t.Run("VouchForDirectory: "+tt.name, func(t *testing.T) {
			t.Parallel()

			var got *TenantDirectory
			err := client.ExecuteFunc(tt.ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				var err error
				got, err = tt.rules.VouchForDirectory(ctx, txn, tt.station)

				return err
			})
			if tt.wantRefusal != "" {
				if !httpio.HasConflict(err) || httpio.Message(err) != tt.wantRefusal {
					t.Fatalf("VouchForDirectory() error = %v, want the Conflict %q", err, tt.wantRefusal)
				}

				return
			}
			if err != nil {
				t.Fatalf("VouchForDirectory() error = %v, want none", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("VouchForDirectory() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	warningTests := []struct {
		name        string
		rules       *TenantDirectoryRules
		ctx         context.Context
		station     accesstypes.Domain
		want        *Warning
		wantRefusal string
	}{
		{
			name: "the consequence and the button, naming the directory in words and nobody", rules: asks, ctx: through(entra), station: "beta",
			want: &Warning{
				Consequence: "You are saying that your organization's Microsoft directory asks its people for a second factor when they sign in. This application will stop asking them for a code.",
				Confirm:     "Stop asking people who sign in through your organization's Microsoft directory for a code",
			},
		},
		{name: "a station with no directory is refused as the call is", rules: asks, ctx: through(entra), station: "gamma", wantRefusal: noDirectory},
		{
			name: "a caller who is not signed in through the directory is refused as the call is", rules: asks, ctx: password, station: "beta",
			wantRefusal: strings.Replace(notThrough, "%s", "You signed in with a password.", 1),
		},
		{name: "an application that asks nobody is refused as the call is", rules: asksNobody, ctx: through(entra), station: "beta", wantRefusal: nobodyAsked},
		{name: "a station that vouches already has nothing to turn on", rules: asks, ctx: through(provider), station: "delta", wantRefusal: vouches},
	}
	for _, tt := range warningTests {
		t.Run("VouchForDirectoryWarning: "+tt.name, func(t *testing.T) {
			t.Parallel()

			var got *Warning
			err := client.ExecuteFunc(tt.ctx, func(ctx context.Context, txn ReadWriteTransaction) error {
				var err error
				got, err = tt.rules.VouchForDirectoryWarning(ctx, txn, tt.station)

				return err
			})
			if tt.wantRefusal != "" {
				if !httpio.HasConflict(err) || httpio.Message(err) != tt.wantRefusal {
					t.Fatalf("VouchForDirectoryWarning() error = %v, want the Conflict %q", err, tt.wantRefusal)
				}

				return
			}
			if err != nil {
				t.Fatalf("VouchForDirectoryWarning() error = %v, want none", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("VouchForDirectoryWarning() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	stopTests := []struct {
		name        string
		station     accesstypes.Domain
		want        *TenantDirectory
		wantRefusal string
	}{
		{name: "a station that vouches, by a password session and rules that ask nothing of it", station: "delta", want: deltaRow},
		{name: "a station that does not vouch is answered as it is", station: "beta", want: betaRow},
		{name: "a station with no directory has nothing to take back", station: "gamma", wantRefusal: nothingBack},
	}
	for _, tt := range stopTests {
		t.Run("StopVouchingForDirectory: "+tt.name, func(t *testing.T) {
			t.Parallel()

			var got *TenantDirectory
			err := client.ExecuteFunc(password, func(ctx context.Context, txn ReadWriteTransaction) error {
				var err error
				got, err = stationRules().SecondFactorsBy(failing).StopVouchingForDirectory(ctx, txn, tt.station)

				return err
			})
			if tt.wantRefusal != "" {
				if !httpio.HasConflict(err) || httpio.Message(err) != tt.wantRefusal {
					t.Fatalf("StopVouchingForDirectory() error = %v, want the Conflict %q", err, tt.wantRefusal)
				}

				return
			}
			if err != nil {
				t.Fatalf("StopVouchingForDirectory() error = %v, want none", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("StopVouchingForDirectory() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	// A record that carries the default seat alone: the word has no column, the three
	// rules answer an error of the program, and Directory answers as before.
	seatAlone := *stationColumns
	seatAlone.SecondFactorColumn = ""
	noWord := NewTenantDirectoryRules(stationsTable, stationsKey, &seatAlone, "station").SecondFactorsBy(says(true, true))

	t.Run("a record without the column answers the program's error from all three", func(t *testing.T) {
		t.Parallel()

		calls := map[string]func(ctx context.Context, txn ReadWriteTransaction) error{
			"VouchForDirectory": func(ctx context.Context, txn ReadWriteTransaction) error {
				_, err := noWord.VouchForDirectory(ctx, txn, "delta")

				return err
			},
			"VouchForDirectoryWarning": func(ctx context.Context, txn ReadWriteTransaction) error {
				_, err := noWord.VouchForDirectoryWarning(ctx, txn, "delta")

				return err
			},
			"StopVouchingForDirectory": func(ctx context.Context, txn ReadWriteTransaction) error {
				_, err := noWord.StopVouchingForDirectory(ctx, txn, "delta")

				return err
			},
		}
		for name, call := range calls {
			err := client.ExecuteFunc(through(provider), call)
			if err == nil || httpio.HasConflict(err) || !strings.Contains(err.Error(), noColumn) {
				t.Errorf("%s() error = %v, want an error that is no refusal and says %q", name, err, noColumn)
			}
		}
	})

	t.Run("Directory answers the row's word, and false where the record carries the default seat alone", func(t *testing.T) {
		t.Parallel()

		read := func(rules *TenantDirectoryRules) (*TenantDirectory, error) {
			var got *TenantDirectory
			err := client.ExecuteFunc(password, func(ctx context.Context, txn ReadWriteTransaction) error {
				var err error
				got, err = rules.Directory(ctx, txn, "delta")

				return err
			})

			return got, err
		}
		got, err := read(stationRules())
		if err != nil {
			t.Fatalf("Directory() error = %v", err)
		}
		if diff := cmp.Diff(deltaRow, got); diff != "" {
			t.Errorf("Directory() of a station that vouches mismatch (-want +got):\n%s", diff)
		}
		got, err = read(noWord)
		if err != nil {
			t.Fatalf("Directory() over a record with the default seat alone error = %v", err)
		}
		if diff := cmp.Diff(&TenantDirectory{Key: "delta-0", Directory: provider}, got); diff != "" {
			t.Errorf("Directory() over a record with the default seat alone mismatch (-want +got):\n%s", diff)
		}
	})
}
