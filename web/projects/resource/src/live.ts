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
 */
export interface LiveSubscription {
  resource: string;
  key?: string;
  domain?: string;
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

/** Whether two subscriptions name the same row or list, in the same domain. */
export function sameSubscription(a: LiveSubscription | undefined, b: LiveSubscription | undefined): boolean {
  if (a === undefined || b === undefined) {
    return a === b;
  }
  return subscriptionKey(a) === subscriptionKey(b);
}

/** The subscription as the server distinguishes it: resource, key, and domain. */
function subscriptionKey(subscription: LiveSubscription): string {
  return `${subscription.resource}|${subscription.key ?? ''}|${subscription.domain ?? ''}`;
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
  return subscription.key === undefined && (subscription.domain || undefined) === (change.domain || undefined);
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
   * is hidden, and then once when it is shown again. Returns the release. Several
   * watchers may hold one subscription; it stays current, and renewed, while any does.
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
      }
    };
  }

  /**
   * The version a request for the row or list carries: the latest change taken for it or
   * for its whole resource, the feed's (its `at`) or the tab's own write (its id), else
   * the seed.
   */
  version(subscription: LiveSubscription): string {
    const own = this.versions.get(versionKey(subscription));
    const whole = this.versions.get(resourceKey(subscription.resource));
    if (!own || !whole) {
      return (own ?? whole)?.value ?? this.seedValue;
    }
    if (own.value === own.at && whole.value === whole.at) {
      return later(own.value, whole.value) ?? own.value;
    }
    return own.order > whole.order ? own.value : whole.value;
  }

  /**
   * The tab's current live subscriptions, as the renewal sends them: every watched row and
   * list the server has not dropped, each as its resource, key, and domain and nothing
   * else, since the server refuses a field it does not know.
   */
  subscriptions(): LiveSubscription[] {
    const current: LiveSubscription[] = [];
    for (const [key, held] of this.watchers) {
      if (this.dropped.has(key)) {
        continue;
      }
      const [first] = held;
      if (first) {
        const { resource, key: rowKey, domain } = first.subscription;
        const subscription: LiveSubscription = { resource };
        if (rowKey !== undefined) {
          subscription.key = rowKey;
        }
        if (domain !== undefined) {
          subscription.domain = domain;
        }
        current.push(subscription);
      }
    }
    return current;
  }

  /**
   * Dispatches one change the feed reports. An event carrying the id of one of the
   * tab's own writes is that write's echo: the tab fetched when the write was answered,
   * so nothing happens. Any other is somebody else's change: its `at` becomes the
   * version of the row or list it names, whether or not anything watches, so a page
   * mounted later asks by it, and every watcher it reaches (a row event the row's
   * watchers in every domain, a resource event every watcher of the resource) fetches
   * after the wait that groups such changes: the first starts the wait, the rest join
   * it.
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
   * watcher asks for them anew. At most liveRenewLimit subscriptions travel in one
   * renewal, the server's limit. Nothing is sent while no feed runs or nothing is watched.
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
      this.dropped.add(subscriptionKey(dropped));
    }
    return response ?? undefined;
  }

  /**
   * The logout path, run before the session is logged out: deletes every subscription of
   * the principal and revokes the feed's identity (`all: true`), stops the feed, forgets
   * every version seen, and mints a fresh seed so the next user on this machine never
   * meets this user's cached answers. Best effort: a refused unsubscribe still stops the
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

  /** The watchers a change reaches, in the order they began watching. */
  private reached(change: AnsweredChange): Watcher[] {
    const reached: Watcher[] = [];
    for (const [, held] of this.watchers) {
      for (const watcher of held) {
        if (reaches(change, watcher.subscription)) {
          reached.push(watcher);
        }
      }
    }
    return reached;
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
