import { TestBed } from '@angular/core/testing';
import { MaintenanceError, maintenanceHeader, maintenanceHeaderValue } from '@impulseframework/resource';
import { AlertType, MAINTENANCE_HANDLER } from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';

import { AppUpdateService } from './app-update.service';
import {
  MAINTENANCE_CHECK_CAP,
  MAINTENANCE_CHECK_START,
  MAINTENANCE_MESSAGE,
  MaintenanceProbe,
  MaintenanceService,
  WORKER_BYPASS_HEADER,
} from './maintenance.service';
import { provideAppUpdate } from './provide-app-update';

// The maintenance service over a scripted probe: the first maintenance answer of an outage
// raises one persistent notice with no action and the answers that follow add none, the
// check-back starts at the server's Retry-After (the default when it named none), doubles
// after each check that finds the server still down up to the cap, pauses while the tab is
// hidden and checks as soon as it is visible again, and the first answer without the marker
// takes the notice down and asks the update service to check for a new build; the probe
// itself asks for the entry document's headers past the worker and the browser's cache.

class FakeProbe {
  /** What the next checks answer: whether the server is still down. */
  down = true;
  /** The fake clock's time at each check, in milliseconds. */
  checkedAt: number[] = [];

  get checks(): number {
    return this.checkedAt.length;
  }

  async stillDown(): Promise<boolean> {
    this.checkedAt.push(Date.now());
    return this.down;
  }
}

const second = 1000;

let visibility: DocumentVisibilityState = 'visible';

function setVisibility(state: DocumentVisibilityState): void {
  visibility = state;
  document.dispatchEvent(new Event('visibilitychange'));
}

/** The service as provideAppUpdate() provides it, over the fake probe and no service worker, with the update service's checks counted. */
function start(): { service: MaintenanceService; probe: FakeProbe; updateChecks: () => number } {
  const probe = new FakeProbe();
  TestBed.configureTestingModule({
    providers: [{ provide: MaintenanceProbe, useValue: probe }, provideAppUpdate()],
  });
  const checkNow = vi.spyOn(TestBed.inject(AppUpdateService), 'checkNow').mockResolvedValue(false);
  return { service: TestBed.inject(MaintenanceService), probe, updateChecks: () => checkNow.mock.calls.length };
}

/** The server's maintenance answer, as the client raises it. */
function answer(retryAfter: number | undefined): MaintenanceError {
  return new MaintenanceError('GET', '/api/missions', { message: 'Service Unavailable' }, retryAfter);
}

function notices(): { message: string; type: AlertType; persistent?: boolean; action?: unknown }[] {
  return TestBed.inject(NotificationService)
    .notifications()
    .map((n) => ({ message: n.message, type: n.type, persistent: n.persistent, action: n.action }));
}

describe('MaintenanceService', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    visibility = 'visible';
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility });
  });

  afterEach(() => {
    TestBed.resetTestingModule();
    vi.useRealTimers();
    delete (document as { visibilityState?: unknown }).visibilityState;
  });

  it('provideAppUpdate registers the service as the handler of the maintenance answer', () => {
    const { service } = start();
    expect(TestBed.inject(MAINTENANCE_HANDLER)).toBe(service);
  });

  describe('the notice', () => {
    it("the first answer of an outage raises one persistent notice in the library's words with no action, and the answers that follow add none", () => {
      const { service } = start();
      expect(service.down()).toBe(false);
      service.maintenanceAnswered(answer(30));
      service.maintenanceAnswered(answer(30));
      service.maintenanceAnswered(answer(undefined));
      expect(service.down()).toBe(true);
      expect(notices()).toEqual([{ message: MAINTENANCE_MESSAGE, type: AlertType.INFO, persistent: true, action: undefined }]);
      expect(MAINTENANCE_MESSAGE).not.toContain('503');
      expect(MAINTENANCE_MESSAGE).not.toContain('Service Unavailable');
    });
  });

  describe('the check-back', () => {
    const firstWaits: { name: string; retryAfter: number | undefined; want: number }[] = [
      { name: "starts at the server's Retry-After", retryAfter: 30, want: 30 * second },
      { name: 'starts at the default when the server named none', retryAfter: undefined, want: MAINTENANCE_CHECK_START },
      { name: 'starts at the default when the server named zero', retryAfter: 0, want: MAINTENANCE_CHECK_START },
      { name: 'starts at the cap when the server asked for longer', retryAfter: 15 * 60, want: MAINTENANCE_CHECK_CAP },
    ];

    for (const tt of firstWaits) {
      it(tt.name, async () => {
        const { service, probe } = start();
        service.maintenanceAnswered(answer(tt.retryAfter));
        await vi.advanceTimersByTimeAsync(tt.want - 1);
        expect(probe.checks).toBe(0);
        await vi.advanceTimersByTimeAsync(1);
        expect(probe.checks).toBe(1);
      });
    }

    it('doubles the wait after each check that finds the server still down, up to the cap', async () => {
      const { service, probe } = start();
      const begin = Date.now();
      service.maintenanceAnswered(answer(30));
      // 30 s, then 60 s, then 120 s, then 120 s again and again: the cap.
      await vi.advanceTimersByTimeAsync((30 + 60 + 120 + 120 + 120) * second);
      expect(probe.checkedAt.map((at) => (at - begin) / second)).toEqual([30, 90, 210, 330, 450]);
      expect(notices().length).toBe(1);
      expect(service.down()).toBe(true);
    });

    it('a second answer while the notice is up neither restarts nor shortens the schedule', async () => {
      const { service, probe } = start();
      const begin = Date.now();
      service.maintenanceAnswered(answer(30));
      await vi.advanceTimersByTimeAsync(30 * second);
      service.maintenanceAnswered(answer(1));
      await vi.advanceTimersByTimeAsync(60 * second);
      expect(probe.checkedAt.map((at) => (at - begin) / second)).toEqual([30, 90]);
    });

    it('pauses while the tab is hidden, checks as soon as it is visible again, and goes on doubling from there', async () => {
      const { service, probe } = start();
      service.maintenanceAnswered(answer(30));
      await vi.advanceTimersByTimeAsync(30 * second);
      expect(probe.checks).toBe(1);

      setVisibility('hidden');
      await vi.advanceTimersByTimeAsync(10 * 60 * second);
      expect(probe.checks).toBe(1);

      setVisibility('visible');
      await vi.advanceTimersByTimeAsync(0);
      expect(probe.checks).toBe(2);
      await vi.advanceTimersByTimeAsync(120 * second - 1);
      expect(probe.checks).toBe(2);
      await vi.advanceTimersByTimeAsync(1);
      expect(probe.checks).toBe(3);
    });

    it('a tab hidden when the answer comes checks nothing until it is visible', async () => {
      visibility = 'hidden';
      const { service, probe } = start();
      service.maintenanceAnswered(answer(30));
      await vi.advanceTimersByTimeAsync(10 * 60 * second);
      expect(probe.checks).toBe(0);
      expect(notices().length).toBe(1);
      setVisibility('visible');
      await vi.advanceTimersByTimeAsync(0);
      expect(probe.checks).toBe(1);
    });
  });

  describe('the recovery', () => {
    it('the first answer without the marker takes the notice down, ends the checks, and asks the update service to check for a new build', async () => {
      const { service, probe, updateChecks } = start();
      service.maintenanceAnswered(answer(30));
      await vi.advanceTimersByTimeAsync(30 * second);
      expect(probe.checks).toBe(1);
      expect(updateChecks()).toBe(0);

      probe.down = false;
      await vi.advanceTimersByTimeAsync(60 * second);
      expect(probe.checks).toBe(2);
      expect(notices()).toEqual([]);
      expect(service.down()).toBe(false);
      expect(updateChecks()).toBe(1);

      await vi.advanceTimersByTimeAsync(10 * MAINTENANCE_CHECK_CAP);
      expect(probe.checks).toBe(2);
      expect(vi.getTimerCount()).toBe(0);
    });

    it('a visibility change after the recovery checks nothing', async () => {
      const { service, probe } = start();
      service.maintenanceAnswered(answer(30));
      probe.down = false;
      await vi.advanceTimersByTimeAsync(30 * second);
      expect(probe.checks).toBe(1);
      setVisibility('hidden');
      setVisibility('visible');
      await vi.advanceTimersByTimeAsync(0);
      expect(probe.checks).toBe(1);
    });

    it('a later outage raises a new notice and starts the schedule again at its own Retry-After', async () => {
      const { service, probe, updateChecks } = start();
      service.maintenanceAnswered(answer(30));
      probe.down = false;
      await vi.advanceTimersByTimeAsync(30 * second);
      expect(notices()).toEqual([]);

      probe.down = true;
      service.maintenanceAnswered(answer(5));
      expect(notices().map((n) => n.message)).toEqual([MAINTENANCE_MESSAGE]);
      expect(service.down()).toBe(true);
      await vi.advanceTimersByTimeAsync(5 * second);
      expect(probe.checks).toBe(2);
      await vi.advanceTimersByTimeAsync(10 * second);
      expect(probe.checks).toBe(3);
      expect(updateChecks()).toBe(1);
    });
  });
});

describe('MaintenanceProbe', () => {
  const cases: { name: string; respond: () => Promise<Response>; want: boolean }[] = [
    {
      name: 'a 503 carrying the marker is the server still down',
      respond: () => Promise.resolve(new Response(null, { status: 503, headers: { [maintenanceHeader]: maintenanceHeaderValue } })),
      want: true,
    },
    {
      name: 'a 503 without the marker is not maintenance',
      respond: () => Promise.resolve(new Response(null, { status: 503 })),
      want: false,
    },
    {
      name: 'the document is the server back',
      respond: () => Promise.resolve(new Response(null, { status: 200 })),
      want: false,
    },
    {
      name: 'any other status is the server back',
      respond: () => Promise.resolve(new Response(null, { status: 412 })),
      want: false,
    },
    {
      name: 'no answer at all is the server still down: it is between revisions',
      respond: () => Promise.reject(new TypeError('Failed to fetch')),
      want: true,
    },
  ];

  for (const tt of cases) {
    it(`${tt.name}, asked as a HEAD of the entry document past the worker and the cache`, async () => {
      const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(() => tt.respond());
      try {
        TestBed.configureTestingModule({});
        const probe = TestBed.inject(MaintenanceProbe);
        await expect(probe.stillDown()).resolves.toBe(tt.want);
        expect(fetch).toHaveBeenCalledTimes(1);
        const [url, init] = fetch.mock.calls[0];
        expect(url).toBe(document.baseURI);
        expect(init).toEqual({
          method: 'HEAD',
          cache: 'no-store',
          credentials: 'same-origin',
          headers: { [WORKER_BYPASS_HEADER]: '1' },
        });
      } finally {
        fetch.mockRestore();
        TestBed.resetTestingModule();
      }
    });
  }
});
