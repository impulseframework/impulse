import { FieldName, Resource } from './permissions';
import { FieldMeta, ResourceMeta } from './resource-meta';

/**
 * The resource a list page's writes go to, and whose form the page builds: the
 * metadata's `rowsOf` when the listed resource is a view declaring its backing table,
 * else the listed resource itself. The view declares its backing table, a create goes
 * into the table, and the new row shows up in the view on the next list because the
 * view's SQL reads that table.
 */
export function writeResource(primary: Resource, meta: ResourceMeta | undefined): Resource {
  return (meta?.rowsOf ?? primary) as Resource;
}

/** A resource's primary-key fields in key order, read from its metadata. */
export function keyFields(meta: ResourceMeta | undefined): FieldMeta[] {
  return (meta?.fields ?? [])
    .filter((field) => field.primaryKey !== undefined)
    .sort((a, b) => (a.primaryKey?.ordinalPosition ?? 0) - (b.primaryKey?.ordinalPosition ?? 0));
}

/**
 * The columns a list requests: the configured columns, once each, then every key
 * field not among them, so an operation on a row can lift the key the row carries.
 */
export function requestedColumns(configured: readonly FieldName[], meta: ResourceMeta | undefined): FieldName[] {
  const columns = [...new Set(configured)];
  for (const key of keyFields(meta)) {
    if (!columns.includes(key.fieldName as FieldName)) {
      columns.push(key.fieldName as FieldName);
    }
  }
  return columns;
}

/**
 * Where a list's rows open: the resource whose page a row lands on, the field of the
 * row holding that resource's key, and the page's route.
 */
export interface RowTarget {
  resource: Resource;
  keyField: FieldName;
  route: string;
}

/**
 * Resolves where a list's rows open. With `rowRoute`, the named field of the listed
 * resource carries an `enumeratedResource` in the metadata: that resource is the
 * target and the row's value in the field its key. Without it, the write resource by
 * its single primary key, whose field the view carries under the same name; a
 * compound key with no rowRoute opens nowhere. The route is the target's page where
 * one is registered (`pageRoute`), else its metadata route. A rowRoute naming a field
 * with no target in the metadata is a configuration error and throws, naming the field.
 */
export function rowRouteTarget(
  primary: Resource,
  rowRoute: FieldName | undefined,
  resourceMeta: (resource: Resource) => ResourceMeta | undefined,
  pageRoute: (resource: Resource) => string | undefined = (): undefined => undefined,
): RowTarget | undefined {
  const meta = resourceMeta(primary);
  const routeFor = (resource: Resource): string => pageRoute(resource) ?? resourceMeta(resource)?.route ?? '';

  if (rowRoute) {
    const field = meta?.fields.find((candidate) => candidate.fieldName === rowRoute);
    if (!field) {
      throw new Error(`${primary}: rowRoute names ${rowRoute}, which is not a field of the resource`);
    }
    if (!field.enumeratedResource) {
      throw new Error(
        `${primary}: rowRoute names ${rowRoute}, which names no resource in the metadata; declare the target on the Go struct with the field-scope @enumerate`,
      );
    }
    const resource = field.enumeratedResource as Resource;
    return { resource, keyField: rowRoute, route: routeFor(resource) };
  }

  const keys = keyFields(meta);
  if (keys.length !== 1) {
    return undefined;
  }
  const resource = writeResource(primary, meta);
  return { resource, keyField: keys[0].fieldName as FieldName, route: routeFor(resource) };
}
