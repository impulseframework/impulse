package session

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// stubActions is the application a declaration of impersonation is held to: the
// permissions it registers by resource, in the global scope and in a tenant's, the
// fields that carry a permission themselves, and the classes in use.
type stubActions struct {
	registered map[accesstypes.Resource][]accesstypes.Permission
	domain     map[accesstypes.Resource][]accesstypes.Permission
	fields     map[accesstypes.Resource]accesstypes.TagPermissions
	classes    []accesstypes.ActionClass
}

func (s stubActions) HasPermission(scope accesstypes.PermissionScope, permission accesstypes.Permission, res accesstypes.Resource) bool {
	if scope == accesstypes.DomainPermissionScope {
		return slices.Contains(s.domain[res], permission)
	}

	return slices.Contains(s.registered[res], permission)
}

func (s stubActions) ClassesInUse() []accesstypes.ActionClass {
	return s.classes
}

func (s stubActions) TagsRequiring(res accesstypes.Resource, permission accesstypes.Permission) []accesstypes.Tag {
	var tags []accesstypes.Tag
	for tag, permissions := range s.fields[res] {
		if slices.Contains(permissions, permission) {
			tags = append(tags, tag)
		}
	}
	slices.Sort(tags)

	return tags
}

// impersonationActions is the tests' application: three grants, two actions, a record
// that is listed, read and updated and never created or deleted, with one field that
// carries Update and one that carries Read alone, a tenant's action and the classes
// credential and sensitive.
func impersonationActions() stubActions {
	return stubActions{
		registered: map[accesstypes.Resource][]accesstypes.Permission{
			"ViewAsUser":  {accesstypes.Execute},
			"ActAsUser":   {accesstypes.Execute},
			"AssumeRole":  {accesstypes.Execute},
			"SetPassword": {accesstypes.Execute},
			"Launch":      {accesstypes.Execute},
			"Ships":       {accesstypes.List, accesstypes.Read, accesstypes.Update},
		},
		domain: map[accesstypes.Resource][]accesstypes.Permission{
			"ScrapShip": {accesstypes.Execute},
		},
		fields: map[accesstypes.Resource]accesstypes.TagPermissions{
			"Ships": {"name": {accesstypes.Read, accesstypes.Update}, "registry": {accesstypes.Read}},
		},
		classes: []accesstypes.ActionClass{accesstypes.ClassCredential, "sensitive"},
	}
}

var (
	readOnlyMask         = accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read)
	exceptCredentialMask = accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential))
)

// declaredImpersonations is the three kinds the tests ask of: view as a person, read
// only at most; act as a person, everything except the credential actions; act as a
// role, unmasked.
func declaredImpersonations(t *testing.T) *Impersonations {
	t.Helper()

	declared, err := NewImpersonations(impersonationActions(),
		ImpersonatesUser("view", "ViewAsUser", readOnlyMask),
		ImpersonatesUser("act", "ActAsUser", exceptCredentialMask),
		ImpersonatesRole("role", "AssumeRole", accesstypes.AllowAll()),
	)
	if err != nil {
		t.Fatalf("NewImpersonations() error = %v", err)
	}

	return declared
}

// TestNewImpersonations pins what a declaration is refused for when the application
// starts: each refusal says what is wrong and what to do.
func TestNewImpersonations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		actions ImpersonationActions
		kinds   []ImpersonationKind
		wantErr string
	}{
		{
			name:    "the three kinds",
			actions: impersonationActions(),
			kinds: []ImpersonationKind{
				ImpersonatesUser("view", "ViewAsUser", readOnlyMask),
				ImpersonatesUser("act", "ActAsUser", exceptCredentialMask),
				ImpersonatesRole("role", "AssumeRole", accesstypes.AllowAll()),
			},
		},
		{
			name:    "a mask that names an action, a record's update and a tenant's action",
			actions: impersonationActions(),
			kinds: []ImpersonationKind{
				ImpersonatesUser("act", "ActAsUser", accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("Launch"), accesstypes.RefusePermission(accesstypes.Update, "Ships"), accesstypes.RefuseAction("ScrapShip"))),
			},
		},
		{
			name:    "no actions to hold the declaration to",
			kinds:   []ImpersonationKind{ImpersonatesUser("view", "ViewAsUser", readOnlyMask)},
			wantErr: "the impersonation an application offers is held to its actions: pass the generated collection (router.Collection())",
		},
		{
			name:    "a kind with no name",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("", "ViewAsUser", readOnlyMask)},
			wantErr: `a kind of impersonation has no name (its grant is "ViewAsUser"): a request names the kind it asks for`,
		},
		{
			name:    "two kinds of one name",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("view", "ViewAsUser", readOnlyMask), ImpersonatesUser("view", "ActAsUser", readOnlyMask)},
			wantErr: `two kinds of impersonation are named "view": a request names one kind`,
		},
		{
			name:    "a grant that is no action of the application",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("view", "ViewAsAnyone", readOnlyMask)},
			wantErr: `the kind "view" is allowed by the grant "ViewAsAnyone", which is no action of the application: register it by hand (@manualAddResource(Execute) on the constant) and give it to a role`,
		},
		{
			name:    "a grant that is a record and no action",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("view", "Ships", readOnlyMask)},
			wantErr: `the kind "view" is allowed by the grant "Ships", which is no action of the application`,
		},
		{
			name:    "two kinds under one grant",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("view", "ViewAsUser", readOnlyMask), ImpersonatesUser("act", "ViewAsUser", exceptCredentialMask)},
			wantErr: `the kinds "view" and "act" are both allowed by the grant "ViewAsUser": a grant carries one widest mask, so each kind takes a grant of its own`,
		},
		{
			name:    "a mask that refuses a class no action carries",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("act", "ActAsUser", accesstypes.AllowAll().Refusing(accesstypes.RefuseClass("credentials")))},
			wantErr: `the widest mask of the kind "act" refuses the class "credentials", which no action of the application carries (the classes in use: credential, sensitive)`,
		},
		{
			name:    "a mask that refuses an action the application does not have",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("act", "ActAsUser", accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("SetPasword")))},
			wantErr: `the widest mask of the kind "act" refuses Execute on "SetPasword", and the application registers no Execute on a resource of that name`,
		},
		{
			name:    "a mask that refuses the three writes of a record that offers one of them",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("act", "ActAsUser", accesstypes.AllowAll().Refusing(accesstypes.RefuseWrites("Ships")...))},
		},
		{
			name:    "a mask that refuses one field of a record, which carries the permission itself",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("act", "ActAsUser", accesstypes.AllowAll().Refusing(accesstypes.RefusePermission(accesstypes.Update, "Ships.name")))},
		},
		{
			name:    "a mask that refuses the writes of a record the application does not have",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("act", "ActAsUser", accesstypes.AllowAll().Refusing(accesstypes.RefuseWrites("Shipz")...))},
			wantErr: `the widest mask of the kind "act" refuses Create on "Shipz", and the application registers no Create on a resource of that name`,
		},
		{
			name:    "a mask that refuses a record's permission on an action",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("act", "ActAsUser", accesstypes.AllowAll().Refusing(accesstypes.RefusePermission(accesstypes.Delete, "Launch")))},
			wantErr: `the widest mask of the kind "act" refuses Delete on "Launch", and the application registers no Delete on a resource of that name`,
		},
		{
			name:    "a mask that refuses a record as an action",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("act", "ActAsUser", accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("Ships")))},
			wantErr: `the widest mask of the kind "act" refuses Execute on "Ships", and the application registers no Execute on a resource of that name`,
		},
		{
			name:    "a mask that refuses a field the record does not have",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("act", "ActAsUser", accesstypes.AllowAll().Refusing(accesstypes.RefusePermission(accesstypes.Update, "Ships.nme")))},
			wantErr: `the widest mask of the kind "act" refuses Update on the field "Ships.nme", and the application checks no Update on a field of that name: a field is asked about by itself only when it carries the permission, so name such a field, or name the record to refuse all of it`,
		},
		{
			name:    "a mask that refuses a permission a field does not carry",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("act", "ActAsUser", accesstypes.AllowAll().Refusing(accesstypes.RefusePermission(accesstypes.Update, "Ships.registry")))},
			wantErr: `the widest mask of the kind "act" refuses Update on the field "Ships.registry", and the application checks no Update on a field of that name`,
		},
		{
			name:    "a mask that keeps a kind spelled as no permission kind is",
			actions: impersonationActions(),
			kinds:   []ImpersonationKind{ImpersonatesUser("view", "ViewAsUser", accesstypes.MaskPermissions(accesstypes.DenyAll(), "list", "read"))},
			wantErr: `the widest mask of the kind "view" keeps the kind "list", which is no permission kind: the kinds are Create, Delete, Execute, List, Read and Update, spelled so`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			declared, err := NewImpersonations(tt.actions, tt.kinds...)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewImpersonations() error = %v, want it to carry %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("NewImpersonations() error = %v", err)
			}
			var names []string
			for _, kind := range declared.Kinds() {
				names = append(names, kind.Name())
			}
			var want []string
			for _, kind := range tt.kinds {
				want = append(want, kind.Name())
			}
			if !slices.Equal(names, want) {
				t.Errorf("Kinds() = %v, want %v in the order declared", names, want)
			}
		})
	}
}

// clientStatus reads the status a refusal answers a client with, 0 for an error that
// is no client message.
func clientStatus(err error) int {
	switch {
	case httpio.HasBadRequest(err):
		return http.StatusBadRequest
	case httpio.HasForbidden(err):
		return http.StatusForbidden
	default:
		return 0
	}
}

// TestImpersonations_Request pins the grant and its mask: a session's mask is the
// kind's widest when the request names none, the narrower one when it names one, and a
// request for more than the kind gives, by a person who does not hold the grant, or
// for something the application does not have, is refused with no request to establish.
func TestImpersonations_Request(t *testing.T) {
	t.Parallel()

	holdsAll := []accesstypes.Resource{"ViewAsUser", "ActAsUser", "AssumeRole"}

	tests := []struct {
		name string
		ask  ImpersonationAsk
		// holds are the grants the person asking holds.
		holds         []accesstypes.Resource
		wantMask      accesstypes.PermissionMask
		wantPrincipal accesstypes.Principal
		wantStatus    int
		wantMessage   string
		// wantAsked are the grants the person was asked about.
		wantAsked []accesstypes.Resource
	}{
		{
			name:          "view, no mask named: the kind's widest",
			ask:           ImpersonationAsk{Kind: "view", Principal: "bob"},
			holds:         holdsAll,
			wantMask:      readOnlyMask,
			wantPrincipal: accesstypes.UserPrincipal("bob"),
			wantAsked:     []accesstypes.Resource{"ViewAsUser"},
		},
		{
			name:          "view, a narrower mask named: the narrower one",
			ask:           ImpersonationAsk{Kind: "view", Principal: "bob", Mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Read)},
			holds:         holdsAll,
			wantMask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Read),
			wantPrincipal: accesstypes.UserPrincipal("bob"),
			wantAsked:     []accesstypes.Resource{"ViewAsUser"},
		},
		{
			name:        "view, more than the kind gives: refused, not trimmed",
			ask:         ImpersonationAsk{Kind: "view", Principal: "bob", Mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read, accesstypes.Update, accesstypes.Execute)},
			holds:       holdsAll,
			wantStatus:  http.StatusForbidden,
			wantMessage: `the kind "view" gives reading only, and the request asks for Execute and Update as well: ask for no more than the kind gives, or name no mask to take all of it`,
			wantAsked:   []accesstypes.Resource{"ViewAsUser"},
		},
		{
			name:          "act, no mask named: everything except the credential actions",
			ask:           ImpersonationAsk{Kind: "act", Principal: "bob"},
			holds:         holdsAll,
			wantMask:      exceptCredentialMask,
			wantPrincipal: accesstypes.UserPrincipal("bob"),
			wantAsked:     []accesstypes.Resource{"ActAsUser"},
		},
		{
			name:          "act, kinds named: those kinds, and the kind's refusal stands",
			ask:           ImpersonationAsk{Kind: "act", Principal: "bob", Mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read, accesstypes.Execute)},
			holds:         holdsAll,
			wantMask:      accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read, accesstypes.Execute).Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			wantPrincipal: accesstypes.UserPrincipal("bob"),
			wantAsked:     []accesstypes.Resource{"ActAsUser"},
		},
		{
			name:          "act, one more action refused: joined to the kind's refusal",
			ask:           ImpersonationAsk{Kind: "act", Principal: "bob", Mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("Launch"), accesstypes.RefuseClass("sensitive"))},
			holds:         holdsAll,
			wantMask:      accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential), accesstypes.RefuseClass("sensitive"), accesstypes.RefuseAction("Launch")),
			wantPrincipal: accesstypes.UserPrincipal("bob"),
			wantAsked:     []accesstypes.Resource{"ActAsUser"},
		},
		{
			name:          "role, no mask named: unmasked, as the declaration says",
			ask:           ImpersonationAsk{Kind: "role", Principal: "Dispatcher"},
			holds:         holdsAll,
			wantMask:      accesstypes.AllowAll(),
			wantPrincipal: accesstypes.RolePrincipal("Dispatcher"),
			wantAsked:     []accesstypes.Resource{"AssumeRole"},
		},
		{
			name:          "role, a narrower mask named",
			ask:           ImpersonationAsk{Kind: "role", Principal: "Dispatcher", Mask: readOnlyMask},
			holds:         holdsAll,
			wantMask:      readOnlyMask,
			wantPrincipal: accesstypes.RolePrincipal("Dispatcher"),
			wantAsked:     []accesstypes.Resource{"AssumeRole"},
		},
		{
			name:        "the grant to view is not the grant to act",
			ask:         ImpersonationAsk{Kind: "act", Principal: "bob"},
			holds:       []accesstypes.Resource{"ViewAsUser"},
			wantStatus:  http.StatusForbidden,
			wantMessage: `starting a session of the kind "act" takes Execute on ActAsUser, which you do not hold`,
			wantAsked:   []accesstypes.Resource{"ActAsUser"},
		},
		{
			name:        "the grant to act is not the grant to view",
			ask:         ImpersonationAsk{Kind: "view", Principal: "bob"},
			holds:       []accesstypes.Resource{"ActAsUser"},
			wantStatus:  http.StatusForbidden,
			wantMessage: `starting a session of the kind "view" takes Execute on ViewAsUser, which you do not hold`,
			wantAsked:   []accesstypes.Resource{"ViewAsUser"},
		},
		{
			name:        "a person without the grant learns nothing of the mask they asked for",
			ask:         ImpersonationAsk{Kind: "view", Principal: "bob", Mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.Update)},
			wantStatus:  http.StatusForbidden,
			wantMessage: `starting a session of the kind "view" takes Execute on ViewAsUser, which you do not hold`,
			wantAsked:   []accesstypes.Resource{"ViewAsUser"},
		},
		{
			name:        "a kind the application does not offer",
			ask:         ImpersonationAsk{Kind: "user", Principal: "bob"},
			holds:       holdsAll,
			wantStatus:  http.StatusBadRequest,
			wantMessage: `"user" is not a kind of impersonation this application offers: it offers view, act, role`,
		},
		{
			name:        "no principal",
			ask:         ImpersonationAsk{Kind: "view"},
			holds:       holdsAll,
			wantStatus:  http.StatusBadRequest,
			wantMessage: `a session of the kind "view" acts as someone or something: name the principal`,
		},
		{
			name:        "a mask that refuses a class no action carries",
			ask:         ImpersonationAsk{Kind: "act", Principal: "bob", Mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseClass("credentials"))},
			holds:       holdsAll,
			wantStatus:  http.StatusBadRequest,
			wantMessage: `the mask asked for refuses the class "credentials", which no action of the application carries (the classes in use: credential, sensitive)`,
			wantAsked:   []accesstypes.Resource{"ActAsUser"},
		},
		{
			name:        "a mask that refuses an action the application does not have",
			ask:         ImpersonationAsk{Kind: "act", Principal: "bob", Mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("Lunch"))},
			holds:       holdsAll,
			wantStatus:  http.StatusBadRequest,
			wantMessage: `the mask asked for refuses Execute on "Lunch", and the application registers no Execute on a resource of that name`,
			wantAsked:   []accesstypes.Resource{"ActAsUser"},
		},
		{
			name:        "act, kinds spelled as no permission kind is: refused, not started keeping nothing",
			ask:         ImpersonationAsk{Kind: "act", Principal: "bob", Mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), "list", "read")},
			holds:       holdsAll,
			wantStatus:  http.StatusBadRequest,
			wantMessage: `the mask asked for keeps the kind "list", which is no permission kind: the kinds are Create, Delete, Execute, List, Read and Update, spelled so`,
			wantAsked:   []accesstypes.Resource{"ActAsUser"},
		},
		{
			name:        "view, a kind spelled as no permission kind is: told so, not that it is more than the kind gives",
			ask:         ImpersonationAsk{Kind: "view", Principal: "bob", Mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, "read")},
			holds:       holdsAll,
			wantStatus:  http.StatusBadRequest,
			wantMessage: `the mask asked for keeps the kind "read", which is no permission kind: the kinds are Create, Delete, Execute, List, Read and Update, spelled so`,
			wantAsked:   []accesstypes.Resource{"ViewAsUser"},
		},
		{
			name:          "act, the writes of a record and one field refused: joined to the kind's refusal",
			ask:           ImpersonationAsk{Kind: "act", Principal: "bob", Mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseWrites("Ships")...).Refusing(accesstypes.RefusePermission(accesstypes.Read, "Ships.registry"))},
			holds:         holdsAll,
			wantMask:      exceptCredentialMask.Refusing(accesstypes.RefuseWrites("Ships")...).Refusing(accesstypes.RefusePermission(accesstypes.Read, "Ships.registry")),
			wantPrincipal: accesstypes.UserPrincipal("bob"),
			wantAsked:     []accesstypes.Resource{"ActAsUser"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			declared := declaredImpersonations(t)
			var asked []accesstypes.Resource
			holds := func(_ context.Context, grant accesstypes.Resource) (bool, error) {
				asked = append(asked, grant)

				return slices.Contains(tt.holds, grant), nil
			}

			tt.ask.Reason = "ticket 12"
			tt.ask.MaxDuration = 2 * time.Hour
			req, err := declared.Request(context.Background(), &tt.ask, holds)
			if !slices.Equal(asked, tt.wantAsked) {
				t.Errorf("grants asked about = %v, want %v", asked, tt.wantAsked)
			}
			if tt.wantStatus != 0 {
				if req != nil {
					t.Errorf("Request() answered a request to establish beside its refusal: %+v", req)
				}
				if got := clientStatus(err); got != tt.wantStatus {
					t.Fatalf("Request() error = %v (status %d), want status %d", err, got, tt.wantStatus)
				}
				if msg := httpio.Message(err); msg != tt.wantMessage {
					t.Errorf("Request() message = %q, want %q", msg, tt.wantMessage)
				}

				return
			}
			if err != nil {
				t.Fatalf("Request() error = %v", err)
			}
			if !req.Mask.Equal(tt.wantMask) {
				t.Errorf("Request().Mask = %q, want %q", req.Mask.Text(), tt.wantMask.Text())
			}
			if req.Principal != tt.wantPrincipal {
				t.Errorf("Request().Principal = %v, want %v", req.Principal, tt.wantPrincipal)
			}
			if req.Reason != "ticket 12" || req.MaxDuration != 2*time.Hour {
				t.Errorf("Request() reason %q, cap %v, want the ask's", req.Reason, req.MaxDuration)
			}
			if req.Actor != "" || req.SourceSessionID.Valid {
				t.Errorf("Request() without a session in the context names the actor %q: a server-to-server caller sets it", req.Actor)
			}
		})
	}
}

// TestImpersonations_Request_actorFromSession pins that a request made on a validated
// session names that session's user as the actor and the session as the actor's own.
func TestImpersonations_Request_actorFromSession(t *testing.T) {
	t.Parallel()

	sessionID := impulse.Must(impulse.UUIDFromString("92922509-82d2-4bc7-853a-d73b8926a55f"))
	ctx := context.WithValue(context.Background(), sessioninfo.CtxSessionInfo, &sessioninfo.SessionData{
		SessionInfo: &sessioninfo.SessionInfo{ID: sessionID, Username: "alice"},
	})

	req, err := declaredImpersonations(t).Request(ctx, &ImpersonationAsk{Kind: "view", Principal: "bob"}, func(context.Context, accesstypes.Resource) (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if req.Actor != "alice" || !req.SourceSessionID.Valid || req.SourceSessionID.UUID != sessionID {
		t.Errorf("Request() actor %q, source session %v, want alice and the session the request arrived on", req.Actor, req.SourceSessionID)
	}
}

// TestImpersonations_Request_grantCheckError pins that a grant check that fails is the
// request's failure, never a hold.
func TestImpersonations_Request_grantCheckError(t *testing.T) {
	t.Parallel()

	boom := errors.New("snapshot unavailable")
	req, err := declaredImpersonations(t).Request(context.Background(), &ImpersonationAsk{Kind: "view", Principal: "bob"}, func(context.Context, accesstypes.Resource) (bool, error) {
		return true, boom
	})
	if req != nil || !errors.Is(err, boom) {
		t.Errorf("Request() = %+v, %v, want no request and the check's error", req, err)
	}
}

// TestImpersonations_Request_ceilingIsNotChanged pins that a request's refusals never
// reach the declaration: the next request under the same kind starts from the kind's
// own widest mask.
func TestImpersonations_Request_ceilingIsNotChanged(t *testing.T) {
	t.Parallel()

	declared := declaredImpersonations(t)
	holds := func(context.Context, accesstypes.Resource) (bool, error) {
		return true, nil
	}

	if _, err := declared.Request(context.Background(), &ImpersonationAsk{Kind: "act", Principal: "bob", Mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("Launch"))}, holds); err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	next, err := declared.Request(context.Background(), &ImpersonationAsk{Kind: "act", Principal: "carol"}, holds)
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if !next.Mask.Equal(exceptCredentialMask) {
		t.Errorf("the next request's mask = %q, want the kind's own %q", next.Mask.Text(), exceptCredentialMask.Text())
	}
}
