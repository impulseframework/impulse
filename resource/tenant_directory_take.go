package resource

import (
	"fmt"

	"github.com/impulseframework/impulse/session/signin"
)

// Taking a directory is attested. Nobody checks who may act for an organization, so a
// tenant takes its organization's directory on the word of the person who makes it the
// tenant's: before the act, they read a statement that opens with that word, then says
// what the act does, in the organization's own name as the directory gives it, and they
// type TakeDirectoryPhrase under it. The words are the server's (TakeDirectoryWarning),
// for a tenant that stands and for one created in the same act; the phrase is the
// browser's to hold, as every typed word is.

// TakeDirectoryPhrase is what a person types under the statement before a tenant takes
// their organization's directory (TakeDirectoryWarning).
const TakeDirectoryPhrase = "I am authorized"

// TakeDirectoryWarning is the statement a person reads before a tenant takes directory as
// its own: that they are authorized to act on behalf of the organization, a note on what
// taking it does, and the words for the button. creating says the tenant is created in
// the same act (the create-your-tenant route) rather than standing already. The
// organization is said by the name its registrations or the directory give it, and
// where none does by the words of the button a person signs in to it with
// (directorySaid), never by its connection. It reads nothing, writes nothing and judges
// nothing: the refusals of Set are the caller's to reach first.
func (r *TenantDirectoryRules) TakeDirectoryWarning(directory signin.Directory, creating bool) *Warning {
	tenant := "this " + r.word
	if creating {
		tenant = "the " + r.word + " you are creating"
	}
	name, byLabel := directorySaid(directory, r.namers)
	if name != "" {
		confirm := fmt.Sprintf("Make %s this %s's directory", name, r.word)
		if creating {
			confirm = fmt.Sprintf("Create the %s with %s as its directory", r.word, name)
		}

		return &Warning{
			Consequence: fmt.Sprintf("You are stating that you are authorized to act on behalf of %s.", name),
			Also: []WarningNote{{
				Says: fmt.Sprintf(
					"From now on, people at %s who sign in through it and have no account here are sent to %s. They get in by your invitation, or with a starting role if you open the %s to them. No other %s can use %s.",
					name, tenant, r.word, r.word, name,
				),
			}},
			Confirm: confirm,
		}
	}
	confirm := fmt.Sprintf("Make it this %s's directory", r.word)
	if creating {
		confirm = fmt.Sprintf("Create the %s with your organization's directory", r.word)
	}

	return &Warning{
		Consequence: "You are stating that you are authorized to act on behalf of your organization.",
		Also: []WarningNote{{
			Says: fmt.Sprintf(
				"From now on, people who sign in through %s and have no account here are sent to %s. They get in by your invitation, or with a starting role if you open the %s to them. No other %s can use that directory.",
				byLabel, tenant, r.word, r.word,
			),
		}},
		Confirm: confirm,
	}
}
