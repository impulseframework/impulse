import { TestBed } from '@angular/core/testing';
import { Router } from '@angular/router';
import { ApiDescriptor, createClient } from '@impulseframework/resource';
import { scriptedTransport, ScriptedTransport } from '@impulseframework/resource/testing';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import {
  AlertType,
  IDLE_SESSION_DURATION,
  IDLE_TIMEOUT_REQUIRE_CONFIRMATION,
  IDLE_WARNING_DURATION,
  NotificationMessage,
} from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';
import { firstValueFrom } from 'rxjs';

import { IdleService } from './idle.service';

// The keep-alive belongs to the started service: no session check runs before start(),
// one runs every IDLE_KEEPALIVE_DURATION seconds while it runs, and none after stop(),
// so a logged-out application has no timer due and can settle. A session timed out is ended
// by the sign-out route: the service sends the browser there and ends nothing itself.
//
// The warning is one persistent notice, counted down in place, that any activity ends: the
// application knows the person is back from it and asks for nothing more. The retired
// IDLE_TIMEOUT_REQUIRE_CONFIRMATION changes nothing but one console line.

const descriptor: ApiDescriptor = {
  resources: {},
  methods: {},
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
};

/** The session and what the first authenticated answer loads beside it, each answering. */
function authenticatedServer(): ScriptedTransport {
  return scriptedTransport((request) => {
    switch (request.url) {
      case '/api/user/session':
        return { status: 200, body: { authenticated: true } };
      case '/api/permission-digest':
        return { status: 200, body: {} };
      case '/api/user-domains':
        return { status: 200, body: [] };
      case '/api/features':
        return { status: 200, body: { enabled: [] } };
      default:
        return { status: 404, body: { message: `unscripted ${request.url}` } };
    }
  });
}

describe('IdleService keep-alive', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('checks the session only between start() and stop()', async () => {
    const transport = authenticatedServer();
    TestBed.configureTestingModule({
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
      ],
    });
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    const idle = TestBed.inject(IdleService);
    const sessionChecks = (): number => transport.requests.filter((r) => r.url === '/api/user/session').length;
    const before = sessionChecks();

    await vi.advanceTimersByTimeAsync(90_000);
    expect(sessionChecks()).toBe(before);

    idle.start();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(sessionChecks()).toBe(before + 1);
    await vi.advanceTimersByTimeAsync(30_000);
    expect(sessionChecks()).toBe(before + 2);

    idle.stop();
    await vi.advanceTimersByTimeAsync(90_000);
    expect(sessionChecks()).toBe(before + 2);
  });

  it('a second start() does not double the keep-alive', async () => {
    const transport = authenticatedServer();
    TestBed.configureTestingModule({
      providers: [
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
      ],
    });
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    const idle = TestBed.inject(IdleService);
    const before = transport.requests.filter((r) => r.url === '/api/user/session').length;

    idle.start();
    idle.start();
    await vi.advanceTimersByTimeAsync(30_000);
    expect(transport.requests.filter((r) => r.url === '/api/user/session').length).toBe(before + 1);
    idle.stop();
  });
});

describe('IdleService timing out', () => {
  it('sends the browser to the sign-out route, whose page ends the session, and stops', () => {
    TestBed.configureTestingModule({
      providers: [
        provideResourceTesting({
          transport: authenticatedServer(),
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
      ],
    });
    const navigated = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    const idle = TestBed.inject(IdleService);
    idle.start();
    idle.logoutAndStop();
    expect(navigated).toHaveBeenCalledWith(['/logout']);
    expect(idle.isActive()).toBe(false);
  });
});

describe('IdleService warning', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  /** A ten-second session whose last four seconds are the warning; the router is a spy. */
  function configure(confirmation = false): {
    idle: IdleService;
    notices: () => NotificationMessage[];
    navigated: ReturnType<typeof vi.spyOn>;
  } {
    TestBed.configureTestingModule({
      providers: [
        provideResourceTesting({ transport: authenticatedServer() }),
        { provide: IDLE_SESSION_DURATION, useValue: 10 },
        { provide: IDLE_WARNING_DURATION, useValue: 4 },
        { provide: IDLE_TIMEOUT_REQUIRE_CONFIRMATION, useValue: confirmation },
      ],
    });
    const navigated = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    const notifications = TestBed.inject(NotificationService);
    return { idle: TestBed.inject(IdleService), notices: () => notifications.notifications(), navigated };
  }

  it('shows one persistent notice through the warning and counts it down in place', async () => {
    const { idle, notices } = configure();
    idle.start();

    await vi.advanceTimersByTimeAsync(5_000);
    expect(notices()).toEqual([]);

    await vi.advanceTimersByTimeAsync(1_000);
    expect(notices()).toHaveLength(1);
    expect(notices()[0]).toMatchObject({
      type: AlertType.IDLE,
      persistent: true,
      message: 'You will be logged out in 4 seconds due to inactivity.',
    });
    const id = notices()[0].id;

    await vi.advanceTimersByTimeAsync(2_000);
    expect(notices()).toHaveLength(1);
    expect(notices()[0]).toMatchObject({
      id,
      persistent: true,
      message: 'You will be logged out in 2 seconds due to inactivity.',
    });
    idle.stop();
  });

  it('ends the warning on the next tick after any activity', async () => {
    const { idle, notices } = configure();
    idle.start();
    await vi.advanceTimersByTimeAsync(7_000);
    expect(notices()).toHaveLength(1);

    document.body.dispatchEvent(new Event('mousemove', { bubbles: true }));
    await vi.advanceTimersByTimeAsync(1_000);
    expect(notices()).toEqual([]);

    // The person is back: the session runs its full length again before the next warning.
    await vi.advanceTimersByTimeAsync(4_000);
    expect(notices()).toEqual([]);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(notices()).toHaveLength(1);
    idle.stop();
  });

  it('closing the notice is a click, which ends the warning too', async () => {
    const { idle, notices } = configure();
    idle.start();
    await vi.advanceTimersByTimeAsync(7_000);
    const notifications = TestBed.inject(NotificationService);
    // What the alert's close does: the click reaches the document's capture listener first, then the notice goes.
    document.body.dispatchEvent(new Event('click', { bubbles: true }));
    notifications.dismissGlobalNotification(notices()[0]);

    await vi.advanceTimersByTimeAsync(1_000);
    expect(notices()).toEqual([]);
    await vi.advanceTimersByTimeAsync(4_000);
    expect(notices()).toEqual([]);
    await vi.advanceTimersByTimeAsync(1_000);
    expect(notices()).toHaveLength(1);
    idle.stop();
  });

  it('signs out at the end of the session, the warning replaced by the signed-out notice', async () => {
    const { idle, notices, navigated } = configure();
    idle.start();
    await vi.advanceTimersByTimeAsync(10_000);

    expect(navigated).toHaveBeenCalledWith(['/logout']);
    expect(idle.isActive()).toBe(false);
    expect(notices()).toHaveLength(1);
    expect(notices()[0]).toMatchObject({
      type: AlertType.IDLE,
      message: 'You have been logged out due to inactivity.',
    });
  });

  it('the retired confirmation token changes nothing but one console line', async () => {
    const warned = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const { idle, notices } = configure(true);
    idle.start();
    idle.stop();
    idle.start();
    expect(warned).toHaveBeenCalledTimes(1);
    expect(warned.mock.calls[0][0]).toContain('IDLE_TIMEOUT_REQUIRE_CONFIRMATION is retired');

    await vi.advanceTimersByTimeAsync(7_000);
    expect(notices()).toHaveLength(1);
    document.body.dispatchEvent(new Event('mousemove', { bubbles: true }));
    await vi.advanceTimersByTimeAsync(1_000);
    expect(notices()).toEqual([]);
    idle.stop();
    warned.mockRestore();
  });
});
