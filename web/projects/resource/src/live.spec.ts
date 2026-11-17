import { describe, expect, it } from 'bun:test';
import { Domain, Resource } from './brands';
import { AnyResourceHandle, createClient, Client } from './client';
import { ApiDescriptor, ResourceDescriptor } from './descriptor';
import {
  AnsweredChange,
  ChangeEvent,
  ChangeFeed,
  changesHeader,
  liveChangeWait,
  LiveClock,
  LiveIdentity,
  LiveOptions,
  liveRefetchInterval,
  liveRenewLimit,
  LiveSubscription,
  liveSubscription,
  mintLiveId,
  noLiveRoutesWarning,
  PageEvents,
  pageIsLive,
  parseChangesHeader,
  sameSubscription,
  subscribeHeader,
  unpublishedLiveWarning,
  versionedUrl,
  VisibilityEvents,
} from './live';
import { TransportRequest } from './transport';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';

// The tab's live session: a list or read asked to be live carries the tab's subscribe
// header and the version parameter only while a feed runs; the feed's events reach the
// watchers of the row or list they name and refetch by the event's version; the tab's
// own write fetches at once and its echo in the feed fetches nothing, other people's
// changes wait and become one fetch, and a hidden tab fetches when it is shown; renewal
// carries the tab's current subscriptions and drops what the server drops; the page-leave
// and logout paths unsubscribe; and a resync refetches everything by a fresh seed.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  live: { renewRoute: 'live/renew', unsubscribeRoute: 'live/unsubscribe', tokenRoute: 'live/token' },
  resources: {
    Ships: {
      resource: 'Ships' as Resource,
      property: 'ships',
      route: 'ships',
      scope: 'domain',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25, max: 200 },
      order: [{ field: 'name', direction: 'asc' }],
    },
    ShipClasses: {
      resource: 'ShipClasses' as Resource,
      property: 'shipClasses',
      route: 'ship-classes',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25 },
    },
    Berths: {
      resource: 'Berths' as Resource,
      property: 'berths',
      route: 'berths',
      scope: 'global',
      consolidated: false,
      keys: ['hangarId', 'slot'],
      operations: ['list', 'read'],
      page: { default: 25 },
    },
  },
  methods: {},
};

interface Row {
  id: string;
}

interface Global {
  shipClasses: AnyResourceHandle<Row>;
  berths: AnyResourceHandle<Row, [string, number]>;
}

interface Sector {
  ships: AnyResourceHandle<Row>;
}

const identity: LiveIdentity = {
  uid: 'u-7',
  token: '',
  project: 'lab',
  database: 'live',
  apiKey: '',
  emulator: 'localhost:8080',
};

/** A feed a spec drives by hand: it records what it was started with and emits on command. */
class FakeFeed implements ChangeFeed {
  started: LiveIdentity[] = [];
  stopped = 0;
  private changeListeners = new Set<(event: ChangeEvent) => void>();
  private resyncListeners = new Set<() => void>();

  async start(identity: LiveIdentity): Promise<void> {
    this.started.push(identity);
  }

  async stop(): Promise<void> {
    this.stopped++;
  }

  onChange(listener: (event: ChangeEvent) => void): () => void {
    this.changeListeners.add(listener);
    return () => this.changeListeners.delete(listener);
  }

  onResync(listener: () => void): () => void {
    this.resyncListeners.add(listener);
    return () => this.resyncListeners.delete(listener);
  }

  emit(event: ChangeEvent): void {
    for (const listener of this.changeListeners) {
      listener(event);
    }
  }

  resync(): void {
    for (const listener of this.resyncListeners) {
      listener();
    }
  }

  get listening(): number {
    return this.changeListeners.size + this.resyncListeners.size;
  }
}

/** A window stand-in: the listeners the session attached, fired by the spec. */
class FakePage implements PageEvents {
  listeners = new Map<string, Set<(event: { persisted?: boolean }) => void>>();

  addEventListener(type: string, listener: (event: { persisted?: boolean }) => void): void {
    let held = this.listeners.get(type);
    if (!held) {
      held = new Set();
      this.listeners.set(type, held);
    }
    held.add(listener);
  }

  removeEventListener(type: string, listener: (event: { persisted?: boolean }) => void): void {
    this.listeners.get(type)?.delete(listener);
  }

  fire(type: string, event: { persisted?: boolean } = {}): void {
    for (const listener of [...(this.listeners.get(type) ?? [])]) {
      listener(event);
    }
  }
}

/** A document stand-in: whether the tab is shown, set by the spec, and the visibilitychange listeners. */
class FakeVisibility implements VisibilityEvents {
  visibilityState = 'visible';
  listeners = new Set<() => void>();

  addEventListener(_type: string, listener: () => void): void {
    this.listeners.add(listener);
  }

  removeEventListener(_type: string, listener: () => void): void {
    this.listeners.delete(listener);
  }

  become(state: 'visible' | 'hidden'): void {
    this.visibilityState = state;
    for (const listener of [...this.listeners]) {
      listener();
    }
  }
}

/** A clock the spec moves: the time, and the timers due as it advances, run in the order they fall due. */
class FakeClock implements LiveClock {
  time = 0;
  private timers: { id: number; due: number; run: () => void }[] = [];
  private nextId = 0;

  now(): number {
    return this.time;
  }

  setTimeout(run: () => void, delay: number): unknown {
    const id = ++this.nextId;
    this.timers.push({ id, due: this.time + Math.max(0, delay), run });
    return id;
  }

  clearTimeout(timer: unknown): void {
    this.timers = this.timers.filter((held) => held.id !== timer);
  }

  /** How many timers are waiting. */
  get waiting(): number {
    return this.timers.length;
  }

  /** Moves the time forward, running each timer that falls due on the way, at its own time. */
  advance(milliseconds: number): void {
    const until = this.time + milliseconds;
    for (;;) {
      const due = this.timers.filter((held) => held.due <= until).sort((a, b) => a.due - b.due || a.id - b.id)[0];
      if (!due) {
        break;
      }
      this.timers = this.timers.filter((held) => held !== due);
      this.time = due.due;
      due.run();
    }
    this.time = until;
  }
}

/** The changes header as the server writes it for a write and what it changed in the caller's own change set. */
function changesAnswer(write: string, changes: AnsweredChange[], all = false): Record<string, string> {
  const answer = all ? { write, all } : { write, changes };
  return { [changesHeader.toLowerCase()]: encodeURIComponent(JSON.stringify(answer)) };
}

/** The URL without its version parameter, and the version it carried. */
function split(url: string): { url: string; version: string | undefined } {
  const mark = url.indexOf('?');
  if (mark === -1) {
    return { url, version: undefined };
  }
  const parts = url
    .slice(mark + 1)
    .split('&')
    .filter((part) => part !== '');
  const versionPart = parts.find((part) => part.startsWith('_v='));
  const rest = parts.filter((part) => !part.startsWith('_v='));
  return {
    url: rest.length > 0 ? `${url.slice(0, mark)}?${rest.join('&')}` : url.slice(0, mark),
    version: versionPart ? decodeURIComponent(versionPart.slice(3)) : undefined,
  };
}

/** One scripted answer: its body, status and headers, and what happens while the server works on it. */
interface ScriptedPage {
  rows?: unknown;
  status?: number;
  headers?: Record<string, string>;
  /** Runs before the answer is returned: what reaches the tab while its request is still open. */
  meanwhile?: () => void;
}

/** A transport scripted by URL with the version parameter set aside; the live routes answer themselves. */
function scripted(pages: Record<string, ScriptedPage>): ScriptedTransport {
  return scriptedTransport((request) => {
    if (request.url === '/api/live/token') {
      return { status: 200, body: identity };
    }
    if (request.url === '/api/live/renew') {
      return { status: 200, body: { kept: [], dropped: [], expiresAt: '2026-10-02T12:00:00Z' } };
    }
    if (request.url === '/api/live/unsubscribe') {
      return { status: 204, body: undefined };
    }
    const page = pages[split(request.url).url];
    if (!page) {
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    }
    page.meanwhile?.();
    return { status: page.status ?? 200, body: page.rows, headers: page.headers ?? {} };
  });
}

interface Harness {
  /** What the client's warn hook received, in order. */
  warnings: string[];
  client: Client<Global, Sector>;
  transport: ScriptedTransport;
  feed: FakeFeed;
  page: FakePage;
  /** Whether the tab is shown; shown unless the spec hides it. */
  visibility: FakeVisibility;
  /** The clock the session's waits run on; it moves when the spec moves it. */
  clock: FakeClock;
  /** Moves the clock far enough that every wait for other people's changes has ended. */
  elapse(): void;
  keepalive: { url: string; init: RequestInit }[];
}

function harness(
  pages: Record<string, ScriptedPage>,
  api: ApiDescriptor = descriptor,
  live: Partial<LiveOptions> = {},
): Harness {
  const transport = scripted(pages);
  const page = new FakePage();
  const visibility = new FakeVisibility();
  const clock = new FakeClock();
  const keepalive: { url: string; init: RequestInit }[] = [];
  const warnings: string[] = [];
  const fetchStandIn = (async (url: string | URL | Request, init?: RequestInit) => {
    keepalive.push({ url: String(url), init: init ?? {} });
    return new Response(null, { status: 204 });
  }) as typeof fetch;
  const client = createClient<Global, Sector>(api, {
    baseUrl: '/api',
    transport,
    warn: (message) => warnings.push(message),
    // The zone header has its own suite (time-zone.spec); these pin the live headers alone.
    timeZone: '',
    live: { renewInterval: 60 * 60 * 1000, page, visibility, clock, fetch: fetchStandIn, xsrf: false, ...live },
  });
  const elapse = (): void =>
    clock.advance(Math.max(live.changeWait ?? liveChangeWait, live.refetchInterval ?? liveRefetchInterval));
  return { client, transport, feed: new FakeFeed(), page, visibility, clock, elapse, keepalive, warnings };
}

/** Each request as method, URL without the version, the version, and whether it carried the subscribe header. */
function sent(
  requests: TransportRequest[],
): { method: string; url: string; version: string | undefined; subscribed: boolean }[] {
  return requests.map((request) => ({
    method: request.method,
    ...split(request.url),
    subscribed: request.headers?.[subscribeHeader] !== undefined,
  }));
}

const anvil = 'anvil' as Domain;

describe('mintLiveId', () => {
  it('mints 22 base64url characters, fresh each time', () => {
    const ids = new Set<string>();
    for (let i = 0; i < 50; i++) {
      const id = mintLiveId();
      expect(id).toHaveLength(22);
      expect(/^[A-Za-z0-9_-]{22}$/.test(id)).toBe(true);
      ids.add(id);
    }
    expect(ids.size).toBe(50);
  });
});

describe('versionedUrl', () => {
  const cases: { name: string; url: string; version: string; want: string }[] = [
    { name: 'appends to a URL without a query', url: '/api/ships', version: 'seed', want: '/api/ships?_v=seed' },
    {
      name: 'appends to a URL with a query',
      url: '/api/ships?sort=name&cursor=v4.local.x',
      version: '1700000000000001',
      want: '/api/ships?sort=name&cursor=v4.local.x&_v=1700000000000001',
    },
    {
      name: 'replaces a version already there',
      url: '/api/ships?_v=old&limit=2',
      version: 'new',
      want: '/api/ships?limit=2&_v=new',
    },
    { name: 'encodes the version', url: '/api/ships', version: 'a:b', want: '/api/ships?_v=a%3Ab' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(versionedUrl(tt.url, tt.version)).toBe(tt.want);
    });
  }
});

describe('liveSubscription', () => {
  const ships = descriptor.resources['Ships'] as ResourceDescriptor;
  const berths = descriptor.resources['Berths'] as ResourceDescriptor;
  const cases: {
    name: string;
    descriptor: ResourceDescriptor;
    domain?: Domain;
    key?: unknown[];
    want: LiveSubscription;
  }[] = [
    {
      name: 'a domain-scoped list is the resource in the domain',
      descriptor: ships,
      domain: anvil,
      want: { resource: 'Ships', domain: 'anvil' },
    },
    {
      name: 'a global list is the resource alone, no domain field at all',
      descriptor: berths,
      want: { resource: 'Berths' },
    },
    {
      name: 'a row of a domain-scoped resource is the resource and key in the domain it was read in',
      descriptor: ships,
      domain: anvil,
      key: ['s1'],
      want: { resource: 'Ships', key: 's1', domain: 'anvil' },
    },
    {
      name: 'a row of a global resource carries no domain, and a compound key joins its parts in route order',
      descriptor: berths,
      key: ['h1', 3],
      want: { resource: 'Berths', key: 'h1/3' },
    },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      const got = liveSubscription(tt.descriptor, tt.domain, tt.key);
      expect(got).toEqual(tt.want);
      expect(Object.keys(got).sort()).toEqual(Object.keys(tt.want).sort());
    });
  }

  it('sameSubscription compares the resource, the key, and the domain, treating an absent domain as the global one', () => {
    expect(sameSubscription({ resource: 'Ships', key: 's1' }, { resource: 'Ships', key: 's1' })).toBe(true);
    expect(sameSubscription({ resource: 'Ships', key: 's1' }, { resource: 'Ships', key: 's2' })).toBe(false);
    expect(
      sameSubscription(
        { resource: 'Ships', key: 's1', domain: 'anvil' },
        { resource: 'Ships', key: 's1', domain: 'anvil' },
      ),
    ).toBe(true);
    expect(
      sameSubscription(
        { resource: 'Ships', key: 's1', domain: 'anvil' },
        { resource: 'Ships', key: 's1', domain: 'bastion' },
      ),
    ).toBe(false);
    expect(sameSubscription({ resource: 'Ships', key: 's1', domain: 'anvil' }, { resource: 'Ships', key: 's1' })).toBe(
      false,
    );
    expect(sameSubscription({ resource: 'Berths' }, { resource: 'Berths', domain: undefined })).toBe(true);
    expect(sameSubscription({ resource: 'Ships', domain: 'anvil' }, { resource: 'Ships' })).toBe(false);
    expect(sameSubscription(undefined, undefined)).toBe(true);
    expect(sameSubscription(undefined, { resource: 'Ships' })).toBe(false);
  });

  it('a handle answers its own list and row subscriptions', () => {
    const { client } = harness({});
    expect(client.domain(anvil).ships.subscription()).toEqual({ resource: 'Ships', domain: 'anvil' });
    expect(client.domain(anvil).ships.subscription(['s1'])).toEqual({ resource: 'Ships', key: 's1', domain: 'anvil' });
    expect(client.shipClasses.subscription()).toEqual({ resource: 'ShipClasses' });
    expect(client.berths.subscription(['h1', 3])).toEqual({ resource: 'Berths', key: 'h1/3' });
  });
});

describe('the live option before and after the feed starts', () => {
  it('is a plain request until a feed runs, then carries the header and the seed', async () => {
    const h = harness({
      '/api/sectors/anvil/ships': { rows: [{ id: 's1' }] },
      '/api/sectors/anvil/ships/s1': { rows: { id: 's1' } },
    });
    const ships = h.client.domain(anvil).ships;
    expect(h.client.live.enabled).toBe(true);
    expect(h.client.live.active).toBe(false);
    expect(h.warnings).toEqual([]);

    await ships.list({ live: true });
    await ships.read(['s1'], { live: true });
    expect(sent(h.transport.requests)).toEqual([
      { method: 'GET', url: '/api/sectors/anvil/ships', version: undefined, subscribed: false },
      { method: 'GET', url: '/api/sectors/anvil/ships/s1', version: undefined, subscribed: false },
    ]);

    await h.client.live.start(h.feed);
    expect(h.feed.started).toEqual([identity]);
    expect(h.client.live.active).toBe(true);
    // The identity request carries the tab too, so the log line names it.
    expect(sent(h.transport.requests)[2]).toEqual({
      method: 'GET',
      url: '/api/live/token',
      version: undefined,
      subscribed: true,
    });

    await ships.list({ live: true });
    await ships.read(['s1'], { live: true });
    const live = sent(h.transport.requests).slice(3);
    expect(live.map((r) => [r.url, r.subscribed])).toEqual([
      ['/api/sectors/anvil/ships', true],
      ['/api/sectors/anvil/ships/s1', true],
    ]);
    expect(live.map((r) => r.version)).toEqual([h.client.live.seed, h.client.live.seed]);
    expect(h.transport.requests[3].headers).toEqual({ [subscribeHeader]: h.client.live.tab });
    expect(h.client.live.tab).toHaveLength(22);
    expect(h.client.live.seed).toHaveLength(22);

    // A call that did not ask stays plain while the feed runs.
    await ships.list();
    await ships.read(['s1']);
    expect(sent(h.transport.requests).slice(5)).toEqual([
      { method: 'GET', url: '/api/sectors/anvil/ships', version: undefined, subscribed: false },
      { method: 'GET', url: '/api/sectors/anvil/ships/s1', version: undefined, subscribed: false },
    ]);
    await h.client.live.stop();
  });

  it('an API that serves no live keeps a live call plain and refuses to start a feed', async () => {
    const h = harness({ '/api/ship-classes': { rows: [] } }, { ...descriptor, live: undefined });
    expect(h.client.live.enabled).toBe(false);
    await h.client.shipClasses.list({ live: true });
    expect(sent(h.transport.requests)).toEqual([
      { method: 'GET', url: '/api/ship-classes', version: undefined, subscribed: false },
    ]);
    await expect(h.client.live.start(h.feed)).rejects.toThrow('serves no live subscriptions');
    expect(h.feed.started).toEqual([]);
    // The live call and the start announced the absence in the same words, once between them.
    expect(h.warnings).toEqual([noLiveRoutesWarning]);
  });

  it('starting twice starts the feed once', async () => {
    const h = harness({});
    await h.client.live.start(h.feed);
    await h.client.live.start(h.feed);
    expect(h.feed.started).toHaveLength(1);
    expect(h.transport.requests.filter((r) => r.url === '/api/live/token')).toHaveLength(1);
    await h.client.live.stop();
  });
});

describe('dispatch', () => {
  it('refetches the watchers of the row or list an event names by its version, and records the version for later asks', async () => {
    const h = harness({});
    const live = h.client.live;
    const ships = h.client.domain(anvil).ships;
    const refetches: { who: string; version: string; event?: ChangeEvent }[] = [];
    live.watch(ships.subscription(['s1']), (version, event) => refetches.push({ who: 'row s1', version, event }));
    live.watch(ships.subscription(['s2']), (version, event) => refetches.push({ who: 'row s2', version, event }));
    live.watch(ships.subscription(), (version, event) => refetches.push({ who: 'anvil list', version, event }));
    live.watch(h.client.shipClasses.subscription(), (version, event) =>
      refetches.push({ who: 'classes list', version, event }),
    );
    await live.start(h.feed);
    // The start resynced every watcher by the fresh seed.
    expect(refetches.map((r) => [r.who, r.version === live.seed, r.event])).toEqual([
      ['row s1', true, undefined],
      ['row s2', true, undefined],
      ['anvil list', true, undefined],
      ['classes list', true, undefined],
    ]);
    refetches.length = 0;

    const rowChange: ChangeEvent = {
      kind: 'row',
      resource: 'Ships',
      key: 's1',
      at: '1700000000000001',
      deleted: false,
    };
    h.feed.emit(rowChange);
    // Somebody else's change waits before it fetches (the waits have their own suite).
    expect(refetches).toEqual([]);
    h.elapse();
    expect(refetches).toEqual([{ who: 'row s1', version: '1700000000000001', event: rowChange }]);
    expect(live.version(ships.subscription(['s1']))).toBe('1700000000000001');
    expect(live.version(ships.subscription(['s2']))).toBe(live.seed);
    expect(live.version(ships.subscription())).toBe(live.seed);
    refetches.length = 0;

    const listChange: ChangeEvent = { kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000002' };
    h.feed.emit(listChange);
    h.elapse();
    expect(refetches).toEqual([{ who: 'anvil list', version: '1700000000000002', event: listChange }]);
    refetches.length = 0;

    // Another domain's list, and a global list event for a resource nobody watches: no watcher refetches.
    h.feed.emit({ kind: 'list', resource: 'Ships', domain: 'bastion', at: '1700000000000003' });
    h.feed.emit({ kind: 'list', resource: 'Berths', at: '1700000000000004' });
    h.elapse();
    expect(refetches).toEqual([]);
    // The version was recorded anyway, for a page that mounts later.
    expect(live.version({ resource: 'Berths' })).toBe('1700000000000004');

    // A resource event reaches every row and list of the resource and no other.
    const bulk: ChangeEvent = { kind: 'resource', resource: 'Ships', at: '1700000000000005' };
    h.feed.emit(bulk);
    h.elapse();
    expect(refetches.map((r) => [r.who, r.version])).toEqual([
      ['row s1', '1700000000000005'],
      ['row s2', '1700000000000005'],
      ['anvil list', '1700000000000005'],
    ]);
    expect(live.version(ships.subscription(['s2']))).toBe('1700000000000005');
    refetches.length = 0;

    // An older row event after the bulk one does not move the version back.
    h.feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000004' });
    h.elapse();
    expect(refetches.map((r) => [r.who, r.version])).toEqual([['row s1', '1700000000000005']]);
    await live.stop();
  });

  it("a row event, which names no domain, reaches the row's watchers in every domain it was read in, and the row has one version", async () => {
    const h = harness({});
    const live = h.client.live;
    const refetches: { who: string; version: string }[] = [];
    live.watch(h.client.domain(anvil).ships.subscription(['s1']), (version) =>
      refetches.push({ who: 'anvil s1', version }),
    );
    live.watch(h.client.domain('bastion' as Domain).ships.subscription(['s1']), (version) =>
      refetches.push({ who: 'bastion s1', version }),
    );
    live.watch(h.client.domain(anvil).ships.subscription(['s2']), (version) =>
      refetches.push({ who: 'anvil s2', version }),
    );
    await live.start(h.feed);
    refetches.length = 0;
    h.feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000012' });
    h.elapse();
    expect(refetches).toEqual([
      { who: 'anvil s1', version: '1700000000000012' },
      { who: 'bastion s1', version: '1700000000000012' },
    ]);
    expect(live.version({ resource: 'Ships', key: 's1', domain: 'anvil' })).toBe('1700000000000012');
    expect(live.version({ resource: 'Ships', key: 's1', domain: 'bastion' })).toBe('1700000000000012');
    expect(live.version({ resource: 'Ships', key: 's1' })).toBe('1700000000000012');
    expect(live.version({ resource: 'Ships', key: 's2', domain: 'anvil' })).toBe(live.seed);
    // The two are two subscriptions to renew, since the server re-checks Read in each domain.
    expect(live.subscriptions()).toEqual([
      { resource: 'Ships', key: 's1', domain: 'anvil' },
      { resource: 'Ships', key: 's1', domain: 'bastion' },
      { resource: 'Ships', key: 's2', domain: 'anvil' },
    ]);
    await live.stop();
  });

  it('a global list event with no domain reaches the global list watcher', async () => {
    const h = harness({});
    const versions: string[] = [];
    h.client.live.watch(h.client.shipClasses.subscription(), (version) => versions.push(version));
    await h.client.live.start(h.feed);
    versions.length = 0;
    h.feed.emit({ kind: 'list', resource: 'ShipClasses', at: '1700000000000009' });
    h.elapse();
    h.feed.emit({ kind: 'list', resource: 'ShipClasses', domain: '', at: '1700000000000010' });
    h.elapse();
    expect(versions).toEqual(['1700000000000009', '1700000000000010']);
    await h.client.live.stop();
  });

  it('a released watcher is not refetched, and the version seen stays for a remount', async () => {
    const h = harness({});
    const versions: string[] = [];
    const release = h.client.live.watch({ resource: 'Ships', key: 's1' }, (version) => versions.push(version));
    await h.client.live.start(h.feed);
    release();
    h.feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000011' });
    h.elapse();
    expect(versions).toEqual([h.client.live.seed]);
    expect(h.client.live.version({ resource: 'Ships', key: 's1' })).toBe('1700000000000011');
    expect(h.client.live.subscriptions()).toEqual([]);
    await h.client.live.stop();
  });
});

describe('a live request after a change', () => {
  it('a refetch carries the event version, and the page relations carry it too', async () => {
    const first = '/api/sectors/anvil/ships?limit=2&count=true';
    const second = '/api/sectors/anvil/ships?limit=2&cursor=v4.local.two';
    const h = harness({
      [first]: { rows: [{ id: 's1' }, { id: 's2' }], headers: { 'total-count': '3', link: `<${second}>; rel="next"` } },
      [second]: { rows: [{ id: 's3' }], headers: { link: `<${first}>; rel="prev"` } },
    });
    const ships = h.client.domain(anvil).ships;
    await h.client.live.start(h.feed);
    const page = await ships.page({ limit: 2, count: true, live: true });
    const next = await page.next!();
    expect(next.rows).toEqual([{ id: 's3' }]);

    h.client.live.dispatch({ kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000020' });
    await next.reload();
    await next.prev!();
    const requests = sent(h.transport.requests).filter((r) => r.url.startsWith('/api/sectors'));
    expect(requests.map((r) => [r.url, r.version, r.subscribed])).toEqual([
      [first, h.client.live.seed, true],
      [second, h.client.live.seed, true],
      [second, '1700000000000020', true],
      [first, '1700000000000020', true],
    ]);
    await h.client.live.stop();
  });

  it('a plain page walk carries neither the header nor a version, feed or no feed', async () => {
    const first = '/api/sectors/anvil/ships?limit=2';
    const second = '/api/sectors/anvil/ships?limit=2&cursor=v4.local.two';
    const h = harness({
      [first]: { rows: [{ id: 's1' }], headers: { link: `<${second}>; rel="next"` } },
      [second]: { rows: [{ id: 's3' }] },
    });
    await h.client.live.start(h.feed);
    const page = await h.client.domain(anvil).ships.page({ limit: 2 });
    await page.next!();
    expect(sent(h.transport.requests).filter((r) => r.url.startsWith('/api/sectors'))).toEqual([
      { method: 'GET', url: first, version: undefined, subscribed: false },
      { method: 'GET', url: second, version: undefined, subscribed: false },
    ]);
    await h.client.live.stop();
  });
});

describe('parseChangesHeader', () => {
  // The header as the server writes it: the answer's JSON, percent-encoded as Go's
  // url.PathEscape does, which leaves the colon as it is. The server's test pins the
  // same text for the same answer (TestAnswer_header in resource/live).
  const fromTheServer =
    '%7B%22write%22:%22W1%22%2C%22changes%22:%5B%7B%22kind%22:%22list%22%2C%22resource%22:%22Ships%22%2C%22domain%22:%22a%C3%B1vil%22%7D%2C' +
    '%7B%22kind%22:%22row%22%2C%22resource%22:%22Ships%22%2C%22key%22:%22s%2F1%20%5C%22%C3%A9%5C%22%22%2C%22deleted%22:true%7D%2C' +
    '%7B%22kind%22:%22resource%22%2C%22resource%22:%22MissionBoards%22%7D%5D%7D';

  it('reads the write and its changes from the header as the server writes it', () => {
    expect(parseChangesHeader(fromTheServer)).toEqual({
      write: 'W1',
      changes: [
        { kind: 'list', resource: 'Ships', domain: 'añvil' },
        { kind: 'row', resource: 'Ships', key: 's/1 "é"', deleted: true },
        { kind: 'resource', resource: 'MissionBoards' },
      ],
      all: false,
    });
  });

  const cases: { name: string; value: string | undefined; want: ReturnType<typeof parseChangesHeader> }[] = [
    { name: 'no header', value: undefined, want: undefined },
    { name: 'an empty header', value: '', want: undefined },
    {
      name: 'a write with more changes than the header holds says all',
      value: '%7B%22write%22:%22W2%22%2C%22all%22:true%7D',
      want: { write: 'W2', changes: [], all: true },
    },
    { name: 'text that is not percent-encoding', value: '%E0%A4%A', want: undefined },
    { name: 'text that is not JSON', value: 'write=W1', want: undefined },
    { name: 'JSON that is not an object', value: '%22W1%22', want: undefined },
    { name: 'an answer naming no write', value: encodeURIComponent('{"changes":[]}'), want: undefined },
    {
      name: 'a change of a kind this client does not know is left out, the rest kept',
      value: encodeURIComponent(
        '{"write":"W3","changes":[{"kind":"column","resource":"Ships"},{"kind":"list","resource":"Ships"},7,{"kind":"row"}]}',
      ),
      want: { write: 'W3', changes: [{ kind: 'list', resource: 'Ships' }], all: false },
    },
  ];
  for (const tt of cases) {
    it(tt.name, () => {
      expect(parseChangesHeader(tt.value)).toEqual(tt.want);
    });
  }
});

/** What a watcher fetched: who, by which version, for which event, and when on the spec's clock. */
interface Fetch {
  who: string;
  version: string;
  event?: ChangeEvent;
  at: number;
}

/** Watches a subscription on the harness's session, recording every fetch into `fetches` under `who`. */
function watching(h: Harness, fetches: Fetch[], who: string, subscription: LiveSubscription): () => void {
  return h.client.live.watch(subscription, (version, event) => fetches.push({ who, version, event, at: h.clock.time }));
}

const refit = '/api/sectors/anvil/refit';

describe("the tab's own write", () => {
  it('fetches what the answer names at once, by the write, and the echo in the feed fetches nothing', async () => {
    const h = harness({
      [refit]: {
        headers: changesAnswer('w-1', [
          { kind: 'list', resource: 'Ships', domain: 'anvil' },
          { kind: 'row', resource: 'Ships', key: 's1' },
        ]),
      },
    });
    const live = h.client.live;
    const ships = h.client.domain(anvil).ships;
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', ships.subscription());
    watching(h, fetches, 'row s1', ships.subscription(['s1']));
    watching(h, fetches, 'row s2', ships.subscription(['s2']));
    watching(h, fetches, 'bastion list', h.client.domain('bastion' as Domain).ships.subscription());
    watching(h, fetches, 'classes list', h.client.shipClasses.subscription());
    await live.start(h.feed);
    fetches.length = 0;
    h.clock.advance(60_000);

    // The write is answered: the list and the row it changed are fetched before the
    // caller goes on, with no time passing and no timer started.
    await h.client.request('POST', 'sectors/anvil/refit', { body: { refitId: 'r1' } });
    expect(fetches).toEqual([
      {
        who: 'anvil list',
        version: 'w-1',
        event: { kind: 'list', resource: 'Ships', domain: 'anvil', at: 'w-1', write: 'w-1' },
        at: 60_000,
      },
      {
        who: 'row s1',
        version: 'w-1',
        event: { kind: 'row', resource: 'Ships', key: 's1', at: 'w-1', write: 'w-1' },
        at: 60_000,
      },
    ]);
    expect(h.clock.waiting).toBe(0);
    // A request the page makes itself right after asks by the same version.
    expect(live.version(ships.subscription())).toBe('w-1');
    expect(live.version(ships.subscription(['s1']))).toBe('w-1');
    expect(live.version(ships.subscription(['s2']))).toBe(live.seed);
    fetches.length = 0;

    // The feed reports the same write: nothing is fetched, nothing waits, and the
    // version stays the one the page holds its answer under.
    h.feed.emit({ kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000100', write: 'w-1' });
    h.feed.emit({ kind: 'row', resource: 'Ships', key: 's1', deleted: false, at: '1700000000000100', write: 'w-1' });
    expect(h.clock.waiting).toBe(0);
    h.elapse();
    expect(fetches).toEqual([]);
    expect(live.version(ships.subscription())).toBe('w-1');

    // Somebody else's write to the same list afterwards is theirs: it waits, then fetches by its own version.
    h.feed.emit({ kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000200', write: 'w-theirs' });
    h.elapse();
    expect(fetches.map((f) => [f.who, f.version])).toEqual([['anvil list', '1700000000000200']]);
    await live.stop();
  });

  it('an echo that arrives before the answer starts a wait, and the answer ends it with the one fetch', async () => {
    // The server publishes before it answers, and the feed may be the faster of the two.
    const h: Harness = harness({
      [refit]: {
        headers: changesAnswer('w-1', [{ kind: 'list', resource: 'Ships', domain: 'anvil' }]),
        meanwhile: () => {
          h.feed.emit({ kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000100', write: 'w-1' });
          // Not known for the tab's own yet: it waits as anyone's change does.
          expect(h.clock.waiting).toBe(1);
        },
      },
    });
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;

    await h.client.request('POST', 'sectors/anvil/refit', { body: {} });
    expect(fetches.map((f) => [f.who, f.version, f.at])).toEqual([['anvil list', 'w-1', 0]]);
    expect(h.clock.waiting).toBe(0);
    h.elapse();
    expect(fetches).toHaveLength(1);
    await h.client.live.stop();
  });

  it("a document somebody else's write joined within the second carries their write, and fetches", async () => {
    const h = harness({
      [refit]: { headers: changesAnswer('w-mine', [{ kind: 'list', resource: 'Ships', domain: 'anvil' }]) },
    });
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;

    await h.client.request('POST', 'sectors/anvil/refit', { body: {} });
    expect(fetches).toHaveLength(1);
    // One document for the list in that second: first the tab's own write, then, moved
    // forward, the other person's.
    h.feed.emit({ kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000100', write: 'w-mine' });
    h.feed.emit({ kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000700', write: 'w-theirs' });
    h.elapse();
    expect(fetches.map((f) => f.version)).toEqual(['w-mine', '1700000000000700']);
    await h.client.live.stop();
  });

  it("ends the wait the watcher was in for other people's changes: the one fetch shows those too", async () => {
    const h = harness({
      [refit]: { headers: changesAnswer('w-1', [{ kind: 'list', resource: 'Ships', domain: 'anvil' }]) },
    });
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;

    h.feed.emit({ kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000100', write: 'w-theirs' });
    h.clock.advance(500);
    expect(fetches).toEqual([]);
    await h.client.request('POST', 'sectors/anvil/refit', { body: {} });
    expect(fetches.map((f) => [f.version, f.at])).toEqual([['w-1', 500]]);
    h.elapse();
    expect(fetches).toHaveLength(1);
    await h.client.live.stop();
  });

  it('a resource-level change of the write reaches every row and list of the resource, each once', async () => {
    const h = harness({
      [refit]: {
        headers: changesAnswer('w-1', [
          { kind: 'resource', resource: 'Ships' },
          { kind: 'list', resource: 'Ships', domain: 'anvil' },
        ]),
      },
    });
    const ships = h.client.domain(anvil).ships;
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', ships.subscription());
    watching(h, fetches, 'row s1', ships.subscription(['s1']));
    watching(h, fetches, 'classes list', h.client.shipClasses.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;
    await h.client.request('POST', 'sectors/anvil/refit', { body: {} });
    expect(fetches.map((f) => [f.who, f.version])).toEqual([
      ['anvil list', 'w-1'],
      ['row s1', 'w-1'],
    ]);
    await h.client.live.stop();
  });

  it('an answer that says all fetches everything the tab holds live by a fresh seed, and its echo fetches nothing', async () => {
    const h = harness({ [refit]: { headers: changesAnswer('w-1', [], true) } });
    const live = h.client.live;
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    watching(h, fetches, 'classes list', h.client.shipClasses.subscription());
    await live.start(h.feed);
    const seedBefore = live.seed;
    fetches.length = 0;

    await h.client.request('POST', 'sectors/anvil/refit', { body: {} });
    expect(live.seed).not.toBe(seedBefore);
    expect(fetches.map((f) => [f.who, f.version, f.event])).toEqual([
      ['anvil list', live.seed, undefined],
      ['classes list', live.seed, undefined],
    ]);
    h.feed.emit({ kind: 'resource', resource: 'Ships', at: '1700000000000100', write: 'w-1' });
    h.elapse();
    expect(fetches).toHaveLength(2);
    await live.stop();
  });

  it('an answer naming nothing the tab watches fetches nothing, and the version is taken for a page mounted later', async () => {
    const h = harness({
      [refit]: { headers: changesAnswer('w-1', [{ kind: 'list', resource: 'Ships', domain: 'bastion' }]) },
    });
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;
    await h.client.request('POST', 'sectors/anvil/refit', { body: {} });
    expect(fetches).toEqual([]);
    expect(h.client.live.version({ resource: 'Ships', domain: 'bastion' })).toBe('w-1');
    await h.client.live.stop();
  });

  it("a write answered with no header fetches nothing at once: the feed reports it and the page waits as for anyone's", async () => {
    const h = harness({ [refit]: {} });
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;
    await h.client.request('POST', 'sectors/anvil/refit', { body: {} });
    expect(fetches).toEqual([]);
    h.feed.emit({ kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000100', write: 'w-1' });
    h.clock.advance(liveChangeWait);
    expect(fetches.map((f) => f.version)).toEqual(['1700000000000100']);
    await h.client.live.stop();
  });

  it("before a feed runs the pages are plain, and a write's answer fetches nothing", async () => {
    const h = harness({
      [refit]: { headers: changesAnswer('w-1', [{ kind: 'list', resource: 'Ships', domain: 'anvil' }]) },
    });
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.request('POST', 'sectors/anvil/refit', { body: {} });
    expect(fetches).toEqual([]);
    expect(h.client.live.version({ resource: 'Ships', domain: 'anvil' })).toBe(h.client.live.seed);
  });
});

describe("other people's changes", () => {
  const listChange = (n: number): ChangeEvent => ({
    kind: 'list',
    resource: 'Ships',
    domain: 'anvil',
    at: String(1_700_000_000_000_000 + n),
    write: `w-${n}`,
  });

  it('ten changes inside one second fetch once, two seconds after the first, by the last one', async () => {
    const h = harness({});
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;
    h.clock.advance(10_000);

    // The first at 10 000, the tenth at 10 900.
    for (let n = 0; n < 10; n++) {
      h.feed.emit(listChange(n));
      if (n < 9) {
        h.clock.advance(100);
      }
    }
    expect(h.clock.time).toBe(10_900);
    expect(fetches).toEqual([]);
    h.clock.advance(1_099);
    expect(fetches).toEqual([]);
    h.clock.advance(1);
    expect(fetches).toEqual([{ who: 'anvil list', version: listChange(9).at, event: listChange(9), at: 12_000 }]);
    // Nothing more follows on its own.
    h.clock.advance(60_000);
    expect(fetches).toHaveLength(1);
    expect(h.clock.waiting).toBe(0);
    await h.client.live.stop();
  });

  it('changes that never stop fetch once every five seconds', async () => {
    const h = harness({});
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;

    // A change every half second for half a minute.
    for (let n = 0; n < 60; n++) {
      h.feed.emit(listChange(n));
      h.clock.advance(500);
    }
    expect(fetches.map((f) => f.at)).toEqual([2_000, 7_000, 12_000, 17_000, 22_000, 27_000]);
    // Each fetch carries the latest change heard when it was made.
    expect(fetches.map((f) => f.version)).toEqual([3, 13, 23, 33, 43, 53].map((n) => listChange(n).at));
    await h.client.live.stop();
  });

  it('a change after a quiet stretch waits two seconds again, not five', async () => {
    const h = harness({});
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;

    h.feed.emit(listChange(0));
    h.clock.advance(30_000);
    h.feed.emit(listChange(1));
    h.clock.advance(30_000);
    expect(fetches.map((f) => f.at)).toEqual([2_000, 32_000]);
    await h.client.live.stop();
  });

  it('each row and list waits on its own', async () => {
    const h = harness({});
    const ships = h.client.domain(anvil).ships;
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', ships.subscription());
    watching(h, fetches, 'row s1', ships.subscription(['s1']));
    await h.client.live.start(h.feed);
    fetches.length = 0;

    h.feed.emit(listChange(0));
    h.clock.advance(1_500);
    h.feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000900', write: 'w-row' });
    h.clock.advance(5_000);
    expect(fetches.map((f) => [f.who, f.at])).toEqual([
      ['anvil list', 2_000],
      ['row s1', 3_500],
    ]);
    await h.client.live.stop();
  });

  it("the tab's own write in the middle is fetched at once and does not move the next fetch for other people's changes", async () => {
    const h = harness({
      [refit]: { headers: changesAnswer('w-mine', [{ kind: 'list', resource: 'Ships', domain: 'anvil' }]) },
    });
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;

    h.feed.emit(listChange(0));
    h.clock.advance(2_500);
    await h.client.request('POST', 'sectors/anvil/refit', { body: {} });
    h.clock.advance(500);
    h.feed.emit(listChange(1));
    h.clock.advance(10_000);
    expect(fetches.map((f) => [f.version, f.at])).toEqual([
      [listChange(0).at, 2_000],
      ['w-mine', 2_500],
      [listChange(1).at, 7_000],
    ]);
    await h.client.live.stop();
  });

  it('a watcher released while it waits is not fetched, and its timer is gone', async () => {
    const h = harness({});
    const fetches: Fetch[] = [];
    const release = watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;
    h.feed.emit(listChange(0));
    expect(h.clock.waiting).toBe(1);
    release();
    expect(h.clock.waiting).toBe(0);
    h.elapse();
    expect(fetches).toEqual([]);
    await h.client.live.stop();
  });

  it("an application's own times are honored", async () => {
    const h = harness({}, descriptor, { changeWait: 300, refetchInterval: 1_000 });
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;

    // A burst: one fetch, 300 ms after the first.
    h.feed.emit(listChange(0));
    h.clock.advance(100);
    h.feed.emit(listChange(1));
    h.clock.advance(199);
    expect(fetches).toEqual([]);
    h.clock.advance(1);
    expect(fetches.map((f) => [f.version, f.at])).toEqual([[listChange(1).at, 300]]);
    fetches.length = 0;

    // A stream, a change every 100 ms from 300 to 4 000: once every second.
    for (let n = 2; n < 40; n++) {
      h.feed.emit(listChange(n));
      h.clock.advance(100);
    }
    expect(h.clock.time).toBe(4_100);
    expect(fetches.map((f) => f.at)).toEqual([1_300, 2_300, 3_300]);
    await h.client.live.stop();
  });

  it('the defaults are two seconds and five', () => {
    expect(liveChangeWait).toBe(2_000);
    expect(liveRefetchInterval).toBe(5_000);
  });
});

describe('a hidden tab', () => {
  const listChange = (n: number): ChangeEvent => ({
    kind: 'list',
    resource: 'Ships',
    domain: 'anvil',
    at: String(1_700_000_000_000_000 + n),
    write: `w-${n}`,
  });

  it('fetches nothing while hidden, and once when it is shown again', async () => {
    const h = harness({});
    const ships = h.client.domain(anvil).ships;
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', ships.subscription());
    watching(h, fetches, 'row s1', ships.subscription(['s1']));
    await h.client.live.start(h.feed);
    fetches.length = 0;

    h.visibility.become('hidden');
    for (let n = 0; n < 20; n++) {
      h.feed.emit(listChange(n));
      h.clock.advance(3_000);
    }
    expect(fetches).toEqual([]);
    expect(h.clock.waiting).toBe(0);

    // Shown: the list, which changed, fetches once, by the last change; the row, which did not, does not.
    h.visibility.become('visible');
    expect(fetches).toEqual([{ who: 'anvil list', version: listChange(19).at, event: listChange(19), at: 60_000 }]);
    h.elapse();
    expect(fetches).toHaveLength(1);
    await h.client.live.stop();
  });

  it('shown again with nothing changed fetches nothing', async () => {
    const h = harness({});
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;
    h.visibility.become('hidden');
    h.clock.advance(60_000);
    h.visibility.become('visible');
    h.elapse();
    expect(fetches).toEqual([]);
    await h.client.live.stop();
  });

  it('hidden before its wait ended: nothing at the end of the wait, one fetch when shown', async () => {
    const h = harness({});
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;

    h.feed.emit(listChange(0));
    h.clock.advance(1_000);
    h.visibility.become('hidden');
    h.clock.advance(10_000);
    h.feed.emit(listChange(1));
    h.clock.advance(10_000);
    expect(fetches).toEqual([]);
    h.visibility.become('visible');
    expect(fetches.map((f) => [f.version, f.at])).toEqual([[listChange(1).at, 21_000]]);
    await h.client.live.stop();
  });

  it("the tab's own write answered while hidden waits for the tab to be shown too", async () => {
    const h = harness({
      [refit]: { headers: changesAnswer('w-1', [{ kind: 'list', resource: 'Ships', domain: 'anvil' }]) },
    });
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await h.client.live.start(h.feed);
    fetches.length = 0;

    h.visibility.become('hidden');
    await h.client.request('POST', 'sectors/anvil/refit', { body: {} });
    h.elapse();
    expect(fetches).toEqual([]);
    h.visibility.become('visible');
    expect(fetches.map((f) => f.version)).toEqual(['w-1']);
    await h.client.live.stop();
  });

  it('a resync while hidden fetches when the tab is shown, by the fresh seed, once', async () => {
    const h = harness({});
    const live = h.client.live;
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    await live.start(h.feed);
    fetches.length = 0;

    h.visibility.become('hidden');
    h.feed.emit(listChange(0));
    h.feed.resync();
    expect(fetches).toEqual([]);
    h.visibility.become('visible');
    expect(fetches.map((f) => [f.version, f.event])).toEqual([[live.seed, undefined]]);
    await live.stop();
  });

  it('a session started in a hidden tab makes its pages live when the tab is first shown', async () => {
    const h = harness({});
    const live = h.client.live;
    const fetches: Fetch[] = [];
    watching(h, fetches, 'anvil list', h.client.domain(anvil).ships.subscription());
    h.visibility.become('hidden');
    await live.start(h.feed);
    expect(fetches).toEqual([]);
    h.visibility.become('visible');
    expect(fetches.map((f) => f.version)).toEqual([live.seed]);
    await live.stop();
  });

  it('stops listening to the tab when the session stops', async () => {
    const h = harness({});
    await h.client.live.start(h.feed);
    expect(h.visibility.listeners.size).toBe(1);
    await h.client.live.stop();
    expect(h.visibility.listeners.size).toBe(0);
  });
});

describe('renew', () => {
  it('sends the tab and its current subscriptions, drops what the server drops, and takes a dropped one back when watched anew', async () => {
    const h = harness({});
    const transport = scriptedTransport((request) => {
      if (request.url === '/api/live/token') {
        return { status: 200, body: identity };
      }
      if (request.url === '/api/live/renew') {
        const body = request.body as { subscriptions: LiveSubscription[] };
        return {
          status: 200,
          body: {
            kept: body.subscriptions.filter((s) => s.key !== 's2'),
            dropped: body.subscriptions.filter((s) => s.key === 's2'),
            expiresAt: '2026-10-02T12:05:00Z',
          },
        };
      }
      return { status: 204, body: undefined };
    });
    const client = createClient<Global, Sector>(descriptor, {
      baseUrl: '/api',
      timeZone: '',
      transport,
      live: { renewInterval: 60 * 60 * 1000, page: h.page },
    });
    const ships = client.domain(anvil).ships;

    // Nothing runs before the feed does, and nothing is sent with nothing watched.
    expect(await client.live.renew()).toBeUndefined();
    await client.live.start(h.feed);
    expect(await client.live.renew()).toBeUndefined();

    const releaseS1 = client.live.watch(ships.subscription(['s1']), () => undefined);
    client.live.watch(ships.subscription(['s1']), () => undefined);
    const releaseS2 = client.live.watch(ships.subscription(['s2']), () => undefined);
    client.live.watch(ships.subscription(), () => undefined);
    client.live.watch(client.shipClasses.subscription(), () => undefined);

    const answer = await client.live.renew();
    const renew = transport.requests.find((r) => r.url === '/api/live/renew')!;
    expect(renew.method).toBe('POST');
    expect(renew.headers).toEqual({ [subscribeHeader]: client.live.tab });
    expect(renew.body).toEqual({
      tab: client.live.tab,
      subscriptions: [
        { resource: 'Ships', key: 's1', domain: 'anvil' },
        { resource: 'Ships', key: 's2', domain: 'anvil' },
        { resource: 'Ships', domain: 'anvil' },
        { resource: 'ShipClasses' },
      ],
    });
    expect(JSON.stringify(renew.body)).not.toContain('"domain":""');
    // Each subscription carries the three fields the server knows and nothing else.
    expect(
      (renew.body as { subscriptions: object[] }).subscriptions.every((s) =>
        Object.keys(s).every((k) => ['resource', 'key', 'domain'].includes(k)),
      ),
    ).toBe(true);
    expect(answer?.dropped).toEqual([{ resource: 'Ships', key: 's2', domain: 'anvil' }]);
    expect(answer?.expiresAt).toBe('2026-10-02T12:05:00Z');

    // The dropped row is not renewed again; the first watcher's release keeps s1 current through the second.
    releaseS1();
    expect(client.live.subscriptions()).toEqual([
      { resource: 'Ships', key: 's1', domain: 'anvil' },
      { resource: 'Ships', domain: 'anvil' },
      { resource: 'ShipClasses' },
    ]);

    // Watched anew, the dropped row is asked for again.
    releaseS2();
    client.live.watch(ships.subscription(['s2']), () => undefined);
    expect(
      client.live
        .subscriptions()
        .map((s) => s.key ?? s.domain ?? '')
        .sort(),
    ).toEqual(['', 'anvil', 's1', 's2']);
    await client.live.stop();
  });

  it("a renewal carries at most the server's limit, and a subscription with fields of its own is sent as the three the server knows", async () => {
    const h = harness({});
    await h.client.live.start(h.feed);
    for (let i = 0; i < liveRenewLimit + 5; i++) {
      h.client.live.watch({ resource: 'ShipClasses', key: `c${i}` }, () => undefined);
    }
    h.client.live.watch({ resource: 'Berths', key: 'b1', note: 'mine' } as LiveSubscription, () => undefined);
    await h.client.live.renew();
    const renew = h.transport.requests.find((r) => r.url === '/api/live/renew')!;
    const body = renew.body as { subscriptions: LiveSubscription[] };
    expect(body.subscriptions).toHaveLength(liveRenewLimit);
    expect(h.client.live.subscriptions()).toHaveLength(liveRenewLimit + 6);
    expect(h.client.live.subscriptions().at(-1)).toEqual({ resource: 'Berths', key: 'b1' });
    await h.client.live.stop();
  });

  it('the renew timer runs on the interval', async () => {
    const transport = scripted({});
    const client = createClient<Global, Sector>(descriptor, {
      baseUrl: '/api',
      timeZone: '',
      transport,
      live: { renewInterval: 20, page: new FakePage() },
    });
    const feed = new FakeFeed();
    client.live.watch({ resource: 'ShipClasses' }, () => undefined);
    await client.live.start(feed);
    await new Promise((resolve) => setTimeout(resolve, 70));
    await client.live.stop();
    const renewals = transport.requests.filter((r) => r.url === '/api/live/renew').length;
    expect(renewals >= 2).toBe(true);
    // Stopped, nothing more is sent.
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(transport.requests.filter((r) => r.url === '/api/live/renew').length).toBe(renewals);
  });
});

describe('resync', () => {
  it('mints a fresh seed, forgets every version seen, and refetches every watcher by the seed', async () => {
    const h = harness({});
    const live = h.client.live;
    const refetches: { who: string; version: string; event?: ChangeEvent }[] = [];
    live.watch({ resource: 'Ships', key: 's1' }, (version, event) => refetches.push({ who: 'row', version, event }));
    live.watch({ resource: 'Ships', domain: 'anvil' }, (version, event) =>
      refetches.push({ who: 'list', version, event }),
    );
    await live.start(h.feed);
    const seedAtStart = live.seed;
    h.feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000030' });
    refetches.length = 0;

    h.feed.resync();
    expect(live.seed).not.toBe(seedAtStart);
    expect(refetches).toEqual([
      { who: 'row', version: live.seed, event: undefined },
      { who: 'list', version: live.seed, event: undefined },
    ]);
    expect(live.version({ resource: 'Ships', key: 's1' })).toBe(live.seed);
    await live.stop();
  });
});

describe('leaving and logging out', () => {
  it('pagehide sends the tab unsubscribe with a keepalive fetch, and a restored page renews at once', async () => {
    const h = harness({});
    h.client.live.watch({ resource: 'ShipClasses' }, () => undefined);
    // Before the feed runs, leaving sends nothing: nothing was subscribed.
    h.page.fire('pagehide');
    expect(h.keepalive).toEqual([]);

    await h.client.live.start(h.feed);
    h.page.fire('pagehide');
    expect(h.keepalive).toHaveLength(1);
    const [left] = h.keepalive;
    expect(left.url).toBe('/api/live/unsubscribe');
    expect(left.init.method).toBe('POST');
    expect(left.init.keepalive).toBe(true);
    expect(left.init.credentials).toBe('same-origin');
    expect(JSON.parse(left.init.body as string)).toEqual({ tab: h.client.live.tab, all: false });
    expect((left.init.headers as Record<string, string>)[subscribeHeader]).toBe(h.client.live.tab);
    expect((left.init.headers as Record<string, string>)['Content-Type']).toBe('application/json');

    const renewsBefore = h.transport.requests.filter((r) => r.url === '/api/live/renew').length;
    h.page.fire('pageshow', { persisted: true });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(h.transport.requests.filter((r) => r.url === '/api/live/renew')).toHaveLength(renewsBefore + 1);
    h.page.fire('pageshow', { persisted: false });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(h.transport.requests.filter((r) => r.url === '/api/live/renew')).toHaveLength(renewsBefore + 1);
    await h.client.live.stop();
  });

  const echoes: { name: string; live: Partial<LiveOptions>; cookie?: string; want: Record<string, string> }[] = [
    {
      name: "a token source answers the token, sent under the server's header",
      live: { xsrf: { token: () => 'from-the-extractor' } },
      want: { 'X-XSRF-TOKEN': 'from-the-extractor' },
    },
    {
      name: 'a token source may name the header',
      live: { xsrf: { token: () => 'from-the-extractor', headerName: 'X-Staff-XSRF' } },
      want: { 'X-Staff-XSRF': 'from-the-extractor' },
    },
    { name: 'a token source answering none sends no header', live: { xsrf: { token: () => null } }, want: {} },
    {
      name: 'cookie names read the cookie',
      live: { xsrf: { cookieName: 'staff-xsrf', headerName: 'X-XSRF-TOKEN' } },
      cookie: 'staff-xsrf=from-the-cookie',
      want: { 'X-XSRF-TOKEN': 'from-the-cookie' },
    },
    { name: 'false sends no header', live: { xsrf: false }, want: {} },
  ];
  for (const tt of echoes) {
    it(`the page-leave unsubscribe's XSRF echo: ${tt.name}`, async () => {
      // The cookie reader looks at the document's cookies; the test runner has no document.
      const page = globalThis as { document?: { cookie: string } };
      if (tt.cookie) {
        page.document = { cookie: tt.cookie };
      }
      try {
        const h = harness({}, descriptor, tt.live);
        h.client.live.watch({ resource: 'ShipClasses' }, () => undefined);
        await h.client.live.start(h.feed);
        h.page.fire('pagehide');
        expect(h.keepalive).toHaveLength(1);
        const headers = h.keepalive[0].init.headers as Record<string, string>;
        const xsrfHeaders = Object.fromEntries(Object.entries(headers).filter(([name]) => /xsrf/i.test(name)));
        expect(xsrfHeaders).toEqual(tt.want);
        await h.client.live.stop();
      } finally {
        if (tt.cookie) {
          delete page.document;
        }
      }
    });
  }

  it('stop with the session gone sends no unsubscribe, and still stops the feed, reseeds, and stops listening', async () => {
    const h = harness({});
    const live = h.client.live;
    live.watch({ resource: 'Ships', key: 's1' }, () => undefined);
    await live.start(h.feed);
    const seedBefore = live.seed;

    await live.stop({ sessionGone: true });
    expect(h.transport.requests.filter((r) => r.url === '/api/live/unsubscribe')).toEqual([]);
    expect(h.feed.stopped).toBe(1);
    expect(h.feed.listening).toBe(0);
    expect(live.started).toBe(false);
    expect(live.seed).not.toBe(seedBefore);
    h.page.fire('pagehide');
    expect(h.keepalive).toEqual([]);
  });

  it('stop unsubscribes everything, stops the feed, reseeds, and stops listening to the page', async () => {
    const h = harness({});
    const live = h.client.live;
    live.watch({ resource: 'Ships', key: 's1' }, () => undefined);
    await live.start(h.feed);
    h.feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000040' });
    const seedBefore = live.seed;
    expect(h.feed.listening).toBe(2);

    await live.stop();
    const unsubscribe = h.transport.requests.find((r) => r.url === '/api/live/unsubscribe')!;
    expect(unsubscribe.method).toBe('POST');
    expect(unsubscribe.body).toEqual({ tab: live.tab, all: true });
    expect(unsubscribe.headers).toEqual({ [subscribeHeader]: live.tab });
    expect(h.feed.stopped).toBe(1);
    expect(h.feed.listening).toBe(0);
    expect(live.started).toBe(false);
    expect(live.active).toBe(false);
    expect(live.seed).not.toBe(seedBefore);
    expect(live.version({ resource: 'Ships', key: 's1' })).toBe(live.seed);
    // The page listeners are gone: a later pagehide sends nothing.
    h.page.fire('pagehide');
    expect(h.keepalive).toEqual([]);
    // A feed event after the stop reaches nothing.
    h.feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000041' });
    expect(live.version({ resource: 'Ships', key: 's1' })).toBe(live.seed);
  });

  it('stop before any start sends nothing and still reseeds', async () => {
    const h = harness({});
    const seedBefore = h.client.live.seed;
    await h.client.live.stop();
    expect(h.transport.requests).toEqual([]);
    expect(h.client.live.seed).not.toBe(seedBefore);
  });

  it('a refused unsubscribe still stops the feed', async () => {
    const transport = scriptedTransport((request) =>
      request.url === '/api/live/token'
        ? { status: 200, body: identity }
        : { status: 401, body: { message: 'session gone' } },
    );
    const errors: number[] = [];
    const client = createClient<Global, Sector>(descriptor, {
      baseUrl: '/api',
      timeZone: '',
      transport,
      onError: (error) => errors.push(error.status),
      live: { renewInterval: 60 * 60 * 1000, page: new FakePage() },
    });
    const feed = new FakeFeed();
    await client.live.start(feed);
    await client.live.stop();
    expect(errors).toEqual([401]);
    expect(feed.stopped).toBe(1);
    expect(client.live.started).toBe(false);
  });
});

describe('pageIsLive', () => {
  // The metadata decides the default; the page's own word declines, or asks where the
  // metadata is silent; asking of a resource whose changes are not published is announced.
  const cases: { name: string; choice?: boolean; meta?: { live?: boolean }; want: boolean; announced: boolean }[] = [
    { name: 'published, no choice: live', meta: { live: true }, want: true, announced: false },
    { name: 'published, asked: live', choice: true, meta: { live: true }, want: true, announced: false },
    { name: 'published, declined: plain', choice: false, meta: { live: true }, want: false, announced: false },
    { name: 'not published, no choice: plain, nothing said', meta: { live: false }, want: false, announced: false },
    {
      name: 'not published, asked: plain, announced',
      choice: true,
      meta: { live: false },
      want: false,
      announced: true,
    },
    {
      name: 'not published, declined: plain, nothing said',
      choice: false,
      meta: { live: false },
      want: false,
      announced: false,
    },
    { name: 'metadata without the field, no choice: plain', meta: {}, want: false, announced: false },
    { name: 'metadata without the field, asked: live', choice: true, meta: {}, want: true, announced: false },
    { name: 'no metadata yet, asked: live', choice: true, want: true, announced: false },
  ];
  for (const tt of cases) {
    it(tt.name, () => {
      const warnings: string[] = [];
      expect(pageIsLive('Ships', tt.choice, tt.meta, (message) => warnings.push(message))).toBe(tt.want);
      expect(warnings).toEqual(tt.announced ? [unpublishedLiveWarning('Ships')] : []);
    });
  }
});
