import { signal, WritableSignal } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { ColumnConfig, RESOURCE_DOMAIN } from '@impulseframework/resource-angular/types';
import {
  ApiDescriptor,
  ChangeEvent,
  ChangeFeed,
  ClientBase,
  createClient,
  Domain,
  LiveIdentity,
  Resource,
  subscribeHeader,
  Transport,
  TransportRequest,
  unpublishedLiveWarning,
} from '@impulseframework/resource';
import { ResourceStore } from './resource-store.service';

// The store's live pages over a fake feed: a live row or list page asks plainly until the
// client's live session runs, then carries the tab's subscribe header and the version;
// a change the feed reports to the row, or to the resource in the tenant, refetches it by
// the change's version; a change elsewhere refetches nothing; a resync refetches by a
// fresh seed; the tenant moving moves the list's subscription; and the store's hold on the
// live session ends with the store.

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
  },
  methods: {},
};

const identity: LiveIdentity = {
  uid: 'u-7',
  token: '',
  project: 'lab',
  database: 'live',
  apiKey: '',
  emulator: 'localhost:8080',
};

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
}

/** The URL without its version parameter, and the version it carried. */
function split(url: string): { url: string; version: string | undefined } {
  const mark = url.indexOf('?');
  if (mark === -1) {
    return { url, version: undefined };
  }
  const parts = url.slice(mark + 1).split('&');
  const versionPart = parts.find((part) => part.startsWith('_v='));
  const rest = parts.filter((part) => !part.startsWith('_v='));
  return {
    url: rest.length > 0 ? `${url.slice(0, mark)}?${rest.join('&')}` : url.slice(0, mark),
    version: versionPart ? decodeURIComponent(versionPart.slice(3)) : undefined,
  };
}

interface Scripted {
  transport: Transport;
  requests: TransportRequest[];
}

/** A transport scripted by URL with the version set aside; the live routes answer themselves. */
function scripted(pages: Record<string, { rows: unknown; headers?: Record<string, string> }>): Scripted {
  const requests: TransportRequest[] = [];
  const transport: Transport = async (request) => {
    requests.push(request);
    if (request.url === '/api/live/token') {
      return { status: 200, body: identity };
    }
    if (request.url === '/api/live/renew') {
      return { status: 200, body: { kept: [], dropped: [], expiresAt: '' } };
    }
    if (request.url === '/api/live/unsubscribe') {
      return { status: 204, body: undefined };
    }
    const page = pages[split(request.url).url];
    if (!page) {
      return { status: 404, body: { message: `unscripted ${request.url}` } };
    }
    return { status: 200, body: page.rows, headers: page.headers ?? {} };
  };
  return { transport, requests };
}

/** The data requests as URL without the version, the version, and whether the subscribe header rode along. */
function dataRequests(
  requests: TransportRequest[],
): { url: string; version: string | undefined; subscribed: boolean }[] {
  return requests
    .filter((r) => !r.url.startsWith('/api/live/'))
    .map((r) => ({ ...split(r.url), subscribed: r.headers?.[subscribeHeader] !== undefined }));
}

/** Runs change detection and lets pending microtasks land until `done` answers true, for at most `rounds` rounds. */
async function settle(done: () => boolean, rounds = 50): Promise<void> {
  for (let i = 0; i < rounds && !done(); i++) {
    TestBed.tick();
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  TestBed.tick();
}

function storeOver(
  script: Scripted,
  domain: WritableSignal<Domain | undefined> = signal('anvil' as Domain),
  warnings: string[] = [],
): { store: ResourceStore; client: ClientBase; feed: FakeFeed } {
  const client = createClient(descriptor, {
    baseUrl: '/api',
    transport: script.transport,
    warn: (message) => warnings.push(message),
    live: {
      renewInterval: 60 * 60 * 1000,
      page: { addEventListener: () => undefined, removeEventListener: () => undefined },
    },
  });
  TestBed.configureTestingModule({
    providers: [
      provideRouter([]),
      ResourceStore,
      { provide: RESOURCE_CLIENT, useValue: client },
      { provide: RESOURCE_DOMAIN, useValue: domain },
    ],
  });
  return { store: TestBed.inject(ResourceStore), client, feed: new FakeFeed() };
}

const s1 = '/api/sectors/anvil/ships/s1?capabilities=Update%2CDelete';
const anvilPage = '/api/sectors/anvil/ships?columns=name&count=true';
const bastionPage = '/api/sectors/bastion/ships?columns=name&count=true';

describe('ResourceStore live row', () => {
  it('asks plainly until the feed runs, then by the seed, then by each change to the row, and by a fresh seed on a resync', async () => {
    const script = scripted({ [s1]: { rows: { id: 's1', name: 'Kestrel' } } });
    const { store, client, feed } = storeOver(script);
    store.resourceMeta.set({ route: 'sectors/{sectorID}/ships', fields: [] });
    store.liveChoice.set(true);
    store.uuid.set('s1');
    store.buildStoreViewData();
    await settle(() => store.rowPresent());
    expect(dataRequests(script.requests)).toEqual([{ url: s1, version: undefined, subscribed: false }]);
    expect(client.live.subscriptions()).toEqual([{ resource: 'Ships', key: 's1', domain: 'anvil' }]);

    // The feed starts: the row is asked again, live, by the seed.
    await client.live.start(feed);
    await settle(() => script.requests.length >= 3 && store.viewStatus() === 'resolved');
    expect(feed.started).toEqual([identity]);
    expect(dataRequests(script.requests)[1]).toEqual({ url: s1, version: client.live.seed, subscribed: true });

    // A change to this row: asked again by the change's version, the row kept on screen meanwhile.
    feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000001' });
    TestBed.tick();
    expect(store.rowPresent()).toBe(true);
    await settle(() => script.requests.length >= 4 && store.viewStatus() === 'resolved');
    expect(dataRequests(script.requests)[2]).toEqual({ url: s1, version: '1700000000000001', subscribed: true });

    // A change to another row, and to the list: nothing.
    feed.emit({ kind: 'row', resource: 'Ships', key: 's2', at: '1700000000000002' });
    feed.emit({ kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000003' });
    await settle(() => false, 5);
    expect(dataRequests(script.requests)).toHaveLength(3);

    // A resource-level change reaches the row.
    feed.emit({ kind: 'resource', resource: 'Ships', at: '1700000000000004' });
    await settle(() => script.requests.length >= 5 && store.viewStatus() === 'resolved');
    expect(dataRequests(script.requests)[3]).toEqual({ url: s1, version: '1700000000000004', subscribed: true });

    // A resync: a fresh seed, and the row asked by it.
    const seedBefore = client.live.seed;
    feed.resync();
    await settle(() => script.requests.length >= 6 && store.viewStatus() === 'resolved');
    expect(client.live.seed).not.toBe(seedBefore);
    expect(dataRequests(script.requests)[4]).toEqual({ url: s1, version: client.live.seed, subscribed: true });
    await client.live.stop();
  });

  it('a row that did not ask stays plain while the feed runs, and holds nothing on the live session', async () => {
    const script = scripted({ [s1]: { rows: { id: 's1', name: 'Kestrel' } } });
    const { store, client, feed } = storeOver(script);
    store.resourceMeta.set({ route: 'sectors/{sectorID}/ships', fields: [] });
    store.uuid.set('s1');
    store.buildStoreViewData();
    await settle(() => store.rowPresent());
    await client.live.start(feed);
    feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000001' });
    await settle(() => false, 5);
    expect(dataRequests(script.requests)).toEqual([{ url: s1, version: undefined, subscribed: false }]);
    expect(client.live.subscriptions()).toEqual([]);
    await client.live.stop();
  });

  it('a changed key moves the hold to the new row', async () => {
    const s2 = '/api/sectors/anvil/ships/s2?capabilities=Update%2CDelete';
    const script = scripted({ [s1]: { rows: { id: 's1' } }, [s2]: { rows: { id: 's2' } } });
    const { store, client, feed } = storeOver(script);
    store.resourceMeta.set({ route: 'sectors/{sectorID}/ships', fields: [] });
    store.liveChoice.set(true);
    store.uuid.set('s1');
    store.buildStoreViewData();
    await client.live.start(feed);
    await settle(() => store.viewData()['id'] === 's1');
    store.uuid.set('s2');
    await settle(() => store.viewData()['id'] === 's2');
    expect(client.live.subscriptions()).toEqual([{ resource: 'Ships', key: 's2', domain: 'anvil' }]);
    const before = script.requests.length;
    feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000005' });
    await settle(() => false, 5);
    expect(script.requests).toHaveLength(before);
    feed.emit({ kind: 'row', resource: 'Ships', key: 's2', at: '1700000000000006' });
    await settle(() => script.requests.length > before);
    expect(dataRequests(script.requests).at(-1)).toEqual({ url: s2, version: '1700000000000006', subscribed: true });
    await client.live.stop();
  });
});

describe('ResourceStore live list page', () => {
  const listPage = (store: ResourceStore): void => {
    store.resourceName.set('Ships' as Resource);
    store.resourceMeta.set({ route: 'sectors/{sectorID}/ships', fields: [] });
    store.listColumns.set([{ id: 'name' } as ColumnConfig]);
    store.liveChoice.set(true);
    store.buildStorePage();
  };

  it('asks plainly until the feed runs, then live, and requests the page again by each change to the resource in the tenant', async () => {
    const second = '/api/sectors/anvil/ships?columns=name&cursor=v4.local.two';
    const script = scripted({
      [anvilPage]: {
        rows: [{ id: 's1', name: 'Kestrel' }],
        headers: { 'total-count': '2', link: `<${second}>; rel="next"` },
      },
      [second]: { rows: [{ id: 's2', name: 'Halcyon' }], headers: { link: `<${anvilPage}>; rel="prev"` } },
    });
    const { store, client, feed } = storeOver(script);
    listPage(store);
    await settle(() => store.pageStatus() === 'resolved');
    expect(dataRequests(script.requests)).toEqual([{ url: anvilPage, version: undefined, subscribed: false }]);
    expect(client.live.subscriptions()).toEqual([{ resource: 'Ships', domain: 'anvil' }]);

    await client.live.start(feed);
    await settle(() => script.requests.length >= 3 && store.pageStatus() === 'resolved');
    expect(dataRequests(script.requests)[1]).toEqual({ url: anvilPage, version: client.live.seed, subscribed: true });

    // On the second page, a change to the resource in the tenant requests that page again, by its cursor and the change's version.
    store.turnPage('next');
    await settle(() => store.page().offset === 1);
    expect(dataRequests(script.requests)[2]).toEqual({ url: second, version: client.live.seed, subscribed: true });
    feed.emit({ kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000010' });
    await settle(() => script.requests.length >= 5 && store.pageStatus() === 'resolved');
    expect(dataRequests(script.requests)[3]).toEqual({ url: second, version: '1700000000000010', subscribed: true });
    expect(store.page().offset).toBe(1);
    expect(store.page().rows).toEqual([{ id: 's2', name: 'Halcyon' }]);

    // Another tenant's list, a row of the resource, and another resource: nothing.
    feed.emit({ kind: 'list', resource: 'Ships', domain: 'bastion', at: '1700000000000011' });
    feed.emit({ kind: 'row', resource: 'Ships', key: 's1', at: '1700000000000012' });
    feed.emit({ kind: 'list', resource: 'ShipClasses', at: '1700000000000013' });
    await settle(() => false, 5);
    expect(dataRequests(script.requests)).toHaveLength(4);

    // A resource-level change reaches the list.
    feed.emit({ kind: 'resource', resource: 'Ships', at: '1700000000000014' });
    await settle(() => script.requests.length >= 6 && store.pageStatus() === 'resolved');
    expect(dataRequests(script.requests)[4]).toEqual({ url: second, version: '1700000000000014', subscribed: true });
    await client.live.stop();
  });

  it("the tenant moving moves the subscription, so the new tenant's changes reach the page and the old one's do not", async () => {
    const script = scripted({ [anvilPage]: { rows: [] }, [bastionPage]: { rows: [] } });
    const domain = signal<Domain | undefined>('anvil' as Domain);
    const { store, client, feed } = storeOver(script, domain);
    listPage(store);
    await client.live.start(feed);
    await settle(() => store.pageStatus() === 'resolved');

    domain.set('bastion' as Domain);
    await settle(
      () => script.requests.some((r) => split(r.url).url === bastionPage) && store.pageStatus() === 'resolved',
    );
    expect(client.live.subscriptions()).toEqual([{ resource: 'Ships', domain: 'bastion' }]);
    const before = script.requests.length;
    feed.emit({ kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000000000020' });
    await settle(() => false, 5);
    expect(script.requests).toHaveLength(before);
    feed.emit({ kind: 'list', resource: 'Ships', domain: 'bastion', at: '1700000000000021' });
    await settle(() => script.requests.length > before && store.pageStatus() === 'resolved');
    expect(dataRequests(script.requests).at(-1)).toEqual({
      url: bastionPage,
      version: '1700000000000021',
      subscribed: true,
    });
    await client.live.stop();
  });

  it('a filter changing re-registers nothing: the subscription is the resource in the tenant, not the page', async () => {
    const filtered = '/api/sectors/anvil/ships?filter=name%3Aeq%3AKestrel&columns=name&count=true';
    const script = scripted({ [anvilPage]: { rows: [] }, [filtered]: { rows: [] } });
    const { store, client, feed } = storeOver(script);
    const watched: string[] = [];
    const watch = client.live.watch.bind(client.live);
    client.live.watch = (subscription, refetch) => {
      watched.push(subscription.domain ?? '');
      return watch(subscription, refetch);
    };
    listPage(store);
    await client.live.start(feed);
    await settle(() => store.pageStatus() === 'resolved');
    store.filter.set('name:eq:Kestrel');
    await settle(() => script.requests.some((r) => split(r.url).url === filtered) && store.pageStatus() === 'resolved');
    expect(watched).toEqual(['anvil']);
    await client.live.stop();
  });

  it("the store's hold ends with the store", async () => {
    const script = scripted({ [anvilPage]: { rows: [] }, [s1]: { rows: { id: 's1' } } });
    const { store, client } = storeOver(script);
    listPage(store);
    store.uuid.set('s1');
    store.buildStoreViewData();
    await settle(() => store.pageStatus() === 'resolved' && store.rowPresent());
    expect(client.live.subscriptions()).toEqual([
      { resource: 'Ships', domain: 'anvil' },
      { resource: 'Ships', key: 's1', domain: 'anvil' },
    ]);
    TestBed.resetTestingModule();
    expect(client.live.subscriptions()).toEqual([]);
  });
});

describe('ResourceStore live by default', () => {
  // The metadata's word decides: a resource whose changes are published (ResourceMeta.live)
  // makes its page live unless the config says false; one whose changes are not makes it
  // plain, and a config asking anyway is announced once; metadata without the field, from a
  // generator that predates it, leaves the config's own word standing.
  const page = (store: ResourceStore, meta: { live?: boolean }, choice: boolean | undefined): void => {
    store.resourceName.set('Ships' as Resource);
    store.resourceMeta.set({ route: 'sectors/{sectorID}/ships', fields: [], ...meta });
    store.listColumns.set([{ id: 'name' } as ColumnConfig]);
    store.liveChoice.set(choice);
    store.buildStorePage();
  };
  const fleet = (): Scripted => scripted({ [anvilPage]: { rows: [{ id: 's1', name: 'Kestrel' }] } });

  it('a page on a resource whose changes are published is live when its config says nothing', async () => {
    const script = fleet();
    const { store, client } = storeOver(script);
    page(store, { live: true }, undefined);
    await settle(() => store.pageStatus() === 'resolved');
    expect(store.live()).toBe(true);
    expect(client.live.subscriptions()).toEqual([{ resource: 'Ships', domain: 'anvil' }]);
  });

  it('live: false opts such a page out', async () => {
    const script = fleet();
    const { store, client } = storeOver(script);
    page(store, { live: true }, false);
    await settle(() => store.pageStatus() === 'resolved');
    expect(store.live()).toBe(false);
    expect(client.live.subscriptions()).toEqual([]);
  });

  it('metadata without the field makes no page live by itself', async () => {
    const script = fleet();
    const { store, client } = storeOver(script);
    page(store, {}, undefined);
    await settle(() => store.pageStatus() === 'resolved');
    expect(store.live()).toBe(false);
    expect(client.live.subscriptions()).toEqual([]);
  });

  it('a page asking live: true on a resource whose changes are not published stays plain and is told once', async () => {
    const warnings: string[] = [];
    const script = fleet();
    const { store, client } = storeOver(script, signal('anvil' as Domain), warnings);
    page(store, { live: false }, true);
    await settle(() => store.pageStatus() === 'resolved');
    expect(store.live()).toBe(false);
    expect(client.live.subscriptions()).toEqual([]);
    expect(warnings).toEqual([unpublishedLiveWarning('Ships')]);
    store.liveChoice.set(false);
    TestBed.tick();
    store.liveChoice.set(true);
    await settle(() => false, 3);
    expect(warnings).toHaveLength(1);
  });
});
