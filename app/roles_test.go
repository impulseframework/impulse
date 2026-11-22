package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestRolePolicyScan(t *testing.T) {
	t.Parallel()

	const auth = `package staff

import (
	_ "embed"

	"github.com/impulseframework/impulse/access"
)

//go:embed roles.json
var roleFile access.RoleFile

func Roles() access.RoleFile { return roleFile }

func New(store access.Store, collection access.PermissionCollection) (*access.Client, error) {
	return access.New(store, collection, access.WithDefaultRoles(Roles(), Name))
}
`
	const data = `package config

import (
	"github.com/impulseframework/impulse/access"

	crewauth "example.com/harbor/pkg/auth/crew"
	"example.com/harbor/pkg/router"
)

func engines(store access.Store) error {
	if _, err := access.New(store, router.Collection(), access.WithDefaultRoles(crewauth.Roles(), crewauth.Name)); err != nil {
		return err
	}
	if _, err := access.New(store, router.Collection(), access.WithDefaultRoles(loadRoles(), Name)); err != nil {
		return err
	}
	_, err := access.New(store, router.Collection(), access.WithDefaultRoles())

	return err
}
`
	const variable = `package crew

import (
	_ "embed"

	"github.com/impulseframework/impulse/access"
)

//go:embed roles.json
var roleFile access.RoleFile

func Roles() access.RoleFile { return roleFile }

func New(store access.Store, collection access.PermissionCollection, auth string) (*access.Client, error) {
	return access.New(store, collection, access.WithDefaultRoles(Roles(), auth))
}
`
	const otherName = `package config

import (
	"github.com/impulseframework/impulse/access"

	"example.com/harbor/pkg/auth/members"
	"example.com/harbor/pkg/auth/staff"
	"example.com/harbor/pkg/router"
)

func engines(store access.Store) error {
	if _, err := access.New(store, router.Collection(), access.WithDefaultRoles(staff.Roles(), members.Name)); err != nil {
		return err
	}
	if _, err := access.New(store, router.Collection(), access.WithDefaultRoles(staff.Roles(), "staff")); err != nil {
		return err
	}
	_, err := access.New(store, router.Collection(), access.WithDefaultRoles(staff.Roles(), authName()))

	return err
}
`
	// placed is an auth package handing its role file over in the shapes the check tells
	// apart: an option held in a variable, appended to a slice and returned, none of them
	// among an access.New call's arguments; a Name the function declares for itself, which
	// hides the package's; and a Roles the function takes as a parameter. The package's
	// Name is a constant of this file.
	const placed = `package staff

import (
	_ "embed"

	"github.com/impulseframework/impulse/access"

	"example.com/harbor/pkg/auth/members"
)

//go:embed roles.json
var roleFile access.RoleFile

const Name = "staff"

func Roles() access.RoleFile { return roleFile }

func assigned() access.Option {
	option := access.WithDefaultRoles(Roles(), Name)

	return option
}

func appended(options []access.Option) []access.Option {
	return append(options, access.WithDefaultRoles(Roles(), Name))
}

func returned() access.Option {
	return access.WithDefaultRoles(Roles(), Name)
}

func shadowed(store access.Store, collection access.PermissionCollection) (*access.Client, error) {
	Name := members.Name

	return access.New(store, collection, access.WithDefaultRoles(Roles(), Name))
}

func handed(store access.Store, collection access.PermissionCollection, Roles func() access.RoleFile) (*access.Client, error) {
	return access.New(store, collection, access.WithDefaultRoles(Roles(), Name))
}
`
	const deploy = `package deploy

import (
	"context"

	"github.com/impulseframework/impulse/access"
)

func CheckRoles(ctx context.Context, client *access.Client) error {
	_, err := client.CheckPolicy(ctx)

	return err
}
`
	const migrate = `package main

import "context"

func run(ctx context.Context, data *Data) error {
	if _, err := data.Staff().Access().CheckPolicy(ctx); err != nil {
		return err
	}

	return nil
}
`
	tests := []struct {
		name       string
		files      map[string]string
		wantRoles  []DefaultRoles
		wantChecks []PolicyCheck
	}{
		{
			name:  "the role file handed over unqualified, qualified, and in other shapes",
			files: map[string]string{"pkg/auth/staff/staff.go": auth, "pkg/config/data.go": data},
			wantRoles: []DefaultRoles{
				{File: "pkg/auth/staff/staff.go", Line: 15, RolesPackage: "example.com/harbor/pkg/auth/staff", NamePackage: "example.com/harbor/pkg/auth/staff", NameText: "Name", InEngine: true},
				{File: "pkg/config/data.go", Line: 11, RolesPackage: "example.com/harbor/pkg/auth/crew", NamePackage: "example.com/harbor/pkg/auth/crew", NameText: "crewauth.Name", InEngine: true},
				{File: "pkg/config/data.go", Line: 14, NamePackage: "example.com/harbor/pkg/config", NameText: "Name", InEngine: true},
				{File: "pkg/config/data.go", Line: 17, InEngine: true},
			},
		},
		{
			name:  "the role file handed over with the auth's name in a variable",
			files: map[string]string{"pkg/auth/crew/crew.go": variable},
			wantRoles: []DefaultRoles{
				{File: "pkg/auth/crew/crew.go", Line: 15, RolesPackage: "example.com/harbor/pkg/auth/crew", NameText: "auth", InEngine: true},
			},
		},
		{
			name:  "the role file handed over under another package's name, under a name written out, and under a call's answer",
			files: map[string]string{"pkg/config/data.go": otherName},
			wantRoles: []DefaultRoles{
				{File: "pkg/config/data.go", Line: 12, RolesPackage: "example.com/harbor/pkg/auth/staff", NamePackage: "example.com/harbor/pkg/auth/members", NameText: "members.Name", InEngine: true},
				{File: "pkg/config/data.go", Line: 15, RolesPackage: "example.com/harbor/pkg/auth/staff", NameText: `"staff"`, InEngine: true},
				{File: "pkg/config/data.go", Line: 18, RolesPackage: "example.com/harbor/pkg/auth/staff", NameText: "authName()", InEngine: true},
			},
		},
		{
			name:  "the role file handed over outside an access.New call, under a Name the function declares, and as a Roles the function takes",
			files: map[string]string{"pkg/auth/staff/staff.go": placed},
			wantRoles: []DefaultRoles{
				{File: "pkg/auth/staff/staff.go", Line: 19, RolesPackage: "example.com/harbor/pkg/auth/staff", NamePackage: "example.com/harbor/pkg/auth/staff", NameText: "Name"},
				{File: "pkg/auth/staff/staff.go", Line: 25, RolesPackage: "example.com/harbor/pkg/auth/staff", NamePackage: "example.com/harbor/pkg/auth/staff", NameText: "Name"},
				{File: "pkg/auth/staff/staff.go", Line: 29, RolesPackage: "example.com/harbor/pkg/auth/staff", NamePackage: "example.com/harbor/pkg/auth/staff", NameText: "Name"},
				{File: "pkg/auth/staff/staff.go", Line: 35, RolesPackage: "example.com/harbor/pkg/auth/staff", NameText: "Name", InEngine: true},
				{File: "pkg/auth/staff/staff.go", Line: 39, NamePackage: "example.com/harbor/pkg/auth/staff", NameText: "Name", InEngine: true},
			},
		},
		{
			name:  "the policy checks, through the client and through an accessor",
			files: map[string]string{"pkg/deploy/deploy.go": deploy, "cmd/deployment/migrate/main.go": migrate},
			wantChecks: []PolicyCheck{
				{File: "cmd/deployment/migrate/main.go", Line: 6},
				{File: "pkg/deploy/deploy.go", Line: 10},
			},
		},
		{
			name:  "test files are not read",
			files: map[string]string{"pkg/auth/staff/staff_test.go": auth, "pkg/deploy/deploy_test.go": deploy},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			files := map[string]string{"go.mod": "module example.com/harbor\n\ngo 1.26\n"}
			for rel, content := range tt.files {
				files[rel] = content
			}
			for rel, content := range files {
				abs := filepath.Join(root, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			a, err := Discover(root)
			if err != nil {
				t.Fatalf("Discover() error = %v", err)
			}
			if diff := cmp.Diff(tt.wantRoles, a.DefaultRoles); diff != "" {
				t.Errorf("DefaultRoles mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tt.wantChecks, a.PolicyChecks); diff != "" {
				t.Errorf("PolicyChecks mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
