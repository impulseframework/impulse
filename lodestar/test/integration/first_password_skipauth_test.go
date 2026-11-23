//go:build skipAuth

package integration

import (
	"io"
	"net/http"
	"testing"

	"github.com/impulseframework/impulse/lodestar/pkg/auth"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
)

// TestFirstPassword_signsInWithItAndThroughTheDirectory proves on the served stack that a
// person who signed in through their directory alone, and then chose a first password,
// signs in anew with that password and anew through the directory as before. The sector
// whose directory he signs in through is not directory only here: the governor turns that
// off first, since a directory-only sector leaves its directory's accounts no password to
// choose. Before the password, a password login refuses him; after it, a session the
// password opens names no directory, and one the directory opens names the persona
// provider.
func TestFirstPassword_signsInWithItAndThroughTheDirectory(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{persona: &auth.Persona{Issuer: "https://persona.example.com", ClientID: "lodestar-pr12"}})
	if _, err := s.crew.SeedCast(ctx, io.Discard); err != nil {
		t.Fatalf("SeedCast() error = %v", err)
	}
	// The seed makes Bastion directory only, and its directory is the one the warden signs
	// in through: while that holds, his account has no password to choose. The governor
	// turns it off first, so what follows is a person whose sector takes a password too.
	governor := newBrowser(t, s, consoleAPI)
	governor.signIn(ctx, "governor")
	if status, body := governor.do(ctx, http.MethodPost, sectorPath(bastion, "end-sector-directory-only"), []byte(`{}`)); status != http.StatusOK {
		t.Fatalf("turning Bastion's directory only off as the governor: status %d, want 200: %s", status, body)
	}
	warden := newBrowser(t, s, consoleAPI)
	warden.signInAsPersona(ctx, "warden")

	// The contrast: he has no password yet, so a password login refuses him.
	before := newBrowser(t, s, consoleAPI)
	if status, body := before.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the warden's password login: status %d: %s", status, body)
	}
	if status, body := before.login(ctx, "warden", personaPassword); status != http.StatusUnauthorized {
		t.Fatalf("the warden's password login before he chose a password: status %d, want 401: %s", status, body)
	}

	// He adds his address, confirms it from the mailed link, and chooses his first
	// password with the code mailed to it.
	const wardensAddress = "warden@fleet.example"
	if status, body := warden.do(ctx, http.MethodPost, consoleAPI+"/user/email", jsonBody(t, map[string]string{"email": wardensAddress})); status != http.StatusOK {
		t.Fatalf("adding the warden's address: status %d: %s", status, body)
	}
	_, token := lastMailTo(t, s, wardensAddress, "verify")
	if status, body := warden.do(ctx, http.MethodPost, consoleAPI+"/user/email/verify", jsonBody(t, map[string]string{"token": token})); status != http.StatusOK {
		t.Fatalf("confirming the warden's address from the mailed link: status %d: %s", status, body)
	}
	code := firstPasswordCode(ctx, t, warden)
	chosen := jsonBody(t, map[string]string{"code": code, "password": personaPassword})
	if status, body := warden.do(ctx, http.MethodPost, consoleAPI+"/user/first-password", chosen); status != http.StatusOK {
		t.Fatalf("choosing the warden's first password with the code that was mailed: status %d: %s", status, body)
	}

	// A new browser signs in with the password.
	withPassword := newBrowser(t, s, consoleAPI)
	withPassword.signIn(ctx, "warden")
	sess := withPassword.session(ctx)
	if sess["authenticated"] != true || sess["username"] != "warden" {
		t.Errorf("the session of the warden's password sign-in = %v, want authenticated as the warden", sess)
	}
	if got := sessionDirectory(t, sess); got != nil {
		t.Errorf("the session of the warden's password sign-in reports the directory %+v, want none: the password opened it", got)
	}

	// Another new browser signs in through his directory, as before the password.
	throughDirectory := newBrowser(t, s, consoleAPI)
	throughDirectory.signInAsPersona(ctx, "warden")
	sess = throughDirectory.session(ctx)
	if sess["authenticated"] != true || sess["username"] != "warden" {
		t.Errorf("the session of the warden's sign-in through his directory = %v, want authenticated as the warden", sess)
	}
	if got := sessionDirectory(t, sess); got == nil || got.Connection != crew.PersonaRegistration {
		t.Errorf("the session of the warden's sign-in through his directory reports the directory %+v, want the persona provider", got)
	}
}
