package spannerstore_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/db"
	"github.com/impulseframework/impulse/persona"
	"github.com/impulseframework/impulse/persona/spannerstore"
)

var container *db.SpannerContainer

func TestMain(m *testing.M) {
	ctx := context.Background()
	sc, err := db.NewSpannerContainer(ctx, "1.5.56")
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}
	container = sc

	code := m.Run()

	if err := sc.Close(); err != nil {
		fmt.Println(err)
	}
	os.Exit(code)
}

// newStore makes a database with the shipped migrations and returns the store over it
// with the client the test reads behind the store's back.
func newStore(ctx context.Context, t *testing.T) (*spannerstore.Store, *spanner.Client) {
	t.Helper()

	sum := sha256.Sum256([]byte(t.Name()))
	database, err := container.CreateDatabase(ctx, "t"+hex.EncodeToString(sum[:])[:24])
	if err != nil {
		t.Fatalf("SpannerContainer.CreateDatabase() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("SpannerDB.Close() error = %v", err)
		}
	})
	if err := database.MigrateUp("file://../schema/spanner/migrations"); err != nil {
		t.Fatalf("SpannerDB.MigrateUp() error = %v", err)
	}

	return spannerstore.New(database.Client), database.Client
}

// at is a timestamp as Spanner stores it: UTC, microseconds.
func at(t time.Time) time.Time {
	return t.UTC().Truncate(time.Microsecond)
}

func registration(clientID string, expires time.Time) *persona.Registration {
	return &persona.Registration{
		Deployment:          persona.Deployment{ClientID: clientID, Environment: "tst", PullRequest: 12},
		ApplicationClientID: "lanterns",
		RedirectURIs:        []string{"https://lanterns-pr12.example.com/api/user/persona/callback"},
		Cast: []persona.Persona{
			{Login: "cadet", Name: "Cadet Example", Email: "cadet@example.com", Roles: []string{"Cadet"}},
			{Login: "navigator", Name: "Navigator Example", Email: "navigator@example.com", Roles: []string{"Navigator", "Crew"}},
		},
		RegisteredBy: "deployer@example.iam.gserviceaccount.com",
		RegisteredAt: at(time.Now()),
		ExpiresAt:    at(expires),
	}
}

func TestStore_Registrations(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, _ := newStore(ctx, t)

	if _, err := store.Registration(ctx, "lanterns-pr12"); !errors.Is(err, persona.ErrUnknownRegistration) {
		t.Fatalf("Registration() before any put: error = %v, want ErrUnknownRegistration", err)
	}
	want := registration("lanterns-pr12", time.Now().Add(7*24*time.Hour))
	if err := store.PutRegistration(ctx, want); err != nil {
		t.Fatalf("PutRegistration() error = %v", err)
	}
	got, err := store.Registration(ctx, "lanterns-pr12")
	if err != nil {
		t.Fatalf("Registration() error = %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("registration mismatch (-want +got):\n%s", diff)
	}

	// A second put under the same client ID replaces the first: a redeploy's cast.
	want.Cast = want.Cast[:1]
	want.RegisteredAt = at(time.Now())
	if err := store.PutRegistration(ctx, want); err != nil {
		t.Fatalf("PutRegistration() again error = %v", err)
	}
	if got, err = store.Registration(ctx, "lanterns-pr12"); err != nil || len(got.Cast) != 1 {
		t.Errorf("Registration() after the replacement = %d personas, %v; want 1", len(got.Cast), err)
	}

	// The sweep removes the expired alone.
	if err := store.PutRegistration(ctx, registration("lanterns-pr13", time.Now().Add(-time.Hour))); err != nil {
		t.Fatalf("PutRegistration(expired) error = %v", err)
	}
	n, err := store.ExpireRegistrations(ctx, time.Now())
	if err != nil || n != 1 {
		t.Errorf("ExpireRegistrations() = %d, %v; want 1 removed", n, err)
	}
	if _, err := store.Registration(ctx, "lanterns-pr13"); !errors.Is(err, persona.ErrUnknownRegistration) {
		t.Errorf("Registration(expired) after the sweep: error = %v, want ErrUnknownRegistration", err)
	}
	if _, err := store.Registration(ctx, "lanterns-pr12"); err != nil {
		t.Errorf("Registration(live) after the sweep: error = %v, want it kept", err)
	}

	if err := store.DeleteRegistration(ctx, "lanterns-pr12"); err != nil {
		t.Fatalf("DeleteRegistration() error = %v", err)
	}
	if _, err := store.Registration(ctx, "lanterns-pr12"); !errors.Is(err, persona.ErrUnknownRegistration) {
		t.Errorf("Registration() after the delete: error = %v, want ErrUnknownRegistration", err)
	}
	if err := store.DeleteRegistration(ctx, "lanterns-pr12"); err != nil {
		t.Errorf("DeleteRegistration() of a removed registration error = %v, want none", err)
	}
}

func TestStore_Requests(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, _ := newStore(ctx, t)

	want := &persona.Request{
		ID: "11111111-1111-4111-8111-111111111111", ClientID: "lanterns-pr12", RedirectURI: "https://lanterns-pr12.example.com/cb", State: "s", Nonce: "n",
		Scopes: []string{"openid", "email", "profile"}, ResponseType: "code", ResponseMode: "", CodeChallenge: "challenge", CodeChallengeMethod: "S256",
		LoginHint: "cadet", CreatedAt: at(time.Now()),
	}
	if err := store.CreateRequest(ctx, want); err != nil {
		t.Fatalf("CreateRequest() error = %v", err)
	}
	got, err := store.Request(ctx, want.ID)
	if err != nil {
		t.Fatalf("Request() error = %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("request mismatch (-want +got):\n%s", diff)
	}
	if _, err := store.Request(ctx, "22222222-2222-4222-8222-222222222222"); !errors.Is(err, persona.ErrUnknownRequest) {
		t.Errorf("Request(unknown) error = %v, want ErrUnknownRequest", err)
	}
	if _, err := store.RequestByCode(ctx, "code-1"); !errors.Is(err, persona.ErrUnknownRequest) {
		t.Errorf("RequestByCode() before a code: error = %v, want ErrUnknownRequest", err)
	}

	authTime := at(time.Now())
	if err := store.CompleteRequest(ctx, want.ID, "cadet", "u-pat", authTime); err != nil {
		t.Fatalf("CompleteRequest() error = %v", err)
	}
	if err := store.SaveCode(ctx, want.ID, "code-1"); err != nil {
		t.Fatalf("SaveCode() error = %v", err)
	}
	got, err = store.RequestByCode(ctx, "code-1")
	if err != nil {
		t.Fatalf("RequestByCode() error = %v", err)
	}
	want.Subject, want.ActorID, want.Done, want.AuthTime, want.Code = "cadet", "u-pat", true, authTime, "code-1"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("completed request mismatch (-want +got):\n%s", diff)
	}
	if err := store.CompleteRequest(ctx, "22222222-2222-4222-8222-222222222222", "x", "y", authTime); !errors.Is(err, persona.ErrUnknownRequest) {
		t.Errorf("CompleteRequest(unknown) error = %v, want ErrUnknownRequest", err)
	}
	if err := store.SaveCode(ctx, "22222222-2222-4222-8222-222222222222", "code-2"); !errors.Is(err, persona.ErrUnknownRequest) {
		t.Errorf("SaveCode(unknown) error = %v, want ErrUnknownRequest", err)
	}

	if err := store.DeleteRequest(ctx, want.ID); err != nil {
		t.Fatalf("DeleteRequest() error = %v", err)
	}
	if _, err := store.Request(ctx, want.ID); !errors.Is(err, persona.ErrUnknownRequest) {
		t.Errorf("Request() after the delete: error = %v, want ErrUnknownRequest", err)
	}
	if _, err := store.RequestByCode(ctx, "code-1"); !errors.Is(err, persona.ErrUnknownRequest) {
		t.Errorf("RequestByCode() after the delete: error = %v, want ErrUnknownRequest", err)
	}

	old := *want
	old.ID, old.Code, old.CreatedAt = "33333333-3333-4333-8333-333333333333", "", at(time.Now().Add(-time.Hour))
	if err := store.CreateRequest(ctx, &old); err != nil {
		t.Fatalf("CreateRequest(old) error = %v", err)
	}
	if n, err := store.ExpireRequests(ctx, time.Now().Add(-10*time.Minute)); err != nil || n != 1 {
		t.Errorf("ExpireRequests() = %d, %v; want 1 removed", n, err)
	}
}

func TestStore_Tokens(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, _ := newStore(ctx, t)

	want := &persona.Token{ID: "44444444-4444-4444-8444-444444444444", RequestID: "r", ClientID: "lanterns-pr12", Subject: "cadet", Audience: []string{"lanterns-pr12"}, Scopes: []string{"openid"}, ExpiresAt: at(time.Now().Add(5 * time.Minute))}
	if err := store.CreateToken(ctx, want); err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}
	got, err := store.Token(ctx, want.ID)
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("token mismatch (-want +got):\n%s", diff)
	}
	if _, err := store.Token(ctx, "55555555-5555-4555-8555-555555555555"); !errors.Is(err, persona.ErrUnknownToken) {
		t.Errorf("Token(unknown) error = %v, want ErrUnknownToken", err)
	}
	if n, err := store.ExpireTokens(ctx, time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Errorf("ExpireTokens() = %d, %v; want 1 removed", n, err)
	}
}

func TestStore_SigningKeys(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, _ := newStore(ctx, t)

	if keys, err := store.SigningKeys(ctx); err != nil || len(keys) != 0 {
		t.Fatalf("SigningKeys() before any key = %d keys, %v; want none", len(keys), err)
	}
	first, second := newKey(t), newKey(t)
	for _, k := range []*persona.SigningKey{
		{ID: "66666666-6666-4666-8666-666666666666", Key: first, CreatedAt: at(time.Now().Add(-time.Hour))},
		{ID: "77777777-7777-4777-8777-777777777777", Key: second, CreatedAt: at(time.Now())},
	} {
		if err := store.AddSigningKey(ctx, k); err != nil {
			t.Fatalf("AddSigningKey(%s) error = %v", k.ID, err)
		}
	}
	keys, err := store.SigningKeys(ctx)
	if err != nil || len(keys) != 2 {
		t.Fatalf("SigningKeys() = %d keys, %v; want 2", len(keys), err)
	}
	if keys[0].ID != "77777777-7777-4777-8777-777777777777" || !keys[0].Key.Equal(second) || !keys[1].Key.Equal(first) {
		t.Errorf("SigningKeys() order or material wrong: first %s", keys[0].ID)
	}
}

func TestStore_RecordSignIn(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	store, client := newStore(ctx, t)

	entry := &persona.SignIn{
		ID:          "88888888-8888-4888-8888-888888888888",
		Deployment:  persona.Deployment{ClientID: "lanterns-pr12", Environment: "tst", PullRequest: 12},
		Application: persona.Application{ID: "app-1", Tenant: "lakeside", Name: "Lanterns", ClientID: "lanterns"},
		Persona:     "cadet",
		Actor:       persona.Actor{ID: "u-pat", Name: "Pat Example", Email: "pat@example.com"},
		At:          at(time.Now()),
	}
	if err := store.RecordSignIn(ctx, entry); err != nil {
		t.Fatalf("RecordSignIn() error = %v", err)
	}
	row, err := client.Single().ReadRow(ctx, "PersonaSignIns", spanner.Key{entry.ID}, []string{"ClientId", "ApplicationId", "Tenant", "Persona", "ActorId", "ActorEmail"})
	if err != nil {
		t.Fatalf("ReadRow(PersonaSignIns) error = %v", err)
	}
	var clientID, appID, tenant, who, actorID, actorEmail string
	if err := row.Columns(&clientID, &appID, &tenant, &who, &actorID, &actorEmail); err != nil {
		t.Fatalf("Row.Columns() error = %v", err)
	}
	if clientID != "lanterns-pr12" || appID != "app-1" || tenant != "lakeside" || who != "cadet" || actorID != "u-pat" || actorEmail != "pat@example.com" {
		t.Errorf("sign-in row = %s %s %s %s %s %s", clientID, appID, tenant, who, actorID, actorEmail)
	}
}

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() error = %v", err)
	}

	return key
}
