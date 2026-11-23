import { rowCapabilities } from '@impulseframework/resource';
import { addDays, isValid, startOfDay } from 'date-fns';
import {
  MethodMeta,
  MethodPresentation,
  nameInWords,
  RecordData,
  Resource,
  RPCFieldMeta,
} from '@impulseframework/resource-angular/types';

// A name in words lives with the types, where a page that draws no list reads it too; it
// is exported from here as it always was.
export { nameInWords };

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
  /**
   * What the row says in the method's place, where the page's predicate keeps the method
   * off the row and says why (MethodPresentation.otherwise): the entry is those words and
   * runs nothing.
   */
  says?: string;
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
 * The methods that name no target row and are offered on the rows of the resource
 * (`offeredOn` naming it), in the order given: a list of the resource asks the server about
 * them with each row, and a page that fills one from a row offers it where the row's answer
 * lists it.
 */
export function methodsOfferedOn(
  methods: Iterable<string>,
  metaOf: (method: string) => MethodMeta | undefined,
  resource: Resource,
): string[] {
  const offeredOn: string[] = [];
  for (const method of methods) {
    if (metaOf(method)?.offeredOn?.includes(resource)) {
      offeredOn.push(method);
    }
  }
  return offeredOn;
}

/** The method as the page offers it: the page's words and icon where its configuration names the method, the name in words otherwise. */
export function offered(
  method: string,
  meta: MethodMeta,
  presentations: readonly MethodPresentation[] | undefined,
): OfferedMethod {
  return offeredAs(
    method,
    meta,
    presentations?.find((p) => p.method === method),
  );
}

/**
 * The method as one presentation offers it. A page may name a method more than once,
 * each time with its own words, predicate and fill (a flag turned on, and the same
 * method turning it off), so the presentation offered is the one given, not the first
 * that names the method.
 */
export function offeredAs(
  method: string,
  meta: MethodMeta,
  presentation: MethodPresentation | undefined,
): OfferedMethod {
  return {
    method,
    meta,
    label: presentation?.label ?? nameInWords(method),
    icon: presentation?.icon,
    presentation,
  };
}

/** Whether a presentation offers its method on the row: the page has not hidden it and its row predicate does not say no. */
function presents(presentation: MethodPresentation, row: RecordData): boolean {
  return !presentation.hidden && presentation.when?.(row) !== false;
}

/**
 * What a presentation says on a row its predicate keeps its method off: the words the
 * page gives in the method's place (`otherwise`). Nothing where the page hides the
 * method, where the predicate does not say no, and where the page gives no words.
 */
function saidInstead(presentation: MethodPresentation, row: RecordData): string | undefined {
  if (presentation.hidden || presentation.when?.(row) !== false) {
    return undefined;
  }
  return presentation.otherwise?.(row) || undefined;
}

/**
 * The methods one row offers: of the methods that target the listed resource, those the
 * row's capability answer lists, less the ones the page hides and the ones whose row
 * predicate says no. A method the page names more than once is offered once for each
 * presentation that holds for the row. A presentation whose predicate keeps the method
 * off the row is an entry all the same where the page says what the row shows in its
 * place (`otherwise`): the words, marked as said (`says`), which run nothing. A row read
 * without its capability answer offers none and says nothing: nothing is known about what
 * the caller may run on it.
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
    const named = (presentations ?? []).filter((presentation) => presentation.method === method);
    if (named.length === 0) {
      methods.push(offeredAs(method, metaOf(method), undefined));
      continue;
    }
    for (const presentation of named) {
      const entry = offeredAs(method, metaOf(method), presentation);
      if (presents(presentation, row)) {
        methods.push(entry);
        continue;
      }
      const says = saidInstead(presentation, row);
      if (says) {
        methods.push({ ...entry, says });
      }
    }
  }
  return methods;
}

/**
 * The methods a row offers that name no target row and are filled from it: the ones the
 * page names with a `fill`, each while the caller holds Execute on the method (the
 * digest's answer), less the ones the page hides and the ones whose row predicate says
 * no. On a row of a resource the method's metadata lists (`offeredOn`), the server answers
 * for the method: it is offered only where the row's Execute answer lists it, and on no
 * such row read without that answer. On a row of any other resource, and for a method that
 * lists none, the server gives no answer for the row and the grant alone decides. Each
 * presentation is offered as itself, so a method named twice is offered under each one's
 * own words and filled as each one fills it. As for a method that names its row
 * (rowMethods), a presentation whose predicate keeps the method off the row is an entry of
 * words where the page says what the row shows in its place
 * (`otherwise`).
 */
export function filledMethods(
  row: RecordData,
  resource: Resource | undefined,
  presentations: readonly MethodPresentation[] | undefined,
  metaOf: (method: string) => MethodMeta | undefined,
  granted: (method: string) => boolean,
): OfferedMethod[] {
  const methods: OfferedMethod[] = [];
  for (const presentation of presentations ?? []) {
    const meta = metaOf(presentation.method);
    if (!presentation.fill || !meta || meta.target || !granted(presentation.method)) {
      continue;
    }
    // The server answers for the method on the rows of this resource: what a row's answer
    // leaves out is not offered, and nothing is said in its place.
    if (
      resource !== undefined &&
      meta.offeredOn?.includes(resource) &&
      !rowCapabilities(row)?.Execute?.includes(presentation.method)
    ) {
      continue;
    }
    const entry = offeredAs(presentation.method, meta, presentation);
    if (presents(presentation, row)) {
      methods.push(entry);
      continue;
    }
    const says = saidInstead(presentation, row);
    if (says) {
      methods.push({ ...entry, says });
    }
  }
  return methods;
}

/**
 * A row's menu in the order the page names its methods: the entries whose presentation is
 * one of the page's, in the order of the page's presentations, whichever way each is
 * offered (by the row's own answer, rowMethods, or filled from the row, filledMethods),
 * and after them the entries of methods the page does not name, in the order given. An
 * entry goes by its own presentation, so a method the page names twice keeps each entry
 * at its own presentation's place. Nothing is dropped and nothing added; with no
 * presentations the entries are answered as given.
 */
export function inNamedOrder(
  offered: readonly OfferedMethod[],
  presentations: readonly MethodPresentation[] | undefined,
): OfferedMethod[] {
  if (!presentations?.length) {
    return [...offered];
  }
  // An entry of a method the page does not name goes after every named one.
  const placeOf = (entry: OfferedMethod): number => {
    const place = entry.presentation ? presentations.indexOf(entry.presentation) : -1;
    return place === -1 ? presentations.length : place;
  };
  return offered
    .map((entry, given) => ({ entry, given, place: placeOf(entry) }))
    .sort((a, b) => a.place - b.place || a.given - b.given)
    .map(({ entry }) => entry);
}

/**
 * Whether the row a list sits under offers a method the page names above the list: a
 * method that names a row (`@target`) runs there against that row, so the row's
 * capability answer lists it and the page's row predicate does not say no. The
 * predicate is asked of that row and told `listed`, the rows the list shows. A list
 * under no row, and a row read without its capability answer, offer none.
 */
export function parentOffers(
  parent: RecordData | undefined,
  presentation: MethodPresentation,
  listed: readonly RecordData[] = [],
): boolean {
  if (!parent || !rowCapabilities(parent)?.Execute?.includes(presentation.method)) {
    return false;
  }
  return presentation.when?.(parent, listed) !== false;
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
 * method's target. The empty text is a value where the method says the part may be
 * empty (`target.mayBeEmpty`), as the group of a role held directly is, and is sent as
 * it is; in every other part of the key it names no row, and the row is refused saying
 * the field is empty.
 */
export function targetBody(method: string, meta: MethodMeta, row: RecordData): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  const mayBeEmpty = meta.target?.mayBeEmpty ?? [];
  for (const [requestField, rowField] of Object.entries(meta.target?.key ?? {})) {
    const value = row[rowField];
    if (value === undefined || value === null) {
      throw new Error(`${method} names its target row by ${rowField}, which this row does not carry`);
    }
    if (value === '' && !mayBeEmpty.includes(requestField)) {
      throw new Error(
        `${method} names its target row by ${rowField}, which is empty on this row: only a text field of the key that the method does not declare required may be empty`,
      );
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
 * judges a required one; false and 0 are values and are sent. A day picked for a field
 * the page names in `throughDay` is sent as the end of that day (afterPickedDay).
 */
export function requestBody(
  preset: Record<string, unknown>,
  asked: readonly RPCFieldMeta[],
  values: Record<string, unknown>,
  throughDay: readonly string[] = [],
): Record<string, unknown> {
  const body: Record<string, unknown> = { ...preset };
  for (const field of asked) {
    const value = values[field.fieldName];
    if (holdsValue(value)) {
      body[field.fieldName] = throughDay.includes(field.fieldName) ? afterPickedDay(value) : value;
    }
  }
  return body;
}

/**
 * The instant a day picked as the last day of a period is sent as: the first instant of
 * the day after it, in the viewer's time zone. A period's end is the first instant it no
 * longer counts, so the day picked counts whole. The day after is the calendar's, so a
 * day a clock change makes shorter or longer ends where the next one begins. A value
 * that is no date is sent as it is.
 */
export function afterPickedDay(value: unknown): unknown {
  return value instanceof Date && isValid(value) ? addDays(startOfDay(value), 1) : value;
}
