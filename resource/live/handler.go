package live

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/go-playground/errors/v5"
	"github.com/impulseframework/impulse/accesstypes"
	"github.com/impulseframework/impulse/httpio"
	"github.com/impulseframework/impulse/logger"
	"github.com/impulseframework/impulse/resource"
	"github.com/impulseframework/impulse/session/sessioninfo"
)

// The refusals a subscribing request can meet before any handler runs.
const (
	notSessionOutletMsg    = SubscribeHeader + " is not served on this outlet: it serves no browser sessions"
	malformedTabMessageFmt = "invalid " + SubscribeHeader + " value: a tab id is 1 to 64 characters of [A-Za-z0-9_-]"
	otherGroupAuthFmt      = "this request is bound to the %s auth, and the routes it reached were mounted for the %s auth's sessions " +
		"(live.Subscribing, which an outlet's generated routes run under). A request comes through one auth, the one its outlet's group binds: " +
		"mount these routes under the %s auth's own group, or give the group's resource.BindAuth and its routes one auth's name"
)

// Tab reads the subscribe header: the tab id when the request subscribes, the empty
// string when it does not, and a 400 error when the header's value is not a tab id.
func Tab(r *http.Request) (string, error) {
	tab := r.Header.Get(SubscribeHeader)
	if tab == "" {
		return "", nil
	}
	if !ValidTab(tab) {
		return "", httpio.NewBadRequestMessage(malformedTabMessageFmt)
	}

	return tab, nil
}

// Subscribing is the middleware a session-serving outlet's generated routes run under.
// auth is the name of the auth whose sessions the outlet serves (the auth package's
// Name); every request has it bound (resource.WithAuth), so the live layer keys the
// session principal by it (PrincipalID) and the rules that keep a person from acting on
// themselves tell the caller from a person of another auth. A request its outlet's
// group bound already (resource.BindAuth) keeps the group's auth: where that is this
// auth, as it is wherever the generated router mounts both, the request passes on, and
// where it is another auth the request is refused with a server error whose cause names
// both. A request carrying the subscribe header has the tab noted on its request log
// line and is refused when the tab is malformed; a request without the header passes on
// with nothing else done. An auth name that is empty, or that carries AuthSeparator or
// a "/", panics as the routes are registered, so a server never starts with a name that
// cannot begin a principal id.
func Subscribing(auth string) func(http.Handler) http.Handler {
	if err := checkAuthName(auth); err != nil {
		panic(err.Error())
	}

	return func(next http.Handler) http.Handler {
		return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
			if err := groupAuthRefusal(r.Context(), auth); err != nil {
				return httpio.NewEncoder(w).ClientMessage(r.Context(), err)
			}
			r = r.WithContext(resource.WithAuth(r.Context(), auth))
			if r.Header.Get(SubscribeHeader) == "" {
				next.ServeHTTP(w, r)

				return nil
			}
			tab, err := Tab(r)
			if err != nil {
				return httpio.NewEncoder(w).ClientMessage(r.Context(), err)
			}
			logger.FromReq(r).AddRequestAttribute(LogAttribute, tab)
			next.ServeHTTP(w, r)

			return nil
		})
	}
}

// groupAuthRefusal refuses a request its outlet's group bound (resource.BindAuth) to an
// auth that is not the one the routes under it were mounted for; nil otherwise.
func groupAuthRefusal(ctx context.Context, auth string) error {
	if bound := resource.BoundAuth(ctx); bound != "" && bound != auth {
		return errors.Newf(otherGroupAuthFmt, bound, auth, auth)
	}

	return nil
}

// checkAuthName refuses an auth name that cannot begin a principal id: an empty one, one
// carrying AuthSeparator, which would make the boundary between the auth and the user
// name ambiguous, and one carrying "/", which no Firestore document id holds.
func checkAuthName(auth string) error {
	switch {
	case auth == "":
		return errors.New("live.Subscribing(): the auth's name is empty; pass the name of the auth whose sessions the outlet serves, the auth package's Name")
	case strings.Contains(auth, AuthSeparator):
		return errors.Newf("live.Subscribing(%q): an auth's name cannot carry %q, which separates it from the user name in a principal id", auth, AuthSeparator)
	case strings.Contains(auth, "/"):
		return errors.Newf("live.Subscribing(%q): an auth's name cannot carry \"/\", which no Firestore document id holds", auth)
	}

	return nil
}

// Refusing is the middleware an API-key outlet's generated routes run under: the
// outlet serves no browser sessions, so a request carrying the subscribe header is
// refused naming the header, after the tab is noted on its request log line. A machine
// client wanting change notification is a different consumer on a topic.
func Refusing() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return httpio.Log(func(w http.ResponseWriter, r *http.Request) error {
			if r.Header.Get(SubscribeHeader) == "" {
				next.ServeHTTP(w, r)

				return nil
			}
			logger.FromReq(r).AddRequestAttribute(LogAttribute, r.Header.Get(SubscribeHeader))

			return httpio.NewEncoder(w).ClientMessage(r.Context(), httpio.NewBadRequestMessage(notSessionOutletMsg))
		})
	}
}

// Gate is the endpoint gate of a decoded request: whether the required permission is
// granted, or granted under a condition, on the resource in the request's scope. A
// decoded QuerySet is one.
type Gate interface {
	Permitted(ctx context.Context) (bool, error)
}

// Query is a decoded list or row request as Subscribe reads it: its gate, and the
// lists the names it shows are read from (resource.QuerySet.NameSources), which the
// request registers with its own subscription. A decoded QuerySet is one.
type Query interface {
	Gate
	nameSourcer
}

// nameSourcer answers the lists a decoded request's names are read from: for each name
// the request brings into play whose grant the person holds, the pointed-to resource's
// list in the tenant the names are read in (resource.QuerySet.NameSources).
type nameSourcer interface {
	NameSources(ctx context.Context) ([]resource.NameSource, error)
}

// Subscribe registers the request's subscription when the request carries the
// subscribe header and its gate admits it, before the handler runs the query: a commit
// that lands during the query then finds the subscription. With it, in the same write
// to the record, it registers the list of each resource the names the request shows
// are read from, in the tenant they are read in, so a write to one of those resources
// tells the page as it tells the list's own page; the answer names them (NamesHeader on
// w, which the handler has not written yet), so the tab renews them with the request's
// own. A refused request registers nothing. Reading the lists is best effort: where it
// fails, the request's own subscription is registered alone and the failure is logged.
// A record that fails to take the subscriptions is logged and the request is still
// answered, with no header: the live layer is best effort end to end, and a data
// request never fails over it.
func Subscribe(ctx context.Context, w http.ResponseWriter, r *http.Request, svc Service, query Query, sub *Subscription) {
	tab, err := Tab(r)
	if err != nil || tab == "" {
		// A malformed tab was refused by the outlet's middleware before the handler ran.
		return
	}
	permitted, err := query.Permitted(ctx)
	if err != nil {
		logger.FromCtx(ctx).Errorf("live: the gate check for the %s subscription of tab %s failed; the subscription is not registered: %v", sub.Resource, tab, err)

		return
	}
	if !permitted {
		return
	}
	principal, err := PrincipalID(ctx)
	if err != nil {
		logger.FromCtx(ctx).Errorf("live: the %s subscription of tab %s is not registered; the page is served without it: %v", sub.Resource, tab, err)

		return
	}
	registered := sub.Normalized()
	stamp(&registered, principal, tab, time.Now().Add(SubscriptionTTL))
	names := namedLists(ctx, query, &registered, tab)
	ctx, cancel := context.WithTimeout(ctx, RecordTimeout)
	defer cancel()
	if err := svc.Register(ctx, withNamed(&registered, names)); err != nil {
		logger.FromCtx(ctx).Errorf("live: registering the %s subscription of tab %s failed; the page is served without it: %v", sub.Resource, tab, err)

		return
	}
	setNamesHeader(ctx, w, &registered, names)
}

// stamp sets on the subscription what one request registers it with: the principal
// and the tab, and the expiry every subscription the request registers takes.
func stamp(sub *Subscription, principal, tab string, expiry time.Time) {
	sub.Principal = principal
	sub.Tab = tab
	sub.Expiry = expiry
}

// namedLists returns the subscriptions a request registers beside its own (own, as the
// record keeps it, stamped): one list subscription for each list the request's names
// are read from, stamped as own is, each once and none that is own itself (a list of
// crew that shows each one's lead, who is crew). Each keeps the name field the renewal
// re-checks it by. A failure to read the lists is logged, and nothing is registered
// beside own.
func namedLists(ctx context.Context, query nameSourcer, own *Subscription, tab string) []namedList {
	sources, err := query.NameSources(ctx)
	if err != nil {
		logger.FromCtx(ctx).Errorf("live: reading the lists the names of the %s request of tab %s are read from failed; the request's own subscription is registered without them: %v", own.Resource, tab, err)

		return nil
	}
	seen := []string{own.ID()}
	var names []namedList
	for _, source := range sources {
		list := ListSubscription(source.Resource, source.Domain).Normalized()
		stamp(&list, own.Principal, own.Tab, own.Expiry)
		if id := list.ID(); !slices.Contains(seen, id) {
			seen = append(seen, id)
			names = append(names, namedList{sub: list, field: source.Field})
		}
	}

	return names
}

// namedList is a list a request registers because it shows names read from it: the
// subscription, and the pointed-to resource's name field as the grants name it (empty
// where the name is the resource's key).
type namedList struct {
	sub   Subscription
	field accesstypes.Tag
}

// withNamed is what one write to the record takes for a request: its own subscription,
// then the lists its names are read from.
func withNamed(own *Subscription, names []namedList) []Subscription {
	subs := make([]Subscription, 0, 1+len(names))
	subs = append(subs, *own)
	for i := range names {
		subs = append(subs, names[i].sub)
	}

	return subs
}

// Names is what NamesHeader carries: the request's own subscription as the record keeps
// it (Of), and the lists registered beside it because the request shows names read
// from them (Names), each with its name field (NameField), which the renewal re-checks
// it by. Of is a list by its resource and domain, a row by its resource and key and no
// domain, and a list across tenants by its resource, AcrossTenants and, where the page
// narrowed it, Domains.
type Names struct {
	Of    SubscriptionRequest   `json:"of"`
	Names []SubscriptionRequest `json:"names"`
}

// header renders the names as NamesHeader carries them: their JSON, percent-encoded.
func (n *Names) header() (string, error) {
	encoded, err := json.Marshal(n)
	if err != nil {
		return "", errors.Wrap(err, "json.Marshal()")
	}

	return url.PathEscape(string(encoded)), nil
}

// setNamesHeader names on the answer the lists the request registered beside its own
// subscription. A request that registered none sets nothing.
func setNamesHeader(ctx context.Context, w http.ResponseWriter, own *Subscription, names []namedList) {
	if len(names) == 0 {
		return
	}
	header := Names{Of: requestOf(own), Names: make([]SubscriptionRequest, 0, len(names))}
	for i := range names {
		named := requestOf(&names[i].sub)
		named.NameField = string(names[i].field)
		header.Names = append(header.Names, named)
	}
	value, err := header.header()
	if err != nil {
		logger.FromCtx(ctx).Errorf("live: naming the lists the names of the %s request of tab %s are read from failed; the answer is sent without them: %v", own.Resource, own.Tab, err)

		return
	}
	w.Header().Set(NamesHeader, value)
}

// requestOf is a subscription, as the record keeps it, as the renew route carries it.
func requestOf(sub *Subscription) SubscriptionRequest {
	req := SubscriptionRequest{Resource: string(sub.Resource), Key: sub.Key, Domain: string(sub.Domain)}
	if sub.Across != nil {
		req.AcrossTenants = true
		if sub.Across.Narrowed != nil {
			narrowed := make([]string, 0, len(sub.Across.Narrowed))
			for _, domain := range sub.Across.Narrowed {
				narrowed = append(narrowed, string(domain))
			}
			req.Domains = strings.Join(narrowed, ",")
		}
	}

	return req
}

// AcrossTenantsList is a decoded list across tenants as the live layer reads it: its
// resource, the set of tenants a subscription to it is held over with the page's
// filter on the tenant (nil for every tenant), and the lists the names it shows are
// read from. A QuerySet decoded for a list across tenants is one
// (resource.QuerySet.TenantsListed, resource.QuerySet.NameSources).
type AcrossTenantsList interface {
	Resource() accesstypes.Resource
	TenantsListed(ctx context.Context) (set, narrowed []accesstypes.Domain, err error)
	nameSourcer
}

// AcrossTenantsLiveness is what SubscribeAcrossTenants answers of a list across tenants:
// whether it is served as a live one. A list whose set of tenants is larger than
// MaxSubscriptionTenants is served plain though the page asked for live: no
// subscription holds it, and its answer is not marked cacheable, so the browser asks
// again each time, as it does for any list that is not live. The handler marks the
// response through it (SetCacheControl).
type AcrossTenantsLiveness struct {
	plain bool
}

// Plain reports whether the list is served plain though the page asked for live.
func (l AcrossTenantsLiveness) Plain() bool {
	return l.plain
}

// SetCacheControl marks the response as SetCacheControl does, unless the list is served
// plain: no change will ever tell the browser to ask a plain list again, so its answer
// is not the browser's to keep.
func (l AcrossTenantsLiveness) SetCacheControl(w http.ResponseWriter, r *http.Request) {
	if l.plain {
		return
	}
	SetCacheControl(w, r)
}

// SubscribeAcrossTenants registers the subscription of a list across tenants when the
// request carries the subscribe header, before the handler runs the query: one
// subscription over the set of tenants the list reads for the person, so a commit in
// any of them finds it and a commit in a tenant the person cannot enter does not. A list
// across tenants is plain unless the page asks, so a request without the header
// registers nothing; neither does a list that reads no tenant, which nothing can change.
// A set larger than MaxSubscriptionTenants registers nothing either, and the answer says
// the list is served plain: that is the ceiling's decision and no failure, so nothing is
// logged for it. A list that is registered registers with it, as Subscribe does, the
// lists the names it shows are read from, and its answer names them. As with Subscribe,
// a set that cannot be worked out or a record that fails to take the subscription is
// logged and the request is still answered.
func SubscribeAcrossTenants(ctx context.Context, w http.ResponseWriter, r *http.Request, svc Service, list AcrossTenantsList) AcrossTenantsLiveness {
	tab, err := Tab(r)
	if err != nil || tab == "" {
		// A malformed tab was refused by the outlet's middleware before the handler ran.
		return AcrossTenantsLiveness{}
	}
	res := list.Resource()
	set, narrowed, err := list.TenantsListed(ctx)
	if err != nil {
		logger.FromCtx(ctx).Errorf("live: working out the tenants of the %s subscription across tenants of tab %s failed; the subscription is not registered: %v", res, tab, err)

		return AcrossTenantsLiveness{}
	}
	if len(set) == 0 {
		return AcrossTenantsLiveness{}
	}
	if len(set) > MaxSubscriptionTenants {
		return AcrossTenantsLiveness{plain: true}
	}
	principal, err := PrincipalID(ctx)
	if err != nil {
		logger.FromCtx(ctx).Errorf("live: the %s subscription across tenants of tab %s is not registered; the page is served without it: %v", res, tab, err)

		return AcrossTenantsLiveness{}
	}
	registered := AcrossTenantsSubscription(res, set, narrowed).Normalized()
	stamp(&registered, principal, tab, time.Now().Add(SubscriptionTTL))
	names := namedLists(ctx, list, &registered, tab)
	ctx, cancel := context.WithTimeout(ctx, RecordTimeout)
	defer cancel()
	if err := svc.Register(ctx, withNamed(&registered, names)); err != nil {
		logger.FromCtx(ctx).Errorf("live: registering the %s subscription across tenants of tab %s failed; the page is served without it: %v", res, tab, err)

		return AcrossTenantsLiveness{}
	}
	setNamesHeader(ctx, w, &registered, names)

	return AcrossTenantsLiveness{}
}

// SetCacheControl marks a list or read response cacheable by the browser when its
// request carried the version parameter: Cache-Control private with the subscription's
// window, and the no-cache Pragma and Expires the outlet's NoCaching middleware set are
// dropped so nothing contradicts it. Every other response is left uncached.
func SetCacheControl(w http.ResponseWriter, r *http.Request) {
	if !r.URL.Query().Has(VersionParam) {
		return
	}
	header := w.Header()
	header.Set("Cache-Control", CacheControl)
	header.Del("Pragma")
	header.Del("Expires")
}

// Publish writes a committed request's changes into its subscribers' change sets, after
// the commit and before the response: the rows the request's transactions wrote, each
// group under the domain its patch was decoded in (domain is the request's route
// domain, which rows written by hand take), expanded with the virtual and computed
// resources those rows reach (derivations, the generated value; nil reaches none). It is
// bounded by PublishTimeout, a failure is logged, and the request answers either way.
// Nothing happens for a request that committed nothing.
//
// The request is one write: Publish mints its id, every document it publishes carries
// it, and the documents that went into the caller's own change set are named on the
// answer with it (ChangesHeader on w, which the handler has not written yet), so the tab
// that wrote fetches them at once and knows the write when its change set reports it. A
// request with no session principal (a machine's, a scheduled call's), and one that
// reached nothing the caller holds live, answers without the header. So does a request
// any of whose publishes failed: a publish that failed may have written some of its
// documents, and a tab told the write's id would take those for changes it had fetched
// for; told nothing, it fetches for every document that got through as for anyone's.
//
// The tables the request touched whole (resource.TouchedRows.TouchTable) go into the
// publish of the route domain, which is made though no row was written in it.
func Publish(ctx context.Context, w http.ResponseWriter, svc Service, derivations *Derivations, domain accesstypes.Domain, touched *resource.TouchedRows) {
	if touched.Empty() {
		return
	}
	write := rand.Text()
	caller := callerID(ctx)
	ctx, cancel := context.WithTimeout(ctx, PublishTimeout)
	defer cancel()
	own, failed := publishTouched(ctx, svc, derivations, domain, touched, write, caller)
	if failed {
		return
	}
	if err := setChangesHeader(w, Answer{Write: write, Changes: own}); err != nil {
		logger.FromCtx(ctx).Errorf("live: naming the write's changes on its answer failed; the page fetches when its change set reports them: %v", err)
	}
}

// Announce publishes changes that are no request's own write: what a sign-in, a hook or a
// job committed outside a generated handler. It is Publish without the answer: the
// documents name no write, so every tab that holds a target refetches it. Rows written
// with no domain are published under the zero domain, with the tables touched whole. It
// is bounded by PublishTimeout, a failure is logged and the other domains are still
// published, and nothing happens when the collector is empty or svc is nil.
func Announce(ctx context.Context, svc Service, derivations *Derivations, touched *resource.TouchedRows) {
	if svc == nil || touched.Empty() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, PublishTimeout)
	defer cancel()
	publishTouched(ctx, svc, derivations, "", touched, "", "")
}

// publishTouched makes a collector's publishes: one per domain its rows were written in,
// the rows without a domain under fallback, and the tables touched whole in fallback's,
// which is made even when no row was written in it. Every document carries write. It
// answers the documents that went into caller's own change set, and whether any publish
// failed; a failure is logged and the other domains are still published.
func publishTouched(ctx context.Context, svc Service, derivations *Derivations, fallback accesstypes.Domain, touched *resource.TouchedRows, write, caller string) ([]ChangeDocument, bool) {
	own := make(Changes)
	failed := false
	byDomain := touched.Rows(fallback)
	tables := touched.Tables()
	if _, ok := byDomain[fallback]; !ok && len(tables) > 0 {
		byDomain[fallback] = nil
	}
	for _, changeDomain := range slices.Sorted(maps.Keys(byDomain)) {
		var whole []accesstypes.Resource
		if changeDomain == fallback {
			whole = tables
		}
		writes := derivations.Expand(byDomain[changeDomain], whole...)
		writes.ID = write
		changes, err := svc.Publish(ctx, changeDomain, writes)
		if err != nil {
			logger.FromCtx(ctx).Errorf("live: publishing the changes of domain %q failed; the pages refetch at their next change: %v", changeDomain, err)
			failed = true

			continue
		}
		for _, doc := range changes[caller] {
			own.add(caller, doc)
		}
	}

	return own[caller], failed
}

// Answer is what a write's answer tells the tab that wrote, in ChangesHeader: the
// write's id, which every change document the write published carries, and the
// documents that went into the caller's own change set, which the tab fetches at once.
// All stands for the documents when naming them would outgrow MaxChangesHeaderLength:
// the tab then fetches everything it holds live.
type Answer struct {
	Write   string           `json:"write"`
	Changes []ChangeDocument `json:"changes,omitempty"`
	All     bool             `json:"all,omitempty"`
}

// header renders the answer as ChangesHeader carries it: its JSON, percent-encoded.
func (a Answer) header() (string, error) {
	encoded, err := json.Marshal(a)
	if err != nil {
		return "", errors.Wrap(err, "json.Marshal()")
	}

	return url.PathEscape(string(encoded)), nil
}

// setChangesHeader names the write's changes on its answer. An answer naming no
// document sets nothing, and one whose documents outgrow MaxChangesHeaderLength names
// the write alone, with All.
func setChangesHeader(w http.ResponseWriter, answer Answer) error {
	if len(answer.Changes) == 0 {
		return nil
	}
	value, err := answer.header()
	if err != nil {
		return err
	}
	if len(value) > MaxChangesHeaderLength {
		value, err = Answer{Write: answer.Write, All: true}.header()
		if err != nil {
			return err
		}
	}
	w.Header().Set(ChangesHeader, value)

	return nil
}

// callerID is the principal id of the session the request speaks for, whose change set
// a write's answer names documents of; empty for a request with no session principal
// bound (a machine outlet's, a scheduled call's) and for one live pages refuse
// (PrincipalID), neither of which holds a change set.
func callerID(ctx context.Context) string {
	if _, ok := ctx.Value(sessioninfo.CtxSessionInfo).(*sessioninfo.SessionData); !ok {
		return ""
	}
	id, err := PrincipalID(ctx)
	if err != nil {
		return ""
	}

	return id
}

// PrincipalID is the session principal's id as the subscription record, the change set
// and the browser's identity carry it: the name of the auth the request came through
// (bound by the outlet's group, resource.BindAuth, or by Subscribing where no group
// binds), AuthSeparator, and the person for a user principal (an ordinary session, or
// an impersonated user), named as the permission store names them, by the id of their
// account, or the role prefixed with "role:" for a session established as a role. A
// user of the crew auth is crew|0b5e…, the account's id, and a session established as
// the Auditor role through the crew auth is crew|role:Auditor. A person renamed keeps
// their id, and so their subscriptions.
//
// Nothing is truncated or hashed. An id longer than MaxPrincipalIDLength, Firebase's
// uid limit, fails with a 403 client message whose cause names the id, its length and
// the limit, so the person uses the application while live pages refuse; a request no
// auth was bound to fails as a server error, since its principal could not be told from
// another auth's.
func PrincipalID(ctx context.Context) (string, error) {
	auth, ok := resource.AuthFrom(ctx)
	if !ok {
		return "", errors.New("live.PrincipalID(): no auth is bound to the request; the outlet's generated routes bind it (live.Subscribing)")
	}
	id := auth + AuthSeparator + principalName(ctx)
	if len(id) > MaxPrincipalIDLength {
		cause := errors.Newf("live.PrincipalID(): the principal id %q is %d bytes, over Firebase's uid limit of %d", id, len(id), MaxPrincipalIDLength)

		return "", httpio.NewForbiddenMessageWithError(cause, "Pages do not update on their own for your sign-in. Reload a page to see what has changed.")
	}

	return id, nil
}

// principalName is the session principal as its id names it after the auth: the person
// for a user principal, as the permission store names them (the id of their account),
// the role marked "role:" for a session established as a role.
func principalName(ctx context.Context) string {
	principal := sessioninfo.PrincipalFromCtx(ctx)
	if role, ok := principal.Role(); ok {
		return rolePrefix + string(role)
	}
	user, _ := principal.User()

	return string(user)
}
