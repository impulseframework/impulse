package access

import (
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/internal/words"
)

// groupNotFoundMessage is what a person is told when the group they act on is not there.
const groupNotFoundMessage = "This group does not exist."

// roleInWords names a role as a sentence a person reads names it, "Night shift lead" for
// NightShiftLead, never by its code name, which a refusal's cause keeps for the server's
// log. The rule is the library's one (words.RoleInWords).
func roleInWords(role accesstypes.Role) string {
	return words.RoleInWords(string(role))
}

// placeInWords says where a role is held as a sentence a person reads says it: "for the
// whole application", "in every tenant" or "in a tenant", never by its scope and never by
// the tenant's id, which a refusal's cause keeps for the server's log.
func placeInWords(scope accesstypes.PolicyScope) string {
	switch {
	case scope.IsGlobal():
		return "for the whole application"
	case scope.IsEveryDomain():
		return "in every tenant"
	default:
		return "in a tenant"
	}
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
