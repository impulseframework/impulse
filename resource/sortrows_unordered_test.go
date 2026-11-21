package resource

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
)

// wordsRow is a row with a field that shows its column in other words than the column
// holds: a mask read as words.
type wordsRow struct {
	ID   impulse.UUID
	Mask accesstypes.NullMaskWords
}

// Resource implements Resourcer.
func (wordsRow) Resource() accesstypes.Resource {
	return "wordsRows"
}

// DefaultConfig implements Resourcer.
func (wordsRow) DefaultConfig() Config {
	return Config{}
}

type wordsRequest struct {
	ID   impulse.UUID              `json:"id"   perm:"-"`
	Mask accesstypes.NullMaskWords `json:"mask"`
}

// TestQueryDecoder_refusesAnUnorderedField pins the refusal of a sort on a field whose
// value is not its column's (Unordered): the database would order the rows by the
// stored text and a page's cursor would be built from the words, so a page would skip
// or repeat rows. The type and a pointer to it are both no order, and the refusal says
// why.
func TestQueryDecoder_refusesAnUnorderedField(t *testing.T) {
	t.Parallel()

	for _, typ := range []reflect.Type{reflect.TypeFor[accesstypes.NullMaskWords](), reflect.TypeFor[*accesstypes.NullMaskWords]()} {
		if sortableType(typ) {
			t.Errorf("sortableType(%s) = true, want false: its value is not its column's", typ)
		}
	}
	if !sortableType(reflect.TypeFor[string]()) {
		t.Error("sortableType(string) = false, want true")
	}

	resSet, err := NewSet[wordsRow, wordsRequest](accesstypes.List)
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := NewQueryDecoder[wordsRow, wordsRequest](resSet)
	if err != nil {
		t.Fatal(err)
	}
	_, err = decoder.DecodeWithoutPermissions(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?sort=mask", http.NoBody))
	if err == nil || !strings.Contains(err.Error(), "field mask cannot be sorted by: it shows its column in other words than the column holds") {
		t.Errorf("DecodeWithoutPermissions(sort=mask) error = %v, want the refusal naming the field and why", err)
	}
}
