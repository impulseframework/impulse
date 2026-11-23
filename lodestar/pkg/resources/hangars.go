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
	// Name carries the prefix fact alone: a stored LOWER(Name) column sits after SectorId
	// in HangarsBySectorIdNameLower, so `name:startswith:anvil` seeks
	// that index with the sector bound, case-folded, and the field answers nothing else;
	// Hangars has no search index, so no box.
	//
	// The hangar attribute is the hangar itself, its key: the Armorer's grants test it
	// against the two sets the hangar passes yield (hangar IN subject.hangarsOpen, hangar
	// IN subject.hangarsRun; HangarPass).
	//
	// A hangar's delete is declared (@deleteCascade): its passes and its ships go with it,
	// with the ships' refits and their tasks, each through its own delete, and a sortie
	// flown or a droid report filed on one of its ships refuses it.
	//
	// Demonstrates: @domain, consolidation.exclusion, search.prefix.
	//
	// @resource
	// @permissionScope(domain)
	// @deleteCascade(HangarPass, Ship, Refit, RefitTask, refusedBy: Sortie, DroidReport)
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
