import { Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { SwUpdate, UnrecoverableStateEvent, VersionEvent } from '@angular/service-worker';
import { ApiVersionError } from '@impulseframework/resource';
import { VERSION_REFUSAL_HANDLER } from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';
import { Subject } from 'rxjs';

import {
  AppReloader,
  AppUpdateService,
  OUT_OF_DATE_MESSAGE,
  RELOAD_GUARD_KEY,
  RELOAD_LABEL,
  RELOAD_WAIT_LIMIT,
  SERVER_OLDER_MESSAGE,
  UNRECOVERABLE_MESSAGE,
  UPDATE_CHECK_INTERVAL,
  VERSION_READY_MESSAGE,
} from './app-update.service';
import { provideAppUpdate } from './provide-app-update';

// The update service over a fake worker: a ready event raises one persistent notice with
// Reload, Reload goes through the seam, an unrecoverable state raises its notice, an absent
// or disabled worker leaves everything inert, the hourly check runs only while the tab is
// visible, and reloadWhenReady() reloads on the next ready event with no notice, or at once
// when a version is already ready.

class FakeSwUpdate {
  readonly versionUpdates = new Subject<VersionEvent>();
  readonly unrecoverable = new Subject<UnrecoverableStateEvent>();
  isEnabled = true;
  checks = 0;
  /** What the next checks answer: whether a new version was found and is ready. */
  found = false;

  async checkForUpdate(): Promise<boolean> {
    this.checks++;
    return this.found;
  }

  ready(): void {
    this.versionUpdates.next({
      type: 'VERSION_READY',
      currentVersion: { hash: 'old' },
      latestVersion: { hash: 'new' },
    });
  }
}

class FakeReloader {
  reloads = 0;
  /** Every call on the seam, in order. */
  calls: string[] = [];

  reload(): void {
    this.reloads++;
    this.calls.push('reload');
  }

  async unregisterWorkers(): Promise<void> {
    this.calls.push('unregisterWorkers');
  }
}

@Component({ template: '' })
class BlankComponent {}

/** The server's refusal of the build, as the client raises it. */
function refusal(serverVersion: string, appVersion: string): ApiVersionError {
  const serverOlder = serverVersion < appVersion;
  return new ApiVersionError('GET', '/api/missions', { message: 'refused' }, serverVersion, appVersion, serverOlder);
}

let visibility: DocumentVisibilityState = 'visible';

function setVisibility(state: DocumentVisibilityState): void {
  visibility = state;
  document.dispatchEvent(new Event('visibilitychange'));
}

/** The service as provideAppUpdate() starts it, over the given worker (none when null), with a router that has not navigated. */
function start(worker: FakeSwUpdate | null): { service: AppUpdateService; reloader: FakeReloader } {
  TestBed.configureTestingModule({
    providers: [
      ...(worker ? [{ provide: SwUpdate, useValue: worker }] : []),
      { provide: AppReloader, useClass: FakeReloader },
      provideRouter([{ path: 'deck', component: BlankComponent }]),
      provideAppUpdate(),
    ],
  });
  return { service: TestBed.inject(AppUpdateService), reloader: TestBed.inject(AppReloader) as unknown as FakeReloader };
}

/** Puts the first page on screen: one navigation has ended. */
async function onScreen(): Promise<void> {
  await TestBed.inject(Router).navigateByUrl('/deck');
  expect(TestBed.inject(Router).navigated).toBe(true);
}

function notices(): { message: string; persistent?: boolean; action?: { label: string; run: () => void } }[] {
  return TestBed.inject(NotificationService)
    .notifications()
    .map((n) => ({ message: n.message, persistent: n.persistent, action: n.action }));
}

describe('AppUpdateService', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    visibility = 'visible';
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => visibility });
    sessionStorage.clear();
  });

  afterEach(() => {
    TestBed.resetTestingModule();
    vi.useRealTimers();
    delete (document as { visibilityState?: unknown }).visibilityState;
  });

  describe('the notices', () => {
    it('a ready event raises one persistent notice with Reload, and a second ready event adds none', () => {
      const worker = new FakeSwUpdate();
      const { service } = start(worker);
      worker.ready();
      worker.ready();
      expect(service.ready()).toBe(true);
      expect(notices()).toEqual([
        { message: VERSION_READY_MESSAGE, persistent: true, action: expect.objectContaining({ label: RELOAD_LABEL }) },
      ]);
    });

    it('the Reload action reloads through the seam', () => {
      const worker = new FakeSwUpdate();
      const { reloader } = start(worker);
      worker.ready();
      notices()[0].action?.run();
      expect(reloader.reloads).toBe(1);
    });

    it('an unrecoverable state raises its notice with Reload', () => {
      const worker = new FakeSwUpdate();
      const { reloader } = start(worker);
      worker.unrecoverable.next({ type: 'UNRECOVERABLE_STATE', reason: 'a file of this version is gone' });
      expect(notices()).toEqual([
        { message: UNRECOVERABLE_MESSAGE, persistent: true, action: expect.objectContaining({ label: RELOAD_LABEL }) },
      ]);
      notices()[0].action?.run();
      expect(reloader.reloads).toBe(1);
    });

    it('other version events raise nothing', () => {
      const worker = new FakeSwUpdate();
      start(worker);
      worker.versionUpdates.next({ type: 'VERSION_DETECTED', version: { hash: 'new' } });
      worker.versionUpdates.next({ type: 'NO_NEW_VERSION_DETECTED', version: { hash: 'old' } });
      expect(notices()).toEqual([]);
    });
  });

  describe('an absent or disabled worker', () => {
    const cases: { name: string; worker: () => FakeSwUpdate | null }[] = [
      { name: 'no worker provided', worker: () => null },
      {
        name: 'a disabled worker',
        worker: () => {
          const worker = new FakeSwUpdate();
          worker.isEnabled = false;
          return worker;
        },
      },
    ];

    for (const tt of cases) {
      it(`${tt.name} is inert and starts no timer`, async () => {
        const worker = tt.worker();
        const { service, reloader } = start(worker);
        expect(service.enabled).toBe(false);
        expect(vi.getTimerCount()).toBe(0);
        await vi.advanceTimersByTimeAsync(2 * UPDATE_CHECK_INTERVAL);
        expect(worker?.checks ?? 0).toBe(0);
        await expect(service.checkNow()).resolves.toBe(false);
        service.reloadWhenReady();
        expect(reloader.reloads).toBe(0);
        expect(notices()).toEqual([]);
      });
    }
  });

  describe('the checks', () => {
    it('run hourly while the tab is visible, stop while it is hidden, and run once when it is visible again', async () => {
      const worker = new FakeSwUpdate();
      start(worker);
      expect(worker.checks).toBe(0);

      await vi.advanceTimersByTimeAsync(UPDATE_CHECK_INTERVAL);
      expect(worker.checks).toBe(1);
      await vi.advanceTimersByTimeAsync(UPDATE_CHECK_INTERVAL);
      expect(worker.checks).toBe(2);

      setVisibility('hidden');
      await vi.advanceTimersByTimeAsync(3 * UPDATE_CHECK_INTERVAL);
      expect(worker.checks).toBe(2);

      setVisibility('visible');
      await vi.advanceTimersByTimeAsync(0);
      expect(worker.checks).toBe(3);
      await vi.advanceTimersByTimeAsync(UPDATE_CHECK_INTERVAL);
      expect(worker.checks).toBe(4);
    });

    it('a tab hidden at start runs no timer until it is visible', async () => {
      visibility = 'hidden';
      const worker = new FakeSwUpdate();
      start(worker);
      await vi.advanceTimersByTimeAsync(2 * UPDATE_CHECK_INTERVAL);
      expect(worker.checks).toBe(0);
      setVisibility('visible');
      await vi.advanceTimersByTimeAsync(0);
      expect(worker.checks).toBe(1);
    });

    it('checkNow answers what the worker found and swallows a failed check', async () => {
      const worker = new FakeSwUpdate();
      const { service } = start(worker);
      await expect(service.checkNow()).resolves.toBe(false);
      worker.found = true;
      await expect(service.checkNow()).resolves.toBe(true);
      worker.checkForUpdate = () => Promise.reject(new Error('offline'));
      await expect(service.checkNow()).resolves.toBe(false);
    });
  });

  describe('the version refusal', () => {
    it('provideAppUpdate registers the service as the handler', () => {
      const { service } = start(new FakeSwUpdate());
      expect(TestBed.inject(VERSION_REFUSAL_HANDLER)).toBe(service);
    });

    describe('while the application is still starting', () => {
      it('checks, then reloads onto the new build on the ready event, with no notice', async () => {
        const worker = new FakeSwUpdate();
        const { service, reloader } = start(worker);
        service.versionRefused(refusal('1.6.0', '1.5.0'));
        await vi.advanceTimersByTimeAsync(0);
        expect(worker.checks).toBe(1);
        expect(reloader.reloads).toBe(0);
        worker.ready();
        expect(reloader.reloads).toBe(1);
        expect(notices()).toEqual([]);
        expect(sessionStorage.getItem(RELOAD_GUARD_KEY)).toBe('1.6.0');
        await vi.advanceTimersByTimeAsync(RELOAD_WAIT_LIMIT);
        expect(reloader.reloads).toBe(1);
      });

      it('reloads plainly when no new build is ready within the limit', async () => {
        const worker = new FakeSwUpdate();
        const { service, reloader } = start(worker);
        service.versionRefused(refusal('1.6.0', '1.5.0'));
        await vi.advanceTimersByTimeAsync(RELOAD_WAIT_LIMIT - 1);
        expect(reloader.reloads).toBe(0);
        await vi.advanceTimersByTimeAsync(1);
        expect(reloader.reloads).toBe(1);
        expect(reloader.calls).toEqual(['reload']);
        expect(notices()).toEqual([]);
      });

      it('reloads at once when a new version is already ready', async () => {
        const worker = new FakeSwUpdate();
        const { service, reloader } = start(worker);
        worker.ready();
        service.versionRefused(refusal('1.6.0', '1.5.0'));
        expect(reloader.reloads).toBe(1);
      });

      const noWorker: { name: string; worker: () => FakeSwUpdate | null }[] = [
        { name: 'no worker', worker: () => null },
        {
          name: 'a disabled worker',
          worker: () => {
            const worker = new FakeSwUpdate();
            worker.isEnabled = false;
            return worker;
          },
        },
      ];

      for (const tt of noWorker) {
        it(`with ${tt.name} it reloads plainly at once`, () => {
          const { service, reloader } = start(tt.worker());
          service.versionRefused(refusal('1.6.0', '1.5.0'));
          expect(reloader.calls).toEqual(['reload']);
          expect(notices()).toEqual([]);
          expect(sessionStorage.getItem(RELOAD_GUARD_KEY)).toBe('1.6.0');
        });
      }

      it('several refusals at once reload once', async () => {
        const { service, reloader } = start(null);
        service.versionRefused(refusal('1.6.0', '1.5.0'));
        service.versionRefused(refusal('1.6.0', '1.5.0'));
        service.versionRefused(refusal('1.6.0', '1.5.0'));
        expect(reloader.reloads).toBe(1);
      });
    });

    describe('once a page is on screen', () => {
      it('raises one persistent notice with Reload and reloads nothing by itself', async () => {
        const worker = new FakeSwUpdate();
        const { service, reloader } = start(worker);
        await onScreen();
        service.versionRefused(refusal('1.6.0', '1.5.0'));
        service.versionRefused(refusal('1.6.0', '1.5.0'));
        await vi.advanceTimersByTimeAsync(RELOAD_WAIT_LIMIT);
        expect(reloader.reloads).toBe(0);
        expect(worker.checks).toBe(0);
        expect(notices()).toEqual([
          { message: OUT_OF_DATE_MESSAGE, persistent: true, action: expect.objectContaining({ label: RELOAD_LABEL }) },
        ]);
        expect(sessionStorage.getItem(RELOAD_GUARD_KEY)).toBeNull();
        notices()[0].action?.run();
        expect(reloader.calls).toEqual(['reload']);
      });
    });

    describe('the loop guard', () => {
      it('a tab that already reloaded by itself for this server release raises the notice instead', () => {
        sessionStorage.setItem(RELOAD_GUARD_KEY, '1.6.0');
        const { service, reloader } = start(null);
        service.versionRefused(refusal('1.6.0', '1.5.0'));
        expect(reloader.reloads).toBe(0);
        expect(notices().map((n) => n.message)).toEqual([OUT_OF_DATE_MESSAGE]);
      });

      it('a newer server release gets its own automatic reload', () => {
        sessionStorage.setItem(RELOAD_GUARD_KEY, '1.6.0');
        const { service, reloader } = start(null);
        service.versionRefused(refusal('1.7.0', '1.5.0'));
        expect(reloader.reloads).toBe(1);
        expect(sessionStorage.getItem(RELOAD_GUARD_KEY)).toBe('1.7.0');
      });

      it('storage that throws neither stops the reload nor leaks', () => {
        const getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
          throw new Error('storage disabled');
        });
        const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
          throw new Error('storage disabled');
        });
        try {
          const { service, reloader } = start(null);
          service.versionRefused(refusal('1.6.0', '1.5.0'));
          expect(reloader.reloads).toBe(1);
        } finally {
          getItem.mockRestore();
          setItem.mockRestore();
        }
      });
    });

    describe('a server older than the build', () => {
      const cases: { name: string; screen: boolean }[] = [
        { name: 'while starting', screen: false },
        { name: 'once a page is on screen', screen: true },
      ];

      for (const tt of cases) {
        it(`${tt.name} it unregisters the workers, then reloads onto the server's build`, async () => {
          const { service, reloader } = start(new FakeSwUpdate());
          if (tt.screen) {
            await onScreen();
          }
          service.versionRefused(refusal('1.4.0', '1.5.0'));
          await vi.advanceTimersByTimeAsync(0);
          expect(reloader.calls).toEqual(['unregisterWorkers', 'reload']);
          expect(notices()).toEqual([]);
          expect(sessionStorage.getItem(RELOAD_GUARD_KEY)).toBe('1.4.0');
        });
      }

      it('past the guard it raises its notice, whose Reload unregisters the workers first', async () => {
        sessionStorage.setItem(RELOAD_GUARD_KEY, '1.4.0');
        const { service, reloader } = start(new FakeSwUpdate());
        service.versionRefused(refusal('1.4.0', '1.5.0'));
        await vi.advanceTimersByTimeAsync(0);
        expect(reloader.calls).toEqual([]);
        expect(notices().map((n) => n.message)).toEqual([SERVER_OLDER_MESSAGE]);
        notices()[0].action?.run();
        await vi.advanceTimersByTimeAsync(0);
        expect(reloader.calls).toEqual(['unregisterWorkers', 'reload']);
      });
    });
  });

  describe('reloadWhenReady', () => {
    it('checks, then reloads on the ready event with no notice', async () => {
      const worker = new FakeSwUpdate();
      const { service, reloader } = start(worker);
      service.reloadWhenReady();
      await vi.advanceTimersByTimeAsync(0);
      expect(worker.checks).toBe(1);
      expect(reloader.reloads).toBe(0);
      worker.ready();
      expect(reloader.reloads).toBe(1);
      expect(notices()).toEqual([]);
    });

    it('reloads at once when a version is already ready, with no further check', async () => {
      const worker = new FakeSwUpdate();
      const { service, reloader } = start(worker);
      worker.ready();
      expect(notices().length).toBe(1);
      service.reloadWhenReady();
      await vi.advanceTimersByTimeAsync(0);
      expect(reloader.reloads).toBe(1);
      expect(worker.checks).toBe(0);
    });
  });
});
