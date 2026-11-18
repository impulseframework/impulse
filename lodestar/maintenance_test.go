package main

import (
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/resource/maintenance"
)

// TestMaintenancePageSaysWhoseItIs holds the maintenance page to the brands the two
// browser applications declare: a navigation under /portal gets the page under the
// portal's name and logo, and one under /console or at the root under the console's.
// The name it expects is read from the declaration (web/console/brand.json,
// web/portal/brand.json), so a name changed there without running impulse brand again
// fails here, and the logo is the one impulse brand wrote for that application.
//
// Demonstrates: webapp.brand.
func TestMaintenancePageSaysWhoseItIs(t *testing.T) {
	t.Parallel()

	handler, err := maintenance.Handler(maintenanceBrands())
	if err != nil {
		t.Fatalf("maintenance.Handler(): %v", err)
	}

	tests := []struct {
		name    string
		path    string
		project string
	}{
		{name: "the root, which leads to the console", path: "/", project: "console"},
		{name: "a page of the console", path: "/console/sectors", project: "console"},
		{name: "a page of the portal", path: "/portal/tracker", project: "portal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			declaration, err := os.ReadFile(filepath.Join("web", tt.project, "brand.json"))
			if err != nil {
				t.Fatalf("reading the declaration: %v", err)
			}
			var declared struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(declaration, &declared); err != nil {
				t.Fatalf("the declaration does not parse: %v", err)
			}
			if declared.Name == "" {
				t.Fatal("the declaration names no application")
			}
			logo, err := os.ReadFile(filepath.Join("pkg", "brand", tt.project+".png"))
			if err != nil {
				t.Fatalf("reading the logo impulse brand wrote: %v", err)
			}

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, http.NoBody)
			req.Header.Set("Sec-Fetch-Dest", "document")
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			req.Header.Set("Accept", "text/html")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
			}
			// html/template writes a few characters of the data URL as character references,
			// which the browser reads back; the page is compared as the browser reads it.
			page := html.UnescapeString(rec.Body.String())
			if want := "<title>" + declared.Name + " is down for maintenance</title>"; !strings.Contains(page, want) {
				t.Errorf("%s: the page does not carry %q", tt.path, want)
			}
			if want := `src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(logo) + `"`; !strings.Contains(page, want) {
				t.Errorf("%s: the page does not carry the logo of the %s", tt.path, tt.project)
			}
		})
	}
}
