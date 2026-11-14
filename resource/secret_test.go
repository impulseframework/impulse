package resource

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// sealedRow keeps change records and holds a secret: the resource struct declares it with
// the word the generator reads, conditions:"secret".
type sealedRow struct {
	ID         string  `spanner:"Id"`
	Title      string  `spanner:"Title"`
	AccessCode *string `spanner:"AccessCode" conditions:"secret"`
}

func (sealedRow) Resource() accesstypes.Resource {
	return "SealedRows"
}

func (sealedRow) Config() Config {
	return Config{}.SetTrackChanges(true)
}

func (sealedRow) DefaultConfig() Config {
	return Config{}
}

// A change set names a secret field that changed and keeps neither of its values, in
// the diff of an update and in the record of a delete; a secret set to the value it
// holds is no change, as for any field.
func TestPatchSet_Diff_keepsNoValueOfASecret(t *testing.T) {
	t.Parallel()

	before, after := "open-sesame", "close-sesame"
	stored := &sealedRow{ID: "row-1", Title: "before", AccessCode: &before}

	p := NewPatchSet(NewMetadata[sealedRow]()).SetKey("ID", "row-1")
	p.Set("Title", "after")
	p.Set("AccessCode", &after)
	got, err := p.Diff(stored)
	if err != nil {
		t.Fatalf("Diff() error = %v", err)
	}
	want := map[accesstypes.Field]DiffElem{
		"Title":      {Old: "before", New: "after"},
		"AccessCode": {Secret: true},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Diff() mismatch (-want +got):\n%s", diff)
	}

	// As the change record stores it: the field's name, and neither value.
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, value := range []string{before, after} {
		if strings.Contains(string(encoded), value) {
			t.Errorf("the change set carries the secret %q: %s", value, encoded)
		}
	}
	if !strings.Contains(string(encoded), `"AccessCode":{"Old":null,"New":null,"Secret":true}`) {
		t.Errorf("the change set does not name the secret as changed: %s", encoded)
	}
	// A field that is no secret keeps the shape it had.
	if !strings.Contains(string(encoded), `"Title":{"Old":"before","New":"after"}`) {
		t.Errorf("a field that is no secret changed shape: %s", encoded)
	}

	// The same secret again is no change.
	same := before
	unchanged := NewPatchSet(NewMetadata[sealedRow]()).SetKey("ID", "row-1")
	unchanged.Set("AccessCode", &same)
	got, err = unchanged.Diff(stored)
	if err != nil {
		t.Fatalf("Diff() of the same secret: error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Diff() of the same secret = %v, want no change", got)
	}

	// A delete records every field the row held; the secret, by name alone.
	deleted, err := p.deleteChangeSet(stored)
	if err != nil {
		t.Fatalf("deleteChangeSet() error = %v", err)
	}
	wantDeleted := map[accesstypes.Field]DiffElem{
		"ID":         {Old: "row-1"},
		"Title":      {Old: "before"},
		"AccessCode": {Secret: true},
	}
	if diff := cmp.Diff(wantDeleted, deleted); diff != "" {
		t.Errorf("deleteChangeSet() mismatch (-want +got):\n%s", diff)
	}
}

// A resource with no secret reads as it always did.
func TestMetadata_secret(t *testing.T) {
	t.Parallel()

	sealed := NewMetadata[sealedRow]()
	if !sealed.secret("AccessCode") || sealed.secret("Title") || sealed.secret("ID") {
		t.Errorf("sealedRow's secrets = %v, want AccessCode alone", sealed.secrets)
	}
	if plain := NewMetadata[trackedForkRow](); len(plain.secrets) != 0 {
		t.Errorf("trackedForkRow's secrets = %v, want none", plain.secrets)
	}
}
