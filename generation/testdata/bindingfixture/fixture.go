// Package bindingfixture provides parsed-struct fixtures for the binding-annotation
// resolution tests: column and join-path @attribute bindings, the @domain tenancy
// binding, and the subject-side @subjectSet / @subjectValue vocabulary, plus one
// struct per rejection the resolver enforces.
package bindingfixture

import (
	"context"

	"github.com/impulseframework/impulse"
	"github.com/impulseframework/impulse/resource"
)

// Struct-level annotations only parse from a TypeSpec doc comment, so scoped
// structs sit in grouped type blocks like real project sources do.
type (
	// MaintenanceTask carries the well-formed binding shapes. It is
	// domain-scoped because @domain pairs with domain scope (both directions
	// of the tenancy pairing are rejection shapes below).
	//
	// @permissionScope(domain)
	MaintenanceTask struct {
		ID impulse.UUID `spanner:"Id"`

		// @domain
		StationID impulse.UUID `spanner:"StationId"`

		// @attribute(crew)
		CrewID impulse.UUID `spanner:"CrewId"`

		// @attribute(shipClass, via: Class)
		ShipID impulse.UUID `spanner:"ShipId"`

		// @attribute(sector, via: StationID.Sector)
		BerthID impulse.UUID `spanner:"BerthId"`

		// @attribute(estimatedCost)
		EstimatedCost int64 `spanner:"EstimatedCost"`
	}
)

// Ship is the one-hop target: ShipID resolves shipClass to Ships.Class.
type Ship struct {
	ID    impulse.UUID `spanner:"Id"`
	Class string       `spanner:"Class"`
}

// Berth is the first hop of the two-hop path; Station the second.
type Berth struct {
	ID        impulse.UUID `spanner:"Id"`
	StationID impulse.UUID `spanner:"StationId"`
}

type Station struct {
	ID     impulse.UUID `spanner:"Id"`
	Sector string       `spanner:"Sector"`
}

type (
	// CrewMember anchors the subject-side set vocabulary.
	//
	// @permissionScope(domain)
	CrewMember struct {
		ID impulse.UUID `spanner:"Id"`

		// @subjectSet(crews, value: CrewID)
		UserID impulse.UUID `spanner:"UserId"`

		CrewID impulse.UUID `spanner:"CrewId"`

		// @domain
		StationID impulse.UUID `spanner:"StationId"`
	}
)

// UserProfile anchors the scalar vocabulary; UserID is the primary key.
type UserProfile struct {
	// @subjectValue(approvalLimit, value: ApprovalLimit)
	UserID impulse.UUID `spanner:"UserId"`

	ApprovalLimit int64 `spanner:"ApprovalLimit"`
}

// HomeProfile anchors a dotted scalar: homeSector continues through the
// StationID foreign key to Stations.Sector, whose type the value takes.
type HomeProfile struct {
	// @subjectValue(homeSector, value: StationID.Sector)
	UserID impulse.UUID `spanner:"UserId"`

	StationID impulse.UUID `spanner:"StationId"`
}

// UnsupportedValueType yields a column outside the comparison-type
// vocabulary: no attribute could ever compare against it, so it is refused as
// an @attribute on such a column is.
type UnsupportedValueType struct {
	ID impulse.UUID `spanner:"Id"`

	// @subjectSet(payloads, value: Payload)
	UserID impulse.UUID `spanner:"UserId"`

	Payload []byte `spanner:"Payload"`
}

// The rejection shapes, one struct each.

type ReservedName struct {
	ID impulse.UUID `spanner:"Id"`

	// @attribute(new)
	CrewID impulse.UUID `spanner:"CrewId"`
}

type BadCharset struct {
	ID impulse.UUID `spanner:"Id"`

	// @attribute(crew-chief)
	CrewID impulse.UUID `spanner:"CrewId"`
}

type DuplicateName struct {
	ID impulse.UUID `spanner:"Id"`

	// @attribute(crew)
	CrewID impulse.UUID `spanner:"CrewId"`

	// @subjectSet(crew, value: CrewID)
	UserID impulse.UUID `spanner:"UserId"`
}

type PathOffNonFK struct {
	ID impulse.UUID `spanner:"Id"`

	// @attribute(sector, via: Sector)
	Label string `spanner:"Label"`
}

type PathUnknownField struct {
	ID impulse.UUID `spanner:"Id"`

	// @attribute(shipClass, via: Nope)
	ShipID impulse.UUID `spanner:"ShipId"`
}

type PathThroughNonFK struct {
	ID impulse.UUID `spanner:"Id"`

	// @attribute(deep, via: Class.Deeper)
	ShipID impulse.UUID `spanner:"ShipId"`
}

type ScalarOnNonUnique struct {
	ID impulse.UUID `spanner:"Id"`

	// @subjectValue(approvalLimit, value: ApprovalLimit)
	UserID impulse.UUID `spanner:"UserId"`

	ApprovalLimit int64 `spanner:"ApprovalLimit"`
}

// ScalarOnCompositeKey anchors a @subjectValue on one column of a composite primary
// key: the PRIMARY_KEY index is unique over (GroupId, UserId), so a user may hold a
// row per group and the anchor is refused.
type ScalarOnCompositeKey struct {
	GroupID impulse.UUID `spanner:"GroupId"`

	// @subjectValue(seat, value: Seat)
	UserID impulse.UUID `spanner:"UserId"`

	Seat int64 `spanner:"Seat"`
}

// CrewSeat is a table keyed by two columns: a seat a person holds in a group.
type CrewSeat struct {
	GroupID impulse.UUID `spanner:"GroupId"`
	UserID  impulse.UUID `spanner:"UserId"`
	Rank    int64        `spanner:"Rank"`
}

// ScalarOnCompositeUnique anchors a @subjectValue on one column of a composite unique
// index over (UserId, GroupId): the same one-row-per-group shape under a secondary
// index, refused for the same reason.
type ScalarOnCompositeUnique struct {
	ID impulse.UUID `spanner:"Id"`

	// @subjectValue(seat, value: Seat)
	UserID impulse.UUID `spanner:"UserId"`

	GroupID impulse.UUID `spanner:"GroupId"`

	Seat int64 `spanner:"Seat"`
}

// NullFilteredAnchor anchors a @subjectValue on a nullable user column under a
// null-filtered single-column unique index: accepted, since the index still enforces
// one row per non-null user and the subject subquery compares by equality.
type NullFilteredAnchor struct {
	ID impulse.UUID `spanner:"Id"`

	// @subjectValue(approvalLimit, value: ApprovalLimit)
	UserID *impulse.UUID `spanner:"UserId"`

	ApprovalLimit int64 `spanner:"ApprovalLimit"`
}

type UnknownValueField struct {
	ID impulse.UUID `spanner:"Id"`

	// @subjectSet(crews, value: Nope)
	UserID impulse.UUID `spanner:"UserId"`
}

// Struct-level annotations only parse from a TypeSpec doc comment, so this
// rejection shape sits in a grouped type block like real project sources do.
type (
	// PartitionBlindAnchor is the missing-tenancy rejection: a domain-scoped
	// resource anchoring subject vocabulary with no @domain binding, so
	// subject.<name> would match the user's rows from every tenant.
	//
	// @permissionScope(domain)
	PartitionBlindAnchor struct {
		ID impulse.UUID `spanner:"Id"`

		// @subjectSet(crews, value: CrewID)
		UserID impulse.UUID `spanner:"UserId"`

		CrewID impulse.UUID `spanner:"CrewId"`
	}
)

type DoubleTenant struct {
	ID impulse.UUID `spanner:"Id"`

	// @domain
	StationID impulse.UUID `spanner:"StationId"`

	// @domain
	RegionID impulse.UUID `spanner:"RegionId"`
}

// GlobalTenant is the reverse tenancy-pairing rejection: a @domain binding on
// a resource that is not domain-scoped has no tenant partition to resolve to.
type GlobalTenant struct {
	ID impulse.UUID `spanner:"Id"`

	// @domain
	StationID impulse.UUID `spanner:"StationId"`
}

// TenantStatedTwice restates the bare tenant key's derived decode behavior
// through a conditions tag.
type (
	// @permissionScope(domain)
	TenantStatedTwice struct {
		ID impulse.UUID `spanner:"Id"`

		// @domain
		StationID impulse.UUID `spanner:"StationId" conditions:"immutable"`
	}
)

type (
	// UnboundTenant is the mandatory-binding rejection: domain-scoped with no
	// @domain declared anywhere.
	//
	// @permissionScope(domain)
	UnboundTenant struct {
		ID   impulse.UUID `spanner:"Id"`
		Name string       `spanner:"Name"`
	}
)

type (
	// PathTenantTask resolves its tenant across a join path — the target
	// shape a located-row frame verifies through the gate: the row itself
	// carries no tenant key.
	//
	// @permissionScope(domain)
	PathTenantTask struct {
		ID impulse.UUID `spanner:"Id"`

		// @domain(via: StationID)
		BerthID impulse.UUID `spanner:"BerthId"`

		Name string `spanner:"Name"`
	}
)

type (
	// PathTenantLog resolves its tenant two hops away, through PathTenantTask's own
	// hop: the bindings graph draws the hop the two paths share once. Its bare
	// task attribute sits on that same foreign key, so the graph points it at
	// PathTenantTask with a reference edge.
	//
	// @permissionScope(domain)
	PathTenantLog struct {
		ID impulse.UUID `spanner:"Id"`

		// @domain(via: BerthID.StationID)
		// @attribute(task)
		TaskID impulse.UUID `spanner:"TaskId"`

		Note string `spanner:"Note"`
	}
)

// VirtualWithBinding stands in for every schemaless struct kind in the
// rejection test.
type VirtualWithBinding struct {
	ID impulse.UUID `spanner:"Id"`

	// @attribute(crew)
	CrewID impulse.UUID `spanner:"CrewId"`
}

// The views: a view's projection carries its own columns, so a view declares the
// bare bindings alone. TaskDigest is the well-formed shape; the others are one
// rejection each.
type (
	// TaskDigest is a tenant-scoped view over the tasks: its tenant column, and two
	// facts its query selects, each a column a grant's condition reads by name.
	//
	// @virtual
	// @permissionScope(domain)
	TaskDigest struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id"`

		// @domain
		StationID impulse.UUID `spanner:"StationId"`

		// @attribute(overdue)
		Overdue bool `spanner:"Overdue"`

		// @attribute(crew)
		CrewID impulse.UUID `spanner:"CrewId"`

		Note string `spanner:"Note"`
	}

	// FleetDigest is a global view keyed by one field: a row of it is addressed by
	// the key alone.
	//
	// @virtual
	FleetDigest struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id"`

		// @attribute(grounded)
		Grounded bool `spanner:"Grounded"`
	}

	// KeylessDigest declares no key, so no row of it can be addressed.
	//
	// @virtual
	KeylessDigest struct {
		Total int64 `spanner:"Total"`
	}

	// CompoundDigest keys a tenant-scoped view by its tenant column and an id.
	//
	// @virtual
	// @permissionScope(domain)
	CompoundDigest struct {
		// @primarykey
		// @domain
		StationID impulse.UUID `spanner:"StationId"`

		// @primarykey
		ID impulse.UUID `spanner:"Id"`
	}

	// CompoundFleetDigest keys a global view by two fields.
	//
	// @virtual
	CompoundFleetDigest struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id"`

		// @primarykey
		Slot int64 `spanner:"Slot"`
	}

	// DigestByPath declares a join path on a view's field: a view has no schema
	// to resolve one against.
	//
	// @virtual
	DigestByPath struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id"`

		// @attribute(shipClass, via: Class)
		ShipID impulse.UUID `spanner:"ShipId"`
	}

	// DigestReservedName names a fact with a word of the condition language.
	//
	// @virtual
	DigestReservedName struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id"`

		// @attribute(subject)
		CrewID impulse.UUID `spanner:"CrewId"`
	}

	// DigestBadCharset names a fact outside the identifier charset.
	//
	// @virtual
	DigestBadCharset struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id"`

		// @attribute(crew-chief)
		CrewID impulse.UUID `spanner:"CrewId"`
	}

	// DigestDuplicateName declares one name on two fields.
	//
	// @virtual
	DigestDuplicateName struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id"`

		// @attribute(crew)
		CrewID impulse.UUID `spanner:"CrewId"`

		// @attribute(crew)
		LeadID impulse.UUID `spanner:"LeadId"`
	}

	// DigestUnsupportedType declares a fact on a column no condition can compare.
	//
	// @virtual
	DigestUnsupportedType struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id"`

		// @attribute(payload)
		Payload []byte `spanner:"Payload"`
	}

	// DigestWithLists declares facts on list columns: each is a list attribute,
	// typed by one item, which a condition judges item by item.
	//
	// @virtual
	// @permissionScope(domain)
	DigestWithLists struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id"`

		// @domain
		StationID impulse.UUID `spanner:"StationId"`

		// @attribute(label)
		Labels []string `spanner:"Labels"`

		// @attribute(score)
		Scores []int64 `spanner:"Scores"`

		// @attribute(open)
		Open bool `spanner:"Open"`
	}

	// DigestListOfUnsupported declares a fact on a list whose items no condition
	// can compare.
	//
	// @virtual
	DigestListOfUnsupported struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id"`

		// @attribute(part)
		Parts [][]byte `spanner:"Parts"`
	}

	// DigestSubjectSet declares subject vocabulary on a view, which stays a
	// table's alone.
	//
	// @virtual
	DigestSubjectSet struct {
		// @primarykey
		ID impulse.UUID `spanner:"Id"`

		// @subjectSet(crews, value: CrewID)
		UserID impulse.UUID `spanner:"UserId"`

		CrewID impulse.UUID `spanner:"CrewId"`
	}
)

// StatefulTask carries the @state marker; TaskState is the state column's
// enum-backed type.
type TaskState string

type StatefulTask struct {
	ID impulse.UUID `spanner:"Id"`

	// @state(default: open)
	State TaskState `spanner:"State"`

	// ShipID is a context reference: an FK leaving the workflow tree, drawn
	// dashed in the workflow graph.
	ShipID impulse.UUID `spanner:"ShipId"`

	Notes string `spanner:"Notes"`
}

type StateOnNonFK struct {
	ID impulse.UUID `spanner:"Id"`

	// @state(default: open)
	Label string `spanner:"Label"`
}

type StateBadDefault struct {
	ID impulse.UUID `spanner:"Id"`

	// @state(default: bogus)
	State TaskState `spanner:"State"`
}

type StateStatedTwice struct {
	ID impulse.UUID `spanner:"Id"`

	// @state(default: open)
	State TaskState `spanner:"State" conditions:"output_only"`
}

// TaskPart is a direct workflow member; PartOrder chains through it.
type TaskPart struct {
	ID impulse.UUID `spanner:"Id"`

	// @stateRoot(StatefulTask)
	TaskID impulse.UUID `spanner:"TaskId"`

	// ShipID pins that a member's FK off the tree is a context edge too, and
	// that two edges to one target share one context node.
	ShipID impulse.UUID `spanner:"ShipId"`
}

type PartOrder struct {
	ID impulse.UUID `spanner:"Id"`

	// @stateRoot(StatefulTask)
	PartID impulse.UUID `spanner:"PartId"`
}

type UnknownRootMember struct {
	ID impulse.UUID `spanner:"Id"`

	// @stateRoot(Mystery)
	TaskID impulse.UUID `spanner:"TaskId"`
}

type ChainBreakMember struct {
	ID impulse.UUID `spanner:"Id"`

	// @stateRoot(StatefulTask) — the FK lands outside the workflow
	ShipID impulse.UUID `spanner:"ShipId"`
}

type (
	// @permissionScope(domain)
	ScopedMember struct {
		ID impulse.UUID `spanner:"Id"`

		// @stateRoot(StatefulTask)
		TaskID impulse.UUID `spanner:"TaskId"`
	}
)

// Client stands in for the application's RPC client, the third Execute parameter.
type Client struct{}

// The transition fixtures: one well-formed shape and one struct per rejection
// the resolver enforces.
type (
	// ApproveTask is the well-formed declared transition.
	//
	// @rpc
	// @transition(StatefulTask, from: open, to: approved)
	ApproveTask struct {
		// @target
		TaskID impulse.UUID
		Note   string
	}

	// CloseTask exercises the multi-valued from set.
	//
	// @rpc
	// @transition(StatefulTask, from: open, approved, to: closed)
	CloseTask struct {
		// @target
		TaskID impulse.UUID
	}

	// @rpc
	// @transition(Mystery, from: open, to: approved)
	TransitionUnknownRoot struct {
		// @target
		TaskID impulse.UUID
	}

	// @rpc
	// @transition(Ship, from: open, to: approved)
	TransitionStatelessRoot struct {
		// @target
		TaskID impulse.UUID
	}

	// @rpc
	// @transition(StatefulTask, from: bogus, to: approved)
	TransitionBadFrom struct {
		// @target
		TaskID impulse.UUID
	}

	// @rpc
	// @transition(StatefulTask, from: open, open, to: approved)
	TransitionDuplicateFrom struct {
		// @target
		TaskID impulse.UUID
	}

	// @rpc
	// @transition(StatefulTask, from: open, to: bogus)
	TransitionBadTo struct {
		// @target
		TaskID impulse.UUID
	}

	// @rpc
	// @transition(StatefulTask, from: open, to: approved)
	TransitionNoTarget struct {
		TaskID impulse.UUID
	}

	// @rpc
	// @transition(StatefulTask, from: open, to: approved)
	TransitionTwoTargets struct {
		// @target
		TaskID impulse.UUID
		// @target
		OtherID impulse.UUID
	}

	// @rpc
	// @transition(StatefulTask, from: open, to: approved)
	TransitionTargetTypeMismatch struct {
		// @target
		TaskID string
	}

	// @rpc
	// @transition(StatefulTask, from: open, to: approved)
	TransitionClientForm struct {
		// @target
		TaskID impulse.UUID
	}

	// @rpc
	// @permissionScope(domain)
	// @transition(StatefulTask, from: open, to: approved)
	TransitionScopeMismatch struct {
		// @target
		TaskID impulse.UUID
	}

	// @rpc
	TargetWithoutTransition struct {
		// @target
		TaskID impulse.UUID
	}

	// @rpc
	PlainTargetTask struct {
		// @target(StatefulTask)
		TaskID impulse.UUID
		Note   string
	}

	// PlainTargetMaintenance targets a domain-scoped root with a bare-column
	// tenancy binding: the frame reads the located row's own tenant key.
	//
	// @rpc
	// @permissionScope(domain)
	PlainTargetMaintenance struct {
		// @target(MaintenanceTask)
		TaskID impulse.UUID
	}

	// PlainTargetPathTenant targets a domain-scoped root whose tenancy binding
	// is a join path: the frame verifies tenancy through the gate.
	//
	// @rpc
	// @permissionScope(domain)
	PlainTargetPathTenant struct {
		// @target(PathTenantTask)
		TaskID impulse.UUID
	}

	// @rpc
	// @transition(StatefulTask, from: open, to: approved)
	TransitionTargetWithArg struct {
		// @target(StatefulTask)
		TaskID impulse.UUID
	}

	// PlainTargetDigest targets a row of a tenant-scoped view: the gate locates
	// it by the key within the request's tenant.
	//
	// @rpc
	// @permissionScope(domain)
	PlainTargetDigest struct {
		// @target(TaskDigest)
		TaskID impulse.UUID
	}

	// PlainTargetFleetDigest targets a row of a global view.
	//
	// @rpc
	PlainTargetFleetDigest struct {
		// @target(FleetDigest)
		ShipID impulse.UUID
	}

	// @rpc
	TargetKeylessDigest struct {
		// @target(KeylessDigest)
		ID impulse.UUID
	}

	// @rpc
	// @permissionScope(domain)
	TargetCompoundDigest struct {
		// @target(CompoundDigest)
		ID impulse.UUID
	}

	// @rpc
	TargetCompoundFleetDigest struct {
		// @target(CompoundFleetDigest)
		ID impulse.UUID
	}

	// PlainTargetCompoundFleetDigest targets a row of a view keyed by two fields: one
	// field for each, named by key: and declared here out of the key's order.
	//
	// @rpc
	PlainTargetCompoundFleetDigest struct {
		// @target(CompoundFleetDigest, key: Slot)
		Slot int64
		// @target(CompoundFleetDigest, key: ID)
		ShipID impulse.UUID
		Note   string
	}

	// PlainTargetCrewSeat targets a row of a table keyed by two columns.
	//
	// @rpc
	PlainTargetCrewSeat struct {
		// @target(CrewSeat, key: GroupID)
		GroupID impulse.UUID
		// @target(CrewSeat, key: UserID)
		UserID impulse.UUID
	}

	// @rpc
	TargetCompoundMissingColumn struct {
		// @target(CompoundFleetDigest, key: ID)
		ShipID impulse.UUID
	}

	// @rpc
	TargetCompoundUnknownKey struct {
		// @target(CompoundFleetDigest, key: ID)
		ShipID impulse.UUID
		// @target(CompoundFleetDigest, key: Berth)
		Slot int64
	}

	// @rpc
	TargetCompoundSameKeyTwice struct {
		// @target(CompoundFleetDigest, key: ID)
		ShipID impulse.UUID
		// @target(CompoundFleetDigest, key: ID)
		OtherID impulse.UUID
	}

	// @rpc
	TargetCompoundTypeMismatch struct {
		// @target(CompoundFleetDigest, key: ID)
		ShipID impulse.UUID
		// @target(CompoundFleetDigest, key: Slot)
		Slot string
	}

	// @rpc
	TargetTwoRoots struct {
		// @target(CompoundFleetDigest, key: ID)
		ShipID impulse.UUID
		// @target(FleetDigest)
		OtherID impulse.UUID
	}

	// @rpc
	TargetTwoFieldsOneColumn struct {
		// @target(FleetDigest)
		ShipID impulse.UUID
		// @target(FleetDigest)
		OtherID impulse.UUID
	}

	// @rpc
	TargetKeyNamesNoKey struct {
		// @target(FleetDigest, key: Slot)
		ShipID impulse.UUID
	}

	// TargetKeyNamesTheKey names the one key column it carries, which it need not.
	//
	// @rpc
	TargetKeyNamesTheKey struct {
		// @target(FleetDigest, key: ID)
		ShipID impulse.UUID
	}

	// TransitionOnDigest names a view as a workflow root.
	//
	// @rpc
	// @permissionScope(domain)
	// @transition(TaskDigest, from: open, to: approved)
	TransitionOnDigest struct {
		// @target
		TaskID impulse.UUID
	}

	// TargetDigestScopeMismatch is a global method over a tenant-scoped view.
	//
	// @rpc
	TargetDigestScopeMismatch struct {
		// @target(TaskDigest)
		TaskID impulse.UUID
	}

	// TargetDigestTypeMismatch carries a key of another type than the view's.
	//
	// @rpc
	TargetDigestTypeMismatch struct {
		// @target(FleetDigest)
		ShipID string
	}
)

// Every RPC fixture declares Execute in the transaction form the transition
// resolver requires; TransitionClientForm alone declares the client form.
func (ApproveTask) Execute(context.Context, resource.ReadWriteTransaction, *Client) error { return nil }
func (CloseTask) Execute(context.Context, resource.ReadWriteTransaction, *Client) error   { return nil }
func (TransitionUnknownRoot) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TransitionStatelessRoot) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TransitionBadFrom) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TransitionDuplicateFrom) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TransitionBadTo) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TransitionNoTarget) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TransitionTwoTargets) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TransitionTargetTypeMismatch) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TransitionScopeMismatch) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetWithoutTransition) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (PlainTargetTask) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (PlainTargetMaintenance) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (PlainTargetPathTenant) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TransitionTargetWithArg) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}

func (PlainTargetDigest) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (PlainTargetFleetDigest) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetKeylessDigest) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetCompoundDigest) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetCompoundFleetDigest) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (PlainTargetCompoundFleetDigest) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (PlainTargetCrewSeat) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetCompoundMissingColumn) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetCompoundUnknownKey) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetCompoundSameKeyTwice) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetCompoundTypeMismatch) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetTwoRoots) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetTwoFieldsOneColumn) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetKeyNamesNoKey) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetKeyNamesTheKey) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TransitionOnDigest) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetDigestScopeMismatch) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}
func (TargetDigestTypeMismatch) Execute(context.Context, resource.ReadWriteTransaction, *Client) error {
	return nil
}

func (TransitionClientForm) Execute(context.Context, resource.Client, *Client) error { return nil }
