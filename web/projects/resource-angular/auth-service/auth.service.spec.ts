import { TestBed } from '@angular/core/testing';
import {
  ApiDescriptor,
  ChangeFeed,
  createClient,
  LiveIdentity,
  LiveRoutes,
  noFeaturesRouteWarning,
  subscribeHeader,
  Warn,
} from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { CHANGE_FEED, LOGIN_REDIRECT_URL } from '@impulseframework/resource-angular/types';
import { firstValueFrom } from 'rxjs';

import { AuthService } from './auth.service';

const descriptor: ApiDescriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

const identity: LiveIdentity = {
  uid: 'u-7',
  token: 'custom.jwt',
  project: 'prod',
  database: '(default)',
  apiKey: 'web-key',
  emulator: '',
};

class FakeFeed implements ChangeFeed {
  started: LiveIdentity[] = [];
  stopped = 0;

  async start(identity: LiveIdentity): Promise<void> {
    this.started.push(identity);
  }

  async stop(): Promise<void> {
    this.stopped++;
  }

  onChange(): () => void {
    return () => undefined;
  }

  onResync(): () => void {
    return () => undefined;
  }
}

/** The flags the features route says are on; a spec changes it between checks. */
let enabledFeatures: string[] = ['debriefs'];
/** The session's operator-set password must change; a spec sets it before a check and clears it after the change. */
let passwordChangeRequired = false;

/** The session, digest, domains, features, and live routes, each answering; `authenticated` is what the session says. */
function serverWith(authenticated: boolean): ScriptedTransport {
  return scriptedTransport((request) => {
    switch (request.url) {
      case '/api/user/session':
        return {
          status: request.method === 'DELETE' ? 204 : 200,
          body: request.method === 'DELETE' ? undefined : { authenticated, passwordChangeRequired },
        };
      case '/api/permission-digest':
        return { status: 200, body: {} };
      case '/api/user-domains':
        return { status: 200, body: [] };
      case '/api/features':
        return { status: 200, body: { enabled: enabledFeatures } };
      case '/api/live/token':
        return { status: 200, body: identity };
      case '/api/live/unsubscribe':
        return { status: 204, body: undefined };
      default:
        return { status: 404, body: { message: `unscripted ${request.url}` } };
    }
  });
}

/** The live block a browser descriptor carries when the outlet serves live subscriptions. */
const liveRoutes: LiveRoutes = {
  renewRoute: 'live/renew',
  unsubscribeRoute: 'live/unsubscribe',
  tokenRoute: 'live/token',
};

function authOver(
  transport: ScriptedTransport,
  options: { live?: LiveRoutes; feed?: ChangeFeed; api?: ApiDescriptor; warn?: Warn } = {},
): AuthService {
  TestBed.configureTestingModule({
    providers: [
      {
        provide: RESOURCE_CLIENT,
        useValue: createClient(
          { ...(options.api ?? descriptor), live: options.live },
          {
            baseUrl: '/api',
            transport,
            warn: options.warn,
            // The zone header has its own suite in the resource package; these pin the live headers alone.
            timeZone: '',
            live: {
              renewInterval: 60 * 60 * 1000,
              page: { addEventListener: () => undefined, removeEventListener: () => undefined },
            },
          },
        ),
      },
      ...(options.feed ? [{ provide: CHANGE_FEED, useValue: options.feed }] : []),
    ],
  });
  return TestBed.inject(AuthService);
}

describe('AuthService', () => {
  it('fails at startup without RESOURCE_CLIENT, naming the way in', () => {
    TestBed.configureTestingModule({});
    expect(() => TestBed.inject(AuthService)).toThrowError(/RESOURCE_CLIENT[\s\S]*provideResourceClient/);
  });

  it('redirectUrl is the LOGIN_REDIRECT_URL signal the client hook writes', () => {
    TestBed.configureTestingModule({
      providers: [{ provide: RESOURCE_CLIENT, useValue: createClient(descriptor, { baseUrl: '/api' }) }],
    });
    const auth = TestBed.inject(AuthService);
    TestBed.inject(LOGIN_REDIRECT_URL).set('/deck');
    expect(auth.redirectUrl()).toBe('/deck');
    auth.redirectUrl.set('');
    expect(TestBed.inject(LOGIN_REDIRECT_URL)()).toBe('');
  });
});

describe('AuthService live session', () => {
  it('starts the feed with the identity the API minted once the session first authenticates, and not again on a keepalive', async () => {
    const transport = serverWith(true);
    const feed = new FakeFeed();
    const auth = authOver(transport, { live: liveRoutes, feed });
    await firstValueFrom(auth.checkUserSession());
    expect(transport.requests.map((r) => [r.method, r.url])).toEqual([
      ['GET', '/api/user/session'],
      ['GET', '/api/permission-digest'],
      ['GET', '/api/user-domains'],
      ['GET', '/api/features'],
      ['GET', '/api/live/token'],
    ]);
    expect(feed.started).toEqual([identity]);
    expect(TestBed.inject(RESOURCE_CLIENT).live.active).toBe(true);

    await firstValueFrom(auth.checkUserSession());
    expect(transport.requests.filter((r) => r.url === '/api/live/token')).toHaveLength(1);
    expect(feed.started).toHaveLength(1);
    await TestBed.inject(RESOURCE_CLIENT).live.stop();
  });

  it('logout ends the live session first, every subscription and the identity, then logs the session out', async () => {
    const transport = serverWith(true);
    const feed = new FakeFeed();
    const auth = authOver(transport, { live: liveRoutes, feed });
    await firstValueFrom(auth.checkUserSession());
    const client = TestBed.inject(RESOURCE_CLIENT);
    const tab = client.live.tab;
    transport.requests.length = 0;

    expect(await firstValueFrom(auth.logout())).toBe(true);
    expect(transport.requests.map((r) => [r.method, r.url, r.body])).toEqual([
      ['POST', '/api/live/unsubscribe', { tab, all: true }],
      ['DELETE', '/api/user/session', undefined],
    ]);
    expect(transport.requests[0].headers).toEqual({ [subscribeHeader]: tab });
    expect(feed.stopped).toBe(1);
    expect(client.live.started).toBe(false);
    expect(auth.authenticated()).toBe(false);
  });

  it('an unauthenticated answer ends a running live session without the unsubscribe no session could speak for', async () => {
    let authenticated = true;
    const transport = scriptedTransport((request) =>
      request.url === '/api/user/session' ? { status: 200, body: { authenticated } } : serverWith(true)(request),
    );
    const feed = new FakeFeed();
    const auth = authOver(transport, { live: liveRoutes, feed });
    await firstValueFrom(auth.checkUserSession());
    expect(feed.started).toHaveLength(1);

    authenticated = false;
    await firstValueFrom(auth.checkUserSession());
    expect(feed.stopped).toBe(1);
    expect(transport.requests.filter((r) => r.url === '/api/live/unsubscribe')).toEqual([]);
    expect(TestBed.inject(RESOURCE_CLIENT).live.started).toBe(false);
  });

  const quiet: { name: string; live: LiveRoutes | undefined; feed: FakeFeed | undefined }[] = [
    { name: 'no feed provided: nothing live is asked, whatever the API serves', live: liveRoutes, feed: undefined },
    {
      name: 'a feed provided to an API that serves no live: nothing live is asked, and nothing fails',
      live: undefined,
      feed: new FakeFeed(),
    },
  ];

  for (const tt of quiet) {
    it(tt.name, async () => {
      const transport = serverWith(true);
      const auth = authOver(transport, { live: tt.live, feed: tt.feed });
      await firstValueFrom(auth.checkUserSession());
      expect(transport.requests.map((r) => r.url)).toEqual([
        '/api/user/session',
        '/api/permission-digest',
        '/api/user-domains',
        '/api/features',
      ]);
      expect(tt.feed?.started ?? []).toEqual([]);
      transport.requests.length = 0;
      await firstValueFrom(auth.logout());
      expect(transport.requests.map((r) => [r.method, r.url])).toEqual([['DELETE', '/api/user/session']]);
    });
  }

  it('a feed that fails to start leaves the session authenticated and the pages plain', async () => {
    const transport = serverWith(true);
    const feed = new (class extends FakeFeed {
      override async start(): Promise<void> {
        throw new Error('auth/invalid-custom-token');
      }
    })();
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    const auth = authOver(transport, { live: liveRoutes, feed });
    const session = await firstValueFrom(auth.checkUserSession());
    expect(session.authenticated).toBe(true);
    expect(auth.authenticated()).toBe(true);
    expect(TestBed.inject(RESOURCE_CLIENT).live.active).toBe(false);
    expect(consoleError).toHaveBeenCalledTimes(1);
    consoleError.mockRestore();
  });
});

describe('AuthService under a forced password change', () => {
  afterEach(() => {
    passwordChangeRequired = false;
  });

  it('an authenticated answer carrying the mark loads nothing, and the first check after the change loads the digest, the domains and the set', async () => {
    passwordChangeRequired = true;
    const transport = serverWith(true);
    const auth = authOver(transport);
    const marked = await firstValueFrom(auth.checkUserSession());
    expect(marked.passwordChangeRequired).toBe(true);
    expect(auth.authenticated()).toBe(true);
    expect(auth.sessionInfo().passwordChangeRequired).toBe(true);
    expect(transport.requests.map((r) => r.url)).toEqual(['/api/user/session']);

    passwordChangeRequired = false;
    const changed = await firstValueFrom(auth.checkUserSession());
    expect(changed.passwordChangeRequired).toBe(false);
    expect(transport.requests.map((r) => r.url).slice(1)).toEqual([
      '/api/user/session',
      '/api/permission-digest',
      '/api/user-domains',
      '/api/features',
    ]);
    expect(auth.featureEnabled('debriefs')).toBe(true);
  });
});

describe('AuthService feature flags', () => {
  beforeEach(() => {
    enabledFeatures = ['debriefs'];
  });

  it('loads the enabled set beside the digest and the domains once the session first authenticates, and answers from it', async () => {
    const transport = serverWith(true);
    const auth = authOver(transport);
    expect(auth.featureEnabled('debriefs')).toBe(false);
    expect(auth.enabledFeatures().size).toBe(0);

    await firstValueFrom(auth.checkUserSession());
    TestBed.tick();
    expect(transport.requests.map((r) => r.url)).toEqual([
      '/api/user/session',
      '/api/permission-digest',
      '/api/user-domains',
      '/api/features',
    ]);
    expect(auth.featureEnabled('debriefs')).toBe(true);
    expect(auth.featureEnabled('manifests')).toBe(false);
    expect(auth.featureEnabled(undefined)).toBe(true);
    expect([...auth.enabledFeatures()]).toEqual(['debriefs']);

    // A keepalive leaves the set alone.
    await firstValueFrom(auth.checkUserSession());
    expect(transport.requests.filter((r) => r.url === '/api/features')).toHaveLength(1);
  });

  it('logout forgets the enabled set', async () => {
    const auth = authOver(serverWith(true));
    await firstValueFrom(auth.checkUserSession());
    TestBed.tick();
    expect(auth.featureEnabled('debriefs')).toBe(true);
    await firstValueFrom(auth.logout());
    TestBed.tick();
    expect(auth.featureEnabled('debriefs')).toBe(false);
    expect(TestBed.inject(RESOURCE_CLIENT).features.loaded).toBe(false);
  });

  it('an unauthenticated answer forgets the enabled set', async () => {
    let authenticated = true;
    const transport = scriptedTransport((request) =>
      request.url === '/api/user/session' ? { status: 200, body: { authenticated } } : serverWith(true)(request),
    );
    const auth = authOver(transport);
    await firstValueFrom(auth.checkUserSession());
    TestBed.tick();
    expect(auth.featureEnabled('debriefs')).toBe(true);

    authenticated = false;
    await firstValueFrom(auth.checkUserSession());
    TestBed.tick();
    expect(auth.featureEnabled('debriefs')).toBe(false);
  });

  it('refreshFeatures reloads the set, and a flip elsewhere is seen only then', async () => {
    const transport = serverWith(true);
    const auth = authOver(transport);
    await firstValueFrom(auth.checkUserSession());
    TestBed.tick();
    enabledFeatures = ['debriefs', 'manifests'];
    expect(auth.featureEnabled('manifests')).toBe(false);

    expect(await firstValueFrom(auth.refreshFeatures())).toEqual(['debriefs', 'manifests']);
    TestBed.tick();
    expect(auth.featureEnabled('manifests')).toBe(true);
  });

  it('ensureFeature loads the set first when it has not loaded, and answers false without a name requirement only when a name is given', async () => {
    const transport = serverWith(true);
    const auth = authOver(transport);
    expect(await firstValueFrom(auth.ensureFeature('debriefs'))).toBe(true);
    expect(await firstValueFrom(auth.ensureFeature('manifests'))).toBe(false);
    expect(await firstValueFrom(auth.ensureFeature(undefined))).toBe(true);
    expect(transport.requests.map((r) => r.url)).toEqual(['/api/features']);
  });

  it('an API that serves no feature flags loads the digest and the domains alone, and every flag answers off', async () => {
    const transport = serverWith(true);
    const warnings: string[] = [];
    const auth = authOver(transport, {
      api: { ...descriptor, features: undefined },
      warn: (message) => warnings.push(message),
    });
    const session = await firstValueFrom(auth.checkUserSession());
    TestBed.tick();
    expect(session.authenticated).toBe(true);
    expect(transport.requests.map((r) => r.url)).toEqual([
      '/api/user/session',
      '/api/permission-digest',
      '/api/user-domains',
    ]);
    expect(auth.featureEnabled('debriefs')).toBe(false);
    expect(auth.enabledFeatures().size).toBe(0);
    expect(await firstValueFrom(auth.ensureFeature('debriefs'))).toBe(false);
    expect(await firstValueFrom(auth.refreshFeatures())).toEqual([]);
    expect(transport.requests.filter((r) => r.url === '/api/features')).toHaveLength(0);
    expect(TestBed.inject(RESOURCE_CLIENT).features.loaded).toBe(false);
    // The two asks of debriefs announced it once between them; the refresh announced every flag.
    expect(warnings).toEqual([noFeaturesRouteWarning('debriefs'), noFeaturesRouteWarning()]);
  });

  it('the gates announce an API that serves no feature flags once per flag, through the client warn hook', async () => {
    const warnings: string[] = [];
    const auth = authOver(serverWith(true), {
      api: { ...descriptor, features: undefined },
      warn: (message) => warnings.push(message),
    });
    await firstValueFrom(auth.checkUserSession());
    TestBed.tick();
    // Sign-in skipped the load and said nothing: no page has asked yet.
    expect(warnings).toEqual([]);

    expect(auth.featureEnabled('debriefs')).toBe(false);
    expect(auth.featureEnabled('debriefs')).toBe(false);
    expect(await firstValueFrom(auth.ensureFeature('debriefs'))).toBe(false);
    expect(warnings).toEqual([noFeaturesRouteWarning('debriefs')]);
    expect(warnings[0]).toBe(
      'feature flag debriefs was asked of an API that serves no feature flags (the generated descriptor carries no features route): ' +
        'it answers off; regenerate the client API with a resource generator that emits the features route',
    );
  });

  it('the gates never announce anything on an API that serves feature flags', async () => {
    const warnings: string[] = [];
    const auth = authOver(serverWith(true), { warn: (message) => warnings.push(message) });
    expect(auth.featureEnabled('debriefs')).toBe(false);
    await firstValueFrom(auth.checkUserSession());
    TestBed.tick();
    expect(auth.featureEnabled('debriefs')).toBe(true);
    expect(auth.featureEnabled('manifests')).toBe(false);
    expect(await firstValueFrom(auth.ensureFeature('manifests'))).toBe(false);
    expect(await firstValueFrom(auth.refreshFeatures())).toEqual(['debriefs']);
    expect(warnings).toEqual([]);
  });

  it('a features route that fails leaves the session authenticated and every flag off', async () => {
    const transport = scriptedTransport((request) =>
      request.url === '/api/features' ? { status: 503, body: { message: 'unavailable' } } : serverWith(true)(request),
    );
    const auth = authOver(transport);
    const session = await firstValueFrom(auth.checkUserSession());
    TestBed.tick();
    expect(session.authenticated).toBe(true);
    expect(auth.authenticated()).toBe(true);
    expect(auth.featureEnabled('debriefs')).toBe(false);
    expect(await firstValueFrom(auth.refreshFeatures())).toEqual([]);
  });
});
