import { describe, expect, it } from 'bun:test';
import { Domain, ListPermission, Method, Resource } from './brands';
import { AnyResourceHandle, Client, createClient, MethodHandle } from './client';
import { ApiDescriptor } from './descriptor';
import { ChangeFeed, LiveIdentity, noLiveRoutesWarning, subscribeHeader } from './live';
import { userTokens } from './tokens';
import { scriptedTransport } from '@impulseframework/resource/testing';

// The two directions of descriptor compatibility: the generator and this package
// release in either order. An older generator's descriptor, without a field this package
// added since (here `live`), builds a client where that feature is off and everything
// else works. A newer generator's descriptor, carrying a field this package does not
// know yet, is accepted by createClient when it arrives as a value, the way zz_gen_api.ts
// hands it over: this file compiling under `tsc -p projects/resource/tsconfig.spec.json`
// is that proof, and the runtime half confirms the client was built over it. Absence is
// tolerated but announced: a live ask of a descriptor without `live` reaches the client's
// warn hook once, in words that name the regeneration that would serve it. A descriptor
// without `tokens`, from a generator that predates them, binds no token handle, and a
// page over it says so instead of asking.

interface Row {
  id: string;
}

interface Global {
  shipClasses: AnyResourceHandle<Row>;
  completeMission: MethodHandle<{ missionId: string }, { settled: boolean }>;
}

interface Sector {
  ships: AnyResourceHandle<Row>;
}

/** The descriptor an older generator wrote: no `live` block and no `tokens` block, since it predates both. */
const older: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  resources: {
    Ships: {
      resource: 'Ships' as Resource,
      property: 'ships',
      route: 'ships',
      scope: 'domain',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25 },
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
  methods: {
    CompleteMission: {
      method: 'CompleteMission' as Method,
      property: 'completeMission',
      route: 'complete-mission',
      scope: 'global',
      answers: true,
    },
  },
};

const anvil = 'anvil' as Domain;

/** Every route answering by its path (a live request carries the version in its query), so each part of the client can be exercised. */
function server() {
  return scriptedTransport((request) => {
    switch (request.url.replace(/\?.*$/, '')) {
      case '/api/ship-classes':
        return { status: 200, body: [{ id: 'sc1' }, { id: 'sc2' }] };
      case '/api/ship-classes/sc1':
        return { status: 200, body: { id: 'sc1' } };
      case '/api/sectors/anvil/ships':
        return { status: 200, body: [{ id: 's1' }] };
      case '/api/complete-mission':
        return { status: 200, body: { settled: true } };
      case '/api/permission-digest':
        return { status: 200, body: { ShipClasses: { List: 'granted' } } };
      case '/api/user-domains':
        return { status: 200, body: ['anvil'] };
      case '/api/live/token':
        return { status: 200, body: identity };
      case '/api/live/unsubscribe':
        return { status: 204, body: undefined };
      default:
        return { status: 404, body: { message: `unscripted ${request.url}` } };
    }
  });
}

const identity: LiveIdentity = {
  uid: 'u-7',
  token: '',
  project: 'lab',
  database: 'live',
  apiKey: '',
  emulator: 'localhost:8080',
};

/** A feed that starts and stops and reports nothing, for the API that serves live. */
const quietFeed: ChangeFeed = {
  start: async () => undefined,
  stop: async () => undefined,
  onChange: () => () => undefined,
  onResync: () => () => undefined,
};

/** A feed that must never be started: the API under test serves no live subscriptions. */
const feed: ChangeFeed = {
  start: async () => {
    throw new Error('the feed was started against an API that serves no live subscriptions');
  },
  stop: async () => undefined,
  onChange: () => () => undefined,
  onResync: () => () => undefined,
};

describe('a descriptor from an older generator, without the live block', () => {
  it('builds a client where live is off and every other part works', async () => {
    const transport = server();
    const warnings: string[] = [];
    const client = createClient<Global, Sector>(older, {
      baseUrl: '/api',
      transport,
      warn: (message) => warnings.push(message),
    });

    // Live is off: the session says so, a live call is a plain request, and no feed starts;
    // the absence is announced once between the live call and the start.
    expect(client.live.enabled).toBe(false);
    expect(client.live.active).toBe(false);
    expect(await client.shipClasses.list({ live: true })).toEqual([{ id: 'sc1' }, { id: 'sc2' }]);
    expect(transport.requests[0].url).toBe('/api/ship-classes');
    expect(transport.requests[0].headers?.[subscribeHeader]).toBeUndefined();
    await expect(client.live.start(feed)).rejects.toThrow('serves no live subscriptions');
    await client.live.stop();
    expect(warnings).toEqual([noLiveRoutesWarning]);

    // Tokens are off: the handle is undefined, and nothing is requested for them.
    expect(userTokens(client)).toBeUndefined();

    // Everything else works: global and domain handles, a method, the digest and its answers.
    expect(await client.shipClasses.read(['sc1'])).toEqual({ id: 'sc1' });
    expect(await client.domain(anvil).ships.list()).toEqual([{ id: 's1' }]);
    expect(await client.completeMission.execute({ missionId: 'm1' })).toEqual({ settled: true });
    await client.permissions.loadDigest();
    expect(await client.permissions.loadDomains()).toEqual(['anvil']);
    expect(client.can(ListPermission, 'ShipClasses' as Resource)).toBe(true);
    expect(client.can(ListPermission, 'Ships' as Resource, anvil)).toBe(false);
    expect(transport.requests.map((r) => [r.method, r.url])).toEqual([
      ['GET', '/api/ship-classes'],
      ['GET', '/api/ship-classes/sc1'],
      ['GET', '/api/sectors/anvil/ships'],
      ['POST', '/api/complete-mission'],
      ['GET', '/api/permission-digest'],
      ['GET', '/api/user-domains'],
    ]);
  });
});

/**
 * What zz_gen_api.ts wraps the descriptor in: the literal keeps its own types, and the
 * constraint is all TypeScript checks it against, so a field this package does not
 * declare passes.
 */
function defineApiDescriptor<T extends ApiDescriptor>(descriptor: T): T {
  return descriptor;
}

/** The block a later generator writes for a feature this package does not know yet. */
const unknownBlock = { route: 'exports', maxRows: 10_000 };

/** The descriptor as the generated file emits it: wrapped, with the unknown field in the literal. */
const wrapped = defineApiDescriptor({
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  resources: {
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
  live: { renewRoute: 'live/renew', unsubscribeRoute: 'live/unsubscribe', tokenRoute: 'live/token' },
  exports: unknownBlock,
});

/** The same descriptor built as a plain value, the unknown field spread in. */
const asValue = { ...older, exports: unknownBlock };

describe('a descriptor from a newer generator, with a field this package does not know', () => {
  const cases: { name: string; descriptor: ApiDescriptor }[] = [
    { name: 'wrapped by the generated defineApiDescriptor', descriptor: wrapped },
    { name: 'handed over as a value', descriptor: asValue },
  ];

  for (const tt of cases) {
    it(`${tt.name} is accepted by createClient, the unknown field carried and ignored`, async () => {
      const transport = server();
      const client = createClient<Global, Sector>(tt.descriptor, { baseUrl: '/api', transport });
      expect(client.descriptor).toBe(tt.descriptor);
      expect((client.descriptor as unknown as { exports: unknown }).exports).toEqual(unknownBlock);
      expect(await client.shipClasses.list()).toEqual([{ id: 'sc1' }, { id: 'sc2' }]);
      expect(transport.requests.map((r) => r.url)).toEqual(['/api/ship-classes']);
    });
  }

  it('the wrapped literal keeps its narrow types, so a resource reads as its declared scope', () => {
    const scope: 'global' = wrapped.resources.ShipClasses.scope;
    expect(scope).toBe('global');
    expect(wrapped.live.tokenRoute).toBe('live/token');
  });

  it('a fresh literal typed as ApiDescriptor is the one form TypeScript refuses, which is why the generated file wraps it', () => {
    // @ts-expect-error a fresh object literal is checked for excess properties, and this package does not declare `exports`
    const typed: ApiDescriptor = { ...older, exports: unknownBlock };
    expect(typed.permissionDigestRoute).toBe('permission-digest');
  });
});

/** A client over the API with the warn hook collecting what it receives. */
function clientWith(api: ApiDescriptor): { client: Client<Global, Sector>; warnings: string[] } {
  const warnings: string[] = [];
  const client = createClient<Global, Sector>(api, {
    baseUrl: '/api',
    transport: server(),
    warn: (message) => warnings.push(message),
  });
  return { client, warnings };
}

describe('the warn hook on an API that serves no live subscriptions', () => {
  const asks: { name: string; ask: (client: Client<Global, Sector>) => Promise<unknown> }[] = [
    {
      name: 'a live list, asked twice',
      ask: async (client) => {
        await client.shipClasses.list({ live: true });
        await client.shipClasses.list({ live: true });
      },
    },
    {
      name: 'a live read, asked twice',
      ask: async (client) => {
        await client.shipClasses.read(['sc1'], { live: true });
        await client.shipClasses.read(['sc1'], { live: true });
      },
    },
    {
      name: 'a live page and its reload',
      ask: async (client) => {
        const page = await client.shipClasses.page({ live: true });
        await page.reload();
      },
    },
    { name: 'a live list in a domain', ask: (client) => client.domain(anvil).ships.list({ live: true }) },
    {
      name: 'starting a feed, twice',
      ask: async (client) => {
        await client.live.start(feed).catch(() => undefined);
        await client.live.start(feed).catch(() => undefined);
      },
    },
    {
      name: 'a live list and then a feed start',
      ask: async (client) => {
        await client.shipClasses.list({ live: true });
        await client.live.start(feed).catch(() => undefined);
      },
    },
  ];

  for (const tt of asks) {
    it(`${tt.name} announces the absence once, naming the regeneration that would serve it`, async () => {
      const { client, warnings } = clientWith(older);
      await tt.ask(client);
      expect(warnings).toEqual([noLiveRoutesWarning]);
      expect(warnings[0]).toBe(
        'this page asked for live updates, but the generated API descriptor carries no live block: the request is served plain ' +
          'and nothing is subscribed; regenerate the client API with a resource generator that emits the live routes',
      );
    });
  }

  it('a plain request on such an API says nothing', async () => {
    const { client, warnings } = clientWith(older);
    await client.shipClasses.list();
    await client.shipClasses.read(['sc1']);
    await client.domain(anvil).ships.list();
    await client.live.stop();
    expect(warnings).toEqual([]);
  });

  it('a descriptor with the live block warns nothing: plain and silent before the feed runs, live after', async () => {
    const { client, warnings } = clientWith(wrapped);
    await client.shipClasses.list({ live: true });
    expect(client.live.active).toBe(false);
    await client.live.start(quietFeed);
    expect(client.live.active).toBe(true);
    await client.shipClasses.list({ live: true });
    const page = await client.shipClasses.page({ live: true });
    await page.reload();
    await client.live.stop();
    expect(warnings).toEqual([]);
  });

  it('the default hook is the console', async () => {
    const seen: string[] = [];
    const original = console.warn;
    console.warn = (message: string) => {
      seen.push(message);
    };
    try {
      const client = createClient<Global, Sector>(older, { baseUrl: '/api', transport: server() });
      await client.shipClasses.list({ live: true });
      await client.shipClasses.list({ live: true });
    } finally {
      console.warn = original;
    }
    expect(seen).toEqual([noLiveRoutesWarning]);
  });
});
