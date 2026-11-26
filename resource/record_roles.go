// record_roles.go is what a collection says about the records a role can be held on
// (@holdsRoles): which resources are such records, what is under each by foreign keys and
// what each names, and where each auth keeps the roles held on them. The generator writes
// it as data; the permission engine reads the answers (RecordsHoldingRoles, RecordWords,
// RecordReach, RecordCondition) through the optional interface it asks a collection for.
//
// A role held on a record counts under a condition the collection writes: the row's way
// to the record, or the record's name of the row, is in a subject set of the records the
// person holds the role on. Those sets are declared nowhere: their names say what they
// are (SubjectSet reads them), and they read the auth's tables of roles held on records.

package resource

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/impulseframework/impulse/accesstypes"
)

// ReservedBindingPrefix opens every binding name the collection makes for the records a
// role can be held on: the attributes by which a row is under a record or named by one
// (RecordWayAttribute, RecordNameAttribute) and the subject sets of the records a person
// holds a role on. A name an application declares does not open with it.
const ReservedBindingPrefix = "held_"

// RecordWayAttribute names the attribute of a resource that resolves, by the resource's
// way n to the record (numbered from 0), to the key of the record the row is under.
func RecordWayAttribute(record accesstypes.Resource, n int) string {
	return ReservedBindingPrefix + "way" + strconv.Itoa(n) + "_" + string(record)
}

// RecordNameAttribute names the attribute of a resource that the record's name n of it
// (numbered from 0, RecordData.Names) refers to: the column the record's foreign key
// references.
func RecordNameAttribute(record accesstypes.Resource, n int) string {
	return ReservedBindingPrefix + "named" + strconv.Itoa(n) + "_" + string(record)
}

// The columns of an auth's tables of roles held on records that the subject sets of
// those records read (access's spannerstore and postgresstore).
const (
	recordDomainColumn = "DomainId"
	recordKeyColumn    = "RecordId"
	recordUserColumn   = "UserId"
	recordGroupColumn  = "GroupId"
	recordStartsColumn = "StartsAt"
	recordEndsColumn   = "EndsAt"
)

// recordPeriodWhere is the half of a record set's where: part that keeps a row inside
// its period alone, word for word the part of the set of a person's groups.
const recordPeriodWhere = "(StartsAt IS NULL OR StartsAt <= now) AND (EndsAt IS NULL OR EndsAt > now)"

// recordSetRole is what a record set's name holds between its auth and its record: a
// role's name.
var recordSetRole = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

// recordSetName names the subject set of the records of the kind record on which a
// person holds role, for the people of auth: by the person (held_by_), or by a group of
// theirs (held_bygroup_); and, with named n at 0 or more, the set of the rows those
// records name by their name n (held_named<n>by_, held_named<n>bygroup_).
func recordSetName(named int, group bool, auth string, role accesstypes.Role, record accesstypes.Resource) string {
	kind := "by"
	if named >= 0 {
		kind = "named" + strconv.Itoa(named) + kind
	}
	if group {
		kind += "group"
	}

	return ReservedBindingPrefix + kind + "_" + auth + "_" + string(role) + "_" + string(record)
}

// RecordData marks a resource as a record a role can be held on.
type RecordData struct {
	// Key is the key's one column.
	Key string

	// One is what a sentence calls one row, in lower case ("project"), and Many what it
	// calls many ("projects").
	One  string
	Many string

	// Names are the rows a record names, in the order of the record's columns' names.
	Names []RecordNameData
}

// clone is a copy of the record's data that shares no slice with it.
func (d *RecordData) clone() *RecordData {
	copied := *d
	copied.Names = slices.Clone(d.Names)

	return &copied
}

// RecordNameData is one foreign key of a record to a row it names.
type RecordNameData struct {
	// Resource is the resource named (Clients), and Column the record's column that
	// names it (ClientId).
	Resource accesstypes.Resource
	Column   string

	// Attribute is the attribute of Resource the test reads, its referenced column.
	Attribute string

	// One and Many are the named resource's words ("client", "clients").
	One  string
	Many string
}

// RecordWayData is one way a row of a resource is under a record.
type RecordWayData struct {
	// Record is the record the row is under (Projects).
	Record accesstypes.Resource

	// Attribute is the resource's attribute that resolves to the record's key by this way.
	Attribute string

	// Self marks the way of the record's own rows: the row is the record itself.
	Self bool
}

// RecordRoleStoreData is where one auth keeps the roles held on records.
type RecordRoleStoreData struct {
	// Auth is the auth, as its package names it.
	Auth string

	// UserRoles is the table of people's roles, and GroupRoles the table of groups' roles,
	// "" where the auth's management has no groups.
	UserRoles  string
	GroupRoles string

	// GroupSet is the subject set of a person's groups, "" with GroupRoles.
	GroupSet string

	// GroupMembers and Groups are the auth's tables of the groups' members and of the
	// groups, "" with GroupRoles: where no struct declares GroupSet, as in a site whose
	// program does not generate the auth's management, the collection reads the set from
	// them (storeGroupSet).
	GroupMembers string
	Groups       string
}

// setResourceHoldsRoles marks res within scope as a record a role can be held on, replacing
// an earlier mark; a nil record takes the mark away.
func (g *GeneratedCollection) setResourceHoldsRoles(scope accesstypes.PermissionScope, res accesstypes.Resource, record *RecordData) {
	if record == nil {
		delete(g.holdsRoles[scope], res)

		return
	}
	if g.holdsRoles[scope] == nil {
		g.holdsRoles[scope] = make(map[accesstypes.Resource]*RecordData)
	}
	g.holdsRoles[scope][res] = record.clone()
}

// setResourceUnder records the ways the rows of res within scope are under the records,
// replacing earlier ones; none takes them away.
func (g *GeneratedCollection) setResourceUnder(scope accesstypes.PermissionScope, res accesstypes.Resource, ways []RecordWayData) {
	if len(ways) == 0 {
		delete(g.under[scope], res)

		return
	}
	if g.under[scope] == nil {
		g.under[scope] = make(map[accesstypes.Resource][]RecordWayData)
	}
	g.under[scope][res] = sortedWays(ways)
}

// sortedWays is a copy of ways sorted by the record they lead to, each record's ways kept
// in the order given: a way's attribute is numbered by that order.
func sortedWays(ways []RecordWayData) []RecordWayData {
	sorted := slices.Clone(ways)
	slices.SortStableFunc(sorted, func(a, b RecordWayData) int {
		return cmp.Compare(a.Record, b.Record)
	})

	return sorted
}

// addRecordRoleStore records where an auth keeps the roles held on records, replacing an
// earlier store of the same auth, the stores kept sorted by auth.
func (g *GeneratedCollection) addRecordRoleStore(store *RecordRoleStoreData) {
	g.recordRoleStores = slices.DeleteFunc(g.recordRoleStores, func(kept RecordRoleStoreData) bool {
		return kept.Auth == store.Auth
	})
	g.recordRoleStores = append(g.recordRoleStores, *store)
	slices.SortFunc(g.recordRoleStores, func(a, b RecordRoleStoreData) int {
		return cmp.Compare(a.Auth, b.Auth)
	})
}

// heldRecord is the mark of the record res, whatever its scope; ok is false for a
// resource that is no record a role can be held on.
func (g *GeneratedCollection) heldRecord(res accesstypes.Resource) (*RecordData, bool) {
	for _, records := range g.holdsRoles {
		if record, ok := records[res]; ok {
			return record, true
		}
	}

	return nil, false
}

// RecordsHoldingRoles lists the resources marked as records a role can be held on
// (CollectionResource.HoldsRoles), sorted. A nil collection marks none.
func (g *GeneratedCollection) RecordsHoldingRoles() []accesstypes.Resource {
	if g == nil {
		return nil
	}
	var records []accesstypes.Resource
	for _, marked := range g.holdsRoles {
		records = slices.AppendSeq(records, maps.Keys(marked))
	}
	slices.Sort(records)

	return records
}

// RecordWords are what a sentence calls one row of res and many, in lower case: a
// marked record's words, else the words a record gives the resource it names, else ""
// twice.
func (g *GeneratedCollection) RecordWords(res accesstypes.Resource) (one, many string) {
	if g == nil {
		return "", ""
	}
	if record, ok := g.heldRecord(res); ok {
		return record.One, record.Many
	}
	for _, marked := range g.RecordsHoldingRoles() {
		record, _ := g.heldRecord(marked)
		for _, name := range record.Names {
			if name.Resource == res {
				return name.One, name.Many
			}
		}
	}

	return "", ""
}

// RecordReach says how a row of res stands to a record of the kind record: it is the
// record itself (self, the record being marked), it is under it by a way that is not the
// record's own (under), or the record names it (named). A resource apart is none of the
// three.
func (g *GeneratedCollection) RecordReach(record, res accesstypes.Resource) (self, under, named bool) {
	if g == nil {
		return false, false, false
	}
	data, marked := g.heldRecord(record)
	self = marked && res == record
	for _, ways := range g.under {
		for _, way := range ways[res] {
			if way.Record == record && !way.Self {
				under = true
			}
		}
	}
	if marked {
		named = slices.ContainsFunc(data.Names, func(name RecordNameData) bool {
			return name.Resource == res
		})
	}

	return self, under, named
}

// RecordPlace spells how a row of res stands to a record of the kind record, column by
// column, for two collections to be compared by (access.UnionCollection): a line for each
// way the row is under it, in the ways' order, then a line for each name of res by the
// record, in the names' order. A way's line is its attribute, a colon, the column of the
// row the way leaves through, and for each hop of the attribute's path a comma, the hop's
// table and the column it is joined on, an arrow and the column it reads
// ("held_way0_Projects: TaskId, Tasks.Id -> ProjectId"); the record's own way is its key
// ("held_way0_Projects: Id"). A name's line is its attribute, a colon, the record's column
// that names the row, an arrow, and the named row's column the attribute reads
// ("held_named0_Projects: ClientId -> Id"). A condition names a way by its number, so two
// sites that reach a table through other columns give the same condition and differ here.
// It is nil where res stands apart from the record, and where record is no record a role
// can be held on.
func (g *GeneratedCollection) RecordPlace(record, res accesstypes.Resource) []string {
	if g == nil {
		return nil
	}
	data, marked := g.heldRecord(record)
	if !marked {
		return nil
	}
	scope := g.Scope(res)
	var lines []string
	for _, way := range g.waysUnder(res) {
		if way.Record != record {
			continue
		}
		attr, _ := g.attributeData(scope, res, way.Attribute)
		line := way.Attribute + ": " + attr.Column
		for _, hop := range attr.Path {
			line += ", " + hop.Table + "." + hop.JoinColumn + " -> " + hop.Column
		}
		lines = append(lines, line)
	}
	for _, name := range data.Names {
		if name.Resource == res {
			attr, _ := g.attributeData(scope, res, name.Attribute)
			lines = append(lines, name.Attribute+": "+name.Column+" -> "+attr.Column)
		}
	}

	return lines
}

// recordRoleStore is where auth keeps the roles held on records; ok is false for an auth
// with no store.
func (g *GeneratedCollection) recordRoleStore(auth string) (RecordRoleStoreData, bool) {
	for _, store := range g.recordRoleStores {
		if store.Auth == auth {
			return store, true
		}
	}

	return RecordRoleStoreData{}, false
}

// keepsGroups reports whether the store keeps the roles groups hold on records, and so
// whether a group's sets are there.
func (s *RecordRoleStoreData) keepsGroups() bool {
	return s.GroupRoles != "" && s.GroupSet != ""
}

// waysUnder are the ways a row of res is under the records, whatever its scope.
func (g *GeneratedCollection) waysUnder(res accesstypes.Resource) []RecordWayData {
	for _, under := range g.under {
		if ways, ok := under[res]; ok {
			return ways
		}
	}

	return nil
}

// RecordCondition is the condition under which a grant of perm on res counts for a holder
// of role on a record of the kind record, for the people of auth: the tests below joined
// by OR, and ok false where there is none, the auth keeps no roles on records, or record
// is no record a role can be held on.
//
// For each way of res to the record, in the order of Under, the way's attribute is in the
// set of the records the person holds the role on, then in the set of those their groups
// hold it on (where the store keeps groups). Unless perm is Create, the same follows for
// each name of res by the record, its attribute in the sets of the rows those records
// name. A create is under the record by the row it writes: a row the record names is
// reached and never made, and neither is the record's own kind (its Self way), by a role
// held on one record. An update asks the tests twice, of the row as stored and of the row
// as the update leaves it (new.), so a row moved out from under every record the role is
// held on is refused; a row reached by the record's own key or by a name is the same row
// after, and the second asking is the first.
func (g *GeneratedCollection) RecordCondition(auth string, role accesstypes.Role, record, res accesstypes.Resource, perm accesstypes.Permission) (condition string, ok bool) {
	if g == nil {
		return "", false
	}
	store, kept := g.recordRoleStore(auth)
	data, marked := g.heldRecord(record)
	if !kept || !marked {
		return "", false
	}

	create := perm == accesstypes.Create
	tests := func(image string) []string {
		var tests []string
		member := func(attribute string, named int) {
			tests = append(tests, image+attribute+" IN subject."+recordSetName(named, false, auth, role, record))
			if store.keepsGroups() {
				tests = append(tests, image+attribute+" IN subject."+recordSetName(named, true, auth, role, record))
			}
		}
		for _, way := range g.waysUnder(res) {
			if way.Record == record && (!create || !way.Self) {
				member(way.Attribute, -1)
			}
		}
		if !create {
			for n, name := range data.Names {
				if name.Resource == res {
					member(name.Attribute, n)
				}
			}
		}

		return tests
	}
	stored := tests("")
	if len(stored) == 0 {
		return "", false
	}
	if perm == accesstypes.Update {
		return "(" + strings.Join(stored, " OR ") + ") AND (" + strings.Join(tests("new."), " OR ") + ")", true
	}

	return strings.Join(stored, " OR "), true
}

// recordSet reads a subject set of the records a person holds a role on from its name
// (recordSetName): the auth is one whose store the collection holds, the record one it
// marks, and what stands between them a role's name. Nothing is kept per role: the set is
// made from the name each time. The set reads the store's table of people's roles held on
// records, or of groups' through the set of a person's groups, keeps the rows of the
// record's kind and the role inside their periods, and yields the records' keys, or with
// a name n the column of each record that names its row. ok is false for any other name.
func (g *GeneratedCollection) recordSet(name string) (SubjectAnchor, bool) {
	rest, ok := strings.CutPrefix(name, ReservedBindingPrefix)
	if !ok {
		return SubjectAnchor{}, false
	}
	named := -1
	if after, ok := strings.CutPrefix(rest, "named"); ok {
		digits := after[:len(after)-len(strings.TrimLeft(after, "0123456789"))]
		n, err := strconv.Atoi(digits)
		if err != nil || strconv.Itoa(n) != digits {
			return SubjectAnchor{}, false
		}
		named, rest = n, after[len(digits):]
	}
	group := false
	switch {
	case strings.HasPrefix(rest, "bygroup_"):
		group, rest = true, strings.TrimPrefix(rest, "bygroup_")
	case strings.HasPrefix(rest, "by_"):
		rest = strings.TrimPrefix(rest, "by_")
	default:
		return SubjectAnchor{}, false
	}
	store, role, record, ok := g.recordSetOwner(rest)
	if !ok {
		return SubjectAnchor{}, false
	}
	data, _ := g.heldRecord(record)
	if named >= len(data.Names) || (group && !store.keepsGroups()) {
		return SubjectAnchor{}, false
	}

	table := store.UserRoles
	binding := SubjectBindingData{
		Name: name, UserColumn: recordUserColumn, Column: recordKeyColumn, Type: AttributeTypeString,
		Where:   recordSetWhere(record, role),
		Offered: true, Auth: store.Auth,
	}
	if group {
		table = store.GroupRoles
		binding.UserColumn = ""
		binding.Who = []SubjectWhoData{{Column: recordGroupColumn, Set: store.GroupSet}}
	}
	if named >= 0 {
		binding.Path = []BindingHop{{Table: string(record), JoinColumn: data.Key, Column: data.Names[named].Column}}
	}

	return SubjectAnchor{
		Resource: accesstypes.Resource(table),
		Scope:    accesstypes.DomainPermissionScope,
		Binding:  binding,
		Domain:   &DomainBindingData{Column: recordDomainColumn},
	}, true
}

// storeGroupSet reads the subject set of a person's groups from the store of the auth it
// is named for, where no struct declares it: the anchor the generated group member
// declares, field for field. The set reads the auth's table of the groups' members by
// the person, keeps each place inside its period, and yields the group; its rows reach
// a tenant through the group, which the request's own tenant must hold, and it describes
// the auth's people. ok is false for a name no store names, and for a store that keeps no
// groups.
func (g *GeneratedCollection) storeGroupSet(name string) (SubjectAnchor, bool) {
	for _, store := range g.recordRoleStores {
		if store.GroupSet != name || store.GroupMembers == "" || store.Groups == "" {
			continue
		}
		domain := &DomainBindingData{Column: recordGroupColumn, Path: []BindingHop{{Table: store.Groups, JoinColumn: "Id", Column: recordDomainColumn}}}

		return SubjectAnchor{
			Resource: accesstypes.Resource(store.GroupMembers),
			Scope:    accesstypes.GlobalPermissionScope,
			Binding: SubjectBindingData{
				Name: name, UserColumn: recordUserColumn, Column: recordGroupColumn, Type: AttributeTypeString,
				Where: &SubjectWhereData{Condition: recordPeriodWhere, Columns: []SubjectColumnData{
					{Name: recordEndsColumn, Column: recordEndsColumn, Type: AttributeTypeTimestamp},
					{Name: recordStartsColumn, Column: recordStartsColumn, Type: AttributeTypeTimestamp},
				}},
				Domain:  domain,
				Offered: true, Auth: store.Auth,
			},
			Domain: domain,
		}, true
	}

	return SubjectAnchor{}, false
}

// recordSetOwner reads what a record set's name holds after its kind,
// <auth>_<Role>_<Record>: an auth with a store, a marked record, and a role's name
// between them.
func (g *GeneratedCollection) recordSetOwner(rest string) (RecordRoleStoreData, accesstypes.Role, accesstypes.Resource, bool) {
	for _, store := range g.recordRoleStores {
		between, ok := strings.CutPrefix(rest, store.Auth+"_")
		if !ok {
			continue
		}
		for _, record := range g.RecordsHoldingRoles() {
			role, ok := strings.CutSuffix(between, "_"+string(record))
			if ok && recordSetRole.MatchString(role) {
				return store, accesstypes.Role(role), record, true
			}
		}
	}

	return RecordRoleStoreData{}, "", "", false
}

// recordSetWhere is a record set's where: part: the rows of the record's kind and the
// role, inside their periods.
func recordSetWhere(record accesstypes.Resource, role accesstypes.Role) *SubjectWhereData {
	return &SubjectWhereData{
		Condition: fmt.Sprintf("Resource = '%s' AND Role = '%s' AND %s", record, role, recordPeriodWhere),
		Columns: []SubjectColumnData{
			{Name: "Resource", Column: "Resource", Type: AttributeTypeString},
			{Name: "Role", Column: "Role", Type: AttributeTypeString},
			{Name: recordStartsColumn, Column: recordStartsColumn, Type: AttributeTypeTimestamp},
			{Name: recordEndsColumn, Column: recordEndsColumn, Type: AttributeTypeTimestamp},
		},
	}
}
