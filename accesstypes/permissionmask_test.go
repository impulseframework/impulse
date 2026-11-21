package accesstypes

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestPermissionMask(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		mask            PermissionMask
		wantZero        bool
		wantAllowed     []Permission
		wantDenied      []Permission
		wantPermissions []Permission
		wantString      string
	}{
		{
			name:            "zero mask is unrestricted",
			mask:            PermissionMask{},
			wantZero:        true,
			wantAllowed:     []Permission{List, Read, Create, Update, Delete, Execute, "Custom"},
			wantPermissions: nil,
			wantString:      "unrestricted",
		},
		{
			name:            "read-only mask",
			mask:            MaskPermissions(DenyAll(), List, Read),
			wantAllowed:     []Permission{List, Read},
			wantDenied:      []Permission{Create, Update, Delete, Execute, "Custom"},
			wantPermissions: []Permission{List, Read},
			wantString:      "List,Read",
		},
		{
			name:            "mask with no permissions allows nothing",
			mask:            DenyAll(),
			wantDenied:      []Permission{List, Read, Create, Update, Delete, Execute},
			wantPermissions: []Permission{},
			wantString:      "",
		},
		{
			name:            "duplicates and the null permission are ignored",
			mask:            MaskPermissions(DenyAll(), Read, Read, NullPermission, Execute),
			wantAllowed:     []Permission{Read, Execute},
			wantDenied:      []Permission{List, NullPermission},
			wantPermissions: []Permission{Execute, Read},
			wantString:      "Execute,Read",
		},
		{
			name:            "the null permission alone allows nothing",
			mask:            MaskPermissions(DenyAll(), NullPermission),
			wantDenied:      []Permission{NullPermission, Read},
			wantPermissions: []Permission{},
			wantString:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.mask.IsZero(); got != tt.wantZero {
				t.Errorf("IsZero() = %v, want %v", got, tt.wantZero)
			}
			for _, perm := range tt.wantAllowed {
				if !tt.mask.Allows(perm) {
					t.Errorf("Allows(%q) = false, want true", perm)
				}
			}
			for _, perm := range tt.wantDenied {
				if tt.mask.Allows(perm) {
					t.Errorf("Allows(%q) = true, want false", perm)
				}
			}
			got := tt.mask.Permissions()
			if (got == nil) != (tt.wantPermissions == nil) || !slices.Equal(got, tt.wantPermissions) {
				t.Errorf("Permissions() = %#v, want %#v", got, tt.wantPermissions)
			}
			if got := tt.mask.String(); got != tt.wantString {
				t.Errorf("String() = %q, want %q", got, tt.wantString)
			}
		})
	}
}

func TestPermissionMask_PermissionsRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mask PermissionMask
	}{
		{name: "read-only", mask: MaskPermissions(DenyAll(), Read, List)},
		{name: "single permission", mask: MaskPermissions(DenyAll(), Execute)},
		{name: "allows nothing", mask: DenyAll()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rebuilt := MaskPermissions(DenyAll(), tt.mask.Permissions()...)
			if !slices.Equal(tt.mask.Permissions(), rebuilt.Permissions()) {
				t.Errorf("round trip: rebuilt %#v, want %#v", rebuilt.Permissions(), tt.mask.Permissions())
			}
			if rebuilt.IsZero() {
				t.Error("rebuilt restricted mask reads as unrestricted")
			}
		})
	}
}

// TestMaskPermissions_fallback pins the empty-list rule: with no permissions
// left after filtering, the caller's fallback is the mask — whatever it is —
// and a non-empty list ignores the fallback entirely.
func TestMaskPermissions_fallback(t *testing.T) {
	t.Parallel()

	readOnly := MaskPermissions(DenyAll(), List, Read)

	tests := []struct {
		name     string
		fallback PermissionMask
		perms    []Permission
		want     PermissionMask
	}{
		{name: "no list falls back to unrestricted", fallback: AllowAll(), perms: nil, want: AllowAll()},
		{name: "no list falls back to nothing", fallback: DenyAll(), perms: []Permission{}, want: DenyAll()},
		{name: "no list falls back to a narrower default", fallback: readOnly, perms: nil, want: readOnly},
		{name: "only the null permission is an empty list", fallback: AllowAll(), perms: []Permission{NullPermission}, want: AllowAll()},
		{name: "a list ignores the fallback", fallback: AllowAll(), perms: []Permission{Execute}, want: MaskPermissions(DenyAll(), Execute)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := MaskPermissions(tt.fallback, tt.perms...)
			if got.IsZero() != tt.want.IsZero() || !slices.Equal(got.Permissions(), tt.want.Permissions()) {
				t.Errorf("MaskPermissions() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPermissionMask_Refuses pins what a mask refuses inside the kinds it keeps: a
// class refuses every action that carries it, a named refusal refuses one permission
// on one resource and its fields, and neither touches a neighbor.
func TestPermissionMask_Refuses(t *testing.T) {
	t.Parallel()

	exceptCredential := AllowAll().Refusing(RefuseClass(ClassCredential))
	exceptLaunch := AllowAll().Refusing(RefuseAction("LaunchMission"))
	exceptShipWrites := AllowAll().Refusing(RefuseWrites("Ships")...)

	tests := []struct {
		name        string
		mask        PermissionMask
		perm        Permission
		res         Resource
		classes     []ActionClass
		wantRefused bool
		wantWhy     string
	}{
		{name: "the unrestricted mask refuses nothing", mask: AllowAll(), perm: Execute, res: "SetPassword", classes: []ActionClass{ClassCredential}},
		{name: "a kind the mask does not keep", mask: MaskPermissions(DenyAll(), List, Read), perm: Execute, res: "SetPassword", wantRefused: true, wantWhy: "the mask does not keep Execute"},
		{name: "an action of a refused class", mask: exceptCredential, perm: Execute, res: "SetPassword", classes: []ActionClass{ClassCredential}, wantRefused: true, wantWhy: "the mask refuses the class credential"},
		{name: "an action of a refused class among others", mask: exceptCredential, perm: Execute, res: "SetPassword", classes: []ActionClass{"sensitive", ClassCredential}, wantRefused: true, wantWhy: "the mask refuses the class credential"},
		{name: "an action of another class", mask: exceptCredential, perm: Execute, res: "Launch", classes: []ActionClass{"sensitive"}},
		{name: "an action of no class", mask: exceptCredential, perm: Execute, res: "Launch"},
		{name: "a class is refused whatever the kind", mask: exceptCredential, perm: List, res: "Secrets", classes: []ActionClass{ClassCredential}, wantRefused: true, wantWhy: "the mask refuses the class credential"},
		{name: "the named action", mask: exceptLaunch, perm: Execute, res: "LaunchMission", wantRefused: true, wantWhy: "the mask refuses Execute on LaunchMission"},
		{name: "the named action's neighbor", mask: exceptLaunch, perm: Execute, res: "LaunchMissions"},
		{name: "another permission on the named action", mask: exceptLaunch, perm: Read, res: "LaunchMission"},
		{name: "a record's update", mask: exceptShipWrites, perm: Update, res: "Ships", wantRefused: true, wantWhy: "the mask refuses Update on Ships"},
		{name: "a record's field is refused with it", mask: exceptShipWrites, perm: Update, res: "Ships.name", wantRefused: true, wantWhy: "the mask refuses Update on Ships"},
		{name: "a record's create", mask: exceptShipWrites, perm: Create, res: "Ships", wantRefused: true, wantWhy: "the mask refuses Create on Ships"},
		{name: "a record's delete", mask: exceptShipWrites, perm: Delete, res: "Ships", wantRefused: true, wantWhy: "the mask refuses Delete on Ships"},
		{name: "a record's reads are kept", mask: exceptShipWrites, perm: Read, res: "Ships.name"},
		{name: "another record's writes are kept", mask: exceptShipWrites, perm: Update, res: "Shipyards"},
		{name: "a field named by itself is refused", mask: AllowAll().Refusing(RefusePermission(Update, "Ships.name")), perm: Update, res: "Ships.name", wantRefused: true, wantWhy: "the mask refuses Update on Ships.name"},
		{name: "a field named by itself leaves its record", mask: AllowAll().Refusing(RefusePermission(Update, "Ships.name")), perm: Update, res: "Ships"},
		{name: "a field named by itself leaves the record's other fields", mask: AllowAll().Refusing(RefusePermission(Update, "Ships.name")), perm: Update, res: "Ships.class"},
		{name: "the kind is asked before the class", mask: MaskPermissions(DenyAll(), List, Read).Refusing(RefuseClass(ClassCredential)), perm: Execute, res: "SetPassword", classes: []ActionClass{ClassCredential}, wantRefused: true, wantWhy: "the mask does not keep Execute"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			why, refused := tt.mask.Refuses(tt.perm, tt.res, tt.classes...)
			if refused != tt.wantRefused || why != tt.wantWhy {
				t.Errorf("Refuses(%s, %s, %v) = %q, %v, want %q, %v", tt.perm, tt.res, tt.classes, why, refused, tt.wantWhy, tt.wantRefused)
			}
		})
	}
}

// TestPermissionMask_UnknownKinds pins which kinds of a mask are no permission kind:
// a kind spelled as none is keeps nothing, and whatever takes a mask from a person
// refuses it rather than start a session that keeps less than was meant.
func TestPermissionMask_UnknownKinds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mask PermissionMask
		want []Permission
	}{
		{name: "every kind", mask: AllowAll().Refusing(RefuseClass(ClassCredential))},
		{name: "no kind", mask: DenyAll()},
		{name: "the six kinds", mask: MaskPermissions(DenyAll(), PermissionKinds()...)},
		{name: "kinds spelled in lowercase", mask: MaskPermissions(DenyAll(), "read", List, "list"), want: []Permission{"list", "read"}},
		{name: "every kind as a name", mask: MaskPermissions(DenyAll(), "*"), want: []Permission{"*"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.mask.UnknownKinds(); !slices.Equal(got, tt.want) {
				t.Errorf("UnknownKinds() = %v, want %v", got, tt.want)
			}
		})
	}

	if got, want := PermissionKinds(), []Permission{Create, Delete, Execute, List, Read, Update}; !slices.Equal(got, want) {
		t.Errorf("PermissionKinds() = %v, want %v", got, want)
	}
}

// TestPermissionMask_refusalsAreNotUnrestricted pins the two questions a mask that
// keeps every kind and refuses something answers differently: it is not the
// unrestricted mask, and it is not read-only.
func TestPermissionMask_refusalsAreNotUnrestricted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		mask          PermissionMask
		wantZero      bool
		wantEveryKind bool
		wantReadOnly  bool
		wantString    string
	}{
		{name: "unrestricted", mask: AllowAll(), wantZero: true, wantEveryKind: true, wantString: "unrestricted"},
		{name: "every kind, a class refused", mask: AllowAll().Refusing(RefuseClass(ClassCredential)), wantEveryKind: true, wantString: "*;-class:credential"},
		{name: "every kind, an action refused", mask: AllowAll().Refusing(RefuseAction("Launch")), wantEveryKind: true, wantString: "*;-on:Execute:Launch"},
		{name: "read-only", mask: MaskPermissions(DenyAll(), List, Read), wantReadOnly: true, wantString: "List,Read"},
		{name: "read only one kind", mask: MaskPermissions(DenyAll(), Read), wantReadOnly: true, wantString: "Read"},
		{name: "read-only with a refusal", mask: MaskPermissions(DenyAll(), List, Read).Refusing(RefuseClass(ClassCredential)), wantReadOnly: true, wantString: "List,Read;-class:credential"},
		{name: "nothing", mask: DenyAll(), wantReadOnly: true, wantString: ""},
		{name: "a kind beyond reading", mask: MaskPermissions(DenyAll(), List, Read, Execute), wantString: "Execute,List,Read"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.mask.IsZero(); got != tt.wantZero {
				t.Errorf("IsZero() = %v, want %v", got, tt.wantZero)
			}
			if got := tt.mask.KeepsEveryKind(); got != tt.wantEveryKind {
				t.Errorf("KeepsEveryKind() = %v, want %v", got, tt.wantEveryKind)
			}
			if got := tt.mask.ReadOnly(); got != tt.wantReadOnly {
				t.Errorf("ReadOnly() = %v, want %v", got, tt.wantReadOnly)
			}
			if got := tt.mask.String(); got != tt.wantString {
				t.Errorf("String() = %q, want %q", got, tt.wantString)
			}
		})
	}
}

// TestPermissionMask_Refusing pins that adding a refusal leaves the mask it was added
// to as it was: a ceiling shared by every request is never changed by one of them.
func TestPermissionMask_Refusing(t *testing.T) {
	t.Parallel()

	ceiling := AllowAll().Refusing(RefuseClass(ClassCredential))
	narrower := ceiling.Refusing(RefuseAction("Launch"), RefuseClass("sensitive"))

	if _, refused := ceiling.Refuses(Execute, "Launch"); refused {
		t.Error("the ceiling refuses Launch after a narrower mask was derived from it")
	}
	if got, want := ceiling.Text(), "*;-class:credential"; got != want {
		t.Errorf("the ceiling's Text() = %q, want %q", got, want)
	}
	if got, want := narrower.Text(), "*;-class:credential;-class:sensitive;-on:Execute:Launch"; got != want {
		t.Errorf("the narrower mask's Text() = %q, want %q", got, want)
	}
	if got := AllowAll().Refusing(); !got.IsZero() {
		t.Errorf("Refusing() with nothing = %v, want the mask itself", got)
	}
}

// TestPermissionMask_Narrow pins the arithmetic of a grant's widest mask and a request
// for a narrower one: the kinds are the request's when it names any and the grant's
// otherwise, the refusals are joined, and a request for a kind the grant does not keep
// is refused, never trimmed.
func TestPermissionMask_Narrow(t *testing.T) {
	t.Parallel()

	readOnly := MaskPermissions(DenyAll(), List, Read)
	exceptCredential := AllowAll().Refusing(RefuseClass(ClassCredential))

	tests := []struct {
		name       string
		ceiling    PermissionMask
		request    PermissionMask
		want       PermissionMask
		wantBeyond []Permission
	}{
		{name: "no request under no mask is no mask", ceiling: AllowAll(), request: AllowAll(), want: AllowAll()},
		{name: "no request takes the ceiling's kinds", ceiling: readOnly, request: AllowAll(), want: readOnly},
		{name: "no request takes the ceiling's refusals", ceiling: exceptCredential, request: AllowAll(), want: exceptCredential},
		{name: "a narrower request under no mask", ceiling: AllowAll(), request: readOnly, want: readOnly},
		{name: "a narrower request under a ceiling of kinds", ceiling: MaskPermissions(DenyAll(), List, Read, Execute), request: readOnly, want: readOnly},
		{name: "the same kinds as the ceiling", ceiling: readOnly, request: MaskPermissions(DenyAll(), Read, List), want: readOnly},
		{name: "nothing is narrower than anything", ceiling: readOnly, request: DenyAll(), want: DenyAll()},
		{name: "a request's kinds keep the ceiling's refusals", ceiling: exceptCredential, request: MaskPermissions(DenyAll(), List, Read, Execute), want: MaskPermissions(DenyAll(), List, Read, Execute).Refusing(RefuseClass(ClassCredential))},
		{name: "a request's refusals are joined to the ceiling's", ceiling: exceptCredential, request: AllowAll().Refusing(RefuseAction("Launch")), want: AllowAll().Refusing(RefuseClass(ClassCredential), RefuseAction("Launch"))},
		{name: "a request's refusals under a ceiling of kinds", ceiling: readOnly, request: AllowAll().Refusing(RefusePermission(Read, "Ships")), want: readOnly.Refusing(RefusePermission(Read, "Ships"))},
		{name: "one kind beyond the ceiling is refused", ceiling: readOnly, request: MaskPermissions(DenyAll(), List, Read, Update), wantBeyond: []Permission{Update}},
		{name: "every kind beyond the ceiling is named", ceiling: readOnly, request: MaskPermissions(DenyAll(), Update, Read, Execute), wantBeyond: []Permission{Execute, Update}},
		{name: "a kind beyond a ceiling that keeps nothing", ceiling: DenyAll(), request: MaskPermissions(DenyAll(), Read), wantBeyond: []Permission{Read}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ceilingBefore := tt.ceiling.Text()
			got, err := tt.ceiling.Narrow(tt.request)
			if after := tt.ceiling.Text(); after != ceilingBefore {
				t.Errorf("Narrow() changed the ceiling from %q to %q", ceilingBefore, after)
			}
			if tt.wantBeyond != nil {
				var wider *WiderMaskError
				if !errors.As(err, &wider) {
					t.Fatalf("Narrow() = %v, %v, want a *WiderMaskError", got, err)
				}
				if !slices.Equal(wider.Beyond, tt.wantBeyond) {
					t.Errorf("WiderMaskError.Beyond = %v, want %v", wider.Beyond, tt.wantBeyond)
				}
				if !got.IsZero() || !wider.Gives.Equal(tt.ceiling) {
					t.Errorf("Narrow() = %v with Gives %v, want no mask and the ceiling %v", got, wider.Gives, tt.ceiling)
				}

				return
			}
			if err != nil {
				t.Fatalf("Narrow() error = %v", err)
			}
			if !got.Equal(tt.want) {
				t.Errorf("Narrow() = %q, want %q", got.Text(), tt.want.Text())
			}
		})
	}
}

// TestWiderMaskError_Error pins the sentence a refused request reads.
func TestWiderMaskError_Error(t *testing.T) {
	t.Parallel()

	_, err := MaskPermissions(DenyAll(), List, Read).Narrow(MaskPermissions(DenyAll(), Update, Delete, Read))
	want := "the request asks for Delete and Update, which is more than is given (reading only)"
	if err == nil || err.Error() != want {
		t.Errorf("Narrow() error = %v, want %q", err, want)
	}
}

// TestPermissionMask_TextRoundTrip pins the persistence form: every mask reads back
// from its text as it was written, names that carry the text's own marks included.
func TestPermissionMask_TextRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mask     PermissionMask
		wantText string
	}{
		{name: "unrestricted", mask: AllowAll(), wantText: "*"},
		{name: "nothing", mask: DenyAll(), wantText: ""},
		{name: "read-only", mask: MaskPermissions(DenyAll(), Read, List), wantText: "List,Read"},
		{name: "every kind except a class", mask: AllowAll().Refusing(RefuseClass(ClassCredential)), wantText: "*;-class:credential"},
		{name: "kinds, classes and named resources", mask: MaskPermissions(DenyAll(), List, Read, Execute).Refusing(RefuseAction("Launch"), RefuseClass("sensitive"), RefuseClass(ClassCredential), RefusePermission(Update, "Ships")), wantText: "Execute,List,Read;-class:credential;-class:sensitive;-on:Execute:Launch;-on:Update:Ships"},
		{name: "nothing kept, a refusal all the same", mask: DenyAll().Refusing(RefuseClass(ClassCredential)), wantText: ";-class:credential"},
		{name: "names that carry the text's marks", mask: MaskPermissions(DenyAll(), "a,b", "*").Refusing(RefuseClass("x;y"), RefusePermission("p:q", "r;s,t u")), wantText: "%2A,a%2Cb;-class:x%3By;-on:p%3Aq:r%3Bs%2Ct+u"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			text := tt.mask.Text()
			if text != tt.wantText {
				t.Errorf("Text() = %q, want %q", text, tt.wantText)
			}
			got, err := ParseMask(text)
			if err != nil {
				t.Fatalf("ParseMask(%q) error = %v", text, err)
			}
			if !got.Equal(tt.mask) {
				t.Errorf("ParseMask(%q) = %q, want the mask that was written", text, got.Text())
			}
		})
	}
}

// TestParseMask pins what the reader accepts beyond its own writing (the spaces an
// older column carried) and that it refuses a text that is no mask's, so a mask is
// never read wider than it was written.
func TestParseMask(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		want    PermissionMask
		wantErr bool
	}{
		{name: "spaces around the kinds", text: " List , Read ", want: MaskPermissions(DenyAll(), List, Read)},
		{name: "an empty kind between commas", text: "List,,Read", want: MaskPermissions(DenyAll(), List, Read)},
		{name: "every kind", text: "*", want: AllowAll()},
		{name: "a part that is no refusal", text: "*;class:credential", wantErr: true},
		{name: "a class refusal with no class", text: "*;-class:", wantErr: true},
		{name: "a named refusal with no resource", text: "*;-on:Execute", wantErr: true},
		{name: "a named refusal with an empty resource", text: "*;-on:Execute:", wantErr: true},
		{name: "a named refusal with no permission", text: "*;-on::Launch", wantErr: true},
		{name: "an empty part", text: "*;", wantErr: true},
		{name: "a kind that does not unescape", text: "List,%zz", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseMask(tt.text)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseMask(%q) error = %v, wantErr %v", tt.text, err, tt.wantErr)
			}
			if tt.wantErr {
				if !got.IsZero() {
					t.Errorf("ParseMask(%q) = %q beside its error, want the zero mask and the caller's own fallback", tt.text, got.Text())
				}

				return
			}
			if !got.Equal(tt.want) {
				t.Errorf("ParseMask(%q) = %q, want %q", tt.text, got.Text(), tt.want.Text())
			}
		})
	}
}

// TestPermissionMask_Words pins the mask as a person reads it.
func TestPermissionMask_Words(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mask PermissionMask
		want string
	}{
		{name: "unrestricted", mask: AllowAll(), want: "everything"},
		{name: "nothing", mask: DenyAll(), want: "nothing"},
		{name: "read-only", mask: MaskPermissions(DenyAll(), List, Read), want: "reading only"},
		{name: "one kind", mask: MaskPermissions(DenyAll(), Read), want: "Read only"},
		{name: "three kinds", mask: MaskPermissions(DenyAll(), List, Read, Execute), want: "Execute, List and Read only"},
		{name: "everything except a class", mask: AllowAll().Refusing(RefuseClass(ClassCredential)), want: "everything except credential actions"},
		{name: "everything except an action", mask: AllowAll().Refusing(RefuseAction("LaunchMission")), want: "everything except the action LaunchMission"},
		{name: "everything except a record's writes", mask: AllowAll().Refusing(RefuseWrites("Ships")...), want: "everything except writing Ships"},
		{
			name: "two of a record's three writes are said one by one",
			mask: AllowAll().Refusing(RefusePermission(Create, "Ships"), RefusePermission(Update, "Ships")), want: "everything except creating Ships and changing Ships",
		},
		{
			name: "a record's writes beside its reads, a field of another and a class",
			mask: AllowAll().Refusing(RefuseWrites("Ships")...).Refusing(RefusePermission(Read, "Ships"), RefusePermission(Update, "Docks.pier"), RefuseClass(ClassAccess)),
			want: "everything except access actions, changing Docks.pier, writing Ships and reading Ships",
		},
		{
			name: "the writes of two records and one write of a field",
			mask: AllowAll().Refusing(RefuseWrites("Ships")...).Refusing(RefuseWrites("Crews")...).Refusing(RefusePermission(Update, "Ships.registry")),
			want: "everything except writing Crews, writing Ships and changing Ships.registry",
		},
		{
			name: "kinds and a record's writes",
			mask: MaskPermissions(DenyAll(), Create, Update, Delete, Read).Refusing(RefuseWrites("Ships")...), want: "Create, Delete, Read and Update only, except writing Ships",
		},
		{name: "a class and an action", mask: AllowAll().Refusing(RefuseAction("LaunchMission"), RefuseClass(ClassCredential)), want: "everything except credential actions and the action LaunchMission"},
		{name: "kinds and a class", mask: MaskPermissions(DenyAll(), List, Read, Execute).Refusing(RefuseClass(ClassCredential)), want: "Execute, List and Read only, except credential actions"},
		{name: "reads of a record", mask: AllowAll().Refusing(RefusePermission(List, "Ships"), RefusePermission(Read, "Ships")), want: "everything except listing Ships and reading Ships"},
		{name: "a permission of the application's own", mask: AllowAll().Refusing(RefusePermission("Approve", "Ships")), want: "everything except Approve on Ships"},
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

// TestMaskRefusal pins what a refusal answers of itself.
func TestMaskRefusal(t *testing.T) {
	t.Parallel()

	if class, ok := RefuseClass(ClassCredential).Class(); !ok || class != ClassCredential {
		t.Errorf("RefuseClass().Class() = %q, %v, want credential, true", class, ok)
	}
	if _, _, ok := RefuseClass(ClassCredential).Named(); ok {
		t.Error("RefuseClass().Named() answered a resource")
	}
	if perm, res, ok := RefuseAction("Launch").Named(); !ok || perm != Execute || res != "Launch" {
		t.Errorf("RefuseAction().Named() = %q, %q, %v, want Execute on Launch", perm, res, ok)
	}
	if _, ok := RefuseAction("Launch").Class(); ok {
		t.Error("RefuseAction().Class() answered a class")
	}
}

// TestActionClass_Valid pins how a class may be named.
func TestActionClass_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		class ActionClass
		want  bool
	}{
		{class: ClassCredential, want: true},
		{class: "money_movement", want: true},
		{class: "tier2", want: true},
		{class: "", want: false},
		{class: "Credential", want: false},
		{class: "2fa", want: false},
		{class: "money-movement", want: false},
		{class: "with space", want: false},
		{class: ActionClass("a" + strings.Repeat("b", 64)), want: false},
	}

	for _, tt := range tests {
		t.Run(string(tt.class), func(t *testing.T) {
			t.Parallel()

			if got := tt.class.Valid(); got != tt.want {
				t.Errorf("ActionClass(%q).Valid() = %v, want %v", tt.class, got, tt.want)
			}
		})
	}
}
