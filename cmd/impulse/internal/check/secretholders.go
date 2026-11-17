package check

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/impulseframework/impulse/app"
)

// secretHolders verifies that a retirement of a version of the application secret reads
// every resources package: each package carrying a generated list of the resources that
// store a secret (SecretHolders(), zz_gen_secret_holders.go) is handed to every
// resource.RetireSecretVersion call outside tests. The call lists the packages by hand,
// so a package left out is never read, and the retirement still says that no stored
// value names the version, which the deployment then disables.
type secretHolders struct{}

// secretHoldersName is the check's name.
const secretHoldersName = "secret-holders"

func (secretHolders) Name() string {
	return secretHoldersName
}

func (secretHolders) Describe() string {
	return "every resources package's generated secret holders (SecretHolders()) are handed to the retirement of a version of the application secret (resource.RetireSecretVersion), so no package's stored secrets go unread"
}

// Meaning explains the obligation for the handoff brief.
func (secretHolders) Meaning() string {
	return "A version of the application secret is disabled once no stored value names it, and the migrate command's `-retire-secret` proves that: it calls `resource.RetireSecretVersion` with the application's secret holders, writes every value that names the version again under the current one, and reads every holder once more. The holders are what the generator writes into each resources package (`SecretHolders()` in `zz_gen_secret_holders.go`, nil for a package that stores no secret), and the call names the packages: one package's `resources.SecretHolders()...`, or several joined, `slices.Concat(consoleresources.SecretHolders(), sharedresources.SecretHolders())...`. A package the call does not name is never read. Each line under the check names a call and a package it is not handed, a call whose holders come from where the check cannot read them (a parameter, a function of the application's own, a list written by hand), or an application whose packages no code retires."
}

func (c secretHolders) Run(_ context.Context, env *Env) Result {
	a := env.App
	if len(a.SecretHolders) == 0 {
		return skip(c.Name(), "no resources package carries generated secret holders (zz_gen_secret_holders.go)")
	}
	holders := slices.SortedFunc(slices.Values(a.SecretHolders), func(x, y app.SecretHolders) int {
		return strings.Compare(x.Package, y.Package)
	})
	packages := make([]string, 0, len(holders))
	for _, h := range holders {
		packages = append(packages, h.Package)
	}
	if len(a.SecretRetirements) == 0 {
		return fail(c.Name(), "no code retires a version of the application secret",
			fmt.Sprintf("resource.RetireSecretVersion is called nowhere outside tests, so the stored secrets of %s are never written under a new version and no version can be disabled: call it from the deploy package with %s, and run it from the migrate command's -retire-secret flag, as a skeleton's pkg/deploy and cmd/deployment/migrate do", strings.Join(packages, ", "), c.holdersArgument(a, packages)))
	}

	var details []string
	for _, r := range a.SecretRetirements {
		details = append(details, c.callFindings(a, r, holders, packages)...)
	}
	if len(details) > 0 {
		return fail(c.Name(), fmt.Sprintf("%d problem(s) with the secret holders a retirement is handed", len(details)), details...)
	}
	calls := make([]string, 0, len(a.SecretRetirements))
	for _, r := range a.SecretRetirements {
		calls = append(calls, fmt.Sprintf("%s:%d", r.File, r.Line))
	}

	return pass(c.Name(), fmt.Sprintf("%d package(s) with generated secret holders, each handed to the retirement at %s", len(packages), strings.Join(calls, ", ")))
}

// callFindings are the findings for one retirement call: the packages it is not handed,
// and holders it takes from where the check cannot read them.
func (c secretHolders) callFindings(a *app.App, r app.SecretRetirement, holders []app.SecretHolders, packages []string) []string {
	var findings []string
	for _, h := range holders {
		if slices.Contains(r.Packages, h.Package) {
			continue
		}
		// A package that stores no secret today is held to the same call: its list
		// fills the day a field of its resources is declared a secret.
		consequence := "a retirement never reads the secrets that package stores and still reports the version retired"
		if !h.Stored {
			consequence = "a secret that package comes to store would never be read by a retirement (its list is empty today)"
		}
		findings = append(findings, fmt.Sprintf("%s:%d: resource.RetireSecretVersion is not handed %s.SecretHolders(), so %s; pass every resources package's generated list in the call: %s", r.File, r.Line, h.Package, consequence, c.holdersArgument(a, packages)))
	}
	if r.Unread {
		findings = append(findings, fmt.Sprintf("%s:%d: resource.RetireSecretVersion takes holders from where they cannot be read here (a parameter, a function of the application's own, or a list written by hand), so whether every package is among them is not known; name each package's generated SecretHolders() in the call: %s", r.File, r.Line, c.holdersArgument(a, packages)))
	}

	return findings
}

// holdersArgument spells the argument that hands a retirement every package's holders:
// the one package's list, or the lists joined.
func (secretHolders) holdersArgument(a *app.App, packages []string) string {
	module := ""
	if a.GoMod != nil && a.GoMod.Module != nil {
		module = a.GoMod.Module.Mod.Path + "/"
	}
	calls := make([]string, 0, len(packages))
	for _, p := range packages {
		calls = append(calls, strings.TrimPrefix(p, module)+".SecretHolders()")
	}
	if len(calls) == 1 {
		return calls[0] + "..."
	}

	return "slices.Concat(" + strings.Join(calls, ", ") + ")..."
}
