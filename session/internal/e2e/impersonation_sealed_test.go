package e2e

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// writeKey stands where the session library's key went in the write an application
// declared while the store exported it. The application had the key's type by
// inference, without naming it; nothing has that type now.
type writeKey = struct{}

// wrappingStore is an application's session store that wraps the library's. It embeds
// the library's store, which is how an application adds to one, and declares the write
// beneath an impersonated session itself, twice: under the name the write had while it
// was an exported method of the store (the shape that was handed the library's key, the
// request and the record), and under the name the session library calls the write by
// now. Each counts its calls, puts another person in the request and the record as it
// could while it stood between the library and the database, and refuses.
type wrappingStore struct {
	sessionstorage.PreauthStore

	exported, unexported atomic.Int32
}

// CreateImpersonatedSession is the write as an application declared it while the store
// exported it.
func (s *wrappingStore) CreateImpersonatedSession(_ context.Context, _ writeKey, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error) {
	s.exported.Add(1)
	req.Username = "mallory"
	imp.Actor = "nobody"

	return impulse.NilUUID, errors.New("the wrapper's own CreateImpersonatedSession was called")
}

// createImpersonatedSession has the name the session library calls the write by, in
// this package: a method of this package's, and not the one the library calls.
func (s *wrappingStore) createImpersonatedSession(_ context.Context, req *sessioninfo.NewSessionRequest, imp *sessioninfo.Impersonation) (impulse.UUID, error) {
	s.unexported.Add(1)
	req.Username = "mallory"
	imp.Actor = "nobody"

	return impulse.NilUUID, errors.New("the wrapper's own createImpersonatedSession was called")
}

// TestSealedStorage_wrapperCannotReplaceTheWrite starts an impersonated session over an
// application's store that wraps the library's and declares the write beneath an
// impersonated session itself, on PostgreSQL and on Spanner alike. The wrapper is a
// session store, since the write of the store it embeds is promoted to it. Neither of
// its own methods is called: the session and its record are written by the library's
// store, as the declaration decided them, and the wrapper's methods see no request, no
// record and no key.
func TestSealedStorage_wrapperCannotReplaceTheWrite(t *testing.T) {
	t.Parallel()

	table, err := sessionstorage.NewImpersonationTable("SessionImpersonations")
	if err != nil {
		t.Fatalf("sessionstorage.NewImpersonationTable() error = %v", err)
	}

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		database := preparePostgres(ctx, t, postgresSessions, postgresImpersonation)
		runWrapperCannotReplaceTheWrite(ctx, t, sessionstorage.NewPostgresPreauth(database.Pool, sessionstorage.WithImpersonation(table)))
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		database := prepareSpanner(ctx, t, spannerSessions, spannerImpersonation)
		runWrapperCannotReplaceTheWrite(ctx, t, sessionstorage.NewSpannerPreauth(database.Client, sessionstorage.WithImpersonation(table)))
	})
}

// runWrapperCannotReplaceTheWrite is the scenario over one library store.
func runWrapperCannotReplaceTheWrite(ctx context.Context, t *testing.T, store sessionstorage.PreauthStore) {
	t.Helper()

	wrapper := &wrappingStore{PreauthStore: store}
	auth, err := session.NewPreauth[session.NoCustomData](wrapper, cookieKey)
	if err != nil {
		t.Fatalf("session.NewPreauth() over a store that wraps the library's error = %v", err)
	}

	declared := session.MustNewImpersonations(session.NamedActions(grantView),
		session.ImpersonatesUser(kindView, grantView, accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read)),
	)
	portal := session.ImpersonationActor{Name: "pat", Realm: "admin-portal"}
	req, err := declared.RequestFor(ctx, portal, &session.ImpersonationAsk{Kind: kindView, Principal: "bob"}, anyoneMay)
	if err != nil {
		t.Fatalf("RequestFor() error = %v", err)
	}
	id, err := auth.API().StartImpersonatedSession(ctx, httptest.NewRecorder(), req)
	if err != nil {
		t.Fatalf("StartImpersonatedSession() over a store that wraps the library's error = %v, want the session started: the wrapper has the library's write by promotion", err)
	}
	if exported, unexported := wrapper.exported.Load(), wrapper.unexported.Load(); exported != 0 || unexported != 0 {
		t.Errorf("the wrapper's own CreateImpersonatedSession was called %d times and its createImpersonatedSession %d times, want neither called: the write is the library's alone", exported, unexported)
	}

	// The row and its record, read back from the database by the library's store: the
	// session of the person the declaration's request named, started by the actor it
	// named, and nobody the wrapper put there.
	written, err := store.Session(ctx, id)
	if err != nil {
		t.Fatalf("Session() of the session started error = %v", err)
	}
	if written.Username != "bob" {
		t.Errorf("the session written is %q's, want bob's", written.Username)
	}
	if record := written.Impersonation; record == nil || record.Actor != portal.Name || record.ActorRealm != portal.Realm {
		t.Errorf("the record written = %+v, want one that names the actor %s of %s", record, portal.Name, portal.Realm)
	}

	// The wrapper's two methods are there, and they count. So no call counted above is
	// the library not calling them, and not methods that count nothing.
	if _, err := wrapper.CreateImpersonatedSession(ctx, writeKey{}, &sessioninfo.NewSessionRequest{}, &sessioninfo.Impersonation{}); err == nil {
		t.Errorf("the wrapper's own CreateImpersonatedSession, called by the test, answered no error")
	}
	if _, err := wrapper.createImpersonatedSession(ctx, &sessioninfo.NewSessionRequest{}, &sessioninfo.Impersonation{}); err == nil {
		t.Errorf("the wrapper's own createImpersonatedSession, called by the test, answered no error")
	}
	if exported, unexported := wrapper.exported.Load(), wrapper.unexported.Load(); exported != 1 || unexported != 1 {
		t.Errorf("the wrapper's own methods, each called once by the test, counted %d and %d calls, want 1 and 1", exported, unexported)
	}
}
