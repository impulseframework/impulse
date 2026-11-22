import { ListPermission, Resource, ResourceMeta, SearchBranchMeta } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { filterFields } from '@impulseframework/resource-angular/grid';
import { CamelCaseToTitlePipe } from '@impulseframework/resource-angular/camel-case-to-title';

// What a search box over a resource searches for this caller, how it says so, and when
// its request must travel in the body. The set is the resource's (its metadata's
// searchFields) narrowed by the caller's grants, never the page's: a picker's box names
// the same fields as the list page of the same resource, and a page that should search
// fewer fields lists a view projecting fewer. A field of the resource's key takes no
// grant of its own, so no role's digest names it: whoever lists the resource searches
// it, as the server does, and the box never says a role leaves it out. A computed
// resource is searched over its real values, so the server refuses the whole search when
// the caller does not list one of the searched fields; the box is then not offered, and
// the page says which fields the caller's role does not see. A field the digest lists
// under a condition does not withhold the box: the digest folds nothing, and the server
// answers the search where the condition is one the check settles from the request and
// it holds.

const title = new CamelCaseToTitlePipe();

/**
 * The digest's List field set for a resource: the fields the caller may list, or
 * undefined when the digest carries no field information for it, which narrows nothing.
 */
export type ListableFields = (resource: Resource) => ReadonlySet<string> | undefined;

const nothingKnown: ListableFields = () => undefined;

/**
 * A resource's metadata by name (the application's RESOURCE_META), which says whether a
 * search branch's child field is part of the child's key.
 */
export type MetaOf = (resource: Resource) => ResourceMeta | undefined;

const noMetadata: MetaOf = () => undefined;

/**
 * The digest's List field set for any resource, read through the auth service: the
 * fields a loaded digest lists; the empty set when it holds no List on the resource at
 * all, so every search field over it drops; undefined when nothing is known, because the
 * digest has not loaded or grants List with no field entries (a keys-only resource),
 * which narrows nothing. Signal-backed through the service, so a computed calling it
 * re-evaluates when a digest lands.
 */
export function listableFieldsOf(auth: AuthService): ListableFields {
  return (resource) => {
    const scope = { resource, permission: ListPermission };
    const fields = Object.keys(auth.fieldPermissionStates(scope));
    if (fields.length > 0) {
      return new Set(fields);
    }
    return auth.hasDigestFor(scope) && !auth.hasPermission(scope) ? new Set<string>() : undefined;
  };
}

/** The branch a dotted search field names, when the metadata describes it. */
function branchOf(meta: ResourceMeta | undefined, field: string): SearchBranchMeta | undefined {
  return meta?.searchBranches?.find((branch) => branch.field === field);
}

/**
 * Whether a field is part of its resource's key. A key field takes no grant of its own:
 * the digest never names it, and whoever lists the resource reads it.
 */
function keyField(meta: ResourceMeta | undefined, field: string): boolean {
  return (
    meta?.fields?.some((candidate) => candidate.fieldName === field && candidate.primaryKey !== undefined) ?? false
  );
}

/**
 * Whether a field is listed for the caller, by the digest's field set for its resource:
 * a key field wherever the digest holds List on the resource at all (the empty set is a
 * digest that holds none), any other field where the set names it. Undefined, nothing
 * is known and nothing narrows.
 */
function listed(fields: ReadonlySet<string> | undefined, field: string, key: boolean): boolean {
  if (fields === undefined) {
    return true;
  }
  return key ? fields.size > 0 : fields.has(field);
}

/**
 * Whether the caller searches a field of the box: a field of the resource when the
 * digest lists it for List, a branch when the digest lists the child field on the child
 * resource, and a key field of either wherever the caller lists that resource. A dotted
 * field the metadata does not describe (metadata from before the branches were
 * described) stays.
 */
function searched(
  meta: ResourceMeta | undefined,
  field: string,
  listable: ReadonlySet<string> | undefined,
  listableOf: ListableFields,
  metaOf: MetaOf,
): boolean {
  const branch = branchOf(meta, field);
  if (branch) {
    return listed(listableOf(branch.resource), branch.fieldName, keyField(metaOf(branch.resource), branch.fieldName));
  }
  if (field.includes('.')) {
    return true;
  }
  return listed(listable, field, keyField(meta, field));
}

/**
 * The search fields the box reads for this caller: the metadata's searchFields less the
 * fields the digest denies for List, a branch judged by the child field's grant on the
 * child resource, a key field by List on its resource alone. `listable` is the digest's
 * field set for the resource itself and `listableOf` answers it for a child; undefined
 * narrows nothing. `metaOf` answers a child's metadata, which says whether a branch's
 * field is the child's key; without it a branch is judged as any other field.
 */
export function searchedFields(
  meta: ResourceMeta | undefined,
  listable: ReadonlySet<string> | undefined,
  listableOf: ListableFields = nothingKnown,
  metaOf: MetaOf = noMetadata,
): string[] {
  return (meta?.searchFields ?? []).filter((field) => searched(meta, field, listable, listableOf, metaOf));
}

/** The resource's search fields the digest denies this caller, so the box can say what it does not search. */
export function deniedSearchFields(
  meta: ResourceMeta | undefined,
  listable: ReadonlySet<string> | undefined,
  listableOf: ListableFields = nothingKnown,
  metaOf: MetaOf = noMetadata,
): string[] {
  return (meta?.searchFields ?? []).filter((field) => !searched(meta, field, listable, listableOf, metaOf));
}

/**
 * The search fields for which the server is sure to refuse a computed list's whole
 * search: each of the resource's own search fields, other than a key field, that the
 * digest does not list. None on a table or a view; none where the digest holds no List on
 * the resource at all, since the page lists nothing; none where nothing is known. A field
 * listed under a condition is not among them: the server may settle the condition from
 * the request and answer.
 */
export function refusedSearchFields(
  meta: ResourceMeta | undefined,
  listable: ReadonlySet<string> | undefined,
): string[] {
  if (meta?.computed !== true || listable === undefined || listable.size === 0) {
    return [];
  }
  return (meta.searchFields ?? []).filter(
    (field) => !field.includes('.') && !keyField(meta, field) && !listable.has(field),
  );
}

/**
 * Whether a box over these fields reaches a field the metadata marks PII, its own or a
 * branch's: the request then travels in the body, as the server requires.
 */
export function searchReachesPII(meta: ResourceMeta | undefined, fields: readonly string[]): boolean {
  return fields.some((field) => {
    const branch = branchOf(meta, field);
    if (branch) {
      return branch.pii === true;
    }
    return meta?.fields.some((candidate) => candidate.fieldName === field && candidate.pii === true) ?? false;
  });
}

/** Whether a filter in the server grammar names a field the metadata marks PII: the request then travels in the body. */
export function filterReachesPII(filter: string, meta: ResourceMeta | undefined): boolean {
  if (filter.trim() === '') {
    return false;
  }
  const pii = new Set((meta?.fields ?? []).filter((field) => field.pii).map((field) => field.fieldName));
  return pii.size > 0 && filterFields(filter).some((field) => pii.has(field));
}

/**
 * A search field's label: the header the page configured for its column, else its name
 * in title case; a child's dotted field under the child's name and its own.
 */
export function searchFieldLabel(field: string, headers: ReadonlyMap<string, string> = new Map()): string {
  const header = headers.get(field);
  if (header) {
    return header;
  }
  return field
    .split('.')
    .map((part) => title.transform(part))
    .join(' ');
}

/** What the box's placeholder says: the fields it searches, by label; "Search" alone when it can name none. */
export function searchPlaceholder(labels: readonly string[]): string {
  return labels.length === 0 ? 'Search' : `Search ${labels.join(', ')}`;
}

/** What the box says under itself about the fields the caller's role leaves out; nothing when none. */
export function searchDeniedHint(labels: readonly string[]): string {
  return labels.length === 0 ? '' : `Not searched for your role: ${labels.join(', ')}`;
}

/**
 * What the page says where it offers no box because the server would refuse the search:
 * the fields the caller's role does not see, by label; nothing when none.
 */
export function searchRefusedNotice(labels: readonly string[]): string {
  if (labels.length === 0) {
    return '';
  }
  const fields = labels.length === 1 ? labels[0] : `${labels.slice(0, -1).join(', ')} or ${labels[labels.length - 1]}`;
  return `You cannot search this list: your role does not see ${fields}.`;
}
