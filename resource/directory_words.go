package resource

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/impulseframework/impulse/session/sessioninfo"
	"github.com/impulseframework/impulse/session/signin"
)

// How a sentence names a directory. A directory is a sign-in method and the connection
// its identities carry, and a connection is a key: for a Microsoft Entra tenant it is
// the tenant's id, which no person knows. So a sentence a person reads never says the
// connection itself. It says the name the application's sign-in registrations give the
// organization, where one does (a listed Workspace or tenant, a provider), and
// otherwise the directory by the button a person signs in to it with ("your
// organization's Microsoft directory"). The same words name a tenant's directory
// wherever a person reads of it: the warning before a tenant is made directory only,
// the warning before it is opened to its directory's people, the refusals of the
// directory's rules, and the reason a tenant is closed to a person in their tenant
// list.

// DirectoryNamer answers what the sign-in registrations of one auth call a directory:
// name is the name a registration that takes the directory's connection gives it, ""
// where none names it, and label is the label of the button of the registration a
// person signs in to it through, "" where the auth's login page offers none. The
// generated identity client of an auth supplies it (DirectoryName), from the session
// library's own answer (session.AuthAPI.DirectoryName); an application names none.
type DirectoryNamer func(directory signin.Directory) (name, label string)

// DirectoryWords says a directory as the middle of a sentence names it to a person: the
// name the first namer that knows one gives it ("Lakeside", "acme.example"), and where
// none does, the directory by the first label a namer answers ("your organization's
// Microsoft directory"). Where no namer answers anything, an application whose
// management is not generated or a directory no registration takes any more, it reads
// what the directory alone says: a Google Workspace's connection is its name or its
// domain, an OpenID Connect provider's is the application's name for it or its issuer,
// said by its host, and any other is said by its kind. It never says a tenant id.
func DirectoryWords(directory signin.Directory, namers ...DirectoryNamer) string {
	name, byLabel := directorySaid(directory, namers)
	if name != "" {
		return name
	}

	return byLabel
}

// DirectoryName is DirectoryWords with the kind of directory before a name, as a
// sentence that first speaks of a directory says it: "Google Workspace acme.example",
// "Microsoft Entra tenant Lakeside", "OpenID Connect provider id.acme.example". A
// directory nobody has a name for is said by its label as DirectoryWords says it,
// which names the kind already.
func DirectoryName(directory signin.Directory, namers ...DirectoryNamer) string {
	name, byLabel := directorySaid(directory, namers)
	if name == "" {
		return byLabel
	}

	return directoryKind(directory.Method) + " " + name
}

// directorySaid answers the name the namers give the directory, or, where they give
// none, the words that say it by a label (byLabel); exactly one of the two is set.
func directorySaid(directory signin.Directory, namers []DirectoryNamer) (name, byLabel string) {
	var label string
	for _, namer := range namers {
		if namer == nil {
			continue
		}
		named, labeled := namer(directory)
		if named != "" {
			return named, ""
		}
		if label == "" {
			label = labeled
		}
	}
	if label != "" {
		return "", directoryByLabel(label)
	}

	// No registration answered: what the directory alone says.
	switch directory.Method {
	case sessioninfo.MethodGoogle:
		// The connection is the application's name for the Workspace, or its domain.
		if directory.Connection != "" {
			return directory.Connection, ""
		}

		return "", directoryByLabel("Google")
	case sessioninfo.MethodOIDC:
		// The connection is the application's name for the provider, or its issuer.
		if directory.Connection != "" {
			return hostOf(directory.Connection), ""
		}

		return "", directoryByLabel("")
	case sessioninfo.MethodAzure:
		// The connection is the tenant's id, which is never said.
		return "", directoryByLabel("Microsoft")
	default:
		return "", directoryByLabel("")
	}
}

// directoryByLabel says a directory nobody has a name for by the label of the button a
// person signs in to it with: "your organization's Microsoft directory" for the label
// "Microsoft". A label that ends in the word directory already is not given it twice,
// and no label at all is "your organization's directory".
func directoryByLabel(label string) string {
	label = strings.TrimSpace(label)
	switch {
	case label == "":
		return "your organization's directory"
	case strings.HasSuffix(strings.ToLower(label), "directory"):
		return "your organization's " + label
	default:
		return "your organization's " + label + " directory"
	}
}

// directoryKind is the kind of directory a sign-in method reaches, as a sentence says
// it before the directory's name.
func directoryKind(method sessioninfo.AuthMethod) string {
	switch method {
	case sessioninfo.MethodGoogle:
		return "Google Workspace"
	case sessioninfo.MethodAzure:
		return "Microsoft Entra tenant"
	case sessioninfo.MethodWorkOS:
		return "WorkOS connection"
	case sessioninfo.MethodOIDC:
		return "OpenID Connect provider"
	default:
		return "Directory"
	}
}

// hostOf is the host of an absolute URL, and the text itself where it is no such URL:
// an OpenID Connect provider's connection is its issuer unless the application named
// it, and a person knows an issuer by its host.
func hostOf(connection string) string {
	if u, err := url.Parse(connection); err == nil && u.IsAbs() && u.Host != "" {
		return u.Host
	}

	return connection
}

// startSentence is words with their first letter in upper case, for the words that
// name a directory by its label at the start of a sentence ("Your organization's
// Microsoft directory is ..."). A name with its kind before it starts in upper case
// already.
func startSentence(words string) string {
	first, size := utf8.DecodeRuneInString(words)
	if first == utf8.RuneError {
		return words
	}

	return string(unicode.ToUpper(first)) + words[size:]
}
