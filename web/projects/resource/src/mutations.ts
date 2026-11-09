import { CapabilitiesProperty } from './digest';
import { ResourceDescriptor } from './descriptor';
import { Operation } from './operations';

/** What the mutation helpers need from a handle: the descriptor names the patchable fields. */
export interface DescribedHandle {
  readonly descriptor: ResourceDescriptor;
}

/** The handle's Patch type, read off its batch operation builder. */
export type PatchOf<H> = H extends { readonly ops: { patch(key: never, value: infer P): Operation } }
  ? P
  : Record<string, unknown>;

/**
 * The patch that turns `before` into `after`: the fields whose values differ, or
 * undefined when nothing does (send no request for an empty diff).
 *
 * Comparison is by content, not identity: a Date on either side compares as an
 * instant (forms hold Date objects where rows hold ISO strings), plain objects and
 * arrays compare structurally, and null is a value — the clear — never a skip. A
 * field the after image does not carry (or carries as undefined) is not part of the
 * form and is never compared.
 *
 * A field the before image does not carry was never read: the server leaves a cell
 * that is masked for this reader out of the row, with no marker, and never returns a
 * write-only field. The control over such a field starts blank, so a blank control
 * (null or an empty string) is nothing typed and not a change; read as a clear, it
 * would empty the cell or fail the save. A value typed into it is sent, and the
 * server's own check decides whether this reader may update the field. A cell that is
 * NULL arrives as an explicit null, so clearing a value the row carries still
 * registers.
 *
 * A differing field outside the descriptor's patchable list throws, naming the
 * resource and field — key, server-owned, and immutable fields alike. Dropping it
 * silently would save something other than what the user sees; the error is the
 * honest answer.
 */
export function changes<H extends DescribedHandle>(handle: H, before: object, after: object): PatchOf<H> | undefined {
  const { resource, patchable } = handle.descriptor;
  const beforeMap = before as Record<string, unknown>;
  const afterMap = after as Record<string, unknown>;

  const patch: Record<string, unknown> = {};
  for (const [field, proposed] of Object.entries(afterMap)) {
    if (field === CapabilitiesProperty || proposed === undefined) {
      continue;
    }
    if (beforeMap[field] === undefined && isBlank(proposed)) {
      continue;
    }
    if (equalContent(beforeMap[field], proposed)) {
      continue;
    }
    if (!patchable?.includes(field)) {
      throw new Error(
        `${resource}.${field} changed but is not patchable — key, server-owned, and immutable fields cannot be saved from a form`,
      );
    }
    patch[field] = proposed;
  }

  return Object.keys(patch).length > 0 ? (patch as PatchOf<H>) : undefined;
}

/** A blank control: nothing typed, in either value a form gives it. */
function isBlank(value: unknown): boolean {
  return value === null || value === '';
}

/** Content equality: Dates by instant, plain objects and arrays structurally. */
function equalContent(a: unknown, b: unknown): boolean {
  if (Object.is(a, b)) {
    return true;
  }
  if (a instanceof Date || b instanceof Date) {
    const left = instantOf(a);
    const right = instantOf(b);
    return left !== undefined && left === right;
  }
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((element, i) => equalContent(element, b[i]));
  }
  if (isPlainObject(a) && isPlainObject(b)) {
    const keys = new Set([...Object.keys(a), ...Object.keys(b)]);
    for (const key of keys) {
      if (!equalContent(a[key], b[key])) {
        return false;
      }
    }
    return true;
  }
  return false;
}

/** The comparable instant of a Date (or of a value a Date is compared against). */
function instantOf(value: unknown): number | undefined {
  if (value instanceof Date) {
    const time = value.getTime();
    return Number.isNaN(time) ? undefined : time;
  }
  if (typeof value === 'string' || typeof value === 'number') {
    const time = new Date(value).getTime();
    return Number.isNaN(time) ? undefined : time;
  }
  return undefined;
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  if (typeof value !== 'object' || value === null) {
    return false;
  }
  const proto: unknown = Object.getPrototypeOf(value);
  return proto === Object.prototype || proto === null;
}
