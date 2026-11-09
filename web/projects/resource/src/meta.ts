import { Resource } from './brands';

/**
 * The display-type vocabulary: the name the ccc generator emits for every field it
 * describes, a table column, a view column, a computed field, and an RPC field alike, and
 * the name a browser chooses a control and a cell renderer by. A leaf's display type is
 * its own name on every path (a time.Time is 'date', a civil.Date 'civildate', a impulse.UUID
 * 'uuid', so a computed UUID field and a UUID column carry the same name), a nested
 * struct, an imported type, and a value with no fixed shape are 'object', a byte slice is
 * 'bytes', a nullable BOOL column is 'nullboolean', and a picker is 'enumerated'. A slice
 * of a leaf adds [], for every leaf but the two that describe one value alone:
 * 'nullboolean' is the tri-state rule for one nullable BOOL column, and a picker stores
 * one key. The list is the generator's own (resource/generation/displaytype.go) and its
 * README's table (section 12), spelled the same, and the generator refuses to emit
 * anything outside it, so the union says exactly what a generated file can carry.
 */
export type ValidDisplayTypes =
  | 'string'
  | 'number'
  | 'boolean'
  /** A nullable BOOL column: null, true, or false (NullBoolean), one tri-state control. */
  | 'nullboolean'
  /** A timestamp (time.Time): a Date in the interface. */
  | 'date'
  /** A calendar date (civil.Date): a Date in the interface, coerced to yyyy-MM-dd on the wire. */
  | 'civildate'
  /** A UUID: a string in the interface, and this name says it is an identifier, not text. */
  | 'uuid'
  /** A picker: the field holds a key into the resource or enumeration the metadata names beside it. */
  | 'enumerated'
  /** A nested struct the generator mirrored or derived, an imported type, or a value with no fixed shape: one opaque object, granted, masked, and selected whole. */
  | 'object'
  /**
   * A byte slice ([]byte, BYTES(n)): the interface says string because encoding/json
   * carries it as base64, and this name says it is not text. A grid shows its size or
   * offers a download, never the base64; a form takes no free-text control for it.
   */
  | 'bytes'
  /** A list of a leaf (an ARRAY column, a slice field): never filtered, indexed, or sorted by; a grid shows the list, a form takes no free-text control for it. */
  | 'string[]'
  | 'number[]'
  | 'boolean[]'
  | 'date[]'
  | 'civildate[]'
  | 'uuid[]'
  | 'object[]'
  | 'bytes[]';

/**
 * An RPC field carries the same vocabulary as a resource field; the name stays because
 * the generated methods file imports it.
 */
export type ValidRPCTypes = ValidDisplayTypes;

/** One value of a fixed enumeration: the stored id and the description shown for it. */
export interface EnumerationOption {
  id: string;
  display: string;
}

export interface RPCFieldMeta {
  fieldName: string;
  displayType: ValidRPCTypes;
  /** The resource whose rows a picker for this field lists; absent when the values are fixed (see enumeration). */
  enumeratedResource?: Resource;
  /** The fixed values of an enumeration table the field names; the picker renders them without a request. */
  enumeration?: EnumerationOption[];
}

export interface MethodMeta {
  route: string;
  /** Set when the method answers with a result body; absent methods resolve with nothing. */
  answers?: true;
  fields: RPCFieldMeta[];
  /** The feature flag that gates the method, by name; absent on an ungated method. See MethodDescriptor.feature. */
  feature?: string;
}

/** When the server accepts a filter on a field: on its own, or only beside an indexed field. */
export type FilterEligibility = 'always' | 'withIndexed';

/** A text match the server answers on a field: the start of the value, its end, or anywhere in it. */
export type SearchMatch = 'startswith' | 'endswith' | 'contains';

/**
 * How a field's masked cells meet a sort or a filter. Only the non-default behavior is
 * named: a field without it conceals.
 */
export type MaskingBehavior = 'positional';

export interface FieldMeta {
  fieldName: string;
  /** Indicates whether the field is required and only applies during resource creation.
   * Use the validators config parameter in all other contexts
   */
  required: boolean;
  primaryKey?: { ordinalPosition: number };
  displayType: ValidDisplayTypes;
  /** The resource whose rows a picker for this key lists; absent when the values are fixed (see enumeration). */
  enumeratedResource?: Resource;
  /**
   * The fixed value set of a key into an @enumerate table, emitted by the generator from
   * the table's rows at generation time — the same rows the Go constants and the
   * TypeScript enum come from. A picker renders these without a request or a List
   * grant. Present exactly when displayType is 'enumerated' and enumeratedResource is
   * absent.
   */
  enumeration?: EnumerationOption[];
  isIndex: boolean;
  /**
   * Whether the server accepts a filter on this field, by the eligibility the query
   * decoder applies, so a table draws a filter control only where the server will
   * answer. `always`: an indexed or unique-indexed table or view field, or a computed
   * resource's `allow_filter` field (its List function filters in memory).
   * `withIndexed`: a table or view `allow_filter` field, accepted only when the same
   * filter also touches an `always` field — once one index has narrowed the rows a
   * second is rarely used, and indexes are a scarce commodity on Spanner, so
   * `allow_filter` conserves them — and a field the server filters through its search
   * fact alone, whose `search` list names the matches that stand alone; a match on such
   * a field, and the search box, count as the indexed touch a companion needs. Absent,
   * a filter naming the field is refused.
   */
  filterable?: FilterEligibility;
  /**
   * The text matches the server answers on this field, derived from the schema: a stored
   * LOWER(col) column leading an index answers `startswith`; a token column under the
   * table's search index answers `contains`, and the anchors its tokenizer declares; a
   * computed resource's `allow_filter` text field answers all three in memory. A match
   * stands alone in a filter whatever `filterable` says, its value travels quoted (see
   * serializeFilter), and a match on a PII field keeps the URL rule (sensitiveFilter).
   * Absent on a field that answers no match: a filter naming one on it is refused.
   */
  search?: SearchMatch[];
  /**
   * Set when the field is declared `pii` on the server: a filter naming it, a text match
   * on it, or a search box that reaches it must travel in the request body, never in a
   * URL, which the server refuses with a 400. The store posts such a request on its own.
   * Absent on every other field. Emitted by the ccc TypeScript generator.
   */
  pii?: true;
  /**
   * Set when the field is declared masking:"positional" on the server: a masked cell
   * still arrives hidden (absent from the row), but the server sorts, filters, and pages
   * on the real column, so a reader who sees some values can tell where the hidden ones
   * fall between them. Absent, the field conceals: a masked cell is NULL to the sort and
   * the filter, sorting in the NULL region and matching only isnull.
   */
  masking?: MaskingBehavior;
  /**
   * The most characters the server accepts in this field, from its column's declared
   * STRING(n) length; per element for a 'string[]' field. A form adds a maxLength
   * validator from it so a value the server would refuse with 400 never leaves the
   * browser. JavaScript counts UTF-16 units and the server counts code points, so the
   * form refuses a little early on astral characters and never accepts what the server
   * refuses. Absent on an unbounded column and on every non-string field. Emitted by the
   * ccc TypeScript generator.
   */
  maxLength?: number;
  /**
   * The server never accepts this field from clients — it is server-owned
   * (output-only, an @state column, or the tenant key) — so forms render it read-only
   * in every mode. Emitted by the ccc TypeScript generator.
   */
  readOnly?: boolean;
  /**
   * The server never returns this field: it is write-only (`conditions:"input_only"`),
   * accepted on create and update and absent from every list and read response, and a
   * `columns=` naming it is refused. The generated row interface has no property for it
   * while the Create and Patch shapes carry it, so a create or edit form renders a blank
   * input for it and a view renders nothing. Emitted by the ccc TypeScript generator.
   */
  writeOnly?: boolean;
  /**
   * The feature flag that gates the field (`@feature` on the field), by name. While the
   * flag is off the field is absent: omitted from every response, an unknown field in a
   * request (400), and omitted from the digest, so a form over the digest's fields never
   * draws it. The generated row type makes it optional. Absent on an ungated field.
   */
  feature?: string;
}

/** One @searchAlso branch of a resource's search box, as the metadata describes it. */
export interface SearchBranchMeta {
  /** The dotted name the box lists the branch under, `clientContact.displayName`. */
  field: string;
  /** The child resource whose List grant on `fieldName` governs the branch. */
  resource: Resource;
  /** The child field's JSON name. */
  fieldName: string;
  /** Set when the child field is declared `pii`: a box reaching it travels in the body. */
  pii?: true;
}

export interface ResourceMeta {
  route: string;
  consolidatedRoute?: string;
  /**
   * The table resource whose rows this view carries, one to one under the same key: the
   * struct-scope @rowsOf on a virtual or computed view. The view declares its backing
   * table, a create goes into the table, and the new row shows up in the view on the
   * next list because the view's SQL reads that table, so a page listing the view sends
   * its create, edit, and delete here and opens a row on this resource's page. Absent,
   * the view is a read-only list.
   */
  rowsOf?: Resource;
  /**
   * Whether a commit's changes to this resource are published to live pages. True on a
   * table-backed resource: the generated handlers publish its touched rows after every
   * commit, so a page on it is live unless its config says `live: false`. False on a view
   * or a computed resource, which no publish reaches yet: a page on it stays plain, and
   * one that says `live: true` is announced once and asks plainly, since its subscription
   * would never hear a change. Absent on metadata from a generator that predates the
   * field: no page is live by itself, and a page's own `live: true` is honored as before.
   * See pageIsLive. Emitted by the ccc TypeScript generator.
   */
  live?: boolean;
  listDisabled?: boolean;
  readDisabled?: boolean;
  createDisabled?: boolean;
  updateDisabled?: boolean;
  deleteDisabled?: boolean;
  fields: FieldMeta[];
  /**
   * The fields the search box `*:search:"…"` reads, by JSON name: every field of the
   * resource with a token column under its table's search index, and a child's field the
   * resource declares through @searchAlso, under its dotted name
   * (`clientContact.displayName`). The server searches those the caller may list and
   * drops a denied one silently, so the browser names the set from this list and the
   * digest. Absent on a resource with no search box: a filter carrying one is refused.
   */
  searchFields?: string[];
  /**
   * The @searchAlso branches among searchFields: for each dotted name, the child
   * resource and the field whose List grant on the child governs the branch, and whether
   * that field is PII. The browser leaves a denied branch out of what the box says it
   * searches, as the server leaves it out of the search, and posts a box that reaches a
   * PII child field. Absent on a resource with no branches.
   */
  searchBranches?: SearchBranchMeta[];
  /**
   * The feature flag that gates the resource, by name; absent on an ungated resource.
   * The generated routes read it to skip the page while the flag is off. See
   * ResourceDescriptor.feature.
   */
  feature?: string;
}

export type Meta = MethodMeta | ResourceMeta;

export type ResourceMap = Record<Resource, ResourceMeta>;

export type NullBoolean = null | true | false;
