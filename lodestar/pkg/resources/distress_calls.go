package resources

import (
	"encoding/json"

	"github.com/impulseframework/impulse"
)

// Position is where a distress call was made, as a GeoJSON Point. Its shape lives
// outside Go, in the GeoJSON specification and the geojson TypeScript package, so the
// type declares its TypeScript type once, here: both clients' resources files import
// Point from geojson, and the column is typed Point. The type is its declaration and
// its annotation alone: a defined type inherits none of json.RawMessage's methods, so
// the generator writes the JSON pair that keeps the point the JSON it was received as
// (zz_gen_json.go) beside the Spanner methods that store it (zz_gen_storage.go).
//
// Demonstrates: typescript.imported-type, typescript.generated-json-methods.
//
// @typescript(Point, from: "geojson")
type Position json.RawMessage

type (
	// DistressCall is an incoming call before it becomes a mission, and the
	// field-direction showcase: CallerContact is PII (rejected in URL filters, flagged in
	// TS metadata) and nullable, since the Cadet's partial-width Create grant (summary,
	// severity) files calls without it, so the column must accept the narrowed create;
	// Transcript is input_only (accepted on mutations, never serialized back); CaseNumber
	// is output_only with a server-issued DC- default; FiledByUserID is output_only, stamped
	// from the session, and the attribute the portal's filedBy = subject grant reads. It
	// holds the id of the filer's account, and FiledByName beside it the filer's username
	// as it was when the call was filed, which is what a page shows.
	//
	// Served on the portal outlet too: Client Cleo files calls through a three-field form,
	// the one PII field an external user writes. Position is where the call was made, a
	// GeoJSON Point whose TypeScript type the Position type declares.
	//
	// A call's delete takes the relays passed along about it (@deleteCascade), which name
	// the call by its case number and not by its key, and the answers to those relays.
	//
	// Demonstrates: pii, input_only, output_only, default_create_fn, create-form-narrowing, outlet.shared, typescript.imported-type, @deleteCascade.
	//
	// @resource
	// @permissionScope(domain)
	// @deleteCascade(DistressRelay)
	// @outlet(default, portal)
	// @order(Severity desc)
	// @page(default: 10, max: 100)
	DistressCall struct {
		ID impulse.UUID `spanner:"Id"`
		// @domain
		SectorID      impulse.UUID `spanner:"SectorId"`
		Summary       string       `spanner:"Summary"`
		Severity      int64        `spanner:"Severity"`
		CallerContact *string      `spanner:"CallerContact" conditions:"pii"`
		Transcript    *string      `spanner:"Transcript"    conditions:"input_only"`
		CaseNumber    string       `spanner:"CaseNumber"    conditions:"output_only" default_create_fn:"defaultCaseNumber"`
		// @attribute(filedBy)
		FiledByUserID string `spanner:"FiledByUserId" conditions:"output_only" default_create_fn:"currentUser"`
		FiledByName   string `spanner:"FiledByName"   conditions:"output_only" default_create_fn:"currentUserName"`
		// Position is nullable: a call relayed by voice may name no point.
		Position *Position `spanner:"Position"`
	}
)
