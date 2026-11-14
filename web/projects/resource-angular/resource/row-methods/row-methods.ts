import { rowCapabilities } from '@impulseframework/resource';
import {
  MethodMeta,
  MethodPresentation,
  RecordData,
  Resource,
  RPCFieldMeta,
} from '@impulseframework/resource-angular/types';

// A generated method that declares a target row (@target) runs against one row of a
// resource. The generated method metadata names the resource and, for each request
// field that carries part of the row's key, the row's field it carries; the server's
// answer on a row (its Execute capabilities) names the methods the caller may run on
// that row. These functions put the two together for a page: which methods a resource's
// rows can offer, which one row does offer, what a method's request takes from the row,
// and what is left for a person to fill.

/** One method as a page offers it: its name, its metadata, and the words and icon it is shown with. */
export interface OfferedMethod {
  method: string;
  meta: MethodMeta;
  label: string;
  icon?: string;
  /** The page's own presentation of the method, when its configuration names it. */
  presentation?: MethodPresentation;
}

/**
 * The methods whose target row is a row of the resource, in the order given: the
 * methods a list of the resource asks the server about and may offer on a row.
 */
export function methodsTargeting(
  methods: Iterable<string>,
  metaOf: (method: string) => MethodMeta | undefined,
  resource: Resource,
): string[] {
  const targeting: string[] = [];
  for (const method of methods) {
    if (metaOf(method)?.target?.resource === resource) {
      targeting.push(method);
    }
  }
  return targeting;
}

/**
 * A name in words: `DisableCrewSectorUser` is "Disable crew sector user", `userId` is
 * "User id". What a method or a request field is called on a page that gives it no words
 * of its own.
 */
export function nameInWords(name: string): string {
  const words = name
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
    .trim()
    .toLowerCase();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/** The method as the page offers it: the page's words and icon where its configuration names the method, the name in words otherwise. */
export function offered(
  method: string,
  meta: MethodMeta,
  presentations: readonly MethodPresentation[] | undefined,
): OfferedMethod {
  const presentation = presentations?.find((p) => p.method === method);
  return {
    method,
    meta,
    label: presentation?.label ?? nameInWords(method),
    icon: presentation?.icon,
    presentation,
  };
}

/**
 * The methods one row offers: of the methods that target the listed resource, those the
 * row's capability answer lists, less the ones the page hides and the ones whose row
 * predicate says no. A row read without its capability answer offers none: nothing is
 * known about what the caller may run on it.
 */
export function rowMethods(
  row: RecordData,
  targeting: readonly string[],
  metaOf: (method: string) => MethodMeta,
  presentations: readonly MethodPresentation[] | undefined,
): OfferedMethod[] {
  const admitted = rowCapabilities(row)?.Execute;
  if (!admitted) {
    return [];
  }
  const methods: OfferedMethod[] = [];
  for (const method of targeting) {
    if (!admitted.includes(method)) {
      continue;
    }
    const entry = offered(method, metaOf(method), presentations);
    if (entry.presentation?.hidden || entry.presentation?.when?.(row) === false) {
      continue;
    }
    methods.push(entry);
  }
  return methods;
}

/**
 * The methods a row offers that name no target row and are filled from it: the ones the
 * page names with a `fill`, each while the caller holds Execute on the method (the
 * digest's answer; the server gives none for a row the method does not name), less the
 * ones the page hides and the ones whose row predicate says no.
 */
export function filledMethods(
  row: RecordData,
  presentations: readonly MethodPresentation[] | undefined,
  metaOf: (method: string) => MethodMeta | undefined,
  granted: (method: string) => boolean,
): OfferedMethod[] {
  const methods: OfferedMethod[] = [];
  for (const presentation of presentations ?? []) {
    const meta = metaOf(presentation.method);
    if (!presentation.fill || !meta || meta.target || presentation.hidden || presentation.when?.(row) === false) {
      continue;
    }
    if (granted(presentation.method)) {
      methods.push(offered(presentation.method, meta, presentations));
    }
  }
  return methods;
}

/**
 * What the page fills in a method's request from a row (the presentation's `fill`),
 * less what it left undefined; nothing where the page fills nothing.
 */
export function filledBody(method: OfferedMethod, row: RecordData | undefined): Record<string, unknown> {
  const filled = method.presentation?.fill?.(row ?? {}) ?? {};
  return Object.fromEntries(Object.entries(filled).filter(([, value]) => value !== undefined));
}

/**
 * What a method's request takes from the row it runs against: each request field that
 * carries part of the row's key, filled from the row's field it carries. A row that
 * lacks one of them cannot be named, and is refused naming the field: the list requests
 * every key field of its rows, so this is a page built on a row that is not the
 * method's target.
 */
export function targetBody(method: string, meta: MethodMeta, row: RecordData): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  for (const [requestField, rowField] of Object.entries(meta.target?.key ?? {})) {
    const value = row[rowField];
    if (value === undefined || value === null || value === '') {
      throw new Error(`${method} names its target row by ${rowField}, which this row does not carry`);
    }
    body[requestField] = value;
  }
  return body;
}

/**
 * The request fields a person fills: every field the target row does not carry and the
 * page has not filled (`filled`, the names of the fields a dialog was opened with).
 */
export function askedFields(meta: MethodMeta, filled: Iterable<string> = []): RPCFieldMeta[] {
  const carried = new Set([...Object.keys(meta.target?.key ?? {}), ...filled]);
  return (meta.fields ?? []).filter((field) => !carried.has(field.fieldName));
}

/** Whether a form's value is one to send: anything but nothing, the empty text and an empty list. */
export function holdsValue(value: unknown): boolean {
  if (Array.isArray(value)) {
    return value.length > 0;
  }
  return value !== undefined && value !== null && value !== '';
}

/**
 * The request a dialog sends: what the row carries, then each field the person filled.
 * An input left empty sends nothing, so an optional field is absent and the server
 * judges a required one; false and 0 are values and are sent.
 */
export function requestBody(
  preset: Record<string, unknown>,
  asked: readonly RPCFieldMeta[],
  values: Record<string, unknown>,
): Record<string, unknown> {
  const body: Record<string, unknown> = { ...preset };
  for (const field of asked) {
    const value = values[field.fieldName];
    if (holdsValue(value)) {
      body[field.fieldName] = value;
    }
  }
  return body;
}
