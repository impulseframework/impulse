package sessioninfo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

func ctxWithSession(username string, imp *Impersonation) context.Context {
	return context.WithValue(context.Background(), CtxSessionInfo, &SessionData{
		SessionInfo:   &SessionInfo{ID: impulse.Must(impulse.UUIDFromString("de6e1a12-2d4d-4c4d-aaf1-d82cb9a9eff5")), Username: username},
		Impersonation: imp,
	})
}

func TestImpersonationAccessors(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.UUIDFromString("de6e1a12-2d4d-4c4d-aaf1-d82cb9a9eff5"))
	sourceID := impulse.Must(impulse.UUIDFromString("7a1d5f6e-0c2b-4a3e-9d8f-1b2c3d4e5f60"))

	// The accounts of alice and bob: what the permission store names each by.
	aliceID := impulse.Must(impulse.UUIDFromString("a11ce000-0000-4000-8000-000000000001"))
	bobID := impulse.Must(impulse.UUIDFromString("b0b00000-0000-4000-8000-000000000002"))
	alice, bob := accesstypes.User(aliceID.String()), accesstypes.User(bobID.String())

	userImp := &Impersonation{
		SessionID: sessionID,
		Actor:     "alice@example.com",
		Principal: accesstypes.UserPrincipal("bob@partner.org"),
		Mask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
	}
	roleImp := &Impersonation{
		SessionID:       sessionID,
		Actor:           "alice@example.com",
		ActorRealm:      "admin-portal",
		SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true},
		Principal:       accesstypes.RolePrincipal("PartnerViewer"),
	}
	// A session type whose users are accounts: alice, signed in here, as bob, and
	// alice under a role. The record names her account, and bob by his.
	accountImp := &Impersonation{
		SessionID:       sessionID,
		Actor:           "alice@example.com",
		ActorID:         impulse.NullUUIDFromUUID(aliceID),
		SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true},
		Principal:       accesstypes.UserPrincipal(bob),
	}
	accountRoleImp := &Impersonation{
		SessionID:       sessionID,
		Actor:           "alice@example.com",
		ActorID:         impulse.NullUUIDFromUUID(aliceID),
		SourceSessionID: impulse.NullUUID{UUID: sourceID, Valid: true},
		Principal:       accesstypes.RolePrincipal("Editor"),
	}

	tests := []struct {
		name          string
		ctx           context.Context
		wantImp       *Impersonation
		wantPrincipal accesstypes.Principal
		wantActor     string
		// wantActorUser is the real user behind the session as the permission store
		// names them.
		wantActorUser accesstypes.User
		wantMask      accesstypes.PermissionMask
		wantKind      string
		wantName      string
		wantAttrs     []Attribute
	}{
		{
			name:          "a session that belongs to no account is named by its username",
			ctx:           ctxWithSession("alice@example.com", nil),
			wantPrincipal: accesstypes.UserPrincipal("alice@example.com"),
			wantActor:     "alice@example.com",
			wantActorUser: "alice@example.com",
		},
		{
			name: "a session of an account is named by the id of the account, and read by its username",
			ctx: context.WithValue(context.Background(), CtxSessionInfo, &SessionData{
				SessionInfo: &SessionInfo{ID: sessionID, Username: "alice@example.com"},
				UserID:      impulse.NullUUIDFromUUID(aliceID),
			}),
			wantPrincipal: accesstypes.UserPrincipal(alice),
			wantActor:     "alice@example.com",
			wantActorUser: alice,
		},
		{
			name: "a resolver's choice wins over the username",
			ctx: context.WithValue(context.Background(), CtxSessionInfo, &SessionData{
				SessionInfo: &SessionInfo{ID: sessionID, Username: "alice@example.com"},
				Principal:   accesstypes.RolePrincipal("Editor"),
			}),
			wantPrincipal: accesstypes.RolePrincipal("Editor"),
			wantActor:     "alice@example.com",
			wantActorUser: "alice@example.com",
		},
		{
			name: "a session that acts as someone else acts as whom its record names, whatever principal the session carries",
			ctx: context.WithValue(context.Background(), CtxSessionInfo, &SessionData{
				SessionInfo:   &SessionInfo{ID: sessionID, Username: "bob@partner.org"},
				Impersonation: userImp,
				Principal:     accesstypes.RolePrincipal("Editor"),
			}),
			wantImp:       userImp,
			wantPrincipal: accesstypes.UserPrincipal("bob@partner.org"),
			wantActor:     "alice@example.com",
			wantActorUser: "alice@example.com",
			wantMask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
			wantKind:      "User",
			wantName:      "bob@partner.org",
			wantAttrs: []Attribute{
				{Key: AttrImpersonationActor, Value: "alice@example.com"},
				{Key: AttrImpersonationPrincipalKind, Value: "User"},
				{Key: AttrImpersonationPrincipal, Value: "bob@partner.org"},
				{Key: AttrImpersonationMask, Value: "List,Read"},
				{Key: AttrImpersonationSessionID, Value: sessionID.String()},
			},
		},
		{
			name:          "impersonated user, read-only",
			ctx:           ctxWithSession("bob@partner.org", userImp),
			wantImp:       userImp,
			wantPrincipal: accesstypes.UserPrincipal("bob@partner.org"),
			wantActor:     "alice@example.com",
			wantActorUser: "alice@example.com",
			wantMask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
			wantKind:      "User",
			wantName:      "bob@partner.org",
			wantAttrs: []Attribute{
				{Key: AttrImpersonationActor, Value: "alice@example.com"},
				{Key: AttrImpersonationPrincipalKind, Value: "User"},
				{Key: AttrImpersonationPrincipal, Value: "bob@partner.org"},
				{Key: AttrImpersonationMask, Value: "List,Read"},
				{Key: AttrImpersonationSessionID, Value: sessionID.String()},
			},
		},
		{
			// A foreign actor has no account here: the name the record carries is no
			// account's id, and so holds nothing in this application.
			name:          "impersonated role from another realm",
			ctx:           ctxWithSession("alice@example.com", roleImp),
			wantImp:       roleImp,
			wantPrincipal: accesstypes.RolePrincipal("PartnerViewer"),
			wantActor:     "alice@example.com",
			wantActorUser: "alice@example.com",
			wantKind:      "Role",
			wantName:      "PartnerViewer",
			wantAttrs: []Attribute{
				{Key: AttrImpersonationActor, Value: "alice@example.com"},
				{Key: AttrImpersonationPrincipalKind, Value: "Role"},
				{Key: AttrImpersonationPrincipal, Value: "PartnerViewer"},
				{Key: AttrImpersonationMask, Value: "unrestricted"},
				{Key: AttrImpersonationSessionID, Value: sessionID.String()},
				{Key: AttrImpersonationActorRealm, Value: "admin-portal"},
				{Key: AttrImpersonationSourceSessionID, Value: sourceID.String()},
			},
		},
		{
			name: "an account impersonated by an account: the subject is the impersonated account, the actor behind it the actor's",
			ctx: context.WithValue(context.Background(), CtxSessionInfo, &SessionData{
				SessionInfo:   &SessionInfo{ID: sessionID, Username: "bob@partner.org"},
				UserID:        impulse.NullUUIDFromUUID(bobID),
				Impersonation: accountImp,
			}),
			wantImp:       accountImp,
			wantPrincipal: accesstypes.UserPrincipal(bob),
			wantActor:     "alice@example.com",
			wantActorUser: alice,
			wantKind:      "User",
			wantName:      bobID.String(),
			wantAttrs: []Attribute{
				{Key: AttrImpersonationActor, Value: "alice@example.com"},
				{Key: AttrImpersonationPrincipalKind, Value: "User"},
				{Key: AttrImpersonationPrincipal, Value: bobID.String()},
				{Key: AttrImpersonationMask, Value: "unrestricted"},
				{Key: AttrImpersonationSessionID, Value: sessionID.String()},
				{Key: AttrImpersonationActorID, Value: aliceID.String()},
				{Key: AttrImpersonationSourceSessionID, Value: sourceID.String()},
			},
		},
		{
			// The session belongs to no account, and carries the actor's username: the
			// person behind it is still the actor's account, never that username.
			name:          "an account under a role: the session belongs to no account, and the actor behind it is the actor's account",
			ctx:           ctxWithSession("alice@example.com", accountRoleImp),
			wantImp:       accountRoleImp,
			wantPrincipal: accesstypes.RolePrincipal("Editor"),
			wantActor:     "alice@example.com",
			wantActorUser: alice,
			wantKind:      "Role",
			wantName:      "Editor",
			wantAttrs: []Attribute{
				{Key: AttrImpersonationActor, Value: "alice@example.com"},
				{Key: AttrImpersonationPrincipalKind, Value: "Role"},
				{Key: AttrImpersonationPrincipal, Value: "Editor"},
				{Key: AttrImpersonationMask, Value: "unrestricted"},
				{Key: AttrImpersonationSessionID, Value: sessionID.String()},
				{Key: AttrImpersonationActorID, Value: aliceID.String()},
				{Key: AttrImpersonationSourceSessionID, Value: sourceID.String()},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			imp, ok := ImpersonationFromCtx(tt.ctx)
			if ok != (tt.wantImp != nil) || imp != tt.wantImp {
				t.Errorf("ImpersonationFromCtx() = (%v, %v), want (%v, %v)", imp, ok, tt.wantImp, tt.wantImp != nil)
			}
			if got := PrincipalFromCtx(tt.ctx); got != tt.wantPrincipal {
				t.Errorf("PrincipalFromCtx() = %v, want %v", got, tt.wantPrincipal)
			}
			if got := ActorFromCtx(tt.ctx); got != tt.wantActor {
				t.Errorf("ActorFromCtx() = %q, want %q", got, tt.wantActor)
			}
			if got := ActorUserFromCtx(tt.ctx); got != tt.wantActorUser {
				t.Errorf("ActorUserFromCtx() = %q, want %q", got, tt.wantActorUser)
			}
			if got := MaskFromCtx(tt.ctx); got.String() != tt.wantMask.String() {
				t.Errorf("MaskFromCtx() = %v, want %v", got, tt.wantMask)
			}
			if imp == nil {
				return
			}
			if got := imp.PrincipalKind(); got != tt.wantKind {
				t.Errorf("PrincipalKind() = %q, want %q", got, tt.wantKind)
			}
			if got := imp.PrincipalName(); got != tt.wantName {
				t.Errorf("PrincipalName() = %q, want %q", got, tt.wantName)
			}
			if got := imp.ActorUser(); got != tt.wantActorUser {
				t.Errorf("ActorUser() = %q, want %q", got, tt.wantActorUser)
			}
			if diff := cmp.Diff(tt.wantAttrs, imp.Attributes()); diff != "" {
				t.Errorf("Attributes() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestImpersonationFromCtx_PanicsWithoutSession(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Error("ImpersonationFromCtx() did not panic without a session in the context")
		}
	}()

	ImpersonationFromCtx(context.Background())
}

func TestActorUserFromCtx_PanicsWithoutSession(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Error("ActorUserFromCtx() did not panic without a session in the context")
		}
	}()

	ActorUserFromCtx(context.Background())
}

func TestImpersonationFromRequest(t *testing.T) {
	t.Parallel()

	imp := &Impersonation{Actor: "alice", Principal: accesstypes.RolePrincipal("Editor"), StartedAt: time.Now()}
	r := httptest.NewRequestWithContext(ctxWithSession("alice", imp), http.MethodGet, "/", http.NoBody)

	got, ok := ImpersonationFromRequest(r)
	if !ok || got != imp {
		t.Errorf("ImpersonationFromRequest() = (%v, %v), want (%v, true)", got, ok, imp)
	}
}

// TestImpersonatorRefusal pins the one sentence and the one log line of the rule that
// nobody is the subject of what they do in a session that acts as someone else. The
// sentence speaks to the person; the line speaks of them, in the third person
// throughout, whatever words the act was said with.
func TestImpersonatorRefusal(t *testing.T) {
	t.Parallel()

	actor := impulse.Must(impulse.UUIDFromString("a11ce000-0000-4000-8000-000000000001"))

	tests := []struct {
		act      string
		wantLine string
	}{
		{act: "disable your own account", wantLine: "disable their own account"},
		{act: "give yourself a role", wantLine: "give themselves a role"},
		{act: "end a session of your own", wantLine: "end a session of their own"},
		{act: "make another hangar pass record your own", wantLine: "make another hangar pass record their own"},
		{act: "take what is yours, or send you a link", wantLine: "take what is theirs, or send them a link"},
		{act: "rename the youth team", wantLine: "rename the youth team"},
	}
	for _, tt := range tests {
		t.Run(tt.act, func(t *testing.T) {
			t.Parallel()

			line, refusal := ImpersonatorRefusal(actor, tt.act)
			wantLine := "impersonation: the person who started this impersonated session (a11ce000-0000-4000-8000-000000000001) asked to " + tt.wantLine +
				": refused, since nobody is the subject of what they do while they act as someone else"
			if line != wantLine {
				t.Errorf("ImpersonatorRefusal() line = %q, want %q", line, wantLine)
			}
			wantSays := "You are acting as someone else, so you cannot " + tt.act + "."
			if !httpio.HasForbidden(refusal) || httpio.Message(refusal) != wantSays {
				t.Errorf("ImpersonatorRefusal() refusal = %v, want a 403 that says %q", refusal, wantSays)
			}
		})
	}
}
