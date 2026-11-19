package generation

import (
	"go/format"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// Test_subjectUserFields pins which columns of a record name the person its rows speak
// for: the annotated column of a subject value or a subject set, each column a who:
// part compares to the requester, and never a column a who: part looks up in another
// subject set, which names a set and no person.
func Test_subjectUserFields(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)

	tests := []struct {
		name       string
		structName string
		want       []string
		wantWords  string
	}{
		{name: "a subject value names its person by the annotated column", structName: "Seat", want: []string{"UserID"}, wantWords: "seat"},
		{name: "a subject set names its person by the annotated column", structName: "TeamMember", want: []string{"UserID"}, wantWords: "team member"},
		{name: "a who: of a person and a set names the person's column alone, once for two sets", structName: "Assignment", want: []string{"UserID"}, wantWords: "assignment"},
		{name: "a dotted value names the person by the row's own column", structName: "Stake", want: []string{"UserID"}, wantWords: "stake"},
		{name: "a who: that follows a set alone names no person", structName: "Delegation", wantWords: "delegation"},
		{name: "a record with no subject value and no subject set names no person", structName: "Project", wantWords: "project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := resolveFixtureBindings(t, c, structs, tt.structName)
			if err != nil {
				t.Fatalf("resolveBindingAnnotations(%s) error = %v", tt.structName, err)
			}
			var got []string
			for _, field := range res.SubjectUserFields() {
				got = append(got, field.Name())
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("SubjectUserFields() of %s mismatch (-want +got):\n%s", tt.structName, diff)
			}
			if got := res.SubjectRecordWords(); got != tt.wantWords {
				t.Errorf("SubjectRecordWords() of %s = %q, want %q", tt.structName, got, tt.wantWords)
			}
		})
	}
}

// Test_subjectGuard_generatedWrites pins that the generated write of a record that
// decides what a person may do asks the rule that nobody is the subject of what they do
// while they act as someone else: both write handlers, the record's own patch route and
// its arm of the consolidated patch, call identity.RefuseImpersonatorAsRowSubject once
// the operation is decoded and before the create, the update or the delete is built, so
// no arm is reached around it. The call carries the two readings: the key the
// operation's path names, which the rule reads the stored row by (the person the row
// names before the write), and the decoded fields, which say whom the write names (the
// person after it). A create under a generated id has no key in its path. A record whose
// rows name no person has no such call, and its handlers are as they were.
func Test_subjectGuard_generatedWrites(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)
	const guard = "identity.RefuseImpersonatorAsRowSubject(ctx, txn, "

	tests := []struct {
		name       string
		structName string
		// wantKey are the lines that build the key, with %s where the handler names
		// the request that carries the path, and wantCall is the call's arguments
		// from the operation's type on; both empty for a record that has no guard.
		wantKey  []string
		wantCall string
	}{
		{
			name:       "a subject value keyed by its person",
			structName: "Seat",
			wantKey:    []string{`subjectKey = subjectKey.Add("UserID", httpio.Param[string](%s, "id"))`},
			wantCall:   `op.Type, subjectKey, patchSet, "seat", "UserID")`,
		},
		{
			name:       "a subject set under a compound key",
			structName: "TeamMember",
			wantKey: []string{
				`subjectKey = subjectKey.Add("TeamID", httpio.Param[impulse.UUID](%s, "id1"))`,
				`subjectKey = subjectKey.Add("UserID", httpio.Param[string](%s, "id2"))`,
			},
			wantCall: `op.Type, subjectKey, patchSet, "team member", "UserID")`,
		},
		{
			name:       "two subject sets under a generated id, whose create has no key",
			structName: "Assignment",
			wantKey: []string{
				"if op.Type != resource.OperationCreate {",
				`subjectKey = subjectKey.Add("ID", httpio.Param[impulse.UUID](%s, "id"))`,
			},
			wantCall: `op.Type, subjectKey, patchSet, "assignment", "UserID")`,
		},
		{name: "a record whose who: follows a set alone", structName: "Delegation"},
		{name: "a record with no subject value and no subject set", structName: "Project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := resolveFixtureBindings(t, c, structs, tt.structName)
			if err != nil {
				t.Fatalf("resolveBindingAnnotations(%s) error = %v", tt.structName, err)
			}
			res.IsConsolidated = true

			r := &resourceGenerator{client: &client{}, applicationName: "TestApp", receiverName: "a"}
			own, err := r.handlerContent(PatchHandler, res)
			if err != nil {
				t.Fatalf("handlerContent() error = %v", err)
			}

			arm := consolidatedCaseData{resourceInfo: res, ResourcePackage: "resources", ReceiverName: "a"}
			data := consolidatedPatchData{
				Resources:       []*resourceInfo{res},
				Package:         "app",
				ResourcePackage: "resources",
				ApplicationName: "App",
				ReceiverName:    "a",
				HandlerName:     "PatchResources",
			}
			if res.PermissionScope == accesstypes.DomainPermissionScope {
				arm.DomainPatternPrefix = "/tenants/{tenantID}"
				data.DomainCases, data.DomainRouteSegment, data.DomainPatternPrefix = []consolidatedCaseData{arm}, "tenants", "/tenants/{tenantID}"
			} else {
				data.GlobalCases = []consolidatedCaseData{arm}
			}
			consolidated, err := (&client{}).generateTemplateOutput("consolidatedPatchTemplate", consolidatedPatchTemplate, data)
			if err != nil {
				t.Fatalf("generateTemplateOutput() error = %v", err)
			}
			if _, err := format.Source(consolidated); err != nil {
				t.Fatalf("the rendered dispatcher does not parse: %v\n%s", err, consolidated)
			}

			handlers := []struct {
				name string
				out  string
				// req is the request that carries the row's key in the handler.
				req string
			}{
				{name: "the record's own patch route", out: string(own), req: "op.Req"},
				{name: "the consolidated patch", out: string(consolidated), req: "req"},
			}
			for _, handler := range handlers {
				if tt.wantCall == "" {
					for _, notWant := range []string{"RefuseImpersonatorAsRowSubject", "subjectKey"} {
						if strings.Contains(handler.out, notWant) {
							t.Errorf("%s of %s carries %q, and the record's rows name no person:\n%s", handler.name, tt.structName, notWant, handler.out)
						}
					}

					continue
				}

				if got := strings.Count(handler.out, guard); got != 1 {
					t.Fatalf("%s of %s asks the rule %d times, want once, ahead of the three arms:\n%s", handler.name, tt.structName, got, handler.out)
				}
				decode, asked := strings.Index(handler.out, ".DecodeOperation(op, "), strings.Index(handler.out, guard)
				create := strings.Index(handler.out, "case resource.OperationCreate:")
				if decode < 0 || create < 0 || asked < decode || asked > create {
					t.Errorf("%s of %s asks the rule at %d, want after the operation is decoded (%d) and before the first arm (%d):\n%s",
						handler.name, tt.structName, asked, decode, create, handler.out)
				}
				for _, arm := range []string{"case resource.OperationUpdate:", "case resource.OperationDelete:"} {
					if at := strings.LastIndex(handler.out, arm); at < asked {
						t.Errorf("%s of %s: the arm %q is at %d, before the rule is asked (%d)", handler.name, tt.structName, arm, at, asked)
					}
				}

				wants := []string{"subjectKey := resource.KeySet{}", guard + tt.wantCall}
				for _, line := range tt.wantKey {
					wants = append(wants, strings.ReplaceAll(line, "%s", handler.req))
				}
				for _, want := range wants {
					if !strings.Contains(handler.out, want) {
						t.Errorf("%s of %s is missing %q:\n%s", handler.name, tt.structName, want, handler.out)
					}
				}
				if tt.structName != "Assignment" && strings.Contains(handler.out, "if op.Type != resource.OperationCreate {") {
					t.Errorf("%s of %s leaves the key out of a create, and the record's key is in every operation's path:\n%s", handler.name, tt.structName, handler.out)
				}
			}
		})
	}
}
