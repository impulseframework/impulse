package resource

import (
	"bytes"
	"context"
	"io"
	"log"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/go-playground/errors/v5"
	"github.com/google/go-cmp/cmp"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// stubClasses answers for the tests' application: the actions it registers, each within
// one scope, with the classes each carries (none for an action that carries no class).
// A name that is not here is no action of the application.
type stubClasses map[accesstypes.PermissionScope]map[accesstypes.Resource][]accesstypes.ActionClass

func (s stubClasses) ActionClasses(scope accesstypes.PermissionScope, action accesstypes.Resource) []accesstypes.ActionClass {
	return s[scope][action]
}

// WriteClasses reports none: the tests' records carry no class on a write. The writes
// that carry one are the generated collection's (TestMasked_generatedCollection_writeClasses).
func (stubClasses) WriteClasses(accesstypes.PermissionScope, accesstypes.Permission, accesstypes.Resource) []accesstypes.ActionClass {
	return nil
}

// HasPermission reports Execute on an action the application registers within scope.
func (s stubClasses) HasPermission(scope accesstypes.PermissionScope, permission accesstypes.Permission, res accesstypes.Resource) bool {
	_, registered := s[scope][res]

	return registered && permission == accesstypes.Execute
}

// stubCollection answers for the tests' application: its actions (stubClasses) and its
// records, each within one scope with the permissions it registers. A name that is in
// neither is one the application registers nothing under.
type stubCollection struct {
	stubClasses
	records map[accesstypes.PermissionScope]map[accesstypes.Resource][]accesstypes.Permission
}

// HasPermission reports Execute on an action, and a record's own permissions on the
// record, as the application registers them within scope.
func (s stubCollection) HasPermission(scope accesstypes.PermissionScope, permission accesstypes.Permission, res accesstypes.Resource) bool {
	return s.stubClasses.HasPermission(scope, permission, res) || slices.Contains(s.records[scope][res], permission)
}

// maskTestClasses are the masked tests' actions and the classes they carry: two
// credential actions of the whole application, one of a tenant, an action of a class of
// the application's own, and two actions that carry no class. Beside them are the
// application's two records, which carry none: Ships, read, updated and deleted, and
// Shipyards, updated.
func maskTestClasses() stubCollection {
	return stubCollection{
		stubClasses: stubClasses{
			accesstypes.GlobalPermissionScope: {
				"SetPassword":    {accesstypes.ClassCredential},
				"UnlinkIdentity": {accesstypes.ClassCredential},
				"WireFunds":      {"money_movement"},
				"Launch":         nil,
				"LaunchProbe":    nil,
			},
			accesstypes.DomainPermissionScope: {
				"RenameUser": {accesstypes.ClassCredential},
			},
		},
		records: map[accesstypes.PermissionScope]map[accesstypes.Resource][]accesstypes.Permission{
			accesstypes.GlobalPermissionScope: {
				"Ships":     {accesstypes.Read, accesstypes.Update, accesstypes.Delete},
				"Shipyards": {accesstypes.Update},
			},
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
		// holds is what the person holds before the mask; maskTestDigest when nil.
		holds      accesstypes.PermissionDigest
		wantDigest accesstypes.PermissionDigest
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
			// Decommission is granted to the person and is no action of the application
			// the check was handed, and Lanch is Launch spelled wrong: neither carries a
			// class anyone declared, so under a mask that refuses a class neither is let
			// through on the guess that it carries none. The application registers
			// nothing under Decommission, so the Read the person holds on it is left out
			// of the digest with its Execute.
			name:          "a refused class and an action the application does not register: denied before policy is asked, and left out of the digest",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Execute,
			check:         []accesstypes.Resource{"Launch", "Decommission", "Lanch", "Decommission.reason"},
			wantDenied:    []accesstypes.Resource{"Decommission", "Lanch", "Decommission.reason"},
			wantDelegated: []accesstypes.Resource{"Launch"},
			holds: accesstypes.PermissionDigest{
				"Launch":       {accesstypes.Execute: accesstypes.DigestGranted},
				"Decommission": {accesstypes.Execute: accesstypes.DigestGranted, accesstypes.Read: accesstypes.DigestGranted},
			},
			wantDigest: accesstypes.PermissionDigest{"Launch": {accesstypes.Execute: accesstypes.DigestGranted}},
		},
		{
			name:          "an action a tenant's scope registers is an action of the application in a check for the whole application",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseClass("money_movement")),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Execute,
			check:         []accesstypes.Resource{"RenameUser", "WireFunds"},
			wantDenied:    []accesstypes.Resource{"WireFunds"},
			wantDelegated: []accesstypes.Resource{"RenameUser"},
			wantDigest:    without(map[accesstypes.Resource][]accesstypes.Permission{"WireFunds": {accesstypes.Execute}}),
		},
		{
			name:          "a mask that refuses no class leaves an action the application does not register to policy",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("Launch")),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Execute,
			check:         []accesstypes.Resource{"Decommission", "Launch"},
			wantDenied:    []accesstypes.Resource{"Launch"},
			wantDelegated: []accesstypes.Resource{"Decommission"},
			holds: accesstypes.PermissionDigest{
				"Launch":       {accesstypes.Execute: accesstypes.DigestGranted},
				"Decommission": {accesstypes.Execute: accesstypes.DigestGranted},
			},
			wantDigest: accesstypes.PermissionDigest{"Decommission": {accesstypes.Execute: accesstypes.DigestGranted}},
		},
		{
			// A record carries no class, so what a mask that refuses a class does to a
			// record the application registers is nothing.
			name:          "a refused class and a record: a record's permissions are policy's",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Update,
			check:         []accesstypes.Resource{"Ships", "Ships.name", "Shipyards"},
			wantDelegated: []accesstypes.Resource{"Ships", "Ships.name", "Shipyards"},
			wantDigest:    without(map[accesstypes.Resource][]accesstypes.Permission{"SetPassword": {accesstypes.Execute}, "UnlinkIdentity": {accesstypes.Execute}, "RenameUser": {accesstypes.Execute}}),
		},
		{
			// Hangars is granted to the person and is nothing the application the check
			// was handed registers, and Shipyrds is Shipyards spelled wrong. Either may
			// be an action of another collection, or of none: under a mask that refuses
			// a class neither is let through on the guess that it is a record.
			name:          "a refused class and a record the application does not register: denied before policy is asked, and left out of the digest",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Update,
			check:         []accesstypes.Resource{"Ships", "Hangars", "Shipyrds", "Hangars.bay"},
			wantDenied:    []accesstypes.Resource{"Hangars", "Shipyrds", "Hangars.bay"},
			wantDelegated: []accesstypes.Resource{"Ships"},
			holds: accesstypes.PermissionDigest{
				"Ships":       {accesstypes.Update: accesstypes.DigestGranted},
				"Hangars":     {accesstypes.Update: accesstypes.DigestGranted, accesstypes.Read: accesstypes.DigestGranted},
				"Hangars.bay": {accesstypes.Update: accesstypes.DigestGranted},
			},
			wantDigest: accesstypes.PermissionDigest{"Ships": {accesstypes.Update: accesstypes.DigestGranted}},
		},
		{
			// The question is of the name, not of the permission asked: Shipyards
			// registers Update alone and its list is policy's all the same, and so is a
			// read of an action of no class.
			name:          "a refused class and a permission a record does not register: the name is the application's, so it is policy's",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.List,
			check:         []accesstypes.Resource{"Shipyards", "Shipyards.dock", "Launch"},
			wantDelegated: []accesstypes.Resource{"Shipyards", "Shipyards.dock", "Launch"},
			wantDigest:    without(map[accesstypes.Resource][]accesstypes.Permission{"SetPassword": {accesstypes.Execute}, "UnlinkIdentity": {accesstypes.Execute}, "RenameUser": {accesstypes.Execute}}),
		},
		{
			name:          "a record the whole application registers is the application's in a check for a tenant",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseClass("money_movement")),
			classes:       maskTestClasses(),
			scope:         accesstypes.DomainScope("tenant"),
			perm:          accesstypes.Update,
			check:         []accesstypes.Resource{"Ships", "Hangars"},
			wantDenied:    []accesstypes.Resource{"Hangars"},
			wantDelegated: []accesstypes.Resource{"Ships"},
			wantDigest:    without(map[accesstypes.Resource][]accesstypes.Permission{"WireFunds": {accesstypes.Execute}}),
		},
		{
			name:          "a mask that refuses no class leaves a record the application does not register to policy",
			mask:          accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("Launch")),
			classes:       maskTestClasses(),
			scope:         accesstypes.GlobalScope(),
			perm:          accesstypes.Update,
			check:         []accesstypes.Resource{"Hangars", "Ships"},
			wantDelegated: []accesstypes.Resource{"Hangars", "Ships"},
			holds: accesstypes.PermissionDigest{
				"Hangars": {accesstypes.Update: accesstypes.DigestGranted},
			},
			wantDigest: accesstypes.PermissionDigest{"Hangars": {accesstypes.Update: accesstypes.DigestGranted}},
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

			holds := tt.holds
			if holds == nil {
				holds = maskTestDigest()
			}
			stub := &stubPermissions{user: "bob", digest: holds, domains: []accesstypes.Domain{"tenant"}}
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

// standardLogReaders shares the standard logger's output among the tests that read it.
// The logger is the process's: two tests that each put a buffer of their own on it take
// each other's lines when they run side by side, and the first to finish hands the
// other's lines back to the terminal. So the first test to ask takes the output, the
// last to finish gives it back, and each looks for its own lines among whatever else
// the package's tests log meanwhile.
type standardLogReaders struct {
	mu       sync.Mutex
	readers  int
	previous io.Writer
	output   *lockedBuffer
}

// take answers the buffer the standard logger writes until the test, and every other
// test that took it, has finished.
func (s *standardLogReaders) take(t *testing.T) *lockedBuffer {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.readers == 0 {
		s.previous = log.Writer()
		s.output = new(lockedBuffer)
		log.SetOutput(s.output)
	}
	s.readers++
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		s.readers--
		if s.readers == 0 {
			log.SetOutput(s.previous)
		}
	})

	return s.output
}

// standardLog is the one taking of the standard logger's output the package's tests
// share.
var standardLog standardLogReaders

// TestMasked_denialIsLoggedAsTheMasks pins the evidence of a mask's denial: the line
// says the mask denied it, names the mask, the person, the resource and which part of
// the mask refused. The standard logger is the process's: the test reads the output
// the package's tests share (standardLog) and looks for each case's own line among
// whatever else they log meanwhile.
func TestMasked_denialIsLoggedAsTheMasks(t *testing.T) {
	t.Parallel()

	output := standardLog.take(t)

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
		{
			name: "a refused class and an action the application does not register",
			mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			perm: accesstypes.Execute,
			res:  "Decommission",
			want: "impersonation: Execute denied by mask (*;-class:credential) for a mask-log person on Decommission: " + unknownActionWhy("Decommission"),
		},
		{
			name: "a refused class and a record the application does not register",
			mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential)),
			perm: accesstypes.Update,
			res:  "Hangars.bay",
			want: "impersonation: Update denied by mask (*;-class:credential) for a mask-log person on Hangars.bay: " + unknownNameWhy("Hangars", accesstypes.Update),
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

// unknownActionWhy is why a mask that refuses a class refuses Execute on a name that is
// no action of the application.
func unknownActionWhy(action string) string {
	return `the mask refuses a class of actions and "` + action + `" is no action of the application, so the classes it carries cannot be known: ` +
		"register it by hand (@manualAddResource(Execute) on the constant, with the @class it carries) or correct the name"
}

// unknownNameWhy is why a mask that refuses a class refuses a permission other than
// Execute on a name the application registers nothing under.
func unknownNameWhy(name string, perm accesstypes.Permission) string {
	return `the mask refuses a class of actions and the application registers nothing under "` + name + `", so what the name is cannot be known: ` +
		"register it by hand (@manualAddResource(" + string(perm) + ") on the constant) or correct the name"
}

// maskedSession is the context of a session that acts as person under mask, started by
// the person named starter.
func maskedSession(person accesstypes.User, mask accesstypes.PermissionMask) context.Context {
	return sessionCtx("starter", &sessioninfo.Impersonation{Actor: "starter", Principal: accesstypes.UserPrincipal(person), Mask: mask})
}

// TestMaskRefusesAction pins the mask's part of a check asked on its own, for an action
// no grant admits: a session with no mask is refused nothing, an ordinary one and an
// impersonated one alike, and a mask refuses the action when it does not keep Execute,
// when it refuses a class the action carries in either scope, or when it refuses the
// action by name. A mask that refuses a class is not judged on a guess: with no classes
// it refuses the action, and it refuses an action the application does not register,
// which a mask that refuses no class leaves alone.
func TestMaskRefusesAction(t *testing.T) {
	t.Parallel()

	classes := stubClasses{
		accesstypes.GlobalPermissionScope: {"CreateTenant": {accesstypes.ClassAccess}},
		accesstypes.DomainPermissionScope: {"MakeTenantDirectoryOnly": {accesstypes.ClassAccess}},
	}
	refusesAccess := accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassAccess))

	tests := []struct {
		name    string
		ctx     context.Context
		classes ActionClasses
		action  accesstypes.Resource
		wantWhy string
		want    bool
	}{
		{
			name:    "a session of the person's own",
			ctx:     sessionCtx("alice", nil),
			classes: classes,
			action:  "CreateTenant",
		},
		{
			name:    "a session that acts as a person with no mask",
			ctx:     maskedSession(aliceUser, accesstypes.AllowAll()),
			classes: classes,
			action:  "CreateTenant",
		},
		{
			name:    "a mask that refuses the action's class",
			ctx:     maskedSession(aliceUser, refusesAccess),
			classes: classes,
			action:  "CreateTenant",
			wantWhy: "the mask refuses the class access",
			want:    true,
		},
		{
			name:    "a mask that refuses the class of an action a tenant's scope registers",
			ctx:     maskedSession(aliceUser, refusesAccess),
			classes: classes,
			action:  "MakeTenantDirectoryOnly",
			wantWhy: "the mask refuses the class access",
			want:    true,
		},
		{
			name:    "a mask that refuses another class",
			ctx:     maskedSession(aliceUser, accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential))),
			classes: classes,
			action:  "CreateTenant",
		},
		{
			name:    "a mask that refuses the action by name",
			ctx:     maskedSession(aliceUser, accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("CreateTenant"))),
			classes: classes,
			action:  "CreateTenant",
			wantWhy: "the mask refuses Execute on CreateTenant",
			want:    true,
		},
		{
			name:    "a mask that refuses another action by name",
			ctx:     maskedSession(aliceUser, accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("WireFunds"))),
			classes: classes,
			action:  "CreateTenant",
		},
		{
			name:    "a mask that does not keep Execute",
			ctx:     maskedSession(aliceUser, accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read, accesstypes.Create)),
			classes: classes,
			action:  "CreateTenant",
			wantWhy: "the mask does not keep Execute",
			want:    true,
		},
		{
			name:    "a mask that refuses a class, judged with no classes",
			ctx:     maskedSession(aliceUser, refusesAccess),
			action:  "CreateTenant",
			wantWhy: "the mask refuses a class of actions and the application handed the check no classes (resource.SessionPermissions takes the generated collection)",
			want:    true,
		},
		{
			name:    "a mask that refuses a class, asked about an action the application does not register",
			ctx:     maskedSession(aliceUser, refusesAccess),
			classes: classes,
			action:  "CreateWorkspace",
			wantWhy: unknownActionWhy("CreateWorkspace"),
			want:    true,
		},
		{
			name:    "a mask that refuses a class the action does not carry, asked about an action the application does not register",
			ctx:     maskedSession(aliceUser, accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassCredential))),
			classes: classes,
			action:  "CreatTenant",
			wantWhy: unknownActionWhy("CreatTenant"),
			want:    true,
		},
		{
			name:    "a mask that refuses no class, asked about an action the application does not register",
			ctx:     maskedSession(aliceUser, accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("WireFunds"))),
			classes: classes,
			action:  "CreateWorkspace",
		},
		{
			name:    "a session of the person's own, asked about an action the application does not register",
			ctx:     sessionCtx("alice", nil),
			classes: classes,
			action:  "CreateWorkspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			why, refused := MaskRefusesAction(tt.ctx, tt.classes, tt.action)
			if refused != tt.want || why != tt.wantWhy {
				t.Errorf("MaskRefusesAction() = %q, %t; want %q, %t", why, refused, tt.wantWhy, tt.want)
			}
		})
	}
}

// TestMaskRefusesAction_generatedCollection pins the rule over the collection an
// application hands the check, the generated one: under a mask that refuses a class, an
// action it registers is judged by the classes it carries (the action no role is given
// among them, which is registered for this check alone), and a name it registers no
// Execute on is refused, a record's name included.
func TestMaskRefusesAction_generatedCollection(t *testing.T) {
	t.Parallel()

	collection, _ := noRoleCollection(t)
	ctx := maskedSession(aliceUser, accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassAccess)))

	tests := []struct {
		name    string
		action  accesstypes.Resource
		wantWhy string
		want    bool
	}{
		{name: "the action no role is given, which carries the class", action: "CreateShop", wantWhy: "the mask refuses the class access", want: true},
		{name: "an action that carries no class", action: "Launch"},
		{name: "a name the collection does not hold", action: "CreateShopp", wantWhy: unknownActionWhy("CreateShopp"), want: true},
		{name: "a record's name, which is no action", action: "Widgets", wantWhy: unknownActionWhy("Widgets"), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			why, refused := MaskRefusesAction(ctx, collection, tt.action)
			if refused != tt.want || why != tt.wantWhy {
				t.Errorf("MaskRefusesAction() = %q, %t; want %q, %t", why, refused, tt.wantWhy, tt.want)
			}
		})
	}
}

// TestMasked_generatedCollection_records pins the rule for a record over the collection
// an application hands the check, the generated one. Under a mask that refuses a class,
// every permission the collection lists is left to policy, a record's and each of its
// fields' alike, as is a permission of a record's that another of its routes registers
// and one none does: only an action that carries the class is the mask's. A permission
// on a name the collection does not hold is refused before policy is asked, whichever
// of the record permissions it is.
func TestMasked_generatedCollection_records(t *testing.T) {
	t.Parallel()

	b := NewCollectionBuilder()
	for _, action := range []accesstypes.Resource{"CreateShop", "Launch"} {
		if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, action); err != nil {
			t.Fatalf("AddMethodResource(%s) error = %v", action, err)
		}
	}
	b.SetActionClasses(accesstypes.GlobalPermissionScope, "CreateShop", []accesstypes.ActionClass{accesstypes.ClassAccess})
	fields := []FieldTags{{Field: "ID", JSON: "id"}, {Field: "Name", JSON: "name"}}
	for scope, sets := range map[accesstypes.PermissionScope]map[accesstypes.Resource][]accesstypes.Permission{
		accesstypes.GlobalPermissionScope: {"Shelves": {accesstypes.List}, "Shelf": {accesstypes.Read}},
		accesstypes.DomainPermissionScope: {"Widgets": {accesstypes.List}, "Widget": {accesstypes.Create, accesstypes.Update, accesstypes.Delete}},
	} {
		for res, perms := range sets {
			set, err := NewSetData(fields, perms...)
			if err != nil {
				t.Fatalf("NewSetData(%s) error = %v", res, err)
			}
			if err := b.AddResourceSet(scope, res, set); err != nil {
				t.Fatalf("AddResourceSet(%s) error = %v", res, err)
			}
		}
	}
	collection, err := NewGeneratedCollection(b.Data())
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	mask := accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassAccess))
	// asked answers what policy was asked about by a check of perm on names under the
	// mask, in a tenant's scope and in the whole application's.
	asked := func(t *testing.T, scope accesstypes.Scope, perm accesstypes.Permission, names ...accesstypes.Resource) []accesstypes.Resource {
		t.Helper()

		stub := &stubPermissions{user: "bob"}
		if _, err := Masked(stub, mask, collection).Check(context.Background(), accesstypes.NewEnvironment(), scope, perm, names...); err != nil {
			t.Fatalf("Check(%s, %v) error = %v", perm, names, err)
		}

		return stub.checkResources
	}
	scopes := []accesstypes.Scope{accesstypes.GlobalScope(), accesstypes.DomainScope("tenant")}

	listed := collection.List()
	if len(listed[accesstypes.Update]) == 0 || len(listed[accesstypes.Read]) == 0 || len(listed[accesstypes.Execute]) == 0 {
		t.Fatalf("the collection lists %v, want records, their fields and actions", listed)
	}
	for perm, names := range listed {
		for _, name := range names {
			for _, scope := range scopes {
				want := []accesstypes.Resource{name}
				if name == "CreateShop" {
					want = nil
				}
				if diff := cmp.Diff(want, asked(t, scope, perm, name)); diff != "" {
					t.Errorf("%s on %s in %s: what policy was asked about mismatch (-want +got):\n%s", perm, name, scope, diff)
				}
			}
		}
	}

	records := []accesstypes.Permission{accesstypes.Create, accesstypes.Read, accesstypes.List, accesstypes.Update, accesstypes.Delete}
	for _, perm := range records {
		for _, scope := range scopes {
			// Shelf registers Read alone: its other permissions are policy's too.
			if diff := cmp.Diff([]accesstypes.Resource{"Shelf", "Shelf.name"}, asked(t, scope, perm, "Shelf", "Shelf.name")); diff != "" {
				t.Errorf("%s on Shelf in %s: what policy was asked about mismatch (-want +got):\n%s", perm, scope, diff)
			}
			if got := asked(t, scope, perm, "Shelfs", "Shelfs.name", "widgets"); len(got) != 0 {
				t.Errorf("%s on names the collection does not hold, in %s: policy was asked about %v, want the mask to refuse them first", perm, scope, got)
			}
		}
	}
}

// TestRefuseActionUnderMask pins the refusal a handler makes of an action no grant
// admits: under a mask that refuses the action the person is answered 403 with one
// sentence, what the session cannot do, in the words the handler gave; why the mask
// refuses it is kept for the server's log and told to no one, and the log carries the
// line the masked check writes for a denial of its own, naming the person acted as; a
// session with no mask is refused nothing and logs nothing. The standard logger is the process's: the test
// reads the output the package's tests share (standardLog), and each case names a
// person no other test of the package names.
func TestRefuseActionUnderMask(t *testing.T) {
	t.Parallel()

	output := standardLog.take(t)

	classes := stubClasses{accesstypes.GlobalPermissionScope: {"CreateTenant": {accesstypes.ClassAccess}}}

	tests := []struct {
		name string
		ctx  context.Context
		// action is the action the handler asks about, with act, what the session
		// cannot do in the answer's words; creating a tenant when empty.
		action accesstypes.Resource
		act    string
		// wantSays is the one sentence the answer says, wantWhy the mask's reason,
		// which the answer keeps for the server's log and tells no one, and wantLogged
		// the log's line; all empty for a session the mask refuses nothing.
		wantSays   string
		wantWhy    string
		wantLogged string
		// person is who the case's session acts as, looked for in the log of a case
		// that is refused nothing.
		person string
	}{
		{
			name:   "a session of the person's own",
			ctx:    sessionCtx("an unmasked-route person", nil),
			person: "an unmasked-route person",
		},
		{
			name:   "a session that acts as a person with no mask",
			ctx:    maskedSession("a maskless-route person", accesstypes.AllowAll()),
			person: "a maskless-route person",
		},
		{
			name:       "a mask that refuses the action's class",
			ctx:        maskedSession("a class-route person", accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassAccess))),
			wantSays:   "You are acting as someone else, and this session cannot create a tenant.",
			wantWhy:    "the mask refuses the class access",
			wantLogged: "impersonation: Execute denied by mask (*;-class:access) for a class-route person on CreateTenant: the mask refuses the class access",
		},
		{
			name:       "a mask that refuses the action by name",
			ctx:        maskedSession("a name-route person", accesstypes.AllowAll().Refusing(accesstypes.RefuseAction("CreateTenant"))),
			wantSays:   "You are acting as someone else, and this session cannot create a tenant.",
			wantWhy:    "the mask refuses Execute on CreateTenant",
			wantLogged: "impersonation: Execute denied by mask (*;-on:Execute:CreateTenant) for a name-route person on CreateTenant: the mask refuses Execute on CreateTenant",
		},
		{
			name:       "a mask that does not keep Execute",
			ctx:        maskedSession("a kind-route person", accesstypes.MaskPermissions(accesstypes.DenyAll(), accesstypes.List, accesstypes.Read, accesstypes.Create)),
			wantSays:   "You are acting as someone else, and this session cannot create a tenant.",
			wantWhy:    "the mask does not keep Execute",
			wantLogged: "impersonation: Execute denied by mask (Create,List,Read) for a kind-route person on [CreateTenant]",
		},
		{
			name: "a session that acts as a role under a mask: the log names the person who started it",
			ctx: sessionCtx("starter", &sessioninfo.Impersonation{
				Actor: "a role-route person", Principal: accesstypes.RolePrincipal("Auditor"), Mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassAccess)),
			}),
			wantSays:   "You are acting as someone else, and this session cannot create a tenant.",
			wantWhy:    "the mask refuses the class access",
			wantLogged: "impersonation: Execute denied by mask (*;-class:access) for a role-route person on CreateTenant: the mask refuses the class access",
		},
		{
			// The handler asks about an action the application never registered: under
			// a mask that refuses a class the session is refused, and the log says which
			// name is no action and what to do about it, as the answer's cause does.
			name:       "a mask that refuses a class, asked about an action the application does not register",
			ctx:        maskedSession("an unregistered-route person", accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassAccess))),
			action:     "CreateWorkspace",
			act:        "create a workspace",
			wantSays:   "You are acting as someone else, and this session cannot create a workspace.",
			wantWhy:    unknownActionWhy("CreateWorkspace"),
			wantLogged: "impersonation: Execute denied by mask (*;-class:access) for an unregistered-route person on CreateWorkspace: " + unknownActionWhy("CreateWorkspace"),
		},
		{
			name:   "a session of the person's own, asked about an action the application does not register",
			ctx:    sessionCtx("an unmasked unregistered-route person", nil),
			action: "CreateWorkspace",
			act:    "create a workspace",
			person: "an unmasked unregistered-route person",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			action, act := tt.action, tt.act
			if action == "" {
				action, act = "CreateTenant", "create a tenant"
			}
			err := RefuseActionUnderMask(tt.ctx, classes, action, act)
			if tt.wantSays == "" {
				if err != nil {
					t.Errorf("RefuseActionUnderMask() error = %v, want none: the session has no mask", err)
				}
				if got := output.String(); strings.Contains(got, tt.person) {
					t.Errorf("log = %q, want no line about %s: no mask denied anything", got, tt.person)
				}

				return
			}
			plainAnswer(t, err, httpio.HasForbidden, tt.wantSays, tt.wantWhy, "start one that may, or stop acting as them first")
			if got := output.String(); !strings.Contains(got, tt.wantLogged) {
				t.Errorf("log = %q, want a line carrying %q", got, tt.wantLogged)
			}
		})
	}
}

// TestMasked_generatedCollection_writeClasses pins what a mask that refuses a class
// does with the writes of records that carry it. The generator gives the class access
// to the create, the change and the delete of a record that decides what a person may
// do, and to the change of a record such a value is read through, so the mask refuses
// each of those before policy is asked, a field of the record with the record, in the
// record's own scope and in the other. What carries no class is policy's: the same
// record read or listed, the create and the delete of the record a value is read
// through, and every write of a record that decides nothing. A mask that refuses
// another class leaves them all to policy.
func TestMasked_generatedCollection_writeClasses(t *testing.T) {
	t.Parallel()

	b := NewCollectionBuilder()
	if err := b.AddMethodResource(accesstypes.GlobalPermissionScope, accesstypes.Execute, "WireFunds"); err != nil {
		t.Fatalf("AddMethodResource(WireFunds) error = %v", err)
	}
	b.SetActionClasses(accesstypes.GlobalPermissionScope, "WireFunds", []accesstypes.ActionClass{"money_movement"})
	fields := []FieldTags{{Field: "ID", JSON: "id"}, {Field: "Name", JSON: "name"}}
	all := []accesstypes.Permission{accesstypes.List, accesstypes.Read, accesstypes.Create, accesstypes.Update, accesstypes.Delete}
	routes := [][]accesstypes.Permission{{accesstypes.List}, {accesstypes.Read}, {accesstypes.Create, accesstypes.Update, accesstypes.Delete}}
	for _, record := range []accesstypes.Resource{"Seats", "Teams", "Shelves"} {
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
	access := []accesstypes.ActionClass{accesstypes.ClassAccess}
	for _, write := range []accesstypes.Permission{accesstypes.Create, accesstypes.Update, accesstypes.Delete} {
		b.SetWriteClasses(accesstypes.DomainPermissionScope, "Seats", write, access)
	}
	b.SetWriteClasses(accesstypes.DomainPermissionScope, "Teams", accesstypes.Update, access)
	collection, err := NewGeneratedCollection(b.Data())
	if err != nil {
		t.Fatalf("NewGeneratedCollection() error = %v", err)
	}

	if diff := cmp.Diff([]accesstypes.ActionClass{accesstypes.ClassAccess, "money_movement"}, collection.ClassesInUse()); diff != "" {
		t.Errorf("ClassesInUse() mismatch (-want +got):\n%s", diff)
	}
	wantWrites := []string{"Create on Seats", "Update on Seats", "Delete on Seats", "Update on Teams"}
	if diff := cmp.Diff(wantWrites, collection.WritesOfClass(accesstypes.ClassAccess)); diff != "" {
		t.Errorf("WritesOfClass(access) mismatch (-want +got):\n%s", diff)
	}
	if got := collection.ActionsOfClass(accesstypes.ClassAccess); len(got) != 0 {
		t.Errorf("ActionsOfClass(access) = %v, want none: the class is carried by the writes of records alone here", got)
	}
	if diff := cmp.Diff(b.Data(), collection.Data()); diff != "" {
		t.Errorf("the collection read back from its data differs from what the builder holds (-builder +read back):\n%s", diff)
	}

	tests := []struct {
		name string
		mask accesstypes.PermissionMask
		// refused are the checks the mask answers itself, as "<Permission> on <name>".
		refused []string
	}{
		{
			name: "a mask that refuses the class access",
			mask: accesstypes.AllowAll().Refusing(accesstypes.RefuseClass(accesstypes.ClassAccess)),
			refused: []string{
				"Create on Seats", "Update on Seats", "Update on Seats.name", "Delete on Seats",
				"Update on Teams", "Update on Teams.name",
			},
		},
		{
			name:    "a mask that refuses another class",
			mask:    accesstypes.AllowAll().Refusing(accesstypes.RefuseClass("money_movement")),
			refused: []string{"Execute on WireFunds"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var refused []string
			for _, scope := range []accesstypes.Scope{accesstypes.GlobalScope(), accesstypes.DomainScope("tenant")} {
				for _, record := range []accesstypes.Resource{"Seats", "Teams", "Shelves"} {
					for _, perm := range all {
						names := []accesstypes.Resource{record}
						if perm == accesstypes.Update {
							names = append(names, record+".name")
						}
						for _, name := range names {
							stub := &stubPermissions{user: "bob"}
							if _, err := Masked(stub, tt.mask, collection).Check(context.Background(), accesstypes.NewEnvironment(), scope, perm, name); err != nil {
								t.Fatalf("Check(%s, %s) error = %v", perm, name, err)
							}
							if len(stub.checkResources) == 0 && scope.IsGlobal() {
								refused = append(refused, string(perm)+" on "+string(name))
							}
							if asked := len(stub.checkResources) != 0; asked == slices.Contains(tt.refused, string(perm)+" on "+string(name)) {
								t.Errorf("%s on %s in %s: policy asked = %t, and the mask %s", perm, name, scope, asked, tt.name)
							}
						}
					}
				}
			}
			stub := &stubPermissions{user: "bob"}
			if _, err := Masked(stub, tt.mask, collection).Check(context.Background(), accesstypes.NewEnvironment(), accesstypes.GlobalScope(), accesstypes.Execute, "WireFunds"); err != nil {
				t.Fatalf("Check(Execute, WireFunds) error = %v", err)
			}
			if len(stub.checkResources) == 0 {
				refused = append(refused, "Execute on WireFunds")
			}
			slices.Sort(refused)
			want := slices.Clone(tt.refused)
			slices.Sort(want)
			if diff := cmp.Diff(want, refused); diff != "" {
				t.Errorf("what the mask refused before policy was asked mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestNewGeneratedCollection_writeClasses pins what a collection refuses of the classes
// a record's writes carry when it is read: a class on a permission that is no write, a
// class on a write of a name that registers Execute, a write listed twice, and a class
// that is no class's name or is carried twice.
func TestNewGeneratedCollection_writeClasses(t *testing.T) {
	t.Parallel()

	access := []accesstypes.ActionClass{accesstypes.ClassAccess}
	record := []accesstypes.Permission{accesstypes.Read, accesstypes.Update}
	tests := []struct {
		name    string
		res     CollectionResource
		wantErr string
	}{
		{
			name: "a class on reading a record",
			res:  CollectionResource{Name: "Seats", Scope: accesstypes.GlobalPermissionScope, Permissions: record, WriteClasses: []WriteClassData{{Permission: accesstypes.Read, Classes: access}}},
			wantErr: `resource "Seats" carries the classes [access] on Read, which is no write of a record: ` +
				`a record's Create, Update and Delete carry a class, and an action carries its own (Classes)`,
		},
		{
			name: "a class on a write of an action",
			res: CollectionResource{
				Name: "Launch", Scope: accesstypes.GlobalPermissionScope, Permissions: []accesstypes.Permission{accesstypes.Execute},
				WriteClasses: []WriteClassData{{Permission: accesstypes.Update, Classes: access}},
			},
			wantErr: `resource "Launch" carries the classes [access] on Update and registers Execute: ` +
				`a record's writes carry a class, and a name that registers Execute is an action, which carries its own (Classes)`,
		},
		{
			name: "a write listed twice",
			res: CollectionResource{
				Name: "Seats", Scope: accesstypes.GlobalPermissionScope, Permissions: record,
				WriteClasses: []WriteClassData{{Permission: accesstypes.Update, Classes: access}, {Permission: accesstypes.Update, Classes: access}},
			},
			wantErr: `resource "Seats" lists the classes of Update twice`,
		},
		{
			name:    "a class that is no class's name",
			res:     CollectionResource{Name: "Seats", Scope: accesstypes.GlobalPermissionScope, Permissions: record, WriteClasses: []WriteClassData{{Permission: accesstypes.Update, Classes: []accesstypes.ActionClass{"Access"}}}},
			wantErr: `resource "Seats" carries the class "Access" on Update, which is not a class's name: a lowercase letter, then lowercase letters, digits and underscores`,
		},
		{
			name: "a class carried twice",
			res: CollectionResource{
				Name: "Seats", Scope: accesstypes.GlobalPermissionScope, Permissions: record,
				WriteClasses: []WriteClassData{{Permission: accesstypes.Update, Classes: []accesstypes.ActionClass{accesstypes.ClassAccess, accesstypes.ClassAccess}}},
			},
			wantErr: `resource "Seats" carries the class "access" on Update twice`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewGeneratedCollection(CollectionData{Resources: []CollectionResource{tt.res}})
			if err == nil || !strings.HasSuffix(err.Error(), "error="+tt.wantErr) {
				t.Errorf("NewGeneratedCollection() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
