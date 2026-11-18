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

// TestSeedUserIDThrough pins the id a seed creates a user under who signs in through a
// directory: the id of the username within the auth and the registration the person
// comes through, the same on every call and for a username spelled in another case.
// Another registration, another auth and another username are each another id, the
// three names are kept apart, and no such id is the id SeedUserID derives, so an
// account seeded for no registration is never read as one seeded for a registration.
func TestSeedUserIDThrough(t *testing.T) {
	t.Parallel()

	type seed struct{ auth, registration, username string }
	tests := []struct {
		name     string
		a, b     seed
		wantSame bool
	}{
		{name: "the same three names give the same id on every call", a: seed{"members", "directory", "ada@lakeside.edu"}, b: seed{"members", "directory", "ada@lakeside.edu"}, wantSame: true},
		{name: "a username spelled in another case is the same name", a: seed{"members", "directory", "Ada@Lakeside.edu"}, b: seed{"members", "directory", "ada@lakeside.EDU"}, wantSame: true},
		{name: "another registration of the auth gives another id", a: seed{"members", "directory", "ada@lakeside.edu"}, b: seed{"members", "okta", "ada@lakeside.edu"}},
		{name: "a registration's name is not read without regard to case: it is the application's own constant", a: seed{"members", "directory", "ada"}, b: seed{"members", "Directory", "ada"}},
		{name: "another auth gives another id", a: seed{"members", "directory", "ada"}, b: seed{"staff", "directory", "ada"}},
		{name: "another username gives another id", a: seed{"members", "directory", "ada"}, b: seed{"members", "directory", "bob"}},
		{name: "the names are kept apart: the auth and the registration", a: seed{"ab", "c", "ada"}, b: seed{"a", "bc", "ada"}},
		{name: "the names are kept apart: the registration and the username", a: seed{"members", "ab", "c"}, b: seed{"members", "a", "bc"}},
		{name: "the registration and the username do not change places", a: seed{"members", "directory", "ada"}, b: seed{"members", "ada", "directory"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, other := SeedUserIDThrough(tt.a.auth, tt.a.registration, tt.a.username), SeedUserIDThrough(tt.b.auth, tt.b.registration, tt.b.username)
			if (got == other) != tt.wantSame {
				t.Errorf("SeedUserIDThrough(%+v) = %s and SeedUserIDThrough(%+v) = %s; want the same id: %v", tt.a, got, tt.b, other, tt.wantSame)
			}
			if got == impulse.NilUUID {
				t.Error("SeedUserIDThrough() = the nil id, which names nobody")
			}
		})
	}

	t.Run("no id seeded for a registration is the id of the same names seeded for none", func(t *testing.T) {
		t.Parallel()
		for _, registration := range []string{"directory", "", "ada"} {
			if SeedUserIDThrough("members", registration, "ada") == SeedUserID("members", "ada") {
				t.Errorf("SeedUserIDThrough(members, %q, ada) = SeedUserID(members, ada); want the two kinds of seed apart", registration)
			}
		}
		// A username cannot stand in for a registration and a username: the two
		// derivations do not share a namespace.
		if SeedUserIDThrough("members", "directory", "ada") == SeedUserID("members", "directory\x00ada") {
			t.Error("an id seeded for a registration equals the id of a username spelled to look like one")
		}
	})

	t.Run("the id is derived from the three names alone, so it is the same on every machine", func(t *testing.T) {
		t.Parallel()
		// A seed that ran under an earlier build wrote its roles under this id, and the
		// first sign-in is recognized by deriving it again, so the value itself is held.
		const want = "f53f9ef1-c828-55be-a206-cd510a062fc8"
		if got := SeedUserIDThrough("members", "directory", "ada@lakeside.edu").String(); got != want {
			t.Errorf(`SeedUserIDThrough("members", "directory", "ada@lakeside.edu") = %s, want %s`, got, want)
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
