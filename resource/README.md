# resource

The `resource` package provides permission-enforced CRUD over Spanner-backed resources:
the `resource` package is the runtime (query decoding, permission enforcement, patch
sets), and the `generation` package at the module root is the Resource Generator that turns annotated source
structs and schema migrations into handlers, routes, request structs, and TypeScript —
including `zz_gen_api.ts`, the typed client surface the framework-neutral
[`@impulseframework/resource`](../web/projects/resource/README.md)
runtime interprets (see the [lodestar demo](../lodestar/README.md)).

Every generated handler is a **frame** around the application's code: the generated
part of one request, from reading the route parameters and the body, through the
permission check at the gate and the row lookup within the caller's tenancy, to the
call into the application and the response, refusals included. What sits inside the
frame is the application's own: an RPC method's `Execute`, a computed resource's
`Read<Name>` and `List<Name>`, a file's content function. The frame owns the response
writer, the transaction, and every status the application does not choose through
`@answers`, which is the guarantee the rest of this document leans on: JSON in, JSON
out, permission-gated, transactional. This document says "the generated frame", or just
"the frame", for that part and "the body" for the application's code inside it.

## Annotation and Struct-Tag Reference

The generator and the runtime are driven by three small vocabularies, all defined in
this document:

1. **Comment annotations** (`@resource`, `@suppress(...)`, …) written in doc comments,
   parsed by the generator.
2. **Struct tags you write** on your source structs (`spanner`, `conditions`, …),
   read by the generator.
3. **Struct tags the generator writes** into `zz_gen` request structs (`json`, `perm`,
   `immutable:"true"`, …), read back by the `resource` runtime. You never write these
   by hand — they are listed so you can read generated code, not so you can author it.

Completeness is enforced by tests: every keyword registered in
[`resourceKeywords()`](../generation/types.go) and every tag-key constant in
[`generation/annotations.go`](../generation/annotations.go) and [`tags.go`](tags.go) must
appear in this document, so a new annotation cannot land undocumented.

The [lodestar demo app](../lodestar/) is the living example; links below point into it.

## 1. Comment annotations

Annotations are written in the doc comment of the declaration they configure. Only
comment lines that **start with `@`** are parsed; anything after the annotation on the
same line is treated as a comment. Arguments go in parentheses, comma-separated;
annotations that take no arguments are written bare. The rule is positional: a prose
line that happens to begin with an `@` word is parsed as an annotation too, and one
that repeats a keyword is refused as used twice, so start such a line with another
word.

```go
// Ship is a starship registered with the port authority.
//
// @resource
type Ship struct { ... }
```

| Annotation | Attaches to | Arguments | Effect |
| --- | --- | --- | --- |
| `@resource` | struct in the resources package | none | Marks the struct as a resource backed by a Spanner table. The generator emits query builders, request structs, handlers, routes, and TypeScript for it. Example: [Ship](../lodestar/pkg/resources/ships.go). |
| `@virtual` | struct in the resources package | none | A resource backed by a view instead of a base table. Because there is no table metadata, indexed fields must be declared with `index`/`uniqueindex` tags (see §2), and the read identity (if any) with `@primarykey`. A view that declares its key serves a keyed read route beside its list, as a table does (`@suppress(readHandler)` removes it). Without a key the view is a whole read-only list, with no read route and no paging: one list route serves every row the filter admits in one response, sorted when a `sort` is asked or an `@order` declared, a numeric `limit` or a `cursor` is refused with a 400 naming `@primarykey` as the way to page (section 4), `@page` is refused at generation, and its metadata says `readDisabled`. A view that carries a table's rows one to one declares the table with `@rowsOf`, so a page listing the view writes to the table; without it the view is a read-only list. A view declares two bindings on fields of its own, each in its bare form, since a view has no schema to resolve a join path against: `@domain` on the tenant column its query selects, and `@attribute` on a column a grant's condition reads by name (a fact about the row, which the view's query computes). A view keyed by one field may be a method's `@target`: the method's Execute grant is then judged against the row, within the tenant (see `@target`). Examples: [OpenMissionsBySquadron](../lodestar/pkg/virtualresources/open_missions_by_squadrons.go); [SectorPilot](../lodestar/pkg/virtualresources/sector_pilots.go), a view with a fact that is a method's target. |
| `@computed` | struct in the resources package | none | A read-only resource (List/Read only) whose rows are produced by hand-written query logic rather than a table. Primary-key fields are marked with `@primarykey`, and the key is optional: without one the resource is a whole read-only list, with no read route and no paging (one list route, every row the filter admits in one response, sorted when asked or declared; a numeric `limit` or a `cursor` is refused naming `@primarykey` as the way to page, `@page` is refused at generation, and the Collection registers List only, so a Read grant on it is refused at deploy). Example: [StandingOrder](../lodestar/pkg/computedresources/standing_orders.go). A computed view that carries a table's rows one to one declares the table with `@rowsOf`, so a page listing it writes to the table. Its permission checks run when the request is decoded, since the rows come from a Go function and no query of the library's. A grant on it may still carry a condition that reads the row: the struct declares the fields a condition may read with a bare `@attribute(name)` on the field, and the library judges each row the function yields, in Go, by the rules a table's rows follow (a row the condition on the resource refuses is not listed, not counted, and read as a row that is not there; a field granted under a condition the row does not meet is left out of that row; a row that would show none of the fields a grant covers is left out). The condition reads the declared fields, `subject`, `now`, and the facts about the caller that are held in tables (`subject.<name>`, a subject set or a subject value). A table's statement reads such a fact as a subquery beside its rows; for a computed resource the library reads it once for the request, when the request is decoded, through the resource client, by a statement built from the same pieces (the anchor table, the caller's rows on it, the anchor's own tenancy filter, a dotted value hop by hop). So a request costs one read for each fact its conditions name, whatever the number of rows, and none where no condition names one. The read is implemented for Spanner, the database the resource client reads; the PostgreSQL statements are rendered and proven by test, as the rest of the PostgreSQL rendering is. Deploy-time role migration validates a computed resource's condition as a table's, against the fields the resource declares, and the generated Collection marks the resource computed so a refusal names that declaration. Example: Lodestar's Flight Lead reads the [expense manifests](../lodestar/pkg/computedresources/expense_manifests.go) of the missions their own squadrons fly (`assignedSquadron IN subject.squadrons`), through the list, the read and the rendered file. A field a list sorts, filters or searches by needs a grant with no condition left once the request is checked (an unconditional grant, or one whose conditions read the time of the request alone, while one of them holds), and `qSet.TakePage()` answers false while a condition is left to judge the rows by. Example: the roles a tenant picks from (section 22). From the browser a computed resource answers a list query exactly as a table does, with no code in its List function: the generated handler applies the filter, then the sort when the request asks one or the struct declares one (`@order`), then the page (cursor, Link header, count) over the rows the function yields; with neither a `sort` nor an `@order` only the whole list (`limit=all`) is served, the rows in the order the function yielded them, after the filter; a paged request without an order is refused (section 4). A field is filterable when it carries `allow_filter`, as on a table; `index` and `uniqueindex` are refused (there is no database index), and `allow_filter` on a field the in-memory evaluator cannot compare fails generation with the field named. A List function that runs its own queries may take pieces of the work to push down — `qSet.Filter().Take(fields…)` returns the conditions on those fields and leaves the handler the rest (defined on an AND-only filter; an OR takes nothing; a field is taken whole, so a body that takes a text field answers its matches too, `Condition.MatchesText`, and one that takes the box reads its `SearchTerms` and `SearchFields`, section 4), `qSet.TakeSort()` returns the total order (empty when the list is not sorted: the body's own order stands) and marks it the body's, and `qSet.TakePage()` returns the page bounds only when paging in the body is correct (nothing left to filter, the sort taken, no count asked) and otherwise answers false so the handler pages. Taking is the only way to opt out, so a body can never make the result wrong. A computed list sorts and pages `NULL` where the application's database does, the same end as the tables beside it (Spanner first ascending and last descending, PostgreSQL the reverse); a body's plain `ORDER BY` already produces that placement, and the generated decoder carries the database type so the handler's in-memory sort and page boundary agree with the rows a body yields. Example: [SectorHazardBoard](../lodestar/pkg/computedresources/sector_hazard_boards.go). |
| `@rpc` | struct in the rpc package | none, or `max: 8MB`, `log: onEvent`, `trace: off` | Declares an RPC method: the struct's fields are the request payload, and the struct declares `Execute` in one of two forms, read off its signature — `Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) error` runs inside the generated handler's read-write transaction; `Execute(ctx context.Context, client resource.Client, rpcClient *Client) error` runs outside one. The third parameter is the application's own RPC client type. `Execute` returns `error` alone, and the handler answers with an empty 200, or `(Result, error)` with `Result` a struct type or a pointer to one, and the handler answers with the result after the transaction commits, mirrored with generated wire names (a nil pointer answers empty). A method answers with identifiers and outcomes, never rows: a `@resource` or `@computed` struct in the result position is refused, since rows are read through their own routes, where permission masking applies. A struct with no `Execute`, or one of another shape, fails generation naming the struct; no interface declaration takes part. Example of an answering method: [InspectShip](../lodestar/pkg/rpc/inspect_ship.go). The generated frame stamps the caller into the context before `Execute` (`resource.CallerFrom`), so a body may arm a typed patch or query with `.Enforce(caller)` and run the pipeline the resource routes run, or ask `caller.Check` for a decision to branch on; without either the body is trusted, the default. A transaction-form method honors `X-Dry-Run: true`: the whole frame and the body run and the transaction rolls back, refusing exactly as the real call would and answering an empty 200 where it would have succeeded; a client-form method refuses the header with 400. The generated TypeScript method metadata says which a method is (`dryRun: true` on a transaction-form method, nothing on a client-form one), so a page knows a dry run previews the call before it sends one, and names on each request field the name a grant's condition reads it by (`attribute: 'role'` for a bare `@attribute(role)` on the field): the Angular library's method dialog rehearses a method this way where the caller's grant on it carries a condition, waiting for the fields a condition may read and marking them when the dry run is refused as not permitted. Examples: [HoldMission](../lodestar/pkg/rpc/hold_mission.go) (armed), [CompleteMission](../lodestar/pkg/rpc/complete_mission.go) (decision as data). Gated by the `Execute` permission. The struct lives in a file named after it in snake case (`claim_mission.go`), as every generated kind does; a name ending in `Test` carries the `_rpc` marker instead (`start_flight_test_rpc.go`), so neither it nor the `zz_gen_` files generated beside it are Go `_test.go` files. Example: [ClaimMission](../lodestar/pkg/rpc/claim_mission.go). `max:` bounds the method's request body (a byte count or `KB`/`MB`/`GB`, 1024-based), applied by its generated handler in place of the application's limit (section 8, request body limits); a method that declares none takes that limit. Example: [IssueBulletin](../lodestar/pkg/rpc/issue_bulletin.go). `log:` is the method's own request log word, `always`, `onEvent`, `sampled` (with `fraction: 0.01`) or `never`, and `trace:` its own trace setting, `followFrontEnd`, `capped` (with `rate: 0.1`) or `off`: the nearest declaration, over its outlet's and the application's (section 8, request logs and traces). A word is refused on a scheduled method, which declares it on `@schedule`, and with `@suppress`, where no route carries it; a trace setting needs `GenerateRouter`. Example: [IngestDroidReports](../lodestar/pkg/rpc/ingest_droid_reports.go). |
| `@enumerate` | named type with underlying type `string` | enum table name | Generates typed constants for the named type from the rows of an enum table, and declares the table an enumeration: its rows are the program's constants, fixed at generation time. Two rules follow. A table-backed field whose foreign key points into the table is emitted as an `enumerated` picker whose values ride in the TypeScript metadata (`enumeration: [{ id, display }]`, Description as the display), so the browser needs neither a request nor a List grant, and this wins even when a struct also exposes the table as a resource. A `@resource` struct backing the table is read-only by derivation: no patch handler, no Create/Update/Delete in the collection, fields marked `readOnly`, and create or update defaults, validators, a delete rule, or a manual patch Set on it fail generation. A table is an enum table when it has an `Id` column and a `Description` column (the generator runs `SELECT DISTINCT Id, Description` against the migrated schema); a `Description` column on a table keyed otherwise, such as the feature flags or an auth's roles, names no enumeration. An enum table with more than 500 rows warns at every generation, since each field keyed into it carries every row in the TypeScript metadata; the warning names the bytes and the fields, and the way out is to drop `@enumerate` from the type and expose the table with a `@resource` struct, whose picker reads it whole or, under a `@page` maximum, one page at a time. Example: [ShipRole](../lodestar/pkg/resources/enums.go) behind [ShipClass.RoleID](../lodestar/pkg/resources/ship_classes.go). |
| `@enumerate` | field of an `@rpc`, `@resource`, `@virtual`, or `@computed` struct | resource or enum table name | Declares which resource a picker for the field lists — the field holds another resource's identifier, and the generator alone says which. The generated metadata emits `displayType: 'enumerated'` with `enumeratedResource: Resources.X`; the browser narrows and presents X's rows and never chooses another resource. X must exist — a table-backed, virtual, or computed resource — and be keyed by a single column, since a picker stores one key. On a list field of an `@rpc` struct (a list of plain values) the field holds several picked keys and the metadata emits `displayType: 'enumerated[]'`; a list field of a table, a view or a computed resource refuses the annotation, since a column stores one key, and so does a list of nested values; a picker of several reads X whole, so an X with a `@page` maximum fails generation there. A picker reads X in one of two ways, chosen by X's `@page` maximum: with none, the whole list in one request (`limit=all`), the chosen row resolved from it, so X may suppress its read; with a maximum, one server page at a time, the chosen row read by key, so X must serve a read — a bounded X with a suppressed read fails generation naming the two ways out (keep the read, or drop the maximum), on this path and on an undeclared foreign key into a routed resource alike. A list field cannot declare one: the field would store many keys where a picker stores one, so `@enumerate` on a slice, an array, or a named list type fails generation on every path naming the struct and field (`struct Ship field Tags: @enumerate on a list field; a picker stores one key`), and a byte slice, one value on the wire, is not a list here. Four rules: a **plain column** (no foreign key) names the resource the picker lists, with no Go validation and no database check, as for an identifier held from outside the schema (example: [Mission.BriefingTemplateID](../lodestar/pkg/resources/missions.go) → [BriefingTemplates](../lodestar/pkg/computedresources/briefing_templates.go)); a **foreign key** may name a resource other than its target whose rows key into the same space, typically a virtual view over the target carrying display columns the target lacks, while the constraint stays the guard at write time — naming the target itself is refused as redundant, the schema already says so (example: [Mission.ClientID](../lodestar/pkg/resources/missions.go) → [ClientRosters](../lodestar/pkg/virtualresources/client_rosters.go)); naming an **enum table** — one an `@enumerate` type declares, with or without a struct over it — renders the field from the inline `enumeration` values exactly as an inferred key into that table does, one rule for declared and inferred (example: [FailMission.ReasonID](../lodestar/pkg/rpc/fail_mission.go)); and a declaration on a foreign key that **already points into an enum table** is refused as a contradiction, since those values are program constants baked into the metadata. A request field has no schema, so it always declares (example: [CompileBriefing.TemplateID](../lodestar/pkg/rpc/compile_briefing.go), naming a computed resource). A declared enumeration follows the RPC outlet rule: naming a resource that is not on the target outlet fails generation with the fix named, where an inferred key degrades to its plain type. |
| `@suppress` | `@resource`, `@computed`, or `@rpc` struct | one or more of `listHandler`, `readHandler`, `patchHandler`, `allHandlers`, `allRoutes` | Skips generating the named handlers, or all routes. Suppressing `readHandler` on a resource a picker lists is refused where the resource declares a `@page` maximum (`@enumerate`). Suppressing `patchHandler` also removes the resource from the consolidated patch handler. `allRoutes` is rejected on consolidated resources unless the patch handler is suppressed or the resource is excluded from consolidation. On an `@rpc` struct, any argument suppresses the generated handler. |
| `@defaultsCreateType` | `@resource` struct | type name | The generated create path calls `Defaults()` on the named type to set defaults when creating the resource. |
| `@defaultsUpdateType` | `@resource` struct | type name | As above, for updates. |
| `@validateCreateType` | `@resource` struct | type name | The generated create path calls `Validate()` on the named type to validate the incoming resource. |
| `@validateUpdateType` | `@resource` struct | type name | As above, for updates. |
| `@validateDeleteType` | `@resource` struct | type name | A delete rule no grant gets around. Before a row of the resource is deleted, the generated delete calls `Validate()` on the named type with the delete patch, inside the delete's transaction, once the caller's grants and their conditions have passed and before anything is buffered. An error it returns refuses the delete and nothing is deleted: `httpio.NewBadRequestMessage` answers 400 with the rule's own sentence, which is what the person reads. Who may delete which rows stays in grant conditions; the rule is for what no role may do, such as a retention period, which a condition cannot hold because conditions add up across a person's grants. A caller the grants refuse hears their Forbidden and never the rule's sentence. A read-only enumeration table refuses the annotation. Example: [SortieExpenseDeleteRule](../lodestar/pkg/resources/sortie_expenses.go). |
| `@deleteCascade` | `@resource` struct | kinds, then `refusedBy:` kinds | Declares what a delete of the record takes with it and what refuses it. `@deleteCascade(Project, Task, TaskComment, refusedBy: Invoice)` on `Client` lists the kinds of row that go with a client, in any order, and after `refusedBy:` the kinds that keep a client from being deleted while it has one. The generator reads what is under the record from the schema, the foreign keys and interleaves that lead up to it, and holds the list to it: every kind under the record is named in one list or the other, or generation fails naming the kind, the record and the way between them. A kind that is under the record only through a refusing kind needs no place, since it has no row there without a row of the kind that refuses. A name that is no resource, a kind that is not under the record, a kind named twice, and a read-only table among the kinds taken fail generation the same way, each in a sentence that says what to change. For the record the generator writes `DeleteTree()` on the resource (`resource.DeleteTree`): the kinds taken in the order their rows are deleted, deepest first, each kind's ways up to the record, and what the delete of one row costs in mutations (`resource.DeleteCost`: the delete, an entry in each index of its table, the change record where changes are tracked, and the release of each stored file). `DeleteTree.Mutations` is the one count of a whole delete, held against the 80,000 mutations Spanner takes in a commit (`resource.DeleteMutationLimit`). A delete of a record with a row of a kind listed under `refusedBy` is refused before the commit in a sentence that counts what refuses it. A delete of a record with rows of the kinds it takes asks first: it deletes nothing and is answered 428 with a question that names what will be deleted, and the same delete saying it takes them (`"everythingUnder": true` as the remove operation's value) deletes those rows in code, each as its own delete would delete it, and the record last. A record that declares nothing is refused while it has rows under it that the database does not remove with it (section 10). The tenant record takes no `@deleteCascade`: the library declares its description, every kind of the application's rows that carry the tenant's key or lead to a row that does, with nothing listed to refuse it. A record lists its own kind where its table has a foreign key to itself (an answer names the message it answers): `@deleteCascade(DistressRelay)` on `DistressRelay` takes the answers under a relay, however deep, and a table with such a key is a kind under its own record like any other, to be named in one list or the other. A kind taken with another record carries its link to itself in the description (`DeleteKind.Within`), so that record's delete takes the rows under its rows too. A foreign key to columns that are not the other table's key (a case number) is a way up like any other: the description holds the columns each step references (`DeleteLink.References`), and the rows are found by them. Example: [Mission](../lodestar/pkg/resources/missions.go), [Client](../lodestar/pkg/resources/clients.go), and [DistressCall](../lodestar/pkg/resources/distress_calls.go) with [DistressRelay](../lodestar/pkg/resources/distress_relays.go). |
| `@primarykey` | field of a `@computed` or `@virtual` struct | none | Marks the field as (part of) the resource's primary key; multiple annotated fields form a compound key in declaration order. Primary-key fields are exempt from field-level permission enforcement (their readability follows the resource-level grant). The key is what gives a row identity: the read route (`/{key}`), the cursor's tie-break (the key fields end every order, so a keyset cursor walks a total order), and the browser's row identity (`keyOf`). Without a key the resource is a whole read-only list, with no read route and no paging: served in one response, sorted when asked or declared, refusing a numeric `limit` or a `cursor` with a 400 that names `@primarykey` as the way to page, and refusing `@page` at generation. Rejected on table-backed `@resource` structs, whose keys come from the schema. Compound example: [SectorHazardBoard](../lodestar/pkg/computedresources/sector_hazard_boards.go); key-less example: [StandingOrder](../lodestar/pkg/computedresources/standing_orders.go). |
| `@rowsOf` | `@virtual` or `@computed` struct | table resource name | The view declares its backing table, a create goes into the table, and the new row shows up in the view on the next list because the view's SQL reads that table. The declaration states row identity, not columns: every row of the view is one row of the named table under the same key, and the view's other columns are its own — joined from other tables, aggregated, or computed in the projection; the generator compares none of them to the table. It checks, each refusal naming the fix: the named resource exists and is a table-backed `@resource` (a `@virtual` or `@computed` target is refused, since a view cannot back a view, and an `@enumerate` table — with or without a struct over it — is refused as a contradiction, since its rows are the program's constants and a create button for them would be policy about nothing); the view declares `@primarykey` fields matching the table's key in count, order, Go field name, and Go type, nullability included, and on a `@virtual` the `spanner` columns as well, the message naming the first mismatch and the table's key; the two share a `@permissionScope`; the table is served on every outlet the view is served on (fail loud, never a silent degrade); and the view does not name itself. Refused on `@resource` and `@rpc` structs. Several views may name one table. The view's TypeScript metadata carries `rowsOf: Resources.X`, so a page listing the view sends its create, edit, and delete to the table and opens a row on the table's page; a view without the declaration is what it is today, a read-only list. One backing table only: two tables sharing one key (a table and its one-to-one extension) is the door left open and not built, and a parent joined to its children is never one to one with anything and stays a read-only list or an RPC. Examples: [MissionBoard](../lodestar/pkg/virtualresources/mission_boards.go), a same-row view over Missions; [SquadronRoster](../lodestar/pkg/virtualresources/squadron_rosters.go) and [PilotAssignment](../lodestar/pkg/virtualresources/pilot_assignments.go), two association views over SquadronMemberships whose compound key is edited and deleted from the list. [OpenMissionsBySquadron](../lodestar/pkg/virtualresources/open_missions_by_squadrons.go) and [FeeByKind](../lodestar/pkg/virtualresources/fee_by_kinds.go) declare nothing and stay read-only lists. |
| `@changedBy` | `@virtual` or `@computed` struct | table names | The tables whose changes change the resource, so a commit on them reaches the resource's live pages. On a virtual resource the declaration replaces what the generator reads off the query (the text behind its `Subquery` method, an embedded file, a string literal or a concatenation of constants, walked with memefish's lexer for the names in table positions, section 14), so it can drop a table the walk misread as well as add one the query does not show; the generator still reads the query, and a declaration that differs from the reading is a warning naming both sides, never a refusal. It is the way out when the reading refuses (a name in a table position that is neither a table nor a CTE in scope, a recursive `WITH`, a text the lexer refuses) and required for a query built at run time. A computed resource is Go, so the declaration is its only source: without one its changes are not published, its pages stay plain, and nothing refuses. The `@rowsOf` root must be in the set, read or declared, and a name the schema lacks is refused; refused on `@resource` and `@rpc` structs. The sets are recorded next to the resources, as each package's generated `LiveDerivations`, and assembled in the handler package's `zz_gen_live_derivations.go` (section 14), so what the generator concluded shows in the diff, and the resource's TypeScript metadata says `live: true`. A table of an Auth's generated management (section 22) is read or declared like any other and recorded by its name, since the type over it is internal to the management's tree. Examples: [MissionBoard](../lodestar/pkg/virtualresources/mission_boards.go), read off its query; [SectorHazardBoard](../lodestar/pkg/computedresources/sector_hazard_boards.go), declared. |
| `@rowWord` | `@resource`, `@virtual` or `@computed` struct | the word for one row, and optionally `plural: <word>`: `@rowWord(pass)`, `@rowWord(person, plural: people)` | The word a person calls one row of the resource by. A generated list speaks of a row in three places, the button that adds one ("Add a hangar pass"), the question before one is deleted ("Delete this hangar pass?") and the message after ("Hangar pass deleted."), and takes the word from the generated metadata (`rowWords: { one, many }`). A resource that states nothing goes by its name split into words (`HangarPass` is "hangar pass", and several are "hangar passes", the plural the generator names the collection by), the tenant record by the label it declares (`@tenant(label: ...)`), and a view that declares its table (`@rowsOf`) by the table's word. A name that ends in an acronym keeps the acronym's letters where there are several (`PilotID` is "pilot id", and several are "pilot IDs"). State the word where the name is not what people call a row, and the plural where the plural rule misses it. Write the word alone, in the case it has inside a sentence: the screen puts "a", "an" or "this" before it and capitalizes it where it opens a sentence, so a word that opens with an article is refused. Refused on an `@rpc` struct, which lists no rows. |
| `@typescript` | the declaration of a type used as a field: a struct, or a named type over another type (`type Position json.RawMessage`) | `Name`, or `Name, from: "module"` | Declares the TypeScript type of a Go type whose shape lives outside Go, once, on the type's declaration, and every generated file that carries the type imports `Name` from `module`, spelled verbatim. A table column, a computed field, or an RPC field typed by it is `Name` in its interface, with display type `object` in the metadata. Without `from:`, `Name` must be a TypeScript built-in (`string`, `number`, `boolean`, `unknown`). A plain struct needs no declaration: its interface is derived from its fields (section 12). A type that writes its own JSON (`MarshalJSON` or `UnmarshalJSON`) must declare, since its fields say nothing about the wire, and is refused without it. A type declared over `json.RawMessage`, or over any type with JSON methods, needs no hand-written methods: the generator writes its JSON pair (section 12), so the type is its declaration and this annotation and nothing else; `json.RawMessage` itself is `unknown` without any declaration. A declaration promising anything but `string` on a plain byte slice (`type Token []byte` with no JSON methods) is refused, since the wire carries base64 there. A slice comes from the field (`[]Position` is `Position[]`), never from the declaration; the declaration is read wherever the type is declared, in the application or in a dependency. Two declarations importing one `Name` from different modules are refused, as is `from:` on a built-in and a non-built-in without it. Not valid on a `@resource`, `@virtual`, `@computed`, or `@rpc` struct, which are typed field by field. Example: [Position](../lodestar/pkg/resources/distress_calls.go). |
| `@attribute` | field of a `@resource` struct (bare form also on a `@virtual` struct and on a field of an `@rpc` or `@computed` struct) | `name[, via: Remote.Segments]` | Declares an attribute binding: the vocabulary name grant conditions reference for this row attribute (ABAC). Bare, the annotated column itself carries the attribute; with `via:`, the binding is a join path leaving through the annotated foreign key — `via:` carries only the remote segments, Go field names on each successive struct, dotted for multi-hop (`via: StationId.Sector`), and every hop must resolve many-to-one through a real foreign key or generation fails. On a virtual resource only the bare form is valid, naming a column the view's projection carries: a fact about the row is a column the view's query selects, whatever join computes it, and a `via:` there is refused with that as the way to the same fact. A view's attribute is a table's from there on: the collection carries it, `access.ValidateRoles` validates a condition's names and literals against it, a grant on the view (the resource's or a field's) may carry a condition that reads it, and so may the Execute grant of a method whose `@target` is the view. Example on a view: [SectorPilot](../lodestar/pkg/virtualresources/sector_pilots.go). On a field of a `@computed` struct the bare form names the row's own Go field: the row comes from the resource's function, so there is no schema for a `via:` path (a fact from elsewhere is a field the function fills), the type is one the library compares in Go (a string, an id, an integer, a float, a bool, a `time.Time` or a `civil.Date`, a pointer to one, or a list of one), and the library judges each row the function yields by reading the field (the `@computed` row). On a list column (an ARRAY: a slice field, never a byte slice) the bare form declares a list attribute, typed by one item: a condition that reads it is judged once for each item of the row's list, the whole condition each time with the item as the attribute's value, and holds only when it holds for every item. So `role != 'Administrator'` over the roles an invitation gives says none of them is Administrator, and `role IN ('Staff', 'Viewer')` that every one is Staff or Viewer. A list with no item, and no list at all, is judged once with no value: no value never permits, and `role IS NULL OR role != 'Administrator'` admits it. The database judges it in one statement (no item fails the condition), on Spanner and on PostgreSQL alike, and it is the rule a request's list field follows. A condition reads one list, on the left of a comparison, as the row holds it: a second list, a list on the right side, the list an update proposes (`new.`) and any list in a Create grant's condition are refused when the roles are deployed, and a list at the end of a `via:` path at generation. Example: the roles of a tenant's invitation (section 22). Every attribute is drawn in the package's generated `zz_gen_bindings.dot`: a `via:` path hop by hop, a bare attribute as a line in its resource's box, pointing at its table when the column is a foreign key (see `@domain`). On a field of an `@rpc` struct the bare form declares a request field a grant's condition may read: the method's Execute grant reads it as `new.<name>`, the word a Create grant uses for the row being written, so a role file can limit what its holder asks for (`new.role != 'Administrator'` on a method that gives a role). The field is a string, an id (`impulse.UUID`), an integer, a float, a bool, a `time.Time` or a `civil.Date`, a pointer to one, or a list of one; a decimal, a database wrapper (`spanner.NullString`) and a struct are refused at generation, since the value is compared as it was decoded. The condition is settled the moment the request is decoded, before any row is looked for: on a method with no `@target` the call is then allowed or refused, in the words a caller with no grant hears, and on a method with a `@target` the part that reads the row goes on to the gate, so one condition may hold both (`fliesElsewhere = false AND new.clearance <= 3`). A request field compares against literals alone: a comparison against `subject`, a subject value or set, `now` or an attribute of the row is refused when the roles are deployed, as is a `new.<name>` the method does not declare. A field left out of the request is its type's empty value, which the condition judges; a nil pointer is no value, and no value never permits. A list is judged once per element and every element has to pass; a list with no element is no value. A method declares one list field this way, and a second is refused at generation. The row's `capabilities=Execute` answer counts a `new.` term as possibly true, since no value is proposed yet, and the permission digest says `conditional`; a dry run of the method (`X-Dry-Run`) answers for a value. Examples: [SetSectorPilotClearance](../lodestar/pkg/rpc/set_sector_pilot_clearance.go), a request field beside a fact of the target row; the generated management's methods that give a role (section 22). Names follow the condition language's identifier rules (`[A-Za-z_][A-Za-z0-9_]*`); `subject`, `now`, and `new` are reserved; a name is declared once per resource, or once per method. |
| `@domain` | field of a `@resource` struct (bare form also on a `@virtual` struct) | none, or `via: Remote.Segments` | Declares the structural tenancy binding: how every row of a domain-scoped resource resolves to its tenant. Bare on the tenant-key column itself, or `via:` a foreign-key path to it (same grammar as `@attribute`). **Mandatory on every domain-scoped table-backed or virtual resource** — missing is a generation error, and so is declaring it on a resource that is not domain-scoped: global scope is the explicit opt-out (design plan §06). On a virtual resource only the bare form is valid, naming a column the view's projection carries. A bare `@domain` derives the tenant column's runtime behavior — never stated twice: the column decodes output-only (create and update closed, so the wire cannot express a tenant write or re-tenant a row) and the framework stamps it from the request's domain partition on create, so the checked domain and the written domain are the same value by construction; restating behavior through `conditions` or `default_create_fn` tags is rejected, and Create/Update on the column are ungrantable while reads stay grantable. Deliberately not an `@attribute` — it is consumed by tenancy injection and never referencable from grant conditions. At most one per resource. It also tenant-filters the subject subqueries anchored on the resource (see the subject rows). The `via:` path is resolved through foreign-key metadata, not through the remote resource's own bindings — a domain-scoped parent table does **not** transitively supply tenancy to resources referencing it; each resource declares its own `@domain`. Whether a resource carries a bare `@domain` or reads its tenant through a `via:` path, and whether a foreign key between two resources that each carry a bare `@domain` holds the tenant column of each, is the schema's choice, and generation takes either (section 10, "A link between two records of a tenant"). Generation warns at every run for a listed table-backed resource whose lists the schema does not serve under this binding: a bare `@domain` with an `@order` and no index leading with the tenant column and then the order columns, where the warning names the index wanted, or a `via:` path, whose lists scan the whole table (section 9). Every attribute and subject binding a package declares, and every `@domain(via:)` path, is drawn in one committed, drift-tested `zz_gen_bindings.dot` beside the workflow graphs: a box per resource declaring a binding, listing its bare bindings as `name: column` (`subject.name: column` for subject vocabulary); dashed boxes for the tables a path lands on or a foreign key points at; a `subject` ellipse for the requester, entering each anchor once with every set and value it yields there; one edge per hop drawn once however many paths share it (solid attribute, dotted domain, dashed subject), labeled `name: column` on a continuing hop and `name: column ⇒ column` where the path terminates; and a thin gray edge from a bare binding on a foreign key to its table, so the two names a condition pairs (`assignedSquadron IN subject.squadrons`) meet at one table. The bare tenant column is not listed, the state column's reference to its enum table is not drawn (the workflow graph covers the states), and a package declaring no binding writes no file. Example: [Lodestar](../lodestar/pkg/resources/zz_gen_bindings.dot). |
| `@state` | field of a `@resource` struct | `default: <value>` | Marks the resource's state column (ABAC design plan §09). The column must be a foreign key to its state enum table (the ordinary Id/Description convention — the FK identifies the table, nothing is declared on it), and the declared default must be one of that table's values. The marker derives the field's behavior — never stated twice: the field decodes output-only (create and update closed, so the wire cannot express a state write; transitions happen inside RPC bodies), Create/Update on it are ungrantable, reads stay grantable, and the generated create patch applies the declared initial state on insert (never a database DEFAULT). State values change only by migration: a mutation permission registered against the state enum table is a generation error, while Read stays grantable. |
| `@stateRoot` | FK field of a `@resource` struct | `RootStructName` | Declares workflow membership (ABAC design plan §09) on the foreign key anchoring the member — the field is the hop, so only the workflow root's struct name is spelled. Each member declares only its immediate hop; chains compose through other members and must reach the root (a cycle or a hop outside the workflow fails generation), every hop is many-to-one through a real foreign key, and member and root permission scopes must match. The generator synthesizes the uniform `state` attribute binding on the root (a column binding on its `@state` field) and every member (a join path through the chain), so one condition text reads identically across the workflow, and emits the assembled graph as a committed, drift-tested `zz_gen_workflow_<root>.dot` file per stateful root: the whole tree — root and members as solid nodes with one edge per hop labeled with the anchoring FK field, dashed context nodes for every FK reference leaving the tree (the tenant record included), a state cluster with every value and the default marked, one labeled edge per declared `@transition`, and a legend; undeclared state changes stay RPC-body business rules the framework cannot see. The TypeScript resource metadata carries the same facts (root, members with hops, states, default, transitions) as a `Workflows` constant, so a frontend can render the graph itself. A resource belongs to at most one workflow. |
| `@transition` | `@rpc` struct | `RootStructName, from: a[, b…], to: c` | Declares the RPC method as a workflow state transition (ABAC design plan §09): the method moves rows of the named root resource along one edge. The root must carry `@state`, every `from`/`to` value must be a value of its state enum table, method and root permission scopes must match, and the struct's `Execute` must be the transaction form, carrying exactly one `@target` field. The generated handler owns the mechanical frame inside the transaction it already runs: before the body it locates the target row within the tenancy predicate (absent or cross-tenant is NotFound) and verifies the pre-image state is in the `from` set, then evaluates any row-referencing condition the caller's Execute grant carries against the same located row — either refusal is one uniform Forbidden naming the method and the row, so the wire never says whether the state or the condition said no (§12); after the body returns without error it stamps the `to` state as the last mutation. The body never reads or writes the state field — it carries only the edge's business effect. Who may run the method stays its Execute grant (grants-only, §09). The declared edge travels in the generated Collection, draws labeled edges in the workflow DOT file, rides the TypeScript method metadata, and answers `capabilities=Execute` per row. Example: [LaunchMission](../lodestar/pkg/rpc/launch_mission.go). |
| `@target` | field of an `@rpc` struct | none, `RootStructName`, or `RootStructName, key: KeyField` | Marks the field carrying the target row's key, its type matching the key column it carries. A method addresses one row: a target keyed by one column is named by one field, and a target keyed by several is named by one field for each column, every one declaring `@target(Root, key: KeyField)` with the key field it carries (the fields may be declared in any order; a column left uncarried, a column carried twice, a field naming no key field, and fields naming two resources are generation errors). With `@transition` it is bare and single (the declared root is the target, and a workflow root is keyed by one column); without one, `@target(Root)` names the row resource directly and the method gets the plain located-row form (ABAC design plan §12): the generated handler locates the row inside its transaction (absent or cross-tenant is NotFound) and evaluates any row-referencing condition on the caller's Execute grant against it, with no state check and no stamp. Either way, a targeted method's Execute grants may carry row conditions — `access.ValidateRoles` validates them against the target resource's binding vocabulary — and the method joins the target resource's per-row `capabilities=Execute` answer. Requires the transaction form of `Execute`; method and target permission scopes must match. A domain-scoped target resolves tenancy through its `@domain` binding, either form: a bare tenant column is read off the located row, a join-path binding is verified with one query in the same transaction — absent and cross-tenant rows answer the same NotFound either way. In the plain form the target may be a view (a `@virtual` struct) keyed by its `@primarykey` field or fields. A view's row is one row of the view's query, and in a tenant-scoped view its key names a row in each tenant that holds one — the tenant column is not part of the key, since `@domain` already holds a row to its tenant, and a key that includes it is refused — so the handler reads no row by the key: it hands the gate the view's own value, named in the package the view is declared in, and one SELECT over the view's query, by the key within the request's tenant, both locates the row and evaluates the grant's condition against it (`ExecuteGate.EnforceView`), a condition that may read the view's `@attribute` facts. No row there answers NotFound, whether the key names nothing or another tenant's row; a key that names two rows there fails the call, as a fault of the view's query. Refused on a view: `@transition`, since a view has no `@state` to move; and a view with no `@primarykey`. The TypeScript method metadata names the target as `target: { resource, key }`, `key` mapping each request field to the row field it carries, so a page fills the request from a row. A key field of text (a `string`, or a named type over one) that does not declare `validate:"required"` is one the method takes empty, and the empty text is a value of the key there, as the group of a role a person holds directly is: the metadata lists the field in `target.mayBeEmpty`, and a page sends the empty text a row holds there as it is. Every other part of a key (a text field declared required, an id, a number) holds a value on every row, and a page refuses a row that is empty there before it sends anything. Examples: [HailShip](../lodestar/pkg/rpc/hail_ship.go), a table's row; [SetSectorPilotClearance](../lodestar/pkg/rpc/set_sector_pilot_clearance.go), a view's; `RemoveCrewSectorRole` of the generated management (section 21), a view's row keyed by three columns, the last of which may be empty. |
| `@callerEnters` | `@rpc` struct | none, or resource structs | Says a row offers the method only to a caller whose session enters every tenant the row names. With no argument the method names its row with `@target` or `@transition`, and that row's struct names the tenants in a `TenantsToEnter` method (`resource.TenantsToEnter`). A method that names no row lists the resource structs whose rows a page runs it from (`@callerEnters(HeldRole, TakenRole)`), and each of them declares `TenantsToEnter`. It shapes what a page offers (`capabilities=Execute`) and refuses nothing: the method's own code refuses the call. |
| `@notOnOwnAccount` | `@rpc` struct | the row's field | Says the row the method names does not offer it to the person whose account the row is (section 5). The field named is the one of the row's struct that holds the account's id, an `impulse.UUID`. The method's own code refuses the call; the declaration shapes what a page offers. |
| `@answers` | `@rpc` struct | `200, 409` | Declares the statuses the method may answer with; the result type carries `HTTPStatus() int` and chooses one per response. Allowed: `200`, `201`, `202`, `204`, and any 4xx except `401`, `403`, and `404`, which stay the frame's own refusals; at least one must be a 2xx. A 4xx answer is the method's refusal with its typed body: in the transaction form the transaction rolls back first, so nothing the body armed commits. `204` writes no body and requires a pointer result returned nil, or an answerless method, whose only permitted declaration is `@answers(204)`. A result declaring `HTTPStatus()` without `@answers`, or the reverse, is a generation error; an undeclared status at runtime answers 500. The TypeScript client resolves `{ status, result }` for a method with declared statuses and still throws on every undeclared 4xx. Example: [CompleteMission](../lodestar/pkg/rpc/complete_mission.go). |
| `@upload` | `@rpc` struct | `max: 5MB`, and `store: S` for a named store | Declares the method as a multipart upload. Its `Execute` takes `resource.Files` third, `Execute(ctx, txn resource.ReadWriteTransaction, files resource.Files, client *Client)`, the transaction form only, since the transaction is what claims the files, and the declaration and the signature go together (either alone is a generation error). With `store: S`, the files go to the named store `S` (section 13) and `Execute` takes `resource.FilesIn[S]`, whose keys are `resource.Key[S]`; the declaration and the signature name one store or neither, and a mismatch is a generation error. The request is `multipart/form-data`: one part named `request` first, carrying the JSON the method's decoder reads exactly as for a JSON RPC, then one or more parts named `file`. `max` (a byte count or `KB`/`MB`/`GB`, 1024-based) bounds the whole body; over it is a 413 naming the maximum, no `file` part a 400, a body that is not multipart a 415. The frame streams each file to the method's store, read off the resource client, under a key it minted, runs the body with the files, and on any failure before commit deletes the objects it streamed and answers with the failure; after a commit nothing more happens, since the rows the body wrote claim the keys. The body records each key in a `@file` column of the same store, a `string` column for the default store and a `resource.Key[S]` column for `S`, whose generated setter takes the typed key, so a key of another store does not compile into it; a key recorded anywhere else is invisible to the release and is the orphaned-file cleanup's. An upload naming a store no `@file` column holds is refused at generation. Reading a file back is the `@file` route (section 13). A dry run streams nothing: the files describe the parts with empty keys. The TypeScript handle gains `upload(body, files)`, which refuses locally over the maximum. Examples: [AttachRefitPhoto](../lodestar/pkg/rpc/attach_refit_photo.go) on the default store, [AttachMissionDocument](../lodestar/pkg/rpc/attach_mission_document.go) on a named one. |
| `@warning` | `@rpc` struct | `typed: open` | Declares that the method warns before it runs (section 7): a person reads what the call will do, in the server's words, and types the word before the confirm button works. The struct declares `Warning(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*resource.Warning, error)` beside `Execute`, and the declaration and the method go together (either alone is a generation error). A request that carries `X-Warning: true` is decoded and checked exactly as the call is, answers the `resource.Warning` in place of running the call, and saves nothing; a method that declares no warning refuses the header with 400. The typed word is one lowercase word of 2 to 24 letters, which the generated metadata carries (`warning: { typed }`). Refused on the client form of `Execute` (the warning is read inside the handler's transaction, which is rolled back), on an `@upload` method, with `@suppress`, and with `@schedule`. |
| `@schedule` | `@rpc` struct | the cron expression in quotes, `zone: "<IANA zone>"` (UTC when absent), and `log: onEvent`: `@schedule("30 3 * * *", zone: "America/Denver")` | Declares the method a scheduled route: Cloud Scheduler calls it on the schedule, read in the zone. The expression is five fields separated by single spaces (minute, hour, day of the month, month, day of the week), each a comma list of `*`, a value, or a range, the `*` and the range optionally stepped (`*/15`, `1-5/2`), months and days of the week also by name (`JAN`, `MON`); an expression outside that, a zone the time zone database does not know, and `Local` are generation errors. A scheduled method takes no input: no field, no `@upload`, no `@permissionScope` (a domain scope is a path parameter, and a scheduled method checks no permission), no `@target` or `@transition`, no `@outlet`, `@feature`, `@formerly` or `@suppress`, each refused naming the struct, and it needs `GenerateRouter`. It is served at `POST /_scheduled/<method in kebab case>` behind the scheduler's token check alone, never on an outlet, never in the permission collection or a TypeScript client. `log:` is the method's own request log word (`always`, `onEvent`, `sampled` with `fraction:`, `never`), set ahead of its handler as a route's is (section 8, request logs and traces); a scheduled method's spans follow the front end, so `trace:` is refused here and on its `@rpc`. Section 19. Example: [PruneDroidReports](../lodestar/pkg/rpc/prune_droid_reports.go). |
| `@file` | field of a `@resource`, `@virtual`, or keyed `@computed` struct (the column holding the store key); or a keyed `@computed` struct | none, `segment`, on a field `name: Field`, `type: Field`, and `log: onEvent`, `trace: off` | A row says which stored object is its file, and the generator serves that file under the row's read route: `GET <read route>/content` answers the bytes with their type, name, size, time, and validator, gated by `Read` on the resource and a `Read` grant on `content`, the route's own field, which the Collection registers with no column behind it (`columns=content` on a read stays a 400; a grant naming it is accepted by `access.ValidateRoles`). On a field, the annotation marks the column holding the store key: `@file` bare serves under `content`, `@file(thumbnail)` under its own segment, and `name:` and `type:` name sibling columns carrying the file's name and media type (a struct may carry several, one per segment). The column's type says which store holds the file: `string` or `*string` is the default store, `resource.Key[S]` or `*resource.Key[S]` the named store `S`, a type the application declares by embedding `resource.Store` (section 13); both are `STRING` columns, and the generated `FileKeys()` names each column with its store. The key column goes off the wire in both directions: never returned on read or list, never accepted on create or update, absent from the TypeScript interface and metadata; a `NOT NULL` key means a row is added by the `@upload` method that stores its file, so `Create` is not registered and the patch handlers refuse a create op naming that way in, while a nullable key leaves `Create` ordinary. On a keyed `@computed` struct, `@file` or `@file(segment)` declares a rendered file: the computed package declares `<Name><Segment>(ctx, key…, qSet *resource.QuerySet[Name], client resource.Client, computedClient *Client) (*resource.Content, error)` beside `Read<Name>`, checked at generation as `Read<Name>`'s callers are, and a nil content is 404. Deleting a row that carries a key, or pointing it at another object, releases the old object: the patch machinery records the key on the transaction and the executor deletes it from the column's store, wired on the resource client (`resource.WithFileStore`, `resource.WithNamedFileStore[S]`), once the commit lands, from every transaction the application runs; one row owns one object, and a row the database deletes by cascade releases nothing (section 13). Under a suppressed read route the declaration serves nothing and still names the key column, so the release and the orphaned-file cleanup see its keys. `log:` and `trace:` are the file route's own request log word and trace setting, as on `@rpc` (section 8, request logs and traces), refused under a suppressed read route, where no route carries them. Refused at generation, naming the struct: a key-less struct, an unknown sibling, two declarations on one segment, a name or type field that is not a `string` or `*string`, a key field that is none of `string`, `*string`, `resource.Key[S]` and `*resource.Key[S]`, a `resource.Key[S]` column without the declaration, the struct-scope form on a table or view, a rendered file under a suppressed read route, and a content function that is missing or has another signature. Examples: [MissionDocument.StoreKey](../lodestar/pkg/resources/mission_documents.go), a stored file; [ExpenseManifest](../lodestar/pkg/computedresources/expense_manifests.go), a rendered one. |
| `@subjectSet` | user-id field of a `@resource` struct; with a `who:` written, one of the fields the `who:` names | `name, value: Field`, the two optional parts `who: <tests>` and `where: <condition>`, on a global-scoped anchor `domain: Field` or `domain: Field.Path`, and in an application with several auths `auth: <the auth's name>` | Declares subject-side set vocabulary: `subject.<name>` in grant conditions is the set of `value:` values on this table's rows whose annotated column matches the requesting user (`crew IN subject.crews`). The annotation designates the user-id column — no separate marker — and is repeatable per anchor; `value:` names the sibling Go field the set yields, dotted to continue through foreign-key hops with the same many-to-one validation as `via:`; every set is drawn in the package's `zz_gen_bindings.dot`: the requester enters the anchor once naming its sets, a bare set is listed in the anchor's box and points at its table when the column is a foreign key, and a dotted value continues as edges (see `@domain`). **Tenancy:** the rendered subject subquery is tenant-filtered by the anchor resource's own `@domain` binding, so a domain-scoped anchor must declare one — generation rejects it otherwise, because without it `subject.<name>` matches the user's rows from every tenant (a membership held at tenant B would satisfy conditions evaluated at tenant A). A global-scoped anchor is the deliberately shared pattern — a certification earned once applies everywhere — and stays unfiltered. Where a global table's rows still belong to tenants, the set says where a row's tenant is found, `domain:`. Where the row carries its tenant's key itself, `domain:` names that column (`domain: TenantID`), and `zz_gen_bindings.dot` lists it under the set in the anchor's box (`subject.keys domain: TenantId`). Where a row belongs to a tenant through a parent (a person's place in a group, the group being kept in one tenant or in none), it names a foreign key of the row and then the path to the column that holds the tenant's key (`domain: GroupID.DomainID`), validated many-to-one as `via:` is, and drawn in `zz_gen_bindings.dot` as an `@domain` path is, dotted, under the set's name (`crewGroups domain: GroupId ⇒ DomainId`). In a request that has a tenant the set then holds that tenant's rows alone, and a row that names another tenant, or none, is left out; in a request that has no tenant it holds every row. Generation refuses a `domain:` on a domain-scoped anchor (its own `@domain` is the filter), on a `@subjectValue`, on a path that does not start at a foreign key of the row, and on a column or a path's end that is not text. Text is all a column's type says, so where generation knows which column of the table a `domain:` ends on holds its tenant, the end must be that column, and the refusal names it: on a domain-scoped table, the column its bare `@domain` sits on (a table that finds its tenant through `@domain(via: ...)` holds no tenant's key, so the path goes on through that foreign key); on the `@tenant` record, its key; on an auth's groups table, the store's `DomainId`. A global table that is none of these tells generation nothing of a tenant, so there the end is held to text alone, and that the column holds a tenant's key is the author's word. `NewGeneratedCollection` refuses a collection built by hand that gives a set a domain on a domain-scoped resource, or gives one to a subject value, so nothing takes the place of a resource's own tenant filter. Note the anchor's own binding is what counts: tenancy never arrives transitively from a domain-scoped parent table (see `@domain`). **Whose people:** a set describes the people of one auth: its user column holds that auth's people and the set is looked up by their ids, so an engine opened for another auth takes no grant that reads it: one in its role file is refused when the file is loaded (`access.WithDefaultRoles`, `access.ValidateRoles`), one written to a custom role is refused, and one found in the store is left out and reported. An application with one auth need not say, and every set is that auth's. An application with several says which on every set it writes, `auth: crew`, the auth's name as its package names it (`@subjectSet(squadrons, value: SquadronID, auth: crew)`), since nothing in a schema says whose ids a user column holds: a table one auth's staff keep about another auth's people is served to the first and names the second. Generation refuses a set that says none where the application has several auths, naming them, and a name that is no auth of the application; a `@subjectValue` takes none, and the set of a person's groups that the generator declares for an auth names that auth itself, in an application with one auth too. A set follows a person through sets of its own auth alone: generation refuses a `who:` that tests a set labeled for another auth, the set of another auth's groups included, since through that test the set would hold rows for people it does not describe. The generated collection carries the name (`SubjectBindingData.Auth`) and the names of the auths its run knew (`CollectionData.Auths`), which the permission engine holds the name a role file is handed over under to, and the role-file check of an auth looks at that auth's sets and at no other auth's (`access.UnreadSetWarning`; `GeneratedCollection.SubjectSets(auth)`). **Type:** the set's comparison type is derived from the `value:` column (the terminal of a dotted value) exactly as `@attribute`'s is, and a grant may test only an attribute of the same type for membership in it; `access.ValidateRoles` refuses the mismatch at deploy. **The two parts:** a set's definition may say two more things, each in the language grant conditions are written in ([`accesstypes/condition`](../accesstypes/condition), whose package documentation states the grammar of both) over the fields of the same struct, named as Go fields. Both are optional, and a set that writes neither is the set described above and generates as it always has. `who:` says which rows are the requester's: one test or several joined by `OR`, each tying a column of the row to the requester, `Field = subject` or `Field IN subject.<another set>`. Left out, it is the annotated column equal to the requester. `where:` says which of those rows count: a condition over the row's own columns compared to literals and to `now`, with the comparisons, `IN (…)`, `IS NULL`, `AND`, `OR` and `NOT`, judged as every condition is, so a row counts only where it is TRUE. One table then yields a set for each level it records, an expired row drops out at its hour, and a set follows a person through any table that declares a set of its own: `@subjectSet(hangarsRun, value: HangarID, who: UserID = subject OR SquadronID IN subject.squadrons, where: LevelID = 'run' AND (ExpiresAt IS NULL OR ExpiresAt > now))`, written on one line or continued over the comment's next lines. A role file does not change: a grant names the set (`hangar IN subject.hangarsRun`) and nothing of the table behind it. Sets build on sets to any depth, and each keeps its own tenant filter: a set on a tenant-scoped table reads the request's tenant, a set on a global table reads every row unless it writes a `domain:`, whichever of the two names the other, so a table kept outside every tenant answers the same in each. Both parts render inside the one `EXISTS` the set already is, the set a `who:` follows as that set's own `EXISTS` with the row's column where an attribute would stand. With a `who:` written the annotation sits on one of the fields the `who:` names, as every annotation that ties a row to the requester or the tenant sits on the column it reads. **Refused at generation**, naming the struct, the field and the set: a `who:` test that does not tie the row to the requester (a comparison to a literal, `!=`, a negated test, tests joined by `AND`), since a set of everyone's rows is one mistake away without it; a `who:` naming a field the struct does not have or one no condition can compare, comparing a column that is not a string to the requester, or writing one test twice; a `who:` naming a set no `@subjectSet` declares (the message lists the sets declared), a `@subjectValue`, or a set whose value type differs from the tested column's; a `who:` naming a set that describes another auth's people where both sets say whose they are, the generated set of another auth's groups included (the message names both auths and where each is said); sets that build on each other in a loop, a set on itself included, with the loop spelled out; the annotation on a field its `who:` does not name, with the fields it may sit on; a `where:` naming something that is not a field of the struct, or a field no condition can compare; a `where:` literal that does not fit the column it is compared to (a string against a number, a timestamp that is not RFC 3339, a date that is not `YYYY-MM-DD`, `now` against anything but a timestamp); a `where:` value that an enumerated column does not hold (a foreign key into an `@enumerate` table, or a field-scope `@enumerate` naming one), with the values it does hold; and a `where:` that names the requester (`subject`, `subject.<name>`) or a proposed value (`new.`), since the test that ties a row to the requester is the `who:` and a set's rows are read as the database holds them. **Index warning:** generation warns, and never refuses, when no index of the set's table serves the set's lookup, one warning for each column the `who:` names (the annotated column for a set that writes none), each naming the `CREATE INDEX` it wants: the tenant column first where the row carries one, then the column, then the value (section 9). **Strings:** a string literal in a `where:` is written in single quotes and holds any character, a comma or a parenthesis among them; a quote inside it is written twice (`Code = ':)'`, `Note != 'it''s'`). A string that is never closed is refused as an unclosed single quote, at the annotation. Examples: [SquadronMembership](../lodestar/pkg/resources/squadron_memberships.go), a set that writes neither part; [HangarPass](../lodestar/pkg/resources/hangar_passes.go), two sets on one table, each with both. **Impersonation:** the record decides what a person may do, so its generated write refuses the real person behind an impersonated session a row that names them, by the annotated column or a column a `who:` compares to the requester, before the write and after it (section 21, "The rules on who is asking"). |
| `@subjectValue` | user-id field of a `@resource` struct | `name, value: Field` | Declares subject-side scalar vocabulary for threshold comparisons (`amount <= subject.approvalLimit`). Same grammar — and the same tenancy rule, and the same place in `zz_gen_bindings.dot` — as `@subjectSet`, valid only where the annotated user-id column is the whole key of a unique index, the single-column primary key included, so the database enforces exactly one row per user; a column of a composite key or composite unique index does not qualify. It takes neither `who:` nor `where:`, the parts of a `@subjectSet`: its one row is the requester's by the unique column, and generation refuses either on it. **Type:** the value's comparison type is derived from the `value:` column as `@attribute`'s is, and a grant may compare it only against an attribute of the same type (`now` only against a timestamp-typed value); `access.ValidateRoles` refuses the mismatch at deploy. **Impersonation:** as for a `@subjectSet`, the record's generated write refuses the real person behind an impersonated session the row that names them (section 21, "The rules on who is asking"). |
| `@manualAddResource` | `accesstypes.Resource` constant of the resources package | `permission[, scope]` | Registers the permission on the resource in the generated Collection for a hand-written route with no generated handler. Repeatable. Scope is `global` or `domain`; omitted means the global default. An `@outlet` annotation on the same constant names the outlets the hand-written route is mounted under, so an outlet-filtered TypeScript target (`ForOutlet`) carries the registration only when it names that outlet; omitted means the default outlet. The constant's value is the resource name and must not contain `:` (reserved for access-defined markers like `accesstypes.GlobalResource`); generation rejects it. The registration reaches the TypeScript constants like a generated one: an `Execute` registration joins `Methods`, any other permission joins `Resources`. A name is registered in one scope: generation refuses a name registered for the whole application and for a tenant, and `resource.NewGeneratedCollection` refuses the same in collection data built by hand. The name holds no dot, which separates a resource from one of its fields: a registration under a name with a dot is refused. The annotation is read from the comment of the constant it stands above: one in the comment above a `const ( ... )` group is refused, with where to write it. The constant's type is `accesstypes.Resource` or an alias of it (`type Name = accesstypes.Resource`): generation refuses the annotation on a constant of any other type, a type declared over `accesstypes.Resource` (`type Name accesstypes.Resource`) and an untyped string among them, with the type to give it. The annotation stands directly above its constant: generation refuses it in a comment that is no constant's (one a blank line parts from the constant below it). On a constant declared in another package, or in a comment there that is no declaration's, it is read by nothing, and `impulse check` reports it (`constant-annotations`). |
| `@class` | `@rpc` struct; `accesstypes.Resource` constant carrying `@manualAddResource(Execute)` | comma list of class names, `credential`, `access` | Marks the action with the classes it carries, which a permission mask refers to: the mask of an impersonated session that refuses a class (`accesstypes.RefuseClass`) refuses every action that carries it, the ones declared after the mask was written among them, so nobody keeps a list of actions in step with the application (section 6). Repeatable, and a list and repeated annotations mean the same. A class is named by a lowercase letter, then lowercase letters, digits and underscores (`money_movement`); the library names two, which its generated identity methods carry (section 21): `credential` (`accesstypes.ClassCredential`), on the methods that change how a person signs in (a new password required, a password removed, a reset link sent, a sign-in identity unlinked, a username or an address changed), and `access` (`accesstypes.ClassAccess`), on the methods that give or take away what someone may do (a role given or removed, a group and who is in it and what it holds, an invitation, and what a sign-in method or a tenant's directory gives everyone who arrives through it; an account disabled, enabled or deleted carries neither class, section 21). An application marks its own actions with either or with a class it names. The generated Collection carries each action's classes (`CollectionResource.Classes`, read back with `ActionClasses`), which is where the masked check reads them, and the collection file declares a constant for each class in use (`ClassCredential`, `ClassAccess`, `ClassMoneyMovement`), so a mask is written against a name the compiler knows and one that names a class no action carries any more does not compile. A registration declared with `WithManualResources` carries its classes in `ManualRegistration.Classes`. A class is an action's alone, a name that registers Execute and nothing else. **Refused at generation:** a name no class may carry, a class named twice on one action, the annotation on a `@resource`, `@virtual` or `@computed` struct (the annotation marks an action; a record's writes are given the class `access` by the generator alone, where the record decides what a person may do, and a mask holds a session off any other record by naming it, `accesstypes.RefuseWrites`), the annotation on a constant that registers nothing, the annotation beside a registration of a permission other than Execute, on a constant or in `ManualRegistration.Classes` (`constant "LedgerRows": @class(sensitive) marks an action, and the registration is of List, which is no action's permission: a class is carried by a registration of Execute alone. …`), a name that carries a class and registers another permission beside Execute (`"ResetLogin" carries the class credential and registers Update beside Execute: …`; the collection refuses the same when the application starts, so a collection file written before the rule is not served), the annotation on a method that registers no action (its handler suppressed with `@suppress`, or a generation that writes no routes: the class rides the registration, so it goes on the constant that registers the action by hand, and is never dropped without a word), and two classes whose constants would share a name. A name registered twice, by a method and by hand, carries the classes of both. Example: [ScrapShip](../lodestar/pkg/rpc/scrap_ship.go). |
| `@manualAddResourceSet` | `@resource` struct | comma list of `listHandler`, `readHandler`, `patchHandler`, or `allHandlers` | Declares that hand-written handlers register this resource's permission Sets for the given handler types; validated against the set of generated handlers. |
| `@outlet` | `@resource`, `@virtual`, `@computed`, or `@rpc` struct; `accesstypes.Resource` constant carrying `@manualAddResource` | comma list of outlet names | Names the router outlets the struct's routes — or the constant's hand-written route — are registered under. Outlets are independent registration surfaces with their own route prefixes — the generator emits a `Generated<Name>Handlers` interface and `generated<Name>Routes` function per outlet, so the application composes different authentication and middleware around each (a browser app on one, a machine REST API on another). The default outlet is declared by `generation.GenerateRoutes` and carries the reserved name `default`; additional outlets are declared with `generation.WithRouterOutlet(name, routePrefix)`, and referencing an undeclared name is a generation error. An outlet serving browser sessions declares it (`WithRouterOutlet(name, prefix, Auth(pkg, flavor))` under the generated router, `ServesSessions()` under a hand-written one; section 8): the generated route tables register the permission-digest and user-domains routes under its prefix, and only a session-serving outlet may be the target of a `GenerateTypescript` call (`ForOutlet(name)`), which filters every emitted TypeScript file to that outlet's members — a client for a session-less outlet would have no permission channels and fail closed on every page, so it is a generation error. Without the annotation a struct is on the default outlet only; naming outlets replaces that default, so `@outlet(default, automation)` serves both while `@outlet(automation)` serves the automation outlet only. Consolidated resources get one consolidated patch dispatcher per outlet (`PatchResources`, `Patch<Name>Resources`), each bundling exactly that outlet's members. The generated router tests cover every outlet's routes and additionally prove isolation: a route's path under an outlet it is not attached to must 404. Example: [Consignment](../lodestar/pkg/resources/consignments.go). |
| `@permissionScope` | `@resource`, `@virtual`, `@computed`, or `@rpc` struct | `global` or `domain` | Sets the permission scope used by all of the resource's registrations. Default: `global`. It also selects the domain the generated handlers evaluate permissions in: global-scoped handlers pass `accesstypes.GlobalDomain`, while domain-scoped handlers read it from a required `/<tenants>/{<tenant>ID}/` route segment pair between the route prefix and the resource path (pair-style, so domain values can never collide with resource or method route names). Both names derive from the package's `@tenant` record: the segment is the record's route name and the parameter its key's route parameter (`Sector` keyed by `ID` gives `/sectors/{sectorID}/`), and a tenant-scoped resource in a package with no `@tenant` record is refused. Domain-scoped routes are wrapped in the generated `DomainGuard`, which asks the application's tenant roster (`TenantRoster().Has`, section 16) and answers 404 for a domain the roster does not hold before the handler runs, and the same 404 for a tenant the session does not reach (`UserPermissions.Reaches`: the tenant's declaration does not admit the way the session was established, section 6). With `generation.WithConcealedDomains()` the guard asks the caller's foothold next (`UserPermissions.HasGrants` in the domain): a domain where the requesting user holds zero grants answers identically to a domain that does not exist (404 on routes, 400 in consolidated op paths), so refusals never confirm a tenant; any grant in the domain restores ordinary 403s (`access.Client.UserHasGrants` answers the foothold question from the in-memory snapshot, so the check stays legal inside the consolidated mutation transaction). Tenant identifiers must be a single URL-safe path segment and must never contain `:` (the character is reserved for access-defined markers; `accesstypes.GlobalDomain` is `access:global`); a URL domain is pure data, and `accesstypes.DomainScope` routes it to a tenant partition by construction, so no value can address the global partition. In the consolidated patch handler, a domain-scoped resource's operations carry the domain in the path exactly as the URL grammar does (`{"op":"patch","path":"/sectors/sector-alpha/berths/…"}`); global operations stay domainless, an unknown domain in an operation path is a 400, and cross-domain batches are legal (each operation is checked in its own partition; the batch is one transaction). Example: [Sector](../lodestar/pkg/resources/sectors.go). |
| `@tenant` | `@resource` struct | none, or `label: "<word>"`: `@tenant(label: "Service district")` | The struct is the tenant record: the table whose rows are the tenants, one per package. Its route name is the segment tenant-scoped routes are served under and its key's route parameter is the domain parameter (`Sector` keyed by `ID` gives `/sectors/{sectorID}/…`); the generator emits `New<Record>Roster(client, opts...)` into the handler package, which builds the `resource.TenantRoster` the generated `DomainGuard` and the consolidated dispatcher ask (section 16); what makes a tenant (the generated `New<Record>Maker`, at the first sign-in of a person staff invited, and the create-your-tenant route) and the record's generated delete path reread the roster after their commit and signal the tenants kind, so a tenant made at run time is served by every instance without a restart or a release. Nobody creates a tenant directly: the record's create op is refused with how one is made (section 16). The record must be global, table-backed, and keyed by one generated id (`ID impulse.UUID`, whose text is the domain in every tenant-scoped URL; what people call the tenant is its `@tenantName` column, changed like any field): `@tenant` on a `@permissionScope(domain)` struct, on a compound key, on a key that is not a generated id (a typed string, a foreign key, an interleaved table), on a `@virtual`, `@computed` or `@rpc` struct, or on a second struct is refused naming the rule, and a tenant-scoped resource, view, computed resource or method in a package with no `@tenant` record is refused (`a tenant-scoped resource needs a tenant record; annotate it @tenant`). A key cannot change on update. The record names what the screen calls a tenant: the generated browser descriptor carries the word, one and many (`domainRoute.word`), for the frame's place switcher and menus, from the record's name in words (`Sector` reads "Sector" and "Sectors", `ShipClass` "Ship class" and "Ship classes"), or from `label:` where an application in production changes the word without renaming its tables; the plural is the generator's, as it spells the table. A label is letters, digits, single spaces and hyphens, starting with a letter; any other is refused. Example: [Sector](../lodestar/pkg/resources/sectors.go). |
| `@directoryOnly` | field of the `@tenant` record, a `bool` column | none | The tenant's people enter through its directory only: a tenant whose flag is true admits a sign-in through its own directory and nothing else, so a password, a personal account and a sign-in through another organization's directory do not reach it. Two people are not held to it: a super administrator, and an administrator of the tenant itself, who enters with a password and a second factor so that a tenant whose directory is broken can be mended from inside (section 16, "Directory only"). The flag restricts that tenant's own door alone: a tenant whose flag is false admits everyone it seats, however they signed in, and a person the flag keeps out of one tenant still enters every other tenant that seats them. The flag is half of what the tenant record declares about who may enter a tenant (section 16); the other half is the directories record (`@tenantDirectory`), which the flag needs, since a tenant whose people enter through its directory only needs a directory to enter through, and the directory of such a tenant is not removed until the flag is turned off. The switch is the tenant's own: the generator writes two methods in the tenant's scope, `Make<Record>DirectoryOnly` and `End<Record>DirectoryOnly`, which the tenant's administrator runs under an Execute grant on each; turning the flag on is held to three rules (the tenant has its directory, the caller is signed in through it, and the caller has a password where their auth has passwords; section 16, "Directory only"). The record's own write paths, which are global, show the flag and never write it: the field is output only on the wire, at create and at update, and the record's change events are the audit. The generated create-your-tenant route leaves it out of its body: a new tenant declares nothing beyond its creator's directory. Refused on a resource that is not the tenant record, on a column that is not a `bool`, and on a second column. Example: [Sector](../lodestar/pkg/resources/sectors.go). |
| `@tenantName` | field of the `@tenant` record, a `string` or `*string` column | none | The tenant's name as its people know it. The generated `New<Record>Roster` reads the column beside the key (`resource.WithTenantNames`), and the generated `UserDomains` handler names each tenant of the session's tenant list from the roster (`resource.WithDomainNames`), so a tenant's own people see its name in the place switcher without holding Read on the record, a global resource; without it the list names each tenant by its key. The generated browser metadata names the column on the record (`nameField`), so a picker over the tenant record lists tenants by name and the people pages name the tenant a role or an invitation is in. Refused on a resource that is not the tenant record, on a column that is not a string, and on a second column. Example: [Sector](../lodestar/pkg/resources/sectors.go). |
| `@name` | field of a `@resource` or `@virtual` struct, a `string` or `*string` | none | The field the resource's rows are known by. Beside every id field that points to one of its rows, by a foreign key or a field-scope `@enumerate`, the generator adds a name field for the name of that row, spelled as the id field with a trailing `Id` dropped, then `Name` (`SquadronID` gives `SquadronName`, `squadronName` on the wire): read-only and stored nowhere, with no column and no schema change. It is declared on the list's and the row's request types marked `nameof:"<the id field>"`, in the resource's generated `NameFields()` with the table and columns the name is looked up in, on the TypeScript row type and its field-name constants (among the PII names where the pointed-to name is `pii`), and in the metadata's `nameFields`. A name field is no column of the resource: the statement looks it up by the id, in one scalar subquery held to the request's tenant where the pointed-to row belongs to one (by its tenant column, or by the way it reaches its tenant through other tables), so a row of another tenant gives no name, and neither does a row whose id is masked. Reading a name takes two grants: the request's own grant on the id field, and the List grant on the pointed-to resource's name field, asked in the request's tenant where the pointed-to resource belongs to a tenant and globally where it does not. An enumeration table's names take the first alone. Where the second is held under a condition, a row gives the name only where the condition holds for the row it points to. A request may name a name field in `columns`, and a request that names no columns is given the name beside every id it returns, less the names the person may not read; a row carries the name under its wire name and leaves it out where there is none. A person without the second grant who names the field in `columns` or `sort` is refused in the words of any field they may not read ("You may not sort or filter hangar passes by the squadron name here."), and a declared `@order` by a name they may not read orders their list as if no row had that name. So does a row whose id they may not read: under a declared `@order` by the name it sorts as a row with no name. `sort` and `@order` may name one, which orders the list by the names, the key breaking ties, and pages by them; no index serves such an order. `@order` naming a name the generator adds no field for is refused, saying to declare the pointed-to resource's `@name`. A list's `filter` may name a name field where the id beside it is filterable and the name is filterable where it lives (the pointed-to name field is indexed, `allow_filter` or carries a search fact; an enumeration table's `Description` always): `squadronName:contains:red` is written `SquadronId IN (SELECT Id FROM Squadrons WHERE …)`, held to the request's tenant and to the condition the name's grant is held under, so the pointed-to table's own index or search index answers it. The name counts as the filter's indexed field only where the id and the pointed-to name are both indexed, and it answers the text matches only where the id is indexed and the pointed-to name carries a search fact. A filter by a name tests the name the row shows: a row that shows no name (no id, a masked id, a row of another tenant, a row the name's grant does not cover) matches `isnull` and nothing else. It takes the same two grants as showing the name, and a person without the second is refused as for `sort`. A name is filtered by in the request's body where the pointed-to name is `pii`, and never joins the search box. The row's own request type carries no filter tag for it; a name field with none is refused as not filterable. The entry in the metadata's `nameFields` states the same with a field's `filterable` and `search`. A column or field that begins with `zzName`, the prefix a statement selects the names under, is refused, saying to rename it. A resource that marks no name is not refused: a field pointing to it gets no name field and stays an ordinary field showing its stored value. The tenant record is known by its `@tenantName` and an enumeration table by its `Description` column; neither needs `@name`. No name field is added for the name of a tenant's row on a list asked outside one tenant (a global resource's, and both lists of a resource declared `@listAcrossTenants`), where the pointed-to resource is a view (`@virtual`: its rows are a query of the application's, which a lookup does not read yet) or a `@computed` resource, or beside a field called `Id` alone. A resource written by hand whose `NameFields()` describes the name of a tenant's row is refused when its list across tenants is decoded, saying to take the name out. Refused on a field that is not a string, on a second field, on a field the resource never returns (a `conditions:"secret"` or `conditions:"input_only"` field, or a `@file` store key: a row's name is shown to everyone who may read the resource's names), on the tenant record (write `@tenantName`), on an enumeration table's struct and on a `@computed` resource; and a name field that would take the name of a field the struct declares, or of another name field, is refused, naming the field to remove or rename. Example: [the fixture](../generation/testdata/namefixture/fixture.go). |
| `@roleOf` | field of a `@resource`, `@virtual` or `@computed` struct, a `string`, a `*string` or a `[]string` | the auth whose roles the field holds, by its name: `@roleOf(Staff)` | The field holds the name of a role of that auth. The generated metadata lists the roles the field may hold (`roles`): the roles the auth's role file defines, a tenant's roles alone on a tenant-scoped resource, and on every other resource the Super Administrator role too. A list writes the field in words, "Night shift lead" for `NightShiftLead`, as the server's sentences name a role; the column's filter offers those roles to pick, in words, and sends the stored name. A picker over a list whose field carries it writes that field in words. A role made at run time (`access.UserManager.AddRole`) is written in words too, and the filter offers it where a row of the list holds it: the table asks the list which roles it holds beyond these. On a list field each item is a role's name; where a line of text follows the name after a space (where the role is held), the name is written in words and the rest as it is. The role file is thereby one of the generator's inputs: after a role is added to the file, renamed or removed, `go generate ./...` writes the lists again, and `impulse check`'s `regen` refuses generated files that are behind the file. |
| `@tenantDirectory` | `@resource` struct, `@permissionScope(domain)` | none | The struct is the directories record: the tenant-scoped table that holds each tenant's directory, the organization its people sign in through, as the sign-in method (`@directoryMethod`) and the connection (`@directoryConnection`). A tenant has one directory and a directory belongs to one tenant: the generator refuses a record whose table has no unique index on the tenant column alone, or none on the method and the connection together, naming the index to add, and the generated write paths refuse first, each with a sentence the person can act on (`resource.TenantDirectoryRules`, section 16). Nobody types a directory in: the method and the connection are output-only, the record's generated create sets both from the directory the caller is signed in through and refuses a session that came by password or with a personal account (any Google account, a Microsoft personal account), and its generated delete is refused while the tenant is directory only. The record's key is an id its create generates. Its tenant is the bare `@domain` column, the tenant's key on the row itself, so a tenant's administrator sets and removes its own directory under the record's Create and Delete grants, and the record's change events are the audit. With it the generated `New<Record>Roster` reads what each tenant declares beside its key (`resource.WithTenantDeclarations`), `New<Record>DeclarationReader` reads one tenant's declaration in the sign-in's transaction for `signin.TenantPolicy` (a package that declares no directories record and makes the tenant record's rows itself is given the reader too, one that answers nothing for every tenant: "No directories", section 16), the record's write paths reload the roster after their commit and signal the tenants kind as the tenant record's do, and the generator emits `Set<Record>Directory`, the one step that sets a tenant's directory from a sign-in, which the create-your-tenant route calls with the directory its creator came through. Refused on a global resource, on the tenant record itself, on a second struct, with a join-path `@domain`, without either column, with a key that is not a generated id, and without either unique index. Example: [SectorDirectory](../lodestar/pkg/resources/sector_directories.go). |
| `@directoryMethod` | field of the `@tenantDirectory` record, a `string` column | none | The column holds the sign-in method a directory is reached through, as `sessioninfo.AuthMethod` spells it: `google`, `azure`, `workos` or `oidc` (an organization's own OpenID Connect provider, named by its registration's connection). The column is output-only: the generated create sets it from the caller's session, and no request writes it. Refused off the directories record, on a column that is not a string, and on a second column. Example: [SectorDirectory](../lodestar/pkg/resources/sector_directories.go). |
| `@directoryConnection` | field of the `@tenantDirectory` record, a `string` column | none | The column holds the connection a directory's identities carry, the value the session's first auth event records beside the method: a Google Workspace by its domain (the registration admitting any Workspace writes the account's domain), a Microsoft Entra tenant by its tenant id, a WorkOS connection by its id. The column is output-only, as the method is. Refused off the directories record, on a column that is not a string, and on a second column. Example: [SectorDirectory](../lodestar/pkg/resources/sector_directories.go). |
| `@directoryDefaultSeat` | field of the `@tenantDirectory` record, a `string` or `*string` column | none | The column names the role a person is given in the tenant at their first sign-in through the tenant's directory, whether their account is created then or stood before, chosen by the tenant's administrator from the tenant's roles on its Sign-in page, with the generated method `Open<Record>ToDirectory` (section 16); NULL (or empty), which `Make<Record>InviteOnly` writes, says the tenant admits the directory's people by invitation only: a stranger is refused with the `invitation_only` refusal and no account is created, and an invitation link is the way in. A directories record without the column admits every stranger by invitation only. The role is given once for the link between the account and the directory: a person whose role an administrator removes is not given it back by signing in, and a link that is deleted and made again gives it again where the person holds no role in the tenant. With it the generated `New<Record>DirectoryReader` answers the tenant whose directory an identity came through with its seat, which the auth's directory admission reads (`session.Auth.SetDirectoryAdmission`) before the method's account rule, and the role is given by the Auth's generated identity client in the transaction that links the account to the directory (section 16). The column is output only on the wire: the two methods write it, and the record's create and update do not. Refused off the directories record, on a column that is neither a string nor a nullable string, and on a second column. Example: [SectorDirectory](../lodestar/pkg/resources/sector_directories.go). |
| `@page` | `@resource`, `@virtual`, or `@computed` struct | `default: N` and/or `max: M` | Declares the list's page sizes: the page a request without `limit` receives, and the largest page a request may ask for. Both are positive integers; the default never exceeds the maximum, and a maximum alone must be at least the generator-wide default of 50. Undeclared, the list serves pages of 50 with no maximum. A request over the maximum is refused with a 400 naming it, never clamped, and a resource with a maximum refuses `limit=all`. The maximum is also the switch a picker reads: a resource with none is small enough to load whole, read in one request with `limit=all` and sort optional; a resource with a maximum is paged, one server page at a time, so its list needs an order (`@order`, or `sort` on the request) and a picker over it reads the chosen row by key, which is why a bounded picker source may not suppress its read (`@enumerate`). Declaring the maximum is the deliberate choice; nothing decides at runtime from an observed size. Refused on a `@computed` or `@virtual` struct with no `@primarykey`, naming the struct and the two ways out (declare the key, or drop `@page`): without a key the resource is a whole read-only list, with no read route and no paging, so a page size on it is a contradiction. The generated TypeScript descriptor carries both numbers. Example: [Mission](../lodestar/pkg/resources/missions.go). |
| `@order` | `@resource`, `@virtual`, or `@computed` struct | comma list of `Field [asc\|desc]` | Declares the order a list takes when the request carries no `sort`, naming Go fields of the struct; the direction defaults to `asc`. The primary key is appended at runtime so the order is total, and a request's `sort` replaces the declared order for that request. A declared order never shows a person what they may not read. Where they may not read a field it names, their list comes in the order of the order's other terms and then the key, as if no row had a value in that field. Where they hold the field under a condition, a row whose cell is masked sorts as a row with no value, as it does under a `sort` by that field. A field declared `masking:"positional"` is the exception, as it is under a `sort`: its masked cells keep their real place. Nothing is refused, since the person asked for no sort. A computed resource follows the same rule: its handler sorts and pages the rows by what the person may see of each, and it sorts again a list whose body took the sort where the person holds a field of the order under a condition. The declaration is optional, and every paged request needs an order from somewhere: a resource that declares none serves a request without `sort` only as the whole list (`limit=all`), which is not sorted — its table or view statement carries no `ORDER BY` and the rows arrive in the database's own order, a computed list keeps the order its body yielded — and refuses a bare GET or a `limit` with a 400 naming the resource and the way out (section 4). A nullable column renders as the plain direction and sorts in its database's own `NULL` placement, so an index on the column serves the order: Spanner places `NULL` first ascending and last descending, PostgreSQL last ascending and first descending. A computed resource follows the same placement: its handler sorts and pages the body's rows where the application's database would. On a computed resource only a leaf field may be named (a nested field is opaque). The generated TypeScript descriptor carries the declared order (`order: [{ field, direction }]`, JSON names), so a browser client knows a request without a `sort` is already ordered and pages by cursor; to a resource that declares none it sends a `sort` of its own or asks `limit=all`. On a table-backed resource with a bare `@domain`, generation warns when no index leads with the tenant column and then these columns in this order and direction, naming the index it wants (section 9). Example: [Mission](../lodestar/pkg/resources/missions.go). |
| `@manualAddPermission` | `accesstypes.Permission` constant of the resources package | none | Declares the constant's value as a permission of the application that no resource registers: one a hand-written route checks for the whole of a scope (`access.Client.CheckUser`, `CheckRole`) and a custom role is given with `AddRolePermission`. The generated Collection lists it with no resource (`CollectionData.Permissions`), which is what makes it a permission the release has: a grant of a permission for the whole of a scope that the Collection does not list is refused when it is written and, when the policy is loaded, left out and reported as a `SkippedGrant`. The annotation stands alone and names no scope: the role a grant is written to decides where it holds. Generation refuses an `@outlet`, `@class` or `@manualAddResource` beside it, any of those three on an `accesstypes.Permission` constant that does not carry it (they are written on a resource's constant; on a permission's constant they would be read by nothing), the annotation on a constant of any other type (an untyped string or an `accesstypes.Resource` among them), a constant whose value is empty, and a permission two constants declare. A permission some resource registers needs no declaration. A declared permission adds nothing to the TypeScript constants, whose `Permissions` are the framework's own. The annotation is read from the comment of the constant it stands above: one in the comment above a `const ( ... )` group is refused, with where to write it. A constant whose type is an alias of `accesstypes.Permission` is read as a permission's. As with `@manualAddResource`, generation refuses it in a comment that is no constant's, and `impulse check` reports it on a constant of another package or in a comment there that is no declaration's. |
| `@feature` | `@resource`, `@virtual`, `@computed`, or `@rpc` struct; field of a `@resource`, `@virtual`, or `@computed` struct | the flag's constant, `Debriefs` | Puts the resource, the field, or the method behind a feature flag, so it exists for the browser and the API only while the flag is on (section 15). The argument is the identifier of a `resource.Feature` constant declared in the resources package (`const Debriefs resource.Feature = "debriefs"`, its doc comment the flag's description); the constant's value is the flag's name, `[a-z][a-z0-9_]{0,63}`, and a name declared twice, a malformed name, an unknown identifier, or a value in place of the identifier is refused at generation naming the constants. On a struct every route of the resource or the method answers 404 while the flag is off, exactly as an unregistered route does, its consolidated arm answers as an unknown resource, and the resource, its fields and the method are absent from the permission digest. On a field the decoders answer the field as unknown while the flag is off (a 400 on `columns`, `sort`, `filter` and a patch body naming it) and the handlers leave it out of every response; a primary key, the tenant key, the state column, a `@file` key, and a column a create must supply (NOT NULL with no default, on a resource that serves a create) cannot be gated, and an `@rpc` struct's fields are not gated one by one. The TypeScript metadata and the descriptor carry `feature: '<name>'` on the gated entry, and the client file declares the `Feature` union and constants. |
| `@searchAlso` | `@resource` struct | `Child.Field`, and `via: Column` where the child has several foreign keys to the parent | Adds a child table's text to the resource's search box (section 4): a term the box searches is found in the parent's own searchable fields or in the named child field, and the parent row is admitted when any of its child rows matches. The child is a table-backed `@resource` of the same package one hop down a foreign key to the parent's single-column key, under the same tenancy (both tenant-scoped on the row, or both global), and the child field carries a search fact (a token column in the child's search index); each is refused otherwise, naming the rule. The child field's own grant governs the branch: a caller denied the field searches the parent without it, a conditional grant conjoins its condition inside the branch. The generated resource declares `SearchBranches()`, the metadata lists the branch in `searchFields` under its dotted name (`clientContact.displayName`), and a box over the parent reads one key set per term (section 4). Generation warns where the child's search index does not store the foreign key, since every matching child row is then read back for its key. Example: [Client](../lodestar/pkg/resources/clients.go). |
| `@listAcrossTenants` | `@resource` or `@virtual` struct with `@permissionScope(domain)` | none | Serves the resource's list a second time without the tenant's segment: `GET` (and `POST`) `/<prefix>/<resources>` reads the resource's rows in every tenant the session reaches in which the caller holds the List grant, with one filter, one sort, one page and one count across them (section 4, a list across tenants). Each row is checked under the caller's grants in its own tenant and carries the reserved `zzTenant` property, the tenant it is in. Only a declared resource gets the route, and the tenant's own list is unchanged by the declaration. It adds the handler `<Plural>AcrossTenants` beside the list handler, the route on the outlets and behind the `@feature` flag the resource declares, outside the `DomainGuard`, and `acrossTenants: true` on the resource's TypeScript descriptor, which gives the browser client `client.acrossTenants.<resources>`. It adds no permission: the list is the resource's List grant, asked in each tenant, and the global permission digest tells the list's page whether that is anything at all for the person (`ListAcrossTenants`, section 5). The generated collection marks the resource, and only a marked resource is decoded as a list across tenants or holds a live subscription over a set of tenants. Reads only: every write stays inside one tenant. Refused on a global resource (there is no tenant to read across), on a resource whose list is suppressed, on a `@computed` resource (its rows come from the application's own function, which answers for the one tenant it is handed) and on an `@rpc` struct, and where another route of the outlet already answers at the list's path (an RPC method named as the resource's plural). A column or field named `zzTenant` is refused on every resource, as the other reserved row names are. Generation warns where a declared table-backed resource with an `@order` has no index that leads with the order columns and carries the tenant column (section 9). Example: [Mission](../lodestar/pkg/resources/missions.go). |
| `@formerly` | field of a `@resource` or `@virtual` struct; field of an `@rpc` struct or of the struct its `Execute` answers with; `@rpc` struct | the former name as it was written, `Title` | Keeps a renamed field or method answering under its former name while applications built before the rename are still answered (section 18). The column never changes; only the wire name moves, and the former wire name derives from the argument as the current one does from the field (`@formerly(Title)` on `Headline` answers `title` beside `headline`). On a field, a request body naming the former name is read as the current one and a body naming both is refused with a 400 saying to send one; `columns`, `sort` and `filter` take either name, with the permission checks running against the current field; and every row carries both keys, each masked when the field is. On an `@rpc` struct the router registers the former route (`/release` beside `/publish`) on the same handler, and the generated router test drives both. The generated collection carries the former names (`TagData.Formerly` on a field, `CollectionResource.Formerly` on a method), and the permission digest mirrors a renamed field's or method's entry under its former name, so an application built before the rename keeps its column or its action. The TypeScript knows only the current name. A former name equal to the current one, or to another field's or method's name or former name, is refused at generation, as is the annotation on a `@computed` struct and the struct form on a `@resource` or `@virtual` struct. |

Exactly one of `@resource`, `@virtual`, `@computed`, or `@rpc` may appear on a struct. The generator refuses a struct carrying more than one, naming the kinds it found.

## 2. Struct tags you write (source structs)

Field-level permissions are structural, not annotated: every non-primary-key field of a
resource implicitly requires the endpoint's permission on `Resource.field` (`List` and
`Read` for reads, `Create`/`Update` for mutations; `Delete` stays resource-level).
Primary keys are exempt — their readability follows the resource-level grant. There is
no per-field permission tag to write: a `perm:` tag on a source struct is a generation
error, and a stale one in a generated request struct fails Set construction at startup.

| Tag | Where | Effect |
| --- | --- | --- |
| `spanner:"ColumnName"` | every field of `@resource`/`@virtual` structs | Maps the field to its Spanner column. Required — a missing tag or unknown column is a generation error, and field nullability must match the column's: a pointer or a Null wrapper on a nullable column, a plain value on a NOT NULL one. A slice-typed field follows the column's nullability, since a Go slice has one form: the Spanner client reads NULL into a nil slice and writes nil as NULL, so `[]byte` types a nullable `BYTES` column and a NOT NULL one alike, as `[]T` does an `ARRAY<T>` (section 12, nullable slices). A pointer to a slice is refused. |
| `conditions:"…"` | resource fields | Comma-separated list of field conditions, see below. Values match exactly (no spaces); a value the generator does not recognize is refused, with the nearest recognized one suggested. |
| `default_create_fn:"pkg.Func"` | resource fields | The generated create path calls the referenced function to populate the field when the request doesn't supply it. A field with a default function is not treated as required. |
| `output_only_update_fn:"pkg.Func"` | resource fields | The generated update path sets the field by calling the referenced function on **every** update; implies output-only. This is the *mechanical enforcement stamp* — a field whose meaning is "this row was updated", like `UpdatedAt`. A timestamp with domain meaning (a "last serviced" written by one business transition) is not an update function: it is an explicit update in the code that owns the business event — see [Ship.LastRefitAt](../lodestar/pkg/resources/ships.go). Declaring an update function on any field also gives the resource a generated `New<Resource>Touch(keys…)`: an update carried entirely by the update functions, running the full update pipeline (permission check, stamps, write conditions, change events) with no caller-set fields — the only way to express "bump the row" (an update patch with no fields set is a silent no-op). Example: [Ship.UpdatedAt](../lodestar/pkg/resources/ships.go) using `resource.CommitTimestampPtr`. |
| `allow_filter:"true"` | resource fields | Permits `filter` expressions on a field that isn't indexed (indexed fields are filterable automatically). Copied through to the generated request structs. On a table or view the filter must also touch an indexed field, which the database parse enforces: once one index has narrowed the rows a second is rarely used, and indexes are a scarce commodity on Spanner, so `allow_filter` conserves them. On a resource with a bare `@domain` column the tenant predicate is not that indexed field: it is the baseline every unfiltered page pays, and the rule asks the filter to narrow below it. The TypeScript field metadata says so: `filterable: 'withIndexed'` on a table or view field, `'always'` on a computed resource's, whose List function filters in memory. Refused on a list field (a slice, an array, or a named type over one; a byte slice is one value) on every kind of struct, since Spanner has no array equality and a computed resource's evaluator compares single values: `Ship.CargoBays: allow_filter on a list field; a filter compares single values`. |
| `index:"true"` | `@virtual` struct fields only | Declares the field indexed (filterable/sortable). Rejected on table-backed resources, which get index information from the schema. Refused on a list field: no index serves an `ARRAY` column. |
| `uniqueindex:"true"` | `@virtual` struct fields only | As `index`, and marks the index unique. Refused on a list field, as `index` is. |
| `search:"Table.Column"` | `@virtual` struct fields only | Names the table column whose search fact the field carries (section 4): the view's SQL projects that column's token column under its own name, and the field answers the matches and joins the box as the table's field does. The generator cannot read a view's projection, so the author states it, as `index` is stated; the fact itself is read off the schema, and a value that is not `Table.Column`, a table or column the schema does not have, or a column with no search fact is refused naming it. On a table-backed struct the tag is refused: the schema states the facts. Example: [ClientRoster.Name](../lodestar/pkg/virtualresources/client_rosters.go). |
| `masking:"positional"` | `@resource` and `@virtual` struct fields | The field's masked cells stay hidden on the wire, but a list orders and filters on the real column, so the page comes off the index and a reader can tell where the hidden values fall. Every untagged field conceals: a sort or filter on it runs over `CASE WHEN <condition> THEN column END` (section 4), which hides where the masked values fall and which no index serves, so a page sorted or filtered by it sorts the tenant's whole partition. Declare `positional` on a field whose rank is not sensitive (a deadline; a fee's rank is) and that a list pays for: named in `@order`, indexed, or `allow_filter`. `masking:"concealing"` is accepted and says the default; another value is refused with the nearest one suggested. Refused as a contradiction on a primary key (keys are exempt from masking), on a field no query sorts or filters by with an index behind it (neither indexed, nor `allow_filter`, nor named in `@order`: there is no index to restore and the rank would be disclosed for nothing), and on `@computed` and `@rpc` structs (a computed list sorts and filters only by fields granted unconditionally, so no masked cell meets a sort or a filter there, and a method is never listed). Copied through to the generated list and read request structs and surfaced in the TypeScript field metadata as `masking: 'positional'`. Role validation (`access.ValidateRoles`, and `access.Client.CheckPolicy` at a deploy) warns where a role's conditional grant lands on a concealing field the resource orders by or admits as a sort or filter key and the role's other grants leave the `CASE` standing; the warning names the field, the cost, and the three ways out (grant the field unconditionally, tag it positional, accept the cost for a table that never pages at volume). Example: [Mission.Deadline](../lodestar/pkg/resources/missions.go). |

Values recognized in a `conditions` tag:

- `immutable` — the client sets the field on create, and it can never change afterward:
  an update touching it is rejected with a 400 (the generator emits `immutable:"true"`
  into the patch request struct — you write the condition, never the emitted tag), and
  the generated Collection never exposes `Update` on the field's tag as grantable. The
  400 names the field (`json field hull is immutable`); an update that names several is
  refused for all of them in one sentence, in the order of their names
  (`json fields berth and hull are immutable`), before any other refusal of a field, so
  the same body always gets the same answer. A name the request accepts in another case
  (`Hull`) is refused the same way, by the declared name.
- `pii` — marks the field as personally identifiable. Emitted as `pii:"true"`, surfaced
  in the TypeScript metadata, and the field is rejected in URL `filter` expressions
  (filter via the POST body instead, which doesn't land in access logs).
- `input_only` — write-only: the client sets the field on create and update, and the
  server never returns it. Read and list structs get `json:"-"`, and a `columns=<field>`
  naming it answers 400. The generated TypeScript row interface omits the property, since
  no list or read response carries it; the metadata entry stays, flagged
  `writeOnly: true`, so a config-driven form still renders the input; the Create and
  Patch shapes and the field-name constants carry it, since a client writes it and a
  grant check names it; and a row read with `capabilities=Update` names it in the
  envelope when the caller's Update grant covers it, since the envelope speaks for every
  field the caller may write, projected or not (section 4). Example:
  [DistressCall.Transcript](../lodestar/pkg/resources/distress_calls.go).
- `secret` — the field holds a secret the application must use itself: an API key, a
  signing key, a credential of another system. It says everything `input_only` says, since
  a secret is never served, and three things more. The value is kept encrypted: the field
  is typed `resource.Secret` (`*resource.Secret` where the column allows `NULL`) on a
  `BYTES` column, the write path seals the value where it turns a patch into columns, and
  code that needs the value opens it with the row's generated `Open<Field>` method
  (section 23). The metadata entry is flagged `secret: true` beside `writeOnly: true`, so
  a form types the value into a masked input whose toggle shows what is being typed;
  there is no stored value for it to show, since the server returns none. And a change
  record names the field as changed and keeps neither its old value nor its new one
  (`"Secret": true` in the change set), where it keeps both for every other field,
  write-only ones included. `input_only` alone does not mean secret: a write-only field
  can be something a person files and the server does not serve back, which is read as it
  is typed and belongs in the record. A value a caller presents to the application to be
  let in (an application token, a password) is not a `secret` field either: the session
  library stores those as hashes, since the application never needs the original back.
  The generator refuses `secret` where the field cannot honor it: on a primary key, on a
  field the server owns (`output_only`, an `@state` column, the tenant key), on a view, on
  a foreign key or a field with a picker (`@enumerate`), on a field typed anything but
  `resource.Secret` (a plain `string` or `[]byte` would be stored as it was typed; the
  refusal names the type to give it), and on a column that is not `BYTES` with room for
  the 42 bytes sealing adds. A method's request field takes the same word, unchanged: text
  or bytes, no picker, and the method's dialog draws the same masked input. That value is
  never stored, so it is never sealed, and a request field typed `resource.Secret` is refused.
  Example:
  [Ship.TransponderKey](../lodestar/pkg/resources/ships.go).
- `output_only` — the server owns the value: returned to clients but never accepted from
  them (patch structs get `json:"-"`, excluding it from both create and update input).
  The value comes from the database or from `default_create_fn` /
  `output_only_update_fn` — and a field with an `output_only_update_fn` is output-only
  even without the condition. Example:
  [DistressCall.CaseNumber](../lodestar/pkg/resources/distress_calls.go).

`immutable`, `input_only`, `secret`, and `output_only` each answer the same question —
what may a REST client do with the field, and when — so they are easy to confuse. In
particular, `immutable` is not `output_only`: an immutable field is client-supplied
exactly once (e.g. an identifier chosen at creation), while an output-only field is never
client-supplied at all (e.g. a commit timestamp).

| `conditions:` | Client reads it | Client sets it on create | Client sets it on update |
| --- | --- | --- | --- |
| *(none)* | ✔ | ✔ | ✔ |
| `input_only` | ✘ | ✔ | ✔ |
| `secret` | ✘ | ✔ | ✔ |
| `output_only` | ✔ | ✘ | ✘ |
| `immutable` | ✔ | ✔ | ✘ (rejected with a 400) |

These conditions describe the REST contract only: what an untrusted client can read and
write over the wire. Application code calling the generated CRUD layer is not constrained
by them — it can write any field; that path is guarded by code review, not by these
rules. Two parts of `secret` are not the wire's alone: the value is sealed and a change
record leaves it out whoever wrote it, application code included, since the patch layer
does both. The `default_create_fn` / `output_only_update_fn` functions are not REST-specific,
however: they run inside the generated patch pipeline and fire for application code
exactly as for REST requests. They fill a field the caller left unset — explicitly
setting the field pre-empts them, which a REST client can never do for an output-only
field but application code can.

One contract to know when driving the patch layer from application code: **an update
patch with no fields set is a silent no-op.** `Apply` and `Buffer` return immediately —
no permission check, no `output_only_update_fn`, no mutation, no change event — so a
keys-only patch does nothing at all (and a REST `PATCH` with an empty body is a harmless
200 for the same reason). "Bump this row's enforcement stamps with no field changes" —
a *touch* — is therefore not expressible as a bare update patch: use the generated
`New<Resource>Touch(keys…)`, which exists exactly when the resource declares an
`output_only_update_fn` field and runs the full update pipeline with the update
functions supplying the write. Keep the two kinds of server-owned timestamp apart:
update functions enforce mechanical stamps (`UpdatedAt`), while a timestamp with domain
meaning (`LastServicedAt`) is an ordinary **explicit** update performed by the one piece
of application code that owns the business event — `output_only` keeps clients out, and
the writing code sets the field like any other.

A second contract sits beside it: **an update that changes nothing is no error.** Where
every field an update sets already holds that value, the update succeeds, and on a
resource that keeps change records it writes no record, since nothing changed. A request
repeated after a timeout, a task a queue delivers twice and a sync that runs again find
the row as they asked, and a REST `PATCH` that restates the row answers 200. Only the
low-level insert-or-update (`resource.CreateOrUpdatePatchType`) keeps its refusal.

Two more things a patch can be told from application code, each decided from whether its
row is there: an update that creates the row when it is not (`OrCreate`), and a create
that leaves the row alone when it is (`OrSkip`). Section 22 has both.

## 3. Struct tags the generator writes (zz_gen request structs)

Read back at runtime by the `resource` package; listed here for reading generated code.

| Tag | Meaning |
| --- | --- |
| `json:"camelName"` | Wire name of the field and the key under which its permissions are registered. `json:"-"` hides the field (input-only fields in read structs; primary keys and output-only fields in patch structs). |
| `perm:"-"` | The primary-key exemption marker, emitted only on primary-key fields of list/read (and computed) structs: the field requires no field-level grant, and its readability follows the resource-level grant. Every field without the marker is enforced structurally with the endpoint's permission. `-` is the only legal value — any other perm value in a request struct is a startup error (the stale-struct guard). |
| `immutable:"true"` | From `conditions:"immutable"`; the patch decoder rejects updates to the field. On the grant side, the generated Collection never lists `Update` on an immutable field's tag, so the (unsatisfiable) update grant can never be assigned to a role. |
| `index:"true"` | From the schema's indexes: the field's column leads some index of the table, the primary key and Spanner's foreign-key backing indexes included, or, on a resource with a bare `@domain` column, sits directly after the tenant column in one, since the list binds the tenant by equality and the index then seeks on both. A trailing key column anywhere else and a `STORING` column carry no tag: a filter on either alone would scan the index. On a virtual resource the tag is authored (`index`/`uniqueindex`, section 2). Makes the field filterable; `sort` is not gated on it. |
| `allow_filter:"true"` | Copied from the source struct; makes an unindexed field filterable. |
| `pii:"true"` | From `conditions:"pii"`; the field is rejected in URL filter expressions. |
| `masking:"positional"` | Copied from the source struct; a sort, filter, or cursor on the field runs on the real column while the cell stays masked in the output. Absent on a concealing field. `positional` is the only value written; any other value in a request struct is a startup error (the stale-struct guard). |
| `sqltype:"STRING(64)"` | The column's declared type, verbatim from the schema, on a patch request-struct field whose value the decoder sizes before anything is buffered (section 11): a string-kinded field on `STRING(n)`, `[]byte` on `BYTES(n)`, a decimal on `NUMERIC`, and a slice of one of those, named or not, on the matching `ARRAY<…>`. Absent on `MAX` columns, on keys and output-only fields (hidden from the patch wire), and on every other type. A value the runtime cannot pair with the field's type is a startup error (the stale-struct guard). |
| `nullable:"true"` | On a patch request-struct field typed by a slice whose column allows NULL, and nowhere else: a Go slice has one form, so the decoder cannot read the fact off the field's type as it does off a pointer or a Null wrapper. The decoder accepts a JSON `null` for the field and stores the nil slice, which the Spanner client writes as NULL (section 12, nullable slices); a slice field without the tag refuses `null` with `<field> cannot be null`, since its column is NOT NULL. `true` is the only value written; any other value, or the tag on a field that is not a slice, is a startup error (the stale-struct guard). |

| `feature:"debriefs"` | From `@feature` on the field: the flag the field is gated behind, on the list, read and patch request-struct fields alike. While the flag is off the decoders answer the field as an unknown column and the handlers leave it out of the response; the digest leaves `Resource.field` out. The value is the flag's name; a value that is not one is a startup error (the stale-struct guard). |
| `formerly:"title"` | From `@formerly` on the field: the former wire name, on the list, read and patch request-struct fields alike. The decoders read a body key, a column, a sort field and a filter field under it as the current field, and refuse a body naming both; the generated collection carries it as the tag's `Formerly`, which the permission digest mirrors (section 18). |
| `search:"token=Name_Search;index=ClientsSearch;role=text;anchors=prefix,suffix;contains"` | The field's search vocabulary (section 4), on the list and read request-struct fields of a table or view: the token column and the search index behind it, how the token column reads the column (`role=text`, `digits` or `date`, with `zone=` for a timestamp's), the anchors it was tokenized with (`anchors=prefix,suffix`), whether it answers an unanchored match (`contains`), whether it folds diacritics (`diacritics`), and the lowercased stored column that gives a case-folded `startswith` (`lower=Name_Lower`). A computed resource's `allow_filter` text fields carry `search:"memory"`: the handler evaluates their matches and the box over the rows the body yields. The decoder reads the tag back to decide which `startswith`, `endswith` and `contains` conditions a field answers and which fields the box searches; a value it cannot read is a startup error (the stale-struct guard). |

## 4. Reserved query parameters

List/Read requests accept exactly these query parameters — anything else is a 400, and
none of them can be used as field names in filters:

| Parameter | Meaning |
| --- | --- |
| `columns` | Comma-separated JSON field names to return; omitted means all accessible fields. Leaving out a sort field or the primary key changes nothing about paging: the cursor reads those keys from the statement, not from the returned row. |
| `filter` | Filter expression over indexed/`allow_filter` fields, e.g. `name:eq:Vanta`. Operators: `eq`, `ne`, `gt`, `lt`, `gte`, `lte`, `in`, `notin`, `isnull`, `isnotnull`, and on a text field with a search fact `startswith`, `endswith` and `contains` (below). A value may be written in double quotes, `name:eq:"Vanta, Ltd."`, where it carries a comma, a pipe, a parenthesis or a space; a backslash escapes a quote or a backslash inside the quotes. The search box is the condition `*:search:"nguyen 1987 4321"`, one per filter (below). On POST query routes the filter may be sent in the body as `{"filter": "…"}` instead (required for `pii` fields, and for the box where it searches one), but not in both places. The expression is validated when the request is decoded, with the rest of the request's shape: a field that is unknown or not filterable, a malformed condition, or a value that does not fit its field is a 400 before any permission check runs, any query executes, or a computed resource's List function is called. A table filter must also touch at least one indexed field, which the database parse enforces; an indexed field is one whose column leads an index or, on a bare-`@domain` resource, follows the tenant column in one (section 3), and the tenant predicate itself does not count. The generated field metadata states the same eligibility, so a browser draws a filter control only where the server will answer: `filterable: 'always'` on an indexed table or view field and on a computed resource's `allow_filter` field, `'withIndexed'` on a table or view `allow_filter` field and on one filterable through its search fact alone (its `search` list names the matches that stand alone, below), and nothing on a field a filter may not name. A name field (`@name`) is filtered by through the pointed-to table, and its entry in `nameFields` carries the same `filterable` and `search`. |
| `sort` | Comma-separated `field[:direction]` entries, e.g. `name:asc,rank:desc`; direction is `asc` (default) or `desc`. Omitted, the resource's `@order` applies. Every paged request needs one or the other: with neither, a request that is not `limit=all` is refused with a 400 naming the resource and the way out (`<Resource> declares no order; add a sort, or ask limit=all`; on a resource with a maximum, `<Resource> serves at most N rows per page and declares no order; add a sort`), and `limit=all` alone is served unsorted. |
| `limit` | The page size: rows returned per request. Omitted, the resource's declared default applies (`@page`, generator-wide default 50). A value over the resource's declared maximum is refused with a 400 naming the maximum, never clamped; `0` is refused. `limit=all` returns every row with no `Link` header, is permitted only on a resource that declares no maximum, and is the one list request that needs no order (`sort`). A `@computed` or `@virtual` resource with no `@primarykey` is served whole on every request, `limit=all` or none: a numeric `limit` on it is refused with a 400 (`<Resource> declares no primary key, so its list is served whole and does not page; drop the limit, or declare @primarykey to page`). |
| `cursor` | The page position, copied from the `Link` header of the page that issued it (`rel="next"` or `rel="prev"`); never assembled by a client. A sealed token carrying the boundary row's sort values, the direction, and a fingerprint of the resource, scope, filter, sort, and limit — a cursor altered, sealed by another application, or presented with a different query, tenant, or page size is a 400. A cursor is valid for the database that issued it: the seal binds it to the application, and its position is read back in that database's `NULL` placement. A request with a cursor carries the same `sort` and `limit` as the page that issued it. Every paged request carries an order (`@order` or `sort`), so every page has its `Link` relations; a cursor never names a position in an unsorted list, because such a list is served only whole. A cursor on a resource with no `@primarykey` is refused with a 400 naming the missing key: the list has no row identity to anchor a position on, so it is served whole (`limit`). |
| `count` | `count=true` on a first page asks for the total number of rows the same WHERE admits, answered in the `Total-Count` header; later pages carry no count and refuse the parameter. A whole list (`limit=all`, or a key-less resource) answers it too. A request that does not ask pays nothing. |
| `offset` | Removed. A request carrying it is refused with a 400 naming `cursor` as its replacement; pages are positioned by the row the cursor names, not by a count of rows to skip. |
| `_v` | The live pages' version (section 14): a client holding a live row or list appends it so the browser's cache keys the answer by the change it last saw. 1 to 64 characters of `[A-Za-z0-9_.:-]`; accepted and skipped, never a filter. A list or read response to a request carrying it is the one response that carries `Cache-Control: private, max-age=300`. |
| `capabilities` | Comma-separated write permissions (`Create`, `Update`, `Delete`, `Execute`) to evaluate per row — the §13 capability envelope. Each returned row gains the reserved `zzCapabilities` property: `Update` carries the positive list of editable JSON field names, every field the caller may write on the row whether or not the read projected it, in name order (a write-only `input_only` field, which no read returns, is named when the caller's Update grant covers it, so an edit form draws its blank input), `Delete` a boolean, `Execute` the positive list of `@target` methods that apply to the row — a declared `@transition` requires the row's pre-image state in its `from` set, a conditional Execute grant ANDs its condition into the same boolean (a plain `@target` method's is the condition alone; unconditional plain methods are structural, no SQL), and the user holds the method's Execute grant — and `Create` the positive list of workflow member resources the user may create beneath the row (§11): the members whose immediate `@stateRoot` hop is this resource, gated by the user's member Create grants, a conditional grant's state terms evaluated against this row's own uniform state binding while terms the parent row cannot answer count potentially-true (an unconditional member grant is structural, no SQL). Advisory hints computed from the same row image and decision instant as the read (conditions render as booleans in the same statement; pure RBAC adds no SQL; a `new.`-referencing term counts potentially-true while the rest of its condition still renders). Enforcement is unchanged. |
| `domains` | On a list across tenants alone (`@listAcrossTenants`, below): the filter on the tenant, tenant keys separated by commas, `domains=<key>,<key>`. The list reads the named tenants among those it would read without the parameter, down to one. A tenant the session does not reach, or one that does not exist, contributes nothing and is never an error, so the answer never says whether a tenant exists; an empty entry is a 400. On every other route, the tenant's own list included, it is an unknown parameter. |

**Dates and times.** A filter value on a date or timestamp column is read the way a
person writes a date, and means the whole of what is written: `deadline:eq:2026-12-15`
is that day, `deadline:lte:12/2026` is through the end of December, and
`deadline:gte:2026-12-15T09:30` is from that minute. The forms are the day in ISO, US or
eight-digit shape (`2026-12-15`, `12/15/2026`, `20261215`), the month (`2026-12`,
`12/2026`), the year (`2026`), and an ISO day with a time of day to the minute or the
second (`2026-12-15T09:30`, `"2026-12-15 09:30:15"`); an RFC 3339 timestamp with its
zone, `2026-12-15T09:30:00Z`, stays the instant it names. Each operator reads the span as
the person means it: `eq` inside it, `ne` outside, `lt` before its start, `lte` before
its end, `gt` from its end, `gte` from its start; an `in` list ORs the spans and a
`notin` list ANDs their outsides. The parse expands the condition into those
comparisons, so the SQL and the in-memory evaluation see plain comparisons on typed
parameters, `(Deadline >= @p1 AND Deadline < @p2)`. A value with no zone of its own is
read in the person's zone, which the resource client sends on every request as the
`Time-Zone` header (an IANA name, `America/New_York`), so the browser filters by the
same day it shows; without the header, in the zone the column's date search fact
declares (below), and in UTC where it declares none. A date column (`civil.Date`) reads
the day, month and year forms with no zone, a month or a year being its days, and refuses
a time of day. What cannot be read is refused in plain words naming the field as written
and the forms with today's date, `deadline: 'tomorrow' is not a date. Write it as
2026-11-03, 11/3/2026, or 2026-11-03T09:30 with the time.`, and the other types answer in
the same shape: `age: 'ten' is not a whole number.`, `fee: 'abc' is not a number.`,
`active: 'maybe' is not true or false.`, `shipId: 'nope' is not an identifier like
0f8fad5b-d9cb-469f-a165-70867728950e.` A header naming no zone is refused in the
header's name. Relative words (today, last week) are not read.

**Search.** A text field answers `startswith`, `endswith` and `contains`, and a resource
answers the search box, exactly where the schema states a fact for it; the generator
derives the vocabulary, writes it onto the request structs as the `search` tag (section
3) and into the TypeScript field metadata as `search: ['contains', 'startswith']`, and
the decoder refuses a match a field does not answer with the same 400 shape as an unknown
operator, naming the matches it does. Two facts. The *search fact*: a hidden `TOKENLIST`
column generated by `TOKENIZE_SUBSTRING` over the column, carried by the table's one
`CREATE SEARCH INDEX`, gives the field `contains` (`SEARCH_SUBSTRING(token, @term)`),
the anchored matches it was tokenized with (`relative_search_types => ['value_prefix',
'value_suffix']` give `startswith` and `endswith`; `short_tokens_only_for_anchors` leaves
the field its anchors alone), and a place in the box. The token column reads the column
in one of three shapes: the column itself (`role=text`, the term bound as typed, Spanner
folding case and punctuation), its digits (`REGEXP_REPLACE(col, r'\D', '')`, `role=digits`,
the term stripped to its digits so `123-45` meets `123454321`), or its date as `YYYYMMDD`
(`FORMAT_DATE('%Y%m%d', col)` or `FORMAT_TIMESTAMP('%Y%m%d', col, zone)`, `role=date`:
a date field joins the box, where a typed `1987`, `1987-03` or `3/14/1987` meets it as a
prefix of `YYYYMMDD`, and answers no text match of its own; a timestamp names the zone
its day is read in, since the database's default zone is not UTC, and one without is
refused). `remove_diacritics` is read
and folded the same way in memory; a word or n-gram tokenizer states no fact and is left
alone, and any other expression over a `TOKENLIST` column is refused at generation rather
than half-read. The *prefix fact*: a stored `LOWER(col)` column that leads an index, or
sits directly after the tenant column in one on a bare-`@domain` resource, gives the
field a case-folded `startswith` (`STARTS_WITH(col_lower, @term)`, the term lowercased by
the server) and nothing else; it never joins the box, so a table serves its box from one
index. A view's author names the table column a field carries with `search:"Table.Column"`
(section 2) and projects the token column under its own name; a computed resource's
`allow_filter` text fields answer all three matches and the box in memory. A field with
a search fact is filterable through its matches without `index` or `allow_filter`, and a
match through the search index, or through the index the lowercased column leads, is the
indexed touch a table filter needs; the box is one too.

The box, `*:search:"nguyen 1987 4321"`, is one condition on the pseudo-field `*`: the
text splits into terms at whitespace, at most 8 terms of at most 64 characters each
within 200 in all, and the condition holds where **every term** is found in **at least
one** of the fields the box searches, each term read for each field's role (a term with no
digits never meets a digits field; a term that is not a date never meets a date field).
The fields are the resource's own fields with the search fact, in declaration order, and
the `@searchAlso` branches (section 1); the TypeScript resource metadata lists them as
`searchFields: ['name', 'contactEmail', 'clientContact.displayName']`, so a screen can say
what the box searches, and the resource decides the list, never the screen. Beside it the
metadata carries what the browser needs to apply the same grants: `pii: true` on a field
declared `pii` (a box reaching one is posted), and `searchBranches: [{ field:
'clientContact.displayName', resource: Resources.ClientContacts, fieldName: 'displayName' }]`,
the child resource and field whose List grant governs each branch, with `pii: true` on a
PII child field, so a denied branch leaves the box's placeholder as it leaves the search. Every
token field contributes `contains` to the box, anchors or not, so a term is found anywhere in
the value; the anchors give the field `startswith` and `endswith` beside it. One box per filter; a
second is a 400. The box is sent as a filter condition like any other, so it joins the
rest of the filter with `,` and `|`, and the whole filter travels in the URL, or in the
POST body where the box searches a `pii` field, as a `pii` field's own condition does.
Under the caller's grants a box field the caller is denied drops out of the box silently
(the box names what it searches, so its absence discloses nothing, where an explicit match
on a denied field is a 403 like any filter on one), a conditionally granted field stays
with its condition conjoined inside its match (`(condition AND SEARCH_SUBSTRING(…))`, so
the search index still serves it, where an `eq` runs over the visible projection), a
positional field renders bare, and a `@searchAlso` branch follows the child field's grant
the same way; on a computed resource every searched field needs the caller's grant
with no condition left once the request is checked, as its sort and filter fields
do. On Spanner a box whose fields come from one
search index renders inline; a box whose fields span several search indexes (a view
projecting two tables' token columns) or whose resource declares `@searchAlso` branches
reads the resource through one key set per term, a `UNION DISTINCT` of the matching keys
from each index and each child, joined on the key and apply-joined into the resource,
since the service refuses an `OR` touching two search indexes in one query block; such a
box joins the rest of the filter with `,` alone (a box under `|` is a 400 naming the
rule), and the statement names the query optimizer version the search functions need in
its request options. A view whose box spans several indexes declares `@rowsOf`, and its
root table carries an index on (tenant column, join column) for each other table its box
reaches, which generation refuses without, naming the index: the branch into that table's
index comes back to the view's rows through the foreign key, and the key set costs the
matches only when that index serves it. On PostgreSQL every match renders as `ILIKE` over the role's
expression of the column and a branch as a correlated `EXISTS`; the matching trigram or
expression index is the application's DDL, as every PostgreSQL index is. `count=true`
counts under the same box, and a cursor's fingerprint covers the filter as before.

A sort or filter runs over the projection the caller can see. A field the caller is
denied cannot be sorted or filtered on: the request is a 403 naming the field, because
the order or the membership of the result would leak its values. A conditionally
granted field can be: the statement orders and filters on `CASE WHEN <condition> THEN
column END`, so a cell the caller may not see is `NULL` for the query — it sorts in the
`NULL` region where the database places it (Spanner first ascending and last descending,
PostgreSQL the reverse, the same as a genuinely `NULL` cell), matches `isnull` and
nothing else, and a cursor positioned on it carries the `NULL` key.
A condition permits only when it is TRUE, and evaluation is three-valued as in SQL: a
comparison against a missing value (a `NULL` column, a `NULL` foreign key or a join path
that reaches no row, a subject value whose requester has no anchor row) is UNKNOWN,
`IN` and `NOT IN` against a missing value are UNKNOWN whether the list is literals or a
subject set, `IS NULL` is TRUE and `IS NOT NULL` FALSE; UNKNOWN refuses exactly as FALSE
does — the row is filtered, the cell masked, the write refused, the capability withheld.
A number literal takes the attribute's storage type: exact against an `INT64` or a
`NUMERIC` column, a double against a `FLOAT64` one. A join-path attribute renders as one
scalar subquery per hop, a subject set as an `EXISTS` guarded by the attribute's nullness,
so the database's own three-valued logic gives these readings. A set's own definition
renders inside that `EXISTS`: its `who:` as the tests that make a row the requester's
(a test against another set is that set's `EXISTS` over the row's column, under that
set's own tenant filter), its `where:` as a condition over the row's columns, then the
match of the set's value to the attribute and the set's tenant filter, as before. A row
is the requester's only where its `who:` is TRUE and counts only where its `where:` is
TRUE, so a `NULL` in either drops the row; the reference evaluator in
[`conditiontest`](../accesstypes/condition/conditiontest) states them in Go and the
semantic differential in this package's tests compares the rendered SQL against it on the
emulator over random conditions and rows.
When the field's condition covers the whole row predicate (every returned row shows
the field) the raw column is used instead. A grant whose condition implies the field's
counts as covered, under a closed set of same-attribute rules: an equality or `IN` list
whose values sit inside the field's `IN` list, a `!=` or `NOT IN` naming every value the
field's does, and a conjunction one of whose terms does; anything else must spell the
field's condition the same way ([`condition.Implies`](../accesstypes/condition/cover.go)).
A `CASE` in `WHERE` or `ORDER BY` cannot use
an index, so only such a field pays for it, and it pays on every page: the partition
sorts whole ([finding 5](../lodestar/perf/REPORT.md)). That is the concealing behavior,
and it is the default for every field. A field declared `masking:"positional"` (section
2) keeps its cell hidden but sorts, filters, and pages on the real column: the index
serves the page, and the field's rank is disclosed. Its cursor still carries the
boundary row's real value: the statement selects it a second time under a reserved
`zzCursor…` column that never reaches the wire, and the token carries it sealed. Every
sort key the request did not select rides the same column — a `columns=` list that
leaves out the sort field or the primary key, or an `@order` field the grid does not
show — so the cursor carries what the statement ordered by (a concealing key's visible
projection, `NULL` where the cell is masked) and the row data stays exactly what was
selected. `index:"true"` stays the declaration of which fields may be filtered. A `@computed` resource's
rows are judged in Go as its function yields them, and no statement renders a visible
projection over them, so its sort and filter fields still require a grant with no condition
left once the request is checked.

The primary key sits outside these rules: it needs no field grant, is never masked, and
its presence in a request keeps no row. So a request for the key alone (`columns=id`) has
no column's condition to stand for the row's, and the condition on the resource itself
filters it: the rows are the ones some grant of the caller's admits, in the list and in
its count.

A paged list answers with headers beside its JSON array body. `Link` (RFC 8288)
carries a complete URL per relation that exists — `rel="next"` and `rel="prev"`, each
the request's own URL with `cursor` set — so the first page has no `prev` and the last
no `next`, and a client follows the URLs as given. `Total-Count` carries the total when
the first page asked `count=true`. Every paged request carries an order (`@order` or
`sort`; without one it is refused, see `sort`), so a page always has the `Link`
relations that exist. `limit=all` answers with no `Link`: the whole list, unsorted
when neither an `@order` nor a `sort` orders it, a table statement then carrying no
`ORDER BY` and a computed list keeping its body's order, with `Total-Count` when asked. A
`@computed` or `@virtual` resource with no `@primarykey` is that whole list on every
request: it has no read route, no page, and no row identity, and a numeric `limit` or a
`cursor` on it is a 400 naming `@primarykey` as the way to page. A list served cross-origin must
add `Link` and `Total-Count` to the CORS exposed headers, or the browser client cannot
read them.

**A list across tenants.** A tenant-scoped resource is served under its tenant's segment,
one tenant to a request. A resource that declares `@listAcrossTenants` (section 1) is
also served without the segment, and that one request reads its rows in a set of
tenants: every tenant the session reaches (the tenants `user-domains` lists, section 5)
in which the caller holds the resource's List grant, narrowed by `domains` when the
request carries it. The request is the list request above in every other respect: the
same `columns`, `filter`, `sort`, `limit`, `cursor` and `count`, one order and one page
across the set, and one `Total-Count` for all of it.

Each row is checked in its own tenant. A person's roles differ by tenant, so the
condition on the resource, each field's visibility and the mask are rendered for the
caller's grants in the row's tenant: a row one tenant's grant admits is listed while
the same row in another tenant would not be, and a field shows on the rows of the
tenants whose grants cover it. Tenants in which the caller's checks come out the same
share one rendering, and the statement carries one branch per such group of tenants,
each led by the group's tenants as a bound array. A field the request names that no
grant covers in one of the tenants is masked on that tenant's rows, left out of the
row as any masked cell is, where the tenant's own list would refuse the request; a
sort or a filter on it reads `NULL` there, as it does for a masked cell. The request is
refused (403) only where a field it names by `columns`, `sort` or `filter` is denied in
every tenant the list reads. A subject set follows the row: the tenant filter of a
subject subquery compares the anchor's tenant to the row's tenant, never to a parameter,
so a membership held in one tenant answers only for that tenant's rows. The search box
works across the set the same way: a field's grant decides per tenant whether the box
searches it there, and a `@searchAlso` branch is judged in the child row's own tenant.

Each row says which tenant it is in, under the reserved `zzTenant` property beside its
fields, so a page groups by tenant and opens a row inside its tenant's routes; a
resource bound through a join path (`@domain(via: ...)`) answers the tenant the path
resolves to. A tenant the session does not reach contributes no row and is named in no
answer, as the tenant's guard conceals it. A caller who holds the List grant in no
tenant, or whose `domains` names none the list would read, reads a list of no tenant:
200 with an empty array and a count of 0, and no statement runs. A set that comes to
exactly one tenant is that tenant's own list: the statement and its parameters are the
ones the tenant's route renders, byte for byte, so its indexes and its plans are the
tenant route's, its refusals are too (a named field no grant covers is a 403 there), and
each row still carries `zzTenant`.

The list is read-only and has no keyed read: a row's link names its tenant, and the row
is read, written and asked for its `capabilities` on its page inside the tenant.
`capabilities` on the list across tenants is refused with a 400 saying so. Only a
declared resource is listed this way: the generated collection carries the declaration
(`CollectionResource.ListsAcrossTenants`), and `QueryDecoder.DecodeAcrossTenants`
refuses a resource the collection does not mark, saying to declare
`@listAcrossTenants`, so a hand-written handler cannot read an undeclared record
across tenants. A cursor of
the list is bound to the resource, the `domains` the request sent, the filter, the order
and the page size, and is refused on the tenant's own route as that route's are refused
here; it is not bound to the set of tenants the session reaches, which is worked out
again on every request, so a person seated in another tenant between two pages reads
its rows from the next page on. The list is plain unless the page asks: a request that
carries the subscribe header registers one live subscription over the set of tenants
the list reads for the person, up to the most one subscription holds, where a tenant's
own list is one over that tenant (section 14). The set is worked out when the list is asked for and
again at each renewal, which carries the page's `domains` back, so a tenant the person
was seated in or removed from since is in or out of it from the next renewal on. After
a write the publisher asks what it asks of every list, who is subscribed to this list
in this tenant, and the record answers from the sets: the page hears a change in a
tenant of its set as that tenant's list changing and fetches once, and it is never
woken by a change in a tenant the person cannot enter. A subscription over a set of
tenants exists for a declared resource alone: the list's request is the only thing that
makes one, and the renewal drops one whose resource the collection does not mark, before
working out its set (`live.WithListsAcrossTenants`, which the generated `LiveRenew`
passes). What this costs and where it stops:

- One subscription holds at most 10,000 tenants (`live.MaxSubscriptionTenants`). The
  record keeps the set on the subscription's one document, and the store bounds what it
  indexes of one document: Firestore allows 40,000 index entries, a tenant of the set
  costs two, so a document's write fails near 20,000 tenants, and the ceiling is half of
  that (the arithmetic is in the Firestore implementation's README). A list that reads
  more tenants than the ceiling registers nothing when the page asks: it is answered as
  a plain list, not marked cacheable and with nothing logged, since that is a decision
  and no failure, and the page shows it as any list that is not live. A subscription
  whose set has grown past the ceiling by a renewal is dropped by it, named in the
  answer's `dropped`, and the tab's other subscriptions are kept and extended.
- A subscription the record does not take at a renewal, a document the store judges
  invalid, is dropped too, with the store's reason logged, and stops none of the tab's
  others; the record out of reach fails the renewal as a whole, and the tab renews again.
- A list the page did not narrow with `domains` is told of a change to the resource's
  list in any tenant of its set, whichever rows the page is showing: it fetches its page
  again and shows what the fetch answers.
- A subscription the renewal dropped, because its set came to no tenant or to more than
  one subscription holds, or its resource is no longer declared, is not made again by a
  later renewal: the page's next request for the list makes it, if there is then a set
  to make it over.

On PostgreSQL the membership renders as `= ANY(@array)` where Spanner's
is `IN UNNEST(@array)`.

## 5. The permission endpoints

Every generated router registers two library-owned endpoints on the default outlet
(applications wire nothing):

```
GET /<prefix>/permission-digest              → the session user's global digest
GET /<prefix>/permission-digest?domain={id}  → one tenant partition's digest
GET /<prefix>/user-domains                   → the domains the session user can see
```

An additional outlet that serves sessions (`Auth(...)` under the generated router,
`ServesSessions()` under a hand-written one) gets the same two routes under its own
prefix, served by the same generated handlers behind the outlet's session middleware;
an outlet without the declaration gets neither (the generated router tests prove its
prefix 404s them). The default outlet serves them unless it is declared `Tokens(...)`: a
machine outlet's generated routes are the resource routes its tokens open and the device
flow's two endpoints, nothing else, so a machine outlet, the default included, registers
neither of these nor the live routes, and when no outlet serves sessions the generated `PermissionDigest`,
`UserDomains` and live handlers are not written at all.

**The digest.** The payload is the user's structural grant enumeration for the requested
scope: resource → permission → `granted` | `conditional` | `conditionalOnTime`, with
field targets under their dotted names (`"WorkOrders.title"`) and **absence meaning
denied** — consumers fail closed by construction. A grant on all of a resource's fields
(`Documents.*`) is listed field by field, over the fields the release declares for the
resource, so a field's own entry is the whole answer for it. It is advisory UI material (which
menus, routes, and form inputs to render); enforcement stays with the endpoint gate, the
read rules, and the write stages. Nothing folds — no `now`, no row data — so a payload
is stable for the life of a policy snapshot and caches cleanly per scope.
`conditionalOnTime` is structure too, read from the conditions' text and never from a
clock: every condition the target is held under reads the time of the request alone
(`now`, `timeOfDay`, `dayOfWeek` against literals), so the server answers the target
whole while one of them holds and refuses it while none does. `conditional` says at
least one reads more (the row, a fact about the caller, a request field), so the server
may judge the target row by row. An unknown or grant-free domain digests to `{}`, so the
endpoint never confirms tenant existence under `WithConcealedDomains`.
`@impulseframework/resource-angular/types` carries the matching `PermissionDigest` /
`PermissionDigestState` types (and `RowCapabilities` for the capability envelope).

**Who a list across tenants is for.** No one tenant's digest can say whether a page that
lists a resource across tenants (section 4) has anything to show a person, so the global
digest says it. For each resource declared `@listAcrossTenants`, the digest of the
global scope carries the entry `<Resource>: {"ListAcrossTenants": "granted"}` while the
person may list the resource in at least one tenant the session reaches
(`resource.ListedInSomeTenant`: the session's reach, then the List grant, granted or
under a condition, one check for each reached tenant until the resource is answered),
and no such entry otherwise. `ListAcrossTenants` is the digest's own answer and no
grant's: no role names it. It follows the session, as the reach does: a tenant that
admits its people through its directory alone does not count for a password session,
and counts for a super administrator's. The entry sits behind the resource's feature
flag with the rest of its entries, and a tenant's digest never carries it. The generated
`PermissionDigest` handler passes `resource.WithListsAcrossTenants(Collection())` in an
application that declares such a resource; the browser library asks the entry for the
page's menu entry and its route, so the application names no permission for the page.

**User domains.** The payload is the sorted list of domains where the user holds at
least one grant, each with the tenant's name
(`[{"domain": "11111111-1111-4111-8111-111111111111", "name": "North"}]`) — the place switcher's question, answered by
the library so no application hand-writes it. A tenant the session reaches is listed
plainly. A tenant that seats the person and that this session cannot enter, a
directory-only tenant whose directory the person did not sign in through, stays in the
list marked `closed`, with the reason as a sentence
(`{"domain": "…", "name": "Acme", "closed": "Acme admits only people who sign in through its own directory. You signed in with a password."}`):
the person holds a seat there, so the list tells them nothing new, and it says why this
sign-in does not open the tenant. A session that came through another directory is told
which, in the words a person knows it by ("You signed in through Google Workspace
north.example.", "You signed in through your organization's Microsoft directory."): the
generated handler passes `WithDirectoryNames` with each auth's namer (section 16, "How a
sentence names a directory"), and the reason never says a connection that is an id. The
tenant's own administrator who signed in with a password, and whose session carries no
second factor, is told the way in they hold: the reason ends "As one of its
administrators you can still enter it: sign out, then sign in again with your password
and the code we email you." It is asked of the same decision the reach is
(`signin.TenantDeclaration.AdmitsAdministrator`), with the code taken as given, so the
sentence and the next sign-in agree; such a session arises when the role, or the switch,
came after the sign-in, and when a password change left the session without its code.
Only a person's own sign-in is told so: a session that
views the application as someone else, and one that acts as a role, are refused the
tenant and are listed none closed. The browser library keeps a closed tenant out of the
session's domains (`PermissionStore.domains`, so the tenant guard refuses it as it
refuses any tenant the session does not reach) and lists it apart in the place switcher,
under "Closed to this sign-in", with the reason (`PermissionStore.closedDomains`,
`AuthService.closedDomains`, `PlaceService.closed`). `resource.ClosedDomains(ctx, perms)`
is the list on the server. The name is the tenant roster's copy of the
record's name column, the field the record marks `@tenantName` (section 1): the generated
roster constructor passes `WithTenantNames` and the generated handler `WithDomainNames(roster)`
to `UserDomainsHandler`, so a tenant's own people read its name without holding Read on the
record; the domain stands in where no name is held. The predicate is concealed tenancy's own
foothold test (`access.Client.UserHasGrants`) over the session's admission
(`UserPermissions.Reaches`), so a domain listed here is exactly a domain whose routes
answer the user with ordinary 403s rather than a concealing 404: the switcher and the
domain guard can never disagree. A tenant is offered only when the session may enter it,
and the application's tenant declaration decides (section 6); a tenant marked `closed`
answers the session as a tenant that does not exist, as it did before the list named it. The global scope is never
a domain. The answer reports grants, not tenants — a domain the application has since
removed still lists while grants in it remain; existence is the tenant roster's
(`@tenant`, section 16). An empty membership is `[]`, never `null`.

### What a refused caller is told

A request the caller's grants refuse is answered 403 with one sentence a person can
read, the same in every application:

| What was refused | The sentence |
|---|---|
| a permission on a resource | "You may not change hangar passes here." |
| a permission on a field alone | "You may not change the budget of hangar passes here." |
| on several fields | "You may not change the budget, the due date or the notes of hangar passes here." |
| a method, or a permission the application declares | "You may not do this here." |
| a method that may not run against the row it names | "You may not do this to this mission." |
| a sort or filter by a field the caller may not see | "You may not sort or filter hangar passes by the budget here." |
| a search of a computed resource by such a field | "You may not search hangar passes by the budget here." |
| a write whose grant holds for some rows and not for this one | "You may change some hangar passes here, but not this one." |
| the same, where the grant is on a field | "You may change the budget of some hangar passes here, but not of this one." |
| a column, a sort or a filter no tenant of a list across tenants grants | the same sentences, ending "anywhere." |
| the history of a feature flag none of whose facts the caller may list | "You may not see this flag's history here." |
| an act the mask of a session that acts as someone refuses | "You are acting as someone else, and this session cannot create a tenant." |
| an answer a session that acts as a role the application runs no sessions as cannot give | "You are acting as a role, and this session cannot do this." |

The verb is the permission's: Create is "add", Read is "see", Update is "change", Delete
is "delete" and List is "list". The resource is its name split into lower-case words
(`HangarPasses` is "hangar passes"), and a field is its JSON name split the same way
(`dueDate` is "the due date"). The sentence carries no id, no permission's code name and
no table's name. Those stay in the request's log line, which names the scope, the
person, the permission and the resources as it always has
(`scope (…), user (…) does not have (Update) on [HangarPasses.budget]`), with the
sentence the caller was sent after it.

A handler that checks a permission itself answers its refusal in the same words with
`resource.NotPermitted`, which takes what the log keeps, the permission and the
resources refused:

```go
if !decisions[resources.ShipsLogEntries].IsGranted() {
	return httpio.NewEncoder(w).ClientMessage(ctx, resource.NotPermitted(
		errors.Newf("user %s does not have List on %s in %s", user, resources.ShipsLogEntries, domain),
		accesstypes.List, resources.ShipsLogEntries,
	))
}
```

A method that names a row (`@target`, `@transition`) and may not run against it says
the record and nothing else: the struct's name split into lower-case words (`Mission` is
"this mission", `SectorPilot` is "this sector pilot"), with no method's name and no id.
The sentence is the same whether the row's state or the grant's condition refused, so
the caller is never told which. The log line keeps the rest, in the words the refusal
used to say (`HoldMission may not run against Mission 8f0c…`). The generated handler
and the method's gate both answer with `resource.TargetRefused`, which takes the
method, the record's struct name and the row's key values; a handler that refuses a row
itself answers the same way:

```go
return resource.TargetRefused("HoldMission", "Mission", p.MissionID)
```

A method whose row is not there for the caller is answered 404 by the same rule: "This
mission does not exist.", the record's name in words and nothing else. The sentence is
the same whether no row has the key or the row is another tenant's, so the caller is
never told that a row is there. The log line keeps the struct's name and the key
(`Mission 8f0c… does not exist`). The generated handler and the method's gate both
answer with `resource.TargetNotFound`, which takes the record's struct name and the
row's key values. A computed record's file route answers a row its read does not show
with it too, and a content function's nil is told the same sentence. A handler that
finds no row itself answers the same way:

```go
return resource.TargetNotFound("Mission", p.MissionID)
```

The library says the same sentence where it finds the row missing itself: a read of a
row that is not there for the caller, and an update or a delete whose row it looks for
and does not find, are answered "This mission does not exist.", by the record's struct
name in words. What it looked for (the statement's `WHERE` clause with its values, or
the row's key) is kept for the log and never sent. A write whose record names another
that is not there for the caller is answered "A record this one refers to does not
exist." A record that reads its tenant through other rows (`@domain(via: ...)`) is
answered so on both writes: when it is created under a row of another tenant, and when
an update moves it under one. The answer is given before any condition of a grant is
judged, so a row of another tenant is answered exactly as no row is. On a create, a
condition that reads through a row the record names reads it only where it is the
request's tenant's; a row of another tenant reads as no row.

A method can also be held to where its caller's session may go. `@callerEnters` on the method
says a row offers it only to a caller whose session enters every tenant the row names, and the
row's struct names them in a `TenantsToEnter` method: a query that selects a column named
`Tenant` beside its key columns, and `Match`, which pairs each key column with the column of the
row it is matched to (one pair for a row named by one column, a pair for each column of a row
named by several). A row read with `capabilities=Execute` then leaves the method out for a
caller whose session does not enter one of those tenants (`UserPermissions.Reaches`). A method
that names no row can be held the same way on the rows a page runs it from:
`@callerEnters(Row, ...)` names the resource structs, each declares `TenantsToEnter`, and a row
of one of them lists the method in its `Execute` answer as a row lists a method that names it.
The generated method metadata carries the resources (`offeredOn`), and a page that fills the
method's request from a row of one of them offers it only where the row's answer lists it;
from a row of any other resource the method is offered by the grant alone. The declaration
shapes what a page offers and refuses nothing: the method's own code refuses the call.

A method can be kept off a person's own row as well. `@notOnOwnAccount(ID)` on a method that
names its row says the row is an account's, by the field named, and that the row does not
offer the method to the person whose account it is. A row read with `capabilities=Execute`
leaves the method out where the field holds the caller's own account, and in a session that
views as someone else on two rows: the row of the person viewed as and the row of the real
person behind the session. As with `@callerEnters`, the declaration refuses nothing: the
method refuses the call.

## 6. Impersonated sessions

The `session` package can establish a session that operates as another user or as a role
on behalf of an authenticated actor, attenuated by an `accesstypes.PermissionMask`. The
mask is the control: a session that impersonates a person with no mask holds everything
that person holds (the session package's "Impersonation is granted with a mask", which
also has the declaration of the kinds an application offers). Such a session is an
ordinary session to every consumer; this package supplies the pieces that must know
about it:

- **`SessionPermissions(ctx, forUser, forRole, roster, declarations, classes)`** composes the
  request's `UserPermissions` from the session: `forUser` (typically
  `access.Client.ForUser`) for a user principal — an ordinary session or an impersonated
  user — and `forRole` (typically `access.Client.ForRole`) for a role principal, admits
  the session into the application's tenants, lists its domains from the roster, then
  applies the session's mask. A role checker satisfies `RolePermissions` — `UserPermissions` without `User()`,
  because a role is not anyone — and the composition supplies `User()` itself: the
  session's effective identity, which for a role principal is the actor who established
  it, so a row condition's `subject` binds to the real person and nobody's identity is
  borrowed. A person is named as the permission store names them, by the id of their
  account (`sessioninfo.SessionData.User`), never by the username, which can change and
  which two Auths over one database can each hold: `User()` is that id, `subject` binds
  to it, and a column that records who did something (the anchor of a `@subjectSet`, a
  `filedBy`) holds it. An application that does not operate sessions as roles passes
  `RolePrincipalsUnsupported` as `forRole`; a role principal then fails closed (every
  check Denied, empty digest, no domains) with the same `User()`. Forgetting the mask fails *open*,
  which is why the composition is here rather than hand-rolled per application.
- **`Masked(perms, mask, classes)`** is the attenuation on its own. A check is Denied,
  before policy is consulted, when the mask does not keep its permission kind (for
  every resource asked about), when the mask refuses a class its resource carries
  (in either scope: the class is the action's, whichever scope registers it), or
  when the mask refuses the permission on the resource by name; the other resources of
  the same check are answered as policy answers them. Each denial is logged as the
  mask's, with the mask, the person, the resource and the reason
  (`impersonation: Execute denied by mask (*;-class:credential) for … on
  SendStaffUserPasswordResetLink: the mask refuses the class credential`), and the
  permission digest drops the same entries, so the frontend's digest agrees with what
  `Check` enforces and no page offers what the session is refused. A field is judged
  by its record. `classes` is where the check reads an action's classes
  (`ActionClasses`): the generated collection, `router.Collection()`, built once and
  held by the application, as the skeletons do. A mask that refuses a class, checked
  with no classes handed in (`nil`), refuses everything: the check cannot know which
  actions carry the class, and it never guesses open. For the same reason such a mask
  refuses Execute on a name that is no action of the application, one the collection
  registers no Execute on for the whole application or for a tenant: a name spelled
  wrong, an action checked by hand and never registered, or an action of a collection
  other than the one handed in. Its classes are not none, they are unknown, and the
  denial says so (`… "CreateWorkspace" is no action of the application, so the classes
  it carries cannot be known: register it by hand (@manualAddResource(Execute) on the
  constant, with the @class it carries) or correct the name`). It is the rule the
  declaration of a kind holds its grant and its mask to when the application starts
  (`session.NewImpersonations`), asked here of the names that arrive with a request. An
  application's own route that asks the mask about an action no grant admits
  (`resource.RefuseActionUnderMask`, `resource.MaskRefusesAction`, section 16) therefore
  registers that action, by hand, under the name it asks about. The other permissions
  are held to the collection too: under such a mask, Create, Read, List, Update or
  Delete on a name the collection registers nothing under, for the whole application
  or for a tenant, is refused before policy is asked (`… the application registers
  nothing under "Shipyrds", so what the name is cannot be known: register it by hand
  (@manualAddResource(Update) on the constant) or correct the name`). A record the
  collection registers carries a class on the writes the generator gave one (below),
  and is left to policy for each of its other permissions, the ones it registers and
  the ones it does not; a field is judged by its record. Every check a generated handler makes names a record or an action of
  the collection, so this rule refuses none of them; it is reached by a check written
  by hand on a name registered nowhere, and by an application of several sites that
  hands each site's check its own collection while one policy holds them all: under a
  mask that refuses a class, a session at one site is refused the names of the other,
  and they are left out of the digest that site serves. A mask that refuses no class
  leaves such a name to policy, as before.
- **`HoldsAction(perms, scope)`** answers whether the request's person holds Execute on
  an action, granted outright, in one scope. It is the grant check the session
  package's declaration of impersonation asks
  (`session.Impersonations.Request(ctx, ask, resource.HoldsAction(perms, scope))`).
- **Nobody is the subject of what they do in such a session.** The mask says what the
  session may do. One thing no mask decides: the person who started the session changes
  nothing of their own account from inside it, a role, a place in a group, a password,
  an address, and writes no row of their own in a record that decides what a person may
  do, a clearance, a spending limit, a place in a team. The generated user management
  refuses every such act whatever the mask keeps. The generated write of a record that
  carries a `@subjectValue` or a `@subjectSet` asks the same rule about the person the
  row names, before the write and after it (`identity.RefuseImpersonatorAsRowSubject`),
  about a set of theirs the row names (`identity.RefuseImpersonatorAsSetSubject`), and
  the write of a record a dotted `value:` is read through asks it about the row their
  own value is read through (`identity.RefuseImpersonatorAsReadThroughSubject`), with
  nothing declared for it. An application's own action that names a person asks it
  with `identity.RefuseImpersonatorAsSubject(ctx, account, act)` (section 21, "The rules
  on who is asking", which also says what the rule cannot see, and the class `access`
  and the named refusals a mask carries for it).
- **The tenant list and the admission.** `roster` is the application's tenant roster
  (`DomainRoster`, the `TenantRoster`'s `Domains`; nil in an application without
  tenants), and `declarations` is the application's reader of what a tenant declares
  about who may enter it (`TenantDeclarations`: a `signin.TenantDeclaration` by domain —
  the directories the tenant admits its people through, and whether through those only;
  nil when the application keeps none, and every tenant admits every session). A tenant
  is offered only when the session may enter it, and the tenant's declaration decides:
  `UserPermissions.Reaches(domain)` is true when the declaration admits the way the
  session was established — its sign-in's method and connection, the session's first auth
  event (`sessioninfo.SessionData.EstablishedBy`; `signin.TenantDeclaration.Admits` is the
  decision, the same one the sign-in policy makes over every tenant of the person) — or
  when the session's actor is a super administrator, a holder of the role the access
  library defines in every auth (`accesstypes.SuperAdministrator`), who reaches every
  tenant whatever its declaration says; or when the tenant is directory only and the
  session is its own administrator's, established with a password and carrying a second
  factor (`signin.TenantDeclaration.AdmitsAdministrator`, the decision the sign-in
  policy's second rule makes): a tenant's administrators keep a way into their own
  tenant, so one whose directory is broken is not one nobody in it can mend. An
  administrator here is a person who holds one of the roles the application names as a
  tenant's administrator's (`access.WithTenantAdministratorRoles`) in that tenant
  itself, by a membership of their own or through a group kept in the tenant, inside its
  period, asked of the engine at every request (`SessionUserChecker.AdministersTenant`,
  which `access.UserChecker` implements), so the way closes at the next request once the
  role is taken away. Nobody else is exempt, a person holding another role in the global
  partition, a role in every tenant or a role through a group kept for the whole
  application included, and an administrator who signed in through another
  organization's directory or with a personal account. The checker handed to
  `SessionPermissions` answers the super administrator's question
  (`SessionUserChecker.SuperAdministrator`), by the test the sign-in policy's first rule
  asks (`accesstypes.RoleCollection.HoldsSuperAdministrator`). A tenant's declaration
  restricts that tenant's own door alone: a tenant that is not directory only, and a
  tenant that declares nothing, admits every session of a person it seats, a password, a
  personal account and a sign-in through another organization's directory included, and
  a directory-only tenant admits a session established through its own directory and no
  other. So a session reaches every tenant that seats the person, except a tenant that
  is directory only and whose directory is not the one the person signed in through;
  those are the session's closed tenants (`ClosedDomains`), which the tenant list of a
  person's own sign-in names with the reason (section 5). An account that signs in
  through a directory-only tenant's directory does not get that far another way: the
  sign-in policy refuses it a password, a personal account and any other directory at
  the sign-in, whichever tenants seat the person (the session library's reference, "The
  tenant policy", rule 2). An impersonated
  session is established by its impersonation event, so a directory-only tenant refuses
  it unless the actor, the real person behind it, is a super administrator; a super
  administrator's session started by a person who is none is refused it. The
  administrators' way in is never an impersonated session's: it is a password sign-in's. In a tenant the session does not reach it
  holds nothing — every `Check` Denied, an empty digest, no foothold — so `Domains`, the
  picker's list, is the roster filtered by the grant and the admission, the generated
  `DomainGuard` refuses a direct request for the tenant exactly as it refuses a domain
  that is not a tenant, and the two can never disagree. One composed value reads a
  tenant's declaration once, whatever is asked about the tenant after.
- **`UserEvent(ctx)`** — the event source every generated write handler stamps onto
  `DataChangeEvents` — writes a person as their username with the id of their account,
  and the session last: `bob (account id), session id`. It names the actor first for an
  impersonated session: `alice (account id) impersonating bob (account id), session id`
  and `alice (account id) as role PartnerViewer, session id`. A person with no account
  here (an actor another application authenticated) is written by name alone. Every
  tracked data change carries evidence of the real person with no regeneration, and a
  rename changes nothing about who a change was made by.

## 7. RPC methods: what a method is for

The generator enforces a method's shape (`@rpc`, the `Execute` forms, the wire
vocabulary, the frame). Three conventions it cannot see are stated here, and hold
across every application on this stack:

- **A method answers with identifiers and outcomes, never rows.** `Execute` may return
  `(Result, error)`, and the result is for what the call decided or created: the id of
  the row it opened, the timestamp it stamped, the readings it folded. Rows are read
  through their resource routes, where permission masking applies field by field; a
  result that carried a row would carry it unmasked. The generator refuses a `@resource`
  or `@computed` struct in the result position; this sentence covers the shapes it
  cannot recognize.
- **A method that only answers a question is a computed resource, not an RPC.** A read
  with no write belongs on a `List`/`Read` route with a `List`/`Read` grant, where the
  digest, the client handles, and the masking all already apply. There is no read-only
  RPC form, by decision. A caller-scoped question ("my standing", "my current level") is
  a computed resource whose `List` yields the caller's one row: `QuerySet.User()` names
  the identity the permission check ran as — the viewed person under a view-as session,
  the real actor under an act-as-role session — beside `QuerySet.Scope()`, and the
  context carries the same caller a method's body reads (`resource.CallerFrom`). A table row
  that is the caller's own is a `List` grant whose condition binds the user column to
  `subject`; nothing new is needed.
- **A transaction-form body keeps its effects inside the transaction.** A message, a
  webhook, a file written from inside `Execute` cannot be rolled back, so a dry run of
  that method lies and a retried transaction repeats the effect. Write an outbox row
  the transaction commits and let a worker deliver it; if an effect truly cannot wait,
  the method is the client form, which the dry run refuses for exactly this reason.
  A file a request carries is the one exception, and it has its own form: an
  `@upload` method's frame streams the files to the store the method names before the
  body runs, the body records the minted keys in `@file` columns of that store, and the
  transaction's commit is what claims them; on any failure before commit the frame
  deletes the objects it streamed. The body never writes the store. The other direction
  is the executor's: a row deleted or pointed at another object releases the old object,
  which the executor deletes from the store once the commit lands (section 13). What
  neither covers is a crash between the stream and the commit, or between the commit and
  the delete, which leaves an object no row claims; the orphaned-file cleanup the
  framework ships (`resource/filestore`, section 13) removes such objects once they are
  older than the window, and the window is the application's decision. The cleanup is
  the safety net, never the mechanism.

### A method that warns before it runs

Some calls a person should not be able to make by a slip of the hand: the one that
opens a sign-in method to the world, the one that deletes a year of records. Such a
method declares a warning, and the dialog a page runs it through shows the warning
before anything is saved. Three things make a warning, and the method declares all of
them:

- **The consequence, in the server's words for that call.** The method's `Warning`
  writes it, inside the handler's transaction, with the request as the person has filled
  it so far: it can read the row the call runs against and count what the call would
  touch, which no page can. It answers a `resource.Warning`: `Consequence`, the
  sentences a person reads, and `Confirm`, the words on the button, which say what the
  button does ("Retire the Kestrel"), never "OK". A warning whose consequence falls on
  things a person would want to read one by one (the people who lose access, the
  missions that are canceled) may carry them in `Names`, and the dialog draws them as a
  list of their own under the sentence. `Names` holds only what the caller may see: the
  method decides it, as it decides the sentence. A warning with more to say than one
  sentence and one list carries notes in `Also`: each is a sentence (`Says`) with the
  names it speaks of (`Names`), and the dialog draws them under the first, in order. A
  note holds only what the caller may see, as `Names` does.
- **The typed word.** `@warning(typed: retire)` names the word a person types before
  the confirm button works.
- **The warning style.** The library's method dialog draws the consequence in its
  warning colors under a warning icon, with no configuration.

```go
// RetireShip takes a ship out of service and cancels the missions it was assigned.
//
// @rpc
// @warning(typed: retire)
type RetireShip struct {
	// @target(Ship)
	ShipID impulse.UUID
}

// Warning says what retiring this ship will do, before anyone confirms it.
func (m *RetireShip) Warning(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) (*resource.Warning, error) {
	ship, missions, err := client.shipAndOpenMissions(ctx, txn, m.ShipID)
	if err != nil {
		return nil, errors.Wrap(err, "Client.shipAndOpenMissions()")
	}

	return &resource.Warning{
		Consequence: fmt.Sprintf("%s is taken out of service for good. Its %d open missions are canceled, and their crews are told.", ship.Name, missions),
		Confirm:     "Retire " + ship.Name,
	}, nil
}

// Execute retires the ship.
func (m *RetireShip) Execute(ctx context.Context, txn resource.ReadWriteTransaction, client *Client) error {
	...
}
```

The dialog asks for the warning by sending the request to the method's own route with
`X-Warning: true` (`resource.WarningHeader`; the browser client's
`handle.warning(body)`). The generated frame decodes that request and checks the caller
exactly as it checks the call: the same Execute grant, the same located row, the same
condition on the grant, so a caller who may not run the method reads no warning of it,
and every refusal answers as the call's own would. It then calls `Warning` in place of
`Execute`, rolls the transaction back, and answers the warning with 200. Nothing is
saved by asking, so the dialog asks as it opens and again after each change to an
input, and the words follow what the person has picked. A request field the method
marks `validate:"required"` must be in the request, as in the call's, so the dialog
waits for those inputs before it asks; a field the warning should speak of before it is
filled is left to `Execute` to require, in its own words. A method that declares no
warning refuses the header with 400 and does not run, so a client generated apart from
the server never runs a call it meant to ask about. A warning with no consequence, or
with no words for the button, is the method's mistake and fails the request (500).

`X-Warning` and `X-Dry-Run` each take the value `true`, in any case, or are left off the
request. Every generated frame that reads either refuses any other value with 400,
naming the header, the value it takes and what was sent, and does not run the call
(`resource.AsksWarning`, `resource.IsDryRun`, each answering the refusal): `1`, `yes`,
`false`, an empty value, and the header sent twice. A client that sends `X-Dry-Run: 1`
to rehearse a write is told so, and writes nothing.

Either header asks that the call be held back, so a call that has no dry run and no
warning refuses a request that carries one: 400, naming the header and saying the call
has none, and the call is not run. Only a method has either (an `@rpc` struct's, an
upload's, a tenant's own that the generator writes, and `SetFeature`), and its frame
answers both headers itself. Every other route the generated router mounts (a list, a
read, a collection's patch, the consolidated patch with its creates, patches and
deletes, the creation of a tenant, a scheduled call, the live routes, and the session
library's own: the sign-in, a person's account routes, the device flow) is mounted
behind one middleware, `resource.NoDryRunOrWarning`, which refuses either header with
the value `true` and any other value as the frames do. The router knows which routes
read the headers, so the refusal is said once there and no frame repeats it. The
generated router test (`TestGeneratedDryRunAndWarning`) sends both headers to every
route in its table, the resources' routes and the ones beside them, and holds each to
one side or the other; that a frame registered ahead of the refusal reads both headers
is pinned on the generator's templates, and for `SetFeature` on its handler's test. The
browser client sends the two headers only from a method's handle (`dryRun`, `warning`),
to the method's own route. What stands outside the refusal serves no call: the files
of a browser application and of the programs a release ships, and the redirect at the
root. A route an application mounts beside the generated ones is its own, and takes
the middleware by naming it.

The warning is no permission check and no second gate on the route: a script that
holds the grant calls the method without asking. It is what stands between a person
and a click, which is why the generator makes it the method's own and no page's: every
page that runs the method, the library's and a hand-written one's through
`openMethodDialog`, draws the same warning. The generated authorization matrix carries
a denied case under `X-Warning` for every method that declares one.

Two things a method deliberately cannot do. It cannot reach the response writer: no
cookies, no session started or ended, no header of its own. Its status is chosen
through `@answers` and the result's `HTTPStatus()`, never written directly, so a
declared 4xx still travels as a typed body with the transaction rolled back. A method
whose `Execute` returns only an error has no result to choose with, so the one status
it may declare is `@answers(204)`, and the frame writes No Content where the empty 200
would go; the response encoder has no status setter to reach for. The
moment a body can write the response, every method can start a session, and the
frame's guarantee — JSON in, JSON out, permission-gated, transactional — stops meaning
anything; sign-up flows that end in a login belong beside the login routes in the
auth package. And it cannot
be armed by default: inside `Execute`, typed queries and patches run trusted unless the
body calls `Enforce(caller)`, because `@state` columns are structurally unwritable from
the wire and the frame's own status stamp must stay privileged. The method's entry
contract with the permission system is `Execute` on itself, its target row, its
transition, and any row condition on the Execute grant, all visible in the collection.

A body may ask the caller's grant on another method, where one act is made of another's:
`resource.CallerFrom(ctx).ExecuteGate(ctx, method, request)` answers the decision the
other method's own decode would reach for the request the body builds, in the scope the
request was checked in. No grant is the frame's refusal (403). A condition on the grant
that reads the request's fields (`new.<name>`) is settled against the request handed,
and `ExecuteGate.Settled` says whether that decided it. What is left reads the other
method's target row: `EnforceView` judges a row the database holds, and
`EnforceProposed` the values of a row the body proposes where the database holds none,
by the condition language's own evaluator. A condition that reads an attribute the
proposed row does not carry is an error, never a pass. The generated user management
asks this way what a change to a group needs (section 21), and which roles may be the
default role of a sign-in method opened to sign-up.

A computed resource's functions are asked as the caller too. Its generated frames stamp
the caller the request was checked as (`qSet.Caller()`: the checker, the scope and the
environment of the query's own check) into the context before the `List` function, the
`Read` function or the function that renders a file runs, as a method's frame does
before `Execute`. So `resource.CallerFrom(ctx)` answers the same person in a list as in
a method, and a list whose rows depend on what the caller may do elsewhere asks it
there: the generated list of the roles an open method's default role is picked from
lists the roles the caller may give through the method that gives a role, so a picker
offers what the method it feeds would accept. Within a request served by a generated
frame of a method or of a computed resource there is always a caller. No caller means a
call the application makes outside any request (its bootstrap, a job, a hook of the
sign-in) or one of the acts a generated route does in the application's name, such as
seating the person who creates a tenant; the user management reads it that way and
judges nothing there (section 21). A handler written by hand that runs code which asks
who the caller is stamps one itself (`resource.WithCaller`).

## 8. The generated router

`GenerateRoutes` emits the route tables: one `generated<Outlet>Routes` function and
`Generated<Outlet>Handlers` interface per outlet, plus `NewTestRouter`, which serves them
bare for test composition. What sits in front of them in production is the router, and
`generation.GenerateRouter()` emits that too, beside the tables in the same package:
`zz_gen_router.go` and its proof, `zz_gen_router_test.go`. Without the option the
contract is unchanged: generated route tables plus a hand-written router.

**What the program declares.** Two facts per outlet that the generator cannot derive.
The auth behind a session outlet, `generation.Auth(importPath, methods...)`: the auth
package (the package exporting `Name`) and the sign-in methods its `session.Auth`
registers, in the order the login page offers them: `PasswordMethod()`, and each
external method under its registration name, `GoogleMethod("google")`,
`AzureMethod("lakeside-entra")`, `WorkOSMethod("sso")`, `OIDCMethod("okta")`,
`PersonaMethod("persona")`, with
`.Labeled(text)` for the label the page shows (the method's own name otherwise: Password,
Google, Microsoft, Single sign-on, Persona; for an `OIDCMethod`, the registration name
with each word capitalized, Okta). `OIDCMethod` is one organization's identity provider,
registered with the session library's generic OpenID Connect method (`oidc.SignIn`), a
provider known by its issuer alone such as Okta or Keycloak: it takes `.SyncsRoles()`
(the registration reconciles roles to the ID token's `roles` claim). A persona method is
optional at run time:
the router mounts its login and callback and the methods route offers it only where the
auth's handlers are non-nil (the method is registered where the persona provider is
configured, the test environments), so the same build offers it nowhere else; it does
not take `.SyncsRoles()`. No method declares who it admits with no invitation: each
starts invite only, and an administrator opens one to sign-up, or makes it invite only
again, in the running application (section 21, "Who may sign up"). An outlet naming no
method, a registration name outside
`[a-z][a-z0-9-]*`, an external method named `password`, a name declared twice or an
empty label is refused. A machine outlet declares `generation.Tokens(importPath)` instead,
naming the auth package of a session outlet whose `Auth` issues the bearer tokens its
requests carry (machine outlets, below). And the browser
application an outlet serves, when it serves one: `generation.WebApp(mountPath)`, `"/"`
for the application at the root, `"/console"` for one under a path. The default outlet
declares them on `GenerateRoutes`, the others on `WithRouterOutlet`. A session `Auth`
makes the outlet serve sessions exactly as `ServesSessions()` does; `ServesSessions()`
stays for applications that keep a hand-written router. Lodestar's program:

```go
generation.GenerateRouter(),
generation.GenerateRoutes("pkg/router", "api",
	generation.Auth("github.com/impulseframework/impulse/lodestar/pkg/auth/crew", generation.PasswordMethod(), generation.GoogleMethod("google")),
	generation.Manages("github.com/impulseframework/impulse/lodestar/pkg/auth/members"),
	generation.WebApp("/console"),
),
generation.WithRouterOutlet("droids", "droids", generation.Tokens("github.com/impulseframework/impulse/lodestar/pkg/auth/crew")),
generation.WithRouterOutlet("portal", "portal/api",
	generation.Auth("github.com/impulseframework/impulse/lodestar/pkg/auth/members", generation.GoogleMethod("directory").Labeled("Company directory").SyncsRoles()),
	generation.WebApp("/portal"),
),
```

A registration's `.SyncsRoles()` and an outlet's `Manages(importPath)` shape the Auth's
generated management (section 21): whose roles a directory writes, and which other
Auth's people the outlet manages. A registration declaring
`.SyncsRoles()` stands alone on its Auth: the directory is then the authority for everyone
the Auth holds, and a second method beside it is refused.

Under `GenerateRouter` every outlet says how it authenticates, one way: an outlet with
neither `Auth` nor `Tokens`, or with both, fails generation, as do a machine outlet with
a `WebApp`, a machine outlet whose `Tokens` names an auth no session outlet declares with
`Auth`, the retired `APIKey()` (the refusal names `Tokens` as its replacement), two
outlets on one mount path, and a browser application mounted under an API prefix.
Without the option the three declarations are refused rather than ignored.

A browser application at `"/"` must be the only one. An installed browser application's
scope is every URL under its start, so beside a second application one at `/` would own
the origin: the application under a prefix would never get its own install prompt, and
its notifications and links would be attributed to the one at `/`. Generation refuses
`WebApp("/")` declared beside any other `WebApp`, naming both outlets; one application
at `/` with no other stays valid and is then the catch-all. With several applications
none is mounted at `/`, and the router answers `GET /` alone with a temporary redirect
to the default outlet's application, its mount path with a trailing slash (`/console/`),
or to the first declared outlet's that serves one when the default outlet serves none.
Nothing else changes at the root: every other unmatched path is 404.

**The generated file** holds four things, in this order. The chain comment opens it as
the package documentation: every outlet's middleware in order, outermost first, one line
per group, each chain followed by what it stands in front of. It is the review surface:
what sits in front of every REST handler, readable without the code beneath it.
`Handlers` is the full surface the router needs: the generated handlers of every outlet;
each session outlet's session surface as an interface the generator writes for it,
`<Outlet>Session`: the `Auth`'s middleware and session routes, `Methods`, the pending
sign-in's three handlers, and each declared method's handlers under its registration
name (`PasswordLogin`; `GoogleLogin` and `GoogleCallback` for a method named `google`;
`LakesideEntraLogout` too for an Azure one), the default outlet's embedded
(`DefaultSession`) and each additional one's through a getter named after it
(`Portal() PortalSession`); `<Outlet>Tokens` per machine outlet (`Droids() DroidsTokens`,
embedded as `DefaultTokens` for a default machine outlet), the token check the issuing
auth's `*session.MachineOutlet` satisfies, and on the issuing auth's session surface the
device and token handlers (`DeviceAuthorize`, `DeviceToken`, `DeviceLookup`,
`DeviceApprove`, `DeviceDeny`, `TokensList`, `TokensCreate`, `TokensRevoke`), which the
auth's `Device()` and `ApplicationTokens()` return; `LogExporter`, where
the request log goes, from which the router builds the request logger; `SecurityHeaders`,
`NoCaching`, and `CompressionMiddleware`; and per `WebApp` a `DeepLink` and `Assets`
pair, prefixed with the outlet's name for an additional outlet
(`PortalDeepLink`, `PortalAssets`), which the application builds on
`resource.BrowserApp` (section 17). Route-parameter capture (`httpio.WithParams`) is
mounted by the router itself. `Hooks` is a struct, never a map, so an outlet added or
removed surfaces as a compile error at every hook that names it:

```go
type Hooks struct {
	// Outermost runs after tracing and ahead of the request logger on every request.
	Outermost []func(http.Handler) http.Handler
	// Root registers routes outside every outlet: health checks, webhooks, scheduler
	// triggers. They sit behind the every-request chain and nothing else.
	Root func(r chi.Router)
	// One field per outlet, named after it. The hook runs inside the outlet's
	// authenticated group (or the machine outlet's token-checked group): r already sits behind the outlet's
	// guards, and generated registers the outlet's generated routes, behind the
	// read-only mask the router mounts for them on a session outlet. Nil registers them
	// directly.
	Default func(r chi.Router, generated func(chi.Router))
	Portal  func(r chi.Router, generated func(chi.Router))
	Droids  func(r chi.Router, generated func(chi.Router))
}
```

Hooks compose inward only. A hook adds middleware and routes under the guards it is
handed and chooses where inside its own group the generated routes register; it never
sees the outer router, so no generated route can be lifted out from behind session
validation or the XSRF guard. A hook must call `generated` exactly once; the router
refuses one that does not, at construction. `New(h Handlers, hooks Hooks) *chi.Mux` is
written linear and inline, the way a hand router reads: tracing, `hooks.Outermost`, the
request logger, the security headers, and parameter capture; `hooks.Root`; one group per
outlet, top to bottom; a not-found handler per outlet prefix, so an unknown API path is
404 and never a browser application's entry document; then the web apps, longer mount
paths first, so an application at `"/"` is the catch-all; and, with several web apps, the
root redirect.

A session outlet's group is `resource.BindAuth(<pkg>.Name)` (with two session auths; the
router's own, so a misspelled or removed auth package is a compile error there), `NoCaching`,
`CompressionMiddleware`, `StartSession`, `SetXSRFToken`; the session routes under the
prefix; then the authenticated sub-group, `ValidateSession` and `ValidateXSRFToken`
around the hook and the generated routes. In that sub-group the routes a signed-in
person calls on their own account (an invitation's acceptance, the address they add and
verify, their tokens and device authorizations) stand in front of the hook: what a hook
asks of a request, a tenant or a role say, gates the application's routes and never a
person's own account. What must hold for every route the router registers there it
mounts itself, wherever the hook registers the generated routes and whatever middleware
it adds: the read-only mask of a session that views as
someone else (`EnforceReadOnlyMask`) in front of the account routes and, behind the
hook, in front of the generated routes, and each account route that writes refuses an
impersonated session in its handler. A route the hook registers by hand carries what
the hook gives it: an application that views as people mounts the mask on its own
writes, and its route that ends a view in front of it. The session routes, every one under
`<prefix>/user/`: `GET user/methods`, what the login page is told (`session.Auth.Methods`,
handed the declared methods at the paths below, so the page and the router read one
declaration); `GET user/session` and `DELETE user/session`; `GET user/pending`; and per
external method under its registration name, `GET user/<name>/login` and `GET
user/<name>/callback` (`GET user/<name>/logout` too for Azure, the directory's
front-channel logout). The sign-in's POSTs sit behind the outlet's XSRF guard alone, as
the session library wires them: `POST user/password/login`, `POST user/password/change`,
`POST user/password/remove`, `POST user/password/strength`, `POST user/pending/cancel`,
`POST user/pending/code` (mails the sign-in code to a sign-in that waits for the second
factor), `POST user/pending/code/verify` and `POST user/pending/email` (the address a person who holds
none gives for the code); `POST user/email/confirm` (the link an administrator's change
of a person's address mails to the new address, confirmed by a person who need not be
signed in); and, for an Auth with a password method, `POST user/pending/confirm` (a
sign-in that matched an account with a password, finished with that password), `POST
user/password-reset` (a person's own request for a reset link), `POST
user/password-reset/open` and `POST user/password-reset/complete` (the mailed link
opened, and completed with the new password). An Auth with no password method gives
nobody a password, so its outlet serves no `user/pending/confirm`, names none in what
the login page is told, and its session surface needs no `PendingConfirmWithPassword`. A browser reaches them with the XSRF cookie its first request
was issued, echoed in the header. The methods route names the reset paths
(`passwordReset`), the request's only while an administrator lets people ask for a link
themselves (section 21, "Who may sign up"), so the login page offers "Forgot your
password?" from the list it already reads. The password change
(`PasswordChange`, the password method's `ChangeUserPassword`) validates the session
itself, so it sits outside `ValidateSession`: a person an administrator required a new
password of, who chooses it before anything else is served, reaches it, and the methods route tells the login
page where (`changePassword`), which renders the change form before the signed-in page
(section 21). The removal of a person's own password (`PasswordRemove`, the password
method's `RemoveUserPassword`) is mounted beside it the same way, and the methods route
names it as `removePassword`: where the person's account page posts the password as
proof. The password strength question (`PasswordStrength`, the password method's
`Strength`) reads no session and is mounted beside them; the methods route names it as
`passwordStrength`, where every page that sets a password asks while the person types.

With two session auths the router binds every request of an outlet to the outlet's auth,
and `resource.SessionPermissions` answers a bound request only from that auth's permission
engine. The checkers it is handed say which auth their engine was opened for (`Auth()`);
where that is another auth, or none, every check, digest and list of the request answers an
error that names both, so the request fails with a 500 and the log says which engine was
asked. The application chooses the engine for a request by `resource.BoundAuth(ctx)`. A
surface written by hand binds its own requests the same way, with
`resource.BindAuth(<pkg>.Name)` in front of its routes. The bind is the one record of a
request's auth: `resource.AuthFrom` answers it on every route of the group, a route
registered by hand in the outlet's hook among them, and nothing under the group changes it.
A call of `resource.WithAuth` leaves a request a group bound as it is. A second
`resource.BindAuth` that names another auth fails the request with a 500, and so do an
outlet's generated routes (`live.Subscribing`) mounted under a group of another auth; the
logged cause names both auths.

**Machine outlets.** A machine outlet declares the auth whose `Auth` issues its tokens,
`Tokens(importPath)`, the same `*session.Auth` a session outlet of the program binds to
through `Auth`; its people issue the tokens there. The outlet's group is `resource.BindAuth`
(when several auths are declared), `NoCaching`, `CompressionMiddleware`,
`ReleaseHeader`, the device flow's two public endpoints (`POST <prefix>/oauth/device/authorize`
and `POST <prefix>/oauth/token`, RFC 8628 and the refresh grant, served by the issuing
auth's `DeviceAuthorize` and `DeviceToken`), then the token check,
`<Outlet>Tokens().ValidateToken`, the hook and the routes: no session handling and no
XSRF guard. The check requires `Authorization: Bearer <token>`, answers a request without
a live token of the outlet's with 401 and `WWW-Authenticate: Bearer realm="<outlet>"`,
and binds the request to the person the token acts for, so the handlers behind it run
the same permission checks as the browser's. The issuing auth's session outlet gains the
token routes behind its session and XSRF guards, `GET` and `POST user/tokens`, `POST
user/tokens/revoke`, `GET user/device`, `POST user/device/approve` and `POST
user/device/deny`: a signed-in person issues, lists and revokes their own application
tokens (the secret is in the answer once) and approves or denies a command-line client's
device authorization by the code it shows. The console carries those two pages from the
client library: the token page, where the person issues, lists and revokes their tokens,
which the library's frame serves in every place (Your API keys in the person's menu,
`<mount>/api-keys`), and a device page at `<mount>/device`, where the link a program
prints (`/console/device?userCode=XXXX-XXXX`) lands for approval; the issuing auth
names the device page as its device verification path
(`session.WithDeviceVerificationURL("/console/device")`), which the device authorization
tells the client. The issuing outlet's generated descriptor carries a `tokens` block the
pages configure themselves from, the routes above in the form of `userDomainsRoute`
and the machine outlets the auth issues for, by name and mount path
(`outlets: [{ name: 'droids', prefix: '/droids' }]`); an outlet whose auth issues none,
and one that only manages the issuing auth, carry no block. The chain comment names the
issuing auth per machine outlet and the generated test proves the check: no token and a
token the auth never issued are challenged, a token opens the routes, and the device
endpoints sit in front of the check. The application writes nothing for the check: the
auth package registers the outlet on its `Auth` before the first request
(`session.RegisterMachineOutlet(auth, "droids")`, beside its sign-in methods) and the
`Handlers` getter returns it.

**Shipped programs.** A machine outlet serves the programs the release ships to their
installed copies, which fetch their next release there with the token they already carry:
`generation.ShipsProgram("cmd/corral", generation.Platforms("linux/amd64"))` beside
`Tokens`, naming the program's main package by its root-relative directory (its name is
the directory's base) and the platforms, each `<os>/<arch>`. The release file lists the
programs on the outlet's entry (`"programs": [{"name": "corral", "dir": "cmd/corral",
"platforms": ["linux/amd64"]}]`), from which the version build compiles each for its
platforms and copies the files into the image beside the server, one per platform named
`corral-linux-amd64`, in the directory `APP_PROGRAMS` names. The generated router mounts
the handler of `resource/shipped` behind the token check at `GET
<prefix>/programs/<name>/<release>/<os>/<arch>`, which answers the file with its SHA-256
in `X-Program-Sha256`, or 404 with one sentence saying why: the program or platform is not
shipped, the release is not the server's (naming which it is, so a copy asks again for the
release it read from `X-Api-Version`), the image carries no programs, or the file is
missing. A copy follows the release every answer names through `shipped.Follower`: its
`Transport` wraps the program's transport and keeps the newest release seen, `Newer` says
when it is newer than the copy's own (`shipped.OwnRelease`: the release stamped at link
time, else the module version a `go install` copy carries; a development copy never
upgrades), and `Upgrade` fetches the file with the copy's token, writes it beside the
running executable, verifies the checksum, keeps the running file as `.old`, renames the
new file over the running path and returns the release; the program logs it and exits 0
for its service manager to start the new release. A session outlet declaring the option
is refused, and the generated test proves the route sits behind the token check.

**The auth in the route tables.** Each session outlet's routes function binds the name of
its auth to every request, for the live pages (section 14) and for the generated
management's rules on a person acting on themselves: `generatedRoutes` opens with
`live.Subscribing(crew.Name)`, so the routes file imports every declared auth package,
whether one auth is declared or several, and `NewTestRouter` binds the same names. Where
the application writes its own router, the generator does not know the auths, so each
session outlet's routes function takes the name as its last parameter,
`generatedRoutes(r, h, staff.Name)`, and `NewTestRouter` takes one per session outlet in
outlet order, `NewTestRouter(h, staff.Name)`; a router that passes none does not
compile. A machine outlet binds none and registers no live routes: its resource
routes refuse a request carrying `X-Subscribe`, and so do the default outlet's when it
is declared `Tokens(...)`.

**Release versions.** A browser application and the server each carry the release they
were built from, and the server refuses an application it no longer answers. The
application sends its release in `X-Api-Version` with every request. The server's is
`Handlers.ServerVersion()`, which the application answers from its configuration's
`APP_VERSION` (`dev` where no release built it), and the generator program declares how
far back a session outlet answers with `generation.OldestAnswered("1.5.0")` on
`GenerateRoutes` or `WithRouterOutlet`, or `generation.OldestAnswered(generation.ThisRelease)`
for a release that changes the API inside a maintenance window, when only the server's
own release is answered. Without the option every release that sends the header is
answered. The check (`resource.CheckAPIVersion`) sits behind the outlet's guards and
ahead of its hook, so it covers the generated routes and the hook's own: a request whose
release is between the oldest answered and the server's, inclusive, is answered; any
other is refused with 412 and `X-Api-Version: <server release>` before any handler runs
and before its body is read. A request without the header is answered, so file links in
`<img>` and `<a>`, navigations, curl, and bundles built before the header shipped keep
working, and a version that is not a release on either side (`dev`, or anything that is
not a semantic version) never refuses and is never refused. The session routes (login,
callback, `/user/session`) sit outside the check, and the stored-file routes (`@file`)
are exempt, so a login always works and a file link never fails on a version. A machine
outlet is not checked, and refuses the option. Every response on a checked route carries
`Vary: X-Api-Version`, so a browser never serves one release's cached answer to another.
Every answer of an outlet, checked or not, names the server's release in `X-Api-Version`
(`resource.ReleaseHeader`, ahead of the session handling on every outlet, the machine and
scheduled outlets included), so the methods list a login page reads before any sign-in
carries it, a browser application shows which release it talks to, and `curl` shows it; a
`dev` server is named as it is. Versions compare as semantic versions, with or without a
leading `v`. The chain comment
names the check per outlet with its oldest answered release, and the generated test
proves it: in range answered, below or above refused with the header and no handler run
and no body read, no header and `dev` answered, session and file routes answered at any
release, the machine outlets unchecked, and the refusal landing before the hook.

**The environment's name.** Beside its release a server names the environment it runs in,
wherever that is not production, so a browser application can show it and a problem
report can say where it happened. The name is `Handlers.ServerEnvironment()`, which the
application answers from its configuration's `APP_ENVIRONMENT`: a deployment sets the
variable to the environment's name (`tst`, `development`) everywhere but production,
which leaves it unset. The generated router names it on every answer in `X-Environment`
(`resource.NameEnvironment`, in front of every route, last in the chain every request
passes), percent-encoded as a path segment is, so a name with a space or a letter outside
ASCII arrives whole; a server that reports no name sends no such header, which is how
production reads. The browser's client keeps the name from any answer
(`client.serverEnvironment`) and the library's About pop-up shows it and copies it, with
the application's name and the release, as one line for a report. The generated test
proves both halves: a server told its environment names it on an answer, the answer for
an address no route serves included, and a server told none names none.

**The release file.** Beside the router the generator writes `zz_gen_release.json`, which
names every outlet and, for each session outlet, the oldest release it answers, so a
deploy tells from the checkout alone, without building or running the server, whether a
release needs a maintenance window. It is written only when a router is generated, with
the other generated files, and the sweep removes it when the router goes. One entry per
outlet, keyed by the outlet's name (`default` for `GenerateRoutes`, the name given to
`WithRouterOutlet` otherwise): a session outlet carries `oldestAnswered`, the release as
declared (`"1.5.0"`), `"this"` for `generation.ThisRelease`, or `""` when the option is
absent and every release that sends the header is answered; a machine outlet carries
`"kind": "api-key"` (the machine kind's name in the file) and no `oldestAnswered`, since
it is never checked. Lodestar's, with
no release declared on either session outlet:

```json
{
  "outlets": {
    "default": {
      "oldestAnswered": ""
    },
    "droids": {
      "kind": "api-key"
    },
    "portal": {
      "oldestAnswered": ""
    }
  }
}
```

`resource.ReadReleaseFile(dir)` reads it from the router package's directory into a
`resource.ReleaseFile` (`Outlets` by name, each a `ReleaseOutlet` with `APIKey` and
`OldestAnswered`, `"this"` read back as `resource.ThisRelease`), refusing a file that is
not this shape, so the deploy and the tests share one parser; the name is
`resource.ReleaseFileName`. An application with scheduled methods (section 19) also finds
them listed there, under `scheduled`, in path order: each route's `path`, its cron
`schedule` and its `timeZone`, read into `ReleaseFile.Scheduled`; the key is absent when
there are none. An application that declares request log words or trace settings (below,
request logs and traces) also finds its surfaces listed there, under `surfaces`, in prefix
order: each surface's `prefix` (`/` for the application default, an outlet's prefix, a
route's path, or a prefix mounted by hand), its `kind` (`prefix` for the application
default, an outlet and a prefix mounted by hand, matched against the start of the path;
`route` for an annotated method's path, matched to its end; a file written before the
kind existed reads as prefixes), its `log` word (`always`, `onEvent`, `sampled` with its
`fraction`, `never`) and its `traces` setting (`followFrontEnd`, `capped` with its `rate`,
`off`), the words and the setting each present only where declared, read into
`ReleaseFile.Surfaces`; bedrock renders the stack's own log exclusion from them, a route
matched to the end of its path and a prefix by prefix.

**The generated test** drives every generated route through `New` with recording stubs
and asserts the middleware each request passed through, in order, for its outlet; that
each flavor's session routes answer behind the group and before the guards; that under
every prefix an unknown path is 404 and nothing under one outlet's prefix reaches another
outlet's group; that each browser application answers at its mount path through its
deep-link rewrite; with several applications, that the root alone redirects to the
default outlet's and an unmatched path is 404; and that the hooks sit where the chain
comment says. The chain comment is proven, not stated. `NewTestRouter` and the route tests are unchanged, and the
application's own hooks are the application's suite's to exercise.

**Adopting it.** Lodestar keeps one hand file in its router package, `hooks.go`: an
`AppHandlers` interface (the generated `Handlers` plus its own routes) and an
`AppHooks(h AppHandlers) Hooks` function, and `main` calls `router.New(app,
router.AppHooks(app))`. An application whose router is exactly the base passes
`router.Hooks{}`. The escape hatch stays: an application that needs something the shape
cannot carry removes the option and hand-writes its router on the generated route
tables, losing only the boilerplate and the generated chain test, and passes each
session outlet's auth name to its routes function.

**The generated authorization matrix.** `GenerateHandlerTests(dir)` emits, beside the
emulator bootstrap, a test that drives every generated route through `NewTestRouter`:
without the required permission each must fail closed, and with exactly that permission a
list or read must reach data access. The application hand-writes one function,
`newTestHandler`, wiring its App to the scripted grants; Lodestar's is
[test/authz/harness_test.go](../lodestar/test/authz/harness_test.go). For domain-scoped
routes `newTestHandler` adds the suite's tenant, the generated router's `TestDomain`
constant (an id's text, as a tenant's key is one), to the test application's tenant
roster with `Add`, since the empty schema holds no tenant row; under
concealed domains a case with no grants has no foothold and is answered as if the domain
did not exist. The scripted grants
are unconditional and the schema is empty, so the matrix pins the endpoint gate alone: a
granted case means the request passed the gate, never that a row is visible, and a
conditional grant would pass the gate exactly as an unconditional one does. What a
condition admits or refuses is proven by the application's integration suites over seeded
rows and the real permission engine provisioned from its role files, as Lodestar's
[test/integration](../lodestar/test/integration) suites do.

### Request body limits

Every request body the generated router serves is bounded, in one place, at a limit the
application sets once. `generation.WithBodyLimit(bytes)` sets it; an application that
sets none gets 4 MiB. The routes file declares it as the router package's `BodyLimit`,
and a body over its limit answers 413 naming the limit, before the handler reads past it.

Two groups carry it. The resource routes, the consolidated patch route, the session
routes, the scheduled routes and the other generated JSON routes register on a group the
router wraps in `resource.BodyLimit(BodyLimit)`. The upload, live and RPC routes register
beside that group, not inside it, and bound their own bodies where they read them: an
upload at its `@upload(max:)`, a live route at its own constant, and an RPC method at its
`@rpc(max:)` or, when it declares none, at `BodyLimit`, which its generated handler
applies before decoding. They sit beside the group rather than inside it because a
nested `http.MaxBytesReader` can only tighten the limit outside it: an upload inside the
default group would be cut off at 4 MiB however large its declaration. The generated
router test proves the split, sending every route a body one byte over `BodyLimit` and
expecting the read refused on the bounded routes and intact on the self-bounded ones, so
a hook that wrapped the body above the generated routes fails the test on the next
generate.

The application's own routes under `hooks.Root` take the limit too; a route an outlet
hook registers runs beside the generated routes with no limit of its own, and applies
`resource.BodyLimit` itself if it reads a body. Lodestar sets 2 MiB
([generator.go](../lodestar/cmd/generate/generator.go)) and declares a 64 KB limit on
[IssueBulletin](../lodestar/pkg/rpc/issue_bulletin.go).

### Request logs and traces

An application declares, per surface, what a request writes to the log and how its spans
are sampled, and the generated router applies the declarations as a request descends. An
application that declares nothing keeps today's behavior: every request writes its entry,
and its spans follow the front end.

**The words.** A request log word says when the request's entry is written, decided by
the root's request logger when the request ends: `generation.LogAlways()` writes every
entry; `generation.LogOnEvent()` writes it when any line attached or the status is 400 or
above; `generation.LogSampled(fraction)` is on event plus a per-request draw at the
fraction, between 0 and 1; `generation.LogNever()` writes nothing. Each takes
`.MinSeverity(logging.Warning)` to drop the lines below a floor before they attach. A
trace setting says how the request's spans are sampled, applied by the tracer as the span
starts, since a span's sampled flag is fixed then: `generation.TracesFollowFrontEnd()` is
today's behavior, the front end's decision carried in the trace header;
`generation.TracesCapped(rate)` is that and a cap at the rate, decided by the trace ID so
one trace is kept or dropped the same way wherever it is capped at the rate;
`generation.TracesOff()` records nothing. On an annotation the words are `log: always`, `log: onEvent`,
`log: sampled` with `fraction: 0.01`, `log: never`, and the settings `trace: followFrontEnd`,
`trace: capped` with `rate: 0.1`, `trace: off`.

**Where they are declared.** `generation.WithRequestLog(word)` sets the application
default, the word every request starts with. `generation.OutletRequestLog(word)` and
`generation.OutletTraces(setting)` are outlet options on `GenerateRoutes` and
`WithRouterOutlet`: the outlet's word is set on its group, and on its not-found handler, so
an unknown path under the prefix is logged as its requests are. `@rpc(log: onEvent)`,
`@rpc(trace: off)`, the same two on `@file`, and `@schedule(log: onEvent)` are a route's
own, set ahead of its handler in the routes file with `logger.WithPolicy`. A prefix the
application mounts by hand, through `hooks.Root` or an outlet's hook, is declared with
`generation.WithMountedRoutes(prefix, word, setting)`, so the router, the tracer and the
release file learn it from one place; a prefix at or under `/_scheduled`, or equal to an
outlet's, is refused, since those are the router's own. The nearest declaration wins: a
route's over its outlet's, an outlet's over the application default, and a prefix mounted
by hand beneath an outlet keeps its own word under the outlet's. A handler may change its
own request at run time through `logger.FromReq(r).SetPolicy(word)`. Every one of these
needs `GenerateRouter`, which builds the request logger and the tracer with them; a
hand-written router passes `logger.DefaultPolicy` and `logger.PolicyByPrefix` to
`logger.NewRequestLogger` itself.

**What the router renders.** The root's request logger takes `logger.DefaultPolicy` with
the application default and, with any prefix mounted by hand, `logger.PolicyByPrefix` with
the prefixes, longest first; the tracer's handler takes `tracer.Surfaces` with every
surface that declares a trace setting, by the pattern it is mounted at, where `{segment}`
matches one path segment and the longest pattern wins; each outlet group that declares a
word starts with `logger.WithPolicy`, and with prefixes mounted by hand beneath it with
a generated `outletRequestLog` that excepts them. The chain comment names the word and the
setting at each place, and the generated router test proves them with the logger's
console exporter deciding each request's entry: the application default on a root route,
quiet and failed; an outlet's word inside its group, and an outlet without one taking the
default; a route's own word on the generated route; a prefix mounted by hand keeping its
word; and a handler setting its own request's word. The release file lists every declared
surface under `surfaces` (above). Lodestar declares one quiet surface, the beacon pulses
under `/beacons/` on event with traces off, and `IngestDroidReports` on event
([generator.go](../lodestar/cmd/generate/generator.go),
[ingest_droid_reports.go](../lodestar/pkg/rpc/ingest_droid_reports.go)), and
[request_log_test.go](../lodestar/test/integration/request_log_test.go) reads the console
exporter's decisions on the served stack.

## 9. Indexes for the shapes the package injects

Tenancy, grant conditions, subject sets and values, the visible projection, and the write
checks all land inside the application's queries. Measured on a real Spanner instance at
half a million rows ([lodestar/perf/REPORT.md](../lodestar/perf/REPORT.md)), they need this
much from the schema:

- A resource with a bare `@domain` column that is listed at volume wants an index on the
  tenant key followed by its `@order` columns. Spanner's foreign-key backing index on the
  tenant column alone drives the list, but every page then sorts the whole partition; the
  composite index reads the page's rows and nothing else (52 rows against 20,060 at
  10,000 rows per tenant). Generation warns at every run for a listed resource in this
  shape whose table has no index leading with the tenant column and then the `@order`
  columns, in order and direction, and names the `CREATE INDEX` it wants; the primary
  key is not named because Spanner appends it to every secondary index. A resource
  with no `@order` is silent: its sort-less list is not sorted, and the backing index on
  the tenant column serves it. So is a resource whose table a unique index on the tenant
  column alone holds to one row for a tenant (a tenant's directory, for one): one row
  has nothing to sort.
- A nullable `@order` column renders as the plain direction with no stated `NULL`
  placement, so the same composite index serves it. Each database keeps its own placement
  (Spanner sorts `NULL` first ascending and last descending, PostgreSQL the reverse) and
  the keyset cursor follows it across the `NULL` boundary; a cursor is read by the database
  that issued it.
- A filter seeks the same way. A filter on a column that leads an index, or that follows
  the tenant column in one on a bare `@domain` resource, reads the index from that
  prefix, and those are the columns the generated list struct tags `index:"true"`
  (section 3); a filter on any other column alone is refused at decode rather than
  scanned, and `allow_filter` admits one beside an indexed field. A global request
  carries no partition predicate, so for it the tenant-second seek is an index scan,
  the same cost class as its ordered list, which already sorts the whole table.
- A resource with join-path tenancy, `@domain(via: ...)`, is scanned whole when listed:
  every row of the table, all tenants, with a lookup per row up the path. No index on the
  child changes that, because no column on the child names the tenant. Join paths suit
  writes and small or parent-scoped tables; a table listed at volume carries its tenant key
  on the row. Generation warns at every run for every listed join-path resource, so the
  application decides knowingly. The warnings are informational, never a refusal, and the
  generator returns them through `Generator.Warnings()`; an application's generate program
  prints them after a successful run, one per line as `Warning: <text>`, as Lodestar's
  does. The same program, run with `-audit`, prints the audit pass's findings after them
  as `Audit: <text>` (section 13): advisory findings a normal generation never prints.
- A list across tenants (`@listAcrossTenants`, section 4) is not served by the index the
  tenant's own list wants. That index leads with the tenant column, and a list over
  several tenants has no one tenant to seek to: its statement filters the tenant column
  against a bound array and orders by the `@order` columns across every tenant of the
  set. It wants an index of its own that leads with the `@order` columns, in order and
  direction, and carries the tenant column, later in its key or in `STORING`, so the
  page is read in order and the filter on the tenant is answered from the index before
  a row is read; without it every page reads and sorts the rows of every tenant the
  caller reaches. Generation warns at every run for a declared table-backed resource
  with a bare `@domain` column and an `@order` whose table has no such index, naming
  the `CREATE INDEX ... STORING (<tenant column>)` it wants; a tenant column in the
  primary key rides every index and needs no `STORING`. A declared resource with no
  `@order` lists by primary key, which the table serves, and is silent; one bound
  through a join path carries the join-path warning above. This shape is reasoned from
  the statement and is not in the measured study the other bullets cite: the measure
  at volume is still to be taken. A set of one tenant is the tenant's own list and
  uses the tenant's index.
- A search (section 4) reads the table's one search index: `CREATE SEARCH INDEX
  ClientsSearch ON Clients(Name_Search, ContactEmail_Search)`, over hidden `TOKENLIST`
  columns generated by `TOKENIZE_SUBSTRING`. A tenant-scoped resource partitions it,
  `PARTITION BY SectorId`, so a search reads one tenant's tokens; generation refuses a
  partition by any other column and warns at every run for a tenant-scoped resource whose
  search index is unpartitioned. A `@searchAlso` child's index stores the foreign key the
  branch joins through, `STORING (ClientId)`, or generation warns that every matching child
  row is read back for its key. A case-folded `startswith` alone needs no search index: a
  stored `LOWER(Name)` column second in the tenant's index, `CREATE INDEX
  HangarsBySectorIdNameLower ON Hangars(SectorId, Name_Lower)`, serves it as a seek. A box
  over several indexes runs one key set per term, each a search-index read, and the box's
  limits (8 terms, 64 characters each, 200 in all) bound the statement.
- Subject-set conditions are key lookups per row against the anchor (its key leads with
  the compared column and the user), so their cost is the partition's size per page, not
  the anchor's. Subject values are one row through the unique index generation requires.
  On a computed resource each fact a condition names is read once per request and the
  rows are judged in memory. A subject set's read selects the caller's anchor rows by
  the user column (in the request's tenant where the anchor is partitioned), so it is a
  seek where the anchor's key or an index leads with the user and a scan of the anchor,
  once per request, where none does: an anchor a computed resource's conditions read at
  volume carries an index by user.
- A subject set's lookup wants an index on the set's table that leads with the tenant
  column (where the row carries one), the column that ties the row to the requester, and
  the value the set yields, in any order: the lookup binds each by equality. A set whose
  `who:` names several columns is one lookup for each, so it wants one index for each.
  Generation warns at every run for each column no index serves, naming the statement
  (`SubjectSetIndexWarning`: `CREATE INDEX HangarPassesBySectorIdSquadronIdHangarId ON
  HangarPasses(SectorId, SquadronId, HangarId)`). The primary key counts, and so does a
  secondary index with the key columns Spanner appends to it; a null-filtered index and
  a search index do not. It is a warning as the tenant list's is, accepted the way the
  others are: a small table needs no index. A `where:` adds no lookup of its own; it
  filters the rows the lookup finds.
- A sort on a conditionally visible column runs over `CASE WHEN <condition> THEN column
  END`, which no index serves: the page sorts the partition. The `CASE` is dropped when
  the field's condition covers the whole row predicate (a role whose every grant on the
  resource carries the one condition, or a condition that implies it: an equality inside
  the field's `IN` list prunes too), and a field declared `masking:"positional"` never
  renders one; `access.ValidateRoles` and `access.Client.CheckPolicy` warn, per role, where a concealing sort or filter
  key keeps its `CASE`. Lists that page at volume sort on unconditionally visible
  columns, on positional ones, or on columns whose condition the row filter proves.
- Write and insert checks are point lookups at any volume.
- After a bulk load, `ANALYZE`; Spanner otherwise refreshes statistics about every three
  days, and a plan can flip with them.

## 10. Refused commits

Spanner checks foreign keys, interleaved parents, `NOT NULL`, column lengths, primary and
unique keys, and `CHECK` constraints for buffered mutations when the transaction commits,
so a write the schema refuses surfaces from the commit, not from the patch that caused it.
`SpannerClient.ExecuteFunc` answers such a commit with a 4xx and a message the library
composes itself. The translation keys on the gRPC code alone, never on the message text,
which differs between the emulator and the service and is not documented. The message
says the records whose patches the transaction buffered, in words, and never a resource's
or a table's code name, the referencing table, the constraint, the index, the key, the
value, or a count. One buffered patch is said by its record's struct name in words ("This
hangar"; "This record" where the patch gives no name), several by their resources' names
in words, each once, in the order they were buffered:

| gRPC code | The transaction buffered | Status | Message |
| --- | --- | --- | --- |
| `FailedPrecondition` (a delete of a row other rows still reference through a foreign key, or as an interleaved parent; a write whose foreign key names a row that does not exist; a required column left empty; a string longer than its column's declared length) | One delete | 409 | `This hangar cannot be deleted while other records still refer to it.` |
| | Several deletes | 409 | `One of the hangars or ships this request deletes cannot be deleted while other records still refer to it.` |
| | One create or update | 409 | `This ship refers to a record that does not exist, or holds a value that is too long for its field.` |
| | Several creates or updates | 409 | `One of the ships or missions this request adds or changes refers to a record that does not exist, or holds a value that is too long for its field.` |
| | Deletes and writes together, or nothing buffered through the transaction wrapper | 409 | `The request could not be applied: a deleted record is still referenced, or a referenced record does not exist.` |
| `AlreadyExists` (a duplicate primary key; a duplicate unique-index value) | Any create, one write | 409 | `This ship already exists, or it has a value that must be unique and another record already has it.` |
| | Any create, several writes | 409 | `One of the clients or ships this request adds or changes already exists, or it has a value that must be unique and another record already has it.` |
| | Updates only, one | 409 | `This client has a value that must be unique, and another record already has it.` |
| | Updates only, several | 409 | `One of the clients or ships this request changes has a value that must be unique, and another record already has it.` |
| | Nothing buffered | 409 | `The request could not be applied: a record with this key or a unique value already exists.` |
| `OutOfRange` (a violated `CHECK` constraint) | One write | 400 | `This mission has a value that is outside the range allowed.` |
| | Several writes | 400 | `One of the ships or missions this request adds or changes has a value that is outside the range allowed.` |
| | Nothing buffered | 400 | `The request could not be applied: a value is outside the range the record allows.` |
| `NotFound` (an update of a row that does not exist; an interleaved child whose parent does not exist) | One update | 404 | `This client does not exist.` |
| | Several updates | 404 | `One of the clients or pilots this request changes does not exist.` |
| | Any create, one write | 409 | `This refit task refers to a record that does not exist.` |
| | Any create, several writes | 409 | `One of the refits or refit tasks this request adds or changes refers to a record that does not exist.` |
| | Nothing buffered | 409 | `The request could not be applied: a record to update does not exist, or a referenced record does not exist.` |
| anything else (`InvalidArgument`, `Internal`, `Aborted`, …) | | 500 | passes through unchanged |

"Create" is a create or a create-or-update; a touch is an update. Several writes are "this
request adds or changes" where a create is among them and "this request changes" where
every one is an update; one or several is counted over the patches, so two deletes of one
resource are several. Deletes cannot cause any code but `FailedPrecondition`, so deletes
buffered beside writes do not change the other codes' sentences. A tracked resource's
change-event rows do not count among the buffered patches. An error the transaction
function itself returns passes through unchanged, whatever its code. The 404 is said as
every record that is not there is said: where one update was buffered, the record by its
struct's name in words ("this record" for a patch that is not a `PatchSet`); where several
were, the resources by their names in words.

### A delete with rows under the record

A delete of a record that has rows under it is answered before the commit, in a sentence
that says what is in the way. For every record the generator writes the kinds with a row
directly under it on a reference that says nothing of a delete (`DeleteTree.Under`, in the
record's generated `DeleteTree()`): the database removes none of those rows with the
record, so a commit that leaves one refuses the delete. Once the transaction's function
has returned and before the commit, `SpannerClient.ExecuteFunc` counts the rows of each
such kind under each record a patch deleted, one count per kind, inside the same
transaction, and answers the first delete that has one with a 409:

| Under the record | Message |
| --- | --- |
| Rows of one kind | `This ship has 2 sorties. Delete them first.` |
| One row | `This ship has 1 sortie. Delete it first.` |
| Rows of several kinds | `This ship has 9 droid reports and 2 sorties. Delete them first.` |
| Rows that can be taken from under the record | `This squadron has 8 missions. Move or delete them first.` |
| One such row | `This squadron has 1 mission. Move or delete it first.` |
| A row of a kind the record lists under `refusedBy` (`@deleteCascade`) | `This client has 10 missions and 5 consignments, so it can't be deleted.` |
| Rows of kinds the person may list none of | `This ship has records under it that you can't see, so it can't be deleted.` |

- The sentence names the record by its kind, in the words of its resource
  (`DeleteTree.RowName`, the one place a row's name comes from).
- "Move or delete" is said where every kind the sentence names can be taken from under
  the record without being deleted: each of the kind's references to the record has a
  column that may be empty and is no part of the row's key (`DeleteLink.MayBeEmpty`,
  which the generator writes from the schema). A mission's assigned squadron may be
  empty, so the missions can be given to another squadron or to none; a sortie always
  names a ship. A kind with one reference that may not be empty, or one such kind among
  those named, and the sentence says only to delete them.
- A kind the record lists under `refusedBy` answers before any other, counted by every
  way it leads up to the record, and keeps the record from being deleted at all. The
  kinds a record lists as going with it are not in the way: its delete takes them, once
  it has asked (the next part).
- The sentence is written for the person the delete is for: the one the patch is armed
  against, else the caller the request carries. A kind they may not list is never named
  or counted for them, and a grant to list it that carries a condition is not enough,
  since the count is of every row. A trusted call is told every kind. A kind of a
  tenant's rows under a record whose key holds its tenant, the tenant record above all,
  is asked about in that tenant, whatever scope the delete is asked in (section 16,
  "Deleting a tenant").
- A row of another tenant is never counted. Where the kind's rows are a tenant's
  (`@domain`) and the record is a tenant's, or is the tenant record, the count is of the
  rows of the record's own tenant (`DeleteKind.Tenant` and `DeleteTree.Tenant`, which the
  generator writes). A row of another tenant that names the record is not under it, and
  its foreign key refuses the commit in the commit's own sentence (the table that opens
  this section).
- A row the transaction's own patches deleted is left out of the count, by its key: a
  read inside the transaction does not see its buffered writes. So the rows and the
  record go together in one transaction, in either order. A row the transaction's
  patches changed is counted as it was before the transaction, so a request that moves
  a row from under a record and deletes the record is refused: the move and the delete
  are sent one after the other. A transaction that deleted more than 200 rows of a kind
  is not counted over that kind.
- The count never refuses a delete the database would take. A kind whose rows the
  database removes with a row of another table (`DeleteKind.RemovedWith`) is not counted
  once the transaction deleted a row of that table, and a kind the database removes with
  the record itself is not in the description. That is so only of a link that still
  carries the database's rule, which no link to an application's record does.

What the count does not reach is still the commit's to refuse, in the sentences of the
table above: a row under a row the database removes with the record, and a row an update
of the same transaction leaves under the record. The count knows the rows the transaction's patches deleted and no others: code that
removes the rows under a record with raw mutations and deletes the record with a patch in
one transaction is refused over rows that are about to go, so those rows are deleted
through their patches. The mock client holds no rows and refuses nothing here, and
PostgreSQL has no write path.

Example: [the served suite](../lodestar/test/integration/delete_refusal_test.go).

### A delete that takes what is under the record

Rows are deleted in code only, never by the database. No foreign key and no interleave of
an application's records says `ON DELETE CASCADE`: a row the database removes is not
checked against the grants of the person who asked, leaves no change record, is not
announced to open pages and keeps its stored files, and it counts toward the commit's
mutation limit all the same. `impulse check` holds the schema to it (`no-delete-cascade`).
What a delete takes with it is declared on the record and deleted by the library, as
below. A record with rows under it and no declaration is refused, in words that say what
to delete first.

A record that declares what goes with it (`@deleteCascade`) is deleted together with the
rows of the kinds it lists as taken, and the library deletes them in code: it finds the
rows, checks each one as that row's own delete would be checked, and deletes them in the
record's transaction, deepest first and the record last.

Such a delete asks first. A delete of a record with rows to take deletes nothing unless
it says it takes them, and is answered 428 (`httpio.HasPreconditionRequired`) with the
question a person is asked before anything goes:

| Under the record | Message |
| --- | --- |
| Rows of kinds the person may list | `Delete this mission and everything under it: 2 sorties, 1 mission document, 1 sortie expense. This can't be undone.` |
| Some rows of kinds they may not list | `Delete this mission and everything under it: 2 sorties, and records you can't see. This can't be undone.` |
| Rows of kinds they may list none of | `Delete this mission and everything under it? This can't be undone.` |

The same delete, saying it takes them, deletes the rows and the record:

- On the wire the remove operation carries a value, `{"op": "remove", "path": "/missions/…",
  "value": {"everythingUnder": true}}`. A value that is not an object, or that says
  `everythingUnder` with anything but true or false, is a 400.
- The TypeScript client takes it as an option, `missions.remove([id], { everythingUnder: true })`
  and `missions.ops.remove([id], { everythingUnder: true })`. The library's delete dialog
  asks the server's question in the place of its own and sends the second delete when the
  person presses Delete again.
- In Go the generated delete patch of a record that declares its cascade, and of the
  tenant record, carries `WithEverythingUnder()` (`PatchSet.DeleteEverythingUnder`).

A record with nothing to take under it is deleted without a question, whatever the delete
says. Before the question is asked, and whatever the delete says, the delete is refused
with nothing gone where it cannot be made whole:

| What stops it | Status | Message |
| --- | --- | --- |
| A row of a kind the record lists under `refusedBy` | 409 | `This client has 10 missions and 5 consignments, so it can't be deleted.` |
| A row under the record that the same request changes | 409 | `This request also changes a record under this mission. Send the change and the delete separately.` |
| More rows than one commit takes | 409 | `This mission has too many records under it to delete at once. Delete some of them first.` |
| A row the person may not delete, of a kind they may list | 403 | `This mission has sortie expenses that you can't delete, so it can't be deleted.` |
| A row the person may not delete, of a kind they may not list | 403 | `This mission has records under it that you can't delete, so it can't be deleted.` |
| A row its delete rule refuses (`@validateDeleteType`) | the rule's | the rule's own sentence |

- The rows are found level by level from the record, by each kind's ways up to it
  (`DeleteKind.Paths`), on the columns each foreign key references (`DeleteLink.References`),
  the other table's key or not: a relay names its call by the call's case number. A kind's
  link to itself (`DeleteKind.Within`: an answer names the relay it answers) is followed
  until a level finds nothing new, so a row under a row of its own kind goes however deep
  it is, and so does one that is under the record through such a row alone.
- A row is under a record of a tenant only where it is that tenant's. For a kind whose
  rows are a tenant's (`@domain`), each level reads the rows of the record's own tenant
  alone: the tenant the record's key holds (the tenant record, or a record keyed by its
  tenant), or the one the record's row resolves to (`DeleteTree.Tenant`), compared with
  the tenant each row of the kind resolves to (`DeleteKind.Tenant`), through other rows
  where its `@domain` goes through them. A row of another tenant that names the record,
  or a row under it, is not found, counted, named or taken, whoever asks, a trusted
  delete included. Its foreign key refuses the commit, in the commit's own sentence, and
  nothing is deleted. Only a foreign key that holds the tenant's key beside the rest (a
  relay's `(SectorId, CaseNumber)` to its call's) keeps such a row from being written; a
  foreign key on the other row's key alone does not. Which of the two a link is written
  with is the schema's choice ("A link between two records of a tenant", below). A
  record that is no tenant's has under it whatever its links put there, and so does a
  kind whose rows are no tenant's.
- A foreign key of two or more columns is written with its columns in the order of the
  key it references: for an order keyed `(Depot, Number)`, a line's is
  `FOREIGN KEY (OrderDepot, OrderNumber) REFERENCES Orders (Depot, Number)`. The generator
  refuses one written in another order, naming the table and the foreign key and printing
  the line to write. The emulator the schema is read through answers a column's position
  in the referenced key with its position in the foreign key, so the other order would
  read with its columns crossed, and what is under a record would be looked for by the
  wrong column. Where the columns referenced are not the other table's key, the order is
  the one the database holds them in, which the refusal prints.
- A row of a kind the record lists under `refusedBy` refuses the delete wherever it is
  under the record. One under a row that is under the record through a row of its own
  kind alone (an invoice for a task under a task) is on none of the kind's ways up, so it
  is looked for under the rows found, each row counted once, and the delete answers in
  the same counted sentence before any row is checked, asked about or deleted.
- Every row goes through its own kind's generated delete (`DeleteKind.Rows`, which the
  generator writes with `resource.NewTakenRows`): the person's Delete grant on the kind
  and the grant's condition against the row, the kind's delete rule, its change record
  where the kind is tracked, and the release of its stored files. One row that may not
  go stops the whole delete.
- The tenant record declares nothing, and `@deleteCascade` on it is refused: the library
  declares what a tenant's delete takes, which is every kind of the application's whose
  rows carry the tenant's key or lead to a row that does. Its rows are found, asked about
  and deleted as any record's are, with one difference: no grant is asked on a row. The
  grant to delete a tenant answers for every row under it, and a person who may delete a
  tenant need hold no role in it. Each kind's delete rule is asked on every row all the
  same, with its change record and the release of its files (section 16, "Deleting a
  tenant").
- The rows are counted against what one commit takes before any is deleted
  (`DeleteTree.Mutations` against `resource.DeleteMutationLimit`, 80,000 mutations): the
  delete of each row, an entry in each index of its table, its change record and its files.
- The rows are checked as the person the record's delete is armed against, and the
  sentences are written for that person, as the count of the part before is. A trusted
  delete (a patch that is not armed) checks no grant, runs each kind's rule, and is told
  every kind; it is asked the question all the same, and takes the rows once it says so.
- A row the same transaction already deleted is not found again, so a request that deletes
  a row under the record beside the record deletes it once.
- A row under the record that the same transaction changed (an update, or an insert or
  update) refuses the delete, whatever the delete says and before any row is checked.
  The reads show the row as it was before the request, so neither where the row is
  after it nor what is under the row can be told, and a row is never taken on a guess.
  The change and the delete are sent one after the other.

The delete in code is Spanner's write path. The mock client holds no rows and takes none,
and PostgreSQL has no write path.

Example: [the served suite](../lodestar/test/integration/delete_cascade_test.go), over
[Mission](../lodestar/pkg/resources/missions.go), [Client](../lodestar/pkg/resources/clients.go)
and [DistressCall](../lodestar/pkg/resources/distress_calls.go).

### A link between two records of a tenant

How one record of a tenant names another is the schema's choice, made record by record and
link by link: the generator and `impulse check` refuse none of the designs below.

A record keeps its tenant in a column of its own (a bare `@domain`) or reads it through the
record it is under (`@domain(via: ...)`). With the column, its foreign keys can hold the
tenant, as below, and its tenant is its own: it stays where it is when the record it is under
is moved to another tenant. Read through the record it is under, it has no tenant column to
put in a foreign key, and it is always in that record's tenant: moved to another tenant, that
record takes it along. Section 9 has what each costs a list, with the generator's warning for
a list read through other rows.

Between two records that each keep their tenant in a column, a foreign key is written in one
of two ways:

| The foreign key | What it prevents | What it allows |
| --- | --- | --- |
| Holds the tenant, the tenant column of the one beside the tenant column of the other: a pass's `FOREIGN KEY (HangarId, SectorId) REFERENCES Hangars (Id, SectorId)` | A row that names another tenant's row. The database refuses every write that would leave one, whoever writes it: a create, an update of the link, a change of either row's tenant. The caller is answered as for a row that names nothing: `This hangar pass refers to a record that does not exist, or holds a value that is too long for its field.` | Links inside one tenant and no other. It is the key for a record that must never be under another tenant's record. |
| Is the other row's id alone: `FOREIGN KEY (HangarId) REFERENCES Hangars (Id)` | A row that names no row, and nothing more. The database does not compare the two rows' tenants, and the library holds the record being written to the request's tenant without looking whose row a foreign key names. | A record under a record of another tenant. The application's own code can move either one to another tenant, and a caller who knows the id of another tenant's row can name it. It is the key for a record that may be moved between tenants. |

A key that holds the tenant has two columns or more, written in the order "A delete that takes
what is under the record" gives above; the generator prints the line for one written in
another order. A child interleaved in its parent is linked by the parent's key, so the link
holds the tenant only where that key does. What becomes of a row that names another tenant's
row is in the same part: no delete counts or takes it for the other tenant, and its foreign
key refuses the delete of the row it names while it stands.

A record that links to a row of one tenant and to a row of another is a fault of the schema's
design. A schema allows one where a record has two links that nothing holds to one tenant: two
foreign keys on an id alone, or, on a record that reads its tenant through other rows, a
foreign key beside the link its tenant is read through, which is the one link the library
proves when the row is written (section 5). No tool looks for such a record. It is found when
the schema is designed and when it is reviewed. `impulse advise` hands an agent a brief to
read the schema for it.

Example: [a pass's key to its hangar and an answer's to its relay](../lodestar/schema/migrations/000001_Baseline.up.sql),
which hold the sector, and [the served test](../lodestar/test/integration/tenancy_test.go) of
both.

### What the translation does not do

What the library does not do: it reads nothing before buffering to name the offending
field. A read inside the transaction does not see the transaction's own buffered writes, so
two creates sharing a value in one batch would pass such a check and fail at commit anyway,
and concurrent requests would race past it; the commit translation is needed regardless.
The application's validators (`@validateCreateType`, `@validateUpdateType`) are where a rule
is answered as 400 naming the field before anything is buffered; the commit translation is
the backstop for what they do not cover. A delete has its rule too (`@validateDeleteType`):
it reads the row inside the transaction and answers 400 in its own sentence before the
delete is buffered. A value the decoder can size never reaches the
commit through the API: a string longer than its `STRING(n)` column, a byte slice longer
than its `BYTES(n)` column, and a decimal beyond `NUMERIC`'s digits answer 400 naming the
field at decode (section 11). The 409 for a too-long string remains for `STRING(MAX)`,
whose ceiling the decoder does not check, and for application code that drives the patch
layer directly.

Two consequences to know:

- A unique index is global across tenants. The 409 for a duplicate unique value confirms
  that the value exists somewhere, in any tenant; that is the schema's choice in declaring
  the index, not the message's, which names no tenant, row, or value.
- `NotFound` is also Spanner's code for an unknown column or table. A deployment whose
  schema disagrees with the generated code answers 404 or 409 with Spanner's text in the
  server log instead of 500. A schema mismatch fails every write that touches the column,
  so it does not hide behind one request.

The tenancy check's 404 for a referenced row outside the request's partition, and the 404
a tenanted or tracked update answers when its row is missing, are unchanged; they run before
the commit, so the same request answers 404 whatever the resource's shape. A request that
deletes children and their parent in one transaction still succeeds in either order where
the parent declares no `@deleteCascade`: its count runs once the transaction's function has
returned and leaves out the rows the request deleted. A record that declares one is judged
as its delete is buffered, so a row that refuses it (`refusedBy`) is deleted ahead of it in
the request.

## 11. Value limits from the schema

A value the column cannot hold answers 400 naming the field when the request is decoded,
before anything is buffered and before any permission check. The generator carries each
sized column's declared type onto the patch request structs as `sqltype:"…"` (section 3),
the runtime derives the rule from it when the handler's Set is constructed, and the
decoder applies it after the per-field refusals (an unknown field, an immutable field on
update, a null into a field whose column is NOT NULL: a value with no null form, or a slice
without the `nullable:"true"` tag) and before the application validator. A limit is
a fact about the wire value alone, the same class as `cannot be null`; naming it before the
permission check discloses schema shape the metadata already publishes, nothing about rows.

The tag is written exactly where the field's Go type and the column type together have a
rule:

| Go type | Column type | Rule |
| --- | --- | --- |
| `string`, a named string type, `*string`, `spanner.NullString` | `STRING(n)` | at most `n` code points (`utf8.RuneCountInString`); Spanner counts code points, so a combining mark counts and a CJK character counts once |
| `[]byte` | `BYTES(n)` | at most `n` bytes |
| a field declared `conditions:"secret"` (`resource.Secret` on the resource, text on the request) | `BYTES(n)` | at most `n − 42` bytes of text, the 42 being what sealing adds (section 23). The rule is the secret's alone: a string-kinded field that is not declared a secret takes no rule from a `BYTES(n)` column (a named string type that encodes itself to bytes is sized by its own encoding, which the decoder does not know), so it carries no tag and is not sized |
| `decimal.Decimal`, `*decimal.Decimal`, `decimal.NullDecimal`, `spanner.NullNumeric` | `NUMERIC` | trailing zeros trimmed, then at most 29 digits before the decimal point and 9 after (GoogleSQL `NUMERIC` is 38 digits of precision with 9 of scale); `1.1234567890` is accepted, `1.1234567891` is not |
| a slice of a checked type, named or not (`[]string`, `type Marks []string`, `[]decimal.Decimal`) | `ARRAY<…>` of the matching type | each element by its rule |

No tag, and no check, on `STRING(MAX)` and `BYTES(MAX)` (Spanner's ceiling of 2,621,440
characters stays a 409 at commit, section 10); on `impulse.UUID` and `impulse.NullUUID`, whose
unmarshal fixes the shape; on `INT64`, `FLOAT64`, `BOOL`, `DATE`, `TIMESTAMP`, and `JSON`
columns; on keys and output-only fields, which the patch wire never carries; on `@virtual`
fields (a view has no schema types; an `@rowsOf` write lands on the table resource, which is
tagged); and on `@computed` and `@rpc` structs, which have no schema. A null in a nullable
wrapper, or in a slice whose column allows NULL, passes: there is nothing to size. Application code that drives `PatchSet.Set`
directly is trusted and keeps the commit backstop.

One message names **every** offending field the request carried, in struct-field order,
joined with `; `, so a form fixes everything in one round trip. Field names are the JSON
names:

- `name is limited to 64 characters`
- `codes: each value is limited to 4 characters`
- `blob is limited to 4 bytes`
- `fee is limited to 29 digits before the decimal point and 9 after`

A `PATCH` checks only the fields it carries. The consolidated handler decodes each
operation through the same function.

The tag follows the Go type and the column alone: a type's name changes nothing, and a
`@typescript` declaration on the type (section 1) changes the interface, never the limit.

The TypeScript field metadata carries the character limit as `maxLength` for the
string-kinded pairs (`STRING(n)`, and per element for `ARRAY<STRING(n)>`) on the fields
whose display type is `string`, `string[]`, or `enumerated` (a picker's key is a string the
column sizes), so a form control refuses before sending; nothing is emitted for bytes or
`NUMERIC`. A field typed by a declaration importing a name (`@typescript(Name, from:
"module")` over a named string or a named slice on a sized column) has display type
`object`: it carries the tag, since the decoder sizes the column's value, and no
`maxLength`, since the interface type is not a string and a form would count the wrong
thing. JavaScript measures a string in UTF-16 units, so a form refuses a little early on
astral characters and never accepts what the server refuses. The framework-neutral client
does not pre-check a string: the server stays the single authority. Example:
[Ship.Registry](../lodestar/pkg/resources/ships.go) on `STRING(16)`, and
[Mission.Fee](../lodestar/pkg/resources/missions.go) on `NUMERIC`.

## 12. TypeScript types for columns

A field's TypeScript type comes from its Go type alone, and a field whose type reaches
no TypeScript type fails generation naming the resource, the field, the Go type, and the
fix. Nothing falls back to `string`. The same rule types a table column, a view column, a
computed field, and an RPC field, so one Go type is one TypeScript type wherever it
appears.

**How a type resolves.** After aliases are read through (`type NullKindID =
impulse.NullEnum[KindID]` is read as the `NullEnum`), and one pointer is read through (a
pointer column is nullable, as before; a pointer to a slice is refused, below), the
generator tries, in order:

1. the built-in table, by the type's qualified name;
2. a generic row, by the type's origin: `impulse.NullEnum[T]` is `T`'s type;
3. a `@typescript` declaration on the type's declaration (section 1);
4. the type a defined type is declared over, when that type writes its own JSON: `type
   Payload json.RawMessage` is JSON by declaration and resolves as `json.RawMessage` does
   (below);
5. a basic type, or a named type over one, by the basic type's row (`type KindID string`
   is `string`);
6. a byte slice: an unnamed `[]byte`, or a named slice over `byte` with no JSON methods,
   is the `bytes` leaf (below).

A field that resolves here is a leaf. A field that does not is read once more as a list:
one slice level is stripped from a `[]T`, an array, or a named slice type (`type
Attachments []Attachment` is `Attachment[]`), and the element resolves by the same steps.
An element that is a struct with no declaration is **derived** (below). Anything else is
refused:

- `Ships.Manifest: resources.Manifest has no TypeScript type; declare the type's
  TypeScript form with @typescript(Name, from: "module") on its declaration, or use a
  struct for a derived interface`

Every offending field in a run is reported together.

**The built-in table.** `string`, `bool`, and the numeric types by their names;
`impulse.UUID` and `impulse.NullUUID` (`uuid`, a `string` in the interface); `decimal.Decimal`,
`decimal.NullDecimal`, and `spanner.NullNumeric` (the `number` display type, a `string` in
the interface; see below); `time.Time` (`Date`); `civil.Date` (`civilDate`, a
`Date` in the interface); the Spanner Null wrappers (`spanner.NullString` is `string`,
`NullInt64`, `NullFloat32`, and `NullFloat64` are `number`, `NullBool` is
`boolean`, `NullTime` is `Date`, `NullDate` is `civilDate`); `securehash.Hash` (`string`,
its text form); and `spanner.NullJSON` and `json.RawMessage` (`unknown`, a value with no
fixed shape, display type `object`; the two share one wire form, any JSON value or
`null`, and neither can carry a `@typescript` declaration). `unknown` passes
`@typescript-eslint/no-explicit-any`, which rejects `any` alone, and the generated files
are lint-ignored in every web application besides. Nullability keeps coming from the
schema, so a nullable `spanner.NullBool` column renders `nullboolean` exactly as `*bool`
does. A `json.RawMessage` is pass-through: a nil one marshals as `null`, and bytes that are
not valid JSON fail `encoding/json` at response time, the application's own
responsibility. Declare a type whenever a TypeScript type exists for the value (below);
`json.RawMessage` is for the value the application does not model.

**A decimal is a string in the interface.** `decimal.Decimal`, `decimal.NullDecimal`, and
`spanner.NullNumeric` each write their JSON as a quoted string (`"fee":"15000"`), which
keeps every digit a floating-point number would round. So a decimal is `string` in every
generated interface, a row's, a write shape's (`MissionsCreate`, `MissionsPatch`), a
method's request and result, a computed row's, and a struct nested in any of them, and a
list of decimals is `string[]`. The metadata keeps the display type `number`, so a form
draws the number control and a table prints the value as it arrives, and it marks the
field `decimal: true` beside the display type: on a column, a computed row's field and a
method's request field, and on a list of decimals. An integer and a float carry no mark.
The library's number control reads it: for a marked field it writes what the person typed
into the form as text, so the string that is sent has every digit typed, and a field with
no mark is written as a number. A page that prints a decimal formats the string, and a
page of the application's own that sends one sends the string: the quoted form is the one
all three types read back.

**`json.RawMessage` on a Spanner column is refused.** The Spanner client types a named
byte slice as `BYTES`, so a read of a `JSON` column through it fails with a type mismatch
at the first row and a write sends `BYTES` to a `JSON` column, and the client's one hook,
the `EncodeSpanner` and `DecodeSpanner` pair, is one a standard-library type cannot take.
A table or view column typed `json.RawMessage`, a pointer to it, or a slice of it fails
generation naming the two spellings that work, and no `@typescript` clause, since none
can apply; a computed field and an RPC field carry the type as `unknown` without
restriction. The refusal is lifted when the client reads and writes the type on `JSON`
columns (googleapis/google-cloud-go#10720, the open request it cites):

- `Shipments.Payload: the Spanner client stores json.RawMessage as BYTES, so a JSON
  column cannot be read or written through it (googleapis/google-cloud-go#10720); type
  the column spanner.NullJSON, or declare a type over json.RawMessage with
  @typescript(...), whose JSON and Spanner methods the generator writes`

**The `database/sql` Null wrappers are refused.** A field typed `sql.NullString`,
`sql.NullInt64`, any of their six siblings, the generic `sql.Null[T]`, or a pointer to
one fails generation on every path (a table or view column, a computed field, an RPC
request or result field). None of them writes its own JSON, so `encoding/json` carries
the wrapper as `{"Int64":7,"Valid":true}` where the interface would promise `number`,
refuses a bare `7` into it, and reads `null` as the zero value silently; the field lies
in both directions. The pointer to the value (`*int64`) carries `null` on the wire and
through the patch decoder, and is what every adopter writes, so the refusal names it.
The rule is keyed on the package and the `Null` prefix, so a wrapper Go adds later is
refused too; the Spanner wrappers, `impulse.NullUUID`, `impulse.NullEnum[T]`, and
`decimal.NullDecimal` keep their rows, since each writes the value or `null` through its
own JSON methods. A standard-library type cannot carry a `@typescript` declaration, so
the message offers none:

- `Ships.Count: sql.NullInt64 has no JSON form of its own (encoding/json writes it as
  {Int64, Valid}); type a nullable column with the pointer *int64`

**Byte slices.** A `[]byte` is one leaf, never a list of numbers, because that is what the
wire carries: `encoding/json` writes a byte slice as a base64 string and a nil one as
`null`, and reads a base64 string back. The field is `string` in the interface and
`bytes` in the metadata, on every path (a table or view column, a computed field, an RPC
request or result field), so a browser knows the value is not text: a grid shows its size
or offers a download rather than the base64, and a form draws no free-text control for it
(a typed word fails the server's base64 decode with a 400). The line is the one the value
limits draw (section 11): an unnamed `[]byte`, a named slice type over `byte` (`type Digest
[]byte`) with no JSON methods, and, on an RPC or computed field, a pointer to either (on a
table or view column a pointer to a slice is refused, below); a `[][]byte` (an
`ARRAY<BYTES>` column) is `string[]` with display type `bytes[]`. Not on it: a byte array (`[N]byte` stays
`number[]`, since `encoding/json` writes an array as an array), a named type carrying
`@typescript` (it keeps what it declares; a declaration promising anything but `string`
on a plain byte slice with no JSON methods is refused, naming the two ways to carry JSON:
declare the type over `json.RawMessage`, or write its `MarshalJSON` and `UnmarshalJSON`),
one writing its own JSON, and `json.RawMessage` and the types declared over it, which
write JSON, not base64 (the `unknown` row above, and JSON by declaration below).
`maxLength` is never emitted for bytes (section 11); a byte limit can ride the `bytes`
type later.

**JSON by declaration.** A defined type inherits none of the methods of the type it is
declared over, so `type Payload json.RawMessage` would marshal as its underlying byte
slice, base64 where `json.RawMessage` writes JSON. The generator reads the declaration's
right-hand side and gives the pair back: a defined type a field uses on any path (a table
or view column, a computed field, an RPC request or result field, and the fields of every
struct those reach) whose right-hand side is a type with JSON methods, and which has none
of its own, gets `MarshalJSON` (value receiver) and `UnmarshalJSON` (pointer receiver)
converting to and from the right-hand side, generated into `zz_gen_json.go` in the type's
package. Such a type is JSON by declaration: with a `@typescript` declaration it is typed
as declared, and without one it resolves as the type it is declared over does
(`json.RawMessage` to `unknown`, `time.Time` to `Date`), never `bytes`. The whole
declaration is two lines:

```go
// @typescript(Point, from: "geojson")
type Position json.RawMessage
```

A type with hand-written JSON methods keeps them; one method without the other is
refused. A right-hand side without JSON methods (a plain struct, a basic type) gets no
pair, and the type marshals as before. Declare a type whenever a TypeScript type exists
for the value; `json.RawMessage` itself is pass-through for the value the application
does not model. Example: [Position](../lodestar/pkg/resources/distress_calls.go), whose two
hand-written methods the generated pair replaced.

**Nullable slices.** A slice-typed column takes its nullability from the schema, since a
Go slice has one form: the Spanner client reads a NULL `BYTES` or `ARRAY<T>` column into a
nil slice and writes a nil slice as NULL, and `encoding/json` carries nil as `null`. So
`[]byte` types a nullable `BYTES(n)` column and a NOT NULL one alike, `[]int64` an
`ARRAY<INT64>` either way, and the nullability check (section 2) leaves slice fields out.
The generated metadata says `required: false` for the nullable column and `required: true`
for the NOT NULL one without a default; the patch request struct carries `nullable:"true"`
on the nullable one (section 3), so a `null` in a PATCH clears the column, while a `null`
into the NOT NULL one answers `cannot be null` at decode. A pointer to a slice is refused
on a table or view column, naming the plain slice: the client decodes through one pointer
and no more, so `*[]string` fails on the column's first read, NULL or not, and the message
quotes the client's own words for the column's type (a view has no schema type, so there
the quote is left out):

- `Squadrons.Callsigns: *[]string cannot be read by the Spanner client (type **[]string
  cannot be used for decoding ARRAY[STRING]); type the column with the plain slice
  []string, which reads NULL as nil`

Element nullability is not carried: an `ARRAY<INT64>` row holding a NULL element fails at
read into a `[]int64`. Example: [Squadron.Callsigns](../lodestar/pkg/resources/squadrons.go),
an `ARRAY<STRING(16)>` column that is NULL until the marshal files the squadron's callsigns
and `[]` for a squadron that flies silent.

**The display-type vocabulary.** Beside its interface type, every field carries a display
type in the generated metadata (`FieldMeta.displayType`, `RPCFieldMeta.displayType`): the
name a browser chooses a control and a cell renderer by. The vocabulary is one closed
set for a table column, a view column, a computed field, and an RPC field, and a leaf's
display type is its own name on every path, so a computed `impulse.UUID` field is `uuid` as a
column of the same type is, and a computed `civil.Date` field `civildate`. The generator
holds the set as typed constants (`generation/displaytype.go`), renders every
display type through one function that refuses anything outside it, and a unit test
asserts that what the three paths emit is exactly this list; `@impulseframework/resource` lists
the same members in `ValidDisplayTypes`, spelled the same, so an adopter's build passes
by construction and never checks the generator's output.

| Display type | In the interface | Emitted for |
| --- | --- | --- |
| `string` | `string` | text columns and fields, named string types, `securehash.Hash` |
| `number` | `number`; `string` for a decimal | integers and floats; `decimal.Decimal`, `decimal.NullDecimal`, and `spanner.NullNumeric`, a quoted string on the wire |
| `boolean` | `boolean` | a `bool` column that is `NOT NULL`, and every `bool` field on the other paths |
| `nullboolean` | `NullBoolean` | a nullable `BOOL` column (`*bool`, `spanner.NullBool`); the tri-state rule for one column |
| `date` | `Date` | `time.Time`, `spanner.NullTime` |
| `civildate` | `Date` | `civil.Date`, `spanner.NullDate` |
| `uuid` | `string` | `impulse.UUID`, `impulse.NullUUID` |
| `enumerated` | the key's type | a declared or inferred picker (`@enumerate`, a key into a resource or an enumeration table). A foreign key to a column that is not the other table's key (a case number) infers none and keeps its own type, since a picker writes the key of the row picked |
| `object` | the derived or imported interface, or `unknown` | a struct, a `@typescript` type, `spanner.NullJSON`, `json.RawMessage` and a type declared over it |
| `bytes` | `string` | a byte slice, base64 on the wire |
| `string[]`, `number[]`, `boolean[]`, `date[]`, `civildate[]`, `uuid[]`, `object[]`, `bytes[]` | the element's interface type with `[]` | a slice, an array, or a named slice type of the leaf (an `ARRAY<...>` column); element pointers are read through, so `[]*int64` is `number[]` and a nullable `[]*bool` is `boolean[]` |

Never `nullboolean[]` (the tri-state rule is for one nullable `BOOL` column).
`enumerated[]` is a method's request field alone: a list field of an `@rpc` struct that
carries `@enumerate` holds several picked keys (the roles a new user is given), and a
form draws it as a picker of several. A picker of several shows every row of its
source with a mark beside each chosen one, so it reads the source whole: a source with
a `@page` maximum fails generation, naming the maximum to drop. A column stores one
key, so a list field of a table, a view or a computed resource still refuses
`@enumerate`. A list is one value to the query surface:
`allow_filter`, `index`, and `uniqueindex` on a list field are refused at generation
(section 2), and a sort naming one answers 400, since Spanner has no array equality and
cannot index an `ARRAY` column. The Angular library renders the scalars; an array falls
to its text control until it gains a list renderer.

**Derived structs.** A struct a column holds is its own TypeScript interface, derived
from its fields with no annotation and no option, declared in the resource's namespace
(`MissionDocuments.Provenance`) exactly as a computed resource's nested structs are, and
the field is typed `MissionDocuments.Provenance` with display type `object`. The runtime
marshals the column value as declared, so the struct's `json` tags are its wire names:
every field carries one (a missing tag is refused naming the field), `json:"-"` leaves the
field out of the interface, and `omitempty` makes it optional. The walk keeps the wire
rules (exported fields, no embedded fields, no map, interface, array, or recursion), and
the fields inside the struct resolve as leaves by the same steps, so a nested struct is a
second interface in the same namespace and a nested declared type is the same imported
type. Permissions are unchanged: the object is one field, granted, masked, and selected
whole.

A struct that writes its own JSON (`MarshalJSON` or `UnmarshalJSON`) is refused on the
column path unless it declares its type, since its fields do not describe the wire:

- `MissionDocuments.Seal: resources.Seal writes its own JSON (MarshalJSON or
  UnmarshalJSON), so its fields do not describe the wire; add @typescript(...) to its
  declaration`

RPC and computed shapes are unchanged: their mirrors carry generated camel-case tags, and
the mirror is the wire, so a nested struct's own `MarshalJSON` never runs there and
derivation by fields stays consistent. A `@typescript` declaration on a plain struct wins
over derivation, so an application can give a struct a richer TypeScript type when it
wants one.

**Storage.** A struct, a named slice of structs, a declared type over anything but a
basic type, or a type JSON by declaration, held by a `JSON` column and implementing no
Spanner methods of its own, gets `EncodeSpanner` (value receiver, `spanner.NullJSON{Value:
v, Valid: true}`, so a type's own or generated `MarshalJSON` is honored for storage too)
and `DecodeSpanner` (pointer receiver, reading the column's JSON text back, a NULL cell
the zero value) generated into `zz_gen_storage.go` in the package that declares the type,
beside the `zz_gen_json.go` the JSON pair goes into. An application writes no encoder. A
type with hand-written Spanner methods keeps them, whatever column it handles; one method
without the other is refused. Such a type on a column that is not `JSON`, on an unnamed
slice (`[]Provenance` has nothing for a method to attach to), or declared in a package the
generator does not write into is refused naming the fixes:

- `MissionDocuments.Provenance: resources.Provenance is stored by generated JSON methods,
  but column Provenance is STRING(MAX); declare the column JSON, or implement
  EncodeSpanner and DecodeSpanner on the type`
- `DroidReports.Frame: telemetry.Frame is stored by generated JSON methods, but it is
  declared in example.com/app/pkg/telemetry, where the generator writes nothing; declare
  the type in the resources package, implement EncodeSpanner and DecodeSpanner on it, or
  name its package with WithTypes`

**Writable packages and `WithTypes(dir)`.** The generator writes the two method files into
the resources package, the virtual, computed, and RPC packages when configured, and every
package named by `generation.WithTypes(dir)`, a `ResourceOption` in the shape of
`WithRPC(dir)`: a directory whose package name matches, loaded with the run, given more
than once when several packages qualify, and to one generator run when a module has
several (the shared run owns the shared resources package). It is for a shared package
whose types the resources package uses: an external system's client package whose model
type a column holds, which a move into the resources package would misplace and a wrapper
would make a conversion at every boundary, or a package the resources package already
imports, which can never receive its types back without an import cycle. Nothing else is
generated into a `WithTypes` package and its structs do not become resources. Example:
[telemetry.Frame](../lodestar/pkg/telemetry/telemetry.go), the droid link's frame type on a
`DroidReports` column, named in
[Lodestar's generator](../lodestar/cmd/generate/generator.go).

**Imports.** The names every `@typescript` declaration imports are grouped per module
into one import line each, placed after the `@impulseframework/resource` import of the file that
carries them (`zz_gen_resources.ts` for table, view, and computed fields;
`zz_gen_methods.ts` for request and result fields; `zz_gen_api.ts` for the key, create,
and patch shapes). The client library exports no application types.

**Nullable columns in the write shapes.** A row interface leaves a NULL cell absent, so
a nullable column is an optional property there (`notes?: string`). The Create and
Patch shapes say what the wire takes: an explicit `null` clears the column and an absent
property leaves it alone, so a nullable column renders `notes?: string | null` in both,
and in the create-your-tenant body (section 16). A key is never nullable, and a nullable
boolean keeps its `NullBoolean` value type, which carries its own null.

Examples: [MissionDocument.Provenance](../lodestar/pkg/resources/mission_documents.go), a
plain struct on a `JSON` column, derived and stored by generated methods;
[DistressCall.Position](../lodestar/pkg/resources/distress_calls.go), a GeoJSON `Point`
declared with `@typescript(Point, from: "geojson")` over `json.RawMessage`, its JSON pair
generated; [BriefingTemplate.Layout](../lodestar/pkg/computedresources/briefing_templates.go),
a computed field typed `json.RawMessage` itself, `unknown` in both clients; and
[MissionDocument.Digest](../lodestar/pkg/resources/mission_documents.go), the SHA-256 of an
uploaded document on a `BYTES(32)` column, a `string` in both clients' interfaces with
display type `bytes`; and [Ship.CargoBays](../lodestar/pkg/resources/ships.go), an
`ARRAY<INT64>` column typed `[]int64`, `number[]` in the console's interface and its
metadata.

**The generated client files compile against an older client.** A regenerated
`zz_gen_api.ts` and `zz_gen_resources.ts` compile against a `@impulseframework/resource` release
that predates a field they carry, so the generator and the client package release in
either order. Each file hands the one literal the client types through a local generic
identity function, `defineApiDescriptor<T extends ApiDescriptor>(descriptor: T): T` for
the descriptor and `defineResourceMap<T extends ResourceMap>(map: T): T & ResourceMap`
for the resource metadata (the intersection keeps an empty map indexable by a resource
name): TypeScript checks a literal passed through a generic constraint against the fields
the declaration knows and no others, the literal keeps its narrow types (`scope:
'global'`, the display types), and the client reads an absent field as not served, since
every field added to the client's types after the package's first release is optional.
`zz_gen_methods.ts` types its map by the file's own `MethodMeta` and `zz_gen_constants.ts`
casts each value on its own, so neither carries a literal the client types. The wrappers
give up the excess-property check a typed literal had, so the generator's tests carry it
instead: `Test_typescriptClientFiles_compile` type-checks the four emitted files against
two declarations in [generation/testdata/clientdecl](../generation/testdata/clientdecl), one
standing for a client released before `live`, `features` and the feature gate and the one
the next release publishes with every field today's generator emits, and the descriptor
and the map taken out of their wrappers and typed directly must compile against the
current one, so a field the generator learns is a field that declaration learns in the
same change.

## 13. Files

A row says which stored object is its file, and the generator serves that file under
the row's read route. Two halves make a file's life: an `@upload` method (section 7)
stores it and a row records the key the frame minted, and a `@file` declaration serves
it back at `GET <read route>/<segment>`, `content` by default. Nothing is hand-written:
not the permission check, not the not-found answer, not the content type, not the
headers that keep an uploaded file from running script.

**The gate.** Read on the resource and a Read grant on the segment, a field of the
resource with no column behind it. A role that lists documents and reads their rows but
holds no grant on `content` sees the listing and cannot download; the store key is never
the gate. It leaves the server in one place only, as the file's validator (the `ETag`
below), which no route accepts back as a key: nothing answers a link, and no bucket is
public.

**The row.** Located through the resource's own read path with the caller's Read
conditions and tenancy: an absent, cross-tenant, or hidden row is 404, as on the read
route, and a conditional grant on `content` (`client = subject.client`) is the row
condition the statement renders. The columns that deliver the file — the key, and the
`name:` and `type:` columns where declared — are read by the frame for itself, without
field grants. A NULL key is 404 in one sentence a person can read ("This mission
document has no content."), and so is a key the store does not hold ("The content of
this mission document was not found."): the record by its name in words and the file by
its segment, with no key of the row, and the store's key is never written into an
answer. The row's own name and key stay in the server's log.

**The response.** Every file is served as a document that can do nothing, because its
type is the uploader's word. `Content-Type` comes from the type column, then the stored
object's type, then the name's extension, then `application/octet-stream`. Every
response that serves the file (the bytes, a range of them, or a 304) carries
`X-Content-Type-Options: nosniff`, so the browser honors that type and a file lying
about it renders as that type or not at all, and the frame's own
`Content-Security-Policy: sandbox; default-src 'none'`, which gives the file document an
opaque origin and lets no script run in it, whatever its type. The frame adds its policy
beside the one an application's security-headers middleware set, never in its place:
every policy on a response is enforced, so the application keeps its `frame-ancestors`
and the file keeps its sandbox, and an application with no such middleware is protected
the same, since the frame sets both headers itself. `Content-Length` and
`Last-Modified` follow when known; `ETag` is the key, quoted, for a stored file, or the
`Tag` a content function sets for a rendered one. When a validator is sent the frame
sends `Cache-Control: private, no-cache` over the outlet's `no-store`, so the browser
may keep the file and asks again with `If-None-Match`, which the frame answers 304 after
the gate and the row lookup, before the store is opened. A body that seeks (a file on
disk) goes through `http.ServeContent`, range requests included; any other body is
copied. Refusals are JSON bodies with their status, as on every generated route. Bytes
go through the application: no store is exposed and no link is answered.

**What displays and what downloads.** A direct visit to a file route shows the file
inline only when its type is on a short fixed list the browser only displays: PNG,
JPEG, GIF, WebP, AVIF, PDF, and plain text get `Content-Disposition: inline;
filename="…"`. Everything else downloads, with `Content-Disposition: attachment;
filename="…"`: HTML, XHTML, SVG, XML and JavaScript, which can carry script,
`application/octet-stream`, and any type the frame does not recognize. The name is the
name column's or the content's. The rule exists because the type is declared by whoever
uploaded the file: without it, a person who can upload could store an HTML page that
runs script on the application's own origin, in the session of whoever opens its link.
The list is the frame's, not the application's; an application that must show untrusted
HTML to a person builds a sandboxed iframe of its own around the downloaded content. The
disposition governs a navigation alone, so an `<img>` whose source is a file route shows
the image whatever the disposition: an SVG logo in an `<img>` keeps showing while a
direct visit to it downloads. A rendered file follows the same rule, so a `text/csv`
manifest downloads.

**The store.** `resource.FileStore` is a file store as the frames drive it: `Put(ctx,
key, contentType, r)` writes an object once, `Delete(ctx, keys)` removes objects, `Open(ctx,
key)` reads one back as a `*resource.Content` (`ErrFileNotFound` when nothing is stored
under the key). `resource.Content` carries `Name`, `ContentType`, `Size` (-1 unknown),
`ModTime` (zero unknown), `Tag`, and the `Body` the frame closes. The framework's stores
are `resource/filestore`, opened from one URL each: `gs://<bucket>` is a Cloud Storage
bucket, `file://<dir>` a directory confined by `os.Root`, and `mem://` memory, for tests.
The URL is configuration, `APP_FILE_STORE` for the default store and
`APP_FILE_STORE_<NAME>` for a named one, so development keeps files in a directory and
Cloud Run keeps them in a bucket on the same code path; `filestore.Open` refuses a bad
URL and a missing bucket at start, and on Cloud Run (`K_SERVICE` or `CLOUD_RUN_JOB` set)
refuses `file://`, `mem://`, `STORAGE_EMULATOR_HOST` and `GOOGLE_APPLICATION_CREDENTIALS`,
each of which would send the service's files somewhere other than its bucket under its
own identity. A bucket whose permission is refused at start does not stop the process,
since a new grant takes minutes to take effect: the store probes again every five seconds
for ninety seconds, and still refused it logs loudly, answers 503 on every file
operation, and probes again on the next one. Every store follows the rules
the frames rely on: a failed write leaves no object, deleting a missing key succeeds, a
key is any safe relative object name (the upload frame mints UUIDs, which is a fact
about the frame, so an adopter with existing files may pass its rows' names as keys),
and a refused permission is an error of its own, answered 500 and never 404. The root
package imports no object-store SDK; the stores do.

**Named stores.** An application keeps kinds of files apart by declaring a store as a
Go type embedding `resource.Store`, in one line of its own code, in the resources
package or any package the generator run reads:

```go
// Documents holds mission documents in their own bucket.
type Documents struct{ resource.Store }
```

The store's name derives from the type's: `Documents` is the store `documents`, read
from `APP_FILE_STORE_DOCUMENTS`, and `ClientFiles` is `client_files`, read from
`APP_FILE_STORE_CLIENT_FILES`; renaming the store is renaming the type. A column says
its store by its field type, `StoreKey resource.Key[Documents]`, and an upload by the
same type, `@upload(max: 5MB, store: resources.Documents)`, whose `Execute` takes
`resource.FilesIn[resources.Documents]`; the generated setter for the column takes
`resource.Key[Documents]`, so a key minted for another store, or for the default, does
not compile into it, and copying a key the application made takes a conversion visible
in review. Application code that opens an object itself converts with `string(key)` at
the store boundary; the store interface stays untyped. The default store has no type:
a `string` column and an `@upload` with no `store:` are its, as before. One bucket per
store: two stores on one location would have the orphaned-file cleanup delete each
other's files, and where the framework can see it (the stores it opens know their
location) two stores on one location are refused where the client is built.

**Wiring.** The resource client is the one wiring point: `resource.NewSpannerClient(db,
resource.WithFileStore(files), resource.WithNamedFileStore[resources.Documents](docs))`,
which the database drivers' `Open` takes the same options for
(`database.Open(ctx, settings, resource.WithFileStore(files))`, `resource/database/spanner`
or `resource/database/postgres` under the alias `database`, section 20), each store at
most once, a second wiring of one store refused with a panic naming it, as a duplicate
route is. The generated handlers read a store off the client (`Client.FileStore(name)`,
nil when none is wired), and
the generated router refuses to start when a store the package uses is not wired: every
`@file` column's store, routed or not, and every `@upload`'s. An unwired store would
otherwise surface on the first upload, file request or releasing delete; the message
names the store and the option that wires it, never an environment variable. The
executor refuses, before the commit, a transaction that would release a key of a store
that is not wired, so the object is not left behind; this guard covers a job process,
which builds no router. Lodestar opens its two stores from `APP_FILE_STORE` and
`APP_FILE_STORE_DOCUMENTS` ([data.go](../lodestar/pkg/config/data.go)); its suites open
`mem://`.

**Release.** Deleting a row that carries a `@file` key, or pointing it at a new key,
removes the old object from the store once the transaction commits, from every
transaction the application runs: a generated frame, a patch applied on its own,
application code calling `ExecuteFunc`. Nothing is generated into the frames and a body
has nothing to remember. The generator declares the key fields on the resource
(`FileKeys() []resource.FileKey`, beside `DefaultConfig`, each field with its store), a
fact of the schema that no configuration carries and nothing an application writes
changes, and the patch machinery reads them on the transaction: a delete makes one point read of the row's key columns and records each non-NULL key as
released; an update or insert-or-update that sets a key field reads that field's current
value and records it when the row exists, the value is non-NULL, and it differs from the
new one (a new value of NULL included); an update that leaves the key fields alone reads
nothing, and an insert records nothing. The record lives on the transaction wrapper and
belongs to one attempt, so a retried transaction releases what its committing attempt
recorded. When the commit lands, `ExecuteFunc` calls each store's `Delete` with the keys
released from it, synchronously, and the request answers after it; the delete runs
detached from the request's cancellation under its own timeout, since Cloud Run gives a
process CPU only while a request is in flight, and a failed delete is logged naming the
keys and the call still succeeds, since the rows are gone. The stores are the client's
(the Mock client takes the same options, so a unit test can assert what a patch
released); a transaction that would release a key of a store the client does not hold is
refused before it commits, naming the store and the option that wires it. Any error from
the body (`ErrDryRun` included), a commit refusal, or an abort that ends the transaction
releases nothing, because nothing committed. A transaction an application wraps itself
through `NewSpannerReadWriteTransaction` and commits outside the executor reads what it
released with `Released()`, each key with its store, and deletes it itself. The upload
frame's discard, the deletes after a failed transaction, runs detached the same way and
only when nothing committed: a commit whose outcome the Spanner client cannot report
keeps its objects, since the rows may hold them, and logs the keys for the cleanup.

**Limitations.**

- One row owns one object. The keys the upload frame mints are UUIDs, so a key is never
  shared by construction, and no claim check runs before the delete: a body that records
  one key on two rows loses the object at the first release. A file several rows share
  is a row of its own, a table with `@file`, that the other rows reference by foreign
  key; the foreign-key refusal protects it while anything references it, and its own
  delete releases the object.
- Rows the database deletes by a row deletion policy never pass through the patch
  machinery; their objects are the orphaned-file cleanup's. No table's rows go by a
  cascade: rows are deleted in code only (`impulse check`, `no-delete-cascade`).
- A crash between the commit and the delete leaves an object no row claims; the
  cleanup's.
- A reader that located the row before the delete committed may find the object gone;
  the `@file` route answers 404 as for any absent object.

**The audit pass.** A finding of the audit pass is advisory, a shape the framework
handles under a stated limitation, and raising it as a warning would print it on every
generation of every application that carries the shape, so the generator reports it
through a separate pass instead: `Generator.Audit()` returns the findings of the last
`Generate()` (a `[]Finding`, each a sealed kind beside `Warnings()`; nil before a run),
and a normal generation never prints them. An application's generate program prints them
on demand: `go run ./cmd/generate/resourcegenerator -audit` in Lodestar, which prints
the warnings as `Warning: <text>` and then each finding as `Audit: <text>`, one per line
(`impulse audit` runs the same for a skeleton application once it lands); `go generate
./...` passes no flag and prints no finding. No shape is reported today.

**The orphaned-file cleanup.** An object no row holds is left behind only by a crash
between the stream and the commit or by a row deletion policy, and the framework ships
the command that removes it: `filestore.Cleanup`, run as a command of the application's
job process, one store per run. It lists the store's objects older
than the window whose names are UUIDs, the keys the frame mints, keeps every key the
store's `@file` columns hold, and deletes the rest. The keys come from the holders the
generator writes into each resources package, `FileHolders()` in `zz_gen_file_holders.go`:
one `resource.FileHolderOf[T]()` per table resource with a stored file, read with strong
reads, every key column once per run, and one `resource.ComputedFileHolder` per computed
resource with a stored file, whose rows come from application code, so the cleanup takes
its keys from the application's `ComputedKeys` or refuses. A view's keys are its tables',
so views are left out. The job process hands the cleanup every resources package's list;
the framework cannot verify the list is complete, so the window and the rules below
stand between a forgotten package and a deleted file. The window is at least a day
(`filestore.MinimumWindow`), two by default (`filestore.DefaultWindow`), and never
shorter than the longest request or job. The cleanup refuses to run when no row holds
any key in the store while aged objects are in it, since the holders it was handed would
then be the wrong ones (a store with nothing aged to delete is reported and left alone,
so an application that has stored no file yet runs its daily cleanup without a failure),
and when the unclaimed share of the aged objects is above half (`filestore.UnclaimedShare`)
with at least a hundred unclaimed (`filestore.UnclaimedFloor`), since the live keys were
then most likely not all read; it touches only UUID-shaped names, so an object named by
its row (an adopter's existing files) is never the cleanup's; and a dry run lists what it
would delete. It never runs in a pull-request stack, and in production a bucket's soft
delete is the recovery. Lodestar's `cmd/jobs cleanup-files` runs it over both stores and
the walkthrough proves it with one orphan and one live file. The cleanup runs from the
service: a scheduled method (`@schedule`, daily) starts one execution of the job process
with the cleanup command through the job driver (`resource/jobs/cloudrun`), whose settings
the site's configuration embeds (`cloudrun.Settings`, the template job the stack sets on
the service as `APP_JOBS_TEMPLATE`) and whose `Open` takes the version the image bakes in
(`APP_VERSION`): the job of this build is the template's name with the version's key
(`…-jobs-v0-1-15`), which the pipeline made on this build's image, so Cloud
Scheduler calls the service and the service starts the job deployed with it, which a
traffic rollback rolls back too; where no job is configured (development, a pull-request
stack) the start is refused and the call says so. Lodestar's `CleanUpFiles` is the method.

**Rendered files.** A document produced at request time is a computed resource's
content: struct-scope `@file` on a keyed `@computed` struct, and the computed package
declares `<Name><Segment>(ctx, key…, qSet, client, computedClient) (*resource.Content,
error)` beside `Read<Name>`, the QuerySet carrying the checked scope and identity as it
does for `Read<Name>`. The function renders and returns; it never touches the response.
A nil content is 404, in the words a row that is not there gets ("This expense manifest
does not exist."). Set `Tag` to something that changes when the document does and the
browser revalidates for free.

**The browser client.** The generated descriptor lists a resource's segments
(`files: ['content']`), and its handle gains `fileUrl(key, segment = 'content')` beside
`url(key)`. Nothing fetches: the browser addresses the route itself, in an `<img src>`
or an `<a href>`, and the session cookie rides along.

Examples: [MissionDocument.StoreKey](../lodestar/pkg/resources/mission_documents.go), a
stored file with its name and type columns, served to the console's crew and listed but
not served to the client portal; [ExpenseManifest](../lodestar/pkg/computedresources/expense_manifests.go),
a rendered `text/csv` manifest of a mission's booked expenses.

## 14. Live pages

A page that asked for a live list or a live row stays current without polling and
without refetching while nothing changed, and a page mounted again inside a short window
is served by the browser with no request. The server holds no push connection and keeps
scaling to zero.

**The shape.** A request opts in by carrying the header `X-Subscribe: <tab>`, a
client-minted tab id of 1 to 64 characters of `[A-Za-z0-9_-]`; the client library sends
it only on a live page's requests (list pages and record pages, live by default on a
table-backed resource, whose generated metadata says its changes are published, unless
the page says `live: false`), never from a picker, an edit form or an export. On a permitted list or read the generated handler
registers the subscription — who, which tab, which resource, which key or which tenant
domain, until when — *before* it runs the query, so a commit that lands during the
query is not missed; a refused request registers nothing. A row is named by its key as
the read route spells it: a single key's string form, a compound key's parts joined with
`/` in route order (`resource.RowKey`). A list is named by its resource and the request's
domain, empty for a global resource; filter, sort and cursor are not part of a
subscription, so any change to the resource in that domain refetches the page. A
mutation (a patch, the consolidated patch, an RPC method) publishes after its commit and
before its answer: for each row it wrote, the subscribers of the row get a row document
and the subscribers of the resource's list in the row's domain a list document; above
100 rows of one resource in one request, every subscriber of the resource gets one
resource document instead. Writes to one target within one second coalesce. The publish
is bounded by two seconds and a failure is logged; it never fails the request. The
write's answer then names what the write put into the caller's own change set, so the
tab that wrote fetches at once (below). A
request's calls on the subscription record and the identity (register, renew,
unsubscribe, revoke, token) are bounded by five seconds (`live.RecordTimeout`), so a
backend that stops answering never holds a request open. The
browser listens to its own change set (`users/{uid}/changes`, timestamps after the last
one it saw) and refetches the row or list with `_v=<the change's timestamp>` (section 4),
or `_v=<the write's id>` for a write the tab made itself,
and such a response carries `Cache-Control: private, max-age=300` so a remount inside
the window is the browser's own; before any change arrived the client mints a seed at
login, so a second user on a shared machine never hits the first's cached answers. Every
other response stays uncached; the session and digest routes never carry the header.

**A write the resource client did not make.** Code that changes a row some other way
(another library's store, a statement of its own) says so on the collector:
`TouchedRows.Touch` names a row by its key, and `TouchedRows.TouchTable` says a table
changed in rows the writer cannot name, which tells every subscriber of the table and of
every view that reads it, in every tenant, with one resource document each. A
transaction wrapped with `NewSpannerReadWriteTransaction` and committed outside the
executor hands the rows its patches wrote to the collector its context carries when
`Committed` is called. Inside a generated handler the request's publish carries all of
it; outside one, `live.Announce` publishes a collector with no answer to name its
documents on.

**Whose change it is, and when a page fetches.** A page shows a person's own change at
once and groups other people's, and what tells the two apart is the write's id. A
committed request is one write: `live.Publish` mints its id (26 random characters) once,
whatever tenants its rows were written in, and every change document the write publishes
carries it as `write`, in every subscriber's change set. A document that several writes
within one second became carries the last one's, as it carries the last one's timestamp,
so the tab that wrote first finds somebody else's id on the document and fetches for it.
The change's time could not serve: it is Firestore's server timestamp, set when Firestore
commits the document, which the server answering the write never learns; and the
document's id is the target and the second, shared by everybody who wrote to the target
in that second. The write's answer names the id and the documents that went into the
caller's own change set, the ones its own tabs will hear of, in the response header
`X-Changes` (`live.Answer`), as JSON, percent-encoded so a key or a tenant outside ASCII
travels in a header; decoded:

```
X-Changes: {"write":"<id>","changes":[{"kind":"list","resource":"Ships","domain":"<domain>"},{"kind":"row","resource":"Ships","key":"<key>","deleted":true}]}
```

The generated write handlers hand `live.Publish` the answer they have yet to write, so
every write path carries it: a resource's patch route, the consolidated patch, an RPC
method and the create-your-tenant route. The header is at most 2048 bytes
(`live.MaxChangesHeaderLength`); a write that reached more of the caller's subscriptions
than fit answers `{"write":"<id>","all":true}`, and the tab fetches everything it holds
live. An answer carries no header when the write reached nothing the caller holds live,
when the request speaks for no session principal (a machine outlet's, a scheduled
call's), and when a publish of the write failed (it may have written some of its
documents, which a tab told the id would take for changes it had fetched for). Nothing
is lost by the header's absence: the change set reports the change and the page fetches
for it as for anyone's.

The browser does the rest (the client library's README, "Live pages"). The tab that
wrote fetches what the header names when the write is answered, without waiting for its
change set, and does nothing when the change set reports a document carrying that
write's id. A document carrying any other id, or none, is somebody else's change: the
first one starts a wait of two seconds and everything that arrives in the wait becomes
one fetch, and while changes keep arriving a page fetches at most once every five
seconds. A tab that is not shown fetches nothing; it remembers that something changed
and fetches once when it is shown again. The two times are the client library's
defaults, and an application sets its own where it creates its client. The server holds
none of this: it publishes each write as it always did, once.

**Derived resources.** A virtual or computed resource has no rows of its own, so a commit
reaches it through the tables it reads, which the generator records next to the resource:
each package holding published virtual or computed resources gets a generated
`zz_gen_live_derivations.go` with an exported `LiveDerivations` (a `live.Derivations`)
naming, per resource, its `@rowsOf` root, its other tables and whether the set was read
off the query or declared, every table as the generated `Resource` method of the type over
it, so a renamed or dropped table fails to compile there; the handler package's
`zz_gen_live_derivations.go` is the assembly, `liveDerivations`, which imports those
packages and merges their values (`live.Merge`) into the one value every publish expands
its writes with (`live.Publish`, `Derivations.Expand`). A virtual resource's set is read
off the embedded query behind its `Subquery` method with memefish's lexer, never its
parser, so a Spanner feature the parser has not learned does not stop generation: the
names in table positions, after `FROM`, `JOIN` and the commas of a `FROM` list, with a
`WITH` name in scope from the end of its own body, so a CTE named like a table shadows the
table after its body and not inside it, and functions, subqueries, hints and aliases
passed over. A name in a table position that is neither a table nor a CTE in scope, a
recursive `WITH`, and a text the lexer refuses each refuse generation, naming the place;
`@changedBy` on the resource replaces the reading and is the way out, and a declaration
that differs from what the query names is a warning naming both sides, not a refusal. A
computed resource's set is its declaration alone (section 1). A table whose rows the
resource carries one to one under the same key (`@rowsOf`, the root) reaches it exactly as
the table itself is reached: a row document under the same key to the row's subscribers
and a list document to the list's subscribers in the write's domain. Any other table it
reads reaches every subscriber of the resource with one resource document, the same
document the bulk path writes, to its row subscribers and to its list subscribers in the
write's domain, or in every domain when the table or the resource is global; nothing is
read from the database to name the derived rows, and a page may refetch and find nothing
changed, the tolerance a table's list document already has. A resource reached both ways
in one commit gets the resource document alone. The metadata's `live` is true for a
resource with a known set, so its pages are live by default like a table's; a computed
resource with no declaration stays `live: false`.

One kind of table the record names differently. A table of an Auth is declared by the
generated management inside its tree (section 21), in a package Go lets nothing outside
the tree import, so a view of the application's that reads one, a sector's people over
the crew's memberships for instance, cannot write the type. Its entry names the table by
the table's name, as text (`{Name: "CrewUserRoles", Global: true}`), and its comment and
the file's say so. Nothing is given up: the compile failure a type gives protects a
record that outlived a rename, and here the declaration and the record are written by
the same run from the same schema; and a name brings no import, so the tree's own
methods may read the view. The view's query is read, or `@changedBy` declared, exactly
as for any other table. The Auth's handlers publish their commits with the application's
derivations, which the application hands them where it constructs them, so a membership
given or an account disabled through the management reaches the view's pages. A sign-in
is announced too, though no generated handler commits it. Once a sign-in's session
stands, the Auth's identity client tells the open pages of the session it made and of
the person's account, and that the Auth's identities and role memberships changed
(`live.Announce`, through the service and the derivations the Auth's handlers hand it,
`AnnounceThrough`). The roles an accepted invitation, a directory's default role or a
sign-up gives, and a tenant an invitation makes, are announced when their transaction
commits, and so is what a person's leaving a tenant from their own account page writes.
So a list of accounts, a person's page and a tenant's people show a new account, a role
given, a sign-in identity linked and a session started without a reload. Each
announcement asks the subscription record once for every table and view it reaches,
inside the sign-in's request, bounded by the two seconds of every publish; a failure is
logged and the sign-in stands. The session library's other routes (a sign-out, a
password or an address a person changes on their own page, a reset) still publish
nothing, as a write behind any table's back does not.

The lists the generated management declares itself (section 21), a tenant's lists and
the lists of how a person signs in, are recorded where the assembly can reach them: in
the Auth's handlers package (`crewhandlers.LiveDerivations`), since the declarations
package, where the views among them are declared, is internal to the tree. That record
is inside the tree, so it names every table by its type, the Auth's own and the
application's, and every list by its type in the package that declares it (a computed
list, such as how a person signs in, is in `crewidentity`), and the assembly merges it
with the packages' values.

**The routes**, on every session-serving outlet under its API prefix, behind the outlet's
session middleware like every other route:

```
POST <prefix>/live/renew        {"tab":"<tab>","subscriptions":[{"resource":"Ships","key":"<key>","domain":"<domain>"},{"resource":"Ships","domain":"<domain>"}]}
                                → 200 {"kept":[...],"dropped":[...],"expiresAt":"<RFC 3339>"}
POST <prefix>/live/unsubscribe  {"tab":"<tab>"} or {"tab":"<tab>","all":true}   → 204
GET  <prefix>/live/token        → 200 {"uid","token","project","database","apiKey","emulator"}
```

The client renews every 120 seconds; a subscription expires 300 seconds after it was
written or renewed. The renewal re-checks each subscription against the user's grants —
`Read` on the resource for a row, `List` for a list, in the subscription's domain (the
global scope when none is sent) — writes the kept ones with a fresh expiry in one batch,
and echoes, in the order sent, which were kept and which dropped; a resource the grants
do not know is dropped, never refused. A row subscription of a domain-scoped resource
sends the domain the row was read in, or its re-check runs in the global scope, where a
domain-scoped resource holds no grant, and the subscription is dropped. Revocation
therefore ends a subscription at the next renewal, and the refetch is refused meanwhile;
the publisher never checks permission. A page sends `unsubscribe {tab}` best effort as it
leaves; a logout sends `{tab, all: true}`, which deletes every subscription of the
principal and revokes the browser's identity. An unsubscribe that has reached its
handler is finished whatever becomes of the connection: its deletes run under the
handler's own five seconds and not under the request's context, so a page that closes
before the answer leaves no subscription behind, and its closing logs nothing. A record
that refuses the delete, or does not answer within the five seconds, is logged as any
failure is. An unsubscribe that never reaches the handler leaves the tab's subscriptions
to their expiry: they are not renewed, the publisher's lookups pass them over five
minutes after their last renewal at the latest, and the time-to-live policy deletes the
documents (`live/firestore/README.md`). The token route answers the session
principal's id (below), how to reach the change set, and the identity: in production a
Firebase custom token the browser signs in with, against the emulator an empty token and
the emulator host, which the browser connects to with the SDK's mock user token. A
request carrying `X-Subscribe` on a machine outlet answers 400 naming the header:
machine clients wanting change notification are a different consumer on a topic. Every
subscribing request, the live routes included, carries `subscribe=<tab>` on its request
log line (`AddRequestAttribute`).

**The principal id.** Every subscription, change set and browser identity belongs to a
principal id (`live.PrincipalID`): the name of the auth the request came through, `|`,
and the session's user name, or `role:<role>` for a session established as a role, so
`crew|alice`, `members|alice@example.com` and `crew|role:Auditor`. The auth's name is the
one the outlet's routes bind (section 8), so two auths over one database never share an
id: a password user name equal to a directory user's email is two ids and two change
sets, and a logout through one auth ends that person's subscriptions and identity alone.
The id is the Firebase user id (uid) the browser signs in as, and Firebase allows a uid
of at most 128 bytes (`live.MaxPrincipalIDLength`). Nothing is truncated or hashed: a
person whose id is longer uses the application as always while the live pages refuse
them. The token, renew and unsubscribe routes answer 403 ("Pages do not update on their
own for your sign-in. Reload a page to see what has changed."), and a subscribing list or
read registers nothing, each with the reason, the id's length and the limit logged.
The refusal sits in the live routes because the sign-in belongs to the session library
and the auth package, which the generated code does not configure. The session library
sets no length on a user name (its username columns are `STRING(MAX)`), so nothing is
checked when the server starts.

**The seams** are in `resource/live`: `SubscriptionRecord` (register and renew in one
batch, unsubscribe a tab or a principal, the subscribers of a row, of a list in a domain,
of a resource), `ChangePublisher` (`Publish(ctx, domain, writes)`, which answers the
documents it wrote, per principal), `Identity` (the
token payload and the revocation), `Signaler` and `Subscriber` (the signals below),
bundled as `Service`; `Fanout` is the fan-out every publisher implementation writes, and
`Fake` an in-memory service for tests. The Firestore implementation is the live driver,
`resource/live/firestore`: the configuration embeds its `Settings` (the project, the
database, the web API key and the emulator host) and opens it with `Open`, as it opens
the database driver (`resource/database/spanner`, whose `Settings` are the three Spanner
variables and the emulator host and whose `Open` builds the resource client) and the job
driver (`resource/jobs/cloudrun`), each bound under a neutral import alias (section 20);
each driver publishes the declaration of its settings in the package beside it, which
impulse's checks and bedrock's stack read. Against the emulator with no project named,
the live driver opens the emulator's database under `firestore.EmulatorProject`
(`live-emulator`), and the token route hands the browser the same id. The layout is
`subscriptions/{id}`, a flat server-owned collection with a
time-to-live on `expiry` and one composite index per lookup shape, and
`users/{uid}/changes/{id}` with a time-to-live on `expires`, which a user may read for
their own uid and nobody writes from a client; `firestore.rules`, `firestore.indexes.json`
and the layout's README sit beside it. The generated handlers reach all of it through the
application's `LiveService() live.Service` accessor, and the three routes are generated
in `zz_gen_live.go` as delegations to the library's handlers.

**The signals document.** The live service is also how an application's instances tell
each other that something shared changed. One document per application,
`application/signals`, has a field per kind of change, `features`, `tenants` and
`policy`, each `{at: <server timestamp>, by: <the writer: its Cloud Run revision or job execution, else its host, and its process id>}`; a
`Signal(ctx, kind)` writes that kind's field alone (a merge write on the field path),
so two kinds never clobber each other, and one snapshot after quick signals of two
kinds carries both. Each instance holds one snapshot listener on the document
(`Subscribe(kind, onSignal)`, several subscriptions per kind, a subscription made
after the listener began included; `Subscribe` waits on nothing): every snapshot
wakes the subscriptions of each kind whose `at` advanced past the last seen, the first
snapshot against no time seen, so a subscription made before it is woken once for the
kinds the document already holds (a nudge to reread, never a fact) and misses nothing
written after it was made, while one made after it hears only the signals after it. A listener Firestore ends on its own is logged and reopened
with backoff by the subscriber, the one place that owns reconnection, and on the
reopen every kind that advanced while it was down wakes once. A signal carries nothing
but the fact of a change, so the signaler coalesces: while a write of a kind is in
flight, later signals of the kind wait for one write that covers them all, which one
of their callers makes when the write in flight ends, so a burst costs two writes where
the channel answers well inside the limit below. A `Signal` returns once its signal is
sent and leaves nothing to be sent behind it, so an instance that gets no CPU once its
requests have answered (Cloud Run's default) owes no signal; a covering write that fails is the failure of every
caller that waited for it. No caller's context ends another's signal: a caller whose
context ends, or whose deadline passes, under the covering write it is making fails
alone and another waiting caller makes the write, so no call makes more than one write
or waits past its context.
The live service gives every `Signal` its own limit of two seconds
(`live.SignalTimeout`), for its wait and its write together and on top of the caller's
context: a caller that brings no deadline, as the tenant write paths do, is not held by
a channel that does not answer, and a caller whose own deadline is shorter keeps it. On
a channel too slow for both writes of a burst to fit in two seconds, the callers that
waited reach their limit in turn, each of them alone.
A signal's failure is returned for the caller to log and never fails the request, since every
subscriber also holds its copy to its own backstop, on the request's path and not only
on a loop's (sections 15 and 16: the feature flags and the tenant roster; the permission
engine's heartbeat interval in the access package). The kinds are `resource.SignalKind`
(`KindFeatures`, `KindTenants`, `KindPolicy`), re-exported by the live package as
`live.Kind`; the document needs no index, no time-to-live and no rules exposure, since
no client reads it.

**What an application wires.** One method, `LiveService() live.Service`, returning the
Firestore service: the configured Firestore database (`APP_FIRESTORE_DATABASE`, the
database bedrock hands the application; the project, and the optional Firebase web API
key `APP_FIREBASE_API_KEY`), or the emulator (`FIRESTORE_EMULATOR_HOST`). Every
application wires one; the generated handlers assume it, and a test harness wires
`live.NewFake()`. The infrastructure applies the indexes and the time-to-live policies.
Lodestar's wiring is [pkg/config/data.go](../lodestar/pkg/config/data.go) and
[app/app.go](../lodestar/app/app.go).

**The names the client library mirrors**: the header `X-Subscribe`, the parameter `_v`,
the routes `live/renew`, `live/unsubscribe` and `live/token` under the outlet's prefix,
the renewal interval 120 s, the expiry 300 s, the cache header `Cache-Control: private,
max-age=300`, the bulk threshold 100, the response header `X-Changes` and its JSON
(`write`, `changes`, `all`), and the change document's shape (`kind` of `row`,
`list` or `resource`; `resource`; `key` and `deleted` on a row; `domain` on a list; `at`,
`expires` and `write`). The generated descriptor names the routes per outlet:
`live: { renewRoute, unsubscribeRoute, tokenRoute }`.

## 15. Feature flags

A resource, a field, or a method can be put behind a flag that an administrator turns
on and off at runtime, without a release: while the flag is off, the thing does not
exist for the browser or the API, and when it is turned on, every instance serves it
within a moment.

**Declaring a flag.** A flag is a `resource.Feature` constant in the resources package;
its value is the flag's name (`[a-z][a-z0-9_]{0,63}`) and its doc comment the
description an administrator reads:

```go
// Debriefs lets a crew write and read mission debriefs after a return.
const Debriefs resource.Feature = "debriefs"
```

`@feature(Debriefs)` on a `@resource`, `@virtual`, `@computed` or `@rpc` struct gates it
whole; on a field of a table, view or computed struct it gates the field (section 1).
The generator writes `zz_gen_features.go` into the resources package on every
application: `Features()`, the declarations in constant order, and `FeatureGates()`,
every gated resource (by its plural), field (`Resource.field`) and method (by name) with
its flag. Both are written whether or not anything is declared, so the generated
handlers call them unconditionally.

**What a flag does while it is off.** A gated resource's and method's routes answer 404
with the router's not-found body, before the handler runs: the generated route
registration wraps exactly those routes in the application's `FeatureGuard` (outside
the domain guard, where both apply), and a gated resource's arm of the consolidated
patch answers `unknown resource`. A gated field is unknown to the decoders (`columns`,
`sort`, `filter`, a patch body naming it: 400) and absent from every response. The
permission digest leaves the resource, the field (`Resource.field`) and the method out,
exactly as a denied target is left out, so the browser's navigation and forms never show
them. Nothing is checked twice: the gate and the permission check are the same two
answers a denied route gives, and a flag that is off never changes what a grant means.
A `FeatureSet` that is nil (the application wired none) leaves every gated target off.

**The table.** Every application has two tables, `FeatureFlags` (`Name` STRING(64) the
key, `Description`, `Enabled`, `UpdatedAt` a commit timestamp, `UpdatedBy`) and
`FeatureFlagChanges` (`Name`, `ChangedAt` a commit timestamp, `Enabled`, `ChangedBy`),
the second a record of every flip written in the flip's own transaction; the rows outlive
their flag, so there is no foreign key. `resource.FeatureFlagsDDL(dbType)` renders the
statements for Spanner and for PostgreSQL, and an application copies them into a
migration as Lodestar does in
[000001_Baseline.up.sql](../lodestar/schema/migrations/000001_Baseline.up.sql),
pinned to the function by a test. The generator never derives a resource from the table:
`FeatureFlags` has a `Description` column but no `Id`, and an enumeration table is
recognized by the two together.

**Deploying.** `resource.MigrateFeatures(ctx, client, resources.Features())` runs beside
the role check, after the schema migration: a flag declared for the first time is
inserted off, a known flag keeps its state and takes the release's description, and a
flag the release no longer declares is deleted, all in one transaction, with
`UpdatedBy` the process event. A flag's state therefore survives releases and never
leaks a flag the code cannot serve; a new flag is always off until an administrator
turns it on. Lodestar's deploy step is
[pkg/deploy/deploy.go](../lodestar/pkg/deploy/deploy.go), called from the migrate and
bootstrap commands.

**The copy every instance holds.** `resource.LoadFeatures(ctx, client)` reads the table
into a `FeatureSet`, and `FeatureSet.Follow(ctx, liveService)` keeps it current until
ctx ends: the flags ride the `features` kind of the live service's signals document
(section 14). The freshness is the request's own: a lookup (`Enabled`, `Flag`,
`EnabledNames`) that finds the copy older than the backstop
(`resource.WithFeatureBackstop`, five minutes by default), or behind a signal of that
kind this instance received, rereads the table before it answers, under the context
`Follow` was given and never the request's, and one reread serves every request waiting
at that moment. A signal is recorded the moment it arrives, before any reread is tried,
and a reread already in flight then does not clear it. `Follow`'s loop rereads on every
signal and at the backstop interval too, ahead of the requests, so on an instance that
is serving a lookup finds the copy fresh and reads nothing; nothing depends on the loop,
so an instance that got no CPU between requests (Cloud Run's default) answers its next
one from a read no older than the backstop. A failed reread is logged, the copy stays as
it was, and the table is left alone for one second: lookups in that pause are answered
from the copy without a read, so a database that is down is read, and its failure
logged, once a pause and not once a lookup, and the first lookup after the pause tries
again. `Reload`, which a flip's handler calls after its commit, records the flip before
it reads, so a reload that fails (the request that flipped had ended) is made good by
the next lookup, under the set's own context. A flip of this instance ends the pause
too: the database has just taken a write, so the next lookup reads it, and a read that
fails after the flip begins a pause of its own. A database that does not answer holds a
lookup for less than twice the read's timeout (ten seconds): the read in flight when the
lookup came, and at most one after it. A flip therefore reaches every instance that
is serving within a moment, and the backstop bounds the rest. The subscriber is the application's live service,
which every application wires; `Follow` refuses a nil one. The application exposes the
copy as `FeatureSet() *resource.FeatureSet`, asserted on the application type beside
`LiveService`, and the generated decoders, routes and digest read it. Lodestar reads it
through the configuration's database client when the App is built and follows it from
`Start`: [app/app.go](../lodestar/app/app.go).

**The routes**, generated on every application:

```
GET  <prefix>/features                       → 200 {"enabled":["debriefs", ...]}     anyone signed in, every outlet
GET  <prefix>/feature-flags                  → the FeatureFlags list, sort=name       List on FeatureFlags (global)
GET  <prefix>/feature-flags/{featureFlagName} → one flag                              Read on FeatureFlags (global)
GET  <prefix>/feature-flags/{featureFlagName}/changes
                                             → 200 {"changes":[{"changedAt","enabled","changedBy"}, ...],"more":false}
                                                                                      List on FeatureFlags (global)
POST <prefix>/set-feature                    {"name":"debriefs","enabled":true}      Execute on SetFeature (global)
                                             → 200 {"name","enabled","updatedAt"}; an undeclared flag is 404
```

The features route is open to anyone signed in: what the browser reads at start and
again after a flip, and what the client's `features` state (`enabled(name)`,
`subscribe`, `canSet()`) answers from. `FeatureFlags` is a read-only global resource the
generator registers in every application's collection (List and Read over `description`,
`enabled`, `updatedAt` and `updatedBy`, `name` the key, listed by name) and carries in
the TypeScript constants, metadata and descriptor exactly as a generated resource, so
the Feature flags page is the library's list over it and reads it through an ordinary
handle; the routes are the library's handlers (`resource.FeatureFlagsHandler`,
`resource.FeatureFlagHandler`), delegated to from the generated `zz_gen_features.go` in
the handler package, and a struct or a manual registration that would take the name
`FeatureFlags` is refused. Three of its fields are served as a person reads them. The
`description` is the constant's doc comment with the flag's name in words in the
constant's place where the comment opens with it, as a Go comment does
(`FeatureDeclaration.Words`: "CargoManifest shows a ship's cargo bays." is served as
"Cargo manifest shows a ship's cargo bays."), which is how `MigrateFeatures` writes the
table. `updatedAt` and `updatedBy` are when the flag was last flipped and by whom, read
from the flag's newest change record and not from the row's columns of those names,
since the deploy step that declares a flag writes the row too, under a process's name
and at the time of the deploy: so the list says what the flag's history says.
`updatedAt` is the moment of the last flip, and `null` for a flag nobody has flipped.
`updatedBy` is the name the person signs in under, read out of the event source the flip
recorded (`resource.EventActor`; the session and the account's id stay in the table as
evidence), a process's source as it was recorded where a process flipped it, and the
empty string for a flag nobody has flipped. `EventActor` reads the words of a sentence
in which nothing is escaped, so the name is for showing to a reader and for no decision.
A list request that asks for a sort on `updatedAt` or `updatedBy` is refused with 400,
in a sentence that says the two are worked out from the flag's history and what the list
can be sorted on (`name`, `description`, `enabled`): the statement that orders the list
reads the row's own columns, the last write of any kind, so the order would be by
something the answer does not show. The Feature flags page offers no sort on them.

**A flag's history.** `GET <prefix>/feature-flags/{featureFlagName}/changes`
(`resource.FeatureFlagChangesHandler`) reads the flag's change records back newest
first: when each flip was committed, what the flag was turned to, and who did it, by the
same rule as `updatedBy`. It asks no grant of its own. The flag is read first as the
list reads it, under List on `FeatureFlags` with whatever condition the grant carries, so
whoever may see the flag in the list may see its history, a caller without the grant is
refused with 403 and an undeclared flag is 404. Each flip carries what the caller may see
of the flag: its facts ride the grants on the flag's fields that hold the same facts
today (`changedAt` under `updatedAt`, `enabled` under `enabled`, `changedBy` under
`updatedBy`), so a grant that leaves out who last flipped a flag leaves `changedBy` out
of every flip, and a caller who may list none of the three (the description alone, say)
is refused with 403, since the answer would tell them only how often the flag was
flipped. The request's query is not read; nothing asked of the list (`columns`, a
filter, a page) narrows the history. One read answers the newest
`resource.FeatureFlagChangesLimit` flips (100) and says with `more` when older ones are
left out; a flag nobody has flipped answers an empty list. The route reads the change
table and never the `FeatureSet`, so it adds nothing to how the instances keep their
copies.
`SetFeature` is the library's method (`resource.SetFeatureHandler`), registered with
Execute and carried as an RPC method entry: it writes the row and its change record in
one transaction, honors `X-Dry-Run`, refuses `X-Warning` (it declares no warning),
reloads this instance's copy, then signals the other instances through the live
service (`Signal(ctx, resource.KindFeatures)`, bounded by two seconds, a failure
logged), and answers the flag as written. The reload comes first, so this instance
answers from the flip while the signal is being sent; the signal does not run under the
request's context, so a request that ends once the flip is committed still tells the
other instances. An `@rpc` struct named `SetFeature`
is refused.

**The role.** A role that administers flags holds Execute on `SetFeature` and List and
Read on `FeatureFlags` with its four non-key fields, in the global scope:

```json
{
  "name": "FeatureAdministrator",
  "permissions": {
    "List": [{ "resource": "FeatureFlags", "fields": ["description", "enabled", "updatedAt", "updatedBy"] }],
    "Read": [{ "resource": "FeatureFlags", "fields": ["description", "enabled", "updatedAt", "updatedBy"] }],
    "Execute": [{ "resource": "SetFeature" }]
  }
}
```

The digest decides what the Feature flags page may do: a user without Execute sees the
flags and each flag's history and cannot flip one; a user without List is never shown the
page.

**Tests.** The generated authorization matrix carries the features route as an open
case (200 without a grant), the FeatureFlags routes as query pairs, and SetFeature as
denied-only with its dry run, on every outlet serving them. When anything is gated, the
generator also writes `zz_gen_features_test.go` into the handler tests package: every
gated route and field driven in both states of its flag, the flag flipped in the test
database through `resource.SetFeatureEnabled` between them after
`resource.MigrateFeatures` wrote every declared flag, so the suite proves the 404 and the
unknown column off, and the matrix's answers on. The matrix puts every declared flag on
in its own test database before it drives a route, because a gated route is absent while
its flag is off and would answer 404 before the permission gate the matrix pins; the
application's harness needs no rule of its own for that. The application's
`newTestHandler` builds the App over the test database, which reads its flags as it is
built.

**The names the client library mirrors**: the routes `features`, `feature-flags`,
`feature-flags/{featureFlagName}` and `set-feature` under the outlet's prefix; the
features answer `{"enabled": [...]}`; the resource `FeatureFlags` with the JSON fields
`name`, `description`, `enabled`, `updatedAt`, `updatedBy`; the method `SetFeature` with
the body `{"name", "enabled"}` and the result `{"name", "enabled", "updatedAt"}`; the
descriptor's `features: { route: 'features' }` on every application and `feature:
'<name>'` on a gated resource's, method's and field's entry; and in the client file the
`Feature` union of the declared names with the `Feature` constants keyed by the Go
constants' identifiers (`Feature.Debriefs`). The kind the instances signal each other
with is `features` (`resource.KindFeatures`), a field of the `application/signals`
document in the Firestore layout.

## 16. Tenant records

A tenant made at run time is served by every instance without a restart or a release.
The application declares one table as its tenant record with `@tenant` (section 1), and
the library keeps a copy of its keys on every instance: the roster. Nobody creates a
tenant directly: staff bring an organization in by inviting one person, and that
person's first sign-in makes the tenant ("How a tenant comes to exist", below).

**The record.** `@tenant` goes on the global, table-backed `@resource` whose rows are the
tenants; its one key is a generated id (`impulse.UUID`), whose text is
the domain in every tenant-scoped URL. The record's route
name is the segment tenant-scoped routes are served under and its key's route parameter
is the domain parameter: Lodestar's `Sector`, keyed by `ID`, puts the sector-scoped routes
at `/console/api/sectors/{sectorID}/…` while `/console/api/sectors` lists the sectors. A
tenant-scoped resource, view, computed resource or method in a package with no `@tenant`
record is refused, so a tenanted application always has one, and a second `@tenant` is
refused naming the first.

**The key and the name.** A tenant's key is never a word a person types. It is written
into every row the tenant owns (the `@domain` column, typed `impulse.UUID`, or `string`
holding the id's text) and is the domain of every role held in the tenant, so it cannot
change, and a key a person typed could be used again for another tenant after a delete.
The generator therefore refuses a `@tenant` record keyed by anything but an id the
framework generates when the tenant is made. What people call the tenant is the column the record marks
`@tenantName`: an ordinary field, changed under the record's Update grant, and every
instance's roster carries the new name after the write, so the place switcher and the
session's tenant list show it at once. Whether two tenants may carry one name is the
application's to say in its schema: a unique index on the name column refuses the second
(409, as any duplicate of a unique value), and an application that allows it has no such
index; the skeletons and `impulse add tenancy` write the index. An `add` on the record's
collection makes no tenant: it is answered 400, with or without a key in its path, in a
sentence that says how one is made ("a sector is not created directly: invite one
person, and the sector is made when that person first signs in"), whoever asks and
whatever they hold. What names a tenant by hand names it by its
name: `TenantRoster.Named(name)` answers the one tenant that carries a name, and is an
error for a name no tenant or more than one tenant carries, which is how a development
bootstrap reads the tenants of its logins. The development seed inserts its tenants
under fixed ids, so an application's suites name them.

**The roster.** `resource.TenantRoster` is the set of the record's keys an instance
holds, built by the generated constructor in the handler package,
`New<Record>Roster(client, opts...)` (`NewSectorRoster` for `Sector`), which carries the
record's table and key column so the application names neither. `Start(ctx)` reads the
set once, failing the start when the read fails, and then keeps it current until ctx
ends. `Has(domain)` answers from the set behind an atomic pointer: while the copy is
inside its backstop and no tenants signal has arrived since its last read, it waits on
nothing and performs no read (Keeping up, below, says when it rereads first). A domain
the set does not hold is unknown. `Domains(ctx)` lists the set sorted, in `DomainRoster`'s signature, so the
roster's `Domains` is what the application hands `SessionPermissions`. `Add` and
`Remove` change the set at once. The read is one statement selecting the key column,
rendered for Spanner and for PostgreSQL and run outside any request transaction. The
application exposes the roster as `TenantRoster() *resource.TenantRoster`, asserted on the
application type by the generated contract beside `DomainGuard`; a nil roster holds no
tenant, so an application that wires none fails closed on every tenant-scoped route.

**The guard.** The generated `DomainGuard` and the consolidated dispatcher's descent ask
the roster first (`TenantRoster().Has(domain)`) and answer an unknown domain with the guard's
404 or the dispatcher's 400; then the session's reach (`UserPermissions.Reaches`: whether
the tenant's declaration admits the way the session was established, section 6) and answer
a tenant the session may not enter the same way, in every mode; under
`WithConcealedDomains` they ask the caller's foothold last (`UserPermissions.HasGrants` in
the domain) and answer a caller without one the same way. Without concealment the roster
and the reach answer. The record's own routes stay unguarded: they terminate at the
segment's value and are global. The application hand-writes no existence seam; the
generated contract asks for the roster alone, and the reach is the composed session
permissions' answer.

**Keeping up.** What writes the record calls `Reload` after the commit, never inside it,
so the instance that wrote the tenant, or its declaration, serves it at once: the
generated maker an invitation's first sign-in makes the tenant through and the
create-your-tenant route (both below), and the record's generated write paths for an
update or a delete, the standalone PATCH handler and the consolidated dispatcher's case
for the record and for its directories (below); a key cannot change on update. The same path then
signals the tenants kind (`resource.KindTenants`) through the live service's `Signal`,
the application's one signals document, after the commit and before the response, as a
feature flip signals the features kind; a failure to reload or to signal is logged and
never fails the request. Every instance's roster reloads on the signal: `Start`
subscribes to the application's one watch when `resource.WithTenantSignals(liveService)`
is passed, and never opens a listener of its own. The freshness is the request's own: a
lookup (`Has`, `Domains`, `Declarations`, `Name`, `Named`) that finds the copy older
than the backstop (`resource.WithTenantBackstop`, five minutes by default), or behind a
tenants signal this instance received, rereads the table before it answers, under the
context `Start` was given and never the request's, and one reread serves every request
waiting at that moment. A signal is recorded the moment it arrives, before any reread is
tried, and a reread already in flight then does not clear it. `Start`'s loop rereads on
every signal and at the backstop interval too, ahead of the requests, so on an instance
that is serving a lookup finds the copy fresh; nothing depends on the loop, so an
instance that got no CPU between requests (Cloud Run's default) answers its next one
from a read no older than the backstop, and an instance that missed a signal is at most
that far behind. A failed reread is logged, the set stays as it was, and the table is
left alone for one second: lookups in that pause are answered from the set without a
read, so a database that is down is read, and its failure logged, once a pause and not
once a lookup, and the first lookup after the pause tries again. `Reload` records the
change before it reads, so a reload that fails (the request that wrote had ended) is
made good by the next lookup, under the roster's own context. A write of this instance
ends the pause too: the database has just taken a write, so the next lookup reads it,
and a read that fails after the write begins a pause of its own. A database that does
not answer holds a lookup for less than twice the read's timeout (ten seconds): the read
in flight when the lookup came, and at most one after it. A role held in every tenant
domain reaches a new tenant with nothing written for it; the one membership written when a tenant is made is
its first administrator's. `Add` stays for a test harness that seeds a roster without a
table; the generated paths never call it.

**The declaration.** Who may enter a tenant is data on the tenant record, read in two
places. The record declares it with two annotations (section 1): `@directoryOnly` on a
bool column, which says the tenant's people enter through its directory only (no other
sign-in reaches it: not a password, a personal account or another organization's
directory, but for the tenant's own administrator's password with a second factor), and `@tenantDirectory` on a second, tenant-scoped resource
that holds the tenant's directory, the organization its people sign in through, one row
for a tenant, with
`@directoryMethod` on its sign-in method column (`google`, `azure`, `workos`, `oidc`, as the
session library spells them) and `@directoryConnection` on the column carrying the
connection the method's identities record (a Google Workspace by its domain or by the
name the registration gives it, a Microsoft Entra tenant by its tenant id); the
directories record binds to the tenant with the bare `@domain` on the row, never through
a join path, and a second directories record is refused naming the first. Both halves
are the tenant's own to change: its directory under the tenant's grants on the
directories record, and the flag by the method the generator writes in the tenant's
scope ("Directory only", below), never by the tenant record's global write paths; with
`TrackChanges` on the two records, every edit leaves a change event, and no second audit
is needed. The generator then emits, in
the handler package, `New<Record>DeclarationReader()`, a
`signin.DeclarationReader[*spanner.ReadWriteTransaction]` that reads one tenant's
declaration in the sign-in's own transaction: the application hands it to each auth,
whose `New` sets the library's sign-in rule over it
(`signin.TenantPolicy(resource.SignInRoles(engine.UserManager()), reader)`; the auth
whose people may sign up and create a tenant of their own adds
`signin.AdmitWithoutTenant()`), and `resource.SignInRoles`
reads the person's memberships outside the transaction, since the Spanner client refuses
the engine's read under the transaction's context as a nested transaction.
`resource.OutsideSignIn(db, reader)` reads the same declaration outside a sign-in, each
read in a transaction of its own, for the check that says whose password the tenants'
sign-in settings disable (`signin.PasswordsDisabledByTenants`). The roster
keeps the request-time copy: `New<Record>Roster` is built with
`resource.WithTenantDeclarations` naming the columns, `Reload` reads the keys and the
declarations in one read, and `Declarations(ctx, domain)` answers from the copy, nil for
a tenant the roster does not hold, which is what `SessionPermissions` takes as its fifth
argument (section 6): a tenant is offered to a session only when its declaration admits
the way the session was established, or the session's actor is a super administrator.
A declaration restricts its own tenant and nobody else's: a tenant that is not directory
only admits everyone it seats, however they signed in, and its directory closes it to
nobody; it is what seats a stranger at their first sign-in through it (the default
seat, below) and what the tenant can be made only of. An invitation that makes a tenant,
accepted through an organization's directory, and the record's create-your-tenant route
("How a tenant comes to exist", below) each set the directory the person came through on
the new tenant in the row's transaction, beside the person's administrator membership;
the route reloads the roster before it waits on that membership, so the wait's probe asks
what the next request asks.

**No directories.** An application whose resource package declares no `@tenantDirectory`
record has tenants with no directories: no tenant takes one, none is opened to one and
none is made directory only, so a tenant's people come by invitation alone. Where that
package makes the tenant record's rows itself (the record's patch handler is not
suppressed), the generator still emits `New<Record>DeclarationReader()`, over no
columns: `resource.NewTenantDeclarationReader(table, key, nil)` answers nothing for
every tenant, without a read. Each auth's `New` therefore sets `signin.TenantPolicy`
over the generated reader in both kinds of application, and here every tenant admits
everyone it seats, however they signed in. The roster reads no declarations, and
`Declarations` answers nil for every tenant. A package that declares the tenant record
another site serves, with its handlers suppressed, is given no reader: the site that
makes the record's rows is where the directories record is declared or left out.

**One directory, set from a sign-in.** A tenant has one directory, and a directory, its
method and its connection together, belongs to one tenant. The schema holds both with
two unique indexes on the directories table, one on the tenant column and one on the
method and the connection, and the generator refuses a directories record whose table
lacks either, naming the statement to add:

```sql
CREATE UNIQUE INDEX TenantDirectoriesByTenantId ON TenantDirectories(TenantId);
CREATE UNIQUE INDEX TenantDirectoriesByMethodConnection ON TenantDirectories(Method, Connection);
```

The indexes refuse two writes that race, at the commit, where a unique index can only
say that a value exists. The generated write paths refuse first, through
`resource.TenantDirectoryRules`, which the roster file builds for the record
(`New<Record>DirectoryRules`), each refusal a Conflict (409) whose sentence says what is
wrong and what to do, in the record's own word for a tenant:

- a second directory for a tenant: "This tenant already has a directory, Google
  Workspace acme.example. A tenant has one directory: remove it first to set another.";
- a directory another tenant has: "Google Workspace acme.example already has a tenant
  here, and a directory belongs to one tenant. Ask that tenant's administrator to invite
  you." The sentence says the organization has a tenant, which a rule of one tenant for
  a directory cannot keep from being learned; it does not say which;
- a personal account, one of the two values that stand for everyone who is in no
  organization (any Google account, `accounts.google.com`, and Microsoft's personal
  accounts, the consumer tenant id; `signin.Directory.PersonalAccounts`): "A personal
  Google account is not an organization's directory, so it cannot be a tenant's
  directory. Sign in with your organization's account (Google Workspace or Microsoft
  Entra) and try again.";
- removing the directory of a tenant that is directory only: "This tenant is directory
  only: its people sign in through its directory alone, so nobody could enter it
  without one. Turn directory only off first, then remove the directory." Once removed,
  the roles the directory gave stand, and the directory is free for another tenant.

**How a sentence names a directory.** A directory's connection is a key: for a
Microsoft Entra tenant it is the tenant's id, which no person knows. So no sentence a
person reads says the connection itself. `resource.DirectoryWords(directory, namers...)`
says a directory as the middle of a sentence names it, and
`resource.DirectoryName(directory, namers...)` with its kind before a name ("Google
Workspace acme.example", "Microsoft Entra tenant Lakeside"). A namer
(`resource.DirectoryNamer`) answers what one auth's sign-in registrations call the
directory: the name a registration that takes its connection gives it, and the label of
the button a person signs in to it with. The words are the first name any namer gives;
where none names the directory, the first label, as "your organization's Microsoft
directory" for a button labeled Microsoft; and where no namer answers at all (an
application whose user management is not generated, or a directory no registration
takes any more), what the directory alone says: a Google Workspace's connection is its
name or its domain, an OpenID Connect provider's is the application's name for it or its
issuer, said by its host, and any other directory is said by its kind ("your
organization's Microsoft directory", "your organization's directory"). A generated
auth's identity client is a namer (`<Auth>Identity().DirectoryName`, from
`session.AuthAPI.DirectoryName`), and the generated frames pass every auth's:
`New<Record>DirectoryRules().NamedBy(...)` wherever a handler builds the rules, so the
refusals above, the directory-only warning and the open-to-directory warning say the
same words, and `WithDirectoryNames(...)` on the tenant list, for a closed tenant's
reason. Which registrations name a directory is the session library's to say (its
README, "DirectoryName"): a Microsoft Entra tenant has a name only where the
registration lists it with `azure.NamedTenant`.

The caller's grants answer before a rule does: the generated create and delete ask
`PatchSet.Permitted` first, the check `Buffer` opens with (the permission on the record
and on each field the request names), so a caller who holds no grant for the write is
refused with the Forbidden (403) any record answers, and learns nothing a rule's
sentence says.

Nobody types a directory in. The record's method and connection are output-only, so a
request that names either is refused as naming fields the record does not take, and
the generated create (the record's `add`, under its Create grant in the tenant) sets
both from the session: `TenantDirectoryRules.FromSession` answers the directory the
caller is signed in through, held to the rules, and refuses a session that came through
none ("You are signed in with a password. A directory is set by signing in through it:
sign in with your organization's account (Google Workspace or Microsoft Entra), then
make it the directory."). The person who sets a directory is therefore always a member
of the organization; no other proof that a tenant controls it is asked for. The session
read (`GET .../user/session`) carries `directory`, the method and the connection of the
organization's directory the session came through, absent for a password, a personal
account and an impersonation, which is what a tenant's Sign-in page offers its
administrator to make the tenant's ("Make acme.example this tenant's directory") and
what tells it to offer nothing. A write that sets a directory in code, from a sign-in,
takes one step: the generated `Set<Record>Directory(ctx, txn, tenant, directory)`, which
asks the rules and buffers the row through the record's create patch, so its change
event is written with it. The create-your-tenant route calls it with the directory its
creator came through, so a creation from an organization that already has a tenant is
refused with the sentence above and nothing is written, and the maker of an invited
tenant holds the directory the accepting person came through to the same rules; a
caller inside a sign-in hands
it the identity's method and connection over the sign-in's transaction
(`resource.NewSpannerReadWriteTransaction`). The rules read what is committed, on
Spanner; their statements render for PostgreSQL too (`$1`, `$2`, `$3`), where the two
unique indexes hold the rules as they do on Spanner.

**Directory only.** Turning a tenant's directory-only flag on closes the tenant to
everyone who does not sign in through its directory, so the switch is the tenant's own,
turning it on is held to three rules, and the method that turns it on warns before it
runs. For a tenant record that declares the flag and the directories record, the
generator writes two methods in the tenant's scope, served on every session outlet that
serves the record: `Make<Record>DirectoryOnly`, at
`POST /<prefix>/<tenants>/{tenant}/make-<record>-directory-only`, turns the flag on, and
`End<Record>DirectoryOnly`, at
`POST /<prefix>/<tenants>/{tenant}/end-<record>-directory-only`, turns it off. Neither
takes anything in its body (`{}`: the route names the tenant), and `X-Dry-Run: true`
runs the frame and rolls back, as on every method. Both say who may sign in to the
tenant at all, so their actions carry the class `access` (`accesstypes.ClassAccess`)
in the generated collection, as the two methods that say how a tenant admits its
directory's people do (`Open<Record>ToDirectory` and `Make<Record>InviteOnly`, below),
and a session whose mask refuses the class runs neither. The tenant's administrator runs
them: a role file grants Execute on each to the domain role that manages the tenant's
sign-in settings, and the record's own write paths, which are global, show the flag and
never write it (the field is output only on the wire, so a create or an update that
carries it is refused as naming an unknown field, and a role file that grants Create or
Update on it is refused by the deploy-time validation of the role files,
`access.ValidateRoles`, the field requiring neither). `Make<Record>DirectoryOnly` asks
`TenantDirectoryRules.DirectoryOnly` once the caller's Execute grant has answered and
before the flag is written through the record's update patch, and each refusal is a
Conflict (409) whose sentence the person can act on, in the record's own word for a
tenant:

- a tenant that has no directory: "This tenant has no directory, so it cannot be
  directory only: nobody could enter it. Set its directory first: sign in through your
  organization's directory, make it this tenant's directory, then turn directory only
  on.";
- a caller who is not signed in through the tenant's directory themselves: "Directory
  only is turned on by a person who is signed in through this tenant's directory,
  Google Workspace acme.example: that shows the directory works, and that you will not
  shut yourself out. You signed in with a password. Sign in through Google Workspace
  acme.example and try again.";
- a caller whose own account has no password, where their auth has the password method:
  "Directory only is turned on by a person who has a password: an administrator of this
  tenant enters it with a password and a code sent to their email, and that is the way
  back in if the directory has a problem. Choose a password on Your account first, then
  try again." A person who has only ever signed in through the directory has none, and
  chooses one on their account page (the session library's "A person's first
  password"). Where the caller's auth has no password method the rule stands down and
  says nothing: there is no password to have, and such a tenant is opened again by a
  super administrator alone. The rules are told about each auth's passwords by its
  generated identity client (`TenantDirectoryRules.PasswordsBy`,
  `CallerPassword`), which answers for the real person behind the session.

Nobody is exempt from the three, a super administrator included: they are about the
directory and the way back in, not about who may enter. A tenant is directory only from
the start in one case: the invitation that makes it says so, and is then accepted through an
organization's directory and no other way, so the tenant is made with the flag on and
that directory as its own in one transaction ("How a tenant comes to exist", below). The
create-your-tenant route makes no tenant with the flag on. Turning the
flag off, and a call that leaves it on, are plain: no rule holds them. While the flag
is on, the tenant's routes, these two among them, are served to the people the tenant
admits: a person signed in through its directory, a super administrator, and the
tenant's own administrator who signed in with a password and the code sent to their
email. So a tenant whose directory has stopped working is opened again by one of its
own administrators, who turns the flag off or sets the directory right, or by a super
administrator; to an administrator who signed in any other way, and to one whose
password session carries no second factor, the tenant answers as one that does not
exist. The first two rules and the rule that
keeps a directory-only tenant's directory read what is committed, and a transaction's
reads do not see its own writes, so the library's transaction notes each write the rules
pass: one transaction that turns the flag on and removes the tenant's directory is
refused, whichever comes first ("This same request removes this tenant's directory, so
it cannot also turn directory only on: nobody could enter the tenant. Keep the
directory, or leave directory only off."); each method is a request of its own, so over
the wire the two are always two requests, each held to what the other committed, and the
note holds an application's own code that does both in one transaction. After the commit
each method reloads this instance's roster and signals the tenants kind, so the next
request on every instance is decided by the flag. What the flag does once it is on has two exemptions: the super
administrator, who enters every tenant (section 6), and the tenant's own administrators,
who enter their own tenant with a password and a second factor. A person seated through
a role held in every tenant, an administrator's role among them, is kept out like anyone
else, which is the tenant's way to close itself to the application's own people all at
once.

`Make<Record>DirectoryOnly` declares a warning (section 7), so the dialog a page runs it
through shows what the call would do before anything is saved, and holds its button
until the person has typed the word `close` (`resource.DirectoryOnlyWord`: a warning's
word is one fixed lowercase word, which a directory's name is not, so it is the
same for every tenant). Asked for its warning (`X-Warning: true`), the method answers in
place of running (`TenantDirectoryRules.DirectoryOnlyWarning`), naming the directory in
the words a person knows it by ("How a sentence names a directory", above: here a
Workspace by its domain; a Microsoft Entra tenant its registration does not name reads
"Only people who sign in through your organization's Microsoft directory will be able
to enter this tenant."):

```json
{
  "consequence": "Only people who sign in through acme.example will be able to enter this tenant. An administrator of this tenant who has a password can still sign in with it and a code sent to their email. 2 people seated here sign in another way and will lose access.",
  "names": ["Gale Ferris (gale)", "hollis"],
  "also": [{"says": "2 administrators of this tenant have a password and can still enter without the directory. Take the role away first from anyone who should not keep that way in.", "names": ["Ines Okoro (ines)", "wren"]}, {"says": "1 invitation that has not been accepted gives a role that administers this tenant. Whoever accepts it can choose a password and enter without the directory. Withdraw it first if it should not stand.", "names": ["kit@example.com"]}],
  "confirm": "Admit only people who sign in through acme.example"
}
```

The sentence counts everyone seated in the tenant, in the tenant itself or in every
tenant, who has no sign-in through its directory, in every auth whose people the tenant
seats (each auth's generated identity client answers its own, `DirectoryOutsiders`); a
super administrator is not among them, and neither is an administrator of the tenant
who has a password, who does not lose access: the second sentence says how they still
get in, and is there where an auth whose people the tenant seats has the password
method (`DirectoryPeople.AdministratorsEnter`). It is a general sentence; who those
administrators are is said in `also`. `names` lists the people who lose access, each by their name and
sign-in name, only to someone who may list them: the people of an auth are named where
the caller holds List, in the tenant, on the tenant's list of that auth's people
(section 21) and, on no condition, on its `username` and `name` fields, and counted
alone where the caller does not. Where the list is shorter than the count the sentence
says so ("The one of them you may list is named here.", "The 3 of them you may list are
named here."), so a part of them is never read as all of them, and where the caller may
list nobody the warning carries no names and a page says how many without saying who.
An application whose people no generated management lists counts nobody, and the
sentence then gives no number ("Anyone seated here who signs in another way will lose
access."): a count nobody made is never read as nobody.

Where the tenant's administrators keep that way in, `also` says who has it, so the person
closing the tenant sees them first. Its first note counts the tenant's own administrators
who have a password (each auth's `DirectoryAdministrators`, the people list's
`AdministratorWithPassword`), and its second the invitations into the tenant that have not
been accepted and give one of its administrator roles (`DirectoryInvitations`). An
administrator is named to a caller who may list the tenant's people, as above, and an
invitation's address to a caller who holds List, in the tenant, on the tenant's list of
invitations and, on no condition, on its `email` and `name` fields; anyone else reads the
count. A note with nobody to count is left out, and so are both where no auth has the
password method.

The warning is refused as the call is refused, with the same sentences, so a page that
asks for it first shows the refusal and opens no dialog. For a
tenant that is directory only already there is nothing to turn on, and the warning is a
Conflict that says so and names neither the directory nor the people ("This tenant is
directory only already: only people who sign in through its directory enter it. There is
nothing to turn on."); who the flag keeps out now is the people list's to say, below.
`End<Record>DirectoryOnly` declares no warning, and a request for one is refused (400)
and does not run.

The switch as it stands is read in the tenant too:
`GET /<prefix>/<tenants>/{tenant}/directory-only` answers `{"directoryOnly": false}`
under the Execute grant of either method in the tenant, held without a condition (a read
has no request for a condition to judge, so it is Forbidden under a grant that holds on
one), and writes nothing (`resource.DirectoryOnlyStateHandler`). The tenant's
administrator does not read the global record, and this is what a Sign-in page shows
them.

While the switch is off and the tenant has a directory, the read also says what turning
it on takes of the caller (`conditions`, `resource.DirectoryOnlyCondition`), in the
order the call checks: the sign-in through the tenant's directory, then the password
where the caller's auth has the password method. Each is the `sentence` the refusal
above opens with, whether the caller meets it (`met`), and, where they do not, what they
do (`toDo`): the rest of that refusal. The call refuses with `sentence`, a space and
`toDo` of the first one not met, built from the same parts, so the words are held in one
place: a page shows the read's sentences before the person presses, and holds no copy of
its own. The generated read is given the rules the two methods are given, each auth's
`CallerPassword` with them (`PasswordsBy`), and stamps the caller it checked before it
asks them; rules that ask no auth say the first condition alone, though the call still
refuses for the second.

```json
{
  "directoryOnly": false,
  "conditions": [
    {
      "sentence": "Directory only is turned on by a person who is signed in through this tenant's directory, Google Workspace acme.example: that shows the directory works, and that you will not shut yourself out.",
      "met": true
    },
    {
      "sentence": "Directory only is turned on by a person who has a password: an administrator of this tenant enters it with a password and a code sent to their email, and that is the way back in if the directory has a problem.",
      "met": false,
      "toDo": "Choose a password on Your account first, then try again."
    }
  ]
}
```

A tenant with no directory is refused before anything is asked of the person, and a
tenant that is directory only already has nothing to turn on, so the read names no
condition for either. The browser client's `directoryOnlyState(api, tenant)` is the typed read, and the
methods are handles of the tenant's client
(`api.domain(tenant).make<Record>DirectoryOnly`, `.end<Record>DirectoryOnly`); the
library's Sign-in settings page runs each through its method dialog
(`openMethodDialog`), which draws the warning with its list of people and the typed
word, and turning the flag off is a plain confirmation. The page offers the method that
turns it on only where the tenant has a directory and the person meets every condition
of the read, and shows in its place each condition they do not meet.

The read also says what a page of the tenant's sign-in settings shows
(`DirectoryOnlyState`): `Directory`, the tenant's directory as people know it;
`DefaultRole`, the role a person gets the first time they sign in through it, in words;
`Passwords`, whether the caller's auth has a password method; `SignedIn`, a sentence
that says how the caller signed in; `SessionDirectory`, the organization's directory the
caller is signed in through; and `People`, how many people hold a role in the tenant
(`Seated`) and how many of them the switch keeps out (`Outside`), counted as the warning
counts them. The generated rules are told who counts (`CountedBy`): each auth's identity
client answers how many people the tenant seats (`SeatedCount`) and who the switch keeps
out (`DirectoryOutsiders`).

The tenant's own list of its people (section 21) is made from who holds a role in the
tenant, never from who can sign in, so a person the flag keeps out stays in it with
their roles. Where the record declares the flag the list carries one more field,
`cannotEnter`: while the tenant is directory only, the row of a person who has no
sign-in through the tenant's directory says so ("Has not signed in through this
tenant's directory, so cannot enter while this tenant is directory only"), and it is
empty for everyone else and while the flag is off. It is empty too for an administrator
of the tenant who has a password, who enters with it and a code sent to their email:
the list, the count the warning gives and the sign-in agree on who that is (an
administrator's role held in the tenant itself, by a membership of the person's own or
through a group kept in the tenant, inside its period; the roles are the ones the
permission engine was told, `access.WithTenantAdministratorRoles`). The list reads
whether the person has a password and not whether a session of theirs carries the
second factor, which is asked at the sign-in. The sentence speaks of the tenant's
directory and does not name it: the list is a query, which reads the directory's
connection and not what the sign-in registrations call it, and a connection is never
said to a person. The library's People page shows it as a column
("Cannot enter") to a role that is granted the field, so the tenant's administrator sees
who the flag keeps out for as long as it is on.

**The default seat.** What a person gets at their first sign-in through a tenant's
directory is the directory row's to say, before the method's account rule is reached.
The directories record may carry one more column, `@directoryDefaultSeat` (section 1), a
nullable string naming a role of the tenant. A person no user exists for who signs in
through a tenant's directory is created and seated with that role in the tenant, and
signed in; where the row names none (NULL, the default, and every row of a record
without the column), they are refused with the `invitation_only` refusal and no account
is created, unless they followed an invitation link, which is the way past. A person
whose account stood before, made by another tenant's invitation or before the tenant
set its directory or named a role, is given the role too: the sign-in that links their
account to the directory is their first through it, whether the directory's identity
is linked at once or after their password confirmed it, and the link gives the default
role where they hold no role in the tenant. Who is the directory's person is decided by
how they signed in, the link between the account and the directory, never by their
address. The role is given once for a link: a second sign-in finds the link and gives
nothing, so a person whose role an administrator removes is not seated again by signing
in, and an administrator who wants one person out removes the role and leaves the link,
or disables the account. A link that is deleted and made again is a first sign-in
again, and gives the default role where the person holds no role in the tenant. A
person whose link was made before the tenant named a default role is given a role by
hand. A directory no tenant has falls to what was chosen for the method (invite only,
or open to sign-up: section 21, "Who may sign up"), and so does a personal account,
which is no tenant's directory. The generator emits
`New<Record>DirectoryReader()`, a `signin.DirectoryReader[*spanner.ReadWriteTransaction]`
reading the tenant whose directory an identity came through with its seat in the
sign-in's own transaction, and the application hands it, with the seat hook, to each
auth whose roles are the application's own:
`authSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())`.
An auth whose directory names the roles (a registration that syncs them) sets no
admission: its directory is the authority on who holds what, and a default seat would
be overwritten at the next sign-in.
The role is written the way an accepted invitation's roles are, by the Auth's generated
identity client in the transaction that links the account to the directory (section 21,
"At a sign-in"), so the link and its role are committed together or not at all, and the
sign-in
policy, which decides after the commit, finds the membership.

How a tenant admits its directory's people is the tenant's own to say, as the switch
is: the column is output only on the wire, so the record's create and update never
write it, and the generator writes two methods in the tenant's scope, served on every
session outlet that serves the directories record.
`Open<Record>ToDirectory`, at
`POST /<prefix>/<tenants>/{tenant}/open-<record>-to-directory` with `{"role": "Member"}`,
names the role, and `Make<Record>InviteOnly`, at
`POST /<prefix>/<tenants>/{tenant}/make-<record>-invite-only` with `{}`, clears it, so
an empty default seat is invite only, as it always was. A role file grants Execute on
each to the role that manages the tenant's sign-in settings. Opening a tenant lets in
people nobody named, so `Open<Record>ToDirectory` declares a warning (section 7), which
a person confirms by typing `open` (`resource.OpenToDirectoryWord`). Its words are the
server's, for the role as picked so far (`TenantDirectoryRules.OpenToDirectoryWarning`):
"Anyone who signs in through acme.example will be able to enter this tenant without an
invitation and gets the role Night shift lead.", the role in words (`NightShiftLead` is
what is stored), under the button "Open to people who sign in through acme.example";
before a role is picked the sentence ends "Choose the role each of them gets." The
directory is named as the directory-only warning names it, never by a connection that
is an id. The role is held to what giving it by hand needs
(`identity.Manager.DirectoryRole`, through the generated identity client): it is a role
of the tenant ("... is not a role of this tenant. Choose one of the roles the picker
lists.", 400), and the caller could give it there directly, by the grant of the method
that gives a role in a tenant, conditions included (403, saying which role and what to
do; the grant answers before the role is looked for, so the refusal is the same for a
role the tenant holds and one it does not, and tells nobody of a role the grant keeps
from them). A default role is given to people who are not known yet, so the grant is judged
for no person: a grant whose condition reads the person a role is given to admits no
default role. A tenant with no directory is refused (409): there is nobody to open it
to. `Make<Record>InviteOnly` is plain and declares no warning. Each method writes the
column through the directories record's update patch, so the record's change events
are the audit of who opened the door and when. Both give or take away what people may
do, so their actions carry the class `access` (`accesstypes.ClassAccess`) in the
generated collection, as the generated management's role and group methods do (section
21), and a session whose mask refuses the class runs neither. The role is picked from a list of its own,
`<Auth><Record>DirectoryRoles` (Lodestar's `CrewSectorDirectoryRoles`), a computed
resource the generated management declares for the auth that judges the role: the roles
of the tenant that the caller could give there directly
(`identity.Manager.DirectoryRoles`, which asks the same grant for each role), and no
other. The browser metadata names it as the role's picker, so the library's method
dialog offers each role the method would take, with its description, and none it would
refuse; a role file grants List on it to the role that holds the method, and writes no
condition on it, since the grant of the method that gives a role is what the list reads.

The role is judged through the generated management of one auth, so the two methods
are written where the session outlets that serve the directories record are all of one
auth, the program generates that auth's management (`GenerateIdentity`), and the auth's
roles are given by hand. The judgment is the one a method opened to sign-up is held to
(section 21, "Who may sign up"): the role file's grant of the method that gives a role,
which the generated management answers (`grants.go`) for an auth whose directories
record carries the default seat, as it does for one that keeps groups or opens its
methods to sign-up. Where they are not, the program writes neither method, and the
application sets the column in its own code. One consequence:
while a method is open to sign-up, a colleague of a tenant's creator who signs in from
the directory that tenant already has gets no account and no second tenant, but the
invitation-only answer, or the tenant's default role where its row names one: the
tenant's row decides before the method's does.

**How a tenant comes to exist.** Nobody creates a tenant directly. The tenant record's
create operation is refused in the standalone patch handler and in the consolidated
dispatcher (400, "a sector is not created directly: invite one person, and the sector is
made when that person first signs in"), the record's metadata tells the browser client
so (`createDisabled`, so the generic list draws no Create button and the client's handle
has no `create`), and there are two ways a tenant is made, neither a declaration:

- **Staff invite one person.** Where the record is served by session outlets of one
  auth, the program generates that auth's management and no directory writes its roles,
  the generator writes three pieces into that auth's management (section 21, "The
  invitation that makes a tenant"): `Invite<Auth><Record>`, which staff send with the
  person's address and the tenant's details; the list `<Auth>Invited<Record>s` of the
  invitations that still stand; and `Withdraw<Auth>Invited<Record>`. Sending one takes
  the method's grant and the Create grant on the tenant record, on the record and on
  each field the invitation carries. No tenant exists while the invitation waits. The
  first sign-in of the person who follows the mailed link makes the tenant from the
  details, under an id generated then, seats the person as its first administrator and,
  where they signed in through their organization's directory, makes that directory the
  tenant's own, in the transaction that consumes the invitation: all of it or none.
- **A person who signs up creates their own.** Under the same conditions the generator
  writes the create-your-tenant route (`POST <prefix>/create-<record>`, no grant
  needed), which a signed-in person holding no tenant calls with the new tenant's
  fields. Whether anyone may is no declaration: the route reads, in the transaction that
  writes the row, whether an administrator has a sign-in method of the auth open to
  sign-up (section 21, "Who may sign up"), and refuses while every method is invite
  only. The same route answers `GET` with whether the caller may create one now
  (`{"offered": true}`, the generated client's `Create<Record>Offered`), by the four
  facts the creation refuses on: the session's mask does not refuse the creation, the
  caller is not the real person behind a session that acts as a role (both under "The
  first administrator", below), the caller holds no tenant, and a method is open. Whether
  the caller holds a tenant is read from every tenant they hold a grant in
  (`HeldDomains`, a method of `resource.UserPermissions`), and not from the tenants the
  session lists: a tenant that a session's mask leaves nothing of, or that is closed to
  the way the session signed in, is still the person's, so neither opens the way to a
  second one. Every permissions value answers it, since the method is part of the
  interface: a type of the application's that embeds the composed value passes the call
  through, and one that implements the interface itself does not compile until it
  answers, from the person's grants before any mask and never from `Domains`. In a
  session that runs with a role's grants the person is the real person behind it, and
  the list is theirs and not the role's. A page reads the answer before it offers the
  creation, so nobody is shown a dialog the server would refuse; the skeletons' home
  page shows its create-your-tenant card only while the answer is yes.
  The `POST` asks the rule twice. The permissions answer before a body is read, as the
  `GET` does. The auth's tables answer again in the transaction that writes the tenant's
  row and its administrator's membership (`<Auth>Identity().HoldsTenant`,
  `identity.Manager.HoldsTenant`): a role the person holds in one tenant, of their own
  or through a group kept in one, whose period has not ended. So of two creations one
  person sends at once, one is written and the other is refused with the words a second
  creation is always refused in, and a creation an instance has not heard of yet still
  counts. The second read is of the rows and not of what they grant: a role in a tenant
  whose period has not begun, or one that grants nothing, refuses a creation there that
  the first read would have let through.

`@tenant(creation: ...)`, which declared the choice in code, is refused at generation
with these words. A record served by session outlets bound to different auths is refused
at generation, since a tenant is made through one auth's client, and so is a program
that does not generate that auth's management (`GenerateIdentity`). A record served by
no session auth (a hand-written router, a machine outlet alone), or by an auth whose
roles a directory writes, has neither way: its create answers "a sector is not created
directly: the application makes one in its own code", and the application writes the
row through the record's create patch in a transaction of its own, then reloads the
roster.

**The first administrator.** The person who makes a tenant is its first administrator,
whichever way it is made, and the tenant and the membership are committed together or
not at all. An accepted invitation gives the person the roles that administer a tenant
in the auth (`identity.WithAdministratorRoles`, the domain roles the auth names as its
administrator's), written by the auth's generated identity client in the transaction
that consumes the invitation. The create-your-tenant route gives its caller the roles
`<Record>AdministratorRoles()` names (asserted against the application type in the
generated file of the route), through the same client (`<Auth>Identity().AssignRole`,
section 21), in the transaction that writes the tenant's row; a role that does not
exist fails the creation. After the route's commit, and once this instance's roster
lists the tenant, the answer waits until the permission engine answers the membership,
so the person's next request stands in the tenant; an engine that does not answer
within ten seconds is logged and the request answered, since the rows are committed.
In a session that acts as a role, the caller is the real person behind it, who would
become the tenant's administrator: nobody is the subject of what they do while they act
as someone else (section 21), so the route refuses them before it reads the body, with
nothing written ("You are acting as someone else, so you cannot create a sector of your
own."), and its `GET` answers that none is offered. In a session that acts as a person,
the person acted as would become the tenant's administrator, and the mask decides. No
grant admits the route, so no permission is checked there for a mask to deny; the route
carries an action's name instead, `Create<Record>` (Lodestar's `CreateSector`), which
the generated collection registers in the global scope with the class `access`
(`accesstypes.ClassAccess`), and asks the session's mask about it before anything else
(`resource.RefuseActionUnderMask`). A session whose mask refuses the class `access`,
refuses the action by name or keeps no Execute is refused with nothing written ("You
are acting as someone else, and this session cannot create a sector."; why the mask
refuses it is said in the server's log alone), the denial is logged as the mask's, in
the line the masked check writes for a denial of its own, and the route's `GET` answers
that none is offered (`resource.MaskRefusesAction`). A session that acts as a person with no mask
makes the tenant, and the person acted as is its administrator: to let someone act as
a person with no mask is to give them what the person holds. One tenant per person holds
in such a session as in the person's own, whatever its mask shows: a mask that keeps the
creation and leaves nothing of the person's grants in their tenant takes the tenant off
the session's list and not off the person, and the route counts the person's
(`resource.UserPermissions.HeldDomains`). The name is for masks
alone, and the collection says so: the generator registers it as an action no role is
given (`NoRole: true` in the generated collection file,
`resource.CollectionResource.NoRole`). Nothing asks a role for it, so a grant of it
would do nothing and its absence refuses nobody. A role file that names it is refused
wherever the file is validated, in words that say why (the permission engine at start,
`access.ValidateRoles`, and the `roles-file` check of `impulse check`, which reads the
mark from the generated collection file); the browser constants (`Methods` in
`zz_gen_constants.ts`) leave it out on every outlet, so no page that lists what a
person may be shown or given names it; and a registration made by hand under the name
is refused at generation. The collection still answers for the name where a mask is
declared and where the masked check asks (`Scope`, `HasPermission`, `ActionClasses`),
so a kind refuses it by its class or by name.

**The maker.** What makes a tenant from an accepted invitation is generated beside the
roster: `New<Record>Maker(roster, liveService)` in the handler package answers an
`identity.TenantMaker` over the record's own create patch, so the record's rules apply
to a tenant an invitation makes and its change event is written with it. The auth a
tenant is made through takes it: that auth's generated `NewClient` has it as a required
argument (section 21), and the application hands it in where it builds the auth, so the
composition root builds the roster before that auth:

```go
tenants := app.NewSectorRoster(db.ResourceClient, resource.WithTenantSignals(liveService))
crewAuth, err := crew.New(ctx, db.SpannerClient, &crew.Settings{
    // ...
    TenantMaker: app.NewSectorMaker(tenants, liveService),
})
```

The auth packages `impulse` renders carry the field (`Settings.TenantMaker`), refuse a
nil one at the start with a sentence, and pass it to the client. Once the sign-in's
transaction has committed, the maker has this instance's roster reread its table and
signals the tenants kind, so the person's first request stands in the new tenant and
every instance serves it at once.

**Deleting a tenant.** The record's generated delete clears what each auth holds for the
tenant in the transaction that deletes its row, through the generated identity client of
every auth whose management the program generates (`<Auth>Identity().ClearTenant`,
`identity.Manager.ClearTenant`): every membership held in the tenant, the custom roles
made in it with their grants, and the invitations into it that still stand, which are
withdrawn. A person who held roles in the tenant alone then holds none anywhere and is
deleted with their identity links and sessions, by the rule on an account nothing admits
(section 21, "The rules"); a person who holds a role anywhere else stays, and so does a
role held in every tenant, which is no one tenant's. The permission engine is told once
the delete has committed.

The rows under the tenant go with it in the same transaction, and the library deletes
them in code (section 10, "A delete that takes what is under the record"): every row of a
resource of the application's that carries the tenant's key or leads to a row that does,
each through its kind's own delete, so its delete rule (`@validateDeleteType`) is asked,
its change event is written and its stored files are released. The tenant's directory is
one of those rows and goes with the rest, whether the tenant is directory only or not,
and is free for another tenant afterwards; the rule that keeps the directory of a
directory-only tenant is for a tenant that stays and is not asked. No grant is asked on a
row: the grant to delete a tenant answers for everything under it, and a person who may
delete a tenant need hold no role in it. A row of another tenant that names one of the
tenant's rows is not under the tenant and is not taken (section 10, the same part).

A tenant that has such rows is not deleted by a request that does not say so. The remove
is answered 428 with the question a person is asked first (`Delete this tenant and
everything under it? This can't be undone.`), and nothing is gone; the request that goes
ahead carries `"value": {"everythingUnder": true}` on the remove operation, and the
library's delete dialog sends it when the person presses Delete at the question. The
question counts the rows of each kind the person may list in the tenant being deleted
(`Delete this tenant and everything under it: 3 projects, 12 tasks. This can't be
undone.`). A tenant's delete is asked for the whole application, and the rows under the
tenant are the tenant's, so each kind of them is asked about in that tenant: a person
whose roles list a kind there reads its count, and a kind they may not list there is
neither named nor counted (`, and records you can't see.` after the kinds they may list,
or the question alone where they may list none). A tenant with nothing under it is
deleted by the plain remove. A table of the application's that no resource
describes is not a row the library can delete: its foreign key refuses the tenant's
delete while its rows remain, so the application deletes them first, in code. An
application that deletes a tenant in a transaction of its own buffers the record's
generated delete patch with `WithEverythingUnder()` and calls `ClearTenant` there, and a
program that generates no auth's management removes the memberships itself.

Each thing the delete takes is an action of the class `access` when it is done alone (a
role removed, a group deleted, an invitation withdrawn), so the generator gives the
tenant record's delete that class. A session whose mask refuses the class, a person
acting as someone else under a kind that is not to change who may do what, cannot delete
a tenant, whatever the person acted as may do; a kind that is to keep it names it as an
exception (`accesstypes.ExceptPermission`).

With the last-owner rule (section 21, "The rules") an operator brings a customer's
tenant in by inviting its first administrator and holds no role in it: the tenant is its
administrator's from its first moment, and its last administrator cannot leave it with
nobody to give the next role.

**Tests.** The generated authorization matrix drives domain-scoped routes with one
tenant, the generated router's `TestDomain` constant (an id's text), and the
application's `newTestHandler` adds it to the test application's roster with `Add`
(`roster.Add(router.TestDomain)`), since the empty schema holds no tenant row; under
concealed domains a case with no grants has no foothold and is answered as if the domain
did not exist.

## 17. The served browser app

A built browser application is served by one type, `resource.BrowserApp`, from the
directory the build wrote into and under the mount path the application's router
declares (`generation.WebApp`, section 8): `/` for the application at the root,
`/console` for one under a path. `NewBrowserApp(dir, mountPath)` builds it, and the
application's two handlers delegate to it, one line each:

```go
// DeepLink rewrites the console's Angular routes to its entry document.
func (a *App) DeepLink(next http.Handler) http.Handler {
	return a.console.DeepLink(next)
}

// Assets serves the console's built Angular application.
func (a *App) Assets() http.HandlerFunc {
	return a.console.Assets()
}
```

**The deep-link rule.** A request whose last path segment has no extension, Angular
matrix parameters (`;key=value`) removed, is an application route: `DeepLink` rewrites it
to `<mount>/index.html` and passes it on, so a bookmarked or reloaded route loads the
application. A path with an extension passes through unchanged and is a file or a 404.
`Assets` strips the mount prefix, serves the file from the directory, and answers 404 for
a missing file, a path outside the mount, and a directory; a directory is never listed.

**Two cache classes, by file name.** The build stamps most of its files with a content
hash, eight uppercase letters or digits before the extension (`main-ZPJWNJT4.js`,
`chunk-3ANMYK5A.js`, `styles-A4ABYBXD.css`, `media/<name>-<hash>.woff2`). Such a file
answers `Cache-Control: public, max-age=31536000, immutable`: its name changes with its
content, so the service worker and any cache between the browser and the server may keep
it for a year without asking. Every other file answers `Cache-Control: no-cache`:
`index.html`, `ngsw.json`, `ngsw-worker.js`, `safety-worker.js`, `worker-basic.min.js`,
`manifest.webmanifest`, the favicon, the icons, `prerendered-routes.json` and
`3rdpartylicenses.txt` keep their names across builds, so a copy may be kept but must be
revalidated before each use, and an unchanged file answers 304. The validator is a strong
`ETag` over the file's content, beside `Last-Modified`, because the files ride in a
container image whose modification times are the build's. The two classes are what make
the worker safe under any cache: a cache that kept a stale `ngsw.json` or entry document
would hand the worker a build whose hashed files the server no longer has, and a cache
that asked again for every hashed file would make the worker's prefetch of a build cost a
round trip per file. `manifest.webmanifest` answers `Content-Type:
application/manifest+json`, which Go's type table lacks.

**The kill switch.** A build deployed without `ngsw.json` makes every installed worker
unregister itself and drop its caches: the worker fetches `ngsw.json` after each page
load and on each of its checks, and a 404 there is its signal to stand down, after which
the page is served by the network alone.

## 18. Renaming a field or a method

A field or a method can change its name without breaking the browser applications already
built against the old one. `@formerly` (section 1) keeps the old name answered beside the
new one, and the outlet's oldest answered release (section 8) says when the old name can go.

**What moves and what stays.** The column never changes: `@formerly` moves the wire name
only, so no migration runs and no row is rewritten. On a field, the generated request
structs carry `formerly:"<old wire name>"` (section 3) and the runtime reads the old name
as the new one: a create or patch body naming `title` writes the `Headline` column, a body
naming both `title` and `headline` is refused with a 400 saying to send one, and
`columns=title`, `sort=title` and `filter=title:eq:...` run against the live field with its
permission checks. Every row carries both keys, `headline` and `title`, each masked when
the field is. On a method, the router registers the former route on the same handler, so
`POST /console/api/release` and `POST /console/api/publish` run the one method, and the
generated router test drives both. The generated collection carries the former names
(`TagData.Formerly` on a field, `CollectionResource.Formerly` on a method), and the
permission digest mirrors a renamed field's or method's entry under its former name, so an
application built before the rename keeps its column or its action. The TypeScript knows
only the new name: an
application built from it sends and reads `headline`, and only an application built before
the rename still sends `title`.

**Two releases.** Release 1 renames the field and keeps the old name answered:

```go
// @formerly(Title)
Headline string `spanner:"Headline"`
```

Applications built before release 1 keep sending `title` and keep working; applications
built from release 1 send `headline`. Release 2 removes the annotation and raises the
outlet's oldest answered release to release 1 (`generation.OldestAnswered("1.0.0")` where
release 1 is `1.0.0`): an application built before release 1 is answered with a 412
telling it to reload, and `title` leaves the wire. A method renames the same way, with
`@formerly(Release)` on the `Publish` struct in release 1 and the annotation gone in
release 2.

**Cursors.** A page cursor fingerprints its order by the Go field names (section 4), so a
cursor an application holds across release 1 over an order that names the renamed field is
refused once, and that list restarts from its first page; a cursor over any other order
carries across.

## 19. Scheduled methods

A scheduled method is an RPC method Cloud Scheduler calls on a schedule, not a person:
pruning old rows every night, sending a digest every morning. It is an `@rpc` struct with
`@schedule` (section 1) and an `Execute` in either form, and it runs on the service itself;
a schedule never starts a job process. Lodestar's:

```go
// @rpc
// @schedule("30 3 * * *", zone: "America/Denver")
PruneDroidReports struct{}
```

**What a scheduled method is.** It takes no input, since the scheduler sends no body and
names no row or tenant: the struct declares no field, and nothing that adds a path
parameter or a body is admitted. It checks no permission and is in no outlet: the
scheduler's token is its gate, so no role names it, the permission collection and the
digest leave it out, and no TypeScript client carries it. `Execute` runs as the
application, with no caller stamped on its context, so `Enforce` and `Check` refuse inside
it and its queries and patches run trusted; it records its writes under a process
(`resource.ProcessEvent`) rather than a person. The frame is a method's otherwise: the
transaction form runs inside the handler's transaction, the rows it writes are published to
the live pages after the commit, and it answers with its result or its chosen status
(`@answers`), which the scheduler records as the call's success or failure. There is no
dry run.

**The route.** The generated router mounts every scheduled method under one prefix it
reserves, `/_scheduled` (`scheduled.Prefix`), one `POST` each at the method's name in kebab
case: `/_scheduled/prune-droid-reports`. Its group is `NoCaching`, `CompressionMiddleware`
and `SchedulerAuth`, and nothing else: no session handling, no XSRF guard, no version
check, no hook. Under the prefix an unknown path is 404, as under an outlet's. No outlet's
prefix and no browser application's mount path may sit at or under it; each is a generation
error. The route tables carry `GeneratedScheduledHandlers` and `generatedScheduledRoutes`
beside the outlets', `Handlers` embeds the one and declares `SchedulerAuth(next
http.Handler) http.Handler`, the chain comment names the group, and the generated test
drives each scheduled route through `New` behind exactly that chain. `NewTestRouter` does
not mount them.

**The check.** The service is open to the load balancer, so Cloud Run's own permission
check lets every caller through, and the check is the application's. Cloud Scheduler calls
with an OpenID Connect token (an OIDC token: a JSON web token Google signs) minted for the
invoker identity, a service account the application's stack creates for its schedules, with
the route's URL as the token's audience. `resource/scheduled` holds the check: a `Guard`
admits a call whose `Authorization: Bearer` token verifies against Google's signing keys
(`google.golang.org/api/idtoken`: the signature and the expiry) for the audience
`https://<host the call names><path>`, was issued by Google, and carries the invoker
identity as its verified email. Any other call answers 401 Unauthorized, and the reason
(no token, a token that does not verify for this URL, another issuer, an unverified email,
another identity) is logged, never written into the answer. The stack hands the service
the invoker's email in `APP_SCHEDULER_INVOKER` (`scheduled.InvokerVariable`), which the
framework reads itself, as it reads `APP_MAINTENANCE`, so no configuration level declares
it: `scheduled.FromEnvironment(ctx)` builds the guard when the application starts, and with
the variable empty, as in development and in a pull-request stack, the scheduled routes are
off, the start logs so, and every scheduled call is refused. The application's
`SchedulerAuth` delegates to the guard (`return a.scheduler.Middleware(next)`); a nil guard
refuses every call too. `scheduled.NewFake()` stands in for Google's keys in a test: a
token it mints for a URL and an email is one Cloud Scheduler would present.

**The schedule.** The generator writes each scheduled route with its schedule and zone into
the release file (section 8), and that is where the application's stack reads them: bedrock
renders one Cloud Scheduler job per scheduled route in every environment, never in a
pull-request stack, calling `https://<the environment's canonical hostname><path>` with a
token of the invoker identity it creates, and sets `APP_SCHEDULER_INVOKER` on the service.

## 20. The provider drivers

The database, the live service and the job starter open through provider drivers, one
package per provider in the shape the cloud driver set: `resource/database/spanner` and
`resource/database/postgres` (the database), `resource/live/firestore` (the live
service) and `resource/jobs/cloudrun` (the job starter), beside the cloud driver,
`cloud/gcp`. Every driver exports the same names: `Settings`, the env-tagged variables
it reads, which the application's configuration embeds; `Open`, which builds the driver
from them; a `Driver` (the live driver's is its `Service`) with `Close`; and a
`declaration` package beside it that publishes the settings' declaration for the tools
that read an application without loading it (impulse's checks, bedrock's stack).

The application binds each driver under one neutral import alias, one per kind of
driver: `database`, `liveservice` (`live` is the seam package, `resource/live`, which
the same file imports), `jobstarter` (`jobs` is `resource/jobs`, the starter's
interface) and `cloud`. Named so, the application names the vendor once, on the import
line, and moving to another provider's driver is that line alone:

    import database "github.com/impulseframework/impulse/resource/database/spanner"   // the only line that changes for PostgreSQL

    type DatabaseSettings struct{ database.Settings }
    db, err := database.Open(ctx, env.Database.Settings)

The wrappers the configuration declares follow the kind, not the vendor
(`DatabaseSettings` and `LiveSettings`, the config fields `Database` and `Live`, the
accessor `Database()`, `LoadDatabaseSettings`), and a local variable never shadows an
alias (`db, err := database.Open(...)`). The two database drivers take the same `Open`
(`Open(ctx, settings, opts ...resource.ClientOption)`, the file store options of
section 13), and the live driver resolves its own emulator project
(`firestore.EmulatorProject`), so nothing but the import line differs between two
applications on two providers. What stays vendor-specific is the Spanner client the
database driver also holds (`Driver.SpannerClient`), which the session store and the
permission engine open on until the PostgreSQL client lands; the data
level names that seam in one comment. Lodestar
([data.go](../lodestar/pkg/config/data.go), [site.go](../lodestar/pkg/config/site.go)) and
the skeleton candidates are the examples.

**The isolation level on Spanner.** Every read-write transaction the library runs on
the Spanner client names the serializable isolation level: the resource client's
(`SpannerClient.ExecuteFunc`, and so every generated handler and every `@rpc` method in
the transaction form), the session store's, the permission store's, the persona store's
and the migration runner's DML groups. The library decides its rules by reading before
it writes: a sign-in reads the user's row and inserts a session, a delete counts the
rows under the record, taking the role from an auth's last super administrator reads who
else holds it. Each holds only while Spanner serializes the read with what another
transaction writes at the same moment. At repeatable read only two writes of one row
conflict, and two such transactions would both commit. So a level set for the client
(`spanner.ClientConfig.TransactionOptions`) reaches none of the library's transactions.
It applies to the transactions the application opens on the client itself
(`Driver.SpannerClient`), and to `spxscan.UpdateAndGet` and `spxscan.UpdateAndSelect`,
which run the application's own statement.

## 21. User management: an auth's management as generated code

`generation.GenerateIdentity(treeDir)` writes the management of every Auth the program
declares as code of the application: a resource declaration over each table the Auth
keeps, and the methods that change them. The lists are read through the application's
permission surface like any resource, each method is a route behind an Execute grant, and
the same method is a Go call the application makes in a transaction of its own, so a
person is given a role beside the application's own rows and committed with them.
The option requires `GenerateRoutes` and `GenerateHandlers`, and a program that declares
at least one `Auth`. It is the user management of the whole application: accounts, roles,
password reset links, the second factor's reset, lockouts and invitations. In an
application with a tenant record it is also one tenant's management of the people who
hold a role in it: the tenant's lists (its people, the roles held there, its invitations)
and the methods the tenant changes them through, both below; and, for the Auth a tenant
is made through, the invitation staff bring a tenant in by ("The invitation that makes a
tenant", below).

**The tree.** `treeDir` (Lodestar's is `pkg/identity`) is the generator's alone. Per Auth
it holds three packages, the `crew` Auth's being:

- `internal/crewresources`: the declarations, with the query builders and patch types the
  generator writes for any resource, and beside them the views that are a tenant's lists,
  where the application has a tenant record. The package is internal to the tree, so no
  application code reaches a patch constructor: the compiler refuses the import.
- `crewidentity`: the methods, each an `@rpc` struct whose `Execute` is one call into the
  framework's `identity` package, where the rules are, and the `Client` they run over.
- `crewhandlers`: the generated frames of those resources and methods, over a `Deps`
  interface the application's handler type satisfies, constructed with the application's
  live derivations (`crewhandlers.New(app, derivations)`), which every frame publishes
  its commit with. It also holds the record of what changes the tenant's lists and the
  lists of how a person signs in (`crewhandlers.LiveDerivations`), which the
  application's assembly merges into those derivations (section 14).

The generator writes the declarations and the methods before it loads the packages, so
the same run extracts them as it extracts the application's own. From there they are
resources and methods like any other: the route tables, the collection, the TypeScript
client, the generated authorization matrix and the role-file validation carry them with
no further declaration. A Go file in the tree that the generator did not write refuses
the run. The packages an earlier run wrote for an Auth the program no longer declares
are removed, so an Auth taken out of the program, or renamed, leaves nothing to clear by
hand. A tree belongs to one program, since a run removes what it does not write:
`impulse check` refuses two programs naming one tree, and holds every file under a tree
to the regeneration.

**The routes**, for an Auth in the package `crew`, under the prefix of the Auth's own
outlet and of every outlet that declares `Manages` on it. A resource is declared where
the schema holds its table, so an Auth that issues no tokens has no tokens list:

```
GET  <prefix>/crew-session-users, …/{id}                   List, Read on CrewSessionUsers (sort=username)
GET  <prefix>/crew-sessions, …/{id}                        List, Read on CrewSessions (newest first)
GET  <prefix>/crew-session-identities, …/{id}              List, Read on CrewSessionIdentities
GET  <prefix>/crew-sign-in-ways?filter=userId:eq:{id}      List on CrewSignInWays (how one person signs in: the password, then the identities that count)
GET  <prefix>/crew-session-tokens, …/{id}                  List, Read on CrewSessionTokens
GET  <prefix>/crew-session-mail-links, …/{id}              List, Read on CrewSessionMailLinks
GET  <prefix>/crew-session-impersonations, …/{sessionId}   List, Read on CrewSessionImpersonations (newest first; `mask` is each session's mask in words)
GET  <prefix>/crew-session-trusted-devices, …/{id}         List, Read on CrewSessionTrustedDevices (the remembered browsers)
GET  <prefix>/crew-session-sign-in-attempts                List on CrewSessionSignInAttempts
GET  <prefix>/crew-session-auth-events                     List on CrewSessionAuthEvents
GET  <prefix>/crew-user-roles                              List on CrewUserRoles (the role memberships)
GET  <prefix>/crew-held-roles                              List on CrewHeldRoles (the roles people hold, their own and through a group, sort=username,role)
GET  <prefix>/crew-taken-roles                             List on CrewTakenRoles (the roles held for a set time with a reason, the last to end first)
GET  <prefix>/crew-takeable-roles                          List on CrewTakeableRoles (the roles the caller may take for a set time)
GET  <prefix>/crew-take-lengths                            List on CrewTakeLengths (the lengths a role is taken for)
GET  <prefix>/crew-roles                                   List on CrewRoles (the custom roles)
GET  <prefix>/crew-listed-role-grants                      List on CrewListedRoleGrants (the custom roles' grants, each with why the permission engine leaves it out, where it does)
GET  <prefix>/crew-givable-roles                           List on CrewGivableRoles (the roles one of the whole application's methods gives, by where they are held)
GET  <prefix>/crew-groups, …/{id}                          List, Read on CrewGroups (the groups of people, sort=name)
GET  <prefix>/crew-group-members                           List on CrewGroupMembers
GET  <prefix>/crew-group-roles                             List on CrewGroupRoles (the roles the groups hold)
GET  <prefix>/crew-group-users, …/{id}                     List, Read on CrewGroupUsers (the people of the groups, with their names)
GET  <prefix>/crew-user-group-roles, …/{id}                List, Read on CrewUserGroupRoles (the roles people hold through a group)
GET  <prefix>/crew-listed-groups, …/{id}                   List, Read on CrewListedGroups (the groups with what each holds, sort=name)
GET  <prefix>/crew-listed-group-roles                      List on CrewListedGroupRoles (the roles the groups hold, each with how many people lose it)
GET  <prefix>/crew-sign-in-methods                         List on CrewSignInMethods (the methods the Auth registers, with what was chosen for each)
GET  <prefix>/crew-invited-sectors                         List on CrewInvitedSectors (the invitations that make a sector and still stand)
GET  <prefix>/crew-invited-sector-roles                    List on CrewInvitedSectorRoles (the roles a new sector's directory people may start with)
GET  <prefix>/sectors/{sectorID}/crew-sector-users, …/{id}         List, Read on CrewSectorUsers (sort=username)
GET  <prefix>/sectors/{sectorID}/crew-sector-user-roles, …/{id}    List, Read on CrewSectorUserRoles (sort=username,role)
GET  <prefix>/sectors/{sectorID}/crew-sector-held-roles            List on CrewSectorHeldRoles (the roles held in the sector, their own and through a group)
GET  <prefix>/sectors/{sectorID}/crew-sector-invitations, …/{id}   List, Read on CrewSectorInvitations (newest first)
GET  <prefix>/sectors/{sectorID}/crew-sector-identities, …/{id}    List, Read on CrewSectorIdentities (newest first)
GET  <prefix>/sectors/{sectorID}/crew-sector-sign-in-ways?filter=userId:eq:{id}   List on CrewSectorSignInWays (how one of the sector's people signs in)
GET  <prefix>/sectors/{sectorID}/crew-sector-groups, …/{id}             List, Read on CrewSectorGroups (the sector's own groups)
GET  <prefix>/sectors/{sectorID}/crew-sector-group-users, …/{id}        List, Read on CrewSectorGroupUsers
GET  <prefix>/sectors/{sectorID}/crew-sector-group-roles, …/{id}        List, Read on CrewSectorGroupRoles
GET  <prefix>/sectors/{sectorID}/crew-sector-user-group-roles, …/{id}   List, Read on CrewSectorUserGroupRoles
POST <prefix>/rename-crew-user                {"userId","username"}            Execute on RenameCrewUser, targeting CrewSessionUsers
POST <prefix>/delete-crew-user                {"userId"}                       Execute on DeleteCrewUser, targeting CrewSessionUsers
POST <prefix>/set-crew-user-name              {"userId","name"}                Execute on SetCrewUserName, targeting CrewSessionUsers
POST <prefix>/set-crew-user-email             {"userId","email"} → {"sent","reason"}   Execute on SetCrewUserEmail, targeting CrewSessionUsers
POST <prefix>/send-crew-user-password-reset-link {"userId"} → {"sent","reason"}  Execute on SendCrewUserPasswordResetLink, targeting CrewSessionUsers
POST <prefix>/revoke-crew-user-trusted-devices {"userId"}                      Execute on RevokeCrewUserTrustedDevices, targeting CrewSessionUsers
POST <prefix>/unlock-crew-sign-in             {"name"}                         Execute on UnlockCrewSignIn, targeting CrewSessionSignInAttempts
POST <prefix>/invite-crew-user                {"email","name","domain","everyDomain","roles"} → {"id","sent"}   Execute on InviteCrewUser
POST <prefix>/withdraw-crew-invitation        {"invitationId"}                 Execute on WithdrawCrewInvitation, targeting CrewSessionMailLinks
POST <prefix>/invite-crew-sector              {"email","name","sectorName",…,"sectorDirectoryOnly","defaultRole"} → {"id","sent"}   Execute on InviteCrewSector, and Create on Sectors
POST <prefix>/withdraw-crew-invited-sector    {"invitationId"}                 Execute on WithdrawCrewInvitedSector
POST <prefix>/disable-crew-user               {"userId"}                       Execute on DisableCrewUser, targeting CrewSessionUsers
POST <prefix>/enable-crew-user                {"userId"}                       Execute on EnableCrewUser, targeting CrewSessionUsers
POST <prefix>/end-crew-user-sessions          {"userId"}                       Execute on EndCrewUserSessions, targeting CrewSessionUsers
POST <prefix>/require-crew-password-change    {"userId"}                       Execute on RequireCrewPasswordChange, targeting CrewSessionUsers
POST <prefix>/end-crew-session                {"sessionId"}                    Execute on EndCrewSession, targeting CrewSessions
POST <prefix>/end-crew-session-impersonation  {"sessionId"}                    Execute on EndCrewSessionImpersonation, targeting CrewSessionImpersonations
POST <prefix>/unlink-crew-session-identity    {"identityId"}                   Execute on UnlinkCrewSessionIdentity, targeting CrewSessionIdentities
POST <prefix>/remove-crew-password            {"userId"}                       Execute on RemoveCrewPassword, targeting CrewSessionUsers
POST <prefix>/revoke-crew-session-token       {"tokenId"}                      Execute on RevokeCrewSessionToken, targeting CrewSessionTokens
POST <prefix>/assign-crew-role                {"userId","role","domain","everyDomain","startsAt","endsAt","reason"}   Execute on AssignCrewRole
POST <prefix>/take-crew-role                  {"domain","role","length","reason"}        Execute on TakeCrewRole (the caller takes the role themselves)
POST <prefix>/remove-crew-role                {"userId","role","domain","everyDomain"}   Execute on RemoveCrewRole
POST <prefix>/create-crew-group               {"name","description","domain"} → {"id"}   Execute on CreateCrewGroup
POST <prefix>/describe-crew-group             {"groupId","description"}        Execute on DescribeCrewGroup, targeting CrewListedGroups
POST <prefix>/delete-crew-group               {"groupId"}                      Execute on DeleteCrewGroup, targeting CrewListedGroups
POST <prefix>/add-crew-group-member           {"groupId","userId"}             Execute on AddCrewGroupMember, targeting CrewListedGroups
POST <prefix>/remove-crew-group-member        {"groupId","userId"}             Execute on RemoveCrewGroupMember, targeting CrewGroupUsers
POST <prefix>/give-crew-group-role            {"groupId","role","domain","everyDomain"}   Execute on GiveCrewGroupRole, targeting CrewListedGroups
POST <prefix>/take-crew-group-role            {"groupId","role","domain","everyDomain"}   Execute on TakeCrewGroupRole
POST <prefix>/open-crew-sign-up               {"name"}                         Execute on OpenCrewSignUp (declares a warning; {"name","defaultRole"} without tenants)
POST <prefix>/make-crew-invite-only           {"name"}                         Execute on MakeCrewInviteOnly
POST <prefix>/set-crew-self-service-reset     {"on"}                           Execute on SetCrewSelfServiceReset
POST <prefix>/sectors/{sectorID}/invite-crew-sector-user   {"email","name","roles"} → {"id","sent"}                  Execute on InviteCrewSectorUser
POST <prefix>/sectors/{sectorID}/withdraw-crew-sector-invitation   {"invitationId"}     Execute on WithdrawCrewSectorInvitation, targeting CrewSectorInvitations
POST <prefix>/sectors/{sectorID}/assign-crew-sector-role   {"userId","role","startsAt","endsAt","reason"}   Execute on AssignCrewSectorRole, targeting CrewSectorUsers
POST <prefix>/sectors/{sectorID}/remove-crew-sector-role   {"userId","role"}            Execute on RemoveCrewSectorRole, targeting CrewSectorHeldRoles
POST <prefix>/sectors/{sectorID}/unlink-crew-sector-session-identity   {"identityId"}   Execute on UnlinkCrewSectorSessionIdentity, targeting CrewSectorIdentities
POST <prefix>/sectors/{sectorID}/remove-crew-sector-password           {"userId"}       Execute on RemoveCrewSectorPassword, targeting CrewSectorUsers
POST <prefix>/sectors/{sectorID}/create-crew-sector-group          {"name","description"} → {"id"}   Execute on CreateCrewSectorGroup
POST <prefix>/sectors/{sectorID}/describe-crew-sector-group        {"groupId","description"}   Execute on DescribeCrewSectorGroup, targeting CrewSectorGroups
POST <prefix>/sectors/{sectorID}/delete-crew-sector-group          {"groupId"}          Execute on DeleteCrewSectorGroup, targeting CrewSectorGroups
POST <prefix>/sectors/{sectorID}/add-crew-sector-group-member      {"groupId","userId"} Execute on AddCrewSectorGroupMember, targeting CrewSectorGroups
POST <prefix>/sectors/{sectorID}/remove-crew-sector-group-member   {"groupId","userId"} Execute on RemoveCrewSectorGroupMember, targeting CrewSectorGroupUsers
POST <prefix>/sectors/{sectorID}/give-crew-sector-group-role       {"groupId","role"}   Execute on GiveCrewSectorGroupRole, targeting CrewSectorGroups
POST <prefix>/sectors/{sectorID}/take-crew-sector-group-role       {"groupId","role"}   Execute on TakeCrewSectorGroupRole, targeting CrewSectorGroupRoles
POST <prefix>/sectors/{sectorID}/disable-crew-sector-user, enable-…, delete-…, rename-…, set-crew-sector-user-name, set-crew-sector-user-email,
     send-crew-sector-user-password-reset-link, require-crew-sector-password-change,
     revoke-crew-sector-user-trusted-devices, end-crew-sector-user-sessions
                                              the whole application's bodies and answers    Execute on each, targeting CrewSectorUsers
```

The names are table-derived: the Auth's package name in PascalCase and the library's own
name for the table, with the tenant record's name between them for a tenant's list
(`Sector` in the ones under the domain pair, which exist only where the application has
a tenant record). In an application without tenants the six methods of the whole
application that say where (`invite-crew-user`, `assign-crew-role`, `remove-crew-role`,
`create-crew-group`, `give-crew-group-role`, `take-crew-group-role`) have neither `domain`
nor `everyDomain`: every role is held and every group kept for the whole application, and
a request that names either field is refused as any field the method does not have (400).
Every resource over a table is global, none has a write route, and none
carries a password hash or a token hash: no declaration has such a field. A method that
targets a resource is listed in that resource's rows' Execute answer
(`capabilities=Execute`), so a page draws its buttons from the row. While a tenant is
directory only, the methods that change how one of its administrators signs in or
whether their account stands (rename, change the email address, require a new password,
remove the password, unlink a sign-in identity, disable, enable, delete) are in that
person's row only for a caller whose session enters the tenant, and for a super
administrator. Nobody else is shown them, and the server refuses them as before. The
Auth's shape decides which methods exist: the two role methods, the two invitation methods, and the
list and the two methods of the sign-in methods ("Who may sign up", below) are left out
for an Auth whose roles a directory writes (`SyncsRoles`), where the directory admits its
own members and the first sign-in creates the row, the method that requires a new
password, the method that mails a reset link and the one that says whether a person may
ask for one themselves for an Auth with no password method, as are the
method that removes a person's password and the list of how a person signs in, which are
also written only where the schema holds the identities table (the rule they keep reads a
person's identity links), the rename and
the address set by hand for an Auth whose usernames are the addresses (`UsernameIsEmail`:
the name is the address, and it changes when the person verifies a new one), and the
token, impersonation, remembered-browser and unlock methods where the schema holds no
such table. The list of invited tenants and its two methods are written for one Auth,
the one a tenant is made through ("The invitation that makes a tenant", below). A
tenant's method is written where the whole application's method of the same
act is, and the list whose rows it names is declared. The roles table is declared too
(`CrewRole`): the custom roles, which the role rule below reads, served as a list. An
application's own resource or method under one of these names is refused at generation,
as is a resource field named for a method of the generated set (`Resource`,
`DefaultConfig`, `FileKeys`, `SearchBranches`).

**The credential methods carry a class.** The methods that change how a person signs in
are written with `@class(credential)` (`accesstypes.ClassCredential`), in the whole
application's form and in a tenant's alike: the temporary password
(`set-crew-temporary-password`), the required new password
(`require-crew-password-change`), the removal of a password (`remove-crew-password`), the
reset link an administrator sends (`send-crew-user-password-reset-link`), the unlinking
of a sign-in identity (`unlink-crew-session-identity`), the rename (`rename-crew-user`)
and the address set by hand (`set-crew-user-email`). The generated collection carries
the class on each, and a session whose mask refuses the class is refused every one of
them before any policy is read, and its permission digest leaves them out (section 6;
the session package's "Impersonation is granted with a mask"). No method refuses an
impersonated session by a rule of its own for being a credential action: the mask is the
control.

**The methods that change who may do what carry a class too.** The methods that give or
take away what someone may do are written with `@class(access)`
(`accesstypes.ClassAccess`), in both forms: a role given or removed (`assign-crew-role`,
`remove-crew-role`), a role a person takes themselves for a set time (`take-crew-role`,
which refuses every session that acts as someone else by itself as well), a group made or deleted and a person put in one or taken out
(`create-crew-group`, `delete-crew-group`, `add-crew-group-member`,
`remove-crew-group-member`), a role given to a group or taken from it
(`give-crew-group-role`, `take-crew-group-role`), an invitation sent or withdrawn, the
one that makes a tenant among them (`invite-crew-user`, `withdraw-crew-invitation`,
`invite-crew-sector`, `withdraw-crew-invited-sector`), a sign-in method opened to sign-up
or made invite only
(`open-crew-sign-up`, `make-crew-invite-only`), and the switch that lets people ask at
the sign-in page for a link that resets their own password
(`set-crew-self-service-reset`), which says who may get back into an account without an
administrator. What the generator writes for a tenant record, which has no declaration
to annotate, carries the class by its registration: the two methods of the tenant's
directory, `open-sector-to-directory`, which gives a role to everyone who arrives
through the directory, and `make-sector-invite-only`; the two that turn the switch
that makes a tenant sign in with its directory alone, `make-sector-directory-only` and
`end-sector-directory-only`, which say how a tenant's people must sign in to enter it;
and the action of the create-your-tenant route, `CreateSector`, which makes its caller
a tenant's administrator (section 16, "The first administrator"). A session that acts
as a person under a grant declared "everything except the credential actions and the
actions that change who may do what" is refused every one of both classes.

What carries neither class: an account disabled, enabled or deleted
(`disable-crew-user`, `enable-crew-user`, `delete-crew-user`), which is the work a
person who stands in for an administrator is there to do, and which the rule on who is
asking still refuses on the real person's own account ("The rules on who is asking");
the name people read and a group's description
(`set-crew-user-name`, `describe-crew-group`), which are words; the end of a session, of
a person's sessions or of an impersonation, the revocation of a token or of the
remembered browsers, and the unlock, each of which ends a sign-in already made or lets
one be tried again and leaves what the person may do as it was; and the switch of a
feature flag (`SetFeature`). An application puts no class on a record: the generator
gives the class `access` to the writes of every record that carries a subject value or
a subject set, to the change of a record such a value is read through, and to the
delete of the tenant record, which takes every role held in the tenant with it, so a mask
that refuses the class refuses them, and a mask names any other record it is to refuse
(`accesstypes.RefuseWrites`). A value read through a table that no `@resource` struct
declares has no record to carry the class. Generation warns of it at every run, naming
the table (`SubjectPathWarning`): a route written by hand that changes a row of that
table moves the standing of everyone the set reaches, and a mask that refuses the class
does not refuse it until the table is declared or the route is registered as an action
of the class.

The generator's own tests list the methods and fail when one is written without the
class its act is listed with, or with a class it is not listed with: `identityMethodActs`
in `generation/identity_classes_test.go` for the generated identity methods, and
`generatorActions` in `generation/classes_test.go` for the actions the generator
declares with no struct to annotate, the tenant record's among them. They are the two
tables to keep in step.

**A tenant's lists.** An account is one row for the whole application, and a role held
is a row that names where it is held, so neither table can be served under a tenant's
address. In an application with a tenant record (`@tenant`) the generated management
declares four views per Auth, each a tenant record like any other (`@virtual`,
`@permissionScope(domain)`, the tenant in a `domain` column), listed and read under the
domain pair on the outlets that serve the Auth's management. The whole application's
lists carry the names of their tables; a tenant's list is named for what it holds, with
the Auth's name and the application's tenant record's before it. A tenant record whose
name would give a list the name of one of the tables' declarations (a record named
`Session` or `User`) is refused at generation:

- `CrewSectorUsers`, the tenant's people: one row per person who holds a role in
  the tenant or in every tenant, keyed by the account's id, with the columns of
  `CrewSessionUsers`. A person who holds a role in two tenants is a row in each. Two
  facts: `heldElsewhere` (the person also holds a role outside this tenant's own: in
  another tenant, or in every tenant) and `heldGlobally` (the person holds a role for
  the whole application).
- `CrewSectorUserRoles`, the roles held there: one row per person and role, keyed by the
  account's id and the role (`userId`, `role`), with `username` and `createdAt`. Two facts: `role`, the role itself, and `everyDomain`, true
  where the role is held there through a membership in every tenant alone, so it is not
  the tenant's to take away. A role held both ways is the tenant's own row.
- `CrewSectorInvitations`, the tenant's invitations: the mail links of the
  invitation kind that name the tenant, with `email`, `roleNames`, `invitedByUserId`,
  `createdAt`, `expiresAt` and `consumedAt`. Two facts: `pending`, true while the
  invitation stands (not accepted, not withdrawn, not expired), and `role`, the roles
  it gives: a list, so a condition that reads it is judged once for each role and holds
  only when it holds for every one (section 2, `@attribute` on a list column).
- `CrewSectorIdentities`, the identities of the tenant's people: one row per
  identity link of a person of the first list, keyed by the link's id, with the columns
  of `CrewSessionIdentities` (`tenant` is the link's own, the organization its directory
  vouched for), the person's `userId` and `username`, and the person's two facts,
  `heldElsewhere` and `heldGlobally`. The identity of a person who holds a role in two
  tenants is a row in each. A person's page lists their identities by `userId`.

Each fact is a column of the view under a bare `@attribute`, so a grant's condition reads
it by name, and the role file says what a tenant's administrator sees of a person who is
not the tenant's alone. Lodestar's Sector Marshal sees every person of the sector and
their address only where the sector holds them alone, and its Dispatcher the roles the
sector itself gave:

```json
"List": [
  { "resource": "CrewSectorUsers", "fields": ["username", "disabled", "createdAt", "lastSignInAt", "heldElsewhere", "heldGlobally"] },
  { "resource": "CrewSectorUsers", "fields": ["email", "emailVerifiedAt", "passwordChangeRequired"], "condition": "heldElsewhere = false" }
]
"List": [{ "resource": "CrewSectorUserRoles", "fields": ["username", "role"], "condition": "everyDomain = false" }]
```

So an application where a tenant owns its people and one where the people are shared
between tenants are two role files over the same lists. A grant names its fields, as on
any view: a grant with none answers the key alone. The people's list filters and sorts by
`username` and by the two facts; the account's other columns are no filter keys, so a
grant may carry any of them under a condition at no cost to a page.

A view is a query the framework selects from (`FROM (<query>) AS <Resource>`), not a
database object: nothing is created in the schema and nothing is migrated. The generator
writes each view beside the tables it reads, with its query as a constant of the file,
which reads the Auth's memberships, its users, identities or mail links, and the tenant
record's table (a membership held in every tenant is a row in each). A membership names its
person by the id of their account, and the join finds the account by the users table's
key. The generator reads the tenant record before it loads the packages,
off the syntax of the resources package and the schema, since the views are written
before the load: a tenant record whose table the schema does not hold is refused there.
A list is written only where the schema holds the tables it reads, so an Auth with no
mail links table has no invitations list, and one that links no identities has no
identities list. The lists are published like any view
(section 14): a role given, a person created or disabled, or an invitation written
through the generated methods reaches the pages that have a tenant's list open.

What a request of these lists reads. The people's list, the roles held and the identities
read the tenant's memberships and then the account of each person: one row of the users
table, found by its key, since a membership names its person by the id of their account.
The identities and the invitations are read from two indexes of the Auth's schema that
carry the columns the lists show (the identities by user, the mail links by tenant, newest
first). The database joins before it cuts the page, so the cost follows the size of the
tenant and not of the page or of the application.

The lists were measured on Cloud Spanner over 1.07 million users in 2,200 tenants on
2026-11-15, on the smallest instance (100 processing units). A first page of people took
12 ms for a tenant of 100, 18 ms for 500, 89 ms for 5,000 and 0.75 s for 50,000, and a
count about the same; the roles held 10 ms, 34 ms, 0.1 s and 0.8 s; the identities 0.17 s
for a tenant of 100 and 2 s for 50,000. Each reads the tenant's memberships and one
account row per person: no request reads the users table whole, and the rows read are
those of the tenant, at any size of the application. (Read through an index by username,
when a membership named its person by username, the people took 0.45 s at 50,000 and the
roles and identities longer.) The invitations read their own page from the index (26 rows
at any size).
Three costs are worth knowing. A filter on `heldElsewhere` or `heldGlobally`,
or a grant on the list whose condition reads one and so holds every row to it, costs two
index reads for each person of the tenant (seconds at 50,000 people; a condition on a
field costs them for the page's rows alone). A row of the roles held is keyed by the
user's id and the role, two columns the database looks up, so a read of one row, and the
check a method makes of the role it names, reads the one person's memberships (2 to 4 ms
and three rows at 50,000 people; a key that joined the two in one value could not be
looked up, and the same read joined the tenant's memberships, about a second there). And
a schema applied by hand without those two indexes serves the identities and the
invitations by reading their tables whole.

**The roles a tenant picks from.** Where a tenant gives roles by hand, the generated
management declares one more list per Auth, `CrewSectorRoles`: the roles that
exist in the tenant, each with its `name` and `description`. They are the tenant roles of
the role file, the custom roles held in the tenant, and the custom roles held in every
tenant; a role of the whole application and another tenant's custom role are not among
them. It is a computed resource (`@computed`, section 2), declared beside the methods:
the role file's roles are held by the permission engine and only a custom role is a row
of a table, so its list function asks the engine (`identity.Manager.TenantRoles`) and no
query selects it. It serves a list and no read, under the domain pair, and the role file
grants it like any resource.

The role field of each method a person gives a role with in a tenant names the list
(`@enumerate`): `AssignCrewSectorRole`, the invitation that starts a person with a role
(`InviteCrewSectorUser`), and `GiveCrewSectorGroupRole`. A page draws the field as a picker, and a dialog offers what
its own method takes from the caller, so the list is asked for one method at a time and
holds the roles the caller could give with it and no other. A role file says which
roles a person hands out once, on each method's Execute grant, where a condition reads
the role:

```json
"Execute": [{ "resource": "AssignCrewSectorRole", "condition": "new.role != 'SectorMarshal'" }]
```

A row carries a third field for this, `method`: a filter key, and nothing a page shows.
A request names the method whose dialog is picking
(`filter=method:eq:AssignCrewSectorRole`), and for each role of the tenant the list asks
that method's grant, with the role and nobody named (`identity.Manager.RolesToGive`,
which asks the generated `CouldGive` of `identity.RoleGrants`), and leaves out a role it
refuses the caller: Lodestar's Sector Marshal is offered every role of the sector but
the Marshal's own. A grant whose condition also reads the row the method names (the
person, the group) could admit the role for some of them, so the role is offered and
the method decides. A role one method takes and another refuses is in the first one's
list and not in the second's, so each of the four pickers offers exactly what its own
method takes. A request that names no method is answered no role: a picker that does
not say which method it serves is empty, never wider than the method. One that names a
method whose role field does not pick from the list, or names methods any other way
(`in`, `ne`, two conditions), is refused (400) with the names it may send. A request
that names a role the list left out is refused by the method's own grant, as before.

The list's own List grant may carry a condition too, which narrows it further. The
role's name is declared for conditions (`@attribute(name)` on the field), and the library
judges the condition over each role the list answers (a computed resource's rows,
section 2); a request for the names alone and the list's count are trimmed the same way.
Lodestar keeps one, which says nothing its methods' grants do not, to show a condition
judged over a computed resource's rows. A caller filters by the fields they may read,
and a computed list filters only by a field granted with no condition left once the
request is checked (section 2), so a role is granted `method` with the list: among the
fields of a grant that carries no condition, or, beside a grant that carries one, as a
grant of the field alone:

```json
"List": [
  { "resource": "CrewSectorRoles", "fields": ["description"], "condition": "name != 'SectorMarshal'" },
  { "resource": "CrewSectorRoles.method" }
]
```

**The roles the whole application gives.** The whole application's methods pick their
role the same way, from one more list per Auth, `CrewGivableRoles`, declared beside the
methods and served on their outlets (a list and no read). Its rows are the roles that
exist where the role is to be held, each with its `name` and `description`, the
`method` it is listed for, and, in an application with tenants, where it is held:
`domain`, the one tenant ("" for the whole application or every tenant), and
`everyDomain`. The three are filter keys. Filtered by
one tenant (`domain:eq:<id>`) the list holds that tenant's roles; otherwise it holds the
roles held for the whole application (`everyDomain` false) and those held in every
tenant (`everyDomain` true), which a filter on `everyDomain` narrows. A filter that
names tenants any other way beside its other conditions (`in`, `ne`, a range, two
conditions on `domain`) is refused (400), in the application's word for a tenant; one
that joins its conditions with `or` names no method the list can read apart from the
rest, and is answered no role. A role is listed once for each place, so a custom role held both for the whole
application and in every tenant is two rows under one name until the request names a
place, as the dialogs always do. In an application without tenants the list holds the
whole application's roles and has neither field of a place.

The role fields that name it (`@enumerate`) are those of `AssignCrewRole`,
`InviteCrewUser` and `GiveCrewGroupRole`, each written where the Auth has the method. Each request asks where the role is held first (`domain`, then
`everyDomain`, before the role), so a dialog drawn from the metadata asks where, then
offers the roles that exist there. As for a tenant, the list is asked for one method at
a time, named beside the place (`filter=method:eq:InviteCrewUser,domain:eq:<id>`): a
role is listed where that method's grant could let the caller give it there, with the
role, the place and nobody named (`identity.Manager.GivableRoles`, over the generated
`CouldGive`), and left out where it refuses. A role only the invitation's grant admits
is in the invitation's picker and in no other, and a request that names no method is
answered no role. The list's own List grant may carry a
condition on `name` that narrows it further. Two rules hold whatever the grants say: the
Super Administrator role is listed for the whole application alone, and only to a
caller who holds it, since only a super administrator gives it; and a tenant's
administrator's role of a tenant that is directory only is listed only to a caller the
tenant admits (section "Directory only", the rule the methods keep).

Where the Auth keeps groups, the list has one more filter key, `group`. The method
that gives a group a role is asked with the group beside it
(`filter=method:eq:GiveCrewGroupRole,group:eq:<id>,domain:eq:<id>`), and the list then
holds the roles that group may hold there (`identity.Manager.GivableGroupRoles`), by
the rule the method holds the group to (`access.GroupMayHold`): never the Super
Administrator role, and for a group kept in one tenant that tenant's roles alone, so
no role for the whole application, in every tenant or in another tenant. Asked with no
group, `GiveCrewGroupRole` is answered no role, and so is an id that is no group's,
as a group that may hold no role there is: the list never says whether an id is a
group's. The rule for a tenant that is directory only is the method's here too: its
administrator's role is listed for a group kept in that tenant only to a caller the
tenant admits, and for a group kept for the whole application to any caller the grant
lets give it, since such a group makes nobody the tenant's administrator. A group named
beside another method, or any other way than by one id, is refused (400). The role file grants the list to the roles that give one, `group`
among its fields where the role gives a group a role:

```json
"List": [{ "resource": "CrewGivableRoles", "fields": ["description", "method", "group", "domain", "everyDomain"] }]
```

An Auth whose directory writes the roles gives none by hand and has neither list. The
two other lists a role is picked from are limited the same way: the default role of a
method opened to sign-up (`CrewSignUpRoles`) and the role a tenant is opened to its
directory's people with (`CrewSectorDirectoryRoles`) each hold the roles the caller may
give by the method that gives one (`MayGive`).

**A tenant's methods.** Beside the lists the generated management writes the methods a
tenant changes its people through. Each is a method of the tenant's scope
(`@permissionScope(domain)`), served under the domain pair behind an Execute grant of the
tenant's roles, and is named as the whole application's method with the tenant record's
name after the Auth's: `DisableCrewSectorUser` beside `DisableCrewUser`.

- The tenant is the one the request was made under. No body carries a tenant, so no
  request names another: the invitation has no `domain`, and a role is given and taken
  in that tenant alone. A tenant's method acts for a request, so one run
  in a transaction of the application's own, outside any request, is an error: there the
  application runs the whole application's method and names the tenant.
- A method that names something names a row of one of the tenant's lists (`@target` on
  the view): a person by their row of `CrewSectorUsers`, a role held by its row of
  `CrewSectorHeldRoles` (`userId`, `role` and `groupId`, the three columns of the row's
  key, each a `@target` field naming the column it carries; `groupId` is the empty text
  on a role held directly, which the method's metadata says it may be, so a page sends
  it as the row holds it), an invitation by its row of
  `CrewSectorInvitations`, an identity by its row of `CrewSectorIdentities`.
  The frame locates the row within the tenant, so a person who holds no role there, or
  an invitation into another tenant, is not found (404), and
  it judges the grant's condition against the row. The method joins the row's Execute
  answer, so a page draws a row's buttons from the row.
- A role is given to a person who holds one in the tenant already
  (`AssignCrewSectorRole`, its `role` picked from `CrewSectorRoles`); a person
  new to the tenant is created in it or invited to it, holding a role from the start. A role held in every tenant alone is a row of the
  tenant's list and is not the tenant's to take: `RemoveCrewSectorRole` refuses it (409)
  whatever the role file says.
- The rules are the whole application's, in the same code: nobody disables or deletes
  their own account, the last administrator of a tenant's own cannot leave it or give
  up the role (someone whose role lets them take the role away may), a rename moves the
  roles, the last role a person holds anywhere takes the account with it, an
  invitation gives at least one role. A tenant's administrators are counted as the
  permission engine counts them (`access.AdministersTenant`): the people who hold an
  administrator's role in the tenant itself, themselves or through a group kept in the
  tenant. A person who holds the role in every tenant, or through a group kept for the
  whole application, does not stand in for the tenant's last one, since a tenant that
  admits its people through its directory only keeps that person out with everyone
  else. A holding counts while it is in force: one whose period is over, or has not
  begun, makes nobody an administrator, and a person removes their own freely. A
  holder whose account is disabled is not counted either: the account keeps its roles
  and administers nothing until it is enabled again, so the person beside it is the
  tenant's last administrator and keeps the role. A
  refusal that speaks of a tenant says the application's word for one, the tenant
  record's label or its name in words ("You are the last administrator of this
  sector."): the generated client hands it to the rules (`identity.WithTenantWord`).

What a tenant may do to a person who also holds a role outside it, and which roles its
administrator hands out, the role file says, in the conditions of the Execute grants: over
the facts of the row, and over the request's own fields (`new.role`, section 2's
`@attribute` on a field of an `@rpc` struct). Lodestar's Sector Marshal acts on the
account of a person the sector holds alone, gives every role but the Marshal's own, takes
a role away where the sector itself gave it, and withdraws an invitation that stands and
gives no Marshal's role:

```json
"Execute": [
  { "resource": "DisableCrewSectorUser", "condition": "heldElsewhere = false AND heldGlobally = false" },
  { "resource": "AssignCrewSectorRole", "condition": "new.role != 'SectorMarshal'" },
  { "resource": "InviteCrewSectorUser", "condition": "new.role != 'SectorMarshal'" },
  { "resource": "RemoveCrewSectorRole", "condition": "role != 'SectorMarshal' AND everyDomain = false" },
  { "resource": "WithdrawCrewSectorInvitation", "condition": "pending = true AND role != 'SectorMarshal'" }
]
```

A grant with no condition on an account method lets a tenant's administrator rename,
disable or delete a person another tenant also holds: the account is one row for the
whole application. `roles` is a list, judged role by role, and an invitation
with no role is refused by a grant that reads `new.role`, since no value never permits.
The roles of an invitation's row are a list too, judged the same way: an invitation the
whole application's administrator sent in the Marshal's role, alone or among others, is
not the Marshal's to withdraw, and its row's Execute answer leaves the withdrawal out.

**The invitation that makes a tenant.** Nobody creates a tenant directly (section 16,
"How a tenant comes to exist"): staff bring an organization in by inviting one person,
and that person's first sign-in makes the tenant. The generator writes the three pieces
into the management of one Auth, the one a tenant is made through: its people are the
ones the tenant record's session outlets sign in, the application gives its roles (no
directory writes them), and the schema holds its mail links and its memberships. The
Auth's mail links table carries the column the invitation keeps the tenant in
(`NewTenant STRING(MAX)`, which the session migration `impulse` renders has), and a
schema without it is refused at generation, naming the column.

- `Invite<Auth><Record>` (`InviteCrewSector`) sends one. Its request is the person's
  address (`email`), the name the sender gives them (`name`), and the tenant record's own
  fields, each named for the record (`sectorName`, `sectorRegion`, `sectorEstablished`),
  required where the record's create requires them, so the form a page draws from the
  method is the record's own. Where the record declares `@directoryOnly` the request
  carries the switch (`sectorDirectoryOnly`), and where the directories record carries
  `@directoryDefaultSeat` it carries the role the people of the tenant's directory
  start with (`defaultRole`). It answers the invitation's id and whether its message
  went (`{"id","sent"}`); an invitation whose message did not go is ended, not
  mailed, so no link stands that nobody holds.
- `<Auth>Invited<Record>s` (`CrewInvitedSectors`) lists the invitations that make a
  tenant and still stand: not one that was accepted or withdrawn, not one that expired,
  and not an invitation into a tenant that exists. A row carries the address, the
  person's name, the details, the switch, the starting role, who sent it and when it
  was sent and expires. The list is its own grant, and a detail of the tenant is in a
  row only for a caller who may list that field on the tenant record: a caller who
  lists a sector's name alone reads the name the invitation carries and none of its
  other details.
- `<Auth>Invited<Record>Roles` (`CrewInvitedSectorRoles`) lists the roles the people of
  a new tenant's directory may start with, where the invitation carries a starting
  role: the roles held in every tenant that the sender could give a person in a tenant
  directly and that the invitation's own grant does not refuse, each with its
  description, and never the Super Administrator role. The invitation's `defaultRole`
  is picked from it, so its form offers the roles the invitation takes from its sender
  and no other; a sender who may give no role is listed none and sends the invitation
  without one. The list is its own grant: grant List on it to each role that is granted
  `Invite<Auth><Record>`.
- `Withdraw<Auth>Invited<Record>` (`WithdrawCrewInvitedSector`) withdraws one by its id
  (204), and answers 404 for anything but an invitation that makes a tenant and still
  stands. An invitation is not edited: staff who want other details withdraw it and
  send another.

Sending is held to what the sender could have done by creating the tenant. Beside the
method's own grant it takes the Create grant on the tenant record, on the record and on
each field the invitation carries, with every condition: the details are run through
the record's own create, armed with the sender, in a transaction that is rolled back,
so a rule the record's create applies is applied when the invitation is sent, and the
answer goes to the person who can correct it. The starting role is a role the sender
could give a person directly, picked from `<Auth>Invited<Record>Roles`. A value the
record keeps unique in a single column that takes no NULL (the name, under the
skeletons' index) is looked up when the invitation is sent and again when it is
accepted, and one that is taken is refused with a sentence
(409, "a sector with the name Anvil already exists: give the new one another"). The
lookup runs once the sender's grants have answered, so the refusal tells nobody who may
not create a tenant which values are taken. A rule
the database alone applies at a commit, a CHECK constraint or a unique index over
several columns, is not seen when the invitation is sent: it refuses the commit of the
first sign-in, nothing is made, and the invitation stands for staff to withdraw. Two
invitations may wait with the same new name: the first acceptance takes it, and the
second is refused at its first sign-in the same way.

What an invitation says beyond the record's fields, the switch and the starting role,
no Create grant covers. A role file withholds either by a condition on the method's own
grant, which reads each as the attribute its field declares (`@attribute(directoryOnly)`,
`@attribute(role)`): a role that may invite but not close a tenant to its directory is
granted `Invite<Auth><Record>` where `new.directoryOnly = false`.

No invitation carries a secret. A field the wire cannot write is not among the details,
and neither is a secret field (section 23) or a write-only one: a tenant made by
invitation is given such a value afterward, by its administrator. A tenant record whose
create requires one, a column that takes no NULL and has no default, is refused at
generation where an Auth invites, with what to do: let the column take NULL or give it
a default.

While the invitation waits no tenant exists and nothing of it is public. The mailed
message names the application and carries the link, and says nothing of the tenant. The
invitation page, which a person not signed in reads (`POST <prefix>/user/invitation`,
the session library's), is told that the invitation makes a tenant (`newTenant`), what
people will call it (`tenantName`), the invited address, whether it is accepted through
a directory only (`directoryOnly`) and whether an account with a password is offered
(`signUp`), and none of the other details.

The first sign-in of the person who follows the link makes the tenant, in the
transaction that consumes the invitation: the tenant's row through the generated maker
(`New<Record>Maker`, over the record's create patch, so its change event is written),
the person's membership in the roles that administer a tenant
(`identity.WithAdministratorRoles`), and, where they signed in through their
organization's directory, that directory as the tenant's own with the starting role the
invitation names. All of it commits or none does.

- Accepted with a password, the tenant has no directory.
- An invitation that says directory only is accepted through an organization's
  directory alone. Its page offers no account with a password, and a sign-up with one,
  or a sign-in with a personal account, is refused with what to sign in with
  (`organization_account_required`); nothing is made and the invitation stands.
  Accepted through a directory, the tenant is made directory only with that directory
  as its own: the one case in which a tenant is directory only from the start.
- A directory belongs to one tenant, so a first sign-in through a directory another
  tenant has is refused (`organization_has_tenant`), the person told their organization
  is already set up and whom to ask; nothing is made and the invitation stands.

The person enters nothing and changes nothing by accepting: the details are the
sender's. After the commit the instance that took the sign-in rereads its roster and
signals the tenants kind, so the person's next request stands in the new tenant and
every instance serves it.

**Groups of people.** Where the schema holds the permission store's three group tables
under the Auth's prefix (`CrewGroups`, `CrewGroupMembers`, `CrewGroupRoles`; the store's
`DDL()` has them), the generated management serves the Auth's groups. A group holds roles
the way a person does, and a person put in a group holds the group's roles wherever the
group holds them, at their next request, until they leave the group or the group loses
the role. The permission engine folds a group's roles into its members when it compiles
its snapshot, so a check, a sign-in, a tenant's list of its people and the picker of a
person's tenants all see through a group with nothing more said. An Auth whose roles a
directory writes (`SyncsRoles`) keeps no groups, whatever tables the schema holds: the
directory's own groups do that job. The three tables are migrated together, and a schema
holding some of them is refused at generation.

- A group is kept for the whole application or in one tenant. A tenant's group is made
  and managed under the tenant's address by the tenant's own roles, holds any account,
  and holds roles in that tenant alone. A group kept for the whole application is
  managed by the whole application's methods and holds a role in the global scope, in
  one tenant or in every tenant; no tenant's group is ever given a role in another
  tenant (400).
- A tenant puts its own people in its groups. The tenant's method
  (`AddCrewSectorGroupMember`) takes a person the tenant already has, a row of its list
  of people (`CrewSectorUsers`), and answers 404 for any other id, an account or not: a
  tenant's administrator pulls no account into the tenant through a group, and the
  answer says nothing of who belongs elsewhere. An account from outside the tenant is
  put in a tenant's group by whoever manages the whole application's people, with the
  whole application's method (`AddCrewGroupMember`), which takes any account and any
  group.
- A group gives nobody a power that giving the role directly would not. A change to a
  group is a change to what people hold, so it is judged as the role changes it makes,
  beside the grant of the group's own method, which says who manages groups at all.
  Putting a person in a group needs what giving that person each role the group holds,
  where it holds it, needs. Taking a person out needs what taking each of those roles
  from them needs. Giving a group a role needs what giving it to each person in the
  group needs, and taking a role from a group what taking it from each of them needs.
  What a role change needs of the caller is the role file's to say, in the grants of
  the methods that give a role and take one away (`AssignCrewRole`, `RemoveCrewRole`;
  for a request made under a tenant the tenant's own, `AssignCrewSectorRole` and
  `RemoveCrewSectorRole`). The generated package asks the permission engine for the
  caller's grant on that method with the role in question (`crewidentity`'s `grants.go`,
  its answer to `identity.RoleGrants`, over `resource.Caller.ExecuteGate`), so a
  condition on the grant is read as it is for the method itself: where the role file
  grants a sector's marshal `AssignCrewSectorRole` with `new.role != 'SectorMarshal'`,
  the marshal puts nobody in a group that holds SectorMarshal, whatever the grant on
  `AddCrewSectorGroupMember` says. A change the caller could not make directly is
  refused (403), naming the role as a role picker shows it, in words ("Putting a person
  in the group Night shift gives them the role Sector marshal, which you may not give
  them."). A place or a group's role whose period has ended
  gives nobody anything and is not judged; one that has not begun is. A method the
  application runs in a transaction of its own, outside any request, is the
  application's act and is not judged.
- Taking a role away from a person removes the membership of their own. What they hold
  through a group stays, and goes when they leave the group or the group loses the
  role. A tenant's removal (`RemoveCrewSectorRole`) names a row of the tenant's one list
  of roles (`CrewSectorHeldRoles`) by the person, the role and the group the role comes
  through, which is none for a role of the person's own, the row a request that names
  no group names. A row that comes through a group is refused (400), and the refusal
  says where the role is changed.
- No group holds the Super Administrator role (400): a super administrator gives it to
  one person at a time, so a group is no way around the rule that only a super
  administrator makes another. The methods a super administrator always executes stay
  the two that give and take a person's own role.
- A group that still has a member or holds a role is not deleted (409): empty it first.
  The lists of groups say what each holds (`people`, `roles`), counted over what the
  deletion refuses a group for, so a page offers Delete only on a group that holds
  nothing and says what is left on the others; the refusal stays for a request that
  never saw the page. Nobody loses a role because a group was deleted. Deleting a tenant removes the
  tenant's groups with their members and roles, and every role a group of the whole
  application holds in it (`ClearTenant`); deleting a person takes them out of their
  groups.
- A place in a group keeps an account as a role of the person's own does: a person whose
  last role is taken away stays while they are in a group, and a person taken out of
  their last group who holds no role is deleted with their identity links and sessions.
  Where a person belongs to one tenant (`identity.WithOneTenantPerPerson()`), a place in
  a tenant's group counts as a role in that tenant.
- The declaration over the members table declares a subject set, the ids of the groups
  the caller is in, named after the auth: `subject.crewGroups` for Lodestar's auth
  `crew`, `subject.staffGroups` for an auth `staff` (section 2, `@subjectSet`). Every
  list over groups carries the group's id under the attribute `group`, so a role file
  holds a grant to the caller's own groups (`group IN subject.crewGroups`: Lodestar's
  sector crew see the groups they are in and the people in them), and a table of the
  application's own whose rows name a group follows a person through it in a set of its
  own (`who: GroupID IN subject.crewGroups`: a hangar pass in Lodestar names a person, a
  squadron or a group). The members table is no tenant's, so the set names its own way to a
  group's tenant (`domain: GroupID.DomainID`): in a tenant's request it holds the groups
  kept in that tenant alone, so a tenant's row that named another tenant's group, or a
  group kept for the whole application, would give nobody anything; in a request with no
  tenant it holds every group the caller is in. The library writes no such row. Every
  create and every update of a tenant's row that names a group asks, in the write's own
  transaction, where the group is kept, and refuses the write unless that is the row's
  own tenant (400, `groupId names a group that is not one of this sector's own. Choose a
  group kept in this sector: a group kept in another sector, or for the whole application,
  cannot be named here.`, the column by its JSON name and the tenant by the application's
  word for one). An update that moves a row which names a group to another tenant is held
  the same way, and an id that is no group's is refused in the same words, so the answer
  says nothing of what another tenant keeps. The rule is the patch set's, so it holds for
  every writer that writes through a patch (a generated route, a method, a job, a
  bootstrap), armed or trusted, and no application states it: the generator declares the
  columns (`GroupColumns` on the generated resource), every column of a `@domain`
  resource that a `who:` tests against an Auth's groups set. A writer that goes past the
  patch with SQL of its own is outside it. An application with several Auths that keep
  groups has one set for each, named for its Auth (`subject.crewGroups`,
  `subject.staffGroups`), since `subject.<name>` is one namespace for the application.

The lists: `CrewGroups` (the groups, with where each is kept), `CrewGroupUsers` (the
people of the groups, with their names), `CrewGroupRoles` (the roles the groups hold)
and `CrewUserGroupRoles` (the roles people hold through a group, which stands beside
`CrewUserRoles`, the roles each holds themselves, so a page says which is which). A
tenant has its own four (`CrewSectorGroups`, `CrewSectorGroupUsers`,
`CrewSectorGroupRoles`, `CrewSectorUserGroupRoles`): the first three hold the tenant's
own groups, and the last every role a person holds in the tenant through a group, with
`globalGroup` where the group is the whole application's and so not the tenant's to
change. A group of the whole application that holds a role both in the tenant and in
every tenant is one row there, the one that counts now: the tenant's own row gives way
while it has not begun and the one held in every tenant counts. The lists of what people hold leave out a row the engine gives nobody (the Super
Administrator role in a group, a tenant's group with a role outside its tenant). A
tenant's list of its people (`CrewSectorUsers`) holds who a group puts there: the
members of its own groups, and the members of a group of the whole application that
holds a role there, the latter with `heldGlobally`.

**What a page reads of them.** Four lists say what is held in the words a page shows,
each a view like a tenant's lists: nothing is created in the database, and the role file
grants each like any resource.

- `CrewHeldRoles` is the one list of the roles people hold, each with where it comes
  from: a row for each role of a person's own, with its period and the reason it was
  given for, and a row for each role that comes to a person through a group, which
  `groupId` and `groupName` name (both empty for a role of the person's own). A person
  may hold a role both ways, so the group is part of what names a row. A tenant has its
  own (`CrewSectorHeldRoles`), with `globalGroup` where the group is the whole
  application's and `groupPage`, the group's id where the tenant keeps the group and so
  has a page for it. It stands beside `CrewSectorUserRoles`, which holds the roles
  people hold themselves and is read by a row's key; the one list is read as a list
  alone. An auth whose roles a directory writes has the list too, and every row of it
  is the person's own.
- `CrewListedGroups` is the groups with what each holds: `people`, how many people are
  in the group, and `roles`, the roles it holds, a line each: the role's name, and where
  it is held when that is not where the group is kept ("Engineer in sector Anvil",
  "Pilot in every sector"), the tenant by the name people call it by where the tenant
  record marks one (`@tenantName`). Both count what has not ended, a place or a role
  whose period has not begun among it. The whole application's group methods name their
  group by its row here. A tenant's list of its groups (`CrewSectorGroups`) carries the
  two columns itself.
- `CrewListedGroupRoles` is the roles the groups hold, each with `losing`: how many
  people lose the role when the group stops holding it, the people in the group now who
  hold the role there in no other way, neither by a membership of their own nor through
  another role of a group they are in. A person who holds the role in every tenant
  keeps it in each, so they do not lose a role held in one. Nobody loses a role whose
  period has not begun, or one the group gives its members nothing by. A tenant's list
  (`CrewSectorGroupRoles`) carries the count itself.

**The custom roles' grants.** `CrewListedRoleGrants` is the custom roles' grants as the
store holds them, each with `skipped`: empty for a grant its role holds, and for one the
permission engine leaves out when it loads the policy, why, in the words the deploy
check prints. It is a computed list: the engine is asked of every row, and no query
selects it. The grants table itself has no route. The role file grants the list like any
resource, and `skipped` is a field of that grant: a role whose grant does not name it
reads the rows without it, and a grant the engine leaves out then reads as any other.

**The period of an assignment.** A role given to a person, a person's place in a group
and a role given to a group may each carry a period: a start and an end, each optional,
kept in two nullable columns of the permission store's memberships, group members and
group roles tables (`StartsAt`, `EndsAt`; the store's `DDL()` has them, and every policy
store's schema holds them). No start is from now, and no end is until it is taken away.
An assignment counts inside its period alone, and nothing is written when a period
begins or ends: everything that reads an assignment leaves out one outside its period.
An Auth whose roles a directory writes gives none by hand and so gives no period.

- The methods that give a role (`AssignCrewRole`, `AssignCrewSectorRole`), a place in a
  group (`AddCrewGroupMember`, `AddCrewSectorGroupMember`) and a group's role
  (`GiveCrewGroupRole`, `GiveCrewSectorGroupRole`) take the two times, `startsAt` and
  `endsAt`. Whoever may give the role there gives it its period: the method's Execute
  grant, with its condition on `new.role`, is the whole of it.
- Changing a period is giving again: a role the person holds there already is given the
  period of the new request in place of the one it had, so a request that names neither
  time makes the role stand until it is taken away. Ending a role now is taking it away,
  and a period that is over already is refused (400), as one that ends at or before its
  start is.
- Nobody lengthens their own holding. A request that would make something its caller
  holds last longer than it was given for is refused (403): a period for the caller's
  own role or the caller's own place in a group that ends later, or never, or begins
  sooner while it has not begun, and the same for a role of a group the caller is in.
  It holds for a period that has ended too, while its row stands. Someone else who may
  give the role gives it its new period. The caller shortens their own, with one
  exception: the last administrator of a tenant's own does not cut the role short
  there. A period that takes it away from now on, or from a sooner end, is refused
  (403) in the words that refuse them the role's removal, on their own role, on their
  place in the group that gives it and on that group's role, until another person
  administers the tenant.
- The Super Administrator role takes no period (400): there is always a super
  administrator, and a period could leave none.
- The permission engine applies the period at the instant a check is made with, the
  clock a grant's `now` is folded with, so an open session loses a role at its next
  request after the end. The sign-in rule and the session's list of tenants read what a
  person holds at the moment, and so do a tenant's list of its people
  (`CrewSectorUsers`) and the list of the identities of those people: a person whose
  only role in a tenant has ended, or has not begun, is not one of its people. The two
  facts about a person (`heldElsewhere`, `heldGlobally`) read what has not ended.
- The lists of roles and of places carry the period (`startsAt`, `endsAt`) under a
  field grant of their own, leave out a row that has ended and show one that has not
  begun, where it is given another period or taken away: `CrewSectorUserRoles`, the
  lists of a group's people and of the roles it holds, and the roles held through a
  group, whose period is where the place's and the group role's meet. The declarations
  over the three tables themselves (`CrewUserRoles`, `CrewGroupMembers`,
  `CrewGroupRoles`) list each row as the table holds it, period included.
- The subject set of a person's groups counts a place inside its period alone: the
  members' declaration says so with the set's `where:` part, so a condition that tests
  `subject.crewGroups` leaves a group out exactly when the engine stops giving its roles.
- An ended row goes at its person's next sign-in, and no job runs on a schedule. The
  generated client answers the session library's hook for a sign-in that stands
  (`crewidentity.Client.SignedIn(db)`), and the Auth's package sets it where it sets
  the invitation's hook (`session.Auth.SetSignedInHook`); an application that has a hook
  of its own calls the client's from it, since one Auth has one hook. The same hook
  announces the sign-in to the open pages (section 14). The session library calls it
  once the session is committed and logs an error it answers, so the removal neither
  fails a sign-in nor undoes one, and a sign-in with nothing to clear costs one read of
  the person's own rows. What has ended is the database's to say, by its own clock, the
  one the lists and the sign-in rule read: the statement that finds the rows carries the
  condition, before the transaction and again inside the transaction that removes them,
  so a server whose clock runs fast removes nothing that still counts. It removes the
  person's ended roles and
  places and nothing else: never the account, also when the ended role was the last
  thing the person held; the rule on a tenant's last administrator does not judge it;
  the engine is told nothing, since an ended assignment already counts for nothing; and
  each removal is written under the source `identity.EndedByItself`. A role a group
  held for a period is no one person's, so it stands, counting for nothing, until it is
  taken away or given again. Where the sign-in rule refuses a person who holds nothing,
  a person whose every assignment has ended is refused, nothing is cleared for them,
  and the rows go with the account.

**The reason of a role, and taking one for a set time.** A role given to a person may
carry the reason it was given for: one line of plain text, kept in a nullable column of
the permission store's memberships table (`Reason`; the store's `DDL()` has it, and
every policy store's schema holds it).

- The methods that give a person a role (`AssignCrewRole`, `AssignCrewSectorRole`) take
  it as `reason`, which is optional there. It is one line of at most 500 characters; a
  line break is refused (400).
- Nothing edits a reason. Giving the role again writes the period and the reason of the
  new request over the ones it had, so a request that gives none leaves the role with
  none.
- It is shown wherever the roles a person holds are listed, beside the role and when it
  ends: `CrewUserRoles`, `CrewHeldRoles`, `CrewSectorUserRoles` and
  `CrewSectorHeldRoles` carry it under a field grant of its own, so a tenant's
  administrator reads why someone from outside holds a role with them.
- `TakeCrewRole` gives the request's caller a role for a set time, with a reason: the
  act of the Break glass page. The request names no person. It names the tenant (in an
  application that has tenants; without them the role is the whole application's), the
  role, a length and the reason, which is required here. The lengths are the ones
  `CrewTakeLengths` lists (1 hour, 4 hours, 8 hours, 1 day), and the role is held from
  now until the length is over, when it ends by itself as any period does. A longer
  need is an ordinary role with an end.
- Taking gives nobody a power that giving themselves the role directly would not.
  Beside the grant of `TakeCrewRole`, the caller needs what `AssignCrewRole` needs of
  them for the same role in the same place, asked for themselves, which a super
  administrator always holds. `CrewTakeableRoles` lists the roles the caller could take,
  so a page offers what the caller may do. The one rule taking sets aside is that
  nobody lengthens their own holding: taking a role the caller holds there already
  writes the new time and the new reason over the old ones. The last administrator of a
  tenant's own is refused the taking of the role they hold there where its time ends
  sooner than their holding (403), since the tenant would have no administrator once the
  time is over. A session that views as someone else takes nothing (403), and neither
  does a caller who is not a person of the Auth.
- Ending a taken role early is taking it away, by whoever may: a tenant's administrators
  by the role file's grant on `RemoveCrewSectorRole`, headquarters by `RemoveCrewRole`.
  Nothing here is permission code of its own.
- `CrewTakenRoles` lists the roles held for a set time with a reason, whoever gave them:
  a role a person took, and a role given the ordinary way with an end and a reason,
  which is the same thing held the same way. `ended` says the time is over, by the
  database's clock. An ended row stands, with its reason, until its person next signs
  in, which removes it as it removes any ended role: nothing keeps an assignment once
  its row is gone.

**The rules** are the `identity` package's, written once over a `Store` the generated
`crewidentity` package implements with the Auth's own typed queries and patches:

- A person is added by an invitation, which gives at least one role (400 without one).
  Nobody creates an account for someone else: the account is made when the person
  accepts, with a password they choose there or through a directory.
- Nobody chooses a password for someone else. A person who has lost theirs is mailed a
  reset link and chooses the next one themselves, and a person required to change
  theirs chooses another before anything else is served (both below).
- A rename takes with it what is held under the old name: the person's sessions, the
  sessions they hold as someone else with their impersonation records, and every role
  membership. A name another user holds is refused (409).
- An address an administrator sets changes nothing until the person confirms it. The
  method mails a link to the new address once the transaction has committed, and the
  account keeps the address it has until the person follows that link from the new
  mailbox (`POST user/email/confirm`, which the login page reaches with
  `#address=<token>`). The old address is told once the change is made, and never asked
  to allow it. One change stands at a time: a second request withdraws the first one's
  link. An account always keeps an address, so the method replaces one and never takes
  one off, and the answer says whether the link went (`sent`) and, where it did not,
  why (`reason`), which the page shows the administrator.
- A password reset link is mailed to the address the person confirmed, once the
  transaction has committed, and changes nothing until it is used: the password the
  account has signs the person in and their sessions stand. The message says who asked,
  an administrator of the tenant the request was checked in or of the application. The
  link works once; the new password ends every session of the person. The answer says
  whether the link went and why not (the account has no password or no confirmed
  address, or the address was sent too many reset links: five in fifteen minutes, the
  person's own requests counted with the administrators' and apart from sign-in codes).
  The role file decides who may send one, as for every method of the set, and a link to
  a super administrator's account is sent by a super administrator alone, whatever the
  role file grants: the link changes how the account signs in.
- Deleting a user removes their role memberships, ends their sessions, and removes
  their identity links and the account; whatever else the Auth keeps for the person
  goes with the row.
- Resetting a person's second factor forgets every browser the Auth remembered for
  them, so their next sign-in from any of them is asked for the factor. The sessions
  that stand are left. The person is told at their confirmed address, once the
  transaction has committed.
- A person an administrator takes out of a tenant is told at their confirmed address,
  once the transaction has committed: when the last role they held there is taken away,
  when they are taken out of the group that gave them their last holding there, and
  when that group's role is taken away, each of its people who held nothing else there
  told. Where it was the last thing the person held anywhere and the account went with
  it, the notice says so. Nothing is mailed when an administrator deletes the account
  outright, when a tenant is cleared, for a role that ended by itself, or to a person
  who still holds something in the tenant, of their own or through another group.
- A lockout is cleared by removing the count of failed sign-ins kept under the name.
  Each instance of the application trusts what it remembers of a name for five seconds,
  a lock included, so the lock is gone from every instance within that time of the
  commit. The hold on a client that kept trying (its address, its browser) is in the
  memory of the instance that counted it and is not cleared: it ends when its time has
  passed.
- Disabling a user ends every session they hold: their own, and the ones they hold as
  someone else, each with its impersonation record. Ending all of a user's sessions is
  the same act without the disable.
- One session ends by its id; an impersonated session also ends by its record, which is
  then closed as revoked.
- A password change is required only of a user who has a password (400 otherwise): a
  person with none would be shut out with no way back in. Whether a user has one is
  asked of the database, never read into a row.
- An identity link is removed unless it is the last way in of a user with no password
  (409): another identity linked to them must be one that can sign them in, by the
  judgment the removal of a password is held to (below). The last identity that counts
  stays while only ones left from a sign-in the Auth no longer has would remain, and
  one of those goes from beside an identity that counts. A user who holds a password
  may lose every identity. The last super administrator is held to the same rule.
- A password is removed only from a user an identity that can sign them in is linked to
  (409 otherwise, 400 for a user with no password). An identity counts when the session
  library says a sign-in through it is possible (`AuthAPI.SignsInThrough`): its method
  is a sign-in the Auth's login page offers, and a registration of that method takes the
  connection it came through. One left from a sign-in the Auth no longer has does not
  count, so the password stays the person's way in. The last super administrator is
  held to the same rule.
- How a person signs in is read by the same judgment (`CrewSignInWays`, and a tenant's
  `CrewSectorSignInWays`): a row for the password where the person holds one (`way` is
  `password`), and a row for each identity that counts, with the method and the
  connection the identities list shows and the words the login page's button carries
  for the sign-in it comes through (`through`: "Lodestar"; where two registrations of
  one method both take the identity and nothing of it tells them apart, both, joined by
  "or"). Never a hash, a subject or an address.
  It is a computed list, asked for one person at a time (`filter=userId:eq:<id>`): a
  request that names nobody lists nobody's, and one that names people any other way is
  refused (400). It declares the tables that change it (`@changedBy`: the accounts and
  the identities, and for a tenant's list the role memberships and the groups too), so a
  page that holds it open reads it again when a sign-in links an identity or an
  administrator removes a password. A tenant's answers for a person of the tenant's own
  list of people, nothing for anyone else, and carries the two facts of the person's row
  (`heldElsewhere`, `heldGlobally`) for a grant's condition, with `password`, which says
  whether the row is the password. A role file grants it to whoever reads the
  identities. Lodestar's and the skeletons' grant a tenant's administrator the list
  under `password = false OR heldElsewhere = false`: the identities that count of
  every person of the tenant, and the password only of a person no other tenant holds,
  the condition `passwordChangeRequired` is read under. Whether a person another tenant
  holds too has a password is then not the tenant's to learn, the page offers no
  removal for them (it offers one by the rows), and the removal is the whole
  application's administrator's, who reads the password in the whole application's
  list. Every field of that grant is under the condition, so a tenant's administrator
  cannot ask for the list sorted or filtered by one of them (a sort or filter field
  needs a grant with no condition left once the request is checked): the list comes in
  its declared order, the password first.
- A token is revoked with the rest of its device authorization; a token that is expired
  or already revoked is not found (404).
- A role is assigned in the global scope, in one domain, or in every domain, and must
  exist there: a default role of the role file, or a custom role, which is read in the
  caller's transaction. A membership committed by a method reaches the permission engine
  without the caller saying so (the commit calls the engine's `PolicyChanged`).
- An invitation gives roles: at least one (400 without), each of which must exist where
  it is given, the global scope or the tenant `domain` names. The session library
  prepares it (`AuthAPI.PrepareInvitation`: the checks on the address, the token, how
  long the link stands) and keeps the token, the message and the sender. The row is
  written in the caller's transaction, beside whatever else the transaction writes, and
  the link is mailed once the transaction has committed (`resource.AfterCommit`). The
  answer's `sent` says whether the message went: an invitation whose message could not
  be sent is ended, not mailed, so no link stands that nobody received, and the caller
  invites again.
- Withdrawing holds to invitations. A mail link of another kind (a verification, a
  sign-up) is not found, and neither is an invitation that was accepted, withdrawn or
  has expired (404).

**Who may sign up.** Whether a sign-in method admits a person who has no account and no
invitation is chosen for each method in the running application, never in code. The
choice is a row of the Auth's sign-in methods table (`CrewSessionSignInMethods`, declared
with the Auth's other tables and migrated with them): the method's registration name,
whether it is open to sign-up, whom the method admitted when it was opened (`OpenedFor`),
and the default role a new person gets. A method with no row is invite only, which is how
every method starts, and the sign-in reads the row in its own transaction. The generated
management gives every Auth whose roles the application
assigns the page's two halves, with nothing written by the application:

- `CrewSignInMethods`, a list with one row for each method the Auth registers, ordered
  by label: its registration name and label, who can sign in with it in words
  (`admits`: "Anyone with a Google account", "Anyone with an account at acme.example",
  from how wide the method's registration is), `openSignUp`, `defaultRole`,
  `directoryGivesRoles` and `ownRule` (the two kinds of method nothing is chosen for,
  below), `selfServiceReset` (on the password method's row alone: people may ask at the
  sign-in page for a link that resets their own password), and `says`, what a person
  with no invitation meets, as a sentence. It is
  computed from the Auth's registrations and the table's rows, so a method with no row is
  listed as invite only, and a row of a method the Auth no longer registers is not listed.
- `OpenCrewSignUp` and `MakeCrewInviteOnly`, the two methods that write the row. Opening
  declares a warning (`@warning(typed: open)`, section 7): asked with `X-Warning`, the
  server says who will be able to sign in without an invitation and what each of them
  gets, with the words for the confirm button ("Open to anyone with a Google account"),
  and saves nothing. Making a method invite only says nothing first: the people who have
  accounts keep them, and new people need an invitation.
- `SetCrewSelfServiceReset`, for an Auth with a password method: whether a person may
  ask at the sign-in page for a link that resets their own password (`on`). It is off
  until an administrator turns it on, and the choice holds in the running application
  from the commit on, with no restart: the methods route names the request's path while
  it is on, the login page offers "Forgot your password?" from it, and the request's
  route answers 404 while it is off. A link asked for while it was on still works once
  it is off, until it is used or runs out, and a link an administrator sends is never
  held to the choice. Every Auth that declares the password method has the method and
  the list of its sign-in methods, whether or not it carries the choice of who may sign
  up: an Auth that carries none (its people join a tenant somebody else made) has the
  Sign-in page for this switch alone.

What a person who signs up gets depends on the application. Without tenants they get the
method's default role, a role held for the whole application: opening a method without
one is refused at the save, the Super Administrator's role is never one, and the roles
to pick from are the list `CrewSignUpRoles` (name and description). A default role is a
role given to every person who signs up, so it is one the opener could give a person
directly: the generated client asks the permission engine for the caller's grant on the
Auth's assign method with the role as the new value (the judge that already holds a
role given to a group to what its giver may give, `identity.RoleGrants`, written to the
client's `grants.go`), so a role file that conditions the assign method's grant on the
role (`"when": "new.role in [...]"`) holds the default role to the same list. Opening a
method with a role the caller may not give is refused with 403, in one sentence that
names the role as a role picker shows it, in words ("Opening the method gives every
person who signs up the role Sector marshal, which you may not give."), at the warning
as at the save, and `CrewSignUpRoles` lists only the roles the caller may give.
The grant answers before the role is looked for, so the refusal is the same for a role
the application holds and one it does not, and tells nobody of a role the grant keeps
from them.
The role is given by
the identity client in the transaction that makes the account
(`authSession.SetSignUpSeated(identityClient.SignedUp())`, which the Auth's package sets
beside the invitation's hook), so no account is left holding nothing. With tenants the
person gets an account and creates a tenant of their own (section 16, "How a tenant
comes to exist"), so the request names no role, and the choice belongs to one Auth: the one
whose session outlets serve the tenant record, whose sign-in rule admits a person who
holds no tenant yet (`signin.AdmitWithoutTenant()`). Another Auth of a tenanted
application carries neither the list nor the methods: a person who signed up to it could
be given nothing, so its methods stay invite only, and a stranger enters through an
invitation or through the directory of a tenant that names a default role. A method
whose directory gives the roles (`SyncsRoles`) is neither opened nor made invite only:
the directory decides who signs in, the two methods refuse it, and its Auth, where the
directory is the one way in, has neither. A registration that carries an account rule of
its own in code (`signin.AccountRule`) is decided by that rule, which the page cannot
read, so nothing is chosen for it either: its row of the list says the application's own
rule decides (`ownRule`), and the two methods refuse it by name with 409.

**An open method is open for the audience it was opened for.** The warning an
administrator confirms names who can sign in with the method ("Anyone with an account at
acme.example"), and that is a fact of the method's registration in code, which a later
release can widen. So opening a method writes into its row whom the registration admitted
then (`OpenedFor`, the registration's audience in a form that compares exactly:
`signin.Audience.Key`), and the sign-in honors the row only while the registration's
audience is still that one. The row keeps what identifies the people (each Microsoft
Entra tenant's id, each Google Workspace's domain, a generic provider's issuer, each
kind of account outside them) and never what they are called, so a second organization
a release lists under the first one's label is a wider audience, and a renamed
organization is the same one. A method whose registration has changed since, wider or
narrower, is invite only until someone opens it again: the list says that the people it
admits have changed since it was opened and whom it admits now, `openSignUp` is false,
the page offers Open sign-up, and the warning names the audience of today. An
invitation, and a person who has an account, are not affected. A row that keeps no
audience opens nothing, and neither does one that keeps something that is no such key:
the list says the method was opened before its audience was kept.

A person who has an account, and a person who follows an invitation, sign in the same
under either choice. The password method's sign-up routes (`POST <prefix>/user/sign-up`,
`…/sign-up/open`, `…/sign-up/complete`) are on the router of every Auth with the method
and answer 404 while it is invite only; the completion reads the row again in the
transaction that makes the account, so a link mailed while the method was open creates
nothing once it is closed. The table's rows are written through its generated patches
with change tracking on, so the change record keeps who opened a method and who made it
invite only, and when (`DataChangeEvents` under `CrewSessionSignInMethods`); the table
itself has no route, and nothing but the two methods writes it. The role file grants
List on `CrewSignInMethods` (and `CrewSignUpRoles`) and Execute on the methods to the
roles that may decide, and nobody else reads the list or the warning.

**What the application supplies.** One client per Auth, built by the Auth's own package
where it builds the Auth (the skeletons' `New`, which answers it from `Auth.Identity`),
and handed to the handler type, which returns it from an accessor named for the Auth:

```go
// In the Auth's package, once its permission engine and session manager exist.
identityClient, err := crewidentity.NewClient(accessClient,  // the Auth's permission engine
	authSession.API(),                                        // the API of the session.Auth that signs its people in
	settings.TenantMaker,                                     // what makes a tenant, for the Auth a tenant is made through
	identity.WithFirstAdministratorRoles(FirstAdministratorRoles()), // the first administrator's roles, by where they are held
	identity.WithAdministratorRoles(tenantAdministrators))    // the roles that administer a tenant

// In the handler type's package.
func (a *App) CrewIdentity() *crewidentity.Client {
	return a.crewIdentity // crew.Auth.Identity(), handed in by the configuration
}
```

The third argument is there for one Auth, the one a tenant is made through, and for no
other: its generated `NewClient(access, sessions, tenants identity.TenantMaker,
opts...)` requires what makes a tenant from an accepted invitation and refuses nil. The
application builds it with the generated `New<Record>Maker(roster, liveService)` of the
handler package (section 16, "The maker") and hands it to the Auth's package where the
Auth is built, so the roster is built before that Auth; the client hands it to the
`identity` package (`identity.WithTenantMaker`), which an application never calls. The
same client needs the roles that administer a tenant
(`identity.WithAdministratorRoles`): the person who accepts is seated with them, and an
invitation is refused while the Auth names none. An Auth whose first administrator the
deployment invites hands its client the roles that person is given, by where they are
held (`identity.WithFirstAdministratorRoles`, handed the two lists the foothold's
`FirstAdministratorRoles` returns; "At a sign-in", below). The client of every other Auth
takes the engine and the session API alone.

The generated `zz_gen_identity.go` in the handlers package asserts the accessor
(`var _ crewhandlers.Deps = (*App)(nil)`) and holds the Auth package's `TablePrefix` equal
to the one the management was generated for, so an Auth whose tables were renamed fails
the build until the application is generated again. Each method it writes there
constructs the Auth's handlers with the application's own live derivations, so a view of
the application's over the Auth's tables is live under the management's writes (section
14). The role file grants List and Read on
the resources and Execute on the methods to the roles that manage people, in the grammar
every other resource uses.

**At a sign-in.** A person comes to hold a role with no administrator signed in in three
ways: they accept an invitation, which names roles, they sign in for the first time
through the directory of a tenant that names a default role (section 16), and they sign
up through a method an administrator opened, which names a default role ("Who may sign
up", above). The session library runs a
hook inside the transaction of each step and hands it the transaction, and the client
answers each hook over its own membership write, so a role a sign-in gives is held to
the rule a role given by hand is and is committed with the step that causes it: the
invitation is spent and its roles given together, or neither; the account is linked to
the directory and given its role together, or neither, and a person who holds a role in
the tenant already is given nothing. Both read what the person holds by its period, and
neither waits for an ended row to be removed: a role whose period has ended is nothing
the person holds, so the invitation gives it afresh and the directory seats a person
whose only role in the tenant has ended; a role the person holds for a period, or one
that has not begun, is left exactly as it stands, since an invitation gives what is not
held and erases no period. The Auth's package sets them on its session manager
where it builds the client, so no Auth is open without them:

```go
authSession.SetInvitationAccepted(identityClient.InvitationAccepted())
authSession.SetSignUpSeated(identityClient.SignedUp())
authSession.SetDirectoryAdmission(settings.TenantDirectories, identityClient.DirectorySeated())
```

An invitation into a tenant gives its roles in that tenant; one into every tenant gives
them in every tenant; one into the whole application gives them in the global scope; one
that makes a tenant makes it there, in the same transaction, and gives the person the
roles that administer it ("The invitation that makes a tenant", below). The sender says
where the roles go: `domain` for one tenant, `everyDomain` for every tenant, or
neither, which is the whole application. The invitation's row keeps that place
(`TenantId` for one tenant, the boolean `EveryTenant` for every tenant, neither for the
whole application), and the acceptance gives each role there and nowhere else, whatever
another place holds under the same name by then. The mail links table of every Auth
therefore carries the column (`EveryTenant BOOL NOT NULL DEFAULT (false)`, which the
session migration `impulse` renders has), and a schema without it is refused at
generation, naming the column and the statement that adds it. The Auth's list of its
mail links serves the place as two fields (`tenantId` and `everyTenant`), so a page tells
an invitation into every tenant from one into the whole application. A link whose message
could not be mailed ends where the mail failed, and its row says so: the table carries
`MailFailedAt TIMESTAMP OPTIONS (allow_commit_timestamp = true)`, a schema without it is
refused at generation the same way, and the list serves it as `mailFailedAt`, so a page
tells such a link from one that was used or withdrawn. An invitation is held to
its place when it is sent. One that says every tenant takes roles that exist in every
tenant: a role of the whole application alone is refused (400), and a custom role whose
name a role of the whole application carries too is invited like any other, the person
being given the tenants' one. One that says neither takes roles of the
whole application alone: a role that exists only in the tenants is refused (400), in
words that say to choose the tenant it is for, or every tenant. The first
administrator's invitation is the one invitation whose roles are held in two places.
The deployment sends it for the whole application, and the acceptance gives each of its
roles where the Auth's first-administrator setup declares it
(`identity.WithFirstAdministratorRoles`): the roles the setup names for the whole
application there, with the Super Administrator role, and the roles it names for the
tenants in every tenant. No other invitation reads that declaration, and a client built
without it gives the first administrator's roles in the whole application alone. A role
that its place
no longer holds when the person accepts (the role file changed, or a custom role was
deleted, since the invitation was sent) fails the
transaction: the invitation stands and no account is created. The permission engine is
told once the transaction has committed, before the sign-in policy decides. The client
of an Auth whose roles a directory writes has neither of these two hooks: the
directory's answer at every sign-in is that Auth's roles.

A third hook runs once a sign-in stands, apart from it: the client's `SignedIn` removes
the person's roles and places in groups whose period has ended (the period of an
assignment, above), in a transaction of its own on the application's database, and then
announces the sign-in to the open pages (section 14). It is set beside the other two,
and it is the one hook the package of an Auth whose roles a directory writes sets: there
it removes nothing and only announces:

```go
authSession.SetSignedInHook(identityClient.SignedIn(resource.NewSpannerClient(db)))
```

A rename moves nothing the person holds, whoever makes it (an administrator through the
management, or the session library where the sign-in name is the person's email address
and a verified new address renames the user): the permission store names a person by the
id of their account (`identity.User`), so every role stays where it is, and the username
is what people read.

**In a transaction of the application's.** A method is a struct with an `Execute`, so the
application runs it wherever it holds a transaction, beside its own writes:

```go
err := resourceClient.ExecuteFunc(ctx, func(ctx context.Context, txn resource.ReadWriteTransaction) error {
	sector := anvil.String() // the tenant's id
	posting := &crewidentity.AssignCrewRole{UserID: ada, Role: "Dispatcher", Domain: &sector}
	if err := posting.Execute(ctx, txn, a.CrewIdentity()); err != nil {
		return err
	}

	return recordPosting(ctx, txn, ada) // the application's own rows, in the same commit
})
```

The method that gives a role (`AssignCrewRole`) names a person who has an account
already: it reads the user first and answers 404 for an id no user holds. A role is held
in one place (the global scope, the one tenant `Domain` names, or every tenant with
`EveryDomain`), and a role held somewhere else is one more `AssignCrewRole` beside it. A
person new to the application is invited (`InviteCrewUser`), and holds the invitation's
roles once they accept.

Nothing is written until the commit, so a method does not read what an earlier method of
the same transaction buffered: a role is given to a person who has an account already,
and one identity link is removed per user per transaction.

**Limits.** It runs on Spanner. A rule reads the rows as committed: a method does not see
what an earlier method of the same transaction buffered, so the rule on a person's last
role judges one removal per person per transaction, and the rule on the last super
administrator one holder per transaction. The rules of a group read the group as
committed too: who is in it and what it holds, so a person put in a group and a role
given to the group in one transaction of the application's are each judged without the
other. Every generated route runs one method, which
names one person, in a transaction of its own, so no request puts two in one; the limit
is for an application that runs the methods in a transaction it opens itself.

**The rules on who is asking.** Who may run a method is the role file's to say. Five
rules of the user management are the methods' own, whatever the role file grants, and
each judges the request's caller (a method an application runs in a transaction of its
own, outside any request, has no caller and is the application's act):

- Nobody disables or deletes their own account, which is the account of the real person
  behind the session: a person who views as someone else is refused their own account,
  which they could not get back. In a session that acts as someone else that refusal is
  the next rule's, in its sentence and with its log line, so disabling, enabling and
  deleting the real person's own account are refused the same way there. The rule is
  asked by these methods and, beneath them, by the session API's three switches
  (`DeactivateSessionUser`, `ActivateSessionUser`, `DeleteSessionUser`), which every
  generated route goes through. The session store's own `DeactivateUser`,
  `ActivateUser` and `DeleteUser` are the bare writes and are not held to it, so a
  route an application wrote calls the Manager or the session API, never the store.
  They are refused the account they view as too ("You
  cannot disable the account you are viewing as."): a session that views as a person
  does no more than that person may, and nobody disables or deletes themselves.
- In a session that acts as someone else, nobody is the subject of what they do. It is
  the rule above, widened for the person who impersonates to every change of their
  account: a person who acts as an administrator holds, for the length of the session,
  what the administrator may do, and an act on their own account would leave them
  holding it afterward. The real person behind the session is refused a role given to
  them or taken from them, a place in a group, a new password required, a password
  removed, a reset link, an address, a name, a rename, a disable, an enable, a delete,
  the end of their own sessions, their remembered browsers, a sign-in identity and a
  token of theirs, the
  unlock of the counts kept for their account, and an invitation to the address their
  account has confirmed. The refusal says so, in one sentence: "You are acting as
  someone else, so you cannot give yourself a role." The rule reads the session's record
  and nothing else, so it holds whatever the session's mask keeps and whether or not
  the application declared the kind of impersonation. It is for changes: the person's
  own row is still listed and read. The same act on anyone else goes through, and the
  impersonated session itself is still ended by the person who started it. An actor
  from another application has no account here, so the rule has nobody to name.

  An application's own action that names a person asks the same rule, before it
  changes anything, with the id of the person's account:

  ```go
  if err := identity.RefuseImpersonatorAsSubject(ctx, account, "set your own clearance"); err != nil {
      return errors.Wrap(err, "identity.RefuseImpersonatorAsSubject()")
  }
  ```

  A route that answers whether to offer such an action asks the same question without
  refusing anyone, `identity.ImpersonatorIs(ctx, account)`, so a page does not offer what
  the action would refuse. The generated create-your-tenant route does both: its `POST`
  asks the rule for its caller, who would become the tenant's administrator, and its
  `GET` answers that none is offered (section 16, "The first administrator").

  A record that decides what a person may do is held to the rule by its generated
  write. Such a record is one the policy reads about the requester: a struct that
  carries a `@subjectValue` or a `@subjectSet`, whose rows say what `subject.<name>` is
  for the person each row names (a clearance, a spending limit, a place in a team). A
  person who acts as an administrator would otherwise raise their own clearance by the
  record's ordinary write, and keep it after the session ends. So every generated add,
  change and removal of such a record, on the record's own patch route and in the
  consolidated patch, asks `identity.RefuseImpersonatorAsRowSubject` once the operation
  is decoded and before anything is written. The annotation places the call; an
  application declares nothing for it. The rule is asked about the row twice:

  - **Before the write**, about the person the stored row names, read by the key in the
    operation's path inside the write's transaction. A change or a removal of the real
    person's own row is refused ("you cannot change your own pilot record", "remove
    your own pilot record"), and so is handing that row to someone else.
  - **After the write**, about the person the operation's fields name. A row added for
    the real person is refused ("add your own pilot record"), and so is another
    person's row moved onto them ("make another pilot record your own").

  The columns asked are the ones that name a person: the column each annotation is
  written on, and every column a `who:` part compares to the requester (`Field =
  subject`). The refusal names the record by its struct's name in lower-case words.

  A row reaches a person by two more roads, and the generated write asks about each:

  - **A row that names a set the person is in.** A column a `who:` part looks up in
    another set (`TeamID IN subject.teams`) names a set and no person: a pass given to
    a team is given to everyone in it. For each such column and set the write asks
    `identity.RefuseImpersonatorAsSetSubject`, before the write about the value the
    stored row holds and after it about the value the write gives the column. The real
    person adds no such row for a set they are in ("add a pass record for one of your
    own teams"), changes and removes none, and moves none onto their own set.
  - **The row a person's value is read through.** A set or a value declared with a
    dotted `value:` (`TeamID.DivisionID`) is read through a column of another record,
    the team's own row. The write of that record asks
    `identity.RefuseImpersonatorAsReadThroughSubject` for each such column: a change
    that sets the column is refused on the row the real person's own value is read
    through ("change the division id of the team record that decides your own
    divisions"; the column is said by its name in words, and the server's log has it
    by its own). A change that leaves the column alone asks nothing and reads nothing,
    and neither does an add or a removal.

  The generated code names the column and the set, and nothing else. The library builds
  the statement at run time from the set's own declaration
  (`resource.PatchSet.SubjectSetHolds`, `resource.PatchSet.SubjectReadThrough`), with
  the pieces a condition's `x IN subject.<set>` renders, so the rule and the policy
  share one definition of who is in a set: its `who:`, its `where:` and the sets it
  builds on. The real person is asked about by name, in no one tenant. Both questions
  are asked on Spanner and on PostgreSQL, each in its own dialect of the one statement:
  on Spanner through the transaction's reads, on PostgreSQL through the connection the
  PostgreSQL transaction holds. On PostgreSQL the two questions are what is proven,
  against real rows: this package's PostgreSQL client runs no generated write yet, so
  the rule is proven from the request to the row there when that client does. A
  question that cannot be asked (a patch decoded with
  no collection, a PostgreSQL transaction that holds no connection) answers an error
  and the write does not happen, never a pass. The
  caller's grants and the session's mask answer first, so a person the record's write
  is refused gets the same Forbidden as on any record and learns nothing from the
  rule's sentence. Outside an impersonated session nothing is read: the same write
  from the person's own session goes through as it always has, and inside one the same
  write for anyone else goes through. The stored row is read by its key alone and is
  not held to a tenant, since the read only decides whether to refuse; the write that
  follows is held to its tenant as ever.

  What the rule cannot see is whatever reaches the person by another road: a role
  given to a group they are in, an invitation to an address they hold and never
  confirmed on the account, a colleague
  who returns the favor, and an action of the application's own that does not ask.
  These are the rule's stated limits. A session whose mask does not refuse the class
  `access` gives such a role. To let someone act as a person with no mask is to give
  them what the person holds. An application closes them through its mask: a kind of
  impersonation that is not to change who may do what refuses the class `access`. The
  generator gives that class to the create, the change and the delete of every record
  that carries `@subjectValue` or `@subjectSet`, and to the change of a record a dotted
  `value:` is read through, so the mask refuses those writes for every person and not
  the real one alone, with no list of records kept by hand
  (`resource.CollectionResource.WriteClasses`; a record that gains a subject value is
  covered when the application is generated again). A kind that is to keep one of
  those records names it as an exception to the class
  (`accesstypes.RefuseClassExcept(class, accesstypes.ExceptWrites(record)...)`): the
  session writes the record for other people, and the three questions above still
  refuse the real person their own row, their own set and the row their own value is
  read through.

  A row removed with its parent, which may name the person, is a limit of its own. A row
  taken with the record it is under (the record's `@deleteCascade`, or its tenant's
  delete) is deleted by the library and not through the row's own route, so the rule is
  not asked about the person it names, and a mask that refuses the row's own writes does
  not refuse the parent's delete. Where every such row gives something, the delete only
  takes away; where a row restricts (a ban, a cap), the delete gives. An application
  closes it under the mask by refusing each request that removes a row above it:

  - the parent's delete (`accesstypes.RefusePermission(accesstypes.Delete, parent)`);
  - the delete of each record further up while the cascade goes on, since the
    grandparent's delete removes the parent and the row with it;
  - an action that removes one of those rows in its own body, by name
    (`accesstypes.RefuseAction(action)`): an action is asked for Execute on its name,
    not for Delete on the record.

  PostgreSQL has referential actions that change the row where it stands, again with
  no write of it. `ON DELETE SET NULL` and `ON DELETE SET DEFAULT` rewrite the column
  that named the parent when the parent is deleted, and the refusals above close them.
  `ON UPDATE CASCADE`, `SET NULL` and `SET DEFAULT` rewrite it when the parent's key is
  changed: a rank renamed is renamed on every row that holds it, the real person's
  among them. Refusing the parent's update closes that
  (`accesstypes.RefusePermission(accesstypes.Update, parent)`, or the same on the one
  column the foreign key names, `parent.field`), with any action that changes the key.

  A column that records who made a row is another. A grant may compare a column of a
  record to the requester (`bookedBy = subject`). Such a column carries no
  `@subjectValue` or `@subjectSet` annotation, so it is not among the columns asked,
  though the row names a person and decides what they may do. While the application
  stamps the column from the session and people cannot write it (an `output_only`
  field with a `default_create_fn`), no write moves a row that exists onto the real
  person, and a row made in a session that acts as a person is stamped with the person
  acted as. A session that acts as a role is the stated case. A role is not anyone, so
  the request's `User()` there is the person who started the session, and a stamp
  taken from it puts their name on a row the role's powers made. What the grants give
  the row's author (reading it, an action on it) is theirs from then on, after the
  session has ended too, though their own roles might not have let them make the row.
  An application that does not want that closes it one of two ways: the role kind's
  mask refuses the record's create
  (`accesstypes.RefusePermission(accesstypes.Create, record)`), or the function that
  stamps the column answers nobody, a value that is no account's id, in a session that
  acts as a role, so that no grant that compares the column to the requester holds for
  the row. The session's record says it is one
  (`imp, ok := sessioninfo.ImpersonationFromCtx(ctx); ok && imp.Principal.IsRole()`);
  the request's principal does not, since a session of a person's own that the
  application's principal resolver gave a role is a role to `PrincipalFromCtx` too, and
  its `User()` is the row's true author. Where people can write such a column, the
  application closes it by refusing the record's writes under the mask.

  The unlock knows the person's counts by their username, their account and the address
  they confirmed; a count kept under an address the account holds unconfirmed (the mail
  of a first password, or of an address change asked for) is cleared like anyone's. A
  mask that refuses the class `access` refuses the library's share of those. One limit
  no class closes: an account disabled, enabled or deleted carries no class, and the
  rule knows the real person by the account they signed in with, so a second account of
  their own is someone else's to it. A person who acts as an administrator can enable
  that account again, or disable or delete it, as the administrator can; an application
  that must hold this refuses the three actions by name in the kind's mask
  (`accesstypes.RefuseAction`). Every
  method of the identity library that takes a person is held to the rule by a test that
  lists them (`guardedMethods` in `identity/impersonator_listing_test.go`).
- A person who removes a role from themselves in a tenant keeps an administrator role
  nobody else would hold there. The application names the roles that administer a tenant
  when it builds the client (`identity.WithAdministratorRoles(domain)`): they are judged
  among the tenant's holders and those who hold the role in every tenant. A holder
  through a group is a holder, in both directions: a person in a group that holds the
  role there administers the tenant and is counted, and a person who takes themselves
  out of the group that gives them the role, or takes the role from a group they are
  in, is judged as one who removes it from themselves. Each holding counts inside its
  period alone. Without the
  option a person removes no role from themselves in a tenant. A role held in the global
  partition goes freely, since a super administrator gives it back.
- The Super Administrator role (`accesstypes.SuperAdministrator`) is the framework's, in
  every auth and in no role file, held in the global partition alone. Only a holder makes
  or removes another: giving the role, taking it away, inviting a person to it and
  deleting a holder's account each take a caller who holds it, and so do disabling a
  holder's account, which removes a super administrator in effect, and enabling one,
  which makes one who can sign in. A caller who came
  through another Auth (`Manages`) holds no role of this one. Only a holder changes how
  another signs in, since each such act is a way to become the person or to shut them
  out: sending a holder a password reset link, removing their password, requiring them to
  change it, renaming them, setting their email address (where the code of their second
  factor is mailed) and unlinking one of their sign-in identities each take a caller who
  holds the role, whatever the role file grants; a holder acts on themselves and on
  another holder as the role file allows. The caller these rules judge is the real
  person behind the session: a person who views as a super administrator is not one.
  Giving the role ends nothing: the sessions the person holds, their bearer tokens and
  the browsers they asked to be remembered stand, and the session they are signed in
  with acts with the role at its next request. A person who is signed in when the role
  is given is no risk for being signed in; what the role guards is the account, which
  is asked for a second factor at every sign-in from then on. What an administrator
  set on the account before the role, a password or an email address, is left as it is
  too: nobody but a holder gives the role, and the holder who gives it chooses whom to
  trust, the account as it stands included. These are the rules of an auth whose roles
  the application gives. In one whose roles come from its directory the directory
  decides who is a super administrator, the role synchronization writes and removes
  the role at each sign-in as it does any other, and none is seeded. There is always one who can
  sign in: the role is not taken from the last holder whose account is not disabled, and
  that account is neither disabled nor deleted, whoever asks, the application's own
  transactions included. What the role does is fixed where the engine is built: the
  generated client names the methods that give and take the Auth's roles
  (`crewidentity.SuperAdministratorMethods()`), the Auth's package hands them to the
  engine (`access.WithSuperAdministratorMethods`), and a super administrator then runs
  them whatever the role file grants, for any person, in the global scope, in one tenant
  and in every tenant. The client refuses an engine built without them, so an
  application that left the option out does not start.
- While a tenant is directory only, who administers it is changed from inside it, or by
  a super administrator. Directory only is the tenant's switch to keep everyone out who
  does not come through its directory, the application's own staff included, and the
  administrators' way in (a password and the mailed code) is for a person given an
  administrator's role in that tenant itself; a caller whose role file lets them hand
  out roles everywhere would otherwise make that true of themselves. So one judgment is
  asked before each act that could: does the tenant admit the caller's own session
  (`resource.UserPermissions.Reaches`, the reach every request of theirs is held to: a
  session through the tenant's directory, a super administrator's, and the session of
  one of the tenant's own administrators signed in with a password and the code). A
  caller it does not admit is refused (403) giving or taking one of the tenant's
  administrator roles (`access.WithTenantAdministratorRoles`) in that tenant, to a
  person or to a group kept in that tenant, putting a person in or taking one out of a
  group kept there that holds such a role, and an invitation that carries such a role
  there: "… is a role that administers a tenant, and this tenant admits only people who
  sign in through its own directory, which your sign-in does not enter. Who holds the
  role there is changed by one of its administrators, or by a super administrator". The
  account of a person who holds such a role there, of their own or through a group kept
  in that tenant, in an assignment that has not ended, is held to the same judgment,
  since each act on it is a way to become the person or to shut them out: a required
  change of password, a removed password, a new sign-in name, an
  address set, an identity unlinked, the account disabled, enabled or deleted are
  refused with a sentence that says the same of the account. A person who administers
  more than one tenant is changed by a caller every one of them admits, and the refusal
  says so: "Their account is changed by someone whose sign-in enters every tenant they
  administer, or by a super administrator". The sentences use the application's word for
  a tenant (`WithTenantWord`). The list of roles a caller could give (`RolesToGive`)
  leaves such a role out for a caller who would be refused. A session that views the
  application as one of the tenant's administrators gains nothing: an impersonated
  session reaches a directory-only tenant only when the real person behind it is a super
  administrator, so it is refused the same acts. What makes a person a tenant's
  administrator is the permission engine's statement (`access.AdministersTenant`), the
  one the way in is decided on: a role held in every tenant or in the whole application,
  or by a group kept for the whole application, administers no one tenant and is not
  judged, and neither is an assignment that has ended, an act in a tenant that is not
  directory only, or one the application runs in a transaction of its own outside any
  request. The judgment covers the acts that give the way in or take over an
  administrator's sign-in, and no others: ending a person's sessions, forgetting their
  remembered browsers, unlocking their sign-in, revoking a token, withdrawing an
  invitation, changing the name a person is shown by and clearing a deleted tenant are
  not judged. Nor does it look back or ahead: a person put in a tenant's group before
  the group holds an administrator's role, an invitation that carried the role, sent
  before the tenant became directory only and accepted after, and a password or an
  address set on an account before its person was given the role all stand. The caller's
  reach is read from the tenant roster and the engine's snapshot each instance holds, as
  every request's is, so a change made on another instance decides once it has arrived.
  The lists hold what they offer to the same judgment where a method names its row
  (`@callerEnters`, section 5): a person's row leaves out the acts on their account, a
  group's row the method that puts a person in the group, and a row of the people of
  the groups the method that takes the person out, for a caller the method would
  refuse. The whole application's two methods that take a role away, from a person and
  from a group, name no row, and the rows a page runs them from answer for them
  (`@callerEnters` naming the lists): a row of the roles people hold, or of the roles held
  for a set time, leaves out the removal of a tenant's administrator's role held in that
  tenant itself, and a row of the roles the groups hold the removal of such a role from a
  group kept in that tenant, for a caller the method would refuse.
- The removal of a person's last role anywhere deletes the account, with its identity
  links and sessions, in the same transaction and whoever removed the role: an account
  nothing admits is not kept.

A caller is named by the id of their account, as the permission store names anyone, and
the first three rules take the caller for the person acted on only where the request came
through the Auth being managed. An outlet's
routes bind the name of the Auth that signs its people in to every request
(`live.Subscribing(crew.Name)`, read back with `resource.AuthFrom`), and the client holds
the name it was generated for (`crewidentity.AuthName`, which the application's generated
handlers hold equal to the auth package's `Name` at compile time). On an outlet that
serves another Auth's management (`Manages`), the caller is another Auth's person, whose
id is no account of the one being managed: there nobody is refused as acting on themselves.

**One tenant per person.** In an application whose tenants own their people, a person
belongs to one tenant: the tenant that created or invited them manages the account, which
it cannot do for a person another tenant holds too. The application says so where it
builds the client, beside its administrator roles (`identity.WithOneTenantPerPerson()`),
and every way a person comes to hold a role in a tenant is then held to it:

- A role in a tenant given by hand is refused (409) for a person who holds a role in
  another tenant.
- A role is held by an account and never by a name, so an account that is created holds
  nothing until a role is given to it and a rename moves no role: neither brings a person
  into a second tenant.
- An invitation into a tenant is refused when it is sent (409) where a user has
  confirmed the address and holds a role in another tenant, so the sender is told and
  nobody is mailed a link they cannot accept. An address counts once its holder has
  confirmed it, as at a sign-in: one an administrator typed onto an account says nothing
  of who reads its mail, so a tenant cannot keep another from inviting an address by
  writing it on one of its own accounts. An address no user has confirmed is invited,
  and whoever accepts is held to the rule then.
- An invitation that makes a tenant is held to it the same way: sent to an address
  whose confirmed account already belongs to a tenant, it is refused when it is sent
  (409), since the account would belong to two, and whoever accepts one is held to the
  rule at the acceptance, where nothing is made for a person who belongs to another
  tenant.
- An invitation accepted, and a directory's default role, are refused inside the
  sign-in's transaction: nothing is consumed and no account is made, and the sign-in ends
  on the login refusal code `other_tenant`, which the login page holds a sentence for.
  The roles of one sign-in are judged together, so a stranger from one tenant's
  directory who carries another tenant's invitation is refused, and so is a stranger
  from a directory two tenants declare with a default role.

A refusal says the person belongs to another tenant and never which.

The application's own people are outside the rule. A person who holds a role for the
whole application or in every tenant is no tenant's to manage, whatever else they hold
(the role files grant a tenant its account methods on a person it holds alone,
`heldElsewhere = false AND heldGlobally = false`), so they are given a role in any
tenant: an operator who is asked into one customer's tenant after another holds a role
in each. Such a
role is no tenant's either, so it never counts as one and is never refused. So that
nobody is left in several tenants with nothing outside them, the last role a person
holds outside the tenants is not taken away while they hold roles in more than one
(409): take the roles in all but one tenant away first. A place in a group of the whole
application is held outside the tenants as a role is, so a person is not taken out of
the last such group either while nothing else of theirs is.

The rule reads what is committed, as the other rules do: one transaction of the
application's own gives a person roles in one tenant. A person who already holds roles
in two tenants when the option is turned on is given no further role in a tenant until
they are down to one. One writer is outside it: a directory's role sync gives and takes
roles for the whole application and in every tenant alone, on the directory's word.
Without the option a person may hold roles in any number of tenants, as in the skeletons
and in Lodestar.

**The pages.** No application writes a page for an Auth's management. The generated API
description (`zz_gen_api.ts`) carries an `identity` block beside its resources and
methods: for each Auth whose management the outlet serves, the Auth's name and label,
the tenant record's name where the application has one, and a side for each scope
(`global`, the whole application's; `domain`, a tenant's) that names the lists by what
each holds (`users`, `roles`, `rolesToGive` (the list a role is picked from by the methods that give one), `identities`, `signInWays`, `invitations`, and for the whole application
`sessions`, `signInAttempts`, `signInMethods`, `signUpRoles` and, for the Auth a tenant is made through, `invitedTenants`) and the methods by what each does (`invite`,
`withdrawInvitation`, `assignRole`, `removeRole`, `disable`, `enable`, `delete`,
`rename`, `setEmail`, `sendPasswordResetLink`, `requirePasswordChange`,
`endUserSessions`, `revokeTrustedDevices`, `unlinkIdentity`, `removePassword`, and for
the whole application `endSession`, `unlockSignIn`, `openSignUp`, `makeInviteOnly`, `setSelfServiceReset`, `inviteTenant` and `withdrawInvitedTenant`). The names differ in every application
(`CrewSectorUsers`, `DisableCrewSectorUser`); the keys do not. A side names only
what the outlet serves, so a method the Auth's shape leaves out, or a list the schema has
no table for, is absent, and a side without its list of people is not listed. The Angular
library builds the pages from the block at run time
(`identityRoutes(apiDescriptor, resourceMeta, { auth, scope })`, from
`@impulseframework/resource-angular/identity-pages`): the people with a menu on each row
holding the methods the server lists for that row, a person's row page with the roles
held, the ways they sign in and the sign-in identities under it (and the sessions, for
the whole application), the invitations, and for the whole application the page staff
bring a tenant in from, named by the tenant record ("Invited sectors": the invitations
that still stand with a column for each detail the caller may read, the method that
sends one above the list, and Withdraw on each row, each offered only under its grant),
the sessions, the failed sign-ins and the Sign-in page: one row for each sign-in method, Open sign-up on a
method that is invite only and Make invite only on one that is open, each only where the
caller holds the grant and never on a method the directory or the application's own rule
decides, the method's warning drawn by the method dialog, and a notice above the list
while a method is open. Above the ways a person signs in, the page offers
Remove password for an Auth with a password method where the removal can happen: the
server's answer for the person's row lists the method, and the list shows a password and
an identity that counts. It asks first, naming what is left by the words of the rows, and
the offer goes with the password once the list is read again.
A period is picked and shown in days. The server keeps an end as the first instant the
assignment no longer counts, and "until 1 December" means through the end of
1 December: the dialog of a method that gives something for a period sends the day
picked for its end as the first instant of the day after it, in the viewer's time zone
(the method presentation's `throughDay`), and a list shows the last day the assignment
counts, the day that was picked.
They are the library's list and row pages over these resources, drawn from a
configuration the library writes; what a page offers is what the server answers the
caller may do. Lodestar's console takes its accounts pages this way, once for each side.

**Tests.** The generated authorization matrix carries the lists and reads as query pairs
and the methods as denied-only cases. Lodestar's `test/integration/generated_identity_test.go`
drives the methods over the served application (the invitations, whose mail the test
reads, in `generated_identity_invitation_test.go`), `generated_identity_lists_test.go`
holds a sector's lists (each sector's rows, a person of two sectors in each, the
facts, and the grants that carry a condition on one),
`generated_identity_tenant_methods_test.go` a sector's methods (each account method
allowed on a person the sector holds alone, refused on one it shares and not found for
another sector's; the roles given and taken; the identities and the unlink) with
`generated_identity_tenant_invitation_test.go` for the ones that need the session manager
(the invitation, its withdrawal, the required password change),
`generated_identity_remove_password_test.go` holds the removal of a person's password at a
sector and the list of how a person signs in beside it (the password and the identities
that count, an identity of a retired sign-in left out, the sector's people alone), and
`generated_identity_remove_password_skipauth_test.go` over the served
application with the simulated directory (refused while the person holds no identity and
for a role without the grant; once removed, the password signs nobody in and the directory
does; the list in each state),
`generated_identity_one_tenant_test.go` holds the rule of one tenant per person through a
second client built with the option, since Lodestar itself lets a person hold roles in
several sectors,
`generated_identity_groups_test.go` and `generated_identity_periods_test.go` hold the
groups of people and the period of an assignment, and
`generated_identity_group_rules_test.go` the rules of a change to a group (a sector's
form takes the sector's people; who is in a group is changed by someone who may give or
take the roles it holds; nobody lengthens their own holding; the roles held through
groups show the one that counts),
`generated_identity_live_test.go` holds that a method's commit reaches the pages of a
resource derived from the Auth's tables, a sector's open list among them, and its
console's `identity-pages.spec.ts` holds the pages the library builds for the two Auths
to the generated metadata: every resource, column, field and method a page names is one
the description carries.

## 22. Update-or-create and create-unless-there

Application code sometimes writes a row without knowing whether it is there: a task a
queue delivers twice, a sync that runs again, a membership given to a person who may hold
it already. The generated patches say both intents, and neither is a third kind of write:

```go
// Update the row; create it when it is not there.
patch := resources.NewTenantSettingUpdatePatch(tenantID).SetTheme(theme).OrCreate()
if err := patch.Buffer(ctx, txn); err != nil {
	return err
}
created := patch.RowCreated()

// Create the row; leave it exactly as it is when it is there.
patch := resources.NewUserRoleCreatePatch(kind, axis, domain, role, user).SetCreatedAt(now).OrSkip()
if err := patch.Buffer(ctx, txn); err != nil {
	return err
}
skipped := patch.RowSkipped()
```

**What runs.** `Apply` and `Buffer` read the row's key inside the transaction and then run
the create or the update exactly as a plain `CreatePatch` or `UpdatePatch` runs it. So the
two cases differ in everything the resource declares for them, with nothing said at the
call: a `default_create_fn` and the `@state` default fill only a row that is created, an
`output_only_update_fn` stamps only a row that is updated, the create's or the update's
validation runs, an armed patch (`Enforce`) is held to the Create grant or the Update grant
with its condition, and the change record is the create's or the update's. The mutation is
a plain insert or a plain update; Spanner's insert-or-update is not used, and
`resource.CreateOrUpdatePatchType` is left as it was.

| | The row is there | The row is not there |
| --- | --- | --- |
| `UpdatePatch.OrCreate()` | the update, whole; `RowCreated()` is false | the create, whole, with the fields the patch carries; `RowCreated()` is true |
| `CreatePatch.OrSkip()` | nothing: no default, no validation, no write, no change record; `RowSkipped()` is true | the create, whole; `RowSkipped()` is false |

**Which patches.** Every update patch offers `OrCreate`, since an update names its row. A
create patch offers `OrSkip` only where the caller names the key (a composite key, an
interleaved table's key, a key that is a foreign key, a key that is not a UUID): a create
patch that makes its own id
never names a row that is there, so the method is not generated and a call does not
compile.

**The same fields in both cases.** An `OrCreate` patch writes the fields it carries
whether it updates or creates. Where the two must write different things (a value given
only at creation, a counter raised from the stored value), read the row and build the
create patch or the update patch; `OrCreate` is the short form for the case where both
write the same.

**The read.** It asks for the key alone. In a tenant request it is held to the tenant, so
a row another tenant holds under the same key reads as not there, and the create that
follows is refused at commit as any create of a taken key is (section 10). It is made
inside the read-write transaction, so of several writers racing to update-or-create one
row exactly one creates it and the others update it; none is refused.

**The grant.** A patch armed against a caller (`Enforce`) is asked for the grant of what
it is before the row is looked for, so a caller who does not hold it gets the same
refusal whether or not the row is there, and learns nothing from the answer. An `OrSkip`
is a create: it is checked for Create with the fields it carries, and a Create grant's
row condition is judged only when the row is created. An `OrCreate` is an update: it is
asked for Update on the resource, and the fields it carries are then held to the Update
grant when the row is there and to the Create grant when it is not, each with its
condition. A caller who holds Update and not Create is refused when the row is not
there, which says no more than the 404 a plain update gives the same caller.

**Rows of the same transaction.** A read does not see what its own transaction buffered,
which is why two plain creates of one row in one transaction are refused at commit. An
`OrCreate` or `OrSkip` patch asks the transaction's record of the rows its patches wrote
before it reads: a row an earlier patch of the transaction wrote is there, and one it
deleted is not. So a second `OrSkip` of one row skips, and a second `OrCreate` updates.
One limit is the update path's own: under a grant with a row condition, and in a tenant
request, an update's check reads the stored row, so an `OrCreate` of a row the same
transaction created is answered not found, exactly as a plain update of that row is. An
`OrSkip` of such a row skips.

**A transaction that runs again.** A transaction Spanner aborts runs its function again,
and the row may be there on the second attempt where it was not on the first. The patch
decides again on every attempt, and what the earlier attempt's path put on it (a create's
default, a value a hook rewrote) is taken back first, so a patch built once outside the
function is safe to buffer inside it.

**In a batch.** The consolidated endpoint and a resource's own PATCH route take both as
operations, beside `add`, `patch` and `remove`:

```json
[
  {"op": "patch-or-add", "path": "/work-order-tasks/7f3c9a52-0c1e-4d6b-9a57-2f1d6f0b8e11/4", "value": {"instructions": "Seal the aft lock", "done": false}},
  {"op": "add-or-skip", "path": "/work-order-tasks/7f3c9a52-0c1e-4d6b-9a57-2f1d6f0b8e11/5", "value": {"instructions": "Test the seal"}}
]
```

Each names its row in the path, as a `patch` does. A `patch-or-add` is decided inside the
batch's transaction (`Decoder.DecideOperation`: the caller's Update grant on the
resource, then the read of the key) and is then decoded and run as the `patch` or the
`add` it turned out to be. An `add-or-skip` runs as the `add` it is, with its create
patch marked `OrSkip`. So the fields a body may carry, the grant with its condition, the
validation and the change record are a patch's or an add's, with nothing of their own,
and an earlier operation of the same batch counts: two `add-or-skip` of one row land,
where two `add` of it are refused at commit.

Only a resource whose key the client supplies takes them. One whose id the server makes
answers 400 (`Missions takes add, patch and remove, not "patch-or-add"`): no request
names a row of it that may or may not be there. A `patch-or-add` carries one value,
written in both cases, and over the wire a patch may not carry every field a create may.
So a resource whose create must carry a field no patch may carry (an `immutable` field
with no default) takes `add-or-skip` alone, and answers a `patch-or-add` with a 400 that
names the field.

**In the browser.** The generated client builds both for `client.batch`
(`web/projects/resource`): `handle.ops.patchOrAdd(value)` and
`handle.ops.addOrSkip(value)`, where the value's key fields become the path. Each builder
exists only on the handle of a resource that takes the operation, so a call the server
would refuse does not compile. The value of `patchOrAdd` holds what a create must have
and what a patch may change; a field only a create takes does not compile in it. A page
offers an action built on `patchOrAdd` where the caller holds both Create and Update,
since it cannot know which of the two the server will ask for.

**In the library.** The generated identity store writes a membership as a
create-unless-there (`InsertMembership`), so a role a person holds already keeps the time
it was given and giving it twice in one transaction is no error.

**Tests.** `resource/patch_fork_test.go` holds the decision, the rows of the same
transaction, the attempts and the armed cases against the mock transaction, with the
refusal that comes before the read; `resource/patch_fork_spanner_test.go` holds the same
over the emulator, with the racing writers, the change records and the commit that
refuses two plain creates; `resource/operations_fork_test.go` holds the two batch
operations and the decoder's decision; `generation/patchfork_test.go` holds which
generated patch offers which method, and which handler and which client takes which
operation. Lodestar serves both operations over a refit's tasks
(`lodestar/test/integration/batch_decided_test.go`), and the browser client's specs are
`web/projects/resource/src/batch.spec.ts` and `patch-or-add.types.spec.ts`.

## 23. Secrets at rest

A field declared `conditions:"secret"` is kept in the database encrypted. Anyone who can
query the database, or read a backup or an export, reads bytes that open only inside the
service. There is no plain kind: the generator refuses a stored secret that is not typed
`resource.Secret`, so a new secret field cannot be stored as it was typed.

```go
// TransponderKey is the key the ship's transponder signs its answers with.
TransponderKey *resource.Secret `spanner:"TransponderKey" conditions:"secret"`
```

```sql
TransponderKey BYTES(106),  -- a key of up to 64 bytes, and the 42 sealing adds
```

**The application secret.** One secret of the application, 32 random bytes, kept in
versions. It never encrypts anything itself: at start the service derives one working key
for each use and each version from it with HKDF, under a fixed label naming the use and the
version (the session library derives its cookie key the same way), so the keys that seal
resource fields have no relation to the keys of any other use.

**What a column holds.** One value of three parts: the version of the application secret
that sealed it (two bytes, the high byte first, 1 to 65535), a random number drawn for the
write (24 bytes), and the encrypted value with its 16-byte check. The cipher is
XChaCha20-Poly1305 (`golang.org/x/crypto/chacha20poly1305`). Sealing adds 42 bytes
(`resource.SecretOverhead`), so a column that takes a value of up to n bytes is
`BYTES(n+42)`, or `BYTES(MAX)`; the value limit a request is held to (section 11) and the
form's `maxLength` are the column's length less 42, counted in bytes. The version takes
two bytes because a deployment's version numbers never repeat, and a rotation that fails
uses one up: the 255 versions one byte names would run out in a secret's life, with no
way forward.

**The context.** The row's primary key, every key column in key order, is mixed into the
check when a value is sealed and again when it is opened. It is never stored: each
operation takes it from the row it works on. Bytes copied onto another row do not open
there. The table's and the column's names are left out, so renaming either breaks nothing;
the key order is the key's own (the generated `SecretContext`), so reordering the struct's
fields breaks nothing either. A key column the database stamps at commit cannot bind a
secret, and a write of one is refused.

**`resource.Secret`.** The field's type. It holds the stored bytes and nothing else, and
goes in and out of the database unchanged, on Spanner and on PostgreSQL. It prints as
`[secret]` in JSON, in a log line, through `fmt` and in an error's text; it refuses to be
read from JSON; and it has no method that gives the value. `KeyVersion()` says which
version sealed it.

**`resource.SecretKeys`.** The key holder, built once at start from the versions of the
application secret, and the one thing in the process that holds key material (it keeps the
derived keys, not the versions it was built from). The highest version given is the current
one. The data level builds it and hands it to the database client:

```go
keys, err := resource.SecretKeysFromEnv("APP_SECRET_KEY", os.Environ())
// ...
db, err := database.Open(ctx, env.Database.Settings, resource.WithSecretKeys(keys))
```

`resource.WithSecretKeys` is an option of the Spanner, PostgreSQL and mock clients alike
(`resource.NewSpannerClient(db, resource.WithSecretKeys(keys))`); given twice, the client
panics at construction. `resource.NewSecretKeys(map[int][]byte{1: ...})` builds the holder
from fixed bytes, which is what a test does.

**The configuration.** The application's configuration declares the secret beside its
other variables, `secret:"versioned"`: a secret the deployment generates, of which the
service is handed every enabled version.

```go
SecretKey string `env:"APP_SECRET_KEY" secret:"versioned"`
```

No variable has the declared name. Each version arrives in a variable of its own, the name
and the version's number (`APP_SECRET_KEY_1`, `APP_SECRET_KEY_2`), holding the version's 32
bytes in Base64, and `resource.SecretKeysFromEnv` collects whichever are set, reading the
whole environment so that no variable the name numbers is passed over. With none set
it answers no error and a holder that holds no key, so an application that stores no
secret runs without the variable, and one that stores a secret is refused at start by the
name of the variable that is missing (`APP_SECRET_KEY_1`). The declared name itself set to
a value, with no numbered version beside it (`APP_SECRET_KEY=...`), is the secret put in
the wrong variable: an error naming it and the variable to use. A variable that is set and
does not hold a version is an error naming the variable, never its value. A stored value
names its version in two bytes, so a secret's
versions run from 1 to 65535: the variable of a version past 65535, or of version 0
(`APP_SECRET_KEY_65536`, `APP_SECRET_KEY_0`), is an error at start that says the range.
The development environment file carries `APP_SECRET_KEY_1`, generated: the skeletons'
and Lodestar's `.envrc.template` write 32 random bytes to `.app-secret-key.dev` the first
time the file is sourced from the application's root and read the variable from that file
from then on, so a checkout keeps one key and `.gitignore` keeps it out of git. No
template holds a key: a key in a tracked file is every checkout's, and the library cannot
tell it from a real one. `impulse check` (`env-template`) holds the template to that: the
variable set by a line, never a key written in the template, the key's file ignored; and
`--fix` writes the two lines.

**Writing.** No code of the author's. A request and a typed patch give the value as text
(`patch.SetTransponderKey(&key)`; the generated request structs, the typed patches'
setters and the TypeScript shapes carry a `string`), and the write path seals it where it
turns the patch's fields into columns (`PatchSet.Resolve`), with the keys of its
transaction's client and the row's primary key. The same value sealed twice gives different
bytes. A secret set to the value its row already holds is still no change: the write path
opens the stored value to compare, so the update that changes nothing writes no change
record. A grant's condition never reads a secret: the proposed value is left out of the
statement the conditions are judged in.

**Using a value.** The generator writes two methods on the resource struct for each secret
field. Both take the context from the row, so an author cannot pass another row's key, and
a query that reads a secret field reads the row's key columns with it.

```go
ship, err := resources.NewShipQuery().AddColumns(resources.NewShipColumns().TransponderKey()).SetID(id).Read(ctx, txn)
// ...
key, err := ship.Data.OpenTransponderKey(keys)       // anywhere: opens, writes nothing
key, err := ship.Data.OpenTransponderKeyIn(ctx, txn) // in a read-write transaction
```

**Rewriting under a new version.** A value that names an older version than the current
one is written again under the current one the next time it is set, like any write, and
the next time it is opened inside a read-write transaction (`Open<Field>In`, which takes
the keys from the transaction's client). That rewrite is the stored bytes alone: the value
does not change, so no permission is asked, no update function stamps the row and no change
record is written, and it is buffered only when the row, read again inside the transaction,
still holds the bytes that were opened, and never for a row a patch of the same transaction
has already written, since no read sees a buffered write. `Open<Field>(keys)` never writes. At a rotation, a
request already running on a revision that was not handed the new version can meet a value
the new revision rewrote, and answers an error for that request.

**Retiring a version.** A version of the application secret can be disabled once no
stored value names it, and a value nobody sets or opens keeps the version that sealed it.
The application's migrate command retires a version, named by the variable it is handed
over in:

```
migrate -retire-secret APP_SECRET_KEY_1
```

The command opens the data level and calls `resource.RetireSecretVersion` with the
application's secret holders, the generated `SecretHolders()` of each resources package
(one `resource.SecretHolderOf[T]()` for each resource that stores a secret; nil for a
package that stores none). Every stored value that names the version is opened and written
again under the current version, a batch to a transaction, and a final read of every secret
field finds none left. It exits 0 only then, and prints how many values of each field it
wrote, never a value.

A retirement reads the packages its call is handed and no other, and the call names them
by hand: one package's `resources.SecretHolders()...`, or several joined,
`slices.Concat(consoleresources.SecretHolders(), sharedresources.SecretHolders())...`.
`impulse check` (`secret-holders`) fails an application whose call leaves out a package
that carries the generated list, takes its holders from where the check cannot read them
(a parameter, a function of the application's own, a list written by hand), or is missing.

Its environment is the migration's with one variable for each enabled version, the one
being retired among them, since its values are opened under it: a run that was not handed
the version it retires is refused before anything is read, naming the variable that is
missing (`APP_SECRET_KEY_1 is not set`) and the versions it was handed. The current
version, the highest the environment carries, is refused: new values are sealed under it.
So is a variable that is not a version of the secret the keys were read from, and one
whose number is 0 or past 65535, with the range a version's number takes.

A value that does not open (bytes moved from another row, or altered) is left as it is and
does not stop the run: every value that opens is written, in every field of every holder,
and the retirement then fails saying how many values in all do not open and how many it
wrote, and naming the first ten by resource, field and row. Set each again or clear it,
and run the retirement again. A second run finds nothing to write and exits 0. The service
keeps serving meanwhile: a value is written back only while its row still holds the bytes
that were opened, read again inside the writing transaction, and the rewrite is the stored
bytes alone, with no permission check, no update function and no change record. It runs on
Spanner through the resource client and on PostgreSQL by statements of its own, the
PostgreSQL client's transactions not being built; on both the rows are read in key order
and each row once, PostgreSQL a page of 200 at a time after the last key of the page
before, so values that do not open never hide the ones behind them.

**Refusals.**

- A stored field declared `secret` and typed a plain `string` or `[]byte`, or on a column
  that is not `BYTES` with room for a sealed value: refused at generation, with the type
  and the column to give it. A field typed `resource.Secret` and not declared `secret` is
  refused the same way.
- An application that stores a secret and was handed no version of the application
  secret: refused at start. The generated router's `New` calls
  `resource.RequireSecretKeys` with every secret field and panics naming them and the
  variable that is not set (`APP_SECRET_KEY_1`, from the name the keys were read under),
  with what it holds and how to make one; a client built with no keys at all is told to
  read them (`resource.SecretKeysFromEnv`) and wire them (`resource.WithSecretKeys`). A
  write of a secret through a transaction that holds no keys is an error too, and names
  the variable the same way. A command that opens the data level without the generated
  router (the migrate and bootstrap commands) is not refused at start, and fails at its
  first use of a secret.
- The declared name set with no number (`APP_SECRET_KEY`) and no numbered version beside
  it: refused where the keys are read, naming it and `APP_SECRET_KEY_1`.
- A value that names a version the service was not given: a
  `*resource.UnknownSecretVersionError` when it is opened, naming the version and the
  versions the service holds.
- Bytes moved from another row, or altered: `resource.ErrSecretNotOpened` when they are
  opened.
- A configuration variable named as a version of a versioned secret is (`APP_SECRET_KEY_1`
  beside `APP_SECRET_KEY` declared `secret:"versioned"`): refused when the application is
  read (`impulse check`, the deployment's tooling), since the deployment hands the version
  over under that name.

**Tests.** `resource/secret_test.go` holds the type (nothing prints it), the key holder (a
value opens on its row and on no other, under the version that sealed it and no version the
holder was not given, the versions one byte cannot name among them), the write path and
the rewrite against a capturing transaction; `resource/secret_spanner_test.go` and
`resource/secret_postgres_test.go` hold the column (no plain value, the change record
names the field without its values) and a rotation over the emulator and a PostgreSQL
container, and the retirement of a version on both, of versions 256 and 258 under version
65535 as of version 1 under version 2; `resource/secret_retire_test.go` holds the
retirement's refusals, its final check, and that it tells a version from one that shares
one of its two bytes;
`generation/secret_test.go` holds the refusals, the generated methods and the secret
holders. Lodestar's ship carries a transponder key
(`lodestar/test/integration/secret_test.go`), and its migrate command retires a version
over the emulator (`lodestar/cmd/deployment/migrate/main_test.go`).

## 24. The change history

A resource that tracks its changes (`TrackChanges`) writes one record to the
`DataChangeEvents` table for each create, update and delete, in the write's own
transaction: `TableName` and `RowId` say which row, `Sequence` numbers the records one
transaction writes for the row, `EventTime` is the commit timestamp, `EventSource` says
who made the change (section 6, `UserEvent`), and `ChangeSet` holds each changed field
with its old and its new value. `TenantId` holds the key of the tenant the row belongs
to, empty for a row of a table that belongs to no tenant. A secret field is named as
changed with neither value (section 23).

**The table.** `resource.DataChangeEventsDDL(dbType, period)` renders the table's
statements. An application copies them into its first migration, as `impulse new` writes
them and as Lodestar does in
[000001_Baseline.up.sql](../lodestar/schema/migrations/000001_Baseline.up.sql), pinned
to the function by a test. The function has statements for Spanner alone: the package
does not write to PostgreSQL yet, and the table gets its PostgreSQL statements with that.

**Each record's tenant.** The generator declares each tenant resource's way to its
tenant (`TenantBinding`, from its `@domain`; the tenant record's is its own key), and the
patch set writes the tenant on every record, whoever writes the row. A write armed in a
tenant's request takes the request's tenant. Any other write (a method's body, a job, a
bootstrap, a delete that takes rows with it) resolves it from the row: the value the
patch or the stored row holds, then one point read for each step of the way, which sees
the rows the same transaction has buffered. An update that moves a row to another tenant
records the new tenant. A record keeps the tenant its row had when it was written: a
parent moved later in the same transaction does not change it. The index
`DataChangeEventsByTenant` on `(TenantId, EventTime DESC, TableName, RowId, Sequence
DESC)` reads one tenant's records newest first:

```sql
SELECT TableName, RowId, EventTime, EventSource, ChangeSet
  FROM DataChangeEvents@{FORCE_INDEX=DataChangeEventsByTenant}
 WHERE TenantId = @tenant
 ORDER BY EventTime DESC, TableName, RowId, Sequence DESC
 LIMIT 200
```

`impulse check` (`change-history`) says when an application's table lacks a column or
the index.

**More than one write of a row in one transaction.** A record's old values are the row as
it stood just before that write, counting what the same transaction's earlier patches
wrote: the stored row with those writes laid over it. A row created and then updated in
one transaction has two records, numbered by `Sequence`, and the second's old values are
the ones the create gave. A row updated twice has a record for each step, and a change put
back is recorded as a change. A row created and deleted in one transaction has both
records, though it is never stored. A row the transaction deleted is not there for a later
update of it, which is answered 404; a second delete of it writes no second record. On a
row the transaction created, a field no patch set reads as empty: a default the database
gives the column is not known before the commit.

**How long records are kept.** The application's schema says it. `period` is
`resource.KeepChangeHistoryDays(n)` or `resource.KeepChangeHistoryForever()`, and a
period that was never declared renders no statement. With a number of days the
statements end with a row deletion policy on `EventTime`:

```sql
ALTER TABLE DataChangeEvents ADD ROW DELETION POLICY (OLDER_THAN(EventTime, INTERVAL 365 DAY))
```

Spanner then removes each record once it is older than the period, in the background
(its documentation says typically within 72 hours of that moment). Nothing else removes
a change record, so an application that declares forever keeps every record it has ever
written, and the table grows for as long as the application runs. A record names the
person who made the change for as long as it is kept: deleting an account rewrites no
record. An application made by `impulse new` starts at 365 days. To change the period
later, add a migration that replaces the table's row deletion policy with the new number
of days.
