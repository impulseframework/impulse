package resource

// The semantic differential's fixture: a hand-written checked resource with
// every comparison type in a NOT NULL and a nullable column, two join paths,
// a partitioned anchor for subject sets and a global anchor for subject
// values, and the hand-built collection declaring their bindings — the way
// the package's rendering tests build theirs. The schema is the SQL fixture
// under testdata/semantic/schema.

import (
	"math/big"
	"reflect"
	"slices"
	"testing"
	"time"

	"cloud.google.com/go/civil"
	"cloud.google.com/go/spanner"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition/conditiontest"
	"github.com/shopspring/decimal"
)

const semanticResource = accesstypes.Resource("Parcels")

// semanticParcel is the checked resource: the row as the database stores it.
type semanticParcel struct {
	ID          impulse.UUID        `spanner:"Id"`
	Depot       string              `spanner:"Depot"`
	Label       string              `spanner:"Label"`
	Note        *string             `spanner:"Note"`
	Weight      int64               `spanner:"Weight"`
	Pieces      *int64              `spanner:"Pieces"`
	Ratio       float64             `spanner:"Ratio"`
	Density     *float64            `spanner:"Density"`
	Price       decimal.Decimal     `spanner:"Price"`
	Fee         decimal.NullDecimal `spanner:"Fee"`
	Fragile     bool                `spanner:"Fragile"`
	Insured     *bool               `spanner:"Insured"`
	ShippedAt   time.Time           `spanner:"ShippedAt"`
	DeliveredAt *time.Time          `spanner:"DeliveredAt"`
	ShipDate    civil.Date          `spanner:"ShipDate"`
	DueDate     *civil.Date         `spanner:"DueDate"`
	CarrierID   *string             `spanner:"CarrierId"`
	RouteID     *string             `spanner:"RouteId"`
	// Stamps is the list column: no list at all, a list with no item, and an item
	// with no value all occur. No request reads or writes it; conditions do.
	Stamps []spanner.NullString `spanner:"Stamps"`
}

func (semanticParcel) Resource() accesstypes.Resource { return semanticResource }

func (semanticParcel) DefaultConfig() Config { return Config{} }

// semanticListRequest is the read shape: the key exempt, the tenant key
// wire-closed, every other column indexed so a filter may name it alone.
type semanticListRequest struct {
	ID          impulse.UUID        `json:"id"          perm:"-"`
	Depot       string              `json:"-"`
	Label       string              `json:"label"       index:"true"`
	Note        *string             `json:"note"        index:"true"`
	Weight      int64               `json:"weight"      index:"true"`
	Pieces      *int64              `json:"pieces"      index:"true"`
	Ratio       float64             `json:"ratio"       index:"true"`
	Density     *float64            `json:"density"     index:"true"`
	Price       decimal.Decimal     `json:"price"       index:"true"`
	Fee         decimal.NullDecimal `json:"fee"         index:"true"`
	Fragile     bool                `json:"fragile"     index:"true"`
	Insured     *bool               `json:"insured"     index:"true"`
	ShippedAt   time.Time           `json:"shippedAt"   index:"true"`
	DeliveredAt *time.Time          `json:"deliveredAt" index:"true"`
	ShipDate    civil.Date          `json:"shipDate"    index:"true"`
	DueDate     *civil.Date         `json:"dueDate"     index:"true"`
	CarrierID   *string             `json:"carrierId"   index:"true"`
	RouteID     *string             `json:"routeId"     index:"true"`
}

// semanticPatchRequest is the write shape: the tenant key wire-closed.
type semanticPatchRequest struct {
	Depot       string              `json:"-"`
	Label       string              `json:"label"`
	Note        *string             `json:"note"`
	Weight      int64               `json:"weight"`
	Pieces      *int64              `json:"pieces"`
	Ratio       float64             `json:"ratio"`
	Density     *float64            `json:"density"`
	Price       decimal.Decimal     `json:"price"`
	Fee         decimal.NullDecimal `json:"fee"`
	Fragile     bool                `json:"fragile"`
	Insured     *bool               `json:"insured"`
	ShippedAt   time.Time           `json:"shippedAt"`
	DeliveredAt *time.Time          `json:"deliveredAt"`
	ShipDate    civil.Date          `json:"shipDate"`
	DueDate     *civil.Date         `json:"dueDate"`
	CarrierID   *string             `json:"carrierId"`
	RouteID     *string             `json:"routeId"`
}

// semanticField is one projected grant-bearing field with its wire name, in
// declaration order.
type semanticField struct {
	field accesstypes.Field
	json  string
}

var semanticFields = []semanticField{
	{field: "Label", json: "label"},
	{field: "Note", json: "note"},
	{field: "Weight", json: "weight"},
	{field: "Pieces", json: "pieces"},
	{field: "Ratio", json: "ratio"},
	{field: "Density", json: "density"},
	{field: "Price", json: "price"},
	{field: "Fee", json: "fee"},
	{field: "Fragile", json: "fragile"},
	{field: "Insured", json: "insured"},
	{field: "ShippedAt", json: "shippedAt"},
	{field: "DeliveredAt", json: "deliveredAt"},
	{field: "ShipDate", json: "shipDate"},
	{field: "DueDate", json: "dueDate"},
	{field: "CarrierID", json: "carrierId"},
	{field: "RouteID", json: "routeId"},
}

// semanticAttribute is one column attribute: its binding name, the Go field
// it reads, and its comparison type.
type semanticAttribute struct {
	name  string
	field accesstypes.Field
	typ   AttributeType
}

// semanticColumnAttributes are the checked resource's column attributes; the
// two join-path attributes (carrierCode, hubRegion) resolve through the
// world's related rows.
var semanticColumnAttributes = []semanticAttribute{
	{name: "label", field: "Label", typ: AttributeTypeString},
	{name: "note", field: "Note", typ: AttributeTypeString},
	{name: "weight", field: "Weight", typ: AttributeTypeNumber},
	{name: "pieces", field: "Pieces", typ: AttributeTypeNumber},
	{name: "ratio", field: "Ratio", typ: AttributeTypeNumber},
	{name: "density", field: "Density", typ: AttributeTypeNumber},
	{name: "price", field: "Price", typ: AttributeTypeNumber},
	{name: "fee", field: "Fee", typ: AttributeTypeNumber},
	{name: "fragile", field: "Fragile", typ: AttributeTypeBool},
	{name: "insured", field: "Insured", typ: AttributeTypeBool},
	{name: "shippedAt", field: "ShippedAt", typ: AttributeTypeTimestamp},
	{name: "deliveredAt", field: "DeliveredAt", typ: AttributeTypeTimestamp},
	{name: "shipDate", field: "ShipDate", typ: AttributeTypeDate},
	{name: "dueDate", field: "DueDate", typ: AttributeTypeDate},
}

// semanticCollection declares the fixture's vocabulary: every column attribute
// typed, the two join paths, the domain binding, the partitioned anchor's
// subject sets (one dotted), the global anchor's typed subject values (one
// dotted), and the subject sets that write a who: or a where: part
// (semanticPartSets).
func semanticCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	return semanticCollectionOf(t, semanticResources())
}

// semanticCollectionOf builds a collection over the fixture's resources as given.
func semanticCollectionOf(t *testing.T, resources []CollectionResource) *GeneratedCollection {
	t.Helper()

	g, err := NewGeneratedCollection(CollectionData{Resources: resources})
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return g
}

// semanticDrawnWhere is the where: of the fixture's drawnTeams set where no case
// has drawn one: the semantic differential draws a new one for each case.
const semanticDrawnWhere = "Tier >= 0"

// semanticResources is the fixture's collection, resource by resource.
func semanticResources() []CollectionResource {
	return semanticResourcesWith(semanticDrawnWhere)
}

// semanticPassColumns are the columns of the fixture's Passes table a where:
// may read, one of every comparison type and of each storage type a number has.
var semanticPassColumns = []SubjectColumnData{
	{Name: "Team", Column: "Team", Type: AttributeTypeString},
	{Name: "Level", Column: "Level", Type: AttributeTypeString},
	{Name: "Tier", Column: "Tier", Type: AttributeTypeNumber},
	{Name: "Mass", Column: "Mass", Type: AttributeTypeNumber},
	{Name: "Stake", Column: "Stake", Type: AttributeTypeNumber},
	{Name: "Sealed", Column: "Sealed", Type: AttributeTypeBool},
	{Name: "IssuedOn", Column: "IssuedOn", Type: AttributeTypeDate},
	{Name: "ExpiresAt", Column: "ExpiresAt", Type: AttributeTypeTimestamp},
}

// semanticPartSets are the anchors of the subject sets that write a who: or a
// where: part, each shape the two parts take:
//
//   - circles, on a table outside every partition: the default who:, and a
//     where: that drops a row past its ValidUntil;
//   - widerCircles, on the same table: a set that follows another set and names
//     the requester nowhere itself;
//   - editTeams and viewTeams, two sets one partitioned table yields by a
//     column, each a person's own rows or their circles' (a partitioned set
//     building on one outside every partition);
//   - farTeams: two sets deep, through widerCircles to circles;
//   - drawnTeams: a where: drawn for each case of the differential;
//   - passTiers: a set of numbers under a where: over a bool and a double;
//   - badges, outside every partition: a set building on the partitioned teams.
func semanticPartSets(drawnWhere string) []CollectionResource {
	ownOrCircle := []SubjectWhoData{{Column: "UserId"}, {Column: "Circle", Set: "circles"}}
	passColumns := func(names ...string) []SubjectColumnData {
		columns := make([]SubjectColumnData, 0, len(names))
		for _, column := range semanticPassColumns {
			if slices.Contains(names, column.Name) {
				columns = append(columns, column)
			}
		}

		return columns
	}

	return []CollectionResource{
		{
			Name:  "Circles",
			Scope: accesstypes.GlobalPermissionScope,
			SubjectSets: []SubjectBindingData{
				{
					Name: "circles", UserColumn: "UserId", Column: "Circle", Type: AttributeTypeString,
					Where: &SubjectWhereData{
						Condition: "ValidUntil IS NULL OR ValidUntil > now",
						Columns:   []SubjectColumnData{{Name: "ValidUntil", Column: "ValidUntil", Type: AttributeTypeTimestamp}},
					},
				},
				{
					Name: "widerCircles", UserColumn: "InnerCircle", Column: "Circle", Type: AttributeTypeString,
					Who: []SubjectWhoData{{Column: "InnerCircle", Set: "circles"}},
				},
			},
		},
		{
			Name:   "Passes",
			Scope:  accesstypes.DomainPermissionScope,
			Domain: &DomainBindingData{Column: "Depot"},
			SubjectSets: []SubjectBindingData{
				{
					Name: "editTeams", UserColumn: "UserId", Column: "Team", Type: AttributeTypeString,
					Who:   ownOrCircle,
					Where: &SubjectWhereData{Condition: "Level = 'edit' AND (ExpiresAt IS NULL OR ExpiresAt > now)", Columns: passColumns("Level", "ExpiresAt")},
				},
				{
					Name: "viewTeams", UserColumn: "UserId", Column: "Team", Type: AttributeTypeString,
					Who:   ownOrCircle,
					Where: &SubjectWhereData{Condition: "Level IN ('view', 'edit')", Columns: passColumns("Level")},
				},
				{
					Name: "farTeams", UserColumn: "Circle", Column: "Team", Type: AttributeTypeString,
					Who: []SubjectWhoData{{Column: "Circle", Set: "widerCircles"}},
				},
				{
					Name: "drawnTeams", UserColumn: "UserId", Column: "Team", Type: AttributeTypeString,
					Who:   ownOrCircle,
					Where: &SubjectWhereData{Condition: drawnWhere, Columns: semanticPassColumns},
				},
				{
					Name: "passTiers", UserColumn: "UserId", Column: "Tier", Type: AttributeTypeNumber,
					Where: &SubjectWhereData{Condition: "Sealed = false OR Mass >= 10.5", Columns: passColumns("Sealed", "Mass")},
				},
			},
		},
		{
			Name:  "Badges",
			Scope: accesstypes.GlobalPermissionScope,
			SubjectSets: []SubjectBindingData{{
				Name: "badges", UserColumn: "Team", Column: "Badge", Type: AttributeTypeString,
				Who: []SubjectWhoData{{Column: "Team", Set: "teams"}},
			}},
		},
	}
}

// semanticResourcesWith is the fixture's collection with the drawnTeams set's
// where: as given.
func semanticResourcesWith(drawnWhere string) []CollectionResource {
	attributes := make([]AttributeData, 0, len(semanticColumnAttributes)+2)
	for _, attr := range semanticColumnAttributes {
		attributes = append(attributes, AttributeData{Name: attr.name, Column: string(attr.field), Type: attr.typ})
	}
	attributes = append(attributes,
		AttributeData{Name: "stamp", Column: "Stamps", Type: AttributeTypeString, List: true},
		AttributeData{Name: "carrierCode", Column: "CarrierId", Type: AttributeTypeString, Path: []BindingHop{{Table: "Carriers", JoinColumn: "Id", Column: "Code"}}},
		AttributeData{Name: "hubRegion", Column: "RouteId", Type: AttributeTypeString, Path: []BindingHop{
			{Table: "Routes", JoinColumn: "Id", Column: "HubId"},
			{Table: "Hubs", JoinColumn: "Id", Column: "Region"},
		}},
	)

	// Every grant-bearing field registers under the read and write permissions, as
	// the generator registers it; the key is permission-exempt. The Update envelope
	// is planned over the tags the collection registers Update on.
	tags := make([]TagData, 0, 1+len(semanticFields))
	tags = append(tags, TagData{Name: "id"})
	for _, f := range semanticFields {
		tags = append(tags, TagData{Name: accesstypes.Tag(f.json), Permissions: []accesstypes.Permission{accesstypes.Create, accesstypes.List, accesstypes.Read, accesstypes.Update}})
	}

	return append([]CollectionResource{
		{
			Name:        semanticResource,
			Scope:       accesstypes.DomainPermissionScope,
			Permissions: []accesstypes.Permission{accesstypes.List, accesstypes.Read, accesstypes.Create, accesstypes.Update, accesstypes.Delete},
			Tags:        tags,
			Attributes:  attributes,
			Domain:      &DomainBindingData{Column: "Depot"},
		},
		{
			Name:  "Memberships",
			Scope: accesstypes.DomainPermissionScope,
			SubjectSets: []SubjectBindingData{
				{Name: "teams", UserColumn: "UserId", Column: "Team", Type: AttributeTypeString},
				{Name: "tiers", UserColumn: "UserId", Column: "Tier", Type: AttributeTypeNumber},
				{Name: "regions", UserColumn: "UserId", Column: "HubId", Type: AttributeTypeString, Path: []BindingHop{{Table: "Hubs", JoinColumn: "Id", Column: "Region"}}},
			},
			Domain: &DomainBindingData{Column: "Depot"},
		},
		{
			Name:  "Profiles",
			Scope: accesstypes.GlobalPermissionScope,
			SubjectValues: []SubjectBindingData{
				{Name: "nickname", UserColumn: "UserId", Column: "Nickname", Type: AttributeTypeString},
				{Name: "quota", UserColumn: "UserId", Column: "Quota", Type: AttributeTypeNumber},
				{Name: "rate", UserColumn: "UserId", Column: "Rate", Type: AttributeTypeNumber},
				{Name: "allowance", UserColumn: "UserId", Column: "Allowance", Type: AttributeTypeNumber},
				{Name: "active", UserColumn: "UserId", Column: "Active", Type: AttributeTypeBool},
				{Name: "clearedUntil", UserColumn: "UserId", Column: "ClearedUntil", Type: AttributeTypeTimestamp},
				{Name: "startDate", UserColumn: "UserId", Column: "StartDate", Type: AttributeTypeDate},
				{Name: "homeRegion", UserColumn: "UserId", Column: "HomeHubId", Type: AttributeTypeString, Path: []BindingHop{{Table: "Hubs", JoinColumn: "Id", Column: "Region"}}},
			},
		},
	}, semanticPartSets(drawnWhere)...)
}

// semanticVocabulary is the fixture's vocabulary as the condition generator
// sees it, read from the collection so the generator pairs exactly what
// deploy validation admits; postImage marks a write context, and lists says
// whether the list attribute is in it: a create's condition reads the row the
// create proposes, where a list is not judged, so its vocabulary has none.
func semanticVocabulary(t *testing.T, collection *GeneratedCollection, postImage, lists bool) conditiontest.Vocabulary {
	t.Helper()

	bindings, ok := collection.Bindings(accesstypes.DomainPermissionScope, semanticResource)
	if !ok {
		t.Fatal("fixture bindings missing")
	}
	vocab := conditiontest.Vocabulary{PostImage: postImage}
	for _, attr := range bindings.Attributes {
		if attr.List && !lists {
			continue
		}
		vocab.Attributes = append(vocab.Attributes, conditiontest.Attribute{Name: attr.Name, Type: attr.Type, JoinPath: len(attr.Path) > 0, List: attr.List})
	}
	data := collection.Data()
	for i := range data.Resources {
		res := &data.Resources[i]
		for j := range res.SubjectSets {
			set := &res.SubjectSets[j]
			vocab.SubjectSets = append(vocab.SubjectSets, conditiontest.SubjectBinding{Name: set.Name, Type: set.Type})
		}
		for j := range res.SubjectValues {
			value := &res.SubjectValues[j]
			vocab.SubjectValues = append(vocab.SubjectValues, conditiontest.SubjectBinding{Name: value.Name, Type: value.Type})
		}
	}

	return vocab
}

// The related tables' rows, as the harness writes them.
type semanticHub struct {
	ID     string  `spanner:"Id"`
	Region *string `spanner:"Region"`
}

type semanticCarrier struct {
	ID   string  `spanner:"Id"`
	Code *string `spanner:"Code"`
}

type semanticRoute struct {
	ID    string  `spanner:"Id"`
	HubID *string `spanner:"HubId"`
}

type semanticMembership struct {
	ID     string  `spanner:"Id"`
	UserID string  `spanner:"UserId"`
	Depot  string  `spanner:"Depot"`
	Team   *string `spanner:"Team"`
	Tier   *int64  `spanner:"Tier"`
	HubID  *string `spanner:"HubId"`
}

// The anchors of the subject sets that write a who: or a where: part.
type semanticCircle struct {
	ID          string     `spanner:"Id"`
	Circle      *string    `spanner:"Circle"`
	UserID      *string    `spanner:"UserId"`
	InnerCircle *string    `spanner:"InnerCircle"`
	ValidUntil  *time.Time `spanner:"ValidUntil"`
}

type semanticPass struct {
	ID        string              `spanner:"Id"`
	Depot     string              `spanner:"Depot"`
	UserID    *string             `spanner:"UserId"`
	Circle    *string             `spanner:"Circle"`
	Team      *string             `spanner:"Team"`
	Level     *string             `spanner:"Level"`
	Tier      *int64              `spanner:"Tier"`
	Mass      *float64            `spanner:"Mass"`
	Stake     decimal.NullDecimal `spanner:"Stake"`
	Sealed    *bool               `spanner:"Sealed"`
	IssuedOn  *civil.Date         `spanner:"IssuedOn"`
	ExpiresAt *time.Time          `spanner:"ExpiresAt"`
}

type semanticBadge struct {
	ID    string  `spanner:"Id"`
	Team  *string `spanner:"Team"`
	Badge *string `spanner:"Badge"`
}

type semanticProfile struct {
	UserID       string              `spanner:"UserId"`
	Nickname     *string             `spanner:"Nickname"`
	Quota        *int64              `spanner:"Quota"`
	Rate         *float64            `spanner:"Rate"`
	Allowance    decimal.NullDecimal `spanner:"Allowance"`
	Active       *bool               `spanner:"Active"`
	ClearedUntil *time.Time          `spanner:"ClearedUntil"`
	StartDate    *civil.Date         `spanner:"StartDate"`
	HomeHubID    *string             `spanner:"HomeHubId"`
}

// semanticValue converts a stored Go value to the evaluator's storage
// vocabulary: pointers dereferenced (nil is no value), NUMERIC as *big.Rat,
// a date as conditiontest.Date, a UUID as its text, timestamps in UTC.
func semanticValue(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case *string:
		if x == nil {
			return nil
		}

		return *x
	case *int64:
		if x == nil {
			return nil
		}

		return *x
	case *float64:
		if x == nil {
			return nil
		}

		return *x
	case *bool:
		if x == nil {
			return nil
		}

		return *x
	case *time.Time:
		if x == nil {
			return nil
		}

		return x.UTC()
	case time.Time:
		return x.UTC()
	case *civil.Date:
		if x == nil {
			return nil
		}

		return semanticDate(*x)
	case civil.Date:
		return semanticDate(x)
	case decimal.Decimal:
		return x.Rat()
	case decimal.NullDecimal:
		if !x.Valid {
			return nil
		}

		return x.Decimal.Rat()
	case *big.Rat:
		return x
	case impulse.UUID:
		return x.String()
	case string, int64, float64, bool:
		return x
	default:
		panic("semanticValue: unsupported stored type " + reflect.TypeOf(v).String())
	}
}

func semanticDate(d civil.Date) conditiontest.Date {
	return conditiontest.Date{Year: d.Year, Month: d.Month, Day: d.Day}
}

// semanticFieldValue reads one field of a parcel by name.
func semanticFieldValue(p *semanticParcel, field accesstypes.Field) any {
	return fieldValue(reflect.ValueOf(*p), string(field)).Interface()
}

// compareSemantic orders two evaluator values of one storage type, nil first
// so a caller can place the NULL region itself.
func compareSemantic(a, b any) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	switch x := a.(type) {
	case string:
		y, _ := b.(string)

		return cmpOrdered(x, y)
	case int64:
		y, _ := b.(int64)

		return cmpOrdered(x, y)
	case float64:
		y, _ := b.(float64)

		return cmpOrdered(x, y)
	case *big.Rat:
		y, _ := b.(*big.Rat)

		return x.Cmp(y)
	case bool:
		y, _ := b.(bool)

		return cmpOrdered(boolOrder(x), boolOrder(y))
	case time.Time:
		y, _ := b.(time.Time)

		return x.Compare(y)
	case conditiontest.Date:
		y, _ := b.(conditiontest.Date)

		return cmpOrdered(x.String(), y.String())
	default:
		panic("compareSemantic: unsupported value type " + reflect.TypeOf(a).String())
	}
}

func cmpOrdered[T string | int64 | float64 | int](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func boolOrder(b bool) int {
	if b {
		return 1
	}

	return 0
}
