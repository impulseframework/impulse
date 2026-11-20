package generation

import (
	"go/format"
	"slices"
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

// Test_subjectSetTestsAndReadThroughs pins the two other roads a row reaches a person
// by, as the generator reads them off the declarations: the columns a who: part looks
// up in another subject set, with the set, and the columns of a record that a dotted
// value of another record's set is read through, with that set.
func Test_subjectSetTestsAndReadThroughs(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)

	var run []*resourceInfo
	byName := make(map[string]*resourceInfo)
	for _, name := range []string{"Seat", "TeamMember", "Assignment", "Stake", "Delegation", "Project"} {
		res, err := resolveFixtureBindings(t, c, structs, name)
		if err != nil {
			t.Fatalf("resolveBindingAnnotations(%s) error = %v", name, err)
		}
		run = append(run, res)
		byName[name] = res
	}
	through := subjectReadThroughs(run, func(record *resourceInfo) string {
		return c.pluralize(record.Name())
	})
	for _, res := range run {
		res.subjectReadThroughs = through[res]
	}

	tests := []struct {
		structName  string
		wantSets    []string
		wantThrough []string
		wantGuarded bool
	}{
		{structName: "Seat", wantGuarded: true},
		{structName: "TeamMember", wantGuarded: true},
		{structName: "Assignment", wantSets: []string{"TeamID in teams"}, wantGuarded: true},
		{structName: "Stake", wantSets: []string{"TeamID in teams"}, wantGuarded: true},
		{structName: "Delegation", wantSets: []string{"ProjectID in leads"}, wantGuarded: true},
		{structName: "Project", wantThrough: []string{"PortfolioID for portfolios"}, wantGuarded: true},
	}
	for _, tt := range tests {
		t.Run(tt.structName, func(t *testing.T) {
			t.Parallel()

			res := byName[tt.structName]
			var sets, facts []string
			for _, test := range res.SubjectSetTests() {
				sets = append(sets, test.Field.Name()+" in "+test.Set)
			}
			for _, column := range res.SubjectReadThroughs() {
				facts = append(facts, column.Field.Name()+" for "+column.Fact)
			}
			if diff := cmp.Diff(tt.wantSets, sets); diff != "" {
				t.Errorf("SubjectSetTests() of %s mismatch (-want +got):\n%s", tt.structName, diff)
			}
			if diff := cmp.Diff(tt.wantThrough, facts); diff != "" {
				t.Errorf("SubjectReadThroughs() of %s mismatch (-want +got):\n%s", tt.structName, diff)
			}
			if got := res.SubjectGuarded(); got != tt.wantGuarded {
				t.Errorf("SubjectGuarded() of %s = %t, want %t", tt.structName, got, tt.wantGuarded)
			}
		})
	}

	bare, err := resolveFixtureBindings(t, c, structs, "Project")
	if err != nil {
		t.Fatalf("resolveBindingAnnotations(Project) error = %v", err)
	}
	if bare.SubjectGuarded() {
		t.Error("SubjectGuarded() of a record that nothing is read through = true, want false")
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
// rows name a set of people asks identity.RefuseImpersonatorAsSetSubject beside it,
// once for each column and set, and a record a fact about people is read through asks
// identity.RefuseImpersonatorAsReadThroughSubject, once for each column and fact: the
// generated code names the column and the set, and nothing more. A record that decides
// nothing about a person has no such call, and its handlers are as they were.
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
		// wantSets and wantThrough are the arguments, from the record's words on, of
		// each call that asks about a set the row names and about a fact read through
		// the row. readThrough lists the records whose facts are resolved beside the
		// record, as a run resolves every record's.
		wantSets    []string
		wantThrough []string
		readThrough []string
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
			wantSets: []string{`"assignment", "TeamID", "teams")`},
		},
		{
			name:       "a record whose who: follows a set alone asks about the set, and about no person",
			structName: "Delegation",
			wantKey: []string{
				"if op.Type != resource.OperationCreate {",
				`subjectKey = subjectKey.Add("ID", httpio.Param[impulse.UUID](%s, "id"))`,
			},
			wantSets: []string{`"delegation", "ProjectID", "leads")`},
		},
		{
			name:       "a record a set of another record is read through asks about the column it is read from",
			structName: "Project",
			wantKey: []string{
				"if op.Type != resource.OperationCreate {",
				`subjectKey = subjectKey.Add("ID", httpio.Param[impulse.UUID](%s, "id"))`,
			},
			wantThrough: []string{`"project", "PortfolioID", "portfolios")`},
			readThrough: []string{"Stake"},
		},
		{name: "a record with no subject value and no subject set, read through by nothing", structName: "Project"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := resolveFixtureBindings(t, c, structs, tt.structName)
			if err != nil {
				t.Fatalf("resolveBindingAnnotations(%s) error = %v", tt.structName, err)
			}
			res.IsConsolidated = true
			if len(tt.readThrough) > 0 {
				run := []*resourceInfo{res}
				for _, name := range tt.readThrough {
					other, err := resolveFixtureBindings(t, c, structs, name)
					if err != nil {
						t.Fatalf("resolveBindingAnnotations(%s) error = %v", name, err)
					}
					run = append(run, other)
				}
				res.subjectReadThroughs = subjectReadThroughs(run, func(record *resourceInfo) string {
					return c.pluralize(record.Name())
				})[res]
			}

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
				if tt.wantCall == "" && len(tt.wantSets) == 0 && len(tt.wantThrough) == 0 {
					for _, notWant := range []string{"identity.RefuseImpersonator", "subjectKey"} {
						if strings.Contains(handler.out, notWant) {
							t.Errorf("%s of %s carries %q, and the record decides nothing about a person:\n%s", handler.name, tt.structName, notWant, handler.out)
						}
					}

					continue
				}

				wantRow := 0
				if tt.wantCall != "" {
					wantRow = 1
				}
				if got := strings.Count(handler.out, guard); got != wantRow {
					t.Fatalf("%s of %s asks the rule about the person a row names %d times, want %d, ahead of the three arms:\n%s", handler.name, tt.structName, got, wantRow, handler.out)
				}
				for call, wants := range map[string][]string{
					"identity.RefuseImpersonatorAsSetSubject(ctx, txn, op.Type, subjectKey, patchSet, ":         tt.wantSets,
					"identity.RefuseImpersonatorAsReadThroughSubject(ctx, txn, op.Type, subjectKey, patchSet, ": tt.wantThrough,
				} {
					if got := strings.Count(handler.out, call); got != len(wants) {
						t.Fatalf("%s of %s makes %d calls of %q, want %d:\n%s", handler.name, tt.structName, got, call, len(wants), handler.out)
					}
					for _, want := range wants {
						if got := strings.Count(handler.out, call+want); got != 1 {
							t.Errorf("%s of %s makes the call %q %d times, want once:\n%s", handler.name, tt.structName, call+want, got, handler.out)
						}
					}
				}
				decode, asked := strings.Index(handler.out, ".DecodeOperation(op, "), strings.Index(handler.out, "identity.RefuseImpersonatorAs")
				create := strings.Index(handler.out, "case resource.OperationCreate:")
				if last := strings.LastIndex(handler.out, "identity.RefuseImpersonatorAs"); last > create {
					t.Errorf("%s of %s asks its last question at %d, after the first arm (%d):\n%s", handler.name, tt.structName, last, create, handler.out)
				}
				if decode < 0 || create < 0 || asked < decode || asked > create {
					t.Errorf("%s of %s asks the rule at %d, want after the operation is decoded (%d) and before the first arm (%d):\n%s",
						handler.name, tt.structName, asked, decode, create, handler.out)
				}
				for _, arm := range []string{"case resource.OperationUpdate:", "case resource.OperationDelete:"} {
					if at := strings.LastIndex(handler.out, arm); at < asked {
						t.Errorf("%s of %s: the arm %q is at %d, before the rule is asked (%d)", handler.name, tt.structName, arm, at, asked)
					}
				}

				wants := []string{"subjectKey := resource.KeySet{}"}
				if tt.wantCall != "" {
					wants = append(wants, guard+tt.wantCall)
				}
				for _, line := range tt.wantKey {
					wants = append(wants, strings.ReplaceAll(line, "%s", handler.req))
				}
				for _, want := range wants {
					if !strings.Contains(handler.out, want) {
						t.Errorf("%s of %s is missing %q:\n%s", handler.name, tt.structName, want, handler.out)
					}
				}
				if !slices.Contains(tt.wantKey, "if op.Type != resource.OperationCreate {") && strings.Contains(handler.out, "if op.Type != resource.OperationCreate {") {
					t.Errorf("%s of %s leaves the key out of a create, and the record's key is in every operation's path:\n%s", handler.name, tt.structName, handler.out)
				}
			}
		})
	}
}
