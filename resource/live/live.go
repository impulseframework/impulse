// Package live is the server side of live pages: a page that asked for a live list or
// a live row stays current without polling and without refetching while nothing
// changed, and a page mounted again inside a short window is served by the browser
// with no request. The server holds no push connection and keeps scaling to zero.
//
// A request opts in by carrying the subscribe header (X-Subscribe: <tab>). The
// generated list and read handlers register the subscription (who, which tab, which
// resource, which key or domain, until when) before they run the query, so a commit
// that lands during the query is not missed; a refused request registers nothing. A
// mutation publishes, after its commit and before its answer, one change document
// per subscriber into that user's change set, which the browser listens to and
// answers with a refetch carrying the change's timestamp as the version parameter
// (_v); a list or read response to such a request carries Cache-Control so a remount
// inside the window is the browser's own. Every document a write publishes carries
// the write's id, and the write's answer names the id and the documents that went
// into the caller's own change set (ChangesHeader), so the tab that wrote fetches at
// once and knows its own change when the change set reports it. Subscriptions expire
// unless the tab renews them through the renew route, where the server re-checks each
// against the user's grants; a tab unsubscribes when it leaves, and a logout
// unsubscribes everything and revokes the browser's identity.
//
// A subscription and a change set belong to a principal id (PrincipalID): the name of the
// auth the request came through, the separator "|", and the session principal's user name
// or its role marked "role:" (crew|alice, members|alice@example.com, crew|role:Auditor).
// The auth's name is what the outlet's generated routes bind (Subscribing), so two auths
// over one database never share a change set when a password user name equals a
// directory's email; the id is the Firebase uid the browser signs in as, so one longer
// than Firebase's limit is refused, never shortened.
//
// A row is named by its key as the read route spells it (resource.RowKey): a single
// key is its value's string form, a compound key is its parts joined with "/" in route
// order, the order the generated routes take them. The subscription record's key
// field, a renewal's key, and a change document's key all carry exactly that form, so
// the browser that addressed the row by its route and the publisher that saw the patch
// spell it identically.
//
// The service also carries the application's signals between its instances
// (Signaler, Subscriber): one signals document per application with a field per kind
// (features, tenants, policy), a signal of a kind writing that kind's field and waking
// every instance's subscriptions to the kind. The feature flags ride the features kind.
//
// The package holds the seams (SubscriptionRecord, ChangePublisher, Identity, Signaler,
// Subscriber, bundled as Service), the fan-out that every publisher implementation
// shares (Fanout), an in-memory Fake for tests, the handler-side glue the generated
// code calls (Subscribing, Refusing, Subscribe, SetCacheControl, Publish), and the
// three route handlers (RenewHandler, UnsubscribeHandler, TokenHandler). Every
// application wires a Service; the Firestore implementation is the firestore
// subpackage.
package live

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/resource"
)

// The names the client library mirrors.
const (
	// SubscribeHeader carries the client-minted tab id on a request the page asked to
	// be live. Its value is 1 to 64 characters of [A-Za-z0-9_-].
	SubscribeHeader = "X-Subscribe"
	// VersionParam is the version query parameter, the browser cache's key: the change
	// document's timestamp as unix microseconds in decimal, or a seed minted at login.
	// The decoders accept and skip it (resource.VersionParam).
	VersionParam = resource.VersionParam
	// RenewRoute, UnsubscribeRoute and TokenRoute are the live routes under a session
	// outlet's API prefix.
	RenewRoute       = "live/renew"
	UnsubscribeRoute = "live/unsubscribe"
	TokenRoute       = "live/token"
	// LogAttribute is the request-log attribute every subscribing request carries.
	LogAttribute = "subscribe"
	// ChangesHeader is the response header a write's answer carries when the write
	// changed something the caller holds live: the write's id and the change documents it
	// published into the caller's own change set (Answer), as JSON, percent-encoded so a
	// key or a domain outside ASCII travels in a header. The tab that wrote fetches what
	// the header names at once and does nothing when its change set reports the same
	// write.
	ChangesHeader = "X-Changes"
)

// The principal id: what joins the auth's name to the principal, and how long it may be.
const (
	// AuthSeparator joins the auth's name to the principal in a principal id
	// (PrincipalID). It is legal in a Firestore document id and a Firebase uid, and an
	// auth's name never carries it (Subscribing refuses one that does), so the first
	// separator ends the auth's name whatever the user name holds.
	AuthSeparator = "|"
	// MaxPrincipalIDLength is Firebase's limit on a uid, 128, counted in bytes as the
	// Admin SDK counts it. A longer principal id is refused, never truncated or hashed.
	MaxPrincipalIDLength = 128
	// rolePrefix marks a session established as a role in its principal id.
	rolePrefix = "role:"
)

// The numbers.
const (
	// RenewInterval is how often a tab renews its subscriptions.
	RenewInterval = 120 * time.Second
	// SubscriptionTTL is how long a subscription lives after it was written or renewed:
	// about twice the renewal interval, so one missed renewal does not end it.
	SubscriptionTTL = 300 * time.Second
	// ChangeTTL is how long a change document stays in a user's change set.
	ChangeTTL = 600 * time.Second
	// CacheControl is the header value a list or read response carries when its request
	// carried the version parameter.
	CacheControl = "private, max-age=300"
	// BulkThreshold is the number of touched rows per resource in one request above
	// which the publisher stops looking subscribers up row by row and writes one
	// resource document per subscribed user instead.
	BulkThreshold = 100
	// PublishTimeout bounds a request's publish: past it the failure is logged and the
	// request answers.
	PublishTimeout = 2 * time.Second
	// RecordTimeout bounds a request's calls on the subscription record and the identity
	// (register, renew, unsubscribe, revoke, token): past it the call fails as any
	// failure of it does, so a backend that stops answering never holds a request open.
	RecordTimeout = 5 * time.Second
	// MaxChangesHeaderLength is the longest ChangesHeader value a write's answer
	// carries, in bytes. A write that reached more of the caller's subscriptions than
	// fit answers the write's id with All set, and the tab fetches everything it holds
	// live, so the header never outgrows what a proxy in front of the application
	// accepts in a response.
	MaxChangesHeaderLength = 2048
)

// tabPattern is the shape of a tab id.
var tabPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ValidTab reports whether tab has the shape the header carries.
func ValidTab(tab string) bool {
	return tabPattern.MatchString(tab)
}

// Subscription is one live interest of one tab: a row (Key set, Domain empty) or a
// list (Key empty, Domain the tenant domain of a domain-scoped resource, empty for a
// global one). Key is the row's key as the read route spells it: a single key's string
// form, a compound key's parts joined with "/" in route order (resource.RowKey). Domain
// is never a permission concept in the record: it says which list a list subscription
// is of, and nothing else.
//
// A list is held over a set of tenants. One tenant's list is the set of that one tenant,
// its Domain. A list across tenants (Across) is one subscription over the set the
// server worked out for the person: the tenants the session reaches in which the
// person may list the resource, narrowed by the page's filter on the tenant. It is one
// record per tab and list however many tenants the set holds, and the same record again
// at each renewal, with the set as the renewal worked it out. The publisher asks who is
// subscribed to the list in the tenant a write was made in, and the record answers from
// the sets (InTenant).
type Subscription struct {
	Principal string
	Tab       string
	Resource  accesstypes.Resource
	Key       string
	Domain    accesstypes.Domain
	// Across is set on the subscription of a list across tenants, and holds its set.
	Across *TenantSet
	Expiry time.Time
}

// TenantSet is what the subscription of a list across tenants is held over. Narrowed
// is the page's filter on the tenant, nil when the page asked for every tenant: with
// the resource it says which list the subscription is of. Domains is the set of tenants
// the subscription covers, as the server worked it out.
type TenantSet struct {
	Narrowed []accesstypes.Domain
	Domains  []accesstypes.Domain
}

// RowSubscription is the subscription a read registers: the row by resource and key.
func RowSubscription(res accesstypes.Resource, key string) *Subscription {
	return &Subscription{Resource: res, Key: key}
}

// ListSubscription is the subscription a list registers: the resource in the request's
// domain, which is empty for a global resource.
func ListSubscription(res accesstypes.Resource, domain accesstypes.Domain) *Subscription {
	return &Subscription{Resource: res, Domain: domain}
}

// AcrossTenantsSubscription is the subscription a list across tenants registers: the
// resource's list over the set of tenants the server worked out for the person, and the
// page's filter on the tenant (nil for every tenant), which says which list it is.
func AcrossTenantsSubscription(res accesstypes.Resource, set, narrowed []accesstypes.Domain) *Subscription {
	return &Subscription{Resource: res, Across: &TenantSet{Narrowed: narrowed, Domains: set}}
}

// IsRow reports whether the subscription names a row.
func (s *Subscription) IsRow() bool {
	return s.Key != ""
}

// Normalized returns the subscription as the record keeps it: a row subscription
// carries no domain, and only a list across tenants carries a set of its own, sorted,
// each tenant once, under no one domain.
func (s *Subscription) Normalized() Subscription {
	normalized := *s
	if normalized.IsRow() {
		normalized.Domain = ""
	}
	if normalized.IsRow() || normalized.Across == nil {
		normalized.Across = nil

		return normalized
	}
	normalized.Domain = ""
	set := &TenantSet{Domains: sortedSet(normalized.Across.Domains)}
	if normalized.Across.Narrowed != nil {
		set.Narrowed = sortedSet(normalized.Across.Narrowed)
	}
	normalized.Across = set

	return normalized
}

// sortedSet returns the tenants sorted, each once, never nil.
func sortedSet(domains []accesstypes.Domain) []accesstypes.Domain {
	set := slices.Clone(domains)
	if set == nil {
		set = []accesstypes.Domain{}
	}
	slices.Sort(set)

	return slices.Compact(set)
}

// Tenants is the set of tenants the record keeps the subscription under: the set of a
// list across tenants, the one tenant of a tenant's list, and the empty domain alone
// for a global resource's list and for a row.
func (s *Subscription) Tenants() []accesstypes.Domain {
	normalized := s.Normalized()
	if normalized.Across != nil {
		return normalized.Across.Domains
	}

	return []accesstypes.Domain{normalized.Domain}
}

// InTenant returns the subscription as the publisher is answered for one tenant: a row
// or one tenant's list as the record keeps it, and a list across tenants as that
// tenant's list, which is what a change in the tenant names. ok is false when the
// tenant is not in the subscription's set.
func (s *Subscription) InTenant(domain accesstypes.Domain) (sub Subscription, ok bool) {
	normalized := s.Normalized()
	if !slices.Contains(normalized.Tenants(), domain) {
		return Subscription{}, false
	}

	return Subscription{
		Principal: normalized.Principal,
		Tab:       normalized.Tab,
		Resource:  normalized.Resource,
		Key:       normalized.Key,
		Domain:    domain,
		Expiry:    normalized.Expiry,
	}, true
}

// PerTenant returns the subscription once for each tenant of its set (InTenant): what
// the record answers the publisher's question about a whole resource with.
func (s *Subscription) PerTenant() []Subscription {
	tenants := s.Tenants()
	subs := make([]Subscription, 0, len(tenants))
	for _, domain := range tenants {
		if sub, ok := s.InTenant(domain); ok {
			subs = append(subs, sub)
		}
	}

	return subs
}

// The words a list across tenants is named by in a subscription's id.
const (
	everyTenant  = "every tenant"
	onlyTenants  = "only:"
	acrossMarker = "\x00across tenants\x00"
)

// ID is the subscription's document id: the first 32 hex characters of
// sha256("principal|tab|resource|key|domain"), so the same interest of the same tab is
// one document however often it is written. A list across tenants is named by its
// resource and the page's filter on the tenant, never by the set the server worked out,
// so a renewal that comes to another set writes the same document.
func (s *Subscription) ID() string {
	normalized := s.Normalized()
	name := normalized.Principal + "|" + normalized.Tab + "|" + string(normalized.Resource) + "|" + normalized.Key + "|" + string(normalized.Domain)
	if normalized.Across != nil {
		name += acrossMarker + narrowingName(normalized.Across.Narrowed)
	}
	sum := sha256.Sum256([]byte(name))

	return hex.EncodeToString(sum[:])[:32]
}

// narrowingName names the page's filter on the tenant in a subscription's id: every
// tenant for a page that sent none, the tenants it named otherwise.
func narrowingName(narrowed []accesstypes.Domain) string {
	if narrowed == nil {
		return everyTenant
	}
	names := make([]string, 0, len(narrowed))
	for _, domain := range narrowed {
		names = append(names, string(domain))
	}

	return onlyTenants + strings.Join(names, ",")
}

// TokenPayload is what the token route answers: how the browser connects to the
// user's change set. Against the emulator Token is empty and Emulator names the host;
// in production Emulator is empty and Token is a Firebase custom token for UID.
type TokenPayload struct {
	UID      string `json:"uid"`
	Token    string `json:"token"`
	Project  string `json:"project"`
	Database string `json:"database"`
	APIKey   string `json:"apiKey"`
	Emulator string `json:"emulator"`
}

// ChangeKind is what a change document reports: one row, one list, or a whole resource.
type ChangeKind string

// The change kinds.
const (
	RowChange      ChangeKind = "row"
	ListChange     ChangeKind = "list"
	ResourceChange ChangeKind = "resource"
)

// ChangeDocument is one entry of a user's change set, as the publisher writes it and
// the browser reads it: a row document carries the key and whether the row was
// deleted, a list document the domain and no key, a resource document neither. The
// implementation adds the server timestamp (at), the expiry (expires) and the id of
// the write that published it (write, Writes.ID); a document several writes within one
// second became carries the last one's. The JSON form is how a write's answer names a
// document (Answer).
type ChangeDocument struct {
	Kind     ChangeKind           `json:"kind"`
	Resource accesstypes.Resource `json:"resource"`
	Key      string               `json:"key,omitempty"`
	Domain   accesstypes.Domain   `json:"domain,omitempty"`
	Deleted  bool                 `json:"deleted,omitempty"`
}

// ID is the document's id for the given unix second: row|<resource>|<key>|<second>,
// list|<resource>|<domain>|<second>, or resource|<resource>|<second>. Writes to one
// target within one second share an id and coalesce; a key's "/" (a compound key) is
// escaped, since a document id cannot carry one.
func (d ChangeDocument) ID(second int64) string {
	sec := strconv.FormatInt(second, 10)
	switch d.Kind {
	case RowChange:
		return string(RowChange) + "|" + string(d.Resource) + "|" + escapeKey(d.Key) + "|" + sec
	case ListChange:
		return string(ListChange) + "|" + string(d.Resource) + "|" + string(d.Domain) + "|" + sec
	default:
		return string(ResourceChange) + "|" + string(d.Resource) + "|" + sec
	}
}

// target is what two documents must share to coalesce: everything but the deleted flag.
func (d ChangeDocument) target() ChangeDocument {
	d.Deleted = false

	return d
}

// escapeKey makes a row key fit a document id segment.
func escapeKey(key string) string {
	escaped := make([]byte, 0, len(key))
	for i := range len(key) {
		if key[i] == '/' {
			escaped = append(escaped, "%2F"...)

			continue
		}
		escaped = append(escaped, key[i])
	}

	return string(escaped)
}

// Changes are the change documents one publish writes, per principal, in the order the
// fan-out produced them.
type Changes map[string][]ChangeDocument
