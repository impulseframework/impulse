//go:build skipAuth

package integration

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/crew"
	"github.com/impulseframework/impulse/lodestar/pkg/auth/members"
	"github.com/impulseframework/impulse/resource/live"
)

// This suite holds, on the served stack, that what a sign-in writes reaches the open
// pages: the session the library makes, the person's account, the identity a directory
// links, the roles an accepted invitation, a directory's default role or a sign-up gives,
// and what a person's leaving a sector writes. None of it is written by a generated
// handler, so nothing of it is a request's own write; the auth's identity client
// announces it through the application's live service once the generated handlers have
// handed it the service and the derivations (AnnounceThrough).
//
// Each case registers subscriptions on the stack's live service for principals of its
// own, who stand for the open pages of other people, then acts, then reads what each
// principal was told. The subscriptions are registered after every earlier step of the
// case, which may publish changes of its own.

// signInWatcherTab is the tab every watcher of the suite holds its subscriptions on.
const signInWatcherTab = "tab-sign-in-watcher"

// signInWatcher is the live principal of a page the suite holds open: a person of the
// named auth the cast does not hold, named by the word given.
func signInWatcher(auth, word string) string {
	return auth + live.AuthSeparator + "sign-in-watcher-" + word
}

// watching registers the subscriptions on the stack's live service, each on the
// suite's tab and standing for an hour.
func watching(ctx context.Context, t *testing.T, s *served, subs ...live.Subscription) {
	t.Helper()

	expiry := time.Now().Add(time.Hour)
	for i := range subs {
		subs[i].Tab = signInWatcherTab
		subs[i].Expiry = expiry
	}
	if err := s.live.Register(ctx, subs); err != nil {
		t.Fatalf("live.Fake.Register() error = %v", err)
	}
}

// toldOf reports whether the principal's change set holds a document of the resource
// under the key: the row's key for a row's change, "" for a list's or the whole
// resource's.
func toldOf(fake *live.Fake, principal string, res accesstypes.Resource, key string) bool {
	changes := fake.Changes(principal)
	for i := range changes {
		if changes[i].Resource == res && changes[i].Key == key {
			return true
		}
	}

	return false
}

// wantTold fails the test unless the principal was told of the resource under the key,
// naming what the page stands for.
func wantTold(t *testing.T, s *served, principal string, res accesstypes.Resource, key, page string) {
	t.Helper()

	if !toldOf(s.live, principal, res, key) {
		t.Errorf("%s was not told of %s (key %q): its change set = %v", page, res, key, changeNames(s.live.Changes(principal)))
	}
}

// freshConsole is a browser of the console that has taken its XSRF token and is signed
// in as nobody.
func freshConsole(ctx context.Context, t *testing.T, s *served) *browser {
	t.Helper()

	b := newBrowser(t, s, consoleAPI)
	if status, body := b.do(ctx, http.MethodGet, consoleAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session on a fresh browser: status %d: %s", status, body)
	}

	return b
}

// TestSignInLive_personaSignIn signs a persona in on the console with her password and
// sign-in code. The page that lists the crew's sessions is told of the list, the page that
// shows her account is told of its row, and a sector's People list is told in a sector the
// sign-in names nowhere, since the account a sector's people are read from is global.
func TestSignInLive_personaSignIn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	const persona accesstypes.User = "marshal"
	id, ok := accountID(persona)
	if !ok {
		t.Fatalf("the cast holds no account %s", persona)
	}
	sessions, account, people := signInWatcher(crew.Name, "sessions"), signInWatcher(crew.Name, "account"), signInWatcher(crew.Name, "people")
	watching(ctx, t, s,
		live.Subscription{Principal: sessions, Resource: "CrewListedSessions"},
		live.Subscription{Principal: account, Resource: "CrewSessionUsers", Key: id.String()},
		live.Subscription{Principal: people, Resource: crewSectorPeople, Domain: bastion},
	)

	b := newBrowser(t, s, consoleAPI)
	b.signIn(ctx, string(persona))
	if sess := b.session(ctx); sess["authenticated"] != true {
		t.Fatalf("the marshal's session after her sign-in = %v, want authenticated", sess)
	}

	wantTold(t, s, sessions, "CrewListedSessions", "", "the page listing the crew's sessions")
	wantTold(t, s, account, "CrewSessionUsers", id.String(), "the page showing the marshal's account")
	wantTold(t, s, people, crewSectorPeople, "", "Bastion's People list")
}

// TestSignInLive_invitationAcceptedBeforeTheSecondFactor invites a stranger's Gmail
// address into Anvil with a role, and the stranger opens the link and signs up with that
// Google account. Google vouches for the address, so the callback accepts the invitation:
// the account is made, the role given, the link spent and the identity linked, and the
// sign-in then waits for the second factor. Before any is completed, the pages that list
// the accounts, Anvil's people, the roles held, the mailed links, the sign-in identities
// and a person's ways to sign in, the whole application's and Anvil's, are told: the
// acceptance announced its writes, not the session that has not yet started.
func TestSignInLive_invitationAcceptedBeforeTheSecondFactor(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	const voyager = "voyager@gmail.com"
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	inviteCrew(ctx, t, hq, `{"email":"`+voyager+`","roles":["SectorCrew"],"domain":"`+anvil+`"}`)
	_, token := lastMailTo(t, s, voyager, "invitation")
	stranger := newBrowser(t, s, consoleAPI)
	openInvitation(ctx, t, stranger, token)

	accounts, people, held := signInWatcher(crew.Name, "accounts"), signInWatcher(crew.Name, "people"), signInWatcher(crew.Name, "held")
	links, identities := signInWatcher(crew.Name, "links"), signInWatcher(crew.Name, "identities")
	ways, sectorWays := signInWatcher(crew.Name, "ways"), signInWatcher(crew.Name, "sector-ways")
	watching(ctx, t, s,
		live.Subscription{Principal: accounts, Resource: "CrewSessionUsers"},
		live.Subscription{Principal: people, Resource: crewSectorPeople, Domain: anvil},
		live.Subscription{Principal: held, Resource: "CrewHeldRoles"},
		live.Subscription{Principal: links, Resource: "CrewSessionMailLinks"},
		live.Subscription{Principal: identities, Resource: "CrewSessionIdentities"},
		live.Subscription{Principal: ways, Resource: "CrewSignInWays"},
		live.Subscription{Principal: sectorWays, Resource: "CrewSectorSignInWays", Domain: anvil},
	)

	pending := consoleLogin + "?pending=mfa&returnUrl=" + url.QueryEscape(consolePage)
	if status, location := stranger.loginConsoleSignUp(ctx, voyager); status != http.StatusFound || location != pending {
		t.Fatalf("the stranger's sign-up at the invitation: status %d to %q, want %d to %q", status, location, http.StatusFound, pending)
	}
	if sess := stranger.session(ctx); sess["authenticated"] == true {
		t.Fatalf("the stranger's session before the second factor = %v, want none yet", sess)
	}

	wantTold(t, s, accounts, "CrewSessionUsers", "", "the Accounts list")
	wantTold(t, s, people, crewSectorPeople, "", "Anvil's People list")
	wantTold(t, s, held, "CrewHeldRoles", "", "the stranger's held-roles list")
	wantTold(t, s, links, "CrewSessionMailLinks", "", "the mailed links list")
	wantTold(t, s, identities, "CrewSessionIdentities", "", "the sign-in identities list")
	wantTold(t, s, ways, "CrewSignInWays", "", "the stranger's ways to sign in")
	wantTold(t, s, sectorWays, "CrewSectorSignInWays", "", "the stranger's ways to sign in in Anvil")
}

// TestSignInLive_invitationMakesASector sends the invitation that makes a sector, and
// the invited person signs up at its link with a password and enters the code mailed to
// the invited address: the acceptance makes the sector, and the page that lists the
// sectors is told.
func TestSignInLive_invitationMakesASector(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	const founder, email = "dune-founder", "dune-founder@dune.example"
	hq := newBrowser(t, s, consoleAPI)
	hq.signIn(ctx, "governor")
	status, body, _ := inviteSector(ctx, t, hq, map[string]any{"email": email, "sectorName": "Dune", "sectorRegion": "Far frontier", "sectorEstablished": "2226-11-20"})
	assertStatus(t, status, http.StatusOK, body)
	_, token := lastMailTo(t, s, email, "invitation")
	b := newBrowser(t, s, consoleAPI)
	openInvitation(ctx, t, b, token)
	if status, body := b.do(ctx, http.MethodPost, consoleAPI+"/user/invitation/sign-up", jsonBody(t, map[string]string{"username": founder, "password": signUpPassword})); status != http.StatusOK {
		t.Fatalf("signing up at the invitation that makes Dune: status %d: %s", status, body)
	}

	sectors := signInWatcher(crew.Name, "sectors")
	watching(ctx, t, s, live.Subscription{Principal: sectors, Resource: "Sectors"})

	b.secondFactor(ctx, founder)
	if sectorNamed(ctx, t, hq, "Dune") == nil {
		t.Fatal("no sector Dune after its invitation was accepted")
	}

	wantTold(t, s, sectors, "Sectors", "", "the list of sectors")
}

// TestSignInLive_directorySeatsAtTheFirstSignIn signs a stranger in through the fleet's
// directory for the first time: Cinder's directory seats them with its default role, and
// Cinder's People list is told.
func TestSignInLive_directorySeatsAtTheFirstSignIn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	const seafarer = "seafarer@example.com"
	b := freshConsole(ctx, t, s)

	people := signInWatcher(crew.Name, "people")
	watching(ctx, t, s, live.Subscription{Principal: people, Resource: crewSectorPeople, Domain: cinder})

	if status, location := b.loginConsoleGoogle(ctx, seafarer); status != http.StatusFound || location != consolePage {
		t.Fatalf("the seafarer's first sign-in through the directory: status %d to %q, want %d to %q", status, location, http.StatusFound, consolePage)
	}

	wantTold(t, s, people, crewSectorPeople, "", "Cinder's People list")
}

// TestSignInLive_aPersonLeavesASector has the hazard analyst, crew in Anvil and in
// Cinder, leave Cinder from her own account page: Cinder's People list is told.
func TestSignInLive_aPersonLeavesASector(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServedWith(ctx, t, servedOptions{namesTenants: true})
	hazards := newBrowser(t, s, consoleAPI)
	hazards.signIn(ctx, "hazards")

	people := signInWatcher(crew.Name, "people")
	watching(ctx, t, s, live.Subscription{Principal: people, Resource: crewSectorPeople, Domain: cinder})

	if got := leaveTenant(ctx, t, hazards, consoleAPI+"/user/tenants/leave", cinder, false, http.StatusOK); got != `{"accountDeleted":false}` {
		t.Fatalf("leaving Cinder answered %s, want the account kept", got)
	}

	wantTold(t, s, people, crewSectorPeople, "", "Cinder's People list")
}

// TestSignInLive_portalSignIn signs a member in on the portal through its directory,
// which writes the member's roles: the page that lists the members' sessions is told.
func TestSignInLive_portalSignIn(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	s := newServed(ctx, t)
	b := newBrowser(t, s, portalAPI)
	if status, body := b.do(ctx, http.MethodGet, portalAPI+"/user/session", nil); status != http.StatusOK {
		t.Fatalf("GET session before the portal login: status %d: %s", status, body)
	}

	sessions := signInWatcher(members.Name, "sessions")
	watching(ctx, t, s, live.Subscription{Principal: sessions, Resource: "MembersListedSessions"})

	if status, location := b.loginPortal(ctx, clientUser, "ClientPortal"); status != http.StatusFound || location != portalPage {
		t.Fatalf("portal login: status %d to %q, want %d to %q", status, location, http.StatusFound, portalPage)
	}

	wantTold(t, s, sessions, "MembersListedSessions", "", "the page listing the members' sessions")
}
