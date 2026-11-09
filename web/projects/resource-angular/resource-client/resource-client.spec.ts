import {
  HttpClient,
  HttpErrorResponse,
  provideHttpClient,
  withInterceptors,
  withXsrfConfiguration,
} from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting, TestRequest } from '@angular/common/http/testing';
import { Component, ErrorHandler, Injectable, Provider } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import {
  ApiDescriptor,
  ApiError,
  ApiVersionError,
  apiVersionHeader,
  ClientBase,
  createClient,
  MaintenanceError,
  maintenanceHeader,
  maintenanceHeaderValue,
  retryAfterHeader,
} from '@impulseframework/resource';
import {
  AlertType,
  API_VERSION,
  BASE_URL,
  LOGIN_REDIRECT_URL,
  MAINTENANCE_HANDLER,
  MaintenanceHandler,
  VERSION_REFUSAL_HANDLER,
  VersionRefusalHandler,
} from '@impulseframework/resource-angular/types';
import { UiCoreService } from '@impulseframework/resource-angular/ui-core-service';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';

import {
  apiVersionInterceptor,
  httpClientTransport,
  liveClientOptions,
  NO_RESPONSE_MESSAGE,
  provideResourceClient,
  RESOURCE_CLIENT,
  ResourceClientOptions,
  ResourceErrorHandler,
} from './resource-client';

// The Angular adapter renders the client's judgment: a 401 returns the browser to the
// login page with the attempted URL kept, an ApiError nobody caught raises one global
// notice in the server's words, a declared answer and a handled refusal raise none, every
// request moves the activity counter, the release the build names rides every request of
// the client and of the application's own same-origin HttpClient calls, the server's
// refusal of that release goes to the update service and raises no notice of its own, and
// the server's maintenance answer goes to the maintenance service the same way while a 503
// without the marker stays an ordinary error.

@Component({ template: '' })
class BlankComponent {}

afterEach(() => {
  vi.restoreAllMocks();
});

const descriptor: ApiDescriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

function messages(): string[] {
  return TestBed.inject(NotificationService)
    .notifications()
    .map((n) => n.message);
}

describe('provideResourceClient', () => {
  let received: ResourceClientOptions | undefined;
  let http: HttpTestingController;
  let router: Router;
  let client: ClientBase;

  beforeEach(async () => {
    received = undefined;
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter([
          { path: 'deck', component: BlankComponent },
          { path: 'login', component: BlankComponent },
        ]),
        { provide: BASE_URL, useValue: 'https://console.example' },
        provideResourceClient((options) => {
          received = options;
          return createClient(descriptor, { baseUrl: '/api', ...options });
        }),
      ],
    });
    http = TestBed.inject(HttpTestingController);
    router = TestBed.inject(Router);
    await router.navigateByUrl('/deck');
    client = TestBed.inject(RESOURCE_CLIENT);
  });

  it('hands the factory the transport, the error hook, and the release, and provides the error handler', () => {
    expect(typeof received?.transport).toBe('function');
    expect(typeof received?.onError).toBe('function');
    expect(received?.apiVersion).toBe('');
    expect(TestBed.inject(ErrorHandler)).toBeInstanceOf(ResourceErrorHandler);
  });

  it("hands the factory the live session's XSRF token source, which reads the cookie HttpClient's configuration names", () => {
    const xsrf = received?.live?.xsrf;
    expect(xsrf && 'token' in xsrf).toBe(true);
    if (!xsrf || !('token' in xsrf)) return;
    // HttpClient's default cookie, as this TestBed configures it.
    document.cookie = 'XSRF-TOKEN=from-the-cookie; path=/';
    try {
      expect(xsrf.token()).toBe('from-the-cookie');
    } finally {
      document.cookie = 'XSRF-TOKEN=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/';
    }
  });

  it('a request of the client carries no release header when the build names none', () => {
    const pending = client.request('GET', 'missions');
    const request = http.expectOne('/api/missions');
    expect(request.request.headers.has(apiVersionHeader)).toBe(false);
    request.flush([]);
    return pending;
  });

  describe('the handler in effect', () => {
    @Injectable()
    class ApplicationErrorHandler extends ResourceErrorHandler {}

    const cases: { name: string; later: Provider[]; wantError: RegExp | undefined }[] = [
      {
        name: "a later provider re-providing Angular's default handler fails at startup naming the way out",
        later: [{ provide: ErrorHandler, useClass: ErrorHandler }],
        wantError: /ErrorHandler[\s\S]*provideAnimationsAsync\(\)[\s\S]*provideResourceClient/,
      },
      {
        name: "an application's own handler extending ResourceErrorHandler is accepted",
        later: [{ provide: ErrorHandler, useClass: ApplicationErrorHandler }],
        wantError: undefined,
      },
    ];

    for (const tt of cases) {
      it(tt.name, () => {
        TestBed.resetTestingModule();
        TestBed.configureTestingModule({
          providers: [
            provideHttpClient(),
            provideHttpClientTesting(),
            provideRouter([]),
            provideResourceClient((options) => createClient(descriptor, { baseUrl: '/api', ...options })),
            ...tt.later,
          ],
        });
        if (tt.wantError) {
          expect(() => TestBed.inject(RESOURCE_CLIENT)).toThrowError(tt.wantError);
        } else {
          expect(TestBed.inject(RESOURCE_CLIENT)).toBeDefined();
          expect(TestBed.inject(ErrorHandler)).toBeInstanceOf(ApplicationErrorHandler);
        }
      });
    }
  });

  describe('the hook on a refused request', () => {
    const cases: { name: string; status: number; body?: unknown; wantRedirect: string; wantNavigations: string[][] }[] = [
      {
        name: 'a 401 keeps the attempted URL under BASE_URL and moves to the login route',
        status: 401,
        wantRedirect: 'https://console.example/deck',
        wantNavigations: [['/login']],
      },
      {
        name: 'a 403 carrying password_change_required does the same: the login page serves the change form',
        status: 403,
        body: { message: 'Password change required', code: 'password_change_required' },
        wantRedirect: 'https://console.example/deck',
        wantNavigations: [['/login']],
      },
      { name: 'a 403 leaves the router and the redirect alone', status: 403, wantRedirect: '', wantNavigations: [] },
      { name: 'a 500 leaves the router and the redirect alone', status: 500, wantRedirect: '', wantNavigations: [] },
    ];

    for (const tt of cases) {
      it(tt.name, async () => {
        const navigate = vi.spyOn(router, 'navigate').mockResolvedValue(true);
        const pending = client.request('GET', 'missions');
        http
          .expectOne('/api/missions')
          .flush(tt.body ?? { message: 'refused' }, { status: tt.status, statusText: 'refused' });
        await expect(pending).rejects.toThrow(ApiError);
        expect(TestBed.inject(LOGIN_REDIRECT_URL)()).toBe(tt.wantRedirect);
        expect(navigate.mock.calls.map((args) => args[0])).toEqual(tt.wantNavigations);
      });
    }
  });

  it('a 401 on the login page itself moves nothing and keeps no URL', async () => {
    await router.navigateByUrl('/login');
    const navigate = vi.spyOn(router, 'navigate').mockResolvedValue(true);
    const pending = client.request('GET', 'missions');
    http.expectOne('/api/missions').flush({ message: 'refused' }, { status: 401, statusText: 'Unauthorized' });
    await expect(pending).rejects.toThrow(ApiError);
    expect(TestBed.inject(LOGIN_REDIRECT_URL)()).toBe('');
    expect(navigate).not.toHaveBeenCalled();
  });

  it('a declared answer resolves, moves nothing, and raises no notice', async () => {
    const navigate = vi.spyOn(router, 'navigate').mockResolvedValue(true);
    const pending = client.request<{ fee: number }>('POST', 'complete-mission', { body: {}, accept: [409] });
    http.expectOne('/api/complete-mission').flush({ fee: 3 }, { status: 409, statusText: 'Conflict' });
    await expect(pending).resolves.toEqual({ fee: 3 });
    expect(navigate).not.toHaveBeenCalled();
    expect(messages()).toEqual([]);
  });

  it('a refusal the caller catches raises no notice', async () => {
    const pending = client.request('DELETE', 'missions/1').catch((error: unknown) => error);
    http.expectOne('/api/missions/1').flush({ message: 'Ship still docked' }, { status: 409, statusText: 'Conflict' });
    const caught = await pending;
    expect(caught).toBeInstanceOf(ApiError);
    expect(messages()).toEqual([]);
  });
});

class RecordingHandler implements VersionRefusalHandler {
  refusals: ApiVersionError[] = [];

  versionRefused(refusal: ApiVersionError): void {
    this.refusals.push(refusal);
  }
}

/** Lets the client's retry pause and the next request be issued. */
async function settle(): Promise<void> {
  for (let i = 0; i < 3; i++) {
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
}

describe('the release a build names', () => {
  let http: HttpTestingController;
  let handler: RecordingHandler;
  let client: ClientBase;

  function configure(apiVersion: string, withHandler = true): void {
    handler = new RecordingHandler();
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withInterceptors([apiVersionInterceptor])),
        provideHttpClientTesting(),
        provideRouter([{ path: 'login', component: BlankComponent }]),
        { provide: API_VERSION, useValue: apiVersion },
        ...(withHandler ? [{ provide: VERSION_REFUSAL_HANDLER, useValue: handler }] : []),
        provideResourceClient((options) =>
          createClient(descriptor, { baseUrl: '/api', ...options, olderServerRetryDelays: [0, 0, 0] }),
        ),
      ],
    });
    http = TestBed.inject(HttpTestingController);
  }

  it('rides every request of the client in the version header', () => {
    configure('1.5.0');
    client = TestBed.inject(RESOURCE_CLIENT);
    const pending = client.request('GET', 'missions');
    const request = http.expectOne('/api/missions');
    expect(request.request.headers.get(apiVersionHeader)).toBe('1.5.0');
    request.flush([]);
    return pending;
  });

  describe('the interceptor', () => {
    const cases: { name: string; apiVersion: string; url: string; headers?: Record<string, string>; want: string | null }[] = [
      { name: 'a same-origin path gets the header', apiVersion: '1.5.0', url: '/console/api/user/login', want: '1.5.0' },
      { name: 'a relative path gets the header', apiVersion: '1.5.0', url: 'api/impersonate', want: '1.5.0' },
      {
        name: 'an absolute URL on the same origin gets the header',
        apiVersion: '1.5.0',
        url: `${document.location.origin}/console/api/impersonations`,
        want: '1.5.0',
      },
      { name: 'another origin is left alone', apiVersion: '1.5.0', url: 'https://directory.example/token', want: null },
      { name: 'the dev build adds nothing', apiVersion: 'dev', url: '/console/api/user/login', want: null },
      { name: 'a build naming no release adds nothing', apiVersion: '', url: '/console/api/user/login', want: null },
      {
        name: 'a header already on the request is kept',
        apiVersion: '1.5.0',
        url: '/console/api/user/login',
        headers: { [apiVersionHeader]: '1.4.9' },
        want: '1.4.9',
      },
    ];

    for (const tt of cases) {
      it(tt.name, () => {
        configure(tt.apiVersion);
        const pending = TestBed.inject(HttpClient).post(tt.url, {}, { headers: tt.headers }).subscribe();
        const request = http.expectOne(tt.url);
        expect(request.request.headers.get(apiVersionHeader)).toBe(tt.want);
        request.flush({});
        pending.unsubscribe();
      });
    }
  });

  describe('the refusal', () => {
    it('a 412 carrying the server\'s release goes to the handler, moves nothing, and raises no notice', async () => {
      configure('1.5.0');
      client = TestBed.inject(RESOURCE_CLIENT);
      const navigate = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
      const pending = client.request('GET', 'missions').catch((error: unknown) => error);
      http
        .expectOne('/api/missions')
        .flush({ message: 'refused' }, { status: 412, statusText: 'Precondition Failed', headers: { [apiVersionHeader]: '1.6.0' } });
      const error = (await pending) as ApiVersionError;
      expect(error).toBeInstanceOf(ApiVersionError);
      expect(error.serverVersion).toBe('1.6.0');
      expect(error.serverOlder).toBe(false);
      expect(handler.refusals).toEqual([error]);
      expect(navigate).not.toHaveBeenCalled();
      TestBed.inject(ErrorHandler).handleError(error);
      expect(messages()).toEqual([]);
    });

    it('a 412 without the server\'s release is an ordinary refusal', async () => {
      configure('1.5.0');
      client = TestBed.inject(RESOURCE_CLIENT);
      const pending = client.request('GET', 'missions').catch((error: unknown) => error);
      http.expectOne('/api/missions').flush({ message: 'stale' }, { status: 412, statusText: 'Precondition Failed' });
      const error = await pending;
      expect(error).toBeInstanceOf(ApiError);
      expect(error).not.toBeInstanceOf(ApiVersionError);
      expect(handler.refusals).toEqual([]);
    });

    it('a server older than the build is retried, then handed to the handler as the older side', async () => {
      configure('1.5.0');
      client = TestBed.inject(RESOURCE_CLIENT);
      const pending = client.request('GET', 'missions').catch((error: unknown) => error);
      for (let attempt = 0; attempt < 4; attempt++) {
        await settle();
        http
          .expectOne('/api/missions')
          .flush({ message: 'refused' }, { status: 412, statusText: 'Precondition Failed', headers: { [apiVersionHeader]: '1.4.0' } });
      }
      const error = (await pending) as ApiVersionError;
      expect(error).toBeInstanceOf(ApiVersionError);
      expect(error.serverOlder).toBe(true);
      expect(handler.refusals).toEqual([error]);
      http.verify();
    });
  });

  describe('startup', () => {
    const cases: { name: string; apiVersion: string; withHandler: boolean; wantError: RegExp | undefined }[] = [
      { name: 'a release with no handler fails naming provideAppUpdate()', apiVersion: '1.5.0', withHandler: false, wantError: /provideAppUpdate\(\)/ },
      { name: 'a release with the handler starts', apiVersion: '1.5.0', withHandler: true, wantError: undefined },
      { name: 'no release needs no handler', apiVersion: '', withHandler: false, wantError: undefined },
      { name: 'the dev build needs no handler', apiVersion: 'dev', withHandler: false, wantError: undefined },
    ];

    for (const tt of cases) {
      it(tt.name, () => {
        // The check runs with the environment's initializers, at the first injection.
        const start = (): ClientBase => {
          configure(tt.apiVersion, tt.withHandler);
          return TestBed.inject(RESOURCE_CLIENT);
        };
        if (tt.wantError) {
          expect(start).toThrowError(tt.wantError);
        } else {
          expect(start()).toBeDefined();
        }
      });
    }
  });
});

class RecordingMaintenanceHandler implements MaintenanceHandler {
  answers: MaintenanceError[] = [];

  maintenanceAnswered(answer: MaintenanceError): void {
    this.answers.push(answer);
  }
}

describe('the maintenance answer', () => {
  let http: HttpTestingController;
  let handler: RecordingMaintenanceHandler;
  let client: ClientBase;

  function configure(withHandler = true): void {
    handler = new RecordingMaintenanceHandler();
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(),
        provideHttpClientTesting(),
        provideRouter([{ path: 'login', component: BlankComponent }]),
        ...(withHandler ? [{ provide: MAINTENANCE_HANDLER, useValue: handler }] : []),
        provideResourceClient((options) => createClient(descriptor, { baseUrl: '/api', ...options })),
      ],
    });
    http = TestBed.inject(HttpTestingController);
    client = TestBed.inject(RESOURCE_CLIENT);
  }

  /** The maintenance server's answer to anything but a navigation: 503, the marker, Retry-After, no body. */
  const maintenance = {
    status: 503,
    statusText: 'Service Unavailable',
    headers: { [maintenanceHeader]: maintenanceHeaderValue, [retryAfterHeader]: '30' },
  };

  it('a 503 carrying the marker goes to the handler with its Retry-After, moves nothing, and raises no notice', async () => {
    configure();
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    const pending = client.request('GET', 'missions').catch((error: unknown) => error);
    http.expectOne('/api/missions').flush(null, maintenance);
    const error = (await pending) as MaintenanceError;
    expect(error).toBeInstanceOf(MaintenanceError);
    expect(error.status).toBe(503);
    expect(error.retryAfter).toBe(30);
    expect(handler.answers).toEqual([error]);
    expect(navigate).not.toHaveBeenCalled();
    TestBed.inject(ErrorHandler).handleError(error);
    expect(messages()).toEqual([]);
  });

  it('a 503 without the marker is an ordinary error: the handler sees nothing and the notice is raised', async () => {
    configure();
    const pending = client.request('GET', 'missions').catch((error: unknown) => error);
    http.expectOne('/api/missions').flush({ message: 'overloaded' }, { status: 503, statusText: 'Service Unavailable' });
    const error = (await pending) as ApiError;
    expect(error).toBeInstanceOf(ApiError);
    expect(error).not.toBeInstanceOf(MaintenanceError);
    expect(handler.answers).toEqual([]);
    TestBed.inject(ErrorHandler).handleError(error);
    expect(messages()).toEqual(['overloaded']);
  });

  it('without a handler the answer goes to nobody and still raises no notice', async () => {
    configure(false);
    const pending = client.request('GET', 'missions').catch((error: unknown) => error);
    http.expectOne('/api/missions').flush(null, maintenance);
    const error = (await pending) as MaintenanceError;
    expect(error).toBeInstanceOf(MaintenanceError);
    TestBed.inject(ErrorHandler).handleError(error);
    expect(messages()).toEqual([]);
  });
});

describe('ResourceErrorHandler', () => {
  const wrapped = (rejection: unknown): Error => Object.assign(new Error('Uncaught (in promise)'), { rejection });

  const cases: { name: string; error: unknown; wantMessages: string[]; wantConsole: boolean }[] = [
    {
      name: "an uncaught ApiError raises one notice in the server's words",
      error: new ApiError('DELETE', '/api/missions/1', 409, { message: 'Ship still docked' }),
      wantMessages: ['Ship still docked'],
      wantConsole: false,
    },
    {
      name: 'a body with no message falls back to the status',
      error: new ApiError('GET', '/api/missions', 500, undefined),
      wantMessages: ['HTTP 500'],
      wantConsole: false,
    },
    {
      name: "zone.js's wrapped promise rejection is unwrapped to the ApiError",
      error: wrapped(new ApiError('PATCH', '/api/missions/1', 403, { message: 'Not your mission' })),
      wantMessages: ['Not your mission'],
      wantConsole: false,
    },
    {
      name: "Angular's own wrapper is unwrapped the same",
      error: Object.assign(new Error('wrapped'), { ngOriginalError: new ApiError('GET', '/api/x', 404, 'gone') }),
      wantMessages: ['gone'],
      wantConsole: false,
    },
    {
      name: 'a 401 raises none: the hook has already returned the browser to the login page',
      error: new ApiError('GET', '/api/missions', 401, { message: 'Unauthorized' }),
      wantMessages: [],
      wantConsole: false,
    },
    {
      name: 'a 403 carrying password_change_required raises none, for the same reason',
      error: new ApiError('GET', '/api/missions', 403, { message: 'Password change required', code: 'password_change_required' }),
      wantMessages: [],
      wantConsole: false,
    },
    {
      name: "the server's refusal of the build's release raises none: the hook has handed it to the update service",
      error: new ApiVersionError('GET', '/api/missions', { message: 'refused' }, '1.6.0', '1.5.0', false),
      wantMessages: [],
      wantConsole: false,
    },
    {
      name: 'the wrapped refusal the same',
      error: wrapped(new ApiVersionError('GET', '/api/missions', undefined, '1.6.0', '1.5.0', false)),
      wantMessages: [],
      wantConsole: false,
    },
    {
      name: "the server's maintenance answer raises none: the hook has handed it to the maintenance service",
      error: new MaintenanceError('GET', '/api/missions', undefined, 30),
      wantMessages: [],
      wantConsole: false,
    },
    {
      name: 'the wrapped maintenance answer the same',
      error: wrapped(new MaintenanceError('GET', '/api/missions', undefined, undefined)),
      wantMessages: [],
      wantConsole: false,
    },
    {
      name: 'a 503 without the marker raises its notice as any other error does',
      error: new ApiError('GET', '/api/missions', 503, undefined),
      wantMessages: ['HTTP 503'],
      wantConsole: false,
    },
    {
      name: 'a request that got no response raises the no-response notice',
      error: new HttpErrorResponse({ status: 0, url: '/api/missions' }),
      wantMessages: [NO_RESPONSE_MESSAGE],
      wantConsole: false,
    },
    {
      name: 'a wrapped no-response failure the same',
      error: wrapped(new HttpErrorResponse({ status: 0, url: '/api/missions' })),
      wantMessages: [NO_RESPONSE_MESSAGE],
      wantConsole: false,
    },
    {
      name: "a response HttpClient judged itself is not the client's and goes to the console",
      error: new HttpErrorResponse({ status: 500, url: '/other' }),
      wantMessages: [],
      wantConsole: true,
    },
    { name: 'any other error goes to the console', error: new Error('boom'), wantMessages: [], wantConsole: true },
    { name: 'a thrown string goes to the console', error: 'boom', wantMessages: [], wantConsole: true },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      TestBed.configureTestingModule({ providers: [{ provide: ErrorHandler, useClass: ResourceErrorHandler }] });
      const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);

      TestBed.inject(ErrorHandler).handleError(tt.error);

      const notifications = TestBed.inject(NotificationService).notifications();
      expect(notifications.map((n) => n.message)).toEqual(tt.wantMessages);
      expect(notifications.every((n) => n.type === AlertType.ERROR)).toBe(true);
      expect(consoleError.mock.calls.length).toBe(tt.wantConsole ? 1 : 0);
    });
  }
});

describe('httpClientTransport', () => {
  it('keeps the response headers on a success and on an error status', async () => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    const transport = httpClientTransport(TestBed.inject(HttpClient));
    const http = TestBed.inject(HttpTestingController);

    const ok = transport({ method: 'GET', url: '/api/missions' });
    http.expectOne('/api/missions').flush([], { headers: { 'Total-Count': '3', Link: '</api/missions?cursor=x>; rel="next"' } });
    expect((await ok).headers).toEqual({ 'total-count': '3', link: '</api/missions?cursor=x>; rel="next"' });

    const refused = transport({ method: 'GET', url: '/api/missions' });
    http
      .expectOne('/api/missions')
      .flush({ message: 'refused' }, { status: 412, statusText: 'Precondition Failed', headers: { [apiVersionHeader]: '1.6.0' } });
    const response = await refused;
    expect(response.status).toBe(412);
    expect(response.headers).toEqual({ [apiVersionHeader.toLowerCase()]: '1.6.0' });
  });

  const cases: { name: string; respond: (request: TestRequest) => void; rejects: boolean }[] = [
    {
      name: 'a 200 ends the activity it began',
      respond: (request) => request.flush({ ok: true }),
      rejects: false,
    },
    {
      name: 'a refusal resolves for the client to judge and ends the activity',
      respond: (request) => request.flush({ message: 'no' }, { status: 403, statusText: 'Forbidden' }),
      rejects: false,
    },
    {
      name: 'no response rejects and ends the activity',
      respond: (request) => request.error(new ProgressEvent('error')),
      rejects: true,
    },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
      const ui = TestBed.inject(UiCoreService);
      const transport = httpClientTransport(TestBed.inject(HttpClient), ui);

      const pending = transport({ method: 'GET', url: '/api/missions' });
      expect(ui.isLoading()).toBe(true);
      tt.respond(TestBed.inject(HttpTestingController).expectOne('/api/missions'));
      if (tt.rejects) {
        await expect(pending).rejects.toBeInstanceOf(HttpErrorResponse);
      } else {
        await pending;
      }
      expect(ui.isLoading()).toBe(false);
    });
  }

  it('two requests for one URL hold the activity until both settle', async () => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    const ui = TestBed.inject(UiCoreService);
    const transport = httpClientTransport(TestBed.inject(HttpClient), ui);
    const http = TestBed.inject(HttpTestingController);

    const first = transport({ method: 'GET', url: '/api/missions' });
    const second = transport({ method: 'GET', url: '/api/missions' });
    const [firstRequest, secondRequest] = http.match('/api/missions');
    firstRequest.flush([]);
    await first;
    expect(ui.isLoading()).toBe(true);
    secondRequest.flush([]);
    await second;
    expect(ui.isLoading()).toBe(false);
  });

  it('without a counter it counts nothing', async () => {
    TestBed.configureTestingModule({ providers: [provideHttpClient(), provideHttpClientTesting()] });
    const transport = httpClientTransport(TestBed.inject(HttpClient));
    const pending = transport({ method: 'GET', url: '/api/missions' });
    TestBed.inject(HttpTestingController).expectOne('/api/missions').flush([]);
    await pending;
    expect(TestBed.inject(UiCoreService).isLoading()).toBe(false);
  });
});

describe("the live session's XSRF token under a configured cookie name", () => {
  it("answers the cookie withXsrfConfiguration names, so the page-leave unsubscribe echoes the application's cookie", () => {
    let received: ResourceClientOptions | undefined;
    TestBed.configureTestingModule({
      providers: [
        provideHttpClient(withXsrfConfiguration({ cookieName: 'staff-xsrf' })),
        provideHttpClientTesting(),
        { provide: BASE_URL, useValue: 'https://console.example' },
        provideResourceClient((options) => {
          received = options;
          return createClient(descriptor, { baseUrl: '/api', ...options });
        }),
      ],
    });
    TestBed.inject(RESOURCE_CLIENT);
    const xsrf = received?.live?.xsrf;
    expect(xsrf && 'token' in xsrf).toBe(true);
    if (!xsrf || !('token' in xsrf)) return;
    document.cookie = 'staff-xsrf=the-staff-token; path=/';
    try {
      expect(xsrf.token()).toBe('the-staff-token');
    } finally {
      document.cookie = 'staff-xsrf=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/';
    }
  });

  it('without an extractor the adapter passes no live options, so the live session reads the default cookie', () => {
    expect(liveClientOptions(null)).toBeUndefined();
  });
});
