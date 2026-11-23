package session

import (
	"encoding/json"
	"testing"

	"github.com/impulseframework/impulse"
)

// TestSeedUserID pins the id a seed creates a user under: the id of the username within
// the auth, the same on every call, so the roles a seed wrote under it are the user's
// when the seed runs again. Another username or another auth is another person, and the
// two names are kept apart: no auth and username read together as another pair's.
func TestSeedUserID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// The two people compared, each an auth and a username.
		auth, username           string
		otherAuth, otherUsername string
		wantSame                 bool
	}{
		{name: "the same auth and username give the same id on every call", auth: "crew", username: "alice", otherAuth: "crew", otherUsername: "alice", wantSame: true},
		{name: "a username spelled in another case is the same name, as the users table holds it, and gives the same id", auth: "crew", username: "Alice@Lakeside.edu", otherAuth: "crew", otherUsername: "alice@lakeside.EDU", wantSame: true},
		{name: "a username with an @ in it and the Kelvin sign for its k is another address, as the users table holds it, and gives another id", auth: "crew", username: "\u212aim@lakeside.edu", otherAuth: "crew", otherUsername: "kim@lakeside.edu"},
		{name: "a username with no @ in it and the Kelvin sign for its k is the same name, as the users table holds it, and gives the same id", auth: "crew", username: "\u212aim", otherAuth: "crew", otherUsername: "kim", wantSame: true},
		{name: "another username in the auth gives another id", auth: "crew", username: "alice", otherAuth: "crew", otherUsername: "bob"},
		{name: "the username in another auth gives another id", auth: "crew", username: "alice", otherAuth: "members", otherUsername: "alice"},
		{name: "the auth and the username are kept apart: ab with c is not a with bc", auth: "ab", username: "c", otherAuth: "a", otherUsername: "bc"},
		{name: "the auth and the username do not change places", auth: "crew", username: "alice", otherAuth: "alice", otherUsername: "crew"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, other := SeedUserID(tt.auth, tt.username), SeedUserID(tt.otherAuth, tt.otherUsername)
			if (got == other) != tt.wantSame {
				t.Errorf("SeedUserID(%q, %q) = %s and SeedUserID(%q, %q) = %s; want the same id: %v", tt.auth, tt.username, got, tt.otherAuth, tt.otherUsername, other, tt.wantSame)
			}
			for _, id := range []impulse.UUID{got, other} {
				if id == impulse.NilUUID {
					t.Errorf("SeedUserID() = %s, want an id that is not the nil id, which names nobody", id)
				}
			}
		})
	}

	t.Run("the id is derived from the two names alone, so it is the same on every machine", func(t *testing.T) {
		t.Parallel()
		// A seed that ran under an earlier build wrote its roles under this id. A
		// derivation that changed would have the next run write a second set under
		// another one, so the value itself is held, as any version 5 implementation
		// derives it.
		const want = "3839e095-9da2-515b-8e8f-31a7b5acb342"
		if got := SeedUserID("crew", "alice").String(); got != want {
			t.Errorf(`SeedUserID("crew", "alice") = %s, want %s`, got, want)
		}
	})
}

// TestCreateUserRequest_IDIsNotReadFromABody pins that the id of a new user is a Go
// caller's to choose and never a request's. A seeded user's id is derived from its
// names (SeedUserID), so anyone can work it out: nobody else comes to hold it only
// while no request body can name one.
func TestCreateUserRequest_IDIsNotReadFromABody(t *testing.T) {
	t.Parallel()

	id := SeedUserID("crew", "alice").String()
	for _, key := range []string{"id", "ID", "Id", "-"} {
		t.Run("a body that names the id as "+key, func(t *testing.T) {
			t.Parallel()
			body := `{"` + key + `": "` + id + `", "username": "mallory"}`
			var req CreateUserRequest
			if err := json.Unmarshal([]byte(body), &req); err != nil {
				t.Fatalf("json.Unmarshal(%s) error = %v", body, err)
			}
			if req.ID.Valid || req.Username != "mallory" {
				t.Errorf("json.Unmarshal(%s) = %+v, want mallory with a null id", body, req)
			}
		})
	}
}
