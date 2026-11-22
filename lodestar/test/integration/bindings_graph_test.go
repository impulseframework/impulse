// Demonstrates: bindings.dot, @attribute.join-path, @domain.join-path, @subjectSet.dotted-value, @subjectSet.who, @subjectSet.where, @subjectSet.on-set, @subjectSet.on-groups.
package integration

// bindings_graph_test reads the committed bindings graph and asserts every binding the
// resources declare is drawn exactly once: each join path hop by hop in its kind's style,
// each bare binding as a line in its resource's box, each bare binding on a foreign key
// pointing at its table, the requester entering each anchor once, each part a subject set
// writes listed under the set, a set that follows another as an edge from the
// resource declaring that other set, and the path a set's domain: names to its rows'
// tenant, so a name cannot vanish from the review surface or
// draw twice silently. The generator's own test pins the
// file format; this one pins Lodestar's facts.

import (
	"os"
	"strings"
	"testing"
)

func TestBindingsGraphDrawsEveryBindingOnce(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../pkg/resources/zz_gen_bindings.dot")
	if err != nil {
		t.Fatalf("reading the bindings graph: %v", err)
	}
	dot := string(raw)

	const reference = `color=gray55, fontcolor=gray35, arrowhead=vee, arrowsize=0.6, `

	tests := []struct {
		name string
		line string
	}{
		// @attribute(via:) paths, solid.
		{name: "MissionDocument client lands on Missions.ClientId", line: `"MissionDocument" -> "Mission" [label="client: MissionId ⇒ ClientId"];`},
		{name: "Ship hangarZone lands on Hangars.Zone", line: `"Ship" -> "Hangar" [label="hangarZone: HangarId ⇒ Zone"];`},
		{name: "Ship shipRole lands on ShipClasses.RoleId", line: `"Ship" -> "ShipClass" [label="shipRole: ClassId ⇒ RoleId"];`},
		// The synthesized state paths: one hop for a direct member, two for the chain,
		// the shared hop into the root drawn once.
		{name: "RefitTask state lands on Refits.StatusId", line: `"RefitTask" -> "Refit" [label="state: Id ⇒ StatusId"];`},
		{name: "Sortie state lands on Missions.StatusId", line: `"Sortie" -> "Mission" [label="state: MissionId ⇒ StatusId"];`},
		{name: "SortieExpense state continues through Sorties", line: `"SortieExpense" -> "Sortie" [label="state: SortieId"];`},
		// @domain(via:) paths, dotted: the three-hop RefitTask path shares Refit's and
		// Ship's hops, each drawn once.
		{name: "RefitTask domain continues through Refits", line: `"RefitTask" -> "Refit" [style=dotted, label="domain: Id"];`},
		{name: "Refit domain continues through Ships", line: `"Refit" -> "Ship" [style=dotted, label="domain: ShipId"];`},
		{name: "Ship domain lands on Hangars.SectorId", line: `"Ship" -> "Hangar" [style=dotted, label="domain: HangarId ⇒ SectorId"];`},
		{name: "MissionDocument domain lands on Missions.SectorId", line: `"MissionDocument" -> "Mission" [style=dotted, label="domain: MissionId ⇒ SectorId"];`},
		{name: "Sortie domain lands on Missions.SectorId", line: `"Sortie" -> "Mission" [style=dotted, label="domain: MissionId ⇒ SectorId"];`},
		{name: "SortieExpense domain continues through Sorties", line: `"SortieExpense" -> "Sortie" [style=dotted, label="domain: SortieId"];`},
		{name: "Squadron domain lands on Wings.SectorId", line: `"Squadron" -> "Wing" [style=dotted, label="domain: WingId ⇒ SectorId"];`},
		{name: "SquadronMembership domain continues through Squadrons", line: `"SquadronMembership" -> "Squadron" [style=dotted, label="domain: SquadronId"];`},
		// The requester enters each anchor once, naming every set and value it yields.
		{name: "subject enters SquadronMembership for squadrons and wings", line: `"subject" -> "SquadronMembership" [style=dashed, label="squadrons, wings: UserId"];`},
		{name: "subject enters Pilot for clearance and feeLimit", line: `"subject" -> "Pilot" [style=dashed, label="clearance, feeLimit: UserId"];`},
		{name: "subject enters PilotCertification for certifications", line: `"subject" -> "PilotCertification" [style=dashed, label="certifications: UserId"];`},
		{name: "subject enters ClientContact for client", line: `"subject" -> "ClientContact" [style=dashed, label="client: UserId"];`},
		{name: "subject enters HangarPass for the two pass sets, at the column their who: compares to the requester", line: `"subject" -> "HangarPass" [style=dashed, label="hangarsOpen, hangarsRun: UserId"];`},
		// A set that follows another set: an edge from the resource declaring the set
		// followed, labeled with the test, one for each set that writes it.
		{name: "hangarsOpen follows subject.squadrons", line: `"SquadronMembership" -> "HangarPass" [style=dashed, label="hangarsOpen: SquadronId IN subject.squadrons"];`},
		{name: "hangarsRun follows subject.squadrons", line: `"SquadronMembership" -> "HangarPass" [style=dashed, label="hangarsRun: SquadronId IN subject.squadrons"];`},
		// The same for the set the library's group members table declares: the pass
		// sets follow a person through the crew's groups.
		{name: "hangarsOpen follows subject.crewGroups", line: `"CrewGroupMember" -> "HangarPass" [style=dashed, label="hangarsOpen: GroupId IN subject.crewGroups"];`},
		{name: "hangarsRun follows subject.crewGroups", line: `"CrewGroupMember" -> "HangarPass" [style=dashed, label="hangarsRun: GroupId IN subject.crewGroups"];`},
		// The crewGroups set says where a row's sector is found (domain: GroupID.DomainID):
		// the path is drawn dotted, as an @domain path is, under the set's name.
		{name: "the crewGroups set's domain lands on the groups table's DomainId", line: `"CrewGroupMember" -> "CrewGroup" [style=dotted, label="crewGroups domain: GroupId ⇒ DomainId"];`},
		// Each set's parts, listed under the set in the columns' names.
		{name: "HangarPass lists hangarsOpen with its who: and its where:", line: `<tr><td align="left">subject.hangarsOpen: HangarId</td></tr><tr><td align="left">subject.hangarsOpen who: UserId = subject OR SquadronId IN subject.squadrons OR GroupId IN subject.crewGroups</td></tr><tr><td align="left">subject.hangarsOpen where: LevelId IN (&#39;visit&#39;, &#39;run&#39;) AND (ExpiresAt IS NULL OR ExpiresAt &gt; now)</td></tr>`},
		{name: "HangarPass lists hangarsRun with its who: and its where:", line: `<tr><td align="left">subject.hangarsRun: HangarId</td></tr><tr><td align="left">subject.hangarsRun who: UserId = subject OR SquadronId IN subject.squadrons OR GroupId IN subject.crewGroups</td></tr><tr><td align="left">subject.hangarsRun where: LevelId = &#39;run&#39; AND (ExpiresAt IS NULL OR ExpiresAt &gt; now)</td></tr>`},
		// The dotted subject value, dashed.
		{name: "wings lands on Squadrons.WingId", line: `"SquadronMembership" -> "Squadron" [style=dashed, label="wings: SquadronId ⇒ WingId"];`},
		// Bare bindings listed in their resource's box.
		{name: "Mission lists assignedSquadron", line: `<tr><td align="left">assignedSquadron: AssignedSquadronId</td></tr>`},
		{name: "Mission lists its bare state binding last", line: `<tr><td align="left">settlement: Settlement</td></tr><tr><td align="left">state: StatusId</td></tr></table>`},
		{name: "Refit lists its bare state binding last", line: `<tr><td align="left">openedBy: OpenedByUserId</td></tr><tr><td align="left">state: StatusId</td></tr></table>`},
		{name: "Pilot lists both subject values", line: `<tr><td align="left">subject.clearance: Clearance</td></tr><tr><td align="left">subject.feeLimit: FeeLimit</td></tr>`},
		{name: "SquadronMembership lists the bare squadrons set", line: `<tr><td align="left">subject.squadrons: SquadronId</td></tr>`},
		{name: "Client lists insured and trusted", line: `<tr><td align="left">insured: Insured</td></tr><tr><td align="left">trusted: Trusted</td></tr>`},
		{name: "Hangar lists the hangar attribute, its own key", line: `<tr><td align="left"><b>Hangar</b></td></tr><tr><td align="left">hangar: Id</td></tr></table>`},
		// Bare bindings on foreign keys point at their tables: the two sides of
		// assignedSquadron IN subject.squadrons meet at Squadron, requiredCert IN
		// subject.certifications at Certifications, client = subject.client at Client.
		{name: "assignedSquadron points at Squadron", line: `"Mission" -> "Squadron" [` + reference + `label="assignedSquadron"];`},
		{name: "subject.squadrons points at Squadron", line: `"SquadronMembership" -> "Squadron" [` + reference + `label="subject.squadrons"];`},
		{name: "requiredCert points at Certifications", line: `"Mission" -> "Certifications" [` + reference + `label="requiredCert"];`},
		{name: "subject.certifications points at Certifications", line: `"PilotCertification" -> "Certifications" [` + reference + `label="subject.certifications"];`},
		{name: "client points at Client", line: `"Mission" -> "Client" [` + reference + `label="client"];`},
		{name: "subject.client points at Client", line: `"ClientContact" -> "Client" [` + reference + `label="subject.client"];`},
		{name: "kind points at MissionKinds", line: `"Mission" -> "MissionKinds" [` + reference + `label="kind"];`},
		{name: "wing points at Wing", line: `"Squadron" -> "Wing" [` + reference + `label="wing"];`},
		{name: "subject.hangarsOpen points at Hangar, where hangar IN subject.hangarsOpen meets it", line: `"HangarPass" -> "Hangar" [` + reference + `label="subject.hangarsOpen"];`},
		{name: "subject.hangarsRun points at Hangar", line: `"HangarPass" -> "Hangar" [` + reference + `label="subject.hangarsRun"];`},
		// Nodes: a table only landed on or pointed at is dashed; the requester is an ellipse.
		{name: "Wing is landed on", line: `"Wing" [style=dashed];`},
		{name: "Certifications is pointed at", line: `"Certifications" [style=dashed];`},
		{name: "the requester node", line: `"subject" [shape=ellipse];`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := strings.Count(dot, tt.line); got != 1 {
				t.Errorf("bindings graph carries %q %d times, want exactly once:\n%s", tt.line, got, dot)
			}
		})
	}

	// Not drawn: the state columns' references to their enum tables (the workflow
	// graphs cover the states), the bare tenant column (never a condition operand), and
	// any resource declaring no attribute or subject binding.
	for _, absent := range []string{`"MissionStatus`, `"RefitStatus`, `: SectorId</td>`, `"Sector"`, `"Hangar";`, `"Wing";`} {
		if strings.Contains(dot, absent) {
			t.Errorf("bindings graph draws %s, which the rulings leave out:\n%s", absent, dot)
		}
	}
}
