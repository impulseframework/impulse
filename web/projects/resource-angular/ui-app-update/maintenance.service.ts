import { DestroyRef, DOCUMENT, inject, Injectable, NgZone, signal, Signal } from '@angular/core';
import { MaintenanceError, maintenanceHeader, maintenanceHeaderValue } from '@impulseframework/resource';
import { AlertType, MaintenanceHandler } from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';
import { AppUpdateService } from './app-update.service';

/** The notice raised while the server is down for maintenance; the library's words, never the server's. */
export const MAINTENANCE_MESSAGE = 'This application is down for maintenance. It comes back by itself when the work is done.';

/** The wait before the first check-back when the server named no Retry-After: fifteen seconds. */
export const MAINTENANCE_CHECK_START = 15 * 1000;

/** The longest wait between two check-backs: two minutes. */
export const MAINTENANCE_CHECK_CAP = 2 * 60 * 1000;

/**
 * The request header that takes the check-back past the service worker, so the worker
 * never answers it from its cache of the application's files; the worker ignores its value.
 */
export const WORKER_BYPASS_HEADER = 'ngsw-bypass';

/**
 * The one request the maintenance check-back makes: a HEAD of the application's own entry
 * document under its base href, the request the maintenance page's own check-back makes.
 * It needs no session and no database and carries no body either way: a maintenance
 * server answers it 503 with the marker, since it is not a navigation, and the
 * application's server answers it with the document's headers. It is sent past the
 * service worker (WORKER_BYPASS_HEADER, so the worker never answers it from its cache of
 * the application's files) and past the browser's cache. A spec provides its own in its
 * place to script the outage.
 */
@Injectable({ providedIn: 'root' })
export class MaintenanceProbe {
  private readonly document = inject(DOCUMENT);

  /** The URL the check-back asks: the document's base URI, the entry document under the base href. */
  get url(): string {
    return this.document.baseURI;
  }

  /**
   * Asks the server once whether it is still down: true for a 503 carrying the marker and
   * for no answer at all (the server is between revisions), false for any other answer,
   * whatever its status.
   */
  async stillDown(): Promise<boolean> {
    try {
      const response = await globalThis.fetch(this.url, {
        method: 'HEAD',
        cache: 'no-store',
        credentials: 'same-origin',
        headers: { [WORKER_BYPASS_HEADER]: '1' },
      });
      return response.status === 503 && response.headers.get(maintenanceHeader) === maintenanceHeaderValue;
    } catch {
      return true;
    }
  }
}

/**
 * Tells the person when the server is down for maintenance and takes the notice down by
 * itself when the server is back. The client's error hook hands it every maintenance answer
 * (a 503 carrying the maintenance marker, MaintenanceError) through MAINTENANCE_HANDLER,
 * which provideAppUpdate() registers it as. The first answer of an outage raises one
 * persistent notice, MAINTENANCE_MESSAGE, with no action, since there is nothing the
 * person can do; the answers that follow while the notice is up change nothing. While the
 * notice is up the service checks back through MaintenanceProbe: the first check after the
 * server's Retry-After (MAINTENANCE_CHECK_START when it named none), each check that finds
 * the server still down doubling the wait up to MAINTENANCE_CHECK_CAP, no check while the
 * tab is hidden, and one as soon as it is visible again, with the timer outside Angular's
 * zone so it never keeps the application from settling. When the server answers without
 * the marker, whatever the status, the notice is dismissed and AppUpdateService.checkNow()
 * runs, so a new build installed meanwhile raises the update notice; the next request of
 * the client is answered, or refused under the release rule, which does the rest.
 */
@Injectable({ providedIn: 'root' })
export class MaintenanceService implements MaintenanceHandler {
  private readonly notifications = inject(NotificationService);
  private readonly updates = inject(AppUpdateService);
  private readonly probe = inject(MaintenanceProbe);
  private readonly zone = inject(NgZone);
  private readonly document = inject(DOCUMENT);

  private readonly downSignal = signal(false);
  /** True from the first maintenance answer of an outage until a check-back finds the server answering again. */
  readonly down: Signal<boolean> = this.downSignal.asReadonly();

  private noticeId: number | undefined;
  private wait = MAINTENANCE_CHECK_START;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private checking = false;

  constructor() {
    inject(DestroyRef).onDestroy(() => this.stopChecking());
  }

  /**
   * The server answered a request with maintenance. The first answer of an outage raises
   * the notice and starts the check-back at the answer's Retry-After; a later one changes nothing.
   */
  maintenanceAnswered(answer: MaintenanceError): void {
    if (this.downSignal()) {
      return;
    }
    this.downSignal.set(true);
    this.noticeId = this.notifications.addGlobalNotification({
      message: MAINTENANCE_MESSAGE,
      type: AlertType.INFO,
      link: '',
      persistent: true,
    });
    this.wait = firstWait(answer.retryAfter);
    this.zone.runOutsideAngular(() => {
      this.document.addEventListener('visibilitychange', this.onVisibilityChange);
      if (this.visible) {
        this.schedule();
      }
    });
  }

  /** The server answered without the marker: the notice comes down and the update service checks for a new build. */
  private recovered(): void {
    this.stopChecking();
    if (this.noticeId !== undefined) {
      this.notifications.dismissGlobalNotificationById(this.noticeId);
      this.noticeId = undefined;
    }
    this.downSignal.set(false);
    void this.updates.checkNow();
  }

  /** Arms the next check after the current wait, unless one is already armed. */
  private schedule(): void {
    if (this.timer !== undefined) {
      return;
    }
    this.timer = setTimeout(() => {
      this.timer = undefined;
      void this.check();
    }, this.wait);
  }

  /** One check-back: still down doubles the wait and arms the next check while the tab is visible; anything else is the recovery. */
  private async check(): Promise<void> {
    if (this.checking || !this.downSignal()) {
      return;
    }
    this.checking = true;
    try {
      const down = await this.probe.stillDown();
      if (!this.downSignal()) {
        return;
      }
      if (down) {
        this.wait = Math.min(this.wait * 2, MAINTENANCE_CHECK_CAP);
        if (this.visible) {
          this.schedule();
        }
        return;
      }
      this.zone.run(() => this.recovered());
    } finally {
      this.checking = false;
    }
  }

  private stopChecking(): void {
    this.clearTimer();
    this.document.removeEventListener('visibilitychange', this.onVisibilityChange);
  }

  private clearTimer(): void {
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
  }

  private get visible(): boolean {
    return this.document.visibilityState !== 'hidden';
  }

  private readonly onVisibilityChange = (): void => {
    if (!this.downSignal()) {
      return;
    }
    if (this.visible) {
      void this.check();
    } else {
      this.clearTimer();
    }
  };
}

/** The wait before the first check: the server's Retry-After in milliseconds, capped, or the default when it named none or zero. */
function firstWait(retryAfter: number | undefined): number {
  if (retryAfter === undefined || retryAfter <= 0) {
    return MAINTENANCE_CHECK_START;
  }
  return Math.min(retryAfter * 1000, MAINTENANCE_CHECK_CAP);
}
