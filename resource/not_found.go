package resource

import (
	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

const (
	// recordNotFoundMessage is what a caller is told when the record a request names is
	// not there to read or to write: plain words, with nothing of how the server looked
	// for it.
	recordNotFoundMessage = "This record was not found."

	// referencedNotFoundMessage is what a caller is told when a record it writes names
	// another record that is not there for it.
	referencedNotFoundMessage = "A record this one refers to was not found."

	// nothingToChangeMessage is what a caller is told when a write would change nothing.
	nothingToChangeMessage = "There is nothing to change: the record already holds these values."
)

// recordNotFound is the answer for a record that is not there: 404 with
// recordNotFoundMessage. What the server looked for (the statement's WHERE clause with
// its values, or the row's key) stays the cause in the chain, so the server's log says
// it, and the caller is sent no query, no table's name and no column's.
func recordNotFound(res accesstypes.Resource, lookedFor string) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("%s (%s) not found", res, lookedFor), recordNotFoundMessage)
}

// referencedNotFound is the answer for a write whose record names another that is not
// there: 404 with referencedNotFoundMessage. The table the server looked in stays the
// cause in the chain, for the server's log.
func referencedNotFound(res accesstypes.Resource, table string) error {
	return httpio.NewNotFoundMessageWithError(errors.Newf("%s: referenced %s row not found", res, table), referencedNotFoundMessage)
}

// nothingToChange is the answer for a write that would change nothing: 400 with
// nothingToChangeMessage. The row the server read (its WHERE clause with its values)
// stays the cause in the chain, for the server's log.
func nothingToChange(res accesstypes.Resource, where string) error {
	return httpio.NewBadRequestMessageWithError(errors.Newf("no changes to apply for %s (%s)", res, where), nothingToChangeMessage)
}
