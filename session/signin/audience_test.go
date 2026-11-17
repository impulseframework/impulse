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
