import { describe, expect, it } from 'bun:test';
import { Domain, Resource } from './brands';
import { AnyResourceHandle, createClient, Client } from './client';
import { ApiDescriptor, ResourceDescriptor } from './descriptor';
import {
  ChangeEvent,
  ChangeFeed,
  LiveIdentity,
  LiveOptions,
  liveRenewLimit,
  LiveSubscription,
  liveSubscription,
  mintLiveId,
  noLiveRoutesWarning,
  PageEvents,
  pageIsLive,
  sameSubscription,
  subscribeHeader,
  unpublishedLiveWarning,
  versionedUrl,
} from './live';
import { TransportRequest } from './transport';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';

// The tab's live session: a list or read asked to be live carries the tab's subscribe
// header and the version parameter only while a feed runs; the feed's events reach the
// watchers of the row or list they name and refetch by the event's version; renewal
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

/** A transport scripted by URL with the version parameter set aside; the live routes answer themselves. */
function scripted(
  pages: Record<string, { rows?: unknown; status?: number; headers?: Record<string, string> }>,
): ScriptedTransport {
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
  keepalive: { url: string; init: RequestInit }[];
}

function harness(
  pages: Record<string, { rows?: unknown; status?: number; headers?: Record<string, string> }>,
  api: ApiDescriptor = descriptor,
  live: Partial<LiveOptions> = {},
): Harness {
  const transport = scripted(pages);
  const page = new FakePage();
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
    live: { renewInterval: 60 * 60 * 1000, page, fetch: fetchStandIn, xsrf: false, ...live },
  });
  return { client, transport, feed: new FakeFeed(), page, keepalive, warnings };
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
    expect(refetches).toEqual([{ who: 'row s1', version: '1700000000000001', event: rowChange }]);
    expect(live.version(ships.subscription(['s1']))).toBe('1700000000000001');
    expect(live.version(ships.subscription(['s2']))).toBe(live.seed);
    expect(live.version(ships.subscription())).toBe(live.seed);
    refetches.length = 0;

    const listChange: ChangeEvent = { kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000002' };
    h.feed.emit(listChange);
    expect(refetches).toEqual([{ who: 'anvil list', version: '1700000000000002', event: listChange }]);
    refetches.length = 0;

    // Another domain's list, and a global list event for a resource nobody watches: no watcher refetches.
    h.feed.emit({ kind: 'list', resource: 'Ships', domain: 'bastion', at: '1700000000000003' });
    h.feed.emit({ kind: 'list', resource: 'Berths', at: '1700000000000004' });
    expect(refetches).toEqual([]);
    // The version was recorded anyway, for a page that mounts later.
    expect(live.version({ resource: 'Berths' })).toBe('1700000000000004');

    // A resource event reaches every row and list of the resource and no other.
    const bulk: ChangeEvent = { kind: 'resource', resource: 'Ships', at: '1700000000000005' };
    h.feed.emit(bulk);
    expect(refetches.map((r) => [r.who, r.version])).toEqual([
      ['row s1', '1700000000000005'],
      ['row s2', '1700000000000005'],
      ['anvil list', '1700000000000005'],
    ]);
    expect(live.version(ships.subscription(['s2']))).toBe('1700000000000005');
    refetches.length = 0;

    // An older row event after the bulk one does not move the version back.
    h.feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000004' });
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
    h.feed.emit({ kind: 'list', resource: 'ShipClasses', domain: '', at: '1700000000000010' });
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
