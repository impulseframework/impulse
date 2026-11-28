package generation

import (
	"go/format"
	"regexp"
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
// while they act as someone else, and that the questions are written once, in the
// record's own file. The file carries RefuseImpersonatorAs<Name>Subject: one
// identity.RefuseImpersonatorAsRowSubject where the record's rows name a person, one
// identity.RefuseImpersonatorAsSetSubject for each column and set a row names, and one
// identity.RefuseImpersonatorAsReadThroughSubject for each column and fact read through
// the row, each with the write's operation, key and fields; the generated code names
// the column and the set, and nothing more. Both write handlers, the record's own patch
// route and its arm of the consolidated patch, build the key the operation's path names
// (a create under a generated id has none), which the rule reads the stored row by, and
// call the function once, after the operation is decoded and before the create, the
// update or the delete is built, so no arm is reached around it. Neither asks the
// identity library itself. A record that decides nothing about a person has no
// function, no call and no key, and its handlers are as they were.
func Test_subjectGuard_generatedWrites(t *testing.T) {
	t.Parallel()

	structs := fixtureStructs(loadFixture(t, "subjectsetfixture"))
	c := subjectSetFixtureClient(t, structs)
	identityCall := regexp.MustCompile(`identity\.RefuseImpersonatorAs\w+\(`)
	const (
		rowCall     = "identity.RefuseImpersonatorAsRowSubject(ctx, txn, op, key, patchSet, "
		setCall     = "identity.RefuseImpersonatorAsSetSubject(ctx, txn, op, key, patchSet, "
		throughCall = "identity.RefuseImpersonatorAsReadThroughSubject(ctx, txn, op, key, patchSet, "
	)

	tests := []struct {
		name       string
		structName string
		// wantKey are the lines that build the key, with %s where the handler names
		// the request that carries the path; empty for a record that has no guard.
		wantKey []string
		// wantRow, wantSets and wantThrough are the arguments, from the record's words
		// on, of the function's call that asks about the person a row names, and of
		// each call that asks about a set the row names and about a fact read through
		// the row. readThrough lists the records whose facts are resolved beside the
		// record, as a run resolves every record's.
		wantRow     string
		wantSets    []string
		wantThrough []string
		readThrough []string
	}{
		{
			name:       "a subject value keyed by its person",
			structName: "Seat",
			wantKey:    []string{`subjectKey = subjectKey.Add("UserID", httpio.Param[string](%s, "id"))`},
			wantRow:    `"seat", "UserID")`,
		},
		{
			name:       "a subject set under a compound key",
			structName: "TeamMember",
			wantKey: []string{
				`subjectKey = subjectKey.Add("TeamID", httpio.Param[impulse.UUID](%s, "id1"))`,
				`subjectKey = subjectKey.Add("UserID", httpio.Param[string](%s, "id2"))`,
			},
			wantRow: `"team member", "UserID")`,
		},
		{
			name:       "two subject sets under a generated id, whose create has no key",
			structName: "Assignment",
			wantKey: []string{
				"if op.Type != resource.OperationCreate {",
				`subjectKey = subjectKey.Add("ID", httpio.Param[impulse.UUID](%s, "id"))`,
			},
			wantRow:  `"assignment", "UserID")`,
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
			guarded := tt.wantRow != "" || len(tt.wantSets) > 0 || len(tt.wantThrough) > 0
			function := "RefuseImpersonatorAs" + tt.structName + "Subject"

			out, err := c.generateTemplateOutput("resourceFile", resourceFileTemplate, &resourceFileData{Source: "fixture", Package: "resources", Resource: res})
			if err != nil {
				t.Fatalf("generateTemplateOutput(resourceFile) error = %v", err)
			}
			formatted, err := format.Source(out)
			if err != nil {
				t.Fatalf("the rendered record file does not parse: %v\n%s", err, out)
			}
			file := string(formatted)
			if !guarded {
				if strings.Contains(file, "RefuseImpersonator") {
					t.Errorf("the file of %s carries a question of the rule, and the record decides nothing about a person:\n%s", tt.structName, file)
				}
			} else {
				signature := "func " + function + "(ctx context.Context, txn resource.ReadWriteTransaction, op resource.OperationType, key resource.KeySet, patchSet *resource.PatchSet[" + tt.structName + "]) error {"
				if got := strings.Count(file, signature); got != 1 {
					t.Fatalf("the file of %s carries %q %d times, want once:\n%s", tt.structName, signature, got, file)
				}
				start := strings.Index(file, signature)
				body := file[start : start+strings.Index(file[start:], "\n}\n")]
				wantRow := []string{}
				if tt.wantRow != "" {
					wantRow = []string{tt.wantRow}
				}
				for _, calls := range []struct {
					call  string
					wants []string
				}{
					{call: rowCall, wants: wantRow},
					{call: setCall, wants: tt.wantSets},
					{call: throughCall, wants: tt.wantThrough},
				} {
					if got := strings.Count(file, calls.call); got != len(calls.wants) || strings.Count(body, calls.call) != got {
						t.Fatalf("the file of %s makes %d calls of %q, %d of them in %s, want %d, all in it:\n%s", tt.structName, got, calls.call, strings.Count(body, calls.call), function, len(calls.wants), file)
					}
					for _, want := range calls.wants {
						if got := strings.Count(body, calls.call+want); got != 1 {
							t.Errorf("%s makes the call %q %d times, want once:\n%s", function, calls.call+want, got, body)
						}
					}
				}
			}

			r := &resourceGenerator{client: &client{resource: "app/resources"}, applicationName: "TestApp", receiverName: "a"}
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
			call := "resources." + function + "(ctx, txn, op.Type, subjectKey, patchSet)"
			for _, handler := range handlers {
				if !guarded {
					for _, notWant := range []string{"RefuseImpersonator", "subjectKey"} {
						if strings.Contains(handler.out, notWant) {
							t.Errorf("%s of %s carries %q, and the record decides nothing about a person:\n%s", handler.name, tt.structName, notWant, handler.out)
						}
					}

					continue
				}

				if got := strings.Count(handler.out, call); got != 1 {
					t.Fatalf("%s of %s makes the call %q %d times, want once, ahead of the three arms:\n%s", handler.name, tt.structName, call, got, handler.out)
				}
				if identityCall.MatchString(handler.out) {
					t.Errorf("%s of %s asks the identity library itself, and the record's function asks it:\n%s", handler.name, tt.structName, handler.out)
				}
				decode, asked := strings.Index(handler.out, ".DecodeOperation(op, "), strings.Index(handler.out, call)
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

				wants := []string{"subjectKey := resource.KeySet{}"}
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

// Test_resourceInfo_SubjectFunction pins which records' files carry the function their
// questions are written in: every record that decides what a person may do, but for a
// generated management's record with no generated write, which nothing would call.
func Test_resourceInfo_SubjectFunction(t *testing.T) {
	t.Parallel()

	guarded, noWrite := []subjectReadThrough{{Fact: "teams"}}, []HandlerType{PatchHandler}
	tests := []struct {
		name string
		res  *resourceInfo
		want bool
	}{
		{name: "an application's record", res: &resourceInfo{subjectReadThroughs: guarded}, want: true},
		{name: "an application's record with no generated write", res: &resourceInfo{subjectReadThroughs: guarded, SuppressedHandlers: noWrite}, want: true},
		{name: "a management's record with a generated write", res: &resourceInfo{subjectReadThroughs: guarded, identity: &identityInstance{}}, want: true},
		{name: "a management's record with no generated write", res: &resourceInfo{subjectReadThroughs: guarded, identity: &identityInstance{}, SuppressedHandlers: noWrite}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.res.SubjectFunction(); got != tt.want {
				t.Errorf("resourceInfo.SubjectFunction() = %v, want %v", got, tt.want)
			}
		})
	}
}
