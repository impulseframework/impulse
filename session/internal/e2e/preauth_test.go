package e2e

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session"
	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/sessionstorage"
)

// The custom session data table of an application on a Preauth, applied over the base
// migrations alone.
const (
	postgresPreauthTables = "file://testdata/preauth/postgresql"
	spannerPreauthTables  = "file://testdata/preauth/spanner"
)

// TestPreauthSeams_CustomSessionData runs a Preauth with custom session data on each
// backend, over the base migrations and the application's own table: the store takes
// the configuration typed to its transaction, the session type starts on it, and a
// session's row is the one Login was given, then the one an update wrote, or the
// resolver's when Login was given none.
func TestPreauthSeams_CustomSessionData(t *testing.T) {
	t.Parallel()

	t.Run(postgresBackend.name, func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		database := preparePostgres(ctx, t, postgresSessions, postgresPreauthTables)
		runPreauthCustomSessionData(ctx, t, sessionstorage.NewPostgresPreauth(database.Pool))
	})
	t.Run(spannerBackend.name, func(t *testing.T) {
		t.Parallel()
		ctx := t.Context()

		database := prepareSpanner(ctx, t, spannerSessions, spannerPreauthTables)
		runPreauthCustomSessionData(ctx, t, sessionstorage.NewSpannerPreauth(database.Client))
	})
}

// preauthLogin is the body of the Preauth application's POST /login: the name the
// caller vouches for and, when the caller knows it, the partner the session works for.
type preauthLogin struct {
	Username string `json:"username"`
	Partner  string `json:"partner"`
}

// resolvedPartner is the partner the application's resolver gives a session whose Login
// was given no row.
func resolvedPartner(username string) string {
	return "resolved for " + username
}

// preauthSessionData is the application's custom session data resolver. It is asked
// for a session started with no row, here a Login's, and told why the session starts
// and under what name; the request names no user, since a Preauth keeps none.
func preauthSessionData[Tx any](_ context.Context, _ Tx, req *sessioninfo.NewSessionRequest) (*appSessionData, error) {
	if req.Reason != sessioninfo.ReasonPreauth || req.UserID != impulse.NilUUID || req.Identity != nil {
		return nil, errors.Newf("the resolver was asked with reason %q, user %s and identity %v; want %q and neither", req.Reason, req.UserID, req.Identity, sessioninfo.ReasonPreauth)
	}

	return &appSessionData{Partner: resolvedPartner(req.Username)}, nil
}

func runPreauthCustomSessionData[Tx any](ctx context.Context, t *testing.T, store *sessionstorage.Preauth[Tx]) {
	t.Helper()

	sessionData, err := sessionstorage.NewCustomSessionData("SessionCustomData", preauthSessionData[Tx])
	if err != nil {
		t.Fatalf("sessionstorage.NewCustomSessionData() error = %v", err)
	}
	auth, err := session.NewPreauth[appSessionData](store.WithCustomSessionData(sessionData), cookieKey)
	if err != nil {
		t.Fatalf("session.NewPreauth() error = %v", err)
	}
	api := auth.API()

	r := chi.NewRouter()
	r.Use(auth.StartSession)
	r.Post("/login", func(w http.ResponseWriter, r *http.Request) {
		var body preauthLogin
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}
		var given []*appSessionData
		if body.Partner != "" {
			given = append(given, &appSessionData{Partner: body.Partner})
		}
		if _, err := api.Login(r.Context(), w, body.Username, given...); err != nil {
			_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	r.Group(func(r chi.Router) {
		r.Use(auth.ValidateSession, auth.ValidateXSRFToken)

		r.Get("/session-data", func(w http.ResponseWriter, r *http.Request) {
			data, err := api.CustomData(r.Context())
			if err != nil {
				_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

				return
			}
			_ = httpio.NewEncoder(w).Ok(data)
		})
		r.Post("/partner", func(w http.ResponseWriter, r *http.Request) {
			var body appSessionData
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)

				return
			}
			err := api.UpdateCustomSessionData(r.Context(), sessioninfo.IDFromRequest(r), func(data *appSessionData) error {
				data.Partner = body.Partner

				return nil
			})
			if err != nil {
				_ = httpio.NewEncoder(w).ClientMessage(r.Context(), err)

				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	})

	// TLS, because the auth cookies are Secure and a jar only sends them over https.
	server := httptest.NewTLSServer(r)
	t.Cleanup(server.Close)

	// The row Login is given is the session's, and the resolver is not asked.
	alice := newBrowser(t, server)
	alice.expect(ctx, http.StatusNoContent, http.MethodPost, "/login", preauthLogin{Username: "alice", Partner: "north"})
	if got := alice.sessionData(ctx).Partner; got != "north" {
		t.Errorf("the session's partner after a Login given %q = %q", "north", got)
	}

	// An update writes the row, and the next request reads it.
	alice.expect(ctx, http.StatusNoContent, http.MethodPost, "/partner", appSessionData{Partner: "south"})
	if got := alice.sessionData(ctx).Partner; got != "south" {
		t.Errorf("the session's partner after an update to %q = %q", "south", got)
	}

	// A Login given no row takes the resolver's, and each session keeps its own.
	bob := newBrowser(t, server)
	bob.expect(ctx, http.StatusNoContent, http.MethodPost, "/login", preauthLogin{Username: "bob"})
	if got, want := bob.sessionData(ctx).Partner, resolvedPartner("bob"); got != want {
		t.Errorf("the session's partner after a Login given none = %q, want the resolver's %q", got, want)
	}
	if got := alice.sessionData(ctx).Partner; got != "south" {
		t.Errorf("the first session's partner after another's Login = %q, want %q still", got, "south")
	}
}
