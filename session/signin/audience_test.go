package signin

import (
	"slices"
	"testing"
)

// TestAudienceWords pins the words a sign-in method's width is said in: the
// organizations a limited method names, or the kind of account an open one admits.
func TestAudienceWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		audience    Audience
		want        string
		wantLimited bool
	}{
		{name: "one organization", audience: Audience{Organizations: []string{"acme.example"}}, want: "anyone with an account at acme.example", wantLimited: true},
		{name: "two organizations", audience: Audience{Organizations: []string{"acme.example", "initech.example"}}, want: "anyone with an account at acme.example or initech.example", wantLimited: true},
		{name: "three organizations", audience: Audience{Organizations: []string{"a.example", "b.example", "c.example"}}, want: "anyone with an account at a.example, b.example or c.example", wantLimited: true},
		{name: "any account of the provider", audience: Audience{Accounts: "a Google account"}, want: "anyone with a Google account"},
		{name: "organizations beside an open entry are as wide as the open entry", audience: Audience{Organizations: []string{"acme.example"}, Accounts: "a Google Workspace account"}, want: "anyone with a Google Workspace account"},
		{name: "a method that says nothing about itself", audience: Audience{}, want: "anyone who can sign in through it"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.audience.Words(); got != tt.want {
				t.Errorf("Words() = %q, want %q", got, tt.want)
			}
			if got := tt.audience.Limited(); got != tt.wantLimited {
				t.Errorf("Limited() = %v, want %v", got, tt.wantLimited)
			}
		})
	}
}

// TestAudienceKey pins what two audiences are compared by: the identities of whom each
// admits, whatever order they were listed in and whatever they are called. A name is
// the application's to give, and a second organization can carry the first one's, so
// names decide nothing where identities are given: two organizations under one name
// are two, and one organization under two names is one. A method that gives no
// identities is compared by the names it gives.
func TestAudienceKey(t *testing.T) {
	t.Parallel()

	const lake, river = "tenant:1111", "tenant:2222"

	tests := []struct {
		name string
		a, b Audience
		same bool
	}{
		{name: "a second organization under the name of the first", a: Audience{Organizations: []string{"Lakeside"}, Identities: []string{lake}}, b: Audience{Organizations: []string{"Lakeside", "Lakeside"}, Identities: []string{lake, river}}},
		{name: "another organization in the first one's place, under its name", a: Audience{Organizations: []string{"Lakeside"}, Identities: []string{lake}}, b: Audience{Organizations: []string{"Lakeside"}, Identities: []string{river}}},
		{name: "the same organization under another name", a: Audience{Organizations: []string{"Lakeside"}, Identities: []string{lake}}, b: Audience{Organizations: []string{"Lakeside University"}, Identities: []string{lake}}, same: true},
		{name: "the same organizations in another order", a: Audience{Identities: []string{lake, river}}, b: Audience{Identities: []string{river, lake}}, same: true},
		{name: "an organization listed twice", a: Audience{Identities: []string{lake, lake}}, b: Audience{Identities: []string{lake}}, same: true},
		{name: "one organization more", a: Audience{Identities: []string{lake}}, b: Audience{Identities: []string{lake, river}}},
		{name: "one organization fewer", a: Audience{Identities: []string{lake, river}}, b: Audience{Identities: []string{river}}},
		{name: "a kind of account admitted beside the organization", a: Audience{Identities: []string{lake}}, b: Audience{Identities: []string{lake, "any-account"}}},
		{name: "the words for the accounts changed and the identity not", a: Audience{Accounts: "a Google account", Identities: []string{"any-account"}}, b: Audience{Accounts: "any Google account", Identities: []string{"any-account"}}, same: true},
		{name: "an identity that holds the separators of the key", a: Audience{Identities: []string{`a","b`}}, b: Audience{Identities: []string{"a", "b"}}},

		// A method that gives no identities is compared by the names it gives.
		{name: "no identities: the same names in another order", a: Audience{Organizations: []string{"acme.example", "lakeside.edu"}}, b: Audience{Organizations: []string{"lakeside.edu", "acme.example"}}, same: true},
		{name: "no identities: no organizations, nil and empty", a: Audience{Accounts: "a Google account"}, b: Audience{Organizations: []string{}, Accounts: "a Google account"}, same: true},
		{name: "no identities: one name more", a: Audience{Organizations: []string{"acme.example"}}, b: Audience{Organizations: []string{"acme.example", "lakeside.edu"}}},
		{name: "no identities: named organizations widened to any account", a: Audience{Organizations: []string{"acme.example"}}, b: Audience{Accounts: "a Google account"}},
		{name: "no identities: accounts admitted beside the organization", a: Audience{Organizations: []string{"Acme"}}, b: Audience{Organizations: []string{"Acme"}, Accounts: "a Microsoft personal account"}},
		{name: "no identities: another kind of account", a: Audience{Accounts: "a Google account"}, b: Audience{Accounts: "an email address"}},
		{name: "no identities: an organization and a kind of account of one name are two things", a: Audience{Organizations: []string{"partners"}}, b: Audience{Accounts: "partners"}},
		{name: "a name is never an identity of the same spelling", a: Audience{Organizations: []string{"lakeside.edu"}}, b: Audience{Identities: []string{"lakeside.edu"}}},
		{name: "a method that says nothing about itself, twice", a: Audience{}, b: Audience{Organizations: []string{}}, same: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.a.Key() == tt.b.Key(); got != tt.same {
				t.Errorf("Key() of %+v and of %+v equal = %v, want %v (%s, %s)", tt.a, tt.b, got, tt.same, tt.a.Key(), tt.b.Key())
			}
		})
	}
}

// TestIdentitiesOfKey pins what reads as a key: the text Key writes, which reads back as
// the identities it was made of, each once and in order, and nothing else. A row that
// kept its audience in the earlier form, the names of its organizations and the words
// for its accounts, is no key: it equals none, so it opens nothing, whatever audience
// it was written for.
func TestIdentitiesOfKey(t *testing.T) {
	t.Parallel()

	for _, audience := range []Audience{
		{Organizations: []string{"Lakeside", "Riverside"}, Identities: []string{"tenant:2222", "tenant:1111", "tenant:2222"}},
		{Accounts: "a Google account", Identities: []string{"any-account"}},
		{Organizations: []string{"acme.example", "lakeside.edu"}},
		{Organizations: []string{"Acme"}, Accounts: "a Microsoft personal account"},
		{},
	} {
		key := audience.Key()
		got, ok := IdentitiesOfKey(key)
		if !ok || (Audience{Identities: got}).Key() != key {
			t.Errorf("IdentitiesOfKey(%s) = %q, %v; want the identities the key was made of", key, got, ok)
		}
	}
	if got, ok := IdentitiesOfKey(Audience{Identities: []string{"b", "a", "b"}}.Key()); !ok || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("IdentitiesOfKey() of the key of b, a, b = %q, %v; want a and b, each once and in order", got, ok)
	}

	// The earlier form of the key, by names, for the audiences the library's own methods
	// had, and other text that is no key.
	earlier := []string{
		`{"organizations":[],"accounts":"an email address"}`,
		`{"organizations":["lakeside.edu"],"accounts":""}`,
		`{"organizations":["the Microsoft Entra tenant 11111111-1111-1111-1111-111111111111"],"accounts":""}`,
		`{"organizations":[],"accounts":""}`,
	}
	other := []string{"", "open", `{"admits":["b","a"]}`, `{"admits":["a","a"]}`, `{"admits":null}`, `{"admits":["a"],"accounts":"x"}`, `["acme.example"]`, `{"admits":"a"}`}
	for _, text := range append(earlier, other...) {
		if got, ok := IdentitiesOfKey(text); ok {
			t.Errorf("IdentitiesOfKey(%q) = %q, true; want no audience for text that is no key", text, got)
		}
	}
	// No audience's key is the earlier form of any audience's.
	for _, audience := range []Audience{{}, {Accounts: "an email address"}, {Organizations: []string{"lakeside.edu"}}, {Identities: []string{"x"}}} {
		if slices.Contains(earlier, audience.Key()) {
			t.Errorf("Key() of %+v = %s, which is a key of the earlier form", audience, audience.Key())
		}
	}
}
