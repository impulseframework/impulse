package access

import (
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/words"
)

// groupNotFoundMessage is what a person is told when the group they act on is not there.
const groupNotFoundMessage = "This group does not exist."

// roleInWords names a role as a role picker shows it: its words in lower case with the
// first in upper case, so "NightShiftLead" is "Night shift lead". A sentence a person
// reads names a role this way, never by its code name, which the refusal's cause keeps
// for the server's log. It is the identity package's rule, held to the same cases. A
// plural acronym keeps its letters: "KeyIDs" is "Key IDs".
func roleInWords(role accesstypes.Role) string {
	spelled := words.InWords(string(role))
	if spelled == "" {
		return spelled
	}

	return strings.ToUpper(spelled[:1]) + spelled[1:]
}

// roleNotFound is the answer for a role that does not exist where it is asked for: 404
// with "The role Night shift lead does not exist.", the role in words (roleInWords). The
// role as it was asked for stays in the cause, for the server's log.
func roleNotFound(role accesstypes.Role) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("role %q does not exist", role), "The role "+roleInWords(role)+" does not exist.")
}

// groupNotFound is the answer for a group that is not there: 404 with
// groupNotFoundMessage. The group's id stays in the cause, for the server's log.
func groupNotFound(group accesstypes.Group) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("group %q does not exist", group), groupNotFoundMessage)
}
