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
 * the version the refetch carries.
 */
export interface ChangeEvent {
  kind: 'row' | 'list' | 'resource';
  resource: string;
  key?: string;
  domain?: string;
  deleted?: boolean;
  at: string;
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
 * version parameter. `event` is the change that caused it, absent on a resync.
 */
export type LiveRefetch = (version: string, event?: ChangeEvent) => void;

/** The page events the live session listens to; the window, in a browser. */
export interface PageEvents {
  addEventListener(type: string, listener: (event: { persisted?: boolean }) => void): void;
  removeEventListener(type: string, listener: (event: { persisted?: boolean }) => void): void;
}

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
}

/**
 * The tab's live session: the one place live state lives for a client. It mints the tab
 * id and the seed, holds the live rows and lists the pages watch, dispatches the feed's
 * events to them, renews the tab's subscriptions, unsubscribes when the page leaves, and
 * ends everything at logout.
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
  /** The latest `at` seen per row or list (versionKey). Kept past the watcher's release, so a remount asks by the same version and the browser's cache can answer. */
  private readonly lastSeen = new Map<string, string>();
  /** The latest `at` of a resource-level event per resource: every row and list of it is at least that version. */
  private readonly resourceSeen = new Map<string, string>();
  /** Subscriptions the server dropped at the last renewal; not renewed again until a watcher asks for them anew. */
  private readonly dropped = new Set<string>();
  private readonly request: Requester;
  private readonly baseUrl: string;
  private readonly routes: LiveRoutes | undefined;
  private readonly warn: Warn;
  private readonly renewInterval: number;
  private readonly xsrf: XsrfOptions | XsrfTokenSource | false;
  private readonly keepaliveFetch: typeof fetch | undefined;
  private readonly page: PageEvents | undefined;
  private readonly onPageHide = (): void => this.leave();
  private readonly onPageShow = (event: { persisted?: boolean }): void => {
    if (event.persisted) {
      void this.renew().catch(() => undefined);
    }
  };

  constructor(options: LiveSessionOptions) {
    this.request = options.request;
    this.baseUrl = options.baseUrl;
    this.routes = options.routes;
    this.warn = options.warn ?? warnOnce();
    this.renewInterval = options.renewInterval ?? liveRenewInterval;
    this.xsrf = options.xsrf === undefined ? defaultXsrf : options.xsrf;
    this.keepaliveFetch = options.fetch ?? (typeof fetch === 'function' ? fetch.bind(globalThis) : undefined);
    this.page = options.page ?? (typeof window === 'undefined' ? undefined : window);
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
   * Holds a row or list live: `refetch` runs whenever the feed reports a change to it
   * (or to its whole resource) and on every resync. Returns the release. Several watchers
   * may hold one subscription; it stays current, and renewed, while any does.
   */
  watch(subscription: LiveSubscription, refetch: LiveRefetch): () => void {
    const key = subscriptionKey(subscription);
    const watcher: Watcher = { subscription, refetch };
    let held = this.watchers.get(key);
    if (!held) {
      held = new Set();
      this.watchers.set(key, held);
    }
    held.add(watcher);
    this.dropped.delete(key);
    return () => {
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

  /** The version a request for the row or list carries: the latest change seen for it, else the seed. */
  version(subscription: LiveSubscription): string {
    return (
      later(this.lastSeen.get(versionKey(subscription)), this.resourceSeen.get(subscription.resource)) ?? this.seedValue
    );
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
   * Dispatches one change: the watchers of the row or list it names refetch with its
   * `at`; a row event reaches the row's watchers in every domain, since the row is one
   * whatever domain it was read in; a resource event reaches every watcher of the
   * resource. The version is recorded whether or not anything watches, so a page mounted
   * later asks by it.
   */
  dispatch(event: ChangeEvent): void {
    if (event.kind === 'resource') {
      this.resourceSeen.set(event.resource, later(this.resourceSeen.get(event.resource), event.at) ?? event.at);
      this.refetchEach((subscription) => subscription.resource === event.resource, event);
      return;
    }
    if (event.kind === 'row') {
      const key = event.key ?? '';
      this.record(versionKey({ resource: event.resource, key }), event.at);
      this.refetchEach((subscription) => subscription.resource === event.resource && subscription.key === key, event);
      return;
    }
    const domain = event.domain || undefined;
    this.record(versionKey({ resource: event.resource, domain }), event.at);
    this.refetchEach(
      (subscription) =>
        subscription.resource === event.resource &&
        subscription.key === undefined &&
        (subscription.domain || undefined) === domain,
      event,
    );
  }

  /** Forgets every version seen, mints a fresh seed, and refetches every watched row and list by it. */
  resync(): void {
    this.seedValue = mintLiveId();
    this.lastSeen.clear();
    this.resourceSeen.clear();
    for (const [, held] of this.watchers) {
      for (const watcher of [...held]) {
        watcher.refetch(this.seedValue);
      }
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
    this.lastSeen.clear();
    this.resourceSeen.clear();
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

  /** Records a version for a row or list, never moving it back. */
  private record(key: string, at: string): void {
    this.lastSeen.set(key, later(this.lastSeen.get(key), at) ?? at);
  }

  /** Refetches every watcher whose subscription the predicate admits, each by its own version. */
  private refetchEach(admits: (subscription: LiveSubscription) => boolean, event: ChangeEvent): void {
    for (const [, held] of this.watchers) {
      for (const watcher of [...held]) {
        if (admits(watcher.subscription)) {
          watcher.refetch(this.version(watcher.subscription), event);
        }
      }
    }
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
