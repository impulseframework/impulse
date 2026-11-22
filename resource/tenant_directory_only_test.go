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

// Test_directoryOnlyNotes pins the words of the directory-only warning's notes on who
// keeps a way in: the tenant's own administrators who have a password, and the
// invitations not yet accepted that give a role that administers it, one and several.
// Where a note names fewer than it counts and at least one, it says the names are the
// ones the caller may list; where it names nobody or everyone, it says nothing more.
func Test_directoryOnlyNotes(t *testing.T) {
	t.Parallel()

	const (
		oneAdministrator = "1 administrator of this tenant has a password and can still enter without the directory. Take the role away first from anyone who should not keep that way in."
		administrators   = "3 administrators of this tenant have a password and can still enter without the directory. Take the role away first from anyone who should not keep that way in."
		oneInvitation    = "1 invitation that has not been accepted gives a role that administers this tenant. Whoever accepts it can choose a password and enter without the directory. Withdraw it first if it should not stand."
		invitations      = "3 invitations that have not been accepted give a role that administers this tenant. Whoever accepts one can choose a password and enter without the directory. Withdraw first any that should not stand."
	)
	tests := []struct {
		name string
		// sentence is the note's sentence function.
		sentence func(word string, counted, named int) string
		counted  int
		named    int
		want     string
	}{
		{name: "one administrator", sentence: directoryAdministratorsSentence, counted: 1, named: 1, want: oneAdministrator},
		{name: "one administrator, not named", sentence: directoryAdministratorsSentence, counted: 1, want: oneAdministrator},
		{name: "two administrators", sentence: directoryAdministratorsSentence, counted: 2, named: 2, want: "2 administrators of this tenant have a password and can still enter without the directory. Take the role away first from anyone who should not keep that way in."},
		{name: "several administrators, nobody named", sentence: directoryAdministratorsSentence, counted: 3, want: administrators},
		{name: "several administrators, all named", sentence: directoryAdministratorsSentence, counted: 3, named: 3, want: administrators},
		{name: "several administrators, one named", sentence: directoryAdministratorsSentence, counted: 3, named: 1, want: administrators + " The one of them you may list is named here."},
		{name: "several administrators, some named", sentence: directoryAdministratorsSentence, counted: 3, named: 2, want: administrators + " The 2 of them you may list are named here."},
		{name: "one invitation", sentence: directoryInvitationsSentence, counted: 1, named: 1, want: oneInvitation},
		{name: "one invitation, not named", sentence: directoryInvitationsSentence, counted: 1, want: oneInvitation},
		{name: "two invitations", sentence: directoryInvitationsSentence, counted: 2, named: 2, want: "2 invitations that have not been accepted give a role that administers this tenant. Whoever accepts one can choose a password and enter without the directory. Withdraw first any that should not stand."},
		{name: "several invitations, nobody named", sentence: directoryInvitationsSentence, counted: 3, want: invitations},
		{name: "several invitations, all named", sentence: directoryInvitationsSentence, counted: 3, named: 3, want: invitations},
		{name: "several invitations, one named", sentence: directoryInvitationsSentence, counted: 3, named: 1, want: invitations + " The one of them you may list is named here."},
		{name: "several invitations, some named", sentence: directoryInvitationsSentence, counted: 3, named: 2, want: invitations + " The 2 of them you may list are named here."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.sentence("tenant", tt.counted, tt.named); got != tt.want {
				t.Errorf("sentence() = %q, want %q", got, tt.want)
			}
		})
	}
}
