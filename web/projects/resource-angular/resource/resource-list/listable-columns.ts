import { ColumnConfig } from '@impulseframework/resource-angular/types';

/**
 * The configured columns a caller may list, judged by the digest's field-level List
 * entries. Every column when the digest carries no field information (undefined:
 * nothing narrows); otherwise the columns whose fields the digest grants. Key fields
 * are structural, never grant-bearing, and always pass. A concatenated column passes
 * when its own field and every field it reads off this resource pass; a field it reads
 * off a referenced resource travels in that resource's own request and is not judged
 * here.
 */
export function listableColumns(
  configured: readonly ColumnConfig[],
  listable: ReadonlySet<string> | undefined,
  keys: ReadonlySet<string>,
): ColumnConfig[] {
  if (!listable) {
    return [...configured];
  }
  const granted = (id: string): boolean => keys.has(id) || listable.has(id);
  return configured.filter((col) => {
    if (!granted(col.id)) {
      return false;
    }
    if (!('additionalIds' in col)) {
      return true;
    }
    return col.additionalIds.every((extra) => extra.resource !== undefined || granted(extra.id));
  });
}

/**
 * Whether two column lists are the same columns in the same order, each by identity (the
 * page's own configured column objects), so a digest re-evaluated to the same answer (a
 * scope's digest landing, a reload) leaves the columns, and with them the list's request,
 * as they are; a digest that grants a different set changes them.
 */
export function sameColumns(a: readonly ColumnConfig[], b: readonly ColumnConfig[]): boolean {
  return a.length === b.length && a.every((column, i) => column === b[i]);
}
