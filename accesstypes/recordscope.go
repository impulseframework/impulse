package accesstypes

// RecordScope names one record of a tenant as the place a membership is held:
// the tenant the record is in, the record's kind (its resource name) and the
// record's key, as text. A role held there gives its grants on that record and
// on what the record reaches, and nowhere else of the tenant.
//
// It is a type of its own, not a PolicyScope and not a Scope, so a place that
// is one record can never be read as the whole tenant it is in: nothing that
// takes a PolicyScope takes a RecordScope, and nothing converts one into the
// other. Scope converts it into the partition a check of the record's rows is
// asked in, its tenant's.
//
// RecordScope is comparable and usable as a map key. The zero value names
// nothing. The constructor checks nothing, as DomainPolicyScope checks
// nothing; the user manager refuses a value with an empty part.
type RecordScope struct {
	axis     string
	domain   Domain
	resource Resource
	record   string
}

// NewRecordScope returns the place of one record: the record keyed record, of
// the kind resource, in the tenant domain.
func NewRecordScope(domain Domain, resource Resource, record string) RecordScope {
	return RecordScope{domain: domain, resource: resource, record: record}
}

// Domain returns the tenant the record is in.
func (r RecordScope) Domain() Domain {
	return r.domain
}

// Resource returns the record's kind, its resource name.
func (r RecordScope) Resource() Resource {
	return r.resource
}

// Record returns the record's key, as text.
func (r RecordScope) Record() string {
	return r.record
}

// Axis returns the name of the axis the record's tenant belongs to: the
// default axis, the empty string, for every value this package constructs.
func (r RecordScope) Axis() string {
	return r.axis
}

// Scope returns the partition a check of the record's rows is asked in: its
// tenant's scope, never the global one.
func (r RecordScope) Scope() Scope {
	return Scope{axis: r.axis, domain: r.domain}
}

// IsZero reports whether the value is the zero RecordScope, which names no
// record.
func (r RecordScope) IsZero() bool {
	return r == RecordScope{}
}

// String renders the place for display only, as "<resource> <record> in
// <domain>". It must never be parsed; use the accessors for logic.
func (r RecordScope) String() string {
	return string(r.resource) + " " + r.record + " in " + string(r.domain)
}
