//go:build skipAuth

package integration

import (
	"context"
	"net/http"
	"slices"
	"testing"
)

// invitedSectorOf reads the session as the browser and answers the sector the read names
// as the one an accepted invitation seated the person in, and whether it names one.
func invitedSectorOf(ctx context.Context, b *browser) (sector string, named bool) {
	b.t.Helper()

	value, named := b.session(ctx)["invitedTenant"]
	if !named {
		return "", false
	}
	sector, ok := value.(string)
	if !ok {
		b.t.Fatalf("the session read's invitedTenant = %v, want a sector's id", value)
	}

	return sector, true
}

// TestInvitedLanding holds where a person who accepts an invitation into a sector lands,
// on the served stack. The console opens in the sector the person last worked in, or
// their first by name; a person who already holds a seat in one sector and accepts an
// invitation into a second is there for the second, so the application opens in it. The
// browser chooses the page from what the session read says: the first read after an
// acceptance names the invitation's sector (invitedTenant), whatever else the person
// holds, and no later read does, so the next landing is where the person last worked
// again. A sign-in that accepts no invitation is told no sector and lands as ever.
//
//   - by password: the cadet, seated at Anvil, is invited into Cinder at the address she
//     holds, follows the link signed out, and signs in with her password and the sign-in
//     code on a browser nobody remembered;
//   - through the fleet's directory: a person whose first sign-in through it seated them
//     at Cinder is invited into Anvil, follows the link and signs in through the
//     directory;
//   - as they are: the pilot, signed in, follows a link into Cinder and accepts it
//     without signing in again.
//
// Demonstrates: auth.invitation.
func TestInvitedLanding(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	invite := func(t *testing.T, email, sector string) (token string) {
		t.Helper()
		inviteCrew(ctx, t, hq, `{"email":"`+email+`","roles":["SectorCrew"],"domain":"`+sector+`"}`)
		_, token = lastMailTo(t, s, email, "invitation")

		return token
	}
	toldOnce := func(t *testing.T, b *browser, who, sector string, holds []string) {
		t.Helper()
		if got, named := invitedSectorOf(ctx, b); !named || got != sector {
			t.Errorf("the first session read after %s accepted the invitation names the sector %q (named: %t), want %s: the console opens there", who, got, named, sector)
		}
		if got, named := invitedSectorOf(ctx, b); named {
			t.Errorf("a later session read of %s names the sector %q, want none: the invitation's sector is told once", who, got)
		}
		if got := b.domains(ctx); !slices.Equal(got, holds) {
			t.Errorf("the sectors of %s after the acceptance = %v, want %v", who, got, holds)
		}
	}

	t.Run("by password", func(t *testing.T) {
		t.Parallel()

		cadet := string(policyCadet)
		before := newBrowser(t, s, consoleAPI)
		before.signIn(ctx, cadet)
		if got, named := invitedSectorOf(ctx, before); named {
			t.Fatalf("the cadet's sign-in that accepted no invitation is told the sector %q, want none", got)
		}
		if got, want := before.domains(ctx), []string{anvil}; !slices.Equal(got, want) {
			t.Fatalf("the cadet's sectors before the invitation = %v, want %v", got, want)
		}
		token := invite(t, factorAddress(cadet), cinder)

		invited := newBrowser(t, s, consoleAPI)
		openInvitation(ctx, t, invited, token)
		status, body := invited.login(ctx, cadet, personaPassword)
		if status != http.StatusOK {
			t.Fatalf("the cadet's password sign-in at the invitation: status %d: %s", status, body)
		}
		if !heldForFactor(t, body) {
			t.Fatal("the cadet's sign-in on a browser nobody remembered did not wait for the sign-in code")
		}
		invited.secondFactor(ctx, cadet)
		toldOnce(t, invited, "the cadet", cinder, []string{anvil, cinder})
	})

	t.Run("through the fleet's directory", func(t *testing.T) {
		t.Parallel()

		const wayfarer = "wayfarer@example.com"
		before := newBrowser(t, s, consoleAPI)
		if status, body := before.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
			t.Fatalf("GET session before the first sign-in: status %d: %s", status, body)
		}
		if status, location := before.loginConsoleGoogle(ctx, wayfarer); status != http.StatusFound || location != consolePage {
			t.Fatalf("the wayfarer's first sign-in through the directory: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
		}
		if got, named := invitedSectorOf(ctx, before); named {
			t.Fatalf("the wayfarer's sign-in that accepted no invitation is told the sector %q, want none", got)
		}
		if got, want := before.domains(ctx), []string{cinder}; !slices.Equal(got, want) {
			t.Fatalf("the wayfarer's sectors before the invitation = %v, want %v by the directory's default seat", got, want)
		}
		token := invite(t, wayfarer, anvil)

		invited := newBrowser(t, s, consoleAPI)
		openInvitation(ctx, t, invited, token)
		if status, location := invited.loginConsoleGoogle(ctx, wayfarer); status != http.StatusFound || location != consolePage {
			t.Fatalf("the wayfarer's sign-in through the directory at the invitation: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
		}
		toldOnce(t, invited, "the wayfarer", anvil, []string{anvil, cinder})
	})

	t.Run("as they are", func(t *testing.T) {
		t.Parallel()

		pilot := newBrowser(t, s, consoleAPI)
		pilot.signIn(ctx, "pilot")
		held := pilot.domains(ctx)
		if slices.Contains(held, cinder) {
			t.Fatalf("the pilot's sectors before the invitation = %v, want Cinder not among them", held)
		}
		token := invite(t, factorAddress("pilot"), cinder)
		if status, body := pilot.do(ctx, http.MethodPost, consoleAPI+"/user/invitation", jsonBody(t, map[string]string{"token": token})); status != http.StatusOK {
			t.Fatalf("the pilot opens the invitation: status %d: %s", status, body)
		}
		if got, named := invitedSectorOf(ctx, pilot); named {
			t.Fatalf("the pilot's session read before she accepted names the sector %q, want none", got)
		}
		if status, body := pilot.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/accept", nil); status != http.StatusOK {
			t.Fatalf("the pilot accepts: status %d: %s", status, body)
		}
		want := append(slices.Clone(held), cinder)
		slices.Sort(want)
		toldOnce(t, pilot, "the pilot", cinder, want)
	})
}
