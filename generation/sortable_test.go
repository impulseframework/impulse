package generation

import "testing"

// Test_ordersType holds the generator's rule for which fields the server orders to the
// sort fixture, field by field: the metadata says sortable: false exactly where the tag
// says the server refuses the sort. The resource package's test holds the query
// decoder's rule to the same fixture, so a table draws a sort button on a column only
// where the server answers the sort.
func Test_ordersType(t *testing.T) {
	t.Parallel()

	shapes := fixtureStructs(loadFixture(t, "sortfixture"))["Shapes"]
	if shapes == nil {
		t.Fatal("struct Shapes not found in the sort fixture")
	}
	if len(shapes.Fields()) == 0 {
		t.Fatal("the sort fixture's Shapes has no fields")
	}
	for _, field := range shapes.Fields() {
		t.Run(field.Name(), func(t *testing.T) {
			t.Parallel()

			tag, ok := field.LookupTag("orders")
			if !ok || (tag != "yes" && tag != "no") {
				t.Fatalf("field %s carries orders:%q, want yes or no", field.Name(), tag)
			}
			if got, want := ordersType(field.GoType()), tag == "yes"; got != want {
				t.Errorf("ordersType(%s) = %t, want %t", field.Type(), got, want)
			}
			resourceField := &resourceField{Field: field}
			if got, want := resourceField.Unordered(), tag == "no"; got != want {
				t.Errorf("resourceField.Unordered() of %s = %t, want %t", field.Type(), got, want)
			}
			computedField := &computedField{Field: field}
			if got, want := computedField.Unordered(), tag == "no"; got != want {
				t.Errorf("computedField.Unordered() of %s = %t, want %t", field.Type(), got, want)
			}
		})
	}
}
