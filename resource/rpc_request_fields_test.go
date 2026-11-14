package resource

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/civil"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/accesstypes/condition"
)

// rateRequest is a method's decoded request, with one field of each type a condition
// may read.
type rateRequest struct {
	ID      impulse.UUID
	Role    string
	Note    *string
	Level   int64
	Count   uint32
	Ratio   float64
	Urgent  bool
	At      time.Time
	Day     civil.Date
	Roles   []string
	Levels  []int64
	Unnamed string
}

// rateCollection is a collection that registers the method RateThing with every field
// of rateRequest but Unnamed declared for conditions.
func rateCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	b := NewCollectionBuilder()
	if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, "RateThing"); err != nil {
		t.Fatalf("AddMethodResource() error = %v", err)
	}
	b.SetResourceBindings(accesstypes.GlobalPermissionScope, "RateThing", &Bindings{Attributes: []AttributeData{
		{Name: "id", Column: "ID", Type: AttributeTypeString},
		{Name: "role", Column: "Role", Type: AttributeTypeString},
		{Name: "note", Column: "Note", Type: AttributeTypeString},
		{Name: "level", Column: "Level", Type: AttributeTypeNumber},
		{Name: "count", Column: "Count", Type: AttributeTypeNumber},
		{Name: "ratio", Column: "Ratio", Type: AttributeTypeNumber},
		{Name: "urgent", Column: "Urgent", Type: AttributeTypeBool},
		{Name: "at", Column: "At", Type: AttributeTypeTimestamp},
		{Name: "day", Column: "Day", Type: AttributeTypeDate},
		{Name: "eachRole", Column: "Roles", Type: AttributeTypeString},
		{Name: "eachLevel", Column: "Levels", Type: AttributeTypeNumber},
		{Name: "gone", Column: "NoSuchField", Type: AttributeTypeString},
	}})

	return b.GeneratedCollection()
}

// TestSettleRequestFields pins how a grant's condition is settled against a decoded
// request: each type a request field may have, a field with no value, a list judged
// element by element, and what is left for the gate when the condition also reads the
// target row.
func TestSettleRequestFields(t *testing.T) {
	t.Parallel()

	note := "checked"
	base := rateRequest{
		ID:     impulse.UUID{},
		Role:   "Reader",
		Level:  2,
		Count:  7,
		Ratio:  0.25,
		Urgent: false,
		At:     time.Date(2027, 3, 1, 12, 0, 0, 0, time.FixedZone("MST", -7*3600)),
		Day:    civil.Date{Year: 2027, Month: time.March, Day: 1},
	}
	with := func(change func(*rateRequest)) *rateRequest {
		request := base
		change(&request)

		return &request
	}
	tests := []struct {
		name    string
		source  string
		request *rateRequest
		// want is the settled expression's text; TRUE and FALSE are the constants.
		want    string
		wantErr string
	}{
		{name: "a string", source: "new.role != 'Administrator'", request: &base, want: "TRUE"},
		{name: "a string that fails", source: "new.role != 'Reader'", request: &base, want: "FALSE"},
		{name: "a string left out of the request is the empty string, a value", source: "new.role != 'Administrator'", request: with(func(r *rateRequest) { r.Role = "" }), want: "TRUE"},
		{name: "a pointer that points at a value", source: "new.note = 'checked'", request: with(func(r *rateRequest) { r.Note = &note }), want: "TRUE"},
		{name: "a nil pointer is no value, which never permits", source: "new.note != 'checked'", request: &base, want: "FALSE"},
		{name: "a nil pointer never permits under a negation either", source: "NOT (new.note = 'checked')", request: &base, want: "FALSE"},
		{name: "a signed integer", source: "new.level <= 3", request: &base, want: "TRUE"},
		{name: "an unsigned integer", source: "new.count > 7", request: &base, want: "FALSE"},
		{name: "a float", source: "new.ratio < 0.5", request: &base, want: "TRUE"},
		{name: "a bool", source: "new.urgent = false", request: &base, want: "TRUE"},
		{name: "an instant, compared in UTC whatever zone it was decoded in", source: "new.at >= '2027-03-01T19:00:00Z'", request: &base, want: "TRUE"},
		{name: "an instant before the bound", source: "new.at >= '2027-03-01T19:00:01Z'", request: &base, want: "FALSE"},
		{name: "a date", source: "new.day >= '2027-03-01'", request: &base, want: "TRUE"},
		{name: "a date before the bound", source: "new.day > '2027-03-01'", request: &base, want: "FALSE"},
		{name: "a list whose every element passes", source: "new.eachRole IN ('Reader', 'Editor')", request: with(func(r *rateRequest) { r.Roles = []string{"Reader", "Editor"} }), want: "TRUE"},
		{name: "a list with one element that fails", source: "new.eachRole IN ('Reader', 'Editor')", request: with(func(r *rateRequest) { r.Roles = []string{"Reader", "Administrator"} }), want: "FALSE"},
		{name: "a list with no element is no value", source: "new.eachRole != 'Administrator'", request: with(func(r *rateRequest) { r.Roles = []string{} }), want: "FALSE"},
		{name: "a list left out is no value", source: "new.eachRole != 'Administrator'", request: &base, want: "FALSE"},
		{name: "a list of numbers", source: "new.eachLevel <= 3", request: with(func(r *rateRequest) { r.Levels = []int64{1, 2, 3} }), want: "TRUE"},
		{name: "a list beside a scalar: each element is judged with the scalar", source: "new.eachRole != 'Administrator' AND new.level <= 3", request: with(func(r *rateRequest) { r.Roles = []string{"Reader", "Editor"} }), want: "TRUE"},
		{name: "the row's part is left for the gate", source: "heldElsewhere = false AND new.level <= 3", request: &base, want: "heldElsewhere = false"},
		{name: "the row's part is left once for a list", source: "heldElsewhere = false AND new.eachRole != 'Administrator'", request: with(func(r *rateRequest) { r.Roles = []string{"Reader", "Editor"} }), want: "heldElsewhere = false"},
		{name: "a failed request part needs no row", source: "heldElsewhere = false AND new.level <= 1", request: &base, want: "FALSE"},
		{name: "a condition that reads no request field is returned as it is", source: "heldElsewhere = false", request: &base, want: "heldElsewhere = false"},
		{name: "a name the method does not declare", source: "new.unnamed = 'x'", request: &base, wantErr: "does not declare as a request field"},
		{name: "a declared name with no field behind it", source: "new.gone = 'x'", request: &base, wantErr: "is not a field of"},
		{name: "two list fields in one condition", source: "new.eachRole != 'Administrator' AND new.eachLevel <= 3", request: with(func(r *rateRequest) { r.Roles, r.Levels = []string{"Reader"}, []int64{1} }), wantErr: "reads two list fields"},
	}
	collection := rateCollection(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr, err := condition.Parse(tt.source)
			if err != nil {
				t.Fatalf("condition.Parse(%q) error = %v", tt.source, err)
			}
			got, err := settleRequestFields(collection, "RateThing", expr, tt.request)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("settleRequestFields() error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("settleRequestFields() error = %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("settleRequestFields() = %s, want %s", got.String(), tt.want)
			}
		})
	}
}

// TestSettleRequestFields_noCollection pins that a decoder with no collection fails a
// condition that reads a request field, and passes one that reads none through.
func TestSettleRequestFields_noCollection(t *testing.T) {
	t.Parallel()

	reads, err := condition.Parse("new.role != 'Administrator'")
	if err != nil {
		t.Fatalf("condition.Parse() error = %v", err)
	}
	if _, err := settleRequestFields(nil, "RateThing", reads, &rateRequest{}); err == nil || !strings.Contains(err.Error(), "holds no collection") {
		t.Errorf("settleRequestFields() error = %v, want one that names the missing collection", err)
	}

	rowOnly, err := condition.Parse("heldElsewhere = false")
	if err != nil {
		t.Fatalf("condition.Parse() error = %v", err)
	}
	got, err := settleRequestFields(nil, "RateThing", rowOnly, &rateRequest{})
	if err != nil {
		t.Fatalf("settleRequestFields() error = %v", err)
	}
	if got.String() != "heldElsewhere = false" {
		t.Errorf("settleRequestFields() = %s, want the condition as it was", got.String())
	}
}

// TestStorageValue pins the value a condition compares for each type a decoded request
// field may have.
func TestStorageValue(t *testing.T) {
	t.Parallel()

	text := "abc"
	var none *string
	tests := []struct {
		name    string
		value   any
		want    any
		wantErr string
	}{
		{name: "a string", value: "abc", want: "abc"},
		{name: "a pointer to a string", value: &text, want: "abc"},
		{name: "a nil pointer is no value", value: none, want: nil},
		{name: "an int", value: int32(-4), want: int64(-4)},
		{name: "an unsigned int", value: uint16(9), want: int64(9)},
		{name: "an unsigned int beyond a signed 64-bit integer", value: uint64(math.MaxInt64) + 1, wantErr: "beyond what a condition compares"},
		{name: "a float", value: float32(0.5), want: 0.5},
		{name: "a bool", value: true, want: true},
		{name: "an instant is its UTC instant", value: time.Date(2027, 3, 1, 12, 0, 0, 0, time.FixedZone("MST", -7*3600)), want: time.Date(2027, 3, 1, 19, 0, 0, 0, time.UTC)},
		{name: "a date", value: civil.Date{Year: 2027, Month: time.March, Day: 1}, want: condition.Date{Year: 2027, Month: time.March, Day: 1}},
		{name: "an id is its text", value: impulse.UUID{}, want: impulse.UUID{}.String()},
		{name: "a type with no comparison type", value: struct{ A int }{A: 1}, wantErr: "has no comparison type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := storageValue(reflect.ValueOf(tt.value))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("storageValue() error = %v, want one containing %q", err, tt.wantErr)
				}

				return
			}
			if err != nil {
				t.Fatalf("storageValue() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("storageValue() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
