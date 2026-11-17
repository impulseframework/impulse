package signin

import "testing"

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

// TestAudience_OneOrganization pins the width the first link of a deployment's seeded
// account is held to: the method admits the people of exactly one organization and
// nobody else. Several organizations, accounts outside the ones it names, and a method
// that says nothing of itself are each wider. The organizations are counted as the
// registration listed them: two that carry one name are two, though the audience's key
// writes the name once.
func TestAudience_OneOrganization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		audience Audience
		want     bool
	}{
		{name: "one organization", audience: Audience{Organizations: []string{"lakeside.edu"}}, want: true},
		{name: "two organizations", audience: Audience{Organizations: []string{"lakeside.edu", "acme.example"}}},
		{name: "two organizations under one name are two", audience: Audience{Organizations: []string{"lakeside", "lakeside"}}},
		{name: "one organization and accounts outside it", audience: Audience{Organizations: []string{"lakeside.edu"}, Accounts: "a personal Microsoft account"}},
		{name: "any account", audience: Audience{Accounts: "a Google account"}},
		{name: "a method that says nothing of itself", audience: Audience{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.audience.OneOrganization(); got != tt.want {
				t.Errorf("OneOrganization() = %v, want %v", got, tt.want)
			}
		})
	}

	// The key writes each name once, so it cannot be what the width is read from.
	alike := Audience{Organizations: []string{"lakeside", "lakeside"}}
	if read, ok := AudienceOfKey(alike.Key()); !ok || len(read.Organizations) != 1 {
		t.Fatalf("AudienceOfKey(Key()) of two organizations under one name = %+v, %v; the case no longer shows why the width is not read from the key", read, ok)
	}
}

func TestAudienceKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b Audience
		same bool
	}{
		{name: "the same organizations in another order", a: Audience{Organizations: []string{"acme.example", "lakeside.edu"}}, b: Audience{Organizations: []string{"lakeside.edu", "acme.example"}}, same: true},
		{name: "an organization named twice", a: Audience{Organizations: []string{"acme.example", "acme.example"}}, b: Audience{Organizations: []string{"acme.example"}}, same: true},
		{name: "no organizations, nil and empty", a: Audience{Accounts: "a Google account"}, b: Audience{Organizations: []string{}, Accounts: "a Google account"}, same: true},
		{name: "one organization more", a: Audience{Organizations: []string{"acme.example"}}, b: Audience{Organizations: []string{"acme.example", "lakeside.edu"}}},
		{name: "one organization fewer", a: Audience{Organizations: []string{"acme.example", "lakeside.edu"}}, b: Audience{Organizations: []string{"lakeside.edu"}}},
		{name: "named organizations widened to any account", a: Audience{Organizations: []string{"acme.example"}}, b: Audience{Accounts: "a Google account"}},
		{name: "personal accounts admitted beside the organization", a: Audience{Organizations: []string{"Acme"}}, b: Audience{Organizations: []string{"Acme"}, Accounts: "a Microsoft personal account"}},
		{name: "another kind of account", a: Audience{Accounts: "a Google account"}, b: Audience{Accounts: "an email address"}},
		{name: "a name that holds the separators of the key", a: Audience{Organizations: []string{`a","b`}}, b: Audience{Organizations: []string{"a", "b"}}},
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

func TestAudienceOfKey(t *testing.T) {
	t.Parallel()

	for _, want := range []Audience{
		{Organizations: []string{"acme.example", "lakeside.edu"}},
		{Accounts: "a Google account"},
		{Organizations: []string{"Acme"}, Accounts: "a Microsoft personal account"},
		{},
	} {
		got, ok := AudienceOfKey(want.Key())
		if !ok || got.Words() != want.Words() || got.Key() != want.Key() {
			t.Errorf("AudienceOfKey(%s) = %+v, %v; want %+v", want.Key(), got, ok, want)
		}
	}
	for _, text := range []string{"", "open", `{"organizations":["b","a"],"accounts":""}`, `{"accounts":"x"}`, `["acme.example"]`} {
		if got, ok := AudienceOfKey(text); ok {
			t.Errorf("AudienceOfKey(%q) = %+v, true; want no audience for text that is no key", text, got)
		}
	}
}
