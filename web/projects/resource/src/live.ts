import { Domain } from './brands';
import { LiveRoutes, ResourceDescriptor } from './descriptor';
import { Requester } from './permissions';
import { XsrfOptions, defaultXsrf, readCookie } from './transport';
import { Warn, warnOnce } from './warnings';

/** The request header a live request carries: the tab's id. The server registers the subscription under it. */
export const subscribeHeader = 'X-Subscribe';

/**
 * The reserved query parameter a live request carries: the version the browser's cache
 * keys the answer by. Its value is the change document's `at` (unix microseconds) once a
 * change has arrived for the row or list, and the tab's seed before that. The server's
 * decoders accept and skip it.
 */
export const versionParam = '_v';

/** How often a tab renews its live subscriptions, in milliseconds. */
export const liveRenewInterval = 120_000;

/** How many subscriptions one renewal carries at most; the server refuses a longer body. */
export const liveRenewLimit = 1000;

/** How long the server holds a subscription after it was written or renewed, in milliseconds. */
export const liveSubscriptionTtl = 300_000;

/** How long the server keeps a change document, in milliseconds; a page hidden longer than this resyncs on its return. */
export const liveChangeTtl = 600_000;

/**
 * The response header a write's answer carries when the write changed something the
 * caller holds live: the write's id and the changes it published to the caller's own
 * change set, as JSON, percent-encoded (see WriteChanges). The live session fetches what
 * it names at once and does nothing when the feed reports the same write.
 */
export const changesHeader = 'X-Changes';

/**
 * The response header a live request's answer carries when the server registered, beside
 * the request's own subscription, the lists the names the answer shows are read from (a
 * list of ships that shows each one's squadron by name: the squadrons' list in the
 * tenant): the request's own subscription as the server keeps it and those lists, as
 * JSON, percent-encoded (see LiveNames). The live session keeps the lists under the
 * subscription, so a change to one of them fetches the page again, and renews them with it.
 */
export const namesHeader = 'X-Names';

/**
 * How long the first change by someone else waits before the page fetches, in
 * milliseconds: every change that arrives in the wait becomes part of the one fetch.
 * The library's default; an application sets its own in LiveOptions.
 */
export const liveChangeWait = 2_000;

/**
 * The least time between two fetches a page makes for other people's changes, in
 * milliseconds: while changes keep arriving, the page fetches once per interval. The
 * library's default; an application sets its own in LiveOptions.
 */
export const liveRefetchInterval = 5_000;

/**
 * What the session announces, once per client, when a page asks for live updates of an
 * API whose descriptor carries no live block: the request is served plain, and
 * regenerating the client API is what would serve it live.
 */
export const noLiveRoutesWarning =
  'this page asked for live updates, but the generated API descriptor carries no live block: the request is served plain ' +
  'and nothing is subscribed; regenerate the client API with a resource generator that emits the live routes';

/**
 * The Firestore identity the server mints for the session's principal, answered by the
 * descriptor's token route and handed to the feed's `start`. Against the emulator
 * (`emulator` set) `token` is empty and the browser connects with a mock user token for
 * `uid`; in production `emulator` is empty and `token` is a custom token the browser
 * signs in with.
 */
export interface LiveIdentity {
  uid: string;
  token: string;
  project: string;
  database: string;
  apiKey: string;
  emulator: string;
}

/**
 * One change the feed reports. A `row` event names a row by resource and key (and says
 * whether it was deleted); a `list` event names a resource in a domain (no domain for a
 * global resource); a `resource` event names a resource alone and stands for every row
 * and list of it, sent when a request touched more rows than the server looks up one by
 * one. `at` is the change document's server timestamp as unix microseconds in decimal:
 * the version the refetch carries. `write` is the id of the write that published the
 * change, the last one's when several writes within a second became one document; the
 * tab whose write it was learned the id from the write's answer, and takes the event
 * for its own change. On the event a watcher receives for the tab's own write, which no
 * feed has reported yet, `at` is the write's id, the version that fetch carries.
 */
export interface ChangeEvent {
  kind: 'row' | 'list' | 'resource';
  resource: string;
  key?: string;
  domain?: string;
  deleted?: boolean;
  at: string;
  write?: string;
}

/**
 * Whether `event` is the one a watcher receives for the tab's own write, at the write's
 * answer: its `at` is the write's id. An event the feed reported carries the change
 * document's timestamp there, whoever wrote.
 */
export function ownWrite(event: ChangeEvent): boolean {
  return event.write !== undefined && event.at === event.write;
}

/** A change as a write's answer names it: the row, list, or resource, with no timestamp, since no feed has reported it yet. */
export type AnsweredChange = Omit<ChangeEvent, 'at' | 'write'>;

/**
 * What a write's answer tells the tab that wrote (the changes header): the write's id,
 * which every change the write published carries in the feed, and the changes that went
 * into the caller's own change set. `all` stands for the changes when the server had
 * more than its header holds: the tab then fetches everything it holds live.
 */
export interface WriteChanges {
  write: string;
  changes: AnsweredChange[];
  all: boolean;
}

/**
 * Reads the changes header of a write's answer; undefined when the answer carries none,
 * or one this client cannot read, which is then no different from a write that reached
 * nothing the tab holds live: the feed reports the change and the page fetches for it as
 * for anyone's.
 */
export function parseChangesHeader(value: string | undefined): WriteChanges | undefined {
  if (value === undefined || value === '') {
    return undefined;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(decodeURIComponent(value));
  } catch {
    return undefined;
  }
  if (typeof parsed !== 'object' || parsed === null) {
    return undefined;
  }
  const { write, changes, all } = parsed as { write?: unknown; changes?: unknown; all?: unknown };
  if (typeof write !== 'string' || write === '') {
    return undefined;
  }
  const named: AnsweredChange[] = [];
  for (const entry of Array.isArray(changes) ? (changes as unknown[]) : []) {
    const change = answeredChange(entry);
    if (change) {
      named.push(change);
    }
  }
  return { write, changes: named, all: all === true };
}

/**
 * What a live request's answer tells the tab in the names header. `of` is the request's own
 * subscription as the server keeps it: a list by its resource and domain; a row by its
 * resource and key, with no domain, since the server's record of a row names none; a list
 * across tenants by its resource, `acrossTenants`, and the tenants it was narrowed to.
 * `names` are the lists the server registered beside it, the ones the names the answer
 * shows are read from: each a resource, its domain where the resource belongs to a tenant,
 * and its `nameField` unless the name is the resource's key.
 */
export interface LiveNames {
  of: LiveSubscription;
  names: LiveSubscription[];
}

/**
 * Reads the names header of a live request's answer; undefined when the answer carries
 * none, or one this client cannot read, which is then no different from an answer whose
 * names are read from no list: the page is told of changes to its own row or list alone.
 * An entry of `names` that is not a list is passed over.
 */
export function parseNamesHeader(value: string | undefined): LiveNames | undefined {
  if (value === undefined || value === '') {
    return undefined;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(decodeURIComponent(value));
  } catch {
    return undefined;
  }
  if (typeof parsed !== 'object' || parsed === null) {
    return undefined;
  }
  const { of, names } = parsed as { of?: unknown; names?: unknown };
  const holder = namedSubscription(of);
  if (!holder) {
    return undefined;
  }
  const lists: LiveSubscription[] = [];
  for (const entry of Array.isArray(names) ? (names as unknown[]) : []) {
    const list = namedSubscription(entry);
    if (list && list.key === undefined && !list.acrossTenants) {
      lists.push(list);
    }
  }
  return { of: holder, names: lists };
}

/** One subscription of the names header; undefined for an entry of a shape this client does not know. */
function namedSubscription(entry: unknown): LiveSubscription | undefined {
  if (typeof entry !== 'object' || entry === null) {
    return undefined;
  }
  const { resource, key, domain, acrossTenants, domains, nameField } = entry as Record<string, unknown>;
  if (typeof resource !== 'string' || resource === '') {
    return undefined;
  }
  const subscription: LiveSubscription = { resource };
  if (typeof key === 'string' && key !== '') {
    subscription.key = key;
  }
  if (typeof domain === 'string' && domain !== '') {
    subscription.domain = domain;
  }
  if (acrossTenants === true) {
    subscription.acrossTenants = true;
  }
  if (typeof domains === 'string' && domains !== '') {
    subscription.domains = domains;
  }
  if (typeof nameField === 'string' && nameField !== '') {
    subscription.nameField = nameField;
  }
  return subscription;
}

/** One entry of the changes header as a change; undefined for an entry of a shape this client does not know. */
function answeredChange(entry: unknown): AnsweredChange | undefined {
  if (typeof entry !== 'object' || entry === null) {
    return undefined;
  }
  const { kind, resource, key, domain, deleted } = entry as Record<string, unknown>;
  if ((kind !== 'row' && kind !== 'list' && kind !== 'resource') || typeof resource !== 'string') {
    return undefined;
  }
  const change: AnsweredChange = { kind, resource };
  if (typeof key === 'string') {
    change.key = key;
  }
  if (typeof domain === 'string') {
    change.domain = domain;
  }
  if (typeof deleted === 'boolean') {
    change.deleted = deleted;
  }
  return change;
}

/**
 * The change feed: what delivers a user's change set to the tab. `start` receives the
 * identity the server minted and begins listening; `stop` ends it; `onChange` and
 * `onResync` register listeners and return their release. A feed emits `resync` when it
 * cannot vouch for having seen every change (its listener recovered after an error, or the
 * page became visible again after being hidden longer than the change documents live), and
 * the live session answers by refetching every live row and list with a fresh seed, as its
 * own start does. @impulseframework/resource-firestore is the Firestore implementation; this
 * package depends on no SDK.
 */
export interface ChangeFeed {
  start(identity: LiveIdentity): Promise<void>;
  stop(): Promise<void>;
  onChange(listener: (event: ChangeEvent) => void): () => void;
  onResync(listener: () => void): () => void;
}

/**
 * What a live request subscribes to, and what a tab renews. A list is its resource and
 * domain, with no domain for a global resource. A row is its resource and key, and, for a
 * domain-scoped resource, the domain it was read in: the server re-checks Read in that
 * domain at each renewal (a domain-scoped resource holds no grant in the global scope), while
 * its own record of the row ignores the domain, so two tabs reading one row in two domains
 * share it. A row of a global resource carries no domain. Filter, sort, and cursor are not
 * part of a subscription: any change to the resource in the domain refetches the page.
 *
 * A list across tenants (`acrossTenants`) is one subscription over a set of tenants the
 * server works out, when the list is asked for and again at each renewal: the tenants the
 * session reaches in which the person may list the resource, narrowed to `domains` when
 * the page named tenants. It carries no key and no domain. A list change in any tenant it
 * covers refetches the page, and the server sends none from a tenant outside its set.
 */
export interface LiveSubscription {
  resource: string;
  key?: string;
  domain?: string;
  /** Set on the subscription of a list across tenants. */
  acrossTenants?: true;
  /**
   * The tenants a list across tenants was narrowed to, sorted, as the list's own `domains`
   * parameter spells them: their keys separated by commas. Absent for every tenant.
   */
  domains?: string;
  /**
   * Set on a list the names a page shows are read from, as a names header named it: the
   * pointed-to resource's name field as the grants name it. The server re-checks List on
   * that field at each renewal, not List on the resource, since a person may hold a
   * name's grant without the resource's. Absent where the name is the resource's key, and
   * on every subscription a page watches itself.
   */
  nameField?: string;
}

/** The server's answer to a renewal: the subscriptions it kept and the ones the digest no longer grants, echoed as sent. */
export interface RenewResponse {
  kept: LiveSubscription[];
  dropped: LiveSubscription[];
  expiresAt: string;
}

/**
 * What a watcher does when its row or list changed: fetch it again with `version` as the
 * version parameter. `event` is the change that caused it, the latest one when several
 * became one fetch, absent on a resync.
 */
export type LiveRefetch = (version: string, event?: ChangeEvent) => void;

/** The page events the live session listens to; the window, in a browser. */
export interface PageEvents {
  addEventListener(type: string, listener: (event: { persisted?: boolean }) => void): void;
  removeEventListener(type: string, listener: (event: { persisted?: boolean }) => void): void;
}

/** Whether the tab is shown, as the live session and a feed read it; the document, in a browser. */
export interface VisibilityEvents {
  readonly visibilityState: string;
  addEventListener(type: string, listener: () => void): void;
  removeEventListener(type: string, listener: () => void): void;
}

/**
 * The clock the live session waits by: the time in milliseconds and a timer. The
 * platform's by default (Date.now, setTimeout, clearTimeout); a spec passes its own and
 * moves the time itself.
 */
export interface LiveClock {
  now(): number;
  setTimeout(run: () => void, delay: number): unknown;
  clearTimeout(timer: unknown): void;
}

/** The platform's clock. */
const platformClock: LiveClock = {
  now: () => Date.now(),
  setTimeout: (run, delay) => setTimeout(run, delay),
  clearTimeout: (timer) => clearTimeout(timer as ReturnType<typeof setTimeout>),
};

/**
 * Where the page-leave unsubscribe, which bypasses the transport, gets the XSRF token it
 * echoes: a function answering the token the application's HTTP layer would send on a
 * mutating request (null or undefined for none), as Angular's HttpXsrfTokenExtractor
 * does. The header is the server's unless named.
 */
export interface XsrfTokenSource {
  token(): string | null | undefined;
  headerName?: string;
}

/** The knobs of a live session an application or a spec may turn. Every one has a default. */
export interface LiveOptions {
  /** How often the tab renews its subscriptions; defaults to liveRenewInterval. */
  renewInterval?: number;
  /**
   * How long the first change by someone else waits before the page fetches, in
   * milliseconds, so that every change arriving in the wait becomes one fetch; defaults
   * to liveChangeWait.
   */
  changeWait?: number;
  /**
   * The least time between two fetches a page makes for other people's changes, in
   * milliseconds; defaults to liveRefetchInterval.
   */
  refetchInterval?: number;
  /**
   * The XSRF echo on the page-leave unsubscribe, which bypasses the transport: the cookie
   * read under the server's default names, or under names given; a token source when the
   * application's HTTP layer knows the cookie (the Angular adapter passes one); `false`
   * for none.
   */
  xsrf?: XsrfOptions | XsrfTokenSource | false;
  /** The fetch the page-leave unsubscribe rides with keepalive; the global one by default. */
  fetch?: typeof fetch;
  /** Where pagehide and pageshow are heard; the window by default, nothing outside a browser. */
  page?: PageEvents;
  /**
   * Where the session reads whether the tab is shown and hears it change
   * (visibilitychange); the document by default, and outside a browser nothing, where
   * the tab counts as shown.
   */
  visibility?: VisibilityEvents;
  /** The clock the waits run on; the platform's by default. */
  clock?: LiveClock;
}

/** What createClient hands the live session beside the options. */
export interface LiveSessionOptions extends LiveOptions {
  request: Requester;
  baseUrl: string;
  /** The live routes the API serves (the descriptor's `live` block); absent, the API serves no live subscriptions. */
  routes?: LiveRoutes;
  /** Where a live ask of an API that serves none is announced; the client's once-per-message hook, the console by default. */
  warn?: Warn;
}

/**
 * The subscription of a handle's list, or of one of its rows when a key is given: the one
 * place the key's string form (the parts joined with `/` in route order) and the domain a
 * row carries are decided. `domain` is the handle's: set for a domain-scoped resource,
 * undefined for a global one.
 */
export function liveSubscription(
  descriptor: ResourceDescriptor,
  domain: Domain | undefined,
  key?: readonly unknown[],
): LiveSubscription {
  const subscription: LiveSubscription = { resource: descriptor.resource };
  if (key !== undefined) {
    subscription.key = key.map(String).join('/');
  }
  if (domain !== undefined) {
    subscription.domain = domain;
  }
  return subscription;
}

/**
 * The subscription of a handle's list across tenants: the resource, and the tenants the
 * page narrowed the list to (`domains`), sorted, each once. Without `domains` the list is
 * of every tenant the server works out.
 */
export function acrossTenantsSubscription(
  descriptor: ResourceDescriptor,
  domains?: readonly string[],
): LiveSubscription {
  const subscription: LiveSubscription = { resource: descriptor.resource, acrossTenants: true };
  if (domains !== undefined) {
    subscription.domains = [...new Set(domains)].sort().join(',');
  }
  return subscription;
}

/** Whether two subscriptions name the same row or list, in the same domain. */
export function sameSubscription(a: LiveSubscription | undefined, b: LiveSubscription | undefined): boolean {
  if (a === undefined || b === undefined) {
    return a === b;
  }
  return subscriptionKey(a) === subscriptionKey(b);
}

/**
 * The subscription as the server distinguishes it: resource, key, and domain; a list
 * across tenants by its resource and the tenants it was narrowed to.
 */
function subscriptionKey(subscription: LiveSubscription): string {
  if (subscription.acrossTenants) {
    const narrowed = subscription.domains === undefined ? 'every tenant' : `only:${subscription.domains}`;
    return `${subscription.resource}||\u0000across tenants\u0000${narrowed}`;
  }
  return `${subscription.resource}|${subscription.key ?? ''}|${subscription.domain ?? ''}`;
}

/**
 * A list the names a page shows are read from as the renewal tells it apart: its
 * subscription and its name field, since the server re-checks one with a name field by
 * that field's grant and one without by the resource's.
 */
function namesKey(list: LiveSubscription): string {
  return `${subscriptionKey(list)}|${list.nameField ?? ''}`;
}

/** Whether a list across tenants covers the tenant, as far as the page can tell: it named the tenant, or named none. */
function covers(subscription: LiveSubscription, domain: string): boolean {
  return subscription.domains === undefined || subscription.domains.split(',').includes(domain);
}

/**
 * The row or list a version belongs to. A row's version is the row's whatever domain it was
 * read in (a row event names no domain, and the server keeps one record per row); a list's
 * is the resource's in its domain.
 */
function versionKey(subscription: LiveSubscription): string {
  return subscription.key === undefined
    ? `list|${subscription.resource}|${subscription.domain ?? ''}`
    : `row|${subscription.resource}|${subscription.key}`;
}

/** What a resource-level version is kept under: every row and list of the resource is at least that version. */
function resourceKey(resource: string): string {
  return `resource|${resource}`;
}

/** What a change's version is kept under: its row, its list, or, for a resource-level change, its resource. */
function changeKey(change: AnsweredChange): string {
  if (change.kind === 'resource') {
    return resourceKey(change.resource);
  }
  if (change.kind === 'row') {
    return versionKey({ resource: change.resource, key: change.key ?? '' });
  }
  return versionKey({ resource: change.resource, domain: change.domain || undefined });
}

/**
 * Whether a change reaches a subscription: a row change the row in every domain it was
 * read in, since the row is one whatever domain that was; a list change the resource's
 * list in its domain; a resource-level change every row and list of the resource.
 */
function reaches(change: AnsweredChange, subscription: LiveSubscription): boolean {
  if (subscription.resource !== change.resource) {
    return false;
  }
  if (change.kind === 'resource') {
    return true;
  }
  if (change.kind === 'row') {
    return subscription.key === (change.key ?? '');
  }
  if (subscription.acrossTenants) {
    // The server writes a list change into this change set only for a tenant some
    // subscription of the person covers; one the page narrowed away is not its list's.
    return covers(subscription, change.domain ?? '');
  }
  return subscription.key === undefined && (subscription.domain || undefined) === (change.domain || undefined);
}

/**
 * Whether a change reaches a list the names a page shows are read from: a list change of
 * its resource in its domain, or a change to the whole resource. Never a row change, which
 * is for the row's own watchers: every write the list's subscribers are told of reaches
 * them as a change to the list.
 */
function reachesNames(change: AnsweredChange, list: LiveSubscription): boolean {
  if (list.resource !== change.resource || change.kind === 'row') {
    return false;
  }
  return change.kind === 'resource' || (list.domain || undefined) === (change.domain || undefined);
}

/**
 * A watched subscription as the renewal sends it: its resource, key, and domain, or, for a
 * list across tenants, its resource, `acrossTenants`, and the tenants it was narrowed to.
 */
function renewed(watched: LiveSubscription): LiveSubscription {
  const { resource, key, domain, acrossTenants, domains } = watched;
  const subscription: LiveSubscription = { resource };
  if (acrossTenants) {
    subscription.acrossTenants = true;
    if (domains !== undefined) {
      subscription.domains = domains;
    }
    return subscription;
  }
  if (key !== undefined) {
    subscription.key = key;
  }
  if (domain !== undefined) {
    subscription.domain = domain;
  }
  return subscription;
}

/** A list the names of a subscription are read from as the session keeps it: its resource, its domain, and its name field. */
function namesList(list: LiveSubscription): LiveSubscription {
  const kept: LiveSubscription = { resource: list.resource };
  if (list.domain !== undefined) {
    kept.domain = list.domain;
  }
  if (list.nameField !== undefined) {
    kept.nameField = list.nameField;
  }
  return kept;
}

const base64url = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_';

/** A fresh opaque id: 16 random bytes as 22 base64url characters. The tab id and the seed are minted with it. */
export function mintLiveId(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  let out = '';
  for (let i = 0; i < 15; i += 3) {
    const n = (bytes[i] << 16) | (bytes[i + 1] << 8) | bytes[i + 2];
    out += base64url[(n >> 18) & 63] + base64url[(n >> 12) & 63] + base64url[(n >> 6) & 63] + base64url[n & 63];
  }
  return out + base64url[bytes[15] >> 2] + base64url[(bytes[15] & 3) << 4];
}

/** The URL with the version parameter set to `version`, replacing one already there; the rest of the URL as given. */
export function versionedUrl(url: string, version: string): string {
  const mark = url.indexOf('?');
  const path = mark === -1 ? url : url.slice(0, mark);
  const query = mark === -1 ? '' : url.slice(mark + 1);
  const kept = query.split('&').filter((part) => part !== '' && !part.startsWith(`${versionParam}=`));
  kept.push(`${versionParam}=${encodeURIComponent(version)}`);
  return `${path}?${kept.join('&')}`;
}

/** The later of two versions written as decimal microseconds; undefined when neither is set. */
function later(a: string | undefined, b: string | undefined): string | undefined {
  if (a === undefined || b === undefined) {
    return a ?? b;
  }
  if (a.length !== b.length) {
    return a.length > b.length ? a : b;
  }
  return a > b ? a : b;
}

/**
 * The one of two versions taken last: of two the feed reported, the later change; else
 * the one taken after the other, since the id of the tab's own write says nothing of
 * time. Undefined when neither is set.
 */
function takenLast(a: Version | undefined, b: Version | undefined): Version | undefined {
  if (!a || !b) {
    return a ?? b;
  }
  if (a.value === a.at && b.value === b.at) {
    return later(a.value, b.value) === a.value ? a : b;
  }
  return a.order > b.order ? a : b;
}

interface Watcher {
  subscription: LiveSubscription;
  refetch: LiveRefetch;
  /** Whether something changed that the watcher has not fetched for yet. */
  stale: boolean;
  /** The latest change heard since the last fetch; undefined when a resync is what made the watcher stale. */
  event?: ChangeEvent;
  /** The timer of the wait that ends in the fetch for other people's changes, while one runs. */
  timer?: unknown;
  /** When the watcher last fetched for other people's changes, on the session's clock. */
  groupedAt?: number;
  /** Set when the watcher was released: nothing fetches for it any more. */
  released: boolean;
}

/**
 * The version a row, a list, or a resource is asked by: the latest change the feed
 * reported for it (`at`, which never moves back), or the id of the tab's own write when
 * that came after. `order` says which of two versions was taken last.
 */
interface Version {
  value: string;
  at?: string;
  order: number;
}

/**
 * The tab's live session: the one place live state lives for a client. It mints the tab
 * id and the seed, holds the live rows and lists the pages watch, decides when each
 * fetches again, renews the tab's subscriptions, unsubscribes when the page leaves, and
 * ends everything at logout.
 *
 * When a watched row or list fetches again. The tab's own write: at once, when the write
 * is answered, since the answer names what the write changed (the changes header); the
 * feed's later report of the same write fetches nothing. Somebody else's change: the
 * first one starts a wait (`changeWait`, two seconds by default) and every change that
 * arrives in it becomes the one fetch at its end, and while changes keep arriving the
 * row or list fetches once per `refetchInterval` (five seconds by default). While the
 * tab is hidden nothing fetches: the row or list remembers that something changed and
 * fetches once when the tab is shown again.
 *
 * A live request (a list or read called with `{ live: true }`) carries the tab id in the
 * subscribe header and the version in the reserved parameter only while the session is
 * `active`: the API serves live subscriptions and a feed is running. Until then the
 * request is handled exactly as today, and `start` refetches every watched row and list
 * once the feed runs, so a page that mounted before the feed was up becomes live without
 * asking again itself.
 *
 * Nothing cached is keyed to a user other than through the version: the seed is minted
 * when the feed starts and again at logout, so a second user on the machine never meets
 * the first user's cached answers.
 */
export class LiveSession {
  /** The tab's id, minted once per tab. */
  readonly tab = mintLiveId();

  private seedValue = mintLiveId();
  private feed: ChangeFeed | undefined;
  private feedReleases: (() => void)[] = [];
  private timer: ReturnType<typeof setInterval> | undefined;
  /** The watchers by subscription (resource, key, domain), the grain the server renews at. */
  private readonly watchers = new Map<string, Set<Watcher>>();
  /**
   * The version per row or list (versionKey) and per resource (resourceKey). Kept past
   * the watcher's release, so a remount asks by the same version and the browser's cache
   * can answer.
   */
  private readonly versions = new Map<string, Version>();
  private versionOrder = 0;
  /**
   * The ids of the tab's own writes, as their answers named them, each with when it was
   * answered: an event the feed reports with one of them is the tab's own change, which
   * it fetched for already. An id is kept for as long as the server keeps a change
   * document.
   */
  private readonly ownWrites = new Map<string, number>();
  /** Subscriptions the server dropped at the last renewal; not renewed again until a watcher asks for them anew. */
  private readonly dropped = new Set<string>();
  /**
   * The lists the names a watched subscription shows are read from, by the subscription
   * (subscriptionKey) and then by the list (namesKey), as the answers to its requests named
   * them: joined over the answers while the subscription is watched, forgotten with its
   * last watcher and at logout.
   */
  private readonly names = new Map<string, Map<string, LiveSubscription>>();
  private readonly request: Requester;
  private readonly baseUrl: string;
  private readonly routes: LiveRoutes | undefined;
  private readonly warn: Warn;
  private readonly renewInterval: number;
  private readonly changeWait: number;
  private readonly refetchInterval: number;
  private readonly xsrf: XsrfOptions | XsrfTokenSource | false;
  private readonly keepaliveFetch: typeof fetch | undefined;
  private readonly page: PageEvents | undefined;
  private readonly visibility: VisibilityEvents | undefined;
  private readonly clock: LiveClock;
  private readonly onPageHide = (): void => this.leave();
  private readonly onPageShow = (event: { persisted?: boolean }): void => {
    if (event.persisted) {
      void this.renew().catch(() => undefined);
    }
  };
  private readonly onVisibilityChange = (): void => this.shown();

  constructor(options: LiveSessionOptions) {
    this.request = options.request;
    this.baseUrl = options.baseUrl;
    this.routes = options.routes;
    this.warn = options.warn ?? warnOnce();
    this.renewInterval = options.renewInterval ?? liveRenewInterval;
    this.changeWait = options.changeWait ?? liveChangeWait;
    this.refetchInterval = options.refetchInterval ?? liveRefetchInterval;
    this.xsrf = options.xsrf === undefined ? defaultXsrf : options.xsrf;
    this.keepaliveFetch = options.fetch ?? (typeof fetch === 'function' ? fetch.bind(globalThis) : undefined);
    this.page = options.page ?? (typeof window === 'undefined' ? undefined : window);
    this.visibility = options.visibility ?? (typeof document === 'undefined' ? undefined : document);
    this.clock = options.clock ?? platformClock;
  }

  /** Whether the API serves live subscriptions: its descriptor carries the live block naming the routes. */
  get enabled(): boolean {
    return this.routes !== undefined;
  }

  /** The version a row or list is asked by before any change arrived for it: minted when the feed starts and at logout. */
  get seed(): string {
    return this.seedValue;
  }

  /** Whether a feed is running. */
  get started(): boolean {
    return this.feed !== undefined;
  }

  /** Whether a `{ live: true }` call carries the subscribe header and the version: the API serves live and a feed is running. */
  get active(): boolean {
    return this.enabled && this.started;
  }

  /**
   * Answers `active` for a request the caller asked to be live, and announces the plain
   * answer once when the API serves no live subscriptions: the page expected live updates
   * it will not get, and a descriptor from a generator that emits the live routes would
   * serve them. Before the feed runs on an API that serves live, the request is plain
   * and nothing is said, since `start` is what makes it live.
   */
  requested(): boolean {
    if (!this.routes) {
      this.warn(noLiveRoutesWarning);
      return false;
    }
    return this.started;
  }

  /**
   * Holds a row or list live: `refetch` runs when the tab's own write changed it (at
   * once), when the feed reports somebody else's change to it or to its whole resource
   * (after the wait that groups such changes), and on every resync; never while the tab
   * is hidden, and then once when it is shown again. A change to a list the names it
   * shows are read from, as its answers named them (`named`), runs it the same way.
   * Returns the release. Several watchers may hold one subscription; it stays current, and
   * renewed, while any does, and the lists its names are read from with it.
   */
  watch(subscription: LiveSubscription, refetch: LiveRefetch): () => void {
    const key = subscriptionKey(subscription);
    const watcher: Watcher = { subscription, refetch, stale: false, released: false };
    let held = this.watchers.get(key);
    if (!held) {
      held = new Set();
      this.watchers.set(key, held);
    }
    held.add(watcher);
    this.dropped.delete(key);
    return () => {
      watcher.released = true;
      this.settle(watcher);
      const current = this.watchers.get(key);
      if (!current) {
        return;
      }
      current.delete(watcher);
      if (current.size === 0) {
        this.watchers.delete(key);
        this.names.delete(key);
      }
    };
  }

  /**
   * The version a request for the row or list carries: the latest change taken for it or
   * for its whole resource, or for one of the lists the names it shows are read from or
   * their whole resource, the feed's (its `at`) or the tab's own write (its id), else the
   * seed.
   */
  version(subscription: LiveSubscription): string {
    const own = subscription.acrossTenants
      ? this.acrossVersion(subscription)
      : this.versions.get(versionKey(subscription));
    let latest = takenLast(own, this.versions.get(resourceKey(subscription.resource)));
    for (const list of this.names.get(subscriptionKey(subscription))?.values() ?? []) {
      latest = takenLast(latest, this.versions.get(versionKey(list)));
      latest = takenLast(latest, this.versions.get(resourceKey(list.resource)));
    }
    return latest?.value ?? this.seedValue;
  }

  /**
   * The tab's current live subscriptions, as the renewal sends them: every watched row and
   * list the server has not dropped, each as its resource, key, and domain and nothing
   * else, since the server refuses a field it does not know. A list across tenants is its
   * resource, `acrossTenants`, and the tenants it was narrowed to: the page's filter
   * travels with the renewal, and the server works the set out again from it. After each
   * come the lists the names it shows are read from, each once in the whole renewal, as its
   * resource, its domain, and its `nameField`, by which the server re-checks it: a list
   * the tab also watches in its own right is sent in both forms where they differ. One
   * with no `nameField` is the list itself, and is not sent once the server dropped it.
   */
  subscriptions(): LiveSubscription[] {
    const current: LiveSubscription[] = [];
    const sent = new Set<string>();
    const send = (subscription: LiveSubscription): void => {
      const key = namesKey(subscription);
      if (!sent.has(key)) {
        sent.add(key);
        current.push(subscription);
      }
    };
    for (const [key, held] of this.watchers) {
      if (this.dropped.has(key)) {
        continue;
      }
      const [first] = held;
      if (!first) {
        continue;
      }
      send(renewed(first.subscription));
      for (const list of this.names.get(key)?.values() ?? []) {
        if (list.nameField === undefined && this.dropped.has(subscriptionKey(list))) {
          continue;
        }
        send({ ...list });
      }
    }
    return current;
  }

  /**
   * Takes a live request's names header: the lists the server registered beside the
   * request's own subscription, the ones the names its answer shows are read from. They
   * are kept under each watched subscription the header's `of` names, joined with the ones
   * kept already, each once: a list by its resource and domain, a list across tenants by
   * its resource and the tenants it was narrowed to, and a row by its resource and key in
   * every domain the tab watches it in, since the server's record of a row names no
   * domain. From then on a change to one of those lists, or to its whole resource, reaches
   * the subscription's watchers as a change to their own list does; the subscription's
   * version is the latest among its own and theirs; and each renewal carries them. An `of`
   * nothing watches keeps nothing, and nothing happens while no feed runs: the pages are
   * plain then.
   */
  named(names: LiveNames): void {
    if (!this.feed || names.names.length === 0) {
      return;
    }
    for (const holder of this.holders(names.of)) {
      let kept = this.names.get(holder);
      if (!kept) {
        kept = new Map();
        this.names.set(holder, kept);
      }
      for (const list of names.names) {
        kept.set(namesKey(list), namesList(list));
      }
    }
  }

  /**
   * Dispatches one change the feed reports. An event carrying the id of one of the
   * tab's own writes is that write's echo: the tab fetched when the write was answered,
   * so nothing happens. Any other is somebody else's change: its `at` becomes the
   * version of the row or list it names, whether or not anything watches, so a page
   * mounted later asks by it, and every watcher it reaches (a row event the row's
   * watchers in every domain, a resource event every watcher of the resource, a list or
   * resource event of a list the names a subscription shows are read from that
   * subscription's watchers) fetches after the wait that groups such changes: the first
   * starts the wait, the rest join it.
   */
  dispatch(event: ChangeEvent): void {
    if (event.write !== undefined && this.ownWrites.has(event.write)) {
      return;
    }
    this.record(changeKey(event), event.at);
    for (const watcher of this.reached(event)) {
      this.changed(watcher, event);
    }
  }

  /**
   * The version of a list across tenants: the one taken last among the versions of its
   * resource's list in the tenants it covers, since a change to any of them is a change
   * to it.
   */
  private acrossVersion(subscription: LiveSubscription): Version | undefined {
    const prefix = `list|${subscription.resource}|`;
    let latest: Version | undefined;
    for (const [key, version] of this.versions) {
      if (!key.startsWith(prefix) || !covers(subscription, key.slice(prefix.length))) {
        continue;
      }
      if (!latest || version.order > latest.order) {
        latest = version;
      }
    }
    return latest;
  }

  /**
   * Takes a write's answer: what the client calls with the changes header of every
   * answer that carries one. The write's id is remembered, so the feed's report of the
   * same write fetches nothing; the id becomes the version of every row and list the
   * answer names; and each watcher they reach fetches at once, before the caller of the
   * write is answered, whatever wait it was in for other people's changes, since the
   * fetch shows those too. An answer that says `all` fetches everything the tab holds
   * live by a fresh seed, as a resync does. Nothing happens while no feed runs: the
   * pages are plain then.
   */
  wrote(answer: WriteChanges): void {
    if (!this.feed) {
      return;
    }
    const now = this.clock.now();
    for (const [write, answeredAt] of this.ownWrites) {
      if (now - answeredAt > liveChangeTtl) {
        this.ownWrites.delete(write);
      }
    }
    this.ownWrites.set(answer.write, now);
    const reached = answer.all ? this.reseed() : this.taken(answer);
    // The versions are taken already, so a reload the write's caller makes itself asks
    // by them. Each fetch runs in a microtask of its own: still before that caller
    // continues, and a watcher that throws fails neither the write, which is committed,
    // nor the watchers after it.
    for (const watcher of reached) {
      queueMicrotask(() => this.fetch(watcher, false));
    }
  }

  /**
   * Forgets every version seen, mints a fresh seed, and refetches every watched row and
   * list by it: at once while the tab is shown, when it is shown again otherwise.
   */
  resync(): void {
    for (const watcher of this.reseed()) {
      this.fetch(watcher, false);
    }
  }

  /**
   * Starts live pages for the tab: fetches the identity the server minted, starts the
   * feed with it, wires its events to the watchers, begins renewing, listens for the page
   * leaving, and refetches every watched row and list by a fresh seed so each becomes
   * live. On an API that serves no live subscriptions it announces the absence once (an
   * application that provides a feed expects live pages) and throws; it does nothing when
   * a feed is already running.
   */
  async start(feed: ChangeFeed): Promise<void> {
    if (!this.routes) {
      this.warn(noLiveRoutesWarning);
      throw new Error(
        'this API serves no live subscriptions: its descriptor carries no live block, so no change feed can be started for it',
      );
    }
    if (this.feed) {
      return;
    }
    const identity = await this.request<LiveIdentity>('GET', this.routes.tokenRoute, { headers: this.headers() });
    await feed.start(identity);
    this.feed = feed;
    this.feedReleases = [feed.onChange((event) => this.dispatch(event)), feed.onResync(() => this.resync())];
    this.page?.addEventListener('pagehide', this.onPageHide);
    this.page?.addEventListener('pageshow', this.onPageShow);
    // After the feed's own listener, which the feed added in its start: a tab shown
    // again after the feed lost track resyncs first, and that fetch is the one fetch.
    this.visibility?.addEventListener('visibilitychange', this.onVisibilityChange);
    this.timer = setInterval(() => {
      void this.renew().catch(() => undefined);
    }, this.renewInterval);
    this.resync();
  }

  /**
   * Renews the tab's current subscriptions with the server, which re-checks each against
   * the digest, and drops the ones it reports dropped: they are not renewed again until a
   * watcher asks for them anew. A list with a `nameField` the server drops is forgotten
   * from every subscription that kept it, and nothing else is dropped with it: the
   * subscription stays current, and so does a list of the same resource the tab watches
   * in its own right. At most liveRenewLimit subscriptions travel in one renewal, the
   * server's limit. Nothing is sent while no feed runs or nothing is watched.
   */
  async renew(): Promise<RenewResponse | undefined> {
    if (!this.feed || !this.routes) {
      return undefined;
    }
    const subscriptions = this.subscriptions().slice(0, liveRenewLimit);
    if (subscriptions.length === 0) {
      return undefined;
    }
    const response = await this.request<RenewResponse | null>('POST', this.routes.renewRoute, {
      body: { tab: this.tab, subscriptions },
      headers: this.headers(),
    });
    for (const dropped of response?.dropped ?? []) {
      if (dropped.nameField) {
        this.forget(dropped);
        continue;
      }
      this.dropped.add(subscriptionKey(dropped));
    }
    return response ?? undefined;
  }

  /**
   * The logout path, run before the session is logged out: deletes every subscription of
   * the principal and revokes the feed's identity (`all: true`), stops the feed, forgets
   * every version seen and the lists the names of each subscription are read from, and
   * mints a fresh seed so the next user on this machine never meets this user's cached
   * answers. Best effort: a refused unsubscribe still stops the
   * feed. Without a running feed nothing is sent, since nothing was subscribed. When the
   * session is already gone (`sessionGone`: a session check answered unauthenticated),
   * nothing is sent either: the server would refuse a call no session speaks for, and its
   * expiry ends the subscriptions; the feed stops and the seed is reminted all the same.
   */
  async stop(options: { sessionGone?: boolean } = {}): Promise<void> {
    if (this.timer !== undefined) {
      clearInterval(this.timer);
      this.timer = undefined;
    }
    this.page?.removeEventListener('pagehide', this.onPageHide);
    this.page?.removeEventListener('pageshow', this.onPageShow);
    this.visibility?.removeEventListener('visibilitychange', this.onVisibilityChange);
    for (const [, held] of this.watchers) {
      for (const watcher of held) {
        this.settle(watcher);
      }
    }
    const feed = this.feed;
    if (feed) {
      this.feed = undefined;
      for (const release of this.feedReleases) {
        release();
      }
      this.feedReleases = [];
      if (this.routes && !options.sessionGone) {
        try {
          await this.request<unknown>('POST', this.routes.unsubscribeRoute, {
            body: { tab: this.tab, all: true },
            headers: this.headers(),
          });
        } catch {
          // Best effort: the server's TTL ends what this could not.
        }
      }
      try {
        await feed.stop();
      } catch {
        // The feed is being discarded either way.
      }
    }
    this.versions.clear();
    this.ownWrites.clear();
    this.dropped.clear();
    this.names.clear();
    this.seedValue = mintLiveId();
  }

  /**
   * The page-leave unsubscribe: deletes this tab's subscriptions with a keepalive fetch,
   * which outlives the page where the transport would not. Best effort, sent only while a
   * feed runs; a page restored from the back-forward cache renews at once instead.
   */
  leave(): void {
    if (!this.feed || !this.routes || !this.keepaliveFetch) {
      return;
    }
    const headers: Record<string, string> = {
      Accept: 'application/json',
      'Content-Type': 'application/json',
      ...this.headers(),
    };
    if (this.xsrf) {
      const token = 'token' in this.xsrf ? (this.xsrf.token() ?? undefined) : readCookie(this.xsrf.cookieName);
      if (token !== undefined) {
        headers[this.xsrf.headerName ?? defaultXsrf.headerName] = token;
      }
    }
    void this.keepaliveFetch(`${this.baseUrl}/${this.routes.unsubscribeRoute}`, {
      method: 'POST',
      headers,
      body: JSON.stringify({ tab: this.tab, all: false }),
      credentials: 'same-origin',
      keepalive: true,
    }).catch(() => undefined);
  }

  /** The subscribe header every live-route request carries, so each shows the tab on its request log line. */
  private headers(): Record<string, string> {
    return { [subscribeHeader]: this.tab };
  }

  /**
   * Takes a change the feed reported as the version of its row, list, or resource, never
   * moving back: an event no later than the latest one taken changes nothing, since the
   * fetch made for the later one, or for the tab's own write after it, shows it too.
   */
  private record(key: string, at: string): void {
    const current = this.versions.get(key);
    if (current?.at !== undefined && later(current.at, at) === current.at) {
      return;
    }
    this.versions.set(key, { value: at, at, order: ++this.versionOrder });
  }

  /** Mints a fresh seed, forgets every version seen, and answers every watcher, each now stale. */
  private reseed(): Watcher[] {
    this.seedValue = mintLiveId();
    this.versions.clear();
    const all: Watcher[] = [];
    for (const [, held] of this.watchers) {
      for (const watcher of held) {
        watcher.stale = true;
        watcher.event = undefined;
        all.push(watcher);
      }
    }
    return all;
  }

  /**
   * Takes the changes of the tab's own write: the write's id becomes the version of
   * each row, list, or resource the answer names, and the watchers they reach, each
   * once and now stale, are answered.
   */
  private taken(answer: WriteChanges): Watcher[] {
    const reached = new Set<Watcher>();
    for (const change of answer.changes) {
      const key = changeKey(change);
      this.versions.set(key, { value: answer.write, at: this.versions.get(key)?.at, order: ++this.versionOrder });
      for (const watcher of this.reached(change)) {
        watcher.stale = true;
        watcher.event = { ...change, at: answer.write, write: answer.write };
        reached.add(watcher);
      }
    }
    return [...reached];
  }

  /**
   * The watchers a change reaches, in the order they began watching: the ones of the row
   * or list it names, and the ones of a subscription that keeps a list it reaches.
   */
  private reached(change: AnsweredChange): Watcher[] {
    const reached: Watcher[] = [];
    for (const [key, held] of this.watchers) {
      const kept = this.names.get(key);
      const byNames = kept !== undefined && [...kept.values()].some((list) => reachesNames(change, list));
      for (const watcher of held) {
        if (byNames || reaches(change, watcher.subscription)) {
          reached.push(watcher);
        }
      }
    }
    return reached;
  }

  /**
   * The watched subscriptions a names header's `of` names: a list, or a list across
   * tenants, by its own key; a row by its resource and key in every domain it is watched
   * in.
   */
  private holders(of: LiveSubscription): string[] {
    if (of.key === undefined || of.acrossTenants) {
      const key = subscriptionKey(of);
      return this.watchers.has(key) ? [key] : [];
    }
    const holders: string[] = [];
    for (const [key, held] of this.watchers) {
      const [first] = held;
      const subscription = first?.subscription;
      if (
        subscription &&
        !subscription.acrossTenants &&
        subscription.resource === of.resource &&
        subscription.key === of.key
      ) {
        holders.push(key);
      }
    }
    return holders;
  }

  /** Forgets a list the names of a subscription are read from, from every subscription that kept it. */
  private forget(list: LiveSubscription): void {
    const key = namesKey(list);
    for (const [holder, kept] of this.names) {
      kept.delete(key);
      if (kept.size === 0) {
        this.names.delete(holder);
      }
    }
  }

  /** Whether the tab is hidden, read when asked: a feed that resyncs as the tab is shown runs before the session hears of it. */
  private get hidden(): boolean {
    return this.visibility?.visibilityState === 'hidden';
  }

  /**
   * Somebody else's change reached the watcher. The first since its last fetch starts
   * the wait: `changeWait` from now, and no sooner than `refetchInterval` after the last
   * fetch made for such changes; a change that finds the watcher waiting joins the wait.
   * While the tab is hidden no wait starts: the watcher fetches when the tab is shown.
   */
  private changed(watcher: Watcher, event: ChangeEvent): void {
    watcher.event = event;
    if (watcher.stale) {
      return;
    }
    watcher.stale = true;
    if (this.hidden) {
      return;
    }
    const now = this.clock.now();
    const due = Math.max(now + this.changeWait, (watcher.groupedAt ?? -Infinity) + this.refetchInterval);
    watcher.timer = this.clock.setTimeout(() => {
      watcher.timer = undefined;
      this.fetch(watcher, true);
    }, due - now);
  }

  /**
   * Fetches a stale watcher by its version, ending any wait it was in. A hidden tab
   * fetches nothing: the watcher stays stale until the tab is shown. `grouped` says the
   * fetch is the one made for other people's changes, which the next such fetch keeps
   * its distance from.
   */
  private fetch(watcher: Watcher, grouped: boolean): void {
    if (watcher.timer !== undefined) {
      this.clock.clearTimeout(watcher.timer);
      watcher.timer = undefined;
    }
    if (!watcher.stale || watcher.released || this.hidden) {
      return;
    }
    const event = watcher.event;
    watcher.stale = false;
    watcher.event = undefined;
    if (grouped) {
      watcher.groupedAt = this.clock.now();
    }
    watcher.refetch(this.version(watcher.subscription), event);
  }

  /** The tab's visibility changed: shown, every watcher that went stale while it was hidden fetches, once. */
  private shown(): void {
    if (this.hidden) {
      return;
    }
    for (const [, held] of this.watchers) {
      for (const watcher of [...held]) {
        if (watcher.stale) {
          this.fetch(watcher, true);
        }
      }
    }
  }

  /** Ends a watcher's wait and forgets that it was stale: it was released, or the session stopped. */
  private settle(watcher: Watcher): void {
    if (watcher.timer !== undefined) {
      this.clock.clearTimeout(watcher.timer);
      watcher.timer = undefined;
    }
    watcher.stale = false;
    watcher.event = undefined;
  }
}

/**
 * Whether a page is live, from its own choice (`live` on its config, undefined when it made
 * none) and its resource's metadata (`ResourceMeta.live`, whether the resource's changes
 * are published). The metadata decides the default: a resource whose changes are published
 * makes its page live unless the page said false; one whose changes are not, a view or a
 * computed resource, makes it plain, and a page that asked anyway is announced once through
 * `warn` and asks plainly, since its subscription would never hear a change. Metadata
 * without the field, from a generator that predates it, leaves the page's own choice
 * standing, plain when it made none.
 */
export function pageIsLive(
  resource: string,
  choice: boolean | undefined,
  meta: { live?: boolean } | undefined,
  warn: Warn,
): boolean {
  if (choice === false) {
    return false;
  }
  const published = meta?.live;
  if (published === false) {
    if (choice === true) {
      warn(unpublishedLiveWarning(resource));
    }
    return false;
  }
  if (published === true) {
    return true;
  }
  return choice === true;
}

/** The announcement for a page that asked to be live on a resource whose changes are not published. */
export function unpublishedLiveWarning(resource: string): string {
  return (
    `The ${resource} page asked to be live, but the generated metadata says changes to ${resource} are not ` +
    'published (a view or a computed resource), so the page asks as a plain one. Drop live: true from its ' +
    'config, or make the resource one whose changes are published.'
  );
}
