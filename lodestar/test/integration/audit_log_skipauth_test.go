//go:build skipAuth

package integration

// audit_log_skipauth_test: the acts of the crew's audit log that a sign-in through a
// directory takes part in, on the served stack with the fleet's simulated directory.
// The rest of the log is proven beside this file (audit_log_test).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/identity"
	"github.com/impulseframework/impulse/lodestar/app"
	"github.com/impulseframework/impulse/lodestar/pkg/identity/crewidentity"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// fleetDirectory is who acted, in the words of a row the fleet's Google Workspace, Cinder's
// directory, writes at a sign-in.
const fleetDirectory = "the directory example"

// throughFleetDirectory signs the persona in through the fleet's Google Workspace,
// which is Cinder's directory, on a browser of its own. The persona's login has a
// password, so the sign-in waits for it, which links the identity and opens the session.
func throughFleetDirectory(ctx context.Context, t *testing.T, s *served, user string) *browser {
	t.Helper()

	b := primedBrowser(ctx, t, s)
	status, location := b.loginConsoleGoogle(ctx, user)
	if want := consoleLogin + "?pending=confirmation&returnUrl=" + url.QueryEscape(consolePage); status != http.StatusFound || location != want {
		t.Fatalf("console Google login as %s: status %d to %q, want %d to %q", user, status, location, http.StatusFound, want)
	}
	confirmation, err := json.Marshal(map[string]string{"password": personaPassword})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := b.do(ctx, http.MethodPost, consoleAPI+"/user/pending/confirm", confirmation); status != http.StatusOK {
		t.Fatalf("confirming the sign-in with %s's password: status %d: %s", user, status, body)
	}

	return b
}

// TestAuditLog_eachActIsOneRowInWords holds three acts, each one row of the whole
// application's list, with who acted, the act and whom it was done to in words, the name
// of whom it was done to, the sector it was done in and the reason it carried. The
// governor gives the cadet a role in Anvil for three days, with a reason. The pilot signs
// in through the fleet's directory, which is Cinder's, so Cinder manages the account, and
// the governor, a marshal of every sector, removes the pilot's password there. The
// governor signs in through Cinder's directory himself, as the switch asks, and turns
// Cinder directory only.
func TestAuditLog_eachActIsOneRowInWords(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{namesTenants: true})

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	end := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Minute)
	const reason = "Covering the convoy run"
	giving := auditRequest(t, map[string]any{"userId": string(account(t, "cadet")), "role": "Pilot", "domain": anvil, "endsAt": end.Format(time.RFC3339), "reason": reason})
	if status, body := hq.do(ctx, http.MethodPost, crewMethod("assign-crew-role"), giving); status != http.StatusNoContent {
		t.Fatalf("the governor giving the cadet Pilot at Anvil for three days: status %d, want 204: %s", status, body)
	}

	throughFleetDirectory(ctx, t, s, "pilot")
	pilotID := crewAccountID(ctx, t, hq, "pilot")
	if status, body := hq.do(ctx, http.MethodPost, sectorPath(cinder, "remove-crew-sector-password"), jsonBody(t, map[string]string{"userId": pilotID})); status != http.StatusNoContent {
		t.Fatalf("the governor removing the pilot's password at Cinder: status %d, want 204: %s", status, body)
	}

	through := throughFleetDirectory(ctx, t, s, "governor")
	if status, body := through.do(ctx, http.MethodPost, sectorPath(cinder, "make-sector-directory-only"), []byte(`{}`)); status != http.StatusOK {
		t.Fatalf("the governor turning Cinder directory only, signed in through its directory: status %d, want 200: %s", status, body)
	}

	for _, want := range []auditLine{
		{Act: "roleGiven", Who: "governor", What: "Gave the role Pilot until " + end.Format("2006-01-02 15:04") + " UTC", DoneTo: "cadet", SubjectName: "cadet", TenantName: "Anvil", Reason: reason},
		{Act: "passwordRemoved", Who: "governor", What: "Removed the password", DoneTo: "pilot", SubjectName: "pilot", TenantName: "Cinder"},
		{Act: "directoryOnlyOn", Who: "governor", What: "Turned directory only on", DoneTo: "Cinder", SubjectName: "Cinder", TenantName: "Cinder"},
	} {
		if got := lineOf(oneAuditRow(ctx, t, hq, want.Act, want.SubjectName)); got != want {
			t.Errorf("the row of %s done to %s = %+v, want %+v", want.Act, want.SubjectName, got, want)
		}
	}
}

// TestAuditLog_aDirectoryActsAsItself holds the rows a directory writes at a sign-in:
// each names the directory as who acted, and no person, in the list and in the table.
// A stranger who signs in through the fleet's directory is given Cinder's seat, the
// role the directory's people start with. Where a person belongs to one sector, the
// directory moves a person of Bastion who signs in through it: it takes the role they
// hold in Bastion, the row saying where they belong now, and seats them in Cinder.
// Lodestar's own sign-in holds no such rule, so the suite runs the directory's hook of a
// client built with it, over the same tables, in a transaction of its own, as the
// session library runs the hook in the sign-in's.
func TestAuditLog_aDirectoryActsAsItself(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{namesTenants: true})

	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")

	// The seat of a stranger.
	const stranger = "wayfarer@example.com"
	if status, location := primedBrowser(ctx, t, s).loginConsoleGoogle(ctx, stranger); status != http.StatusFound || location != consolePage {
		t.Fatalf("the stranger's first sign-in through the fleet's directory: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
	}

	// The move of a person of Bastion.
	moverID := s.seedCrewAccount(ctx, t, "mover", nil)
	giveAuditedRole(ctx, t, hq, moverID.String(), "Pilot", bastion)
	strict, err := crewidentity.NewClient(s.access, s.crew.Session().API(), app.NewSectorMaker(s.tenants, s.live), identity.WithOneTenantPerPerson())
	if err != nil {
		t.Fatalf("crewidentity.NewClient() with one tenant per person: %v", err)
	}
	seat := []signin.DirectorySeat{{Tenant: accesstypes.Domain(cinder), Role: "SectorCrew", Directory: signin.Directory{Method: sessioninfo.MethodGoogle, Connection: "example"}}}
	var committed func(context.Context)
	if _, err := s.db.ReadWriteTransaction(ctx, func(ctx context.Context, tx *spanner.ReadWriteTransaction) error {
		seated, err := strict.DirectorySeated().Seat(ctx, tx, moverID, "mover", seat)
		if err != nil {
			return fmt.Errorf("DirectorySeated().Seat(mover): %w", err)
		}
		committed = seated

		return nil
	}); err != nil {
		t.Fatalf("the mover's sign-in through Cinder's directory: %v", err)
	}
	if committed != nil {
		committed(ctx)
	}

	seated := "Gave the role Sector crew, the role its directory's people start with"
	rows := map[string]auditLine{}
	for _, row := range auditRows(ctx, t, hq, "", "") {
		if who := text(row, "who"); !strings.HasPrefix(who, "the directory ") {
			continue
		}
		if actor := text(row, "actorUsername"); actor != "" {
			t.Errorf("the row %v names the person %q who acted, want none: a directory acted", row, actor)
		}
		stored := storedAuditRow(ctx, t, s, cell[string](t, row, "id"))
		for _, column := range []string{"ActorUserId", "ActorUsername"} {
			if got := storedText(t, stored, column); got != "" {
				t.Errorf("the table's %s of the directory's row %v = %q, want none", column, row, got)
			}
		}
		line := lineOf(row)
		rows[line.Act+" "+line.DoneTo+" "+line.TenantName] = line
	}
	for _, want := range []auditLine{
		{Act: "roleGiven", Who: fleetDirectory, What: seated, DoneTo: stranger, SubjectName: stranger, TenantName: "Cinder"},
		{Act: "roleTaken", Who: fleetDirectory, What: "Took the role Pilot: they signed in through the directory of Cinder and belong to it now", DoneTo: "mover", SubjectName: "mover", TenantName: "Bastion"},
		{Act: "roleGiven", Who: fleetDirectory, What: seated, DoneTo: "mover", SubjectName: "mover", TenantName: "Cinder"},
	} {
		if got := rows[want.Act+" "+want.DoneTo+" "+want.TenantName]; got != want {
			t.Errorf("the directory's row of %s done to %s in %s = %+v, want %+v", want.Act, want.DoneTo, want.TenantName, got, want)
		}
	}
	if len(rows) != 3 {
		t.Errorf("the rows a directory wrote = %v, want the three", rows)
	}
}
