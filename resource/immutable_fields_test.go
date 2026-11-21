package resource

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

const mooredResource = accesstypes.Resource("mooredResources")

// mooringResource is the resource the immutable-field decoder tests decode into.
type mooringResource struct {
	ID    impulse.UUID `spanner:"Id"`
	Name  string       `spanner:"Name"`
	Berth string       `spanner:"Berth"`
	Hull  string       `spanner:"Hull"`
	Keel  string       `spanner:"Keel"`
}

func (mooringResource) Resource() accesstypes.Resource {
	return mooredResource
}

func (mooringResource) DefaultConfig() Config {
	return Config{}
}

// mooringRequest mirrors a generated patch request struct whose row keeps three of its
// fields for life, declared out of name order, beside one field an update may change.
type mooringRequest struct {
	Name  string `json:"name"`
	Keel  string `json:"keel"  immutable:"true"`
	Hull  string `json:"hull"  immutable:"true"`
	Berth string `json:"berth" immutable:"true"`
}

// TestDecodeToPatch_immutableFields pins the refusal of an update that names a field
// which never changes: one sentence that names every such field of the body, in the
// order of their names, whatever order the body gives them in and however it spells
// them, and before any other refusal of a field. A map is ranged over in a different
// order every time, so each body is decoded many times over: the answer never varies.
func TestDecodeToPatch_immutableFields(t *testing.T) {
	t.Parallel()

	rSet, err := NewSet[mooringResource, mooringRequest](accesstypes.Create, accesstypes.Update)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	mapper, err := NewRequestFieldMapper(new(mooringRequest))
	if err != nil {
		t.Fatalf("NewRequestFieldMapper() error = %v", err)
	}

	tests := []struct {
		name        string
		method      string
		perm        accesstypes.Permission
		body        string
		wantMessage string
		wantFields  []accesstypes.Field
	}{
		{
			name:        "an update naming one immutable field is refused for it",
			method:      http.MethodPatch,
			perm:        accesstypes.Update,
			body:        `{"hull":"H-2"}`,
			wantMessage: "json field hull is immutable",
		},
		{
			name:        "an update naming two is refused for both, in the order of their names",
			method:      http.MethodPatch,
			perm:        accesstypes.Update,
			body:        `{"keel":"K-2","berth":"B-2"}`,
			wantMessage: "json fields berth and keel are immutable",
		},
		{
			name:        "an update naming three is refused for all three",
			method:      http.MethodPatch,
			perm:        accesstypes.Update,
			body:        `{"keel":"K-2","hull":"H-2","berth":"B-2"}`,
			wantMessage: "json fields berth, hull and keel are immutable",
		},
		{
			name:        "a field the update may change beside them changes nothing in the answer",
			method:      http.MethodPatch,
			perm:        accesstypes.Update,
			body:        `{"name":"Anvil","keel":"K-2","hull":"H-2"}`,
			wantMessage: "json fields hull and keel are immutable",
		},
		{
			name:        "the immutable refusal comes before the refusal of a name the request does not have",
			method:      http.MethodPatch,
			perm:        accesstypes.Update,
			body:        `{"anchor":1,"zenith":1,"hull":"H-2","berth":"B-2"}`,
			wantMessage: "json fields berth and hull are immutable",
		},
		{
			name:        "a name in another case reaches the same field and is refused by its declared name",
			method:      http.MethodPatch,
			perm:        accesstypes.Update,
			body:        `{"Hull":"H-2"}`,
			wantMessage: "json field hull is immutable",
		},
		{
			name:        "a name in another case is named once beside the others",
			method:      http.MethodPatch,
			perm:        accesstypes.Update,
			body:        `{"KEEL":"K-2","Berth":"B-2"}`,
			wantMessage: "json fields berth and keel are immutable",
		},
		{
			name:        "two names the request does not have are refused for the first by name",
			method:      http.MethodPatch,
			perm:        accesstypes.Update,
			body:        `{"zenith":1,"anchor":1,"name":"Anvil"}`,
			wantMessage: "invalid field in json - anchor",
		},
		{
			name:       "an update that names none of them is decoded",
			method:     http.MethodPatch,
			perm:       accesstypes.Update,
			body:       `{"name":"Anvil"}`,
			wantFields: []accesstypes.Field{"Name"},
		},
		{
			name:       "a create carries them all",
			method:     http.MethodPost,
			perm:       accesstypes.Create,
			body:       `{"name":"Anvil","keel":"K-1","hull":"H-1","berth":"B-1"}`,
			wantFields: []accesstypes.Field{"Name", "Keel", "Hull", "Berth"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for range 32 {
				r := httptest.NewRequestWithContext(t.Context(), tt.method, "/", strings.NewReader(tt.body))
				patchSet, _, err := decodeToPatch[mooringResource, mooringRequest](rSet, mapper, r, nil, tt.perm, nil)
				if tt.wantMessage != "" {
					if err == nil {
						t.Fatalf("decodeToPatch() error = nil, want message %q", tt.wantMessage)
					}
					if got := httpio.Message(err); got != tt.wantMessage {
						t.Fatalf("httpio.Message() = %q, want %q", got, tt.wantMessage)
					}

					continue
				}
				if err != nil {
					t.Fatalf("decodeToPatch() error = %v", err)
				}
				if diff := cmp.Diff(tt.wantFields, patchSet.Fields()); diff != "" {
					t.Fatalf("patchSet.Fields() mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
