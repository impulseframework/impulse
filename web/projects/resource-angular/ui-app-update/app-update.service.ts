import { DestroyRef, DOCUMENT, inject, Injectable, NgZone, signal, Signal } from '@angular/core';
import { Router } from '@angular/router';
import { SwUpdate, VersionEvent } from '@angular/service-worker';
import { ApiVersionError } from '@impulseframework/resource';
import { AlertType, VersionRefusalHandler } from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';

/** The notice raised when the service worker has a new version of the application ready. */
export const VERSION_READY_MESSAGE = 'A new version of this application is ready.';

/** The notice raised when the service worker reports a state only a reload recovers from. */
export const UNRECOVERABLE_MESSAGE = 'This application needs to be reloaded to keep working.';

/** The notice raised when the server no longer answers this build's release and a page is already on screen. */
export const OUT_OF_DATE_MESSAGE = 'A new version of this application is required. Reload to continue.';

/** The notice raised when the server runs an older release than this build and the tab's one automatic reload for it is spent. */
export const SERVER_OLDER_MESSAGE = 'The server runs an older version of this application. Reload to continue.';

/** How often a visible tab asks the service worker to check for a new version: an hour. */
export const UPDATE_CHECK_INTERVAL = 60 * 60 * 1000;

/** How long a refused, still-starting application waits for the worker to have the new build before it reloads anyway: ten seconds. */
export const RELOAD_WAIT_LIMIT = 10 * 1000;

/** The sessionStorage key holding the server release this tab last reloaded for by itself: the loop guard. */
export const RELOAD_GUARD_KEY = 'impulse.app-update.reloaded-for';

/** The label of the action every update notice carries. */
export const RELOAD_LABEL = 'Reload';

/**
 * The one way the library reloads the document. AppUpdateService calls it, the update
 * notices' Reload action calls it, and a spec replaces it to observe a reload instead of
 * losing the page.
 */
@Injectable({ providedIn: 'root' })
export class AppReloader {
  private document = inject(DOCUMENT);

  /** Reloads the document. */
  reload(): void {
    this.document.defaultView?.location.reload();
  }

  /**
   * Unregisters every service worker of this origin, so the next load fetches the
   * server's build instead of the one the worker holds. Resolves once they are gone, or
   * at once where there is no worker; a failure to unregister does not stop the reload.
   */
  async unregisterWorkers(): Promise<void> {
    const container = this.document.defaultView?.navigator?.serviceWorker;
    if (!container) {
      return;
    }
    try {
      const registrations = await container.getRegistrations();
      await Promise.all(registrations.map((registration) => registration.unregister()));
    } catch {
      // The reload goes ahead; the worker, if it survives, serves what it has.
    }
  }
}

/**
 * Tells the person when a new build of the application is ready and reloads only when
 * they choose. It listens to the service worker (`SwUpdate` from @angular/service-worker)
 * and is inert when no worker is provided or the worker is disabled, as it is in dev mode
 * and in component specs. When the worker has a new version ready it raises one persistent
 * notice, VERSION_READY_MESSAGE, with a Reload action; when the worker reports an
 * unrecoverable state it raises UNRECOVERABLE_MESSAGE the same way. Both reload through
 * AppReloader. Checks run hourly while the tab is visible and whenever it becomes visible
 * again, none while it is hidden, with the timer outside Angular's zone so it never keeps
 * the application from settling; the worker itself checks after every page load.
 *
 * `provideAppUpdate()` (provide-app-update.ts) starts it and registers it as the handler
 * of the server's refusal of this build's release (`versionRefused`), the one case in which the application
 * reloads by itself: while it is still starting, before its first navigation has ended,
 * it reloads onto the new build through `reloadWhenReady()`, with a plain reload when
 * there is no worker or no new build within RELOAD_WAIT_LIMIT; once a page is on screen
 * it raises OUT_OF_DATE_MESSAGE with Reload instead. When the server runs an older
 * release than this build (a rollback), the worker would serve this build again, so the
 * workers are unregistered first and the document reloads onto the server's build. A tab
 * reloads by itself at most once per server release, the guard kept in sessionStorage
 * under RELOAD_GUARD_KEY; a refusal past the guard raises the notice instead. The server's
 * maintenance answer is MaintenanceService's, beside it, which calls `checkNow()` here
 * when the server is back.
 */
@Injectable({ providedIn: 'root' })
export class AppUpdateService implements VersionRefusalHandler {
  private readonly worker = inject(SwUpdate, { optional: true });
  private readonly notifications = inject(NotificationService);
  private readonly reloader = inject(AppReloader);
  private readonly zone = inject(NgZone);
  private readonly document = inject(DOCUMENT);
  private readonly router = inject(Router, { optional: true });

  private readonly readySignal = signal(false);
  /** True once the worker has a new version of the application ready to serve on the next load. */
  readonly ready: Signal<boolean> = this.readySignal.asReadonly();

  private reloadOnReady = false;
  private reloading = false;
  private started = false;
  private timer: ReturnType<typeof setInterval> | undefined;

  constructor() {
    if (!this.worker || !this.enabled) {
      return;
    }
    const subscriptions = [
      this.worker.versionUpdates.subscribe((event) => this.onVersionEvent(event)),
      this.worker.unrecoverable.subscribe(() => this.raiseReloadNotice(UNRECOVERABLE_MESSAGE)),
    ];
    inject(DestroyRef).onDestroy(() => {
      subscriptions.forEach((subscription) => subscription.unsubscribe());
      this.stopTimer();
      this.document.removeEventListener('visibilitychange', this.onVisibilityChange);
    });
  }

  /** Whether a service worker is provided and enabled; without one every method here does nothing. */
  get enabled(): boolean {
    return this.worker?.isEnabled === true;
  }

  /**
   * Starts the periodic checks: hourly while the tab is visible, and once whenever it
   * becomes visible again. provideAppUpdate() calls it; a second call does nothing.
   */
  start(): void {
    if (this.started || !this.enabled) {
      return;
    }
    this.started = true;
    this.zone.runOutsideAngular(() => {
      this.document.addEventListener('visibilitychange', this.onVisibilityChange);
      if (this.visible) {
        this.startTimer();
      }
    });
  }

  /**
   * Asks the worker to check for a new version now. Resolves true when one was found and
   * is ready to activate, false when there is none, when the check fails, or when there is
   * no worker.
   */
  async checkNow(): Promise<boolean> {
    if (!this.worker || !this.enabled) {
      return false;
    }
    try {
      return await this.worker.checkForUpdate();
    } catch {
      return false;
    }
  }

  /** Reloads the document through AppReloader. */
  reload(): void {
    this.reloading = true;
    this.reloader.reload();
  }

  /**
   * Reloads onto the new version as soon as the worker has one ready, with no notice: at
   * once when a version is already ready, else after a check, on the ready event. Without
   * a worker nothing happens; the caller decides what a build with no worker does.
   */
  reloadWhenReady(): void {
    if (this.readySignal()) {
      this.reload();
      return;
    }
    if (!this.enabled) {
      return;
    }
    this.reloadOnReady = true;
    void this.checkNow();
  }

  /**
   * The server refused this build's release. See the class comment for what happens; a
   * refusal that arrives while a reload is already under way changes nothing.
   */
  versionRefused(refusal: ApiVersionError): void {
    if (this.reloading) {
      return;
    }
    if (refusal.serverOlder) {
      this.reloadOnce(refusal.serverVersion, SERVER_OLDER_MESSAGE, () => void this.reloadWithoutWorkers());
      return;
    }
    if (this.onScreen) {
      this.raiseReloadNotice(OUT_OF_DATE_MESSAGE);
      return;
    }
    this.reloadOnce(refusal.serverVersion, OUT_OF_DATE_MESSAGE, () => this.reloadOntoNewBuild());
  }

  /** Whether the application's first page is on screen: its router has completed a navigation. Without a router, yes. */
  private get onScreen(): boolean {
    return this.router ? this.router.navigated : true;
  }

  /** Runs the automatic reload unless this tab already reloaded by itself for this server release; then the notice instead. */
  private reloadOnce(serverVersion: string, message: string, run: () => void): void {
    if (this.reloadedFor() === serverVersion) {
      this.raiseReloadNotice(message, run);
      return;
    }
    this.recordReloadFor(serverVersion);
    run();
  }

  /** Reloads onto the new build the worker is fetching, or plainly when there is no worker or it takes too long. */
  private reloadOntoNewBuild(): void {
    if (!this.enabled) {
      this.reload();
      return;
    }
    setTimeout(() => {
      if (!this.reloading) {
        this.reload();
      }
    }, RELOAD_WAIT_LIMIT);
    this.reloadWhenReady();
  }

  private async reloadWithoutWorkers(): Promise<void> {
    await this.reloader.unregisterWorkers();
    this.reload();
  }

  private reloadedFor(): string | undefined {
    try {
      return this.document.defaultView?.sessionStorage.getItem(RELOAD_GUARD_KEY) ?? undefined;
    } catch {
      return undefined;
    }
  }

  private recordReloadFor(serverVersion: string): void {
    try {
      this.document.defaultView?.sessionStorage.setItem(RELOAD_GUARD_KEY, serverVersion);
    } catch {
      // Without storage the guard cannot hold; the reload still happens once per refusal.
    }
  }

  private onVersionEvent(event: VersionEvent): void {
    if (event.type !== 'VERSION_READY') {
      return;
    }
    this.readySignal.set(true);
    if (this.reloadOnReady) {
      if (!this.reloading) {
        this.reload();
      }
      return;
    }
    this.raiseReloadNotice(VERSION_READY_MESSAGE);
  }

  private raiseReloadNotice(message: string, run: () => void = () => this.reload()): void {
    this.notifications.addGlobalNotification({
      message,
      type: AlertType.INFO,
      link: '',
      persistent: true,
      action: { label: RELOAD_LABEL, run },
    });
  }

  private get visible(): boolean {
    return this.document.visibilityState !== 'hidden';
  }

  private readonly onVisibilityChange = (): void => {
    if (this.visible) {
      void this.checkNow();
      this.startTimer();
    } else {
      this.stopTimer();
    }
  };

  private startTimer(): void {
    if (this.timer !== undefined) {
      return;
    }
    this.timer = setInterval(() => {
      void this.checkNow();
    }, UPDATE_CHECK_INTERVAL);
  }

  private stopTimer(): void {
    if (this.timer !== undefined) {
      clearInterval(this.timer);
      this.timer = undefined;
    }
  }
}
