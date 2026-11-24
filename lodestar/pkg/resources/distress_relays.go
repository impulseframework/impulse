package resources

import (
	"time"

	"github.com/impulseframework/impulse"
)

type (
	// DistressRelay is a message passed along about a distress call, or an answer to one:
	// what the watch heard, who is turning toward it. It holds the two links a delete
	// follows that are not a foreign key to another table's key.
	//
	// CaseNumber names the call by the case number an operator reads out, a column of
	// DistressCalls that is not its key. A case number is unique within a sector, and the
	// foreign key is the relay's sector and case number to the call's, so a relay names a
	// call of its own sector and never another's. A field that holds another table's code
	// is typed in, never picked: a picker writes the key of the row picked. A call's
	// delete takes its relays (DistressCall's @deleteCascade), found by the sector and the
	// case number.
	//
	// AnswersRelayID names the relay this one answers, a row of this same table. The
	// foreign key is the relay's sector and that relay's id, so an answer names a relay of
	// its own sector and never another's. The relay lists its own kind in @deleteCascade,
	// so a relay's delete asks first and then takes its answers, and theirs, however deep;
	// and a call's delete follows the same link, so an answer goes with the call even
	// where it names another call's case number.
	//
	// A relay is known by its message (@name): the list of a call's relays shows the relay
	// an answer answers by its message, which the server reads beside the id, and sorts and
	// filters by it.
	//
	// RelayedByName and RelayedAt are the server's: the session's username as it was then
	// and the commit timestamp, written at create and never by the wire.
	//
	// Demonstrates: @deleteCascade.own-kind, @deleteCascade.other-columns.
	//
	// @resource
	// @permissionScope(domain)
	// @deleteCascade(DistressRelay)
	// @order(RelayedAt asc)
	// @page(default: 25, max: 200)
	DistressRelay struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		SectorID       impulse.UUID     `spanner:"SectorId"`
		CaseNumber     string           `spanner:"CaseNumber"     conditions:"immutable"`
		AnswersRelayID impulse.NullUUID `spanner:"AnswersRelayId" conditions:"immutable"`
		// @name
		Message       string    `spanner:"Message"`
		RelayedByName string    `spanner:"RelayedByName" conditions:"output_only" default_create_fn:"currentUserName"`
		RelayedAt     time.Time `spanner:"RelayedAt"     conditions:"output_only" default_create_fn:"resource.CommitTimestamp"`
	}
)
