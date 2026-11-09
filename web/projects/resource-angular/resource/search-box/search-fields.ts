import { ListPermission, Resource, ResourceMeta, SearchBranchMeta } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { filterFields } from '@impulseframework/resource-angular/grid';
import { CamelCaseToTitlePipe } from '@impulseframework/resource-angular/camel-case-to-title';

// What a search box over a resource searches for this caller, how it says so, and when
// its request must travel in the body. The set is the resource's (its metadata's
// searchFields) narrowed by the caller's grants, never the page's: a picker's box names
// the same fields as the list page of the same resource, and a page that should search
// fewer fields lists a view projecting fewer.

const title = new CamelCaseToTitlePipe();

/**
 * The digest's List field set for a resource: the fields the caller may list, or
 * undefined when the digest carries no field information for it, which narrows nothing.
 */
export type ListableFields = (resource: Resource) => ReadonlySet<string> | undefined;

const nothingKnown: ListableFields = () => undefined;

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
 * Whether the caller searches a field of the box: a field of the resource when the
 * digest lists it for List, a branch when the digest lists the child field on the child
 * resource. A dotted field the metadata does not describe (metadata from before the
 * branches were described) stays.
 */
function searched(
  meta: ResourceMeta | undefined,
  field: string,
  listable: ReadonlySet<string> | undefined,
  listableOf: ListableFields,
): boolean {
  const branch = branchOf(meta, field);
  if (branch) {
    const fields = listableOf(branch.resource);
    return fields === undefined || fields.has(branch.fieldName);
  }
  if (field.includes('.')) {
    return true;
  }
  return listable === undefined || listable.has(field);
}

/**
 * The search fields the box reads for this caller: the metadata's searchFields less the
 * fields the digest denies for List, a branch judged by the child field's grant on the
 * child resource. `listable` is the digest's field set for the resource itself and
 * `listableOf` answers it for a child; undefined narrows nothing.
 */
export function searchedFields(
  meta: ResourceMeta | undefined,
  listable: ReadonlySet<string> | undefined,
  listableOf: ListableFields = nothingKnown,
): string[] {
  return (meta?.searchFields ?? []).filter((field) => searched(meta, field, listable, listableOf));
}

/** The resource's search fields the digest denies this caller, so the box can say what it does not search. */
export function deniedSearchFields(
  meta: ResourceMeta | undefined,
  listable: ReadonlySet<string> | undefined,
  listableOf: ListableFields = nothingKnown,
): string[] {
  return (meta?.searchFields ?? []).filter((field) => !searched(meta, field, listable, listableOf));
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
