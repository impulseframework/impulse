import { Resource } from './brands';

/**
 * The display-type vocabulary: the name the impulse generator emits for every field it
 * describes, a table column, a view column, a computed field, and an RPC field alike, and
 * the name a browser chooses a control and a cell renderer by. A leaf's display type is
 * its own name on every path (a time.Time is 'date', a civil.Date 'civildate', an impulse.UUID
 * 'uuid', so a computed UUID field and a UUID column carry the same name), a nested
 * struct, an imported type, and a value with no fixed shape are 'object', a byte slice is
 * 'bytes', a nullable BOOL column is 'nullboolean', and a picker is 'enumerated'. A slice
 * of a leaf adds [], for every leaf but 'nullboolean', the tri-state rule for one
 * nullable BOOL column. 'enumerated[]' is a method's request field that holds several
 * picked keys; a column stores one key, so no resource field carries it. The list is the
 * generator's own (generation/displaytype.go) and the table of the resource package's
 * README (resource/README.md, section 12), spelled the same, and the generator refuses to
 * emit anything outside it, so the union says exactly what a generated file can carry.
 */
export type ValidDisplayTypes =
  | 'string'
  /**
   * An integer, a float, or a decimal. A decimal is a string in the interface, because
   * its JSON is a quoted string that keeps every digit, and this name says it is drawn
   * and entered as a number.
   */
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
  /** Several picked keys, on a method's request field alone: each a key into the resource or enumeration the metadata names beside it. */
  | 'enumerated[]'
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
  /**
   * Set on a decimal field, and on a list of decimals: its display type is number, which
   * chooses the number control, and its value is text in JSON, so a form sends what the
   * person typed as a string and no digit is lost.
   */
  decimal?: true;
  /** The resource whose rows a picker for this field lists; absent when the values are fixed (see enumeration). */
  enumeratedResource?: Resource;
  /** The fixed values of an enumeration table the field names; the picker renders them without a request. */
  enumeration?: EnumerationOption[];
  /** Set when the method refuses a request that leaves the field empty; a form holds the input to it. */
  required?: true;
  /** Set when a request may leave the field out; a form sends nothing for an input left empty. */
  optional?: true;
  /**
   * Set on a request field that holds a secret (`conditions:"secret"` on the method's
   * field): a form types it into a masked input whose toggle shows what is being typed.
   */
  secret?: true;
  /**
   * The name a grant's condition reads the field by (new.<name>), set on a field that
   * declares it (@attribute). A role file may hold the method to a value of this field,
   * and no value never passes such a condition, so a form that rehearses the call waits
   * for the field and, on a refusal as not permitted, marks it.
   */
  attribute?: string;
}

/** A declared workflow transition: the resource the method moves, the states it runs from, and the state it stamps. */
export interface MethodTransition {
  target: Resource;
  from: string[];
  to: string;
}

/**
 * The row a method runs against (@target): the resource whose row it is, and the request
 * fields that carry the row's key, each with the row's field it carries (one entry for a
 * single-column key, one for each column of a compound one). A page that lists the
 * resource draws the method's button on each row the caller may run it on, and fills
 * the fields from the row.
 */
export interface MethodTarget {
  resource: Resource;
  key: Record<string, string>;
  /**
   * The request fields of the key whose empty text is a value of the key, such as the
   * group of a role a person holds directly: a text field the method does not declare
   * required. A row that holds the empty text there is named by it, and it is sent as it
   * is. Every other part of the key holds a value on every row, and a row that holds
   * none there is refused. Absent where no part of the key may be empty.
   */
  mayBeEmpty?: string[];
}

export interface MethodMeta {
  route: string;
  /** Set when the method answers with a result body; absent methods resolve with nothing. */
  answers?: true;
  /** The statuses the method declares with @answers; a listed 4xx is the method's own answer, not a refusal by the frame. */
  statuses?: number[];
  /** Set on an @upload method: the request is multipart, and the whole body is bounded by maxBytes. */
  upload?: { maxBytes: number };
  /**
   * Set on a method that runs in a transaction: a dry run (X-Dry-Run) runs the whole
   * call and rolls it back, so a refusal can be read before anything is changed. Absent
   * on a method that runs outside one, which refuses the header.
   */
  dryRun?: true;
  /**
   * Set on a method that warns before it runs (@warning): the server writes what the
   * call will do (asked with X-Warning, nothing saved), and a dialog shows it in the
   * warning style and holds its confirm button until the person has typed the word.
   */
  warning?: { typed: string };
  fields: RPCFieldMeta[];
  /** The feature flag that gates the method, by name; absent on an ungated method. See MethodDescriptor.feature. */
  feature?: string;
  /** The workflow transition the method performs; absent on a method that performs none. */
  transition?: MethodTransition;
  /** The row the method runs against; absent on a method with no target row. */
  target?: MethodTarget;
  /**
   * The resources whose rows offer a method that names no target row (@callerEnters naming
   * them): a page fills the method's request from such a row, and the row's Execute answer lists
   * the method only for a caller who holds its grant and whose session enters every tenant the
   * row names. The answer is not the whole of the method's judgment: the call may still be
   * refused for a reason of the method's own. Absent on a method no row answers for, which is
   * offered wherever its grant is held.
   */
  offeredOn?: Resource[];
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
  /**
   * The default the schema declares for a yes or no field, emitted by the generator from
   * the column's `DEFAULT (TRUE)` or `DEFAULT (FALSE)`: what the server stores for a create
   * that leaves the field out. A create form starts the field there, so the form shows
   * what an untouched save stores. Absent where the column declares no default, or one
   * that is neither word, and on every field that is not a yes or no.
   */
  schemaDefault?: boolean;
  primaryKey?: { ordinalPosition: number };
  displayType: ValidDisplayTypes;
  /**
   * Set on a decimal field (a column of Spanner's NUMERIC type or PostgreSQL's numeric),
   * and on a list of decimals. Its display type is number, which chooses the number
   * control, and its value is text in JSON in both directions: a JavaScript number keeps
   * about fifteen digits, and a decimal may hold more. A form sends what the person typed
   * as a string, so no digit is lost. Absent on an integer and a float, which travel as
   * JSON numbers. Emitted by the generator.
   */
  decimal?: true;
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
   * Set when the server refuses a sort by this field: its values have no order the query
   * decoder knows (a list, a map, a nested object), and a request that sorts by it is
   * answered 400. A table draws such a column's heading as words, with nothing to press,
   * so it offers no sort the server would refuse. Absent on every field the server
   * orders: text, numbers, yes or no, moments, dates, decimals and UUIDs. Emitted by the
   * generator from the field's declared type.
   */
  sortable?: false;
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
   * Absent on every other field. Emitted by the impulse TypeScript generator.
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
   * impulse TypeScript generator.
   */
  maxLength?: number;
  /**
   * The server never accepts this field from clients — it is server-owned
   * (output-only, an @state column, or the tenant key) — so forms render it read-only
   * in every mode. Emitted by the impulse TypeScript generator.
   */
  readOnly?: boolean;
  /**
   * The server never returns this field: it is write-only (`conditions:"input_only"`),
   * accepted on create and update and absent from every list and read response, and a
   * `columns=` naming it is refused. The generated row interface has no property for it
   * while the Create and Patch shapes carry it, so a create or edit form renders a blank
   * input for it and a view renders nothing. Emitted by the impulse TypeScript generator.
   */
  writeOnly?: boolean;
  /**
   * The field holds a secret (`conditions:"secret"`): a password, an API key. It is
   * write-only, so `writeOnly` is set with it, and a form types it into a masked input
   * whose toggle shows what is being typed. There is no stored value to show: the server
   * returns none. Emitted by the generator.
   */
  secret?: boolean;
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

/**
 * The words a person calls a resource's rows by: one row ("hangar pass") and several
 * ("hangar passes"), each written alone and in the case it has inside a sentence. A
 * screen puts "a", "an" or "this" before the word and capitalizes it where it opens a
 * sentence.
 */
export interface RowWords {
  one: string;
  many: string;
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
   * See pageIsLive. Emitted by the impulse TypeScript generator.
   */
  live?: boolean;
  /**
   * Set on a computed resource (a `@computed` struct). No query selects its rows: the
   * server's handler orders and filters them by their real values, so the server refuses
   * a sort or a filter by a field unless the caller holds List on the field with no
   * condition. A list draws a column the caller lists under a condition with its heading
   * as words and no filter control, so it offers nothing the server would refuse. Absent
   * on a table and a view, whose sorts and filters run over what the caller may see.
   * Emitted by the impulse TypeScript generator.
   */
  computed?: true;
  /**
   * The words a row of the resource goes by. A list names its row by them on the button
   * that adds one ("Add a hangar pass"), in the question before one is deleted ("Delete
   * this hangar pass?") and in the message after ("Hangar pass deleted."). The generator
   * emits the resource's name split into words, or the words its `@rowWord` states; a
   * view that declares its table (rowsOf) and states none carries the table's. Absent on
   * metadata from a generator that predates the field: a list then says "Create" and
   * speaks of "this row".
   */
  rowWords?: RowWords;
  /**
   * The field a row of the resource is called by, where the resource declares one: the
   * tenant record's name (`@tenantName`). The record is keyed by an id nobody reads, so a
   * picker over it that configures no display of its own lists each row by this field,
   * and a page that names the tenant a row is in reads it from here. Absent on every
   * other resource, and on the tenant record of an application that marks no name.
   */
  nameField?: string;
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
   * The resource's name fields: beside an id field that points to a row with a name, the
   * name of that row. A name is no column of the resource: it is looked up by the id in
   * the pointed-to table, stored nowhere and never written. Absent on a resource none of
   * whose id fields points to a row with a name.
   */
  nameFields?: NameFieldMeta[];
  /**
   * The feature flag that gates the resource, by name; absent on an ungated resource.
   * The generated routes read it to skip the page while the flag is off. See
   * ResourceDescriptor.feature.
   */
  feature?: string;
}

/**
 * One name field of a resource: the name of the row an id field points to, where the
 * pointed-to resource says what its rows are known by (`@name`, the tenant record's
 * `@tenantName`, or an enumeration table's description). It is spelled as the id field
 * with a trailing `Id` dropped, then `Name`: `squadronId` gives `squadronName`.
 */
export interface NameFieldMeta {
  /** The name field's JSON name, as the row type carries it. */
  fieldName: string;
  /** The id field the name stands beside, by JSON name. */
  of: string;
  /** Set when the pointed-to name is declared `pii`: a filter by it travels in the body. */
  pii?: true;
}

export type Meta = MethodMeta | ResourceMeta;

export type ResourceMap = Record<Resource, ResourceMeta>;

export type NullBoolean = null | true | false;
