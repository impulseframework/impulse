/**
 * The state of one grant in the permission digest: `granted` when an unconditional
 * grant covers the target, `conditional` when only condition-limited grants do —
 * render the surface and expect the server to narrow it. Denied is never a value:
 * a target the user cannot reach is absent from the digest, so consumers fail closed.
 */
export type PermissionDigestState = 'granted' | 'conditional';

/**
 * The session user's structural grant enumeration for one scope, served by the
 * generated `GET <api>/permission-digest` endpoint (`?domain=` names a tenant
 * partition; absent means global). Keys are resource names and dotted
 * `Resource.field` names; RPC methods appear under their method name with the
 * `Execute` permission. Advisory UI material only: which routes, menus, and form
 * inputs to render. Enforcement stays server-side.
 */
export type PermissionDigest = Record<string, Record<string, PermissionDigestState | undefined> | undefined>;

/**
 * One row's capability envelope, attached under the reserved `zzCapabilities`
 * property when a list or read opted in with `?capabilities=Create,Update,Delete,Execute`.
 * `Update` is the positive list of editable JSON field names (a field absent from it
 * is not editable on this row); `Delete` is whether the row may be deleted; `Execute`
 * is the positive list of RPC methods whose declared transition applies to this row —
 * the method targets this resource, the row's state is in its `from` set, and the
 * session user holds the method's Execute grant. `Create` is the positive list of
 * workflow member resources the session user may create beneath this row (the
 * create-under-parent affordance: the member's immediate parent hop is this resource,
 * and the member Create grant's state condition is evaluated against this row).
 * Advisory hints evaluated against the row the server returned; enforcement stays
 * server-side.
 */
export interface RowCapabilities {
  Create?: string[];
  Update?: string[];
  Delete?: boolean;
  Execute?: string[];
}

/** The reserved per-row property the capability envelope rides under. */
export const CapabilitiesProperty = 'zzCapabilities';

/** A row that was read with the capability envelope requested. */
export type WithCapabilities<Row> = Row & { readonly [CapabilitiesProperty]?: RowCapabilities };

/**
 * Reads a row's capability envelope, if the read opted in. Rows arrive as generic
 * records, so the reserved property is read structurally; undefined means the read
 * carried no envelope and nothing about the row's editability is known.
 */
export function rowCapabilities(row: object | null | undefined): RowCapabilities | undefined {
  const capabilities = (row as Record<string, unknown> | null | undefined)?.[CapabilitiesProperty];
  return capabilities && typeof capabilities === 'object' ? (capabilities as RowCapabilities) : undefined;
}

/** The reserved query parameter that opts a list or read into the capability envelope. */
export const CapabilitiesQueryParam = 'capabilities';

/** The capabilities a list or read may ask the server to evaluate per row. */
export type Capability = 'Create' | 'Update' | 'Delete' | 'Execute';
