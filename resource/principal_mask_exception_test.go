package resource

import (
	"context"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// exceptionTestCollection is the application the tests of a class's exceptions run
// against: the record Seats, whose three writes carry the class access, the record
// Teams, whose update carries it, the record tenancyResources (a struct of the tests,
// so a patch can be armed over it), whose update carries it, and two actions of the
// class, GrantSeat and RevokeSeat.
func exceptionTestCollection(t *testing.T) *GeneratedCollection {
	t.Helper()

	b := NewCollectionBuilder()
	access := []accesstypes.ActionClass{accesstypes.ClassAccess}
	for _, action := range []accesstypes.Resource{"GrantSeat", "RevokeSeat"} {
		if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, action); err != nil {
			t.Fatalf("AddMethodResource(%s) error = %v", action, err)
		}
		b.SetActionClasses(accesstypes.GlobalPermissionScope, action, access)
	}
	fields := []FieldTags{{Field: "ID", JSON: "id", Perm: "-"}, {Field: "Name", JSON: "name"}, {Field: "Rank", JSON: "rank"}}
	routes := [][]accesstypes.Permission{{accesstypes.List}, {accesstypes.Read}, {accesstypes.Create, accesstypes.Update, accesstypes.Delete}}
	for _, record := range []accesstypes.Resource{"Seats", "Teams", tenancyTestResource} {
		for _, perms := range routes {
			set, err := NewSetData(fields, perms...)
			if err != nil {
				t.Fatalf("NewSetData(%s, %v) error = %v", record, perms, err)
			}
			if err := b.AddResourceSet(accesstypes.DomainPermissionScope, record, set); err != nil {
				t.Fatalf("AddResourceSet(%s, %v) error = %v", record, perms, err)
			}
		}
	}
	for _, write := range []accesstypes.Permission{accesstypes.Create, accesstypes.Update, accesstypes.Delete} {
		b.SetWriteClasses(accesstypes.DomainPermissionScope, "Seats", write, access)
	}
	b.SetWriteClasses(accesstypes.DomainPermissionScope, "Teams", accesstypes.Update, access)
	b.SetWriteClasses(accesstypes.DomainPermissionScope, tenancyTestResource, accesstypes.Update, access)
	collection, err := NewGeneratedCollection(b.Data())
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	return collection
}

// A mask that refuses a class and keeps something out of the refusal leaves what it
// keeps to policy and refuses the rest of the class: a record's writes, an action, or a
// field. The field is kept to a write that sets it and nothing else, and a check that
// does not say which fields it sets is refused the record.
func TestMasked_classExceptions(t *testing.T) {
	t.Parallel()

	collection := exceptionTestCollection(t)
	keepsSeats := accesstypes.AllowAll().Refusing(accesstypes.RefuseClassExcept(accesstypes.ClassAccess, accesstypes.ExceptWrites("Seats")...))
	keepsGrant := accesstypes.AllowAll().Refusing(accesstypes.RefuseClassExcept(accesstypes.ClassAccess, accesstypes.ExceptAction("GrantSeat")))
	keepsName := accesstypes.AllowAll().Refusing(
		accesstypes.RefuseClassExcept(accesstypes.ClassAccess, accesstypes.ExceptPermission(accesstypes.Update, "Seats.name")),
	)
	keepsSeatsRefusesDelete := keepsSeats.Refusing(accesstypes.RefusePermission(accesstypes.Delete, "Seats"))

	tests := []struct {
		name string
		mask accesstypes.PermissionMask
		perm accesstypes.Permission
		res  accesstypes.Resource
		// written are the fields the check says its write sets; none for a check
		// that says nothing.
		written []accesstypes.Resource
		// kept reports that the mask leaves the check to policy.
		kept bool
	}{
		{name: "an excepted record's create", mask: keepsSeats, perm: accesstypes.Create, res: "Seats", kept: true},
		{name: "an excepted record's update", mask: keepsSeats, perm: accesstypes.Update, res: "Seats", kept: true},
		{name: "an excepted record's field", mask: keepsSeats, perm: accesstypes.Update, res: "Seats.rank", kept: true},
		{name: "an excepted record's delete", mask: keepsSeats, perm: accesstypes.Delete, res: "Seats", kept: true},
		{name: "another record of the class", mask: keepsSeats, perm: accesstypes.Update, res: "Teams"},
		{name: "an action of the class", mask: keepsSeats, perm: accesstypes.Execute, res: "GrantSeat"},
		{name: "a refusal by name beats the exception", mask: keepsSeatsRefusesDelete, perm: accesstypes.Delete, res: "Seats"},
		{name: "the exception beside a refusal by name", mask: keepsSeatsRefusesDelete, perm: accesstypes.Update, res: "Seats", kept: true},

		{name: "an excepted action", mask: keepsGrant, perm: accesstypes.Execute, res: "GrantSeat", kept: true},
		{name: "another action of the class", mask: keepsGrant, perm: accesstypes.Execute, res: "RevokeSeat"},
		{name: "a record beside an excepted action", mask: keepsGrant, perm: accesstypes.Update, res: "Seats"},

		{name: "an excepted field", mask: keepsName, perm: accesstypes.Update, res: "Seats.name", kept: true},
		{name: "another field of the record", mask: keepsName, perm: accesstypes.Update, res: "Seats.rank"},
		{name: "the record, by a check that says no field", mask: keepsName, perm: accesstypes.Update, res: "Seats"},
		{
			name: "the record, by a write of the excepted field alone", mask: keepsName, perm: accesstypes.Update, res: "Seats",
			written: []accesstypes.Resource{"Seats.name"}, kept: true,
		},
		{
			name: "the record, by a write of the excepted field and another", mask: keepsName, perm: accesstypes.Update, res: "Seats",
			written: []accesstypes.Resource{"Seats.name", "Seats.rank"},
		},
		{
			name: "the record's create, which the exception does not name", mask: keepsName, perm: accesstypes.Create, res: "Seats",
			written: []accesstypes.Resource{"Seats.name"},
		},
		{
			name: "another record, by a write that names the excepted field", mask: keepsName, perm: accesstypes.Update, res: "Teams",
			written: []accesstypes.Resource{"Seats.name"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			if tt.written != nil {
				record := tt.written[0][:slices.Index([]byte(tt.written[0]), '.')]
				ctx = withWrittenFields(ctx, record, tt.written)
			}
			stub := &stubPermissions{user: "bob"}
			if _, err := Masked(stub, tt.mask, collection).Check(ctx, accesstypes.NewEnvironment(), accesstypes.DomainScope("tenant"), tt.perm, tt.res); err != nil {
				t.Fatalf("Check(%s, %s) error = %v", tt.perm, tt.res, err)
			}
			if kept := len(stub.checkResources) != 0; kept != tt.kept {
				t.Errorf("Check(%s, %s) under %q left to policy = %t, want %t", tt.perm, tt.res, tt.mask.Text(), kept, tt.kept)
			}
		})
	}
}

// The digest of a masked session keeps what an exception keeps, so the pages offer it:
// an excepted record with its fields, an excepted action, and for an excepted field the
// field and its record, without the record's other fields.
func TestMasked_PermissionDigest_classExceptions(t *testing.T) {
	t.Parallel()

	collection := exceptionTestCollection(t)
	granted := func(perms ...accesstypes.Permission) map[accesstypes.Permission]accesstypes.DigestState {
		out := make(map[accesstypes.Permission]accesstypes.DigestState, len(perms))
		for _, perm := range perms {
			out[perm] = accesstypes.DigestGranted
		}

		return out
	}
	held := accesstypes.PermissionDigest{
		"Seats":      granted(accesstypes.Read, accesstypes.Create, accesstypes.Update, accesstypes.Delete),
		"Seats.name": granted(accesstypes.Read, accesstypes.Update),
		"Seats.rank": granted(accesstypes.Read, accesstypes.Update),
		"Teams":      granted(accesstypes.Read, accesstypes.Update),
		"GrantSeat":  granted(accesstypes.Execute),
		"RevokeSeat": granted(accesstypes.Execute),
	}
	reads := func() accesstypes.PermissionDigest {
		return accesstypes.PermissionDigest{
			"Seats":      granted(accesstypes.Read),
			"Seats.name": granted(accesstypes.Read),
			"Seats.rank": granted(accesstypes.Read),
			"Teams":      granted(accesstypes.Read),
		}
	}
	with := func(digest accesstypes.PermissionDigest, res accesstypes.Resource, perms ...accesstypes.Permission) accesstypes.PermissionDigest {
		if digest[res] == nil {
			digest[res] = granted()
		}
		for _, perm := range perms {
			digest[res][perm] = accesstypes.DigestGranted
		}

		return digest
	}

	tests := []struct {
		name       string
		exceptions []accesstypes.MaskException
		want       accesstypes.PermissionDigest
	}{
		{name: "no exception", want: reads()},
		{
			name:       "a record's writes",
			exceptions: accesstypes.ExceptWrites("Seats"),
			want: with(with(with(reads(), "Seats", accesstypes.Create, accesstypes.Update, accesstypes.Delete),
				"Seats.name", accesstypes.Update), "Seats.rank", accesstypes.Update),
		},
		{
			name:       "an action",
			exceptions: []accesstypes.MaskException{accesstypes.ExceptAction("GrantSeat")},
			want:       with(reads(), "GrantSeat", accesstypes.Execute),
		},
		{
			name:       "a field",
			exceptions: []accesstypes.MaskException{accesstypes.ExceptPermission(accesstypes.Update, "Seats.name")},
			want:       with(with(reads(), "Seats", accesstypes.Update), "Seats.name", accesstypes.Update),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mask := accesstypes.AllowAll().Refusing(accesstypes.RefuseClassExcept(accesstypes.ClassAccess, tt.exceptions...))
			got, err := Masked(&stubPermissions{user: "bob", digest: held}, mask, collection).PermissionDigest(context.Background(), accesstypes.DomainScope("tenant"))
			if err != nil {
				t.Fatalf("PermissionDigest() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("PermissionDigest() under %q mismatch (-want +got):\n%s", mask.Text(), diff)
			}
		})
	}
}

// An armed update tells the masked check every field it sets, so a mask that keeps one
// field of a record out of a refused class keeps the update of that field alone, and
// refuses the update that sets another field with it.
func TestPatchSet_armed_tellsTheMaskItsFields(t *testing.T) {
	t.Parallel()

	collection := exceptionTestCollection(t)
	updateSet, err := NewSet[tenancyResource, tenancyCreateRequest](accesstypes.Update)
	if err != nil {
		t.Fatalf("NewSet(Update) error = %v", err)
	}
	mask := accesstypes.AllowAll().Refusing(
		accesstypes.RefuseClassExcept(accesstypes.ClassAccess, accesstypes.ExceptPermission(accesstypes.Update, tenancyTestResource+".name")),
	)

	tests := []struct {
		name   string
		fields []accesstypes.Field
		kept   bool
	}{
		{name: "the excepted field alone", fields: []accesstypes.Field{"Name"}, kept: true},
		{name: "the excepted field and another", fields: []accesstypes.Field{"Name", "StationID"}},
		{name: "another field alone", fields: []accesstypes.Field{"StationID"}},
		{name: "no field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stub := &stubPermissions{user: "bob"}
			p := NewPatchSet(NewMetadata[tenancyResource]()).SetKey("ID", "one")
			for _, field := range tt.fields {
				p.Set(field, "dock four")
			}
			p.SetPatchType(UpdatePatchType).
				EnableUserPermissionEnforcement(updateSet, Masked(stub, mask, collection), accesstypes.DomainScope("tenant"), accesstypes.Update)

			err := p.querySet.checkResourcePermission(context.Background())
			if tt.kept != (err == nil) {
				t.Fatalf("checkResourcePermission() error = %v, want kept = %t", err, tt.kept)
			}
			if asked := len(stub.checkResources) != 0; asked != tt.kept {
				t.Errorf("policy asked = %t, want %t", asked, tt.kept)
			}
		})
	}
}
