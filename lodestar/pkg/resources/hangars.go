package resources

import "github.com/impulseframework/impulse"

type (
	// Hangar is where ships park. It is sector-scoped with the tenancy column on the row
	// itself (the bare @domain form), and it is excluded from the consolidated patch
	// handler, keeping a sector-scoped standalone PATCH surface in the demo.
	//
	// Zone is the attribute Ship reaches through a one-hop join path (hangarZone !=
	// 'quarantine').
	//
	// A hangar's ships go with it: a ship reaches its sector through its hangar, and every
	// link of a table's way to its sector carries the delete rule, so a sector that is
	// deleted takes them. A sortie and a droid's report name a ship from outside that way
	// and say nothing of a delete, so deleting a hangar whose ships have flown is refused
	// at commit; the refusal answers 409 naming Hangars, never the ships or the sorties.
	//
	// Name carries the prefix fact alone: a stored LOWER(Name) column sits after SectorId
	// in HangarsBySectorIdNameLower, so `name:startswith:anvil` seeks
	// that index with the sector bound, case-folded, and the field answers nothing else;
	// Hangars has no search index, so no box.
	//
	// The hangar attribute is the hangar itself, its key: the Armorer's grants test it
	// against the two sets the hangar passes yield (hangar IN subject.hangarsOpen, hangar
	// IN subject.hangarsRun; HangarPass).
	//
	// Demonstrates: @domain, consolidation.exclusion, commit.referential-refusal, search.prefix.
	//
	// @resource
	// @permissionScope(domain)
	// @order(Name asc)
	// @page(default: 25, max: 200)
	Hangar struct {
		// @attribute(hangar)
		ID impulse.UUID `spanner:"Id"`
		// @domain
		SectorID impulse.UUID `spanner:"SectorId"`
		Name     string       `spanner:"Name"`
		Zone     string       `spanner:"Zone"`
	}
)
