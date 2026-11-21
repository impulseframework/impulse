package accesstypes

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestPermissionMask_Refuses_exceptions(t *testing.T) {
	t.Parallel()

	passes := AllowAll().Refusing(
		RefuseClass(ClassCredential),
		RefuseClassExcept(ClassAccess, append(ExceptWrites("Passes"), ExceptAction("AddGroupMember"), ExceptPermission(Update, "Pilots.callSign"))...),
	)

	tests := []struct {
		name    string
		mask    PermissionMask
		perm    Permission
		res     Resource
		classes []ActionClass
		wantWhy string
	}{
		{name: "an excepted record's create is kept", mask: passes, perm: Create, res: "Passes", classes: []ActionClass{ClassAccess}},
		{name: "an excepted record's update is kept", mask: passes, perm: Update, res: "Passes", classes: []ActionClass{ClassAccess}},
		{name: "an excepted record's delete is kept", mask: passes, perm: Delete, res: "Passes", classes: []ActionClass{ClassAccess}},
		{name: "a field of an excepted record is kept with it", mask: passes, perm: Update, res: "Passes.level", classes: []ActionClass{ClassAccess}},
		{name: "an excepted action is kept", mask: passes, perm: Execute, res: "AddGroupMember", classes: []ActionClass{ClassAccess}},
		{name: "an excepted field is kept", mask: passes, perm: Update, res: "Pilots.callSign", classes: []ActionClass{ClassAccess}},
		{name: "the rest of the class stays refused", mask: passes, perm: Update, res: "Memberships", classes: []ActionClass{ClassAccess}, wantWhy: "the mask refuses the class access"},
		{name: "another action of the class stays refused", mask: passes, perm: Execute, res: "RemoveGroupMember", classes: []ActionClass{ClassAccess}, wantWhy: "the mask refuses the class access"},
		{name: "a record whose exception names a field is refused when no field is named", mask: passes, perm: Update, res: "Pilots", classes: []ActionClass{ClassAccess}, wantWhy: "the mask refuses the class access"},
		{name: "another field of that record stays refused", mask: passes, perm: Update, res: "Pilots.clearance", classes: []ActionClass{ClassAccess}, wantWhy: "the mask refuses the class access"},
		{name: "an exception keeps one permission, not the others", mask: passes, perm: Create, res: "Pilots.callSign", classes: []ActionClass{ClassAccess}, wantWhy: "the mask refuses the class access"},
		{
			name: "another refused class the name carries still refuses it", mask: passes, perm: Execute, res: "AddGroupMember",
			classes: []ActionClass{ClassAccess, ClassCredential}, wantWhy: "the mask refuses the class credential",
		},
		{
			name: "a refusal by name beats the exception",
			mask: passes.Refusing(RefusePermission(Delete, "Passes")), perm: Delete, res: "Passes", classes: []ActionClass{ClassAccess},
			wantWhy: "the mask refuses Delete on Passes",
		},
		{
			name: "a kind the mask does not keep is refused whatever is excepted",
			mask: MaskPermissions(DenyAll(), List, Read).Refusing(RefuseClassExcept(ClassAccess, ExceptWrites("Passes")...)), perm: Create, res: "Passes",
			classes: []ActionClass{ClassAccess}, wantWhy: "the mask does not keep Create",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			why, refused := tt.mask.Refuses(tt.perm, tt.res, tt.classes...)
			if refused != (tt.wantWhy != "") || why != tt.wantWhy {
				t.Errorf("Refuses(%s, %s, %v) = %q, %v, want %q", tt.perm, tt.res, tt.classes, why, refused, tt.wantWhy)
			}
		})
	}
}

func TestPermissionMask_RefusesWrite(t *testing.T) {
	t.Parallel()

	access := []ActionClass{ClassAccess}
	field := AllowAll().Refusing(RefuseClassExcept(ClassAccess, ExceptPermission(Update, "Pilots.callSign"), ExceptPermission(Update, "Pilots.homeBase")))

	tests := []struct {
		name    string
		mask    PermissionMask
		perm    Permission
		fields  []Resource
		wantWhy string
	}{
		{name: "a write of one excepted field is kept", mask: field, perm: Update, fields: []Resource{"Pilots.callSign"}},
		{name: "a write of two excepted fields is kept", mask: field, perm: Update, fields: []Resource{"Pilots.callSign", "Pilots.homeBase"}},
		{
			name: "a write that sets another field is refused", mask: field, perm: Update, fields: []Resource{"Pilots.callSign", "Pilots.clearance"},
			wantWhy: "the mask refuses the class access, and its exceptions do not keep Update on Pilots.clearance",
		},
		{name: "a write that names no field is refused", mask: field, perm: Update, wantWhy: "the mask refuses the class access"},
		{
			name: "a create is not the update the exception keeps", mask: field, perm: Create, fields: []Resource{"Pilots.callSign"},
			wantWhy: "the mask refuses the class access, and its exceptions do not keep Create on Pilots.callSign",
		},
		{
			name: "a refusal of the record by name still refuses", mask: field.Refusing(RefusePermission(Update, "Pilots")), perm: Update,
			fields: []Resource{"Pilots.callSign"}, wantWhy: "the mask refuses Update on Pilots",
		},
		{
			name: "a kind the mask does not keep is refused", mask: MaskPermissions(DenyAll(), Read).Refusing(field.Refusals()...), perm: Update,
			fields: []Resource{"Pilots.callSign"}, wantWhy: "the mask does not keep Update",
		},
		{
			name: "a record the mask excepts whole is kept whatever it sets", mask: AllowAll().Refusing(RefuseClassExcept(ClassAccess, ExceptWrites("Pilots")...)),
			perm: Update, fields: []Resource{"Pilots.clearance"},
		},
		{name: "a mask that refuses no class keeps the write", mask: AllowAll().Refusing(RefuseAction("Launch")), perm: Update, fields: []Resource{"Pilots.clearance"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			why, refused := tt.mask.RefusesWrite(tt.perm, "Pilots", tt.fields, access...)
			if refused != (tt.wantWhy != "") || why != tt.wantWhy {
				t.Errorf("RefusesWrite(%s, Pilots, %v) = %q, %v, want %q", tt.perm, tt.fields, why, refused, tt.wantWhy)
			}
		})
	}

	if got, want := field.ExceptedFields(Update, "Pilots"), []Resource{"Pilots.callSign", "Pilots.homeBase"}; !slices.Equal(got, want) {
		t.Errorf("ExceptedFields(Update, Pilots) = %v, want %v", got, want)
	}
	if got := field.ExceptedFields(Create, "Pilots"); got != nil {
		t.Errorf("ExceptedFields(Create, Pilots) = %v, want none", got)
	}
}

func TestPermissionMask_Refusing_exceptions(t *testing.T) {
	t.Parallel()

	passes := RefuseClassExcept(ClassAccess, ExceptWrites("Passes")...)
	passesAndMember := RefuseClassExcept(ClassAccess, append(ExceptWrites("Passes"), ExceptAction("AddGroupMember"))...)

	tests := []struct {
		name string
		mask PermissionMask
		want string
	}{
		{
			name: "a class refused for the first time takes its exceptions",
			mask: AllowAll().Refusing(passes),
			want: "*;-class:access;+class:access:Create:Passes;+class:access:Delete:Passes;+class:access:Update:Passes",
		},
		{name: "a class refused again with none loses them all", mask: AllowAll().Refusing(passes).Refusing(RefuseClass(ClassAccess)), want: "*;-class:access"},
		{name: "a plain refusal does not gain exceptions later", mask: AllowAll().Refusing(RefuseClass(ClassAccess)).Refusing(passes), want: "*;-class:access"},
		{
			name: "a class refused again keeps what both leave alone",
			mask: AllowAll().Refusing(passesAndMember).Refusing(RefuseClassExcept(ClassAccess, ExceptAction("AddGroupMember"), ExceptAction("InviteUser"))),
			want: "*;-class:access;+class:access:Execute:AddGroupMember",
		},
		{
			name: "a field under a record the other side keeps stays as the field",
			mask: AllowAll().Refusing(passes).Refusing(RefuseClassExcept(ClassAccess, ExceptPermission(Update, "Passes.level"))),
			want: "*;-class:access;+class:access:Update:Passes.level",
		},
		{
			name: "the exceptions of one class are not another's",
			mask: AllowAll().Refusing(passes, RefuseClass(ClassCredential)),
			want: "*;-class:access;+class:access:Create:Passes;+class:access:Delete:Passes;+class:access:Update:Passes;-class:credential",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.mask.Text(); got != tt.want {
				t.Errorf("Text() = %q, want %q", got, tt.want)
			}
			parsed, err := ParseMask(tt.mask.Text())
			if err != nil || !parsed.Equal(tt.mask) {
				t.Errorf("ParseMask(Text()) = %q, %v, want the mask back", parsed.Text(), err)
			}
		})
	}

	base := AllowAll().Refusing(passes)
	_ = base.Refusing(RefuseClass(ClassAccess))
	if got := base.Text(); !strings.Contains(got, "+class:access:Create:Passes") {
		t.Errorf("Refusing() changed its receiver, which now reads %q", got)
	}
	if base.Equal(AllowAll().Refusing(RefuseClass(ClassAccess))) {
		t.Error("Equal() takes a class refused with exceptions for the class refused whole")
	}
}

func TestPermissionMask_Narrow_exceptions(t *testing.T) {
	t.Parallel()

	grant := AllowAll().Refusing(RefuseClass(ClassCredential), RefuseClassExcept(ClassAccess, append(ExceptWrites("Passes"), ExceptAction("AddGroupMember"))...))

	tests := []struct {
		name      string
		request   PermissionMask
		want      string
		wantAdded string
	}{
		{
			name: "no request keeps the grant's exceptions", request: AllowAll(),
			want: "*;-class:access;+class:access:Execute:AddGroupMember;+class:access:Create:Passes;+class:access:Delete:Passes;+class:access:Update:Passes;-class:credential",
		},
		{
			name: "a request that names the same exceptions keeps them", request: AllowAll().Refusing(grant.Refusals()...),
			want: "*;-class:access;+class:access:Execute:AddGroupMember;+class:access:Create:Passes;+class:access:Delete:Passes;+class:access:Update:Passes;-class:credential",
		},
		{
			name: "a request drops one exception", request: AllowAll().Refusing(RefuseClassExcept(ClassAccess, ExceptWrites("Passes")...)),
			want: "*;-class:access;+class:access:Create:Passes;+class:access:Delete:Passes;+class:access:Update:Passes;-class:credential",
		},
		{name: "a request drops them all with a plain refusal", request: AllowAll().Refusing(RefuseClass(ClassAccess)), want: "*;-class:access;-class:credential"},
		{
			name: "a request keeps a field of a record the grant keeps", request: AllowAll().Refusing(RefuseClassExcept(ClassAccess, ExceptPermission(Update, "Passes.level"))),
			want: "*;-class:access;+class:access:Update:Passes.level;-class:credential",
		},
		{
			name: "a request that refuses by name what the grant keeps drops the exception", request: AllowAll().Refusing(RefuseWrites("Passes")...),
			want: "*;-class:access;+class:access:Execute:AddGroupMember;-class:credential;-on:Create:Passes;-on:Delete:Passes;-on:Update:Passes",
		},
		{
			name: "a request that keeps no write drops the exceptions of writes", request: MaskPermissions(DenyAll(), List, Read, Execute),
			want: "Execute,List,Read;-class:access;+class:access:Execute:AddGroupMember;-class:credential",
		},
		{
			name: "a request that adds an exception is refused", request: AllowAll().Refusing(RefuseClassExcept(ClassAccess, append(ExceptWrites("Pilots"), ExceptWrites("Passes")...)...)),
			wantAdded: "writing Pilots",
		},
		{
			name: "a request that adds an exception to a class the grant refuses whole is refused", request: AllowAll().Refusing(RefuseClassExcept(ClassCredential, ExceptAction("SetPassword"))),
			wantAdded: "the action SetPassword",
		},
		{
			name: "a class the grant does not refuse is narrower with any exception", request: AllowAll().Refusing(RefuseClassExcept("irreversible", ExceptAction("ScrapShip"))),
			want: "*;-class:access;+class:access:Execute:AddGroupMember;+class:access:Create:Passes;+class:access:Delete:Passes;+class:access:Update:Passes;-class:credential;-class:irreversible;+class:irreversible:Execute:ScrapShip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			before := grant.Text()
			got, err := grant.Narrow(tt.request)
			if after := grant.Text(); after != before {
				t.Errorf("Narrow() changed the grant's mask from %q to %q", before, after)
			}
			if tt.wantAdded != "" {
				var added *AddedExceptionError
				if !errors.As(err, &added) {
					t.Fatalf("Narrow() = %q, %v, want an *AddedExceptionError", got.Text(), err)
				}
				if added.AddedWords() != tt.wantAdded || !got.IsZero() || !added.Gives.Equal(grant) {
					t.Errorf("Narrow() added %q giving %q with the mask %q, want %q added and nothing given", added.AddedWords(), added.Gives.Text(), got.Text(), tt.wantAdded)
				}

				return
			}
			if err != nil {
				t.Fatalf("Narrow() error = %v", err)
			}
			if got.Text() != tt.want {
				t.Errorf("Narrow() = %q, want %q", got.Text(), tt.want)
			}
		})
	}
}

func TestAddedExceptionError_Error(t *testing.T) {
	t.Parallel()

	grant := AllowAll().Refusing(RefuseClassExcept(ClassAccess, ExceptWrites("Passes")...))
	_, err := grant.Narrow(AllowAll().Refusing(RefuseClassExcept(ClassAccess, ExceptAction("InviteUser"))))
	want := "the request keeps the action InviteUser out of the refusal of access actions, which is more than is given (everything except access actions (still allowed: writing Passes))"
	if err == nil || err.Error() != want {
		t.Errorf("Narrow() error = %v, want %q", err, want)
	}
}

func TestParseMask_exceptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		wantErr string
	}{
		{name: "an exception follows its class", text: "*;-class:access;+class:access:Update:Passes"},
		{name: "an exception may stand before its class", text: "*;+class:access:Update:Passes;-class:access"},
		{name: "an exception of a class the text does not refuse", text: "*;-class:credential;+class:access:Update:Passes", wantErr: `keeps something out of the refusal of the class "access", which it does not refuse`},
		{name: "an exception with no resource", text: "*;-class:access;+class:access:Update", wantErr: "names no class, permission and resource"},
		{name: "an exception with an empty class", text: "*;-class:access;+class::Update:Passes", wantErr: "names no class, permission and resource"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mask, err := ParseMask(tt.text)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseMask(%q) = %q, %v, want an error holding %q", tt.text, mask.Text(), err, tt.wantErr)
				}
				if !mask.IsZero() {
					t.Errorf("ParseMask(%q) returned the mask %q beside its error", tt.text, mask.Text())
				}

				return
			}
			if err != nil {
				t.Fatalf("ParseMask(%q) error = %v", tt.text, err)
			}
			if why, refused := mask.Refuses(Update, "Passes", ClassAccess); refused {
				t.Errorf("the mask read from %q refuses the exception: %s", tt.text, why)
			}
			if _, refused := mask.Refuses(Update, "Pilots", ClassAccess); !refused {
				t.Errorf("the mask read from %q keeps the rest of the class", tt.text)
			}
		})
	}
}

func TestPermissionMask_Words_exceptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mask PermissionMask
		want string
	}{
		{
			name: "a record's writes", mask: AllowAll().Refusing(RefuseClass(ClassCredential), RefuseClassExcept(ClassAccess, ExceptWrites("Passes")...)),
			want: "everything except access actions (still allowed: writing Passes) and credential actions",
		},
		{
			name: "an action, a field and a record", mask: AllowAll().Refusing(RefuseClassExcept(ClassAccess, append(ExceptWrites("Passes"), ExceptAction("AddGroupMember"), ExceptPermission(Update, "Pilots.callSign"))...)),
			want: "everything except access actions (still allowed: the action AddGroupMember, writing Passes and changing Pilots.callSign)",
		},
		{
			name: "two of a record's writes are each said", mask: AllowAll().Refusing(RefuseClassExcept(ClassAccess, ExceptPermission(Create, "Passes"), ExceptPermission(Update, "Passes"))),
			want: "everything except access actions (still allowed: creating Passes and changing Passes)",
		},
		{
			name: "under named kinds", mask: MaskPermissions(DenyAll(), Execute, Update).Refusing(RefuseClassExcept(ClassAccess, ExceptAction("AddGroupMember"))),
			want: "Execute and Update only, except access actions (still allowed: the action AddGroupMember)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.mask.Words(); got != tt.want {
				t.Errorf("Words() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMaskRefusal_Exceptions(t *testing.T) {
	t.Parallel()

	refusal := RefuseClassExcept(ClassAccess, ExceptAction("B"), ExceptAction("A"), ExceptAction("B"))
	var words []string
	for _, exception := range refusal.Exceptions() {
		perm, res := exception.Named()
		words = append(words, string(perm)+" "+string(res)+": "+exception.Words())
	}
	if want := []string{"Execute A: the action A", "Execute B: the action B"}; !slices.Equal(words, want) {
		t.Errorf("Exceptions() = %v, want %v", words, want)
	}
	if class, ok := refusal.Class(); !ok || class != ClassAccess {
		t.Errorf("Class() = %q, %v, want access", class, ok)
	}
	if got := RefuseAction("A").Exceptions(); len(got) != 0 {
		t.Errorf("a refusal by name carries the exceptions %v", got)
	}
}
