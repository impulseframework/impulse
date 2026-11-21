package resource

import (
	"reflect"
	"testing"

	"github.com/impulseframework/impulse/generation/testdata/sortfixture"
)

// Test_sortableType_fixture holds the query decoder's rule for which fields a list is
// ordered by to the sort fixture, field by field. The generator's test holds its own
// rule to the same fixture: the generated metadata tells a table which columns take a
// sort, so a field the decoder refuses here is one the metadata marks sortable: false.
func Test_sortableType_fixture(t *testing.T) {
	t.Parallel()

	shapes := reflect.TypeFor[sortfixture.Shapes]()
	if shapes.NumField() == 0 {
		t.Fatal("the sort fixture's Shapes has no fields")
	}
	for i := range shapes.NumField() {
		field := shapes.Field(i)
		t.Run(field.Name, func(t *testing.T) {
			t.Parallel()

			tag := field.Tag.Get("orders")
			if tag != "yes" && tag != "no" {
				t.Fatalf("field %s carries orders:%q, want yes or no", field.Name, tag)
			}
			if got, want := sortableType(field.Type), tag == "yes"; got != want {
				t.Errorf("sortableType(%s) = %t, want %t", field.Type, got, want)
			}
		})
	}
}
