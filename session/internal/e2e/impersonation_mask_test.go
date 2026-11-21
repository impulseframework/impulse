package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"cloud.google.com/go/spanner"
	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// The grants of the mask scenarios' three kinds of impersonation, and the action the
// application marks with a class of its own.
const (
	grantView accesstypes.Resource = "ViewAsUser"
	grantAct  accesstypes.Resource = "ActAsUser"
	grantFull accesstypes.Resource = "ActAsUserUnmasked"
	wireFunds accesstypes.Resource = "WireFunds"
)

// createTenant is the action of the mask scenarios' create-your-tenant route, as the
// generator names and classes it for a tenant record called Tenant. No grant admits the
// route, so no permission is checked there: the mask alone refuses it.
const createTenant accesstypes.Resource = "CreateTenant"

// maskAccessActions are the mask scenarios' sign-in settings, which the generator
// classes access, under the names a tenant record called Tenant and an auth called
// Staff give them: the two methods of the switch that makes a tenant sign in with its
// directory alone, which run in the tenant, and the setting that lets people ask at the
// sign-in page for a link that resets their password.
func maskAccessActions() []accesstypes.Resource {
	return []accesstypes.Resource{
		"EndTenantDirectoryOnly",
		"MakeTenantDirectoryOnly",
		"SetStaffSelfServiceReset",
	}
}

// maskCredentialActions are the mask scenarios' credential actions: the six acts the
// generated identity methods class credential, under the names an auth called Staff
// gives them, and the tenant's form of one.
func maskCredentialActions() []accesstypes.Resource {
	return []accesstypes.Resource{
		"RemoveStaffPassword",
		"RenameStaffUser",
		"RequireStaffPasswordChange",
		"SendStaffTenantUserPasswordResetLink",
		"SendStaffUserPasswordResetLink",
		"SetStaffUserEmail",
		"UnlinkStaffSessionIdentity",
	}
}

// maskCollection is the permission collection of the mask scenarios' application, as a
// generator would write it: the grants, the credential actions and the sign-in settings
// carrying the library's two classes, the action of the create-your-tenant route, which
// carries access too, an action carrying a class of the application's own, and two that
// carry none.
func maskCollection(t *testing.T) *resource.GeneratedCollection {
	t.Helper()

	execute := []accesstypes.Permission{accesstypes.Execute}
	resources := []resource.CollectionResource{
		{Name: grantView, Scope: accesstypes.GlobalPermissionScope, Permissions: execute},
		{Name: grantAct, Scope: accesstypes.GlobalPermissionScope, Permissions: execute},
		{Name: grantFull, Scope: accesstypes.GlobalPermissionScope, Permissions: execute},
		{Name: wireFunds, Scope: accesstypes.GlobalPermissionScope, Permissions: execute, Classes: []accesstypes.ActionClass{"money_movement"}},
		{Name: createTenant, Scope: accesstypes.GlobalPermissionScope, Permissions: execute, Classes: []accesstypes.ActionClass{accesstypes.ClassAccess}},
		{Name: "EndStaffUserSessions", Scope: accesstypes.GlobalPermissionScope, Permissions: execute},
		{Name: "Ledgers", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.List, accesstypes.Read, accesstypes.Update}},
	}
	for _, action := range maskCredentialActions() {
		scope := accesstypes.GlobalPermissionScope
		if strings.Contains(string(action), "Tenant") {
			scope = accesstypes.DomainPermissionScope
		}
		resources = append(resources, resource.CollectionResource{Name: action, Scope: scope, Permissions: execute, Classes: []accesstypes.ActionClass{accesstypes.ClassCredential}})
	}
	for _, action := range maskAccessActions() {
		scope := accesstypes.GlobalPermissionScope
		if strings.Contains(string(action), "Tenant") {
			scope = accesstypes.DomainPermissionScope
		}
		resources = append(resources, resource.CollectionResource{Name: action, Scope: scope, Permissions: execute, Classes: []accesstypes.ActionClass{accesstypes.ClassAccess}})
	}
	collection, err := resource.NewGeneratedCollection(resource.CollectionData{Resources: resources})
	if err != nil {
		t.Fatalf("resource.NewGeneratedCollection() error = %v", err)
	}

	return collection
}

// grantsEverything is a permission checker whose policy grants everything it is asked,
// so whatever a session of the mask scenarios is refused, its mask refused.
type grantsEverything struct {
	user accesstypes.User
}

func (g grantsEverything) Check(_ context.Context, _ accesstypes.Environment, _ accesstypes.Scope, _ accesstypes.Permission, resources ...accesstypes.Resource) (accesstypes.Decisions, error) {
	decisions := make(accesstypes.Decisions, len(resources))
	for _, res := range resources {
		decisions[res] = accesstypes.Granted()
	}

	return decisions, nil
}

func (grantsEverything) PermissionDigest(context.Context, accesstypes.Scope) (accesstypes.PermissionDigest, error) {
	return accesstypes.PermissionDigest{}, nil
}

func (grantsEverything) HasGrants(context.Context, accesstypes.Scope) (bool, error) {
	return true, nil
}

func (g grantsEverything) User() accesstypes.User {
	return g.user
}

func (grantsEverything) SuperAdministrator(context.Context) (bool, error) {
	return false, nil
}

func (grantsEverything) AdministersTenant(context.Context, accesstypes.Domain) (bool, error) {
	return false, nil
}

// maskAsk is the body of the mask scenarios' POST /impersonate: the kind, the person
// to act as and, optionally, a narrower mask than the kind gives.
type maskAsk struct {
	Kind          string                    `json:"kind"`
	Principal     string                    `json:"principal"`
	Mask          []accesstypes.Permission  `json:"mask"`
	RefuseClasses []accesstypes.ActionClass `json:"refuseClasses"`
	RefuseActions []accesstypes.Resource    `json:"refuseActions"`
}

// maskListed is one live impersonated session as the mask scenarios' list answers it:
// the mask as the record keeps it and as a person would say it.
type maskListed struct {
	SessionID  string `json:"sessionId"`
	Actor      string `json:"actor"`
	ActorRealm string `json:"actorRealm"`
	Mask       string `json:"mask"`
	Words      string `json:"words"`
}

// maskHandOff is the body of the mask scenarios' POST /handoff: the server-side
// caller's way in. The calling system names itself and the person it signed in, then
// asks as a person does.
type maskHandOff struct {
	System string `json:"system"`
	Actor  string `json:"actor"`
	maskAsk
}

// madeTenants are the tenants the mask scenarios' create-your-tenant route has made,
// each by the name of the person it made the tenant's administrator.
type madeTenants struct {
	mu     sync.Mutex
	makers []string
}

// add records a tenant made for the person.
func (m *madeTenants) add(maker string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.makers = append(m.makers, maker)
}

// list answers the makers of the tenants made so far, in the order they were made.
func (m *madeTenants) list() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.makers)
}

// capturedLog collects what the application logs through the standard logger while a
// mask scenario runs.
type capturedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *capturedLog) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	n, err := c.buf.Write(p)
	if err != nil {
		return n, errors.Wrap(err, "bytes.Buffer.Write()")
	}

	return n, nil
}

// take returns what was logged since the last call and forgets it.
func (c *capturedLog) take() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	logged := c.buf.String()
	c.buf.Reset()

	return logged
}

// TestImpersonationMask_storedAndEnforced runs the grant that carries a mask on each
// database: a Preauth over the shipped session and impersonation migrations, with the
// declaration of three kinds of impersonation in front of StartImpersonatedSession and
// the session permissions' masked check behind every action. On PostgreSQL and on
// Spanner alike, the record keeps the mask with its refusals in its one Mask column,
// the session reads it back and is refused what it refuses with the denial logged as
// the mask's, and a request for more than the grant gives starts nothing. A mask that
// refuses the class access refuses the tenant's two sign-in settings and the making of a
// tenant, which no grant admits and the mask alone decides, with nothing made; a session
// that acts with no mask does each. A server-side caller, which names an actor another
// system signed in, goes through the same declaration with a check of its own, and its
// session is held to the kind's mask. A request starts one session: a second start from
// the request, or from a copy of it, is refused with nothing written.
//
// The test runs on its own, never in parallel: the application logs through the
// standard logger, which the test redirects to read each denial.
func TestImpersonationMask_storedAndEnforced(t *testing.T) {
	table, err := sessionstorage.NewImpersonationTable("SessionImpersonations")
	if err != nil {
		t.Fatalf("sessionstorage.NewImpersonationTable() error = %v", err)
	}

	t.Run(postgresBackend.name, func(t *testing.T) {
		ctx := t.Context()

		database := preparePostgres(ctx, t, postgresSessions, postgresImpersonation)
		column := func(sessionID string) *string {
			t.Helper()
			var mask *string
			if err := database.QueryRow(ctx, `SELECT "Mask" FROM "SessionImpersonations" WHERE "SessionId" = $1`, sessionID).Scan(&mask); err != nil {
				t.Fatalf("read the record's Mask column: %v", err)
			}

			return mask
		}
		runImpersonationMask(ctx, t, sessionstorage.NewPostgresPreauth(database.Pool, sessionstorage.WithImpersonation(table)), column)
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		ctx := t.Context()

		database := prepareSpanner(ctx, t, spannerSessions, spannerImpersonation)
		column := func(sessionID string) *string {
			t.Helper()
			row, err := database.Client.Single().ReadRow(ctx, "SessionImpersonations", spanner.Key{sessionID}, []string{"Mask"})
			if err != nil {
				t.Fatalf("read the record's Mask column: %v", err)
			}
			var mask spanner.NullString
			if err := row.Column(0, &mask); err != nil {
				t.Fatalf("read the record's Mask column: %v", err)
			}
			if !mask.Valid {
				return nil
			}

			return &mask.StringVal
		}
		runImpersonationMask(ctx, t, sessionstorage.NewSpannerPreauth(database.Client, sessionstorage.WithImpersonation(table)), column)
	})
}

// newMaskApp serves the mask scenarios' application over a store: who holds which
// grant is its own table, and its policy grants every action, so a refusal is a mask's.
func newMaskApp[Tx any](t *testing.T, store *sessionstorage.Preauth[Tx]) *httptest.Server {
	t.Helper()

	auth, err := session.NewPreauth[session.NoCustomData](store, cookieKey)
	if err != nil {
		t.Fatalf("session.NewPreauth() error = %v", err)
	}
	api := auth.API()
	collection := maskCollection(t)
	tenants := &madeTenants{}
	impersonations, err := session.NewImpersonations(collection,
		session.ImpersonatesUser("view", grantView, accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read)),
		session.ImpersonatesUser("act", grantAct, accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential))),
		session.ImpersonatesUser("full", grantFull, accesstypes.AllowAll()),
	)
	if err != nil {
		t.Fatalf("session.NewImpersonations() error = %v", err)
	}
	// Who holds which grant: support views, a stand-in acts, and an owner acts with no
	// mask, which the declaration says in so many words.
	holds := map[string][]accesstypes.Resource{
		"support":  {grantView},
		"stand-in": {grantView, grantAct},
		"owner":    {grantView, grantAct, grantFull},
	}
	permissions := func(r *http.Request) resource.UserPermissions {
		forUser := func(user accesstypes.User) grantsEverything {
			return grantsEverything{user: user}
		}

		return resource.SessionPermissions(r.Context(), forUser, resource.RolePrincipalsUnsupported, nil, nil, collection)
	}
	// run answers 204 when the session's permissions grant Execute on the action in
	// the scope the collection registers it in, and 403 when they deny it.
	run := func(w http.ResponseWriter, r *http.Request) {
		action := accesstypes.Resource(chi.URLParam(r, "action"))
		scope := accesstypes.GlobalScope()
		if collection.HasPermission(accesstypes.DomainPermissionScope, accesstypes.Execute, action) {
			scope = accesstypes.DomainScope("tenant")
		}
		decisions, err := permissions(r).Check(r.Context(), resource.RequestEnvironment(), scope, accesstypes.Execute, action)
		if err != nil {
			_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

			return
		}
		if !decisions[action].IsGranted() {
			_ = httpio.NewEncoder(w).ClientMessage(r.Context(), httpio.NewForbiddenMessagef("Execute on %s is denied", action))

			return
		}
		w.WriteHeader(http.StatusNoContent)
	}

	r := chi.NewRouter()
	r.Use(auth.StartSession)
	r.Post("/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Username string `json:"username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		if _, err := api.Login(r.Context(), w, body.Username); err != nil {
			_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.Get("/authenticated", auth.Authenticated())
	// The server-side caller's way in: another system signed the actor in and hands
	// them over. The route sits outside ValidateSession, since the call arrives on no
	// session of this application, and the application's own check answers for the
	// kind's grant: which kinds the calling system may start. A real application
	// authenticates that system; the scenarios take its name from the body.
	systems := map[string][]accesstypes.Resource{
		"admin-portal": {grantView},
	}
	r.Post("/handoff", func(w http.ResponseWriter, r *http.Request) {
		var body maskHandOff
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		systemMay := func(_ context.Context, grant accesstypes.Resource) (bool, error) {
			return slices.Contains(systems[body.System], grant), nil
		}
		req, err := impersonations.RequestFor(r.Context(),
			session.ImpersonationActor{Name: body.Actor, Realm: body.System},
			&session.ImpersonationAsk{Kind: body.Kind, Principal: body.Principal, Mask: accesstypes.MaskPermissions(accesstypes.AllowAll(), body.Mask...)},
			systemMay,
		)
		if err != nil {
			_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

			return
		}
		id, err := api.StartImpersonatedSession(r.Context(), w, req)
		if err != nil {
			_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

			return
		}
		_ = httpio.NewEncoder(w).Ok(map[string]string{"sessionId": id.String(), "mask": req.Mask().Words()})
	})
	// One request, started from three times: a handler that keeps the request it was
	// given and starts again, from the request and from a copy of it. The answer carries
	// the session the first start made and what each later start was told, for the
	// scenarios to hold a request to one session.
	r.Post("/handoff/twice", func(w http.ResponseWriter, r *http.Request) {
		var body maskHandOff
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		systemMay := func(_ context.Context, grant accesstypes.Resource) (bool, error) {
			return slices.Contains(systems[body.System], grant), nil
		}
		req, err := impersonations.RequestFor(r.Context(),
			session.ImpersonationActor{Name: body.Actor, Realm: body.System}, &session.ImpersonationAsk{Kind: body.Kind, Principal: body.Principal}, systemMay,
		)
		if err != nil {
			_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

			return
		}
		id, err := api.StartImpersonatedSession(r.Context(), w, req)
		if err != nil {
			_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

			return
		}
		copied := *req
		again := make([]string, 0, 2)
		for _, later := range []*session.ImpersonationRequest{req, &copied} {
			// A later start answers into a response of its own, so a session it made
			// would not hide behind the first one's cookies.
			told := "a session was started"
			if _, err := api.StartImpersonatedSession(r.Context(), httptest.NewRecorder(), later); err != nil {
				told = err.Error()
			}
			again = append(again, told)
		}
		_ = httpio.NewEncoder(w).Ok(map[string]any{"sessionId": id.String(), "again": again})
	})
	r.Group(func(r chi.Router) {
		r.Use(auth.ValidateSession, auth.ValidateXSRFToken)

		r.Post("/impersonation/end", auth.EndImpersonation())
		r.Post("/impersonate", func(w http.ResponseWriter, r *http.Request) {
			var body maskAsk
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)

				return
			}
			asked := accesstypes.MaskPermissions(accesstypes.AllowAll(), body.Mask...)
			for _, class := range body.RefuseClasses {
				asked = asked.Refusing(accesstypes.RefuseClass(class))
			}
			for _, action := range body.RefuseActions {
				asked = asked.Refusing(accesstypes.RefuseAction(action))
			}
			held := func(ctx context.Context, grant accesstypes.Resource) (bool, error) {
				return slices.Contains(holds[sessioninfo.FromCtx(ctx).Username], grant), nil
			}
			req, err := impersonations.Request(r.Context(), &session.ImpersonationAsk{Kind: body.Kind, Principal: body.Principal, Mask: asked}, held)
			if err != nil {
				_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

				return
			}
			id, err := api.StartImpersonatedSession(r.Context(), w, req)
			if err != nil {
				_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

				return
			}
			_ = httpio.NewEncoder(w).Ok(map[string]string{"sessionId": id.String(), "mask": req.Mask().Words()})
		})
		r.Get("/impersonations", func(w http.ResponseWriter, r *http.Request) {
			imps, err := api.ActiveImpersonations(r.Context(), nil)
			if err != nil {
				_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

				return
			}
			listed := make([]maskListed, 0, len(imps))
			for _, imp := range imps {
				listed = append(listed, maskListed{SessionID: imp.SessionID.String(), Actor: imp.Actor, ActorRealm: imp.ActorRealm, Mask: imp.Mask.String(), Words: imp.Mask.Words()})
			}
			_ = httpio.NewEncoder(w).Ok(listed)
		})
		// The masked check on its own, behind a safe method: the read-only guard lets a
		// GET through, so a read-only session's denial here is the check's.
		r.Get("/check/{action}", run)
		// The create-your-tenant route's two answers, as the generator writes them. No
		// grant admits the route, so no permission is checked for a mask to deny: the
		// route asks the mask about its action itself, before anything else. Whether
		// the act is offered is answered without a word in the log, and the tenants
		// made are listed for the scenarios to count.
		r.Get("/create-tenant", func(w http.ResponseWriter, r *http.Request) {
			_, refused := resource.MaskRefusesAction(r.Context(), collection, createTenant)
			_ = httpio.NewEncoder(w).Ok(map[string]bool{"offered": !refused})
		})
		r.Get("/tenants", func(w http.ResponseWriter, _ *http.Request) {
			_ = httpio.NewEncoder(w).Ok(tenants.list())
		})
		// The action as an application serves it: behind the read-only guard.
		r.Group(func(r chi.Router) {
			r.Use(auth.EnforceReadOnlyMask)
			r.Post("/run/{action}", run)
			r.Post("/create-tenant", func(w http.ResponseWriter, r *http.Request) {
				if err := resource.RefuseActionUnderMask(r.Context(), collection, createTenant, "create a tenant"); err != nil {
					_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

					return
				}
				// The tenant is made for the person the session is: in a session that
				// acts as someone, the person acted as.
				tenants.add(sessioninfo.FromCtx(r.Context()).Username)
				w.WriteHeader(http.StatusCreated)
			})
		})
	})

	// TLS, because the auth cookies are Secure and a jar only sends them over https.
	server := httptest.NewTLSServer(r)
	t.Cleanup(server.Close)

	return server
}

// runImpersonationMask is the scenario on one database. column reads a record's Mask
// column straight from the table: nil for NULL.
func runImpersonationMask[Tx any](ctx context.Context, t *testing.T, store *sessionstorage.Preauth[Tx], column func(sessionID string) *string) {
	t.Helper()

	server := newMaskApp(t, store)
	credential := maskCredentialActions()
	access := maskAccessActions()

	var logged capturedLog
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(previous) })

	start := func(actor string, ask maskAsk) (b *browser, sessionID, words string) {
		t.Helper()
		b = newBrowser(t, server)
		b.login(ctx, actor)
		var body struct {
			SessionID string `json:"sessionId"`
			Mask      string `json:"mask"`
		}
		if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodPost, "/impersonate", ask), &body); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		return b, body.SessionID, body.Mask
	}
	listed := func(b *browser) map[string]maskListed {
		t.Helper()
		var entries []maskListed
		if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodGet, "/impersonations", nil), &entries); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}
		bySession := make(map[string]maskListed, len(entries))
		for _, entry := range entries {
			bySession[entry.SessionID] = entry
		}

		return bySession
	}

	// made answers the makers of the tenants the application has made so far, and
	// offered whether the create-your-tenant route offers the act to the session.
	made := func(b *browser) []string {
		t.Helper()
		var makers []string
		if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodGet, "/tenants", nil), &makers); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		return makers
	}
	offered := func(b *browser) bool {
		t.Helper()
		var offer struct {
			Offered bool `json:"offered"`
		}
		if err := json.Unmarshal(b.expect(ctx, http.StatusOK, http.MethodGet, "/create-tenant", nil), &offer); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		return offer.Offered
	}

	// The person the sessions act as, unmasked: every action is theirs to run, and so
	// is a tenant of their own to make.
	alice := newBrowser(t, server)
	alice.login(ctx, "alice")
	logged.take()
	for _, action := range slices.Concat(credential, access, []accesstypes.Resource{wireFunds, "EndStaffUserSessions"}) {
		alice.expect(ctx, http.StatusNoContent, http.MethodPost, "/run/"+string(action), nil)
	}
	if !offered(alice) {
		t.Error("alice's own session is not offered a tenant of her own to make")
	}
	alice.expect(ctx, http.StatusCreated, http.MethodPost, "/create-tenant", nil)
	if got, want := made(alice), []string{"alice"}; !slices.Equal(got, want) {
		t.Errorf("the tenants made after alice made hers = %v, want %v", got, want)
	}
	if got := logged.take(); strings.Contains(got, "denied by mask") {
		t.Errorf("alice's own session logged a mask denial:\n%s", got)
	}

	// A request for more than the grant gives, and one from a person who does not hold
	// the grant, start nothing: no session, no record.
	refused := newBrowser(t, server)
	refused.login(ctx, "support")
	refusals := []struct {
		name       string
		ask        maskAsk
		wantStatus int
		says       string
	}{
		{name: "a view that asks for Execute", ask: maskAsk{Kind: "view", Principal: "alice", Mask: []accesstypes.Permission{accesstypes.List, accesstypes.Read, accesstypes.Execute}}, wantStatus: http.StatusForbidden, says: "gives reading only, and the request asks for Execute as well"},
		{name: "a kind whose grant support does not hold", ask: maskAsk{Kind: "act", Principal: "alice"}, wantStatus: http.StatusForbidden, says: "takes Execute on ActAsUser, which you do not hold"},
		{name: "a mask that refuses a class no action carries", ask: maskAsk{Kind: "view", Principal: "alice", RefuseClasses: []accesstypes.ActionClass{"nonesuch"}}, wantStatus: http.StatusBadRequest, says: "nonesuch"},
	}
	for i := range refusals {
		tt := &refusals[i]
		status, body := refused.do(ctx, http.MethodPost, "/impersonate", tt.ask)
		if status != tt.wantStatus || !strings.Contains(string(body), tt.says) {
			t.Errorf("%s: status %d, want %d saying %q: %s", tt.name, status, tt.wantStatus, tt.says, body)
		}
		if _, username, impersonated := refused.whoami(ctx); username != "support" || impersonated {
			t.Errorf("%s: the session after the refusal is %q, impersonated %t; want support's own", tt.name, username, impersonated)
		}
	}
	if entries := listed(refused); len(entries) != 0 {
		t.Errorf("the live impersonations after the refused requests = %v, want none: a refused request starts nothing", entries)
	}

	sessions := []struct {
		name  string
		actor string
		ask   maskAsk
		// wantColumn is the record's Mask column, wantWords the mask as a person would
		// say it, and readOnly says the read-only guard answers the session's writes.
		wantColumn string
		wantWords  string
		readOnly   bool
		// refused are the actions the mask refuses, each with what the log says of it;
		// kept are actions it leaves to policy.
		refused map[accesstypes.Resource]string
		kept    []accesstypes.Resource
		// creation is what the log says when the session is refused the making of a
		// tenant, which no grant admits and the mask alone decides; empty for a session
		// whose mask leaves it, which then makes one for the person it acts as.
		creation string
	}{
		{
			name:       "the grant's own mask when the request names none: reading only",
			actor:      "support",
			ask:        maskAsk{Kind: "view", Principal: "alice"},
			wantColumn: "List,Read",
			wantWords:  "reading only",
			readOnly:   true,
			refused:    refusedBy(slices.Concat(credential, access, []accesstypes.Resource{wireFunds, "EndStaffUserSessions"}), "Execute denied by mask (List,Read) for alice on [%s]"),
			creation:   "Execute denied by mask (List,Read) for alice on [CreateTenant]",
		},
		{
			name:       "a mask without Execute, narrower than the grant's",
			actor:      "stand-in",
			ask:        maskAsk{Kind: "act", Principal: "alice", Mask: []accesstypes.Permission{accesstypes.List, accesstypes.Read, accesstypes.Update}},
			wantColumn: "List,Read,Update;-class:credential",
			wantWords:  "List, Read and Update only, except credential actions",
			refused:    refusedBy(slices.Concat(credential, access, []accesstypes.Resource{wireFunds, "EndStaffUserSessions"}), "Execute denied by mask (List,Read,Update;-class:credential) for alice on [%s]"),
			creation:   "Execute denied by mask (List,Read,Update;-class:credential) for alice on [CreateTenant]",
		},
		{
			name:       "the grant's own mask: everything except the credential actions",
			actor:      "stand-in",
			ask:        maskAsk{Kind: "act", Principal: "alice"},
			wantColumn: "*;-class:credential",
			wantWords:  "everything except credential actions",
			refused:    refusedBy(credential, "Execute denied by mask (*;-class:credential) for alice on %s: the mask refuses the class credential"),
			kept:       slices.Concat(access, []accesstypes.Resource{wireFunds, "EndStaffUserSessions"}),
		},
		{
			name:       "a class of the application's own and one named action, joined to what the grant refuses",
			actor:      "stand-in",
			ask:        maskAsk{Kind: "act", Principal: "alice", RefuseClasses: []accesstypes.ActionClass{"money_movement"}, RefuseActions: []accesstypes.Resource{"EndStaffUserSessions"}},
			wantColumn: "*;-class:credential;-class:money_movement;-on:Execute:EndStaffUserSessions",
			wantWords:  "everything except credential actions, money_movement actions and the action EndStaffUserSessions",
			refused: map[accesstypes.Resource]string{
				"SendStaffUserPasswordResetLink": "Execute denied by mask (*;-class:credential;-class:money_movement;-on:Execute:EndStaffUserSessions) for alice on SendStaffUserPasswordResetLink: the mask refuses the class credential",
				wireFunds:                        "on WireFunds: the mask refuses the class money_movement",
				"EndStaffUserSessions":           "on EndStaffUserSessions: the mask refuses Execute on EndStaffUserSessions",
			},
		},
		{
			name:       "one named action under a grant that gives everything: that action alone",
			actor:      "owner",
			ask:        maskAsk{Kind: "full", Principal: "alice", RefuseActions: []accesstypes.Resource{"SetStaffUserEmail"}},
			wantColumn: "*;-on:Execute:SetStaffUserEmail",
			wantWords:  "everything except the action SetStaffUserEmail",
			refused:    map[accesstypes.Resource]string{"SetStaffUserEmail": "on SetStaffUserEmail: the mask refuses Execute on SetStaffUserEmail"},
			kept:       []accesstypes.Resource{"SendStaffUserPasswordResetLink", "RenameStaffUser", wireFunds, "EndStaffUserSessions"},
		},
		{
			name:       "the class access under a grant that gives everything: the sign-in settings and the making of a tenant",
			actor:      "owner",
			ask:        maskAsk{Kind: "full", Principal: "alice", RefuseClasses: []accesstypes.ActionClass{accesstypes.ClassAccess}},
			wantColumn: "*;-class:access",
			wantWords:  "everything except access actions",
			refused:    refusedBy(access, "Execute denied by mask (*;-class:access) for alice on %s: the mask refuses the class access"),
			kept:       slices.Concat(credential, []accesstypes.Resource{wireFunds, "EndStaffUserSessions"}),
			creation:   "Execute denied by mask (*;-class:access) for alice on CreateTenant: the mask refuses the class access",
		},
		{
			name:       "the making of a tenant alone, refused by its action's name",
			actor:      "owner",
			ask:        maskAsk{Kind: "full", Principal: "alice", RefuseActions: []accesstypes.Resource{createTenant}},
			wantColumn: "*;-on:Execute:CreateTenant",
			wantWords:  "everything except the action CreateTenant",
			kept:       slices.Concat(access, []accesstypes.Resource{wireFunds}),
			creation:   "Execute denied by mask (*;-on:Execute:CreateTenant) for alice on CreateTenant: the mask refuses Execute on CreateTenant",
		},
	}
	for i := range sessions {
		tt := &sessions[i]
		b, sessionID, words := start(tt.actor, tt.ask)
		if words != tt.wantWords {
			t.Errorf("%s: the mask in words = %q, want %q", tt.name, words, tt.wantWords)
		}
		if got := column(sessionID); got == nil || *got != tt.wantColumn {
			t.Errorf("%s: the record's Mask column = %v, want %q", tt.name, deref(got), tt.wantColumn)
		}
		// The list reads the mask back from the record, refusals and all.
		if got := listed(b)[sessionID]; got.Mask != tt.wantColumn || got.Words != tt.wantWords {
			t.Errorf("%s: the list's entry = %+v, want the mask %q, in words %q", tt.name, got, tt.wantColumn, tt.wantWords)
		}

		logged.take()
		for action, wantLogged := range tt.refused {
			// The masked check, behind a safe method.
			b.expect(ctx, http.StatusForbidden, http.MethodGet, "/check/"+string(action), nil)
			if got := logged.take(); !strings.Contains(got, wantLogged) {
				t.Errorf("%s: the check of %s logged %q, want it to carry %q: the denial is the mask's", tt.name, action, got, wantLogged)
			}
			// The action as served: a read-only session's write is stopped by the
			// read-only guard, which records the blocked write; any other by the check.
			status, body := b.do(ctx, http.MethodPost, "/run/"+string(action), nil)
			if status != http.StatusForbidden || tt.readOnly != strings.Contains(string(body), "this session is read-only") {
				t.Errorf("%s: POST /run/%s = %d %s, want 403 (from the read-only guard: %t)", tt.name, action, status, body, tt.readOnly)
			}
			want := wantLogged
			if tt.readOnly {
				want = "impersonation: POST /run/" + string(action) + " refused for"
			}
			if got := logged.take(); !strings.Contains(got, want) {
				t.Errorf("%s: POST /run/%s logged %q, want it to carry %q", tt.name, action, got, want)
			}
		}
		for _, action := range tt.kept {
			b.expect(ctx, http.StatusNoContent, http.MethodPost, "/run/"+string(action), nil)
		}
		if got := logged.take(); strings.Contains(got, "denied by mask") {
			t.Errorf("%s: an action the mask keeps logged a mask denial:\n%s", tt.name, got)
		}

		// The making of a tenant, which no grant admits: the mask alone decides it. A
		// session it leaves the act makes a tenant for the person it acts as; one it
		// refuses is told so, with the denial logged as the mask's and nothing made, and
		// is not offered the act.
		before := made(b)
		logged.take()
		status, body := b.do(ctx, http.MethodPost, "/create-tenant", nil)
		if tt.creation == "" {
			if status != http.StatusCreated {
				t.Errorf("%s: POST /create-tenant = %d %s, want 201: the mask leaves the act", tt.name, status, body)
			}
			if got := logged.take(); strings.Contains(got, "denied by mask") {
				t.Errorf("%s: a creation the mask leaves logged a mask denial:\n%s", tt.name, got)
			}
			if got, want := made(b), slices.Concat(before, []string{"alice"}); !slices.Equal(got, want) {
				t.Errorf("%s: the tenants made = %v, want %v: the tenant is made for the person acted as", tt.name, got, want)
			}
			if !offered(b) {
				t.Errorf("%s: the session is not offered the making of a tenant, which its mask leaves it", tt.name)
			}

			continue
		}
		says, want := "you are acting as someone else, and this session cannot create a tenant", tt.creation
		if tt.readOnly {
			says, want = "this session is read-only", "impersonation: POST /create-tenant refused for"
		}
		if status != http.StatusForbidden || !strings.Contains(string(body), says) {
			t.Errorf("%s: POST /create-tenant = %d %s, want 403 saying %q", tt.name, status, body, says)
		}
		if got := logged.take(); !strings.Contains(got, want) {
			t.Errorf("%s: POST /create-tenant logged %q, want it to carry %q: the denial is the mask's", tt.name, got, want)
		}
		if got := made(b); !slices.Equal(got, before) {
			t.Errorf("%s: the tenants made = %v, want %v: a refused creation makes nothing", tt.name, got, before)
		}
		if offered(b) {
			t.Errorf("%s: the session is offered the making of a tenant, which its mask refuses", tt.name)
		}
	}

	// A server-side caller: the administration application signed alice in and hands
	// her over to view as bob. The portal's declaration gives the kind's mask as it does
	// for a person signed in here, its own check answers for the calling system, and the
	// record names the actor with the system that signed her in. A hand-off the system
	// may not make, one that asks for more than the kind gives, and one that names no
	// actor start nothing.
	handOffs := []struct {
		name       string
		body       maskHandOff
		wantStatus int
		says       string
	}{
		{name: "a kind the calling system may not start", body: maskHandOff{System: "admin-portal", Actor: "alice@example.com", maskAsk: maskAsk{Kind: "act", Principal: "bob"}}, wantStatus: http.StatusForbidden, says: "the application's check of the kind's grant, ActAsUser, refused it"},
		{name: "a system the portal does not know", body: maskHandOff{System: "nobody", Actor: "alice@example.com", maskAsk: maskAsk{Kind: "view", Principal: "bob"}}, wantStatus: http.StatusForbidden, says: "the application's check of the kind's grant, ViewAsUser, refused it"},
		{name: "more than the kind gives", body: maskHandOff{System: "admin-portal", Actor: "alice@example.com", maskAsk: maskAsk{Kind: "view", Principal: "bob", Mask: []accesstypes.Permission{accesstypes.List, accesstypes.Read, accesstypes.Execute}}}, wantStatus: http.StatusForbidden, says: "gives reading only, and the request asks for Execute as well"},
		{name: "no actor named", body: maskHandOff{System: "admin-portal", maskAsk: maskAsk{Kind: "view", Principal: "bob"}}, wantStatus: http.StatusBadRequest, says: "name the actor"},
	}
	portal := newBrowser(t, server)
	for i := range handOffs {
		tt := &handOffs[i]
		status, body := portal.do(ctx, http.MethodPost, "/handoff", tt.body)
		if status != tt.wantStatus || !strings.Contains(string(body), tt.says) {
			t.Errorf("a hand-off with %s: status %d, want %d saying %q: %s", tt.name, status, tt.wantStatus, tt.says, body)
		}
		if authenticated, _, _ := portal.whoami(ctx); authenticated {
			t.Errorf("a hand-off with %s left a session behind: a refused request starts nothing", tt.name)
		}
	}
	var handed struct {
		SessionID string `json:"sessionId"`
		Mask      string `json:"mask"`
	}
	if err := json.Unmarshal(portal.expect(ctx, http.StatusOK, http.MethodPost, "/handoff", maskHandOff{System: "admin-portal", Actor: "alice@example.com", maskAsk: maskAsk{Kind: "view", Principal: "bob"}}), &handed); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got := column(handed.SessionID); got == nil || *got != "List,Read" || handed.Mask != "reading only" {
		t.Errorf("the handed-over session: the record's Mask column = %v, in words %q; want the kind's own, List,Read and reading only", deref(got), handed.Mask)
	}
	if _, username, impersonated := portal.whoami(ctx); username != "bob" || !impersonated {
		t.Errorf("the handed-over session is %q, impersonated %t; want bob's, impersonated", username, impersonated)
	}
	if got := listed(portal)[handed.SessionID]; got.Actor != "alice@example.com" || got.ActorRealm != "admin-portal" || got.Mask != "List,Read" {
		t.Errorf("the handed-over session's record = %+v, want the actor alice@example.com of admin-portal under the mask List,Read", got)
	}
	logged.take()
	portal.expect(ctx, http.StatusForbidden, http.MethodGet, "/check/"+string(wireFunds), nil)
	if got, want := logged.take(), "Execute denied by mask (List,Read) for bob on [WireFunds]"; !strings.Contains(got, want) {
		t.Errorf("the handed-over session's check of WireFunds logged %q, want it to carry %q: the kind's mask holds a session a server-side caller starts", got, want)
	}

	// A request starts one session. The portal hands alice over once more, through a
	// handler that starts from its one request three times: the first start is her
	// session, and the second, and one from a copy of the request, are each refused in
	// plain words with nothing written: one more live impersonated session than before,
	// the first start's, and no other.
	liveBefore := listed(portal)
	again := newBrowser(t, server)
	var once struct {
		SessionID string   `json:"sessionId"`
		Again     []string `json:"again"`
	}
	if err := json.Unmarshal(again.expect(ctx, http.StatusOK, http.MethodPost, "/handoff/twice", maskHandOff{System: "admin-portal", Actor: "alice@example.com", maskAsk: maskAsk{Kind: "view", Principal: "carol"}}), &once); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, username, impersonated := again.whoami(ctx); username != "carol" || !impersonated {
		t.Errorf("the session the request's first start made is %q, impersonated %t; want carol's, impersonated", username, impersonated)
	}
	if len(once.Again) != 2 {
		t.Fatalf("the later starts from the one request were told %q, want an answer for each of the two", once.Again)
	}
	for i, told := range once.Again {
		for _, want := range []string{`the impersonation request (kind "view", alice@example.com acting as user:carol) has been used to start a session already`, "a request starts one session", "Nothing was written"} {
			if !strings.Contains(told, want) {
				t.Errorf("later start %d from the one request was told %q, want it to carry %q", i+1, told, want)
			}
		}
	}
	liveAfter := listed(again)
	if _, started := liveAfter[once.SessionID]; !started || len(liveAfter) != len(liveBefore)+1 {
		t.Errorf("the live impersonations went from %d to %d (the first start's session among them: %t), want one more, the first start's: a refused start writes nothing", len(liveBefore), len(liveAfter), started)
	}

	// A kind whose declaration gives everything keeps no mask: the column is NULL, and
	// the session holds all the person holds, the credential actions among it.
	owner, sessionID, words := start("owner", maskAsk{Kind: "full", Principal: "alice"})
	if got := column(sessionID); got != nil || words != "everything" {
		t.Errorf("an unmasked kind: the record's Mask column = %v, in words %q; want NULL and everything", deref(got), words)
	}
	for _, action := range slices.Concat(credential, access) {
		owner.expect(ctx, http.StatusNoContent, http.MethodPost, "/run/"+string(action), nil)
	}
	// To let someone act as a person with no mask is to give them what the person
	// holds: the making of a tenant too, made for the person acted as.
	before := made(owner)
	logged.take()
	owner.expect(ctx, http.StatusCreated, http.MethodPost, "/create-tenant", nil)
	if got, want := made(owner), slices.Concat(before, []string{"alice"}); !slices.Equal(got, want) {
		t.Errorf("an unmasked kind: the tenants made = %v, want %v", got, want)
	}
	if got := logged.take(); strings.Contains(got, "denied by mask") || !offered(owner) {
		t.Errorf("an unmasked kind: the making of a tenant is not offered, or logged a mask denial:\n%s", got)
	}
}

// refusedBy maps each action to what the log says of its refusal, from a format that
// takes the action's name.
func refusedBy(actions []accesstypes.Resource, format string) map[accesstypes.Resource]string {
	refused := make(map[accesstypes.Resource]string, len(actions))
	for _, action := range actions {
		refused[action] = strings.Replace(format, "%s", string(action), 1)
	}

	return refused
}

// deref renders a nullable column for a failure message.
func deref(col *string) string {
	if col == nil {
		return "NULL"
	}

	return *col
}
