//go:build !skipAuth

package google

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/impulseframework/impulse/session/azure"
	internalcookie "github.com/impulseframework/impulse/session/internal/cookie"
	"github.com/impulseframework/impulse/session/internal/oidctest"
	"github.com/impulseframework/impulse/session/signin"
)

// jar is a browser's cookie store: a cookie written later replaces one of the same name.
type jar map[string]*http.Cookie

func (j jar) store(rec *httptest.ResponseRecorder) {
	for _, c := range rec.Result().Cookies() {
		j[c.Name] = c
	}
}

func (j jar) request(t *testing.T, target string) *http.Request {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
	for _, c := range j {
		r.AddCookie(c)
	}

	return r
}

// TestOIDC_StateCookie_ProvidersCoexist proves an Azure and a Google login started in the
// same browser keep separate state: each callback completes, whichever started last. With
// one shared state cookie the later login overwrote the earlier one's state and PKCE
// verifier, and the earlier callback was refused.
func TestOIDC_StateCookie_ProvidersCoexist(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	cookies := oidctest.NewCookieClient(t)

	const entraTenant = "11111111-1111-1111-1111-111111111111"
	azureIDP := oidctest.NewFakeEntra(t, testClientID)
	azureIDP.TokenClaims = func() map[string]any {
		return map[string]any{"preferred_username": "user@example.com", "tid": entraTenant, "oid": "object-1"}
	}
	googleIDP := newFakeIDP(t)
	googleIDP.TokenClaims = workspaceClaims

	entra := azure.NewOIDC(cookies.ForRegistration("azure"), azureIDP.Server.URL, azure.Tenant(entraTenant), testClientID, "test-secret", "https://app.example.com/azure/callback")
	google := newWithIssuer(cookies.ForRegistration("google"), googleIDP.Server.URL, testClientID, "test-secret", "https://app.example.com/google/callback", Workspace("test", testHostedDomain))

	browser := jar{}
	start := func(authCodeURL func(rec *httptest.ResponseRecorder) (string, error)) string {
		rec := httptest.NewRecorder()
		raw, err := authCodeURL(rec)
		if err != nil {
			t.Fatalf("AuthCodeURL() error = %v", err)
		}
		browser.store(rec)
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("url.Parse() error = %v", err)
		}

		return u.Query().Get("state")
	}

	// Azure first, then Google: the Google login's state cookie is written last.
	azureState := start(func(rec *httptest.ResponseRecorder) (string, error) {
		return entra.AuthCodeURL(ctx, rec, "/azure", signin.LoginOptions{})
	})
	googleState := start(func(rec *httptest.ResponseRecorder) (string, error) {
		return google.AuthCodeURL(ctx, rec, "/google", signin.LoginOptions{})
	})

	if _, ok := browser[internalcookie.StateCookieName(cookies.CookieName, "azure")]; !ok {
		t.Fatalf("browser cookies = %v, want the Azure state cookie %q", browser, internalcookie.StateCookieName(cookies.CookieName, "azure"))
	}
	if _, ok := browser[internalcookie.StateCookieName(cookies.CookieName, "google")]; !ok {
		t.Fatalf("browser cookies = %v, want the Google state cookie %q", browser, internalcookie.StateCookieName(cookies.CookieName, "google"))
	}

	var azureClaims json.RawMessage
	returnURL, _, err := entra.Verify(ctx, httptest.NewRecorder(), browser.request(t, fmt.Sprintf("/azure/callback?code=c&state=%s", url.QueryEscape(azureState))), &azureClaims)
	if err != nil {
		t.Fatalf("Azure Verify() after a later Google login error = %v, want the Azure login to complete", err)
	}
	if returnURL != "/azure" {
		t.Errorf("Azure Verify() returnURL = %q, want %q", returnURL, "/azure")
	}

	var googleClaims json.RawMessage
	returnURL, _, err = google.Verify(ctx, httptest.NewRecorder(), browser.request(t, fmt.Sprintf("/google/callback?code=c&state=%s", url.QueryEscape(googleState))), &googleClaims)
	if err != nil {
		t.Fatalf("Google Verify() error = %v, want the Google login to complete", err)
	}
	if returnURL != "/google" {
		t.Errorf("Google Verify() returnURL = %q, want %q", returnURL, "/google")
	}
}
