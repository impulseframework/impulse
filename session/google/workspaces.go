package google

import (
	"slices"
	"strings"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/session/internal/util"
	"github.com/impulseframework/impulse/session/signin"
)

// AnyAccountConnection is the connection written on the link of a Google account that
// is in none of the registration's named Workspaces and is admitted by AnyGoogleAccount:
// the provider itself, since such an account belongs to no organization the
// application names. A Workspace account admitted by AnyWorkspace carries its domain.
// It is one of the two values that are no organization's directory
// (signin.Directory.PersonalAccounts), so no tenant takes it as its own.
const AnyAccountConnection = signin.GoogleAnyAccountConnection

// anyWorkspaceAccount is the hd hint that asks Google's account chooser for any
// Workspace account, sent when a registration admits more than one domain.
const anyWorkspaceAccount = "*"

// personalDomains are the domains of Google accounts outside any Workspace, which
// AnyWorkspace does not admit and only AnyGoogleAccount does. They are Google's own
// mail: the one kind of address outside a Workspace that Google answers for (vouchesFor).
var personalDomains = []string{"gmail.com", "googlemail.com"}

// vouchesFor reports whether Google's word that an address is verified (the token's
// email_verified) confirms the address: it does only where Google is the address's own
// authority. That is the address of a Workspace account, which the organization that
// owns the domain gave the person (the token carries hd), and an address of Google's
// own mail (personalDomains), told by its ASCII domain alone: a domain written with a
// letter from outside ASCII is not Google's mail, whatever Unicode lowercases the letter
// to (signin.AddressDomain). A Google account can also be made with an address from
// anywhere else, and of that address Google says no more than any provider says of
// one: its word confirms nothing and reaches no account. The account's sign-in name is
// that address all the same, so a first sign-in under the name of an account created
// ahead waits for the code mailed to the address that account holds, and a first
// sign-in to any account with no confirmed address waits for the mailed code.
func vouchesFor(emailVerified bool, email, hostedDomain string) bool {
	if !emailVerified {
		return false
	}

	return hostedDomain != "" || slices.Contains(personalDomains, signin.AddressDomain(email))
}

// WorkspaceSet is the organization slot of the Google method (Config.Workspaces): which
// Google accounts a registration admits, and the connection each identity link carries.
// It is built with Workspace, Workspaces, AnyWorkspace and AnyGoogleAccount; the zero
// value admits no one and is refused at registration. The verified ID token's hd claim,
// the domain of the account's Workspace, selects the entry: a named Workspace whose
// domains contain it, else any Workspace account when AnyWorkspace is in the set, else
// any account when AnyGoogleAccount is; an account no entry admits is refused
// (not_workspace_member).
type WorkspaceSet struct {
	named        []namedWorkspace
	anyWorkspace bool
	anyAccount   bool
	// attestsOpen records Attesting() called on a set with an open entry, which
	// validate refuses: an open entry admits organizations the application does not
	// administer.
	attestsOpen bool
}

// namedWorkspace is one Workspace the application names, with the domains it owns,
// and whether the application declares it as enforcing a second factor.
type namedWorkspace struct {
	name    string
	domains []string
	attests bool
}

// Workspace is one Google Workspace organization: name is the application's own name
// for it, written as the connection of every identity link made through it, and
// domains are the domains the Workspace owns, one or more, checked against the
// verified hd claim. Google puts no stable organization id in the token and one
// Workspace answers for every domain it owns, so the application names the Workspace
// rather than the library keying on a domain. The name is a key: changing it makes
// every sign-in from the Workspace arrive as an unknown identity until the links are
// re-keyed (see the README's "Google Workspace OIDC").
func Workspace(name string, domains ...string) WorkspaceSet {
	return WorkspaceSet{named: []namedWorkspace{{name: name, domains: domains}}}
}

// Workspaces combines sets: several named Workspaces, each with its own connection, or
// named Workspaces beside AnyWorkspace or AnyGoogleAccount, so the Workspaces the
// application knows carry their names and the rest fall through to the open entry. A
// domain belongs to one named Workspace only, and a name is used once.
func Workspaces(sets ...WorkspaceSet) WorkspaceSet {
	var union WorkspaceSet
	for _, s := range sets {
		union.named = append(union.named, s.named...)
		union.anyWorkspace = union.anyWorkspace || s.anyWorkspace
		union.anyAccount = union.anyAccount || s.anyAccount
		union.attestsOpen = union.attestsOpen || s.attestsOpen
	}

	return union
}

// Attesting declares that the Workspaces named in the set enforce a second factor at
// every sign-in, which the application vouches for because it administers them (the
// Workspace's 2-Step Verification enforcement): a sign-in through one of them carries
// the attestation by declaration (sessioninfo.AttestedByDeclaration), recorded as the
// session's attested-by-declaration event, which satisfies the sign-in policy's
// RequireMFA without a code. Google puts no evidence of a second factor in its tokens,
// so the declaration is the only way a Google sign-in attests. It is for named
// Workspaces only: AnyWorkspace and AnyGoogleAccount admit organizations the
// application does not administer, and a set holding either is refused at
// registration; name the administered Workspace beside the open entry instead,
// google.Workspaces(google.Workspace("staff", "example.com").Attesting(), google.AnyGoogleAccount()).
func (s WorkspaceSet) Attesting() WorkspaceSet {
	named := make([]namedWorkspace, len(s.named))
	for i, w := range s.named {
		named[i] = namedWorkspace{name: w.name, domains: w.domains, attests: true}
	}

	return WorkspaceSet{named: named, anyWorkspace: s.anyWorkspace, anyAccount: s.anyAccount, attestsOpen: s.attestsOpen || s.open()}
}

// AnyWorkspace admits an account of any Google Workspace organization: a token with any
// hd claim, none refused. The link's connection is the account's domain, lowercased,
// since the registration names no Workspace; a Workspace whose users should share one
// connection across its domains is named with Workspace beside this entry. Opening the
// method to sign-up then admits anyone with a Google Workspace account, which the
// warning an administrator confirms says.
func AnyWorkspace() WorkspaceSet {
	return WorkspaceSet{anyWorkspace: true}
}

// AnyGoogleAccount admits any Google account, in a Workspace or not: a token with or
// without an hd claim. The email must be verified, as always, since it is the sign-in
// name. The link's connection is AnyAccountConnection for an account outside the named
// Workspaces. The OAuth client's consent screen must be External and published for
// accounts outside your own Workspace to reach the callback; Internal restricts it to
// your Workspace. Opening the method to sign-up then admits anyone with a Google
// account.
func AnyGoogleAccount() WorkspaceSet {
	return WorkspaceSet{anyAccount: true}
}

// What identifies whom the slot admits, as an audience's key spells it: a named
// Workspace by each of its domains, the accounts of every Workspace, and every Google
// account.
const (
	identityWorkspaceDomain = "google-workspace-domain:"
	identityAnyWorkspace    = "google:any-workspace"
	identityAnyAccount      = "google:any-account"
)

// audience is how wide the slot is, for the page where a method is opened to sign-up:
// the domains of the Workspaces it names, and what an account outside them is called
// when the slot admits any.
//
// What the audience is compared by is its identities: each domain of a named
// Workspace, in lower case, never the name the application gives the Workspace, which
// another Workspace can be given in its place. A slot that admits any Workspace, or
// any Google account, admits its named Workspaces' people among everyone else's, so
// its identity is the widest open entry alone.
func (s WorkspaceSet) audience() signin.Audience {
	var a signin.Audience
	for _, w := range s.named {
		a.Organizations = append(a.Organizations, w.domains...)
	}
	switch {
	case s.anyAccount:
		a.Accounts = "a Google account"
		a.Identities = []string{identityAnyAccount}
	case s.anyWorkspace:
		a.Accounts = "a Google Workspace account"
		a.Identities = []string{identityAnyWorkspace}
	default:
		for _, domain := range a.Organizations {
			a.Identities = append(a.Identities, identityWorkspaceDomain+util.LowerASCII(domain))
		}
	}

	return a
}

// validate is the slot's startup check: it refuses an empty slot, a Workspace without a
// name or a domain, a value that is not a domain name, a name used twice and a domain
// in two Workspaces, each naming the field.
func (s WorkspaceSet) validate() error {
	if len(s.named) == 0 && !s.anyWorkspace && !s.anyAccount {
		return errors.New("Workspaces is required: google.Workspace(name, domains...) for one Workspace, google.Workspaces(...) for several, " +
			"google.AnyWorkspace() for any Workspace account, google.AnyGoogleAccount() for any Google account")
	}
	if s.attestsOpen {
		return errors.New("Workspaces: Attesting() declares the Workspaces the application administers; any Workspace and any Google account admit organizations it does not, " +
			"so name the administered Workspace beside the open entry: google.Workspaces(google.Workspace(name, domains...).Attesting(), google.AnyGoogleAccount())")
	}
	names := map[string]bool{}
	domains := map[string]string{}
	for _, w := range s.named {
		if strings.TrimSpace(w.name) == "" {
			return errors.New("Workspaces: a Workspace needs a name, the application's own name for the organization, which keys every link made through it")
		}
		if names[strings.ToLower(w.name)] {
			return errors.Newf("Workspaces: the name %q is used twice", w.name)
		}
		names[strings.ToLower(w.name)] = true
		if len(w.domains) == 0 {
			return errors.Newf("Workspaces: Workspace %q needs at least one domain, checked against the verified hd claim", w.name)
		}
		for _, d := range w.domains {
			if !isDomainName(d) {
				return errors.Newf("Workspaces: %q is not a domain name", d)
			}
			key := util.LowerASCII(d)
			if other, taken := domains[key]; taken {
				return errors.Newf("Workspaces: the domain %q belongs to two Workspaces, %q and %q", d, other, w.name)
			}
			domains[key] = w.name
		}
	}

	return nil
}

// isDomainName reports whether d can be a domain name an hd claim carries: not empty,
// and without spaces, control characters or the characters of a URL or an address.
func isDomainName(d string) bool {
	if d == "" || d != strings.TrimSpace(d) {
		return false
	}

	return !strings.ContainsFunc(d, func(c rune) bool { return c <= ' ' || c == 0x7f || strings.ContainsRune(`/@:*?#\`, c) })
}

// open reports whether the set admits accounts outside the named Workspaces: any
// Workspace account or any Google account.
func (s WorkspaceSet) open() bool {
	return s.anyWorkspace || s.anyAccount
}

// one returns the named Workspace when the set is exactly one and nothing else, which
// is the shape role synchronization reads groups of.
func (s WorkspaceSet) one() (namedWorkspace, bool) {
	if len(s.named) != 1 || s.open() {
		return namedWorkspace{}, false
	}

	return s.named[0], true
}

// connectionFor is the connection of a link made by an account whose verified hd claim
// is hd, and whether the set admits the account at all: the name of the Workspace whose
// domains contain hd, the lowercased domain under AnyWorkspace, AnyAccountConnection
// under AnyGoogleAccount. Domain names are compared without regard to case. An account
// outside a Workspace carries no hd claim and reaches AnyGoogleAccount only.
func (s WorkspaceSet) connectionFor(hd string) (string, bool) {
	connection, _, ok := s.entryFor(hd)

	return connection, ok
}

// entryFor is connectionFor with whether the entry that admits the account is a
// Workspace declared Attesting(): a named Workspace the application administers. The
// open entries never attest.
func (s WorkspaceSet) entryFor(hd string) (connection string, attests, ok bool) {
	if hd != "" {
		for _, w := range s.named {
			for _, d := range w.domains {
				if util.EqualASCIIFold(hd, d) {
					return w.name, w.attests, true
				}
			}
		}
		if s.anyWorkspace {
			return util.LowerASCII(hd), false, true
		}
	}
	if s.anyAccount {
		return AnyAccountConnection, false, true
	}

	return "", false, false
}

// nameOf answers what a person calls the organization behind a link's connection: the
// application's name for a named Workspace, which is the connection its links carry,
// and the domain of an account admitted as any Workspace, which is the connection
// too. It answers "" for the connection of an account in no Workspace
// (AnyAccountConnection), which is no organization, and for a connection the set does
// not admit.
func (s WorkspaceSet) nameOf(connection string) string {
	if connection == AnyAccountConnection || !s.admitsConnection(connection) {
		return ""
	}

	return connection
}

// admitsConnection reports whether an account whose link carries connection could
// sign in through the set and reach that link: whether connectionFor would write the
// same connection for it today. A named Workspace's link carries the Workspace's name
// and stands while the set names it. A link made under AnyGoogleAccount carries
// AnyAccountConnection and stands while the set admits any account. Any other
// connection is the domain of an account admitted under AnyWorkspace: it stands while
// the set admits any Workspace and no named Workspace has taken the domain, since an
// account of that domain would now be written under the Workspace's name and miss the
// link.
func (s WorkspaceSet) admitsConnection(connection string) bool {
	if connection == "" {
		return false
	}
	if connection == AnyAccountConnection {
		return s.anyAccount
	}
	for _, w := range s.named {
		if w.name == connection {
			return true
		}
	}
	if !s.anyWorkspace {
		return false
	}
	for _, w := range s.named {
		if slices.ContainsFunc(w.domains, func(d string) bool { return util.EqualASCIIFold(d, connection) }) {
			return false
		}
	}

	return true
}

// admitsAddress reports whether the set could admit an account whose email address is
// at domain, which the core asks before it sends an invitation there: a domain of a
// named Workspace, any domain but a personal account's under AnyWorkspace, any domain
// under AnyGoogleAccount. Domain names are compared without regard to the case of their
// ASCII letters. Google
// puts the Workspace's primary domain in the hd claim, so an operator who names the
// primary domain only and invites an address at a secondary domain of the same
// Workspace is refused here although Google would admit the sign-in: they add the
// secondary domain to the Workspace.
func (s WorkspaceSet) admitsAddress(domain string) bool {
	if s.anyAccount {
		return true
	}
	for _, w := range s.named {
		if slices.ContainsFunc(w.domains, func(d string) bool { return util.EqualASCIIFold(d, domain) }) {
			return true
		}
	}

	return s.anyWorkspace && !slices.ContainsFunc(personalDomains, func(d string) bool { return util.EqualASCIIFold(d, domain) })
}

// hdHint is the hd parameter of the authorization request, and whether one is sent: the
// domain itself when the set is one Workspace with one domain, so Google's chooser is
// narrowed to it; any Workspace account when the set admits several domains or any
// Workspace; none when any Google account is admitted. It is a hint only; the verifier
// enforces the claim.
func (s WorkspaceSet) hdHint() (string, bool) {
	if s.anyAccount {
		return "", false
	}
	if w, ok := s.one(); ok && len(w.domains) == 1 {
		return w.domains[0], true
	}

	return anyWorkspaceAccount, true
}

// simulatedHostedDomain is the hd claim the simulated sign-in asserts under the skipAuth
// build tag: the first domain of the first named Workspace, a reserved name under
// AnyWorkspace, and none under AnyGoogleAccount alone.
func (s WorkspaceSet) simulatedHostedDomain() string {
	if len(s.named) > 0 {
		return s.named[0].domains[0]
	}
	if s.anyWorkspace {
		return "skipauth.invalid"
	}

	return ""
}
