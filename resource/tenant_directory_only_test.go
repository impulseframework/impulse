package resource

import (
	"testing"
)

// Test_directoryOnlySentence pins the words of what making a tenant directory only
// would do: who will be able to enter, and how many of the people seated there will
// not. A count nobody made is never read as nobody: where no reader read the people,
// the sentence gives no number and does not say that everyone has signed in through
// the directory. Where the warning lists fewer people than it counts, the sentence says
// the list is the ones the caller may list.
func Test_directoryOnlySentence(t *testing.T) {
	t.Parallel()

	const (
		only   = "Only people who sign in through acme.example will be able to enter this tenant."
		enters = " An administrator of this tenant who has a password can still sign in with it and a code sent to their email."
	)
	tests := []struct {
		name string
		// administratorsEnter says the tenant's administrators keep their way in.
		administratorsEnter bool
		counted             bool
		outsiders           int
		named               int
		want                string
	}{
		{name: "the administrators' way in is said before anyone is counted", administratorsEnter: true, counted: true, outsiders: 3, named: 3, want: only + enters + " 3 people seated here sign in another way and will lose access."},
		{name: "and where the switch keeps nobody out, an administrator who has a password and never came through the directory perhaps among them", administratorsEnter: true, counted: true, want: only + enters + " Nobody seated here will lose access."},
		{name: "and where nobody read the people", administratorsEnter: true, want: only + enters + " Anyone seated here who signs in another way will lose access."},
		{name: "nobody read the people: no number, and no word that nobody loses access", counted: false, want: only + " Anyone seated here who signs in another way will lose access."},
		{name: "the people were read and all of them came through the directory", counted: true, want: only + " Everyone seated here has signed in through it."},
		{name: "one person", counted: true, outsiders: 1, named: 1, want: only + " 1 person seated here signs in another way and will lose access."},
		{name: "several, all named", counted: true, outsiders: 3, named: 3, want: only + " 3 people seated here sign in another way and will lose access."},
		{name: "several, nobody named", counted: true, outsiders: 3, want: only + " 3 people seated here sign in another way and will lose access."},
		{name: "several, one named", counted: true, outsiders: 3, named: 1, want: only + " 3 people seated here sign in another way and will lose access. The one of them you may list is named here."},
		{name: "several, some named", counted: true, outsiders: 3, named: 2, want: only + " 3 people seated here sign in another way and will lose access. The 2 of them you may list are named here."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := directoryOnlySentence("acme.example", "tenant", tt.administratorsEnter, tt.counted, tt.outsiders, tt.named); got != tt.want {
				t.Errorf("directoryOnlySentence() = %q, want %q", got, tt.want)
			}
		})
	}
}
