package resources

type (
	// MissionStatus enumerates the mission workflow's seven states; the generated
	// constants are the vocabulary the RPC bodies and computed resources use, so a state
	// is always spelled in a checked constant, never a string.
	//
	// Demonstrates: @enumerate.type, workflow.state-table.
	//
	// @enumerate(MissionStatuses)
	MissionStatus string

	// RefitStatus enumerates the refit workflow's six states, the hangar bays.
	//
	// @enumerate(RefitStatuses)
	RefitStatus string

	// MissionKind enumerates what a mission is: rescue, salvage, escort, courier.
	//
	// @enumerate(MissionKinds)
	MissionKind string

	// ShipRole enumerates a hull's role in the fleet.
	//
	// @enumerate(ShipRoles)
	ShipRole string

	// Certification enumerates the pilot certifications a mission may require.
	//
	// @enumerate(Certifications)
	Certification string

	// FailReason enumerates why a mission failed. FailMission.ReasonID names the table with
	// a field-scope @enumerate, so its picker renders these values from the generated
	// metadata with no request, and the body still validates the reason it is sent.
	//
	// @enumerate(FailReasons)
	FailReason string

	// PassLevel enumerates what a hangar pass allows: a visit, or the run of the hangar.
	// The two pass sets filter on it (HangarPass), and a where: that names a level the
	// table does not hold is refused at generation.
	//
	// @enumerate(PassLevels)
	PassLevel string
)
