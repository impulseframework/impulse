package resource

import (
	"context"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// TestEventActor holds the reading of an event source to its writing: whatever shape
// UserEvent writes, EventActor answers the person who acted by the name they signed in
// under, the actor of an impersonated session; a process's source and a caller's own
// words name no person and are answered as written.
func TestEventActor(t *testing.T) {
	t.Parallel()

	actorAlice := impulse.NullUUIDFromUUID(aliceID)
	written := []struct {
		name string
		ctx  context.Context
		want string
	}{
		{name: "an ordinary session of an account", ctx: accountCtx("alice@example.com", aliceID, nil), want: "alice@example.com"},
		{name: "an ordinary session of no account", ctx: sessionCtx("alice@example.com", nil), want: "alice@example.com"},
		{name: "a username that is a plain word", ctx: accountCtx("gale", aliceID, nil), want: "gale"},
		{name: "a username with a comma and a parenthesis of its own", ctx: accountCtx("gale, r (ops)", aliceID, nil), want: "gale, r (ops)"},
		{
			name: "an impersonated user answers the actor",
			ctx: accountCtx("bob@partner.org", bobID, &sessioninfo.Impersonation{
				Actor: "alice@example.com", ActorID: actorAlice, Principal: accesstypes.UserPrincipal(bobUser),
			}),
			want: "alice@example.com",
		},
		{
			name: "an impersonated user, an actor with no account here",
			ctx: accountCtx("bob@partner.org", bobID, &sessioninfo.Impersonation{
				Actor: "alice@example.com", ActorRealm: "admin-portal", Principal: accesstypes.UserPrincipal(bobUser),
			}),
			want: "alice@example.com",
		},
		{
			name: "an impersonated role answers the actor",
			ctx: sessionCtx("alice@example.com", &sessioninfo.Impersonation{
				Actor: "alice@example.com", ActorID: actorAlice, Principal: accesstypes.RolePrincipal("PartnerViewer"),
			}),
			want: "alice@example.com",
		},
		{
			name: "an impersonated role, an actor with no account here",
			ctx:  sessionCtx("alice@example.com", &sessioninfo.Impersonation{Actor: "alice@example.com", Principal: accesstypes.RolePrincipal("PartnerViewer")}),
			want: "alice@example.com",
		},
	}
	for _, tt := range written {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, source := range []string{UserEvent(tt.ctx), UserProcessEvent(tt.ctx, "nightly")} {
				name, person := EventActor(source)
				if name != tt.want || !person {
					t.Errorf("EventActor(%q) = (%q, %v), want (%q, true)", source, name, person, tt.want)
				}
			}
		})
	}

	others := []struct {
		name   string
		source string
	}{
		{name: "a process", source: ProcessEvent("MigrateFeatures")},
		{name: "a caller's own words", source: "harbormaster (test)"},
		{name: "nothing", source: ""},
		{name: "a session with nobody before it", source: ", session de6e1a12-2d4d-4c4d-aaf1-d82cb9a9eff5"},
	}
	for _, tt := range others {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			name, person := EventActor(tt.source)
			if name != tt.source || person {
				t.Errorf("EventActor(%q) = (%q, %v), want the source as written and no person", tt.source, name, person)
			}
		})
	}
}

// TestEventActorReadsTheWordsOfASource pins the limit EventActor's documentation states:
// an event source is a sentence with nothing escaped, so a username that holds the
// words UserEvent writes between the parts is read as the name that stands before
// them. The answer is for showing to a reader, never for deciding who may do what.
func TestEventActorReadsTheWordsOfASource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		want     string
	}{
		{name: "a username that holds the impersonating words", username: "gale impersonating wells", want: "gale"},
		{name: "a username that holds the role words", username: "gale as role Administrator", want: "gale"},
		{name: "a username that holds the session words", username: "gale, session 7", want: "gale, session 7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			source := UserEvent(accountCtx(tt.username, aliceID, nil))
			name, person := EventActor(source)
			if name != tt.want || !person {
				t.Errorf("EventActor(%q) = (%q, %v), want (%q, true)", source, name, person, tt.want)
			}
		})
	}
}
