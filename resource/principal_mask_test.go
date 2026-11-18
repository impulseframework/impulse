package resource

import (
	"bytes"
	"context"
	"log"
	"strings"
	"sync"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
)

// stubClasses answers the classes of the tests' actions, each within one scope.
type stubClasses map[accesstypes.PermissionScope]map[accesstypes.Resource][]accesstypes.ActionClass

func (s stubClasses) ActionClasses(scope accesstypes.PermissionScope, action accesstypes.Resource) []accesstypes.ActionClass {
	return s[scope][action]
}

// maskTestClasses are the classes the masked tests' actions carry: two credential
// actions of the whole application, one of a tenant, and an action of a class of the
// application's own.
func maskTestClasses() stubClasses {
	return stubClasses{
		accesstypes.GlobalPermissionScope: {
			"SetPassword":    {accesstypes.ClassCredential},
			"UnlinkIdentity": {accesstypes.ClassCredential},
			"WireFunds":      {"money_movement"},
		},
		accesstypes.DomainPermissionScope: {
			"RenameUser": {accesstypes.ClassCredential},
		},
	}
}

// maskTestDigest is what the tests' person holds before any mask.
func maskTestDigest() accesstypes.PermissionDigest {
	return accesstypes.PermissionDigest{
		"SetPassword":    {accesstypes.Execute: accesstypes.DigestGranted},
		"UnlinkIdentity": {accesstypes.Execute: accesstypes.DigestConditional},
		"WireFunds":      {accesstypes.Execute: accesstypes.DigestGranted},
		"Launch":         {accesstypes.Execute: accesstypes.DigestGranted},
		"LaunchProbe":    {accesstypes.Execute: accesstypes.DigestGranted},
		"RenameUser":     {accesstypes.Execute: accesstypes.DigestGranted},
		"Ships":          {accesstypes.Read: accesstypes.DigestGranted, accesstypes.Update: accesstypes.DigestGranted, accesstypes.Delete: accesstypes.DigestGranted},
		"Ships.name":     {accesstypes.Read: accesstypes.DigestGranted, accesstypes.Update: accesstypes.DigestConditional},
		"Shipyards":      {accesstypes.Update: accesstypes.DigestGranted},
	}
}

// TestMasked_refusesInsideAKeptKind pins the mask's refusals inside a kind it keeps:
// an action of a refused class, and a permission refused on a resource by name, are
// Denied before policy is asked about them, their neighbors are answered by policy,
// and the digest leaves out exactly what the check refuses.
func TestMasked_refusesInsideAKeptKind(t *testing.T) {
	t.Parallel()

	everything := maskTestDigest()
	without := func(drop map[accesstypes.Resource][]accesstypes.Permission) accesstypes.PermissionDigest {
		digest := maskTestDigest()
		for res, perms := range drop {
			for _, perm := range perms {
				delete(digest[res], perm)
			}
			if len(digest[res]) == 0 {
				delete(digest, res)
			}
		}

		return digest
	}

	tests := []struct {
		name    string
		mask    accesstypes.PermissionMask
		classes ActionClasses
		scope   accesstypes.Scope
		perm    accesstypes.Permission
		check   []accesstypes.Resource
		// wantDenied are the resources the mask refuses; every other one is Granted.
		wantDenied []accesstypes.Resource
		// wantDelegated are the resources policy was asked about.
		wantDelegated []accesstypes.Resource
		wantDigest    accesstypes.PermissionDigest
	}{
		{
			name:          "a refused class: its actions are denied, the others are policy's",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Execute,
			check:         []accesstypes.Resource{"SetPassword", "Launch", "UnlinkIdentity", "WireFunds"},
			wantDenied:    []accesstypes.Resource{"SetPassword", "UnlinkIdentity"},
			wantDelegated: []accesstypes.Resource{"Launch", "WireFunds"},
			// RenameUser carries the class in a tenant alone, and the whole
			// application's digest leaves it out all the same: the class is the
			// action's, whichever scope registers it.
			wantDigest: without(map[accesstypes.Resource][]accesstypes.Permission{"SetPassword": {accesstypes.Execute}, "UnlinkIdentity": {accesstypes.Execute}, "RenameUser": {accesstypes.Execute}}),
		},
		{
			name:          "a class is the action's whichever scope registers it: a check in the other scope is refused too",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			classes:       maskTestClasses(),
			scope:         accesstypes.DomainScope("tenant"),
			perm:          accesstypes.Execute,
			check:         []accesstypes.Resource{"RenameUser", "SetPassword", "Launch"},
			wantDenied:    []accesstypes.Resource{"RenameUser", "SetPassword"},
			wantDelegated: []accesstypes.Resource{"Launch"},
			wantDigest:    without(map[accesstypes.Resource][]accesstypes.Permission{"RenameUser": {accesstypes.Execute}, "SetPassword": {accesstypes.Execute}, "UnlinkIdentity": {accesstypes.Execute}}),
		},
		{
			name:          "a class of the application's own",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseClass("money_movement")),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Execute,
			check:         []accesstypes.Resource{"WireFunds", "SetPassword"},
			wantDenied:    []accesstypes.Resource{"WireFunds"},
			wantDelegated: []accesstypes.Resource{"SetPassword"},
			wantDigest:    without(map[accesstypes.Resource][]accesstypes.Permission{"WireFunds": {accesstypes.Execute}}),
		},
		{
			name:          "one named action: that action alone",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("Launch")),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Execute,
			check:         []accesstypes.Resource{"Launch", "LaunchProbe", "SetPassword"},
			wantDenied:    []accesstypes.Resource{"Launch"},
			wantDelegated: []accesstypes.Resource{"LaunchProbe", "SetPassword"},
			wantDigest:    without(map[accesstypes.Resource][]accesstypes.Permission{"Launch": {accesstypes.Execute}}),
		},
		{
			name:          "one named action needs no classes",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("Launch")),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Execute,
			check:         []accesstypes.Resource{"Launch", "LaunchProbe"},
			wantDenied:    []accesstypes.Resource{"Launch"},
			wantDelegated: []accesstypes.Resource{"LaunchProbe"},
			wantDigest:    without(map[accesstypes.Resource][]accesstypes.Permission{"Launch": {accesstypes.Execute}}),
		},
		{
			name:          "a record's writes: the record and its fields, its reads kept",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseWrites("Ships")...),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Update,
			check:         []accesstypes.Resource{"Ships", "Ships.name", "Shipyards"},
			wantDenied:    []accesstypes.Resource{"Ships", "Ships.name"},
			wantDelegated: []accesstypes.Resource{"Shipyards"},
			wantDigest:    without(map[accesstypes.Resource][]accesstypes.Permission{"Ships": {accesstypes.Update, accesstypes.Delete}, "Ships.name": {accesstypes.Update}}),
		},
		{
			name:          "a record's reads under a mask that refuses its writes",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseWrites("Ships")...),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Read,
			check:         []accesstypes.Resource{"Ships", "Ships.name"},
			wantDelegated: []accesstypes.Resource{"Ships", "Ships.name"},
			wantDigest:    without(map[accesstypes.Resource][]accesstypes.Permission{"Ships": {accesstypes.Update, accesstypes.Delete}, "Ships.name": {accesstypes.Update}}),
		},
		{
			name:       "everything asked about is refused: policy is not asked",
			mask:       accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			classes:    maskTestClasses(),
			scope:      accesstypes.GlobalScope(),
			perm:       accesstypes.Execute,
			check:      []accesstypes.Resource{"SetPassword", "UnlinkIdentity"},
			wantDenied: []accesstypes.Resource{"SetPassword", "UnlinkIdentity"},
			wantDigest: without(map[accesstypes.Resource][]accesstypes.Permission{"SetPassword": {accesstypes.Execute}, "UnlinkIdentity": {accesstypes.Execute}, "RenameUser": {accesstypes.Execute}}),
		},
		{
			name:       "a refused class with no classes handed: nothing is allowed on a guess",
			mask:       accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			scope:      accesstypes.GlobalScope(),
			perm:       accesstypes.Execute,
			check:      []accesstypes.Resource{"SetPassword", "Launch"},
			wantDenied: []accesstypes.Resource{"SetPassword", "Launch"},
			wantDigest: accesstypes.PermissionDigest{},
		},
		{
			name:       "the kind is refused whatever the class",
			mask:       accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read).Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			classes:    maskTestClasses(),
			scope:      accesstypes.GlobalScope(),
			perm:       accesstypes.Execute,
			check:      []accesstypes.Resource{"SetPassword", "Launch"},
			wantDenied: []accesstypes.Resource{"SetPassword", "Launch"},
			wantDigest: accesstypes.PermissionDigest{"Ships": {accesstypes.Read: accesstypes.DigestGranted}, "Ships.name": {accesstypes.Read: accesstypes.DigestGranted}},
		},
		{
			name:          "the unrestricted mask refuses nothing",
			mask:          accesstypes.AllowAll(),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Execute,
			check:         []accesstypes.Resource{"SetPassword", "Launch"},
			wantDelegated: []accesstypes.Resource{"SetPassword", "Launch"},
			wantDigest:    everything,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stub := &stubPermissions{user: "bob", digest: maskTestDigest(), domains: []accesstypes.Domain{"tenant"}}
			perms := Masked(stub, tt.mask, tt.classes)

			decisions, err := perms.Check(context.Background(), accesstypes.NewEnvironment(), tt.scope, tt.perm, tt.check...)
			if err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			if len(decisions) != len(tt.check) {
				t.Errorf("Check() answered %d resources, want one answer for each of the %d asked", len(decisions), len(tt.check))
			}
			var denied []accesstypes.Resource
			for _, res := range tt.check {
				decision, ok := decisions[res]
				if !ok {
					t.Errorf("Check() left %s unanswered", res)
				}
				if !decision.IsGranted() {
					denied = append(denied, res)
				}
			}
			if diff := cmp.Diff(tt.wantDenied, denied); diff != "" {
				t.Errorf("denied resources mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantDelegated, stub.checkResources); diff != "" {
				t.Errorf("resources policy was asked about mismatch (-want +got):\n%s", diff)
			}

			digest, err := perms.PermissionDigest(context.Background(), tt.scope)
			if err != nil {
				t.Fatalf("PermissionDigest() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantDigest, digest); diff != "" {
				t.Errorf("PermissionDigest() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// lockedBuffer is a buffer the standard logger may write from any goroutine.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, errors.Wrap(err, "bytes.Buffer.Write()")
	}

	return n, nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// TestMasked_denialIsLoggedAsTheMasks pins the evidence of a mask's denial: the line
// says the mask denied it, names the mask, the person, the resource and which part of
// the mask refused. The standard logger is the process's: the test takes its output
// once for all its cases and looks for each case's own line among whatever else the
// package's tests log meanwhile.
func TestMasked_denialIsLoggedAsTheMasks(t *testing.T) {
	t.Parallel()

	var output lockedBuffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })

	tests := []struct {
		name string
		mask accesstypes.PermissionMask
		perm accesstypes.Permission
		res  accesstypes.Resource
		want string
	}{
		{
			name: "a kind the mask does not keep",
			mask: accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read),
			perm: accesstypes.Execute,
			res:  "SetPassword",
			want: "impersonation: Execute denied by mask (List,Read) for a mask-log person on [SetPassword]",
		},
		{
			name: "a refused class",
			mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			perm: accesstypes.Execute,
			res:  "SetPassword",
			want: "impersonation: Execute denied by mask (*;-class:credential) for a mask-log person on SetPassword: the mask refuses the class credential",
		},
		{
			name: "a named action",
			mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("Launch")),
			perm: accesstypes.Execute,
			res:  "Launch",
			want: "impersonation: Execute denied by mask (*;-on:Execute:Launch) for a mask-log person on Launch: the mask refuses Execute on Launch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// The person is named as no other test of the package names one, so the
			// line found is this case's.
			stub := &stubPermissions{user: "a mask-log person"}
			perms := Masked(stub, tt.mask, maskTestClasses())
			if _, err := perms.Check(context.Background(), accesstypes.NewEnvironment(), accesstypes.GlobalScope(), tt.perm, tt.res); err != nil {
				t.Fatalf("Check() error = %v", err)
			}
			if got := output.String(); !strings.Contains(got, tt.want) {
				t.Errorf("log = %q, want a line carrying %q", got, tt.want)
			}
			if len(stub.checkResources) != 0 {
				t.Errorf("policy was asked about %v, want the mask to answer first", stub.checkResources)
			}
		})
	}
}
