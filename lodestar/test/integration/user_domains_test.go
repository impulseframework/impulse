// Demonstrates: tenancy.user-domains, frame.place-switcher.
package integration

// Bootstrap parity for the user-domains endpoint — the place switcher's source: the
// sectors where each persona holds at least one grant, on the same foothold predicate
// as concealed tenancy. Cinder lights only for headquarters, the archivist, and the
// hazard analyst.

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
)

func TestUserDomains(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	tests := []struct {
		name string
		user accesstypes.User
		want []accesstypes.Domain
	}{
		{name: "the governor's three sectors list sorted", user: "governor", want: []accesstypes.Domain{anvil, bastion, cinder}},
		{name: "the archivist reads every sector", user: "archivist", want: []accesstypes.Domain{anvil, bastion, cinder}},
		{name: "the hazard analyst holds Anvil and Cinder", user: "hazards", want: []accesstypes.Domain{anvil, cinder}},
		{name: "the pilot holds Anvil and Bastion", user: "pilot", want: []accesstypes.Domain{anvil, bastion}},
		{name: "the marshal's single sector, global roles excluded", user: "marshal", want: []accesstypes.Domain{anvil}},
		// The client's roles are the directory's, reconciled at login; the membership is
		// held in every sector, so every sector lights; her rows are narrowed by company,
		// not by sector.
		{name: "the client's directory roles light every sector", user: "client", want: []accesstypes.Domain{anvil, bastion, cinder}},
		{name: "a session with no sector roles lists none, as an array", user: "visitor-nobody", want: []accesstypes.Domain{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, body := doRequestAs(t, h, tt.user, http.MethodGet, "/console/api/user-domains", "")
			assertStatus(t, status, http.StatusOK, body)

			var got []accesstypes.Domain
			if err := unmarshalDomains(body, &got); err != nil {
				t.Fatalf("unmarshaling domains %s: %v", body, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("domains = %v, want %v", got, tt.want)
			}
			if len(tt.want) == 0 && strings.TrimSpace(string(body)) != "[]" {
				t.Errorf("empty membership body = %q, want [] (never null)", body)
			}
		})
	}
}

// TestUserDomainsNames pins the name each sector is listed with (@tenantName on the
// sector record's Name), the label the place switcher shows for it.
func TestUserDomainsNames(t *testing.T) {
	t.Parallel()

	_, h, _ := sharedWorld(t)

	status, body := doRequestAs(t, h, "governor", http.MethodGet, "/console/api/user-domains", "")
	assertStatus(t, status, http.StatusOK, body)

	type listed struct {
		Domain string `json:"domain"`
		Name   string `json:"name"`
	}
	var got []listed
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshaling domains %s: %v", body, err)
	}
	want := []listed{{"anvil", "Anvil"}, {"bastion", "Bastion"}, {"cinder", "Cinder"}}
	if !slices.Equal(got, want) {
		t.Errorf("domains = %v, want %v", got, want)
	}
}

// unmarshalDomains reads the user-domains answer, each tenant listed with its domain and
// its name, into the tenants' domains alone, in the order listed.
func unmarshalDomains[T ~string](body []byte, domains *[]T) error {
	var listed []struct {
		Domain string `json:"domain"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		return err
	}
	out := make([]T, 0, len(listed))
	for _, entry := range listed {
		out = append(out, T(entry.Domain))
	}
	*domains = out

	return nil
}
