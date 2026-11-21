package resource

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
)

// hangarPass is a resource whose name is two words, with a field of one word and a field
// of two, so a refusal's words show how each is spelled.
type hangarPass struct {
	ID      string `spanner:"Id"`
	Budget  int64  `spanner:"Budget"`
	DueDate string `spanner:"DueDate"`
	Notes   string `spanner:"Notes"`
}

func (hangarPass) Resource() accesstypes.Resource {
	return "HangarPasses"
}

func (hangarPass) DefaultConfig() Config {
	return Config{}
}

type hangarPassRequest struct {
	ID      string `json:"id"      perm:"-"`
	Budget  int64  `json:"budget"`
	DueDate string `json:"dueDate"`
	Notes   string `json:"notes"`
}

// refusedPass arms a write of a hangar pass that sets fields, for a caller who holds perm
// on the resources granted and on nothing else, and answers what the caller is refused
// with.
func refusedPass(t *testing.T, perm accesstypes.Permission, granted []accesstypes.Resource, fields ...accesstypes.Field) error {
	t.Helper()

	set, err := NewSet[hangarPass, hangarPassRequest](accesstypes.Create, accesstypes.Update)
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}
	perms := &fakeUserPermissions{granted: map[accesstypes.Permission][]accesstypes.Resource{perm: granted}}
	p := NewPatchSet(NewMetadata[hangarPass]()).SetKey("ID", "one")
	for _, field := range fields {
		p.Set(field, "a value")
	}
	p.SetPatchType(UpdatePatchType).EnableUserPermissionEnforcement(set, perms, testScope, perm)

	return p.checkPermissions(t.Context(), SpannerDBType)
}

// A caller who may not do what was asked is told so in one sentence: the permission by
// its verb, the resource by its name in words, a field denied alone by its own name in
// words. The sentence carries no id, no code name and no table name; the error's text,
// which the server's log prints, carries the scope, the person, the permission and the
// resource.
func TestRefusedPermission_plainWords(t *testing.T) {
	t.Parallel()

	const passes = accesstypes.Resource("HangarPasses")
	onThePass := []accesstypes.Resource{passes}

	tests := []struct {
		name    string
		perm    accesstypes.Permission
		granted []accesstypes.Resource
		fields  []accesstypes.Field
		said    string
		kept    []string
	}{
		{name: "create is to add", perm: accesstypes.Create, said: "You may not add hangar passes here."},
		{name: "read is to see", perm: accesstypes.Read, said: "You may not see hangar passes here."},
		{name: "update is to change", perm: accesstypes.Update, said: "You may not change hangar passes here."},
		{name: "delete is to delete", perm: accesstypes.Delete, said: "You may not delete hangar passes here."},
		{name: "list is to list", perm: accesstypes.List, said: "You may not list hangar passes here."},
		{name: "a permission with no plain verb names nothing", perm: "Countersign", said: "You may not do this here."},
		{
			name: "a field denied alone is named", perm: accesstypes.Update, granted: onThePass, fields: []accesstypes.Field{"Budget"},
			said: "You may not change the budget of hangar passes here.", kept: []string{"HangarPasses.budget"},
		},
		{
			name: "a field's name of two words is two words", perm: accesstypes.Create, granted: onThePass, fields: []accesstypes.Field{"DueDate"},
			said: "You may not add the due date of hangar passes here.", kept: []string{"HangarPasses.dueDate"},
		},
		{
			name: "two fields are either one", perm: accesstypes.Update, granted: onThePass, fields: []accesstypes.Field{"Budget", "DueDate"},
			said: "You may not change the budget or the due date of hangar passes here.", kept: []string{"HangarPasses.budget", "HangarPasses.dueDate"},
		},
		{
			name: "three fields are a list", perm: accesstypes.Update, granted: onThePass, fields: []accesstypes.Field{"Budget", "DueDate", "Notes"},
			said: "You may not change the budget, the due date or the notes of hangar passes here.", kept: []string{"HangarPasses.notes"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := refusedPass(t, tt.perm, tt.granted, tt.fields...)
			kept := append([]string{"testDomain", "testUser", "(" + string(tt.perm) + ")", "HangarPasses"}, tt.kept...)
			plainAnswer(t, err, httpio.HasForbidden, tt.said, kept...)
		})
	}
}

// A method names no thing a person knows by a word, so a caller who may not run one is
// told the sentence that names nothing, and the log keeps the method.
func TestRefusedMethod_plainWords(t *testing.T) {
	t.Parallel()

	const said = "You may not do this here."
	caller := &Caller{Permissions: &fakeUserPermissions{}, Scope: testScope, Env: RequestEnvironment()}

	t.Run("a method the caller does not hold", func(t *testing.T) {
		t.Parallel()

		_, err := caller.ExecuteGate(t.Context(), "CountersignPass", nil)
		plainAnswer(t, err, httpio.HasForbidden, said, "testDomain", "testUser", "Execute", "CountersignPass")
	})
	t.Run("a tenant's read asked under one of several methods", func(t *testing.T) {
		t.Parallel()

		err := holdsOneOf(t.Context(), caller, []accesstypes.Resource{"CountersignPass", "VoidPass"})
		plainAnswer(t, err, httpio.HasForbidden, said, "testDomain", "testUser", "Execute", "CountersignPass", "VoidPass")
	})
}

// A write whose grant holds for some rows and not for the one written says that the
// person may do it to others, in the same words as any refusal.
// TestTargetRefused_plainWords holds the answer to a method refused on the row it names:
// the sentence says the record in words and nothing else, and the method, the record's
// struct name and the row's key stay in the words the server's log prints.
func TestTargetRefused_plainWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		label     string
		key       []any
		wantSaid  string
		wantCause string
	}{
		{name: "a row named by one key value", label: "HangarPass", key: []any{"pass-7"}, wantSaid: "You may not do this to this hangar pass.", wantCause: "CountersignPass may not run against HangarPass pass-7"},
		{name: "a row named by a compound key", label: "HeldRole", key: []any{"ann", "Armorer", 3}, wantSaid: "You may not do this to this held role.", wantCause: "CountersignPass may not run against HeldRole ann, Armorer, 3"},
		{name: "a row the method proposes, which has no key", label: "HeldRole", wantSaid: "You may not do this to this held role.", wantCause: "CountersignPass may not run against this HeldRole"},
		{name: "a record with no name names nothing", label: "", key: []any{"pass-7"}, wantSaid: "You may not do this here.", wantCause: "CountersignPass may not run against  pass-7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := TargetRefused("CountersignPass", tt.label, tt.key...)
			plainAnswer(t, err, httpio.HasForbidden, tt.wantSaid, "CountersignPass", "may not run against", tt.wantCause)
		})
	}
}

func TestRefusedCondition_plainWords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		perm      accesstypes.Permission
		resources []accesstypes.Resource
		said      string
	}{
		{name: "the resource", perm: accesstypes.Update, resources: []accesstypes.Resource{"HangarPasses"}, said: "You may change some hangar passes here, but not this one."},
		{name: "a new row", perm: accesstypes.Create, resources: []accesstypes.Resource{"HangarPasses"}, said: "You may add some hangar passes here, but not this one."},
		{
			name: "a field", perm: accesstypes.Update, resources: []accesstypes.Resource{"HangarPasses.budget"},
			said: "You may change the budget of some hangar passes here, but not of this one.",
		},
		{
			name: "two fields", perm: accesstypes.Update, resources: []accesstypes.Resource{"HangarPasses.budget", "HangarPasses.dueDate"},
			said: "You may change the budget or the due date of some hangar passes here, but not of this one.",
		},
		{name: "a permission with no plain verb", perm: "Countersign", resources: []accesstypes.Resource{"HangarPasses"}, said: "You may not do this here."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			q := &QuerySet[hangarPass]{scope: testScope, userPermissions: &fakeUserPermissions{}, requiredPermission: tt.perm}
			kept := []string{"testDomain", "testUser", "(" + string(tt.perm) + ")"}
			for _, res := range tt.resources {
				kept = append(kept, string(res))
			}
			plainAnswer(t, q.conditionRefused(tt.resources), httpio.HasForbidden, tt.said, kept...)
		})
	}
}

// TestRefusedPermission_theLogKeepsWhoAndWhat serves a refusal as a handler does and
// reads both sides of it: the caller's body says the plain sentence and nothing of the
// scope, the person, the permission's code name or the table, and the line the server
// logs for the request carries all four. It is not parallel: the standard logger, which a
// request with no logger of its own writes through, is the process's own.
func TestRefusedPermission_theLogKeepsWhoAndWhat(t *testing.T) { //nolint:paralleltest // it reads the standard logger's output, which is the whole process's
	var logged bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logged)
	t.Cleanup(func() {
		log.SetOutput(previous)
	})

	handler := httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
		return httpio.NewEncoder(w).ClientMessage(r.Context(), refusedPass(t, accesstypes.Update, []accesstypes.Resource{"HangarPasses"}, "Budget"))
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/hangar-passes/one", http.NoBody))

	if recorder.Code != http.StatusForbidden {
		t.Errorf("the answer's status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
	const said = "You may not change the budget of hangar passes here."
	body := recorder.Body.String()
	if !strings.Contains(body, said) {
		t.Errorf("the caller's body is %q, want it to say %q", body, said)
	}
	kept := []string{"testDomain", "testUser", "(Update)", "HangarPasses.budget"}
	for _, word := range kept {
		if strings.Contains(body, word) {
			t.Errorf("the caller's body is %q, which carries %q", body, word)
		}
		if !strings.Contains(logged.String(), word) {
			t.Errorf("the server's log for the request is %q, want it to carry %q", logged.String(), word)
		}
	}
}
