import { computed, inject, Injectable, signal } from '@angular/core';
import { AlertType, CreateNotificationMessage, LOGIN_MESSAGES } from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';

@Injectable({
  providedIn: 'root',
})
export class UiCoreService {
  notifications = inject(NotificationService);
  loadingSignal = signal([] as string[]);
  sidenavOpened = signal(true);
  currentSidenavIdentifier = signal('');

  private loading = this.loadingSignal.asReadonly();
  isLoading = computed(() => this.loading().length > 0);

  private loginMessages = inject(LOGIN_MESSAGES);

  publishError(message: CreateNotificationMessage): void {
    this.notifications.addGlobalNotification(message);
  }

  /**
   * The sentence the application holds for a login refusal code (`LOGIN_MESSAGES`), or an
   * empty string for a code it does not know. A login page renders this for the `code`
   * query parameter a refused login arrives with, never the parameter itself, and for the
   * `code` in the body of a refusal the server answers on a step, whatever its status. The
   * lookup is by own property, so a key inherited from Object is not a code.
   *
   * `retryAfter` is the wait the server named with the refusal, in seconds: the attempt
   * limiter's `too_many_attempts` answers 429 with it. The sentence is then followed by the
   * wait, "Too many attempts. Try again in 14 minutes.": in minutes from a minute up,
   * rounded up, and in seconds below it. Without a wait, or with one of no time, the
   * sentence stands alone; a code the application does not know answers nothing either way.
   */
  loginMessage(code: string, retryAfter?: number): string {
    if (!Object.hasOwn(this.loginMessages, code)) {
      return '';
    }
    const message = this.loginMessages[code];
    if (typeof message !== 'string' || message === '') {
      return '';
    }
    const wait = waitSentence(retryAfter);
    return wait === '' ? message : `${message} ${wait}`;
  }

  /**
   * Publishes the sentence for a login refusal code as a global error notification, for a
   * login page that reports through the alert rather than inline; `retryAfter`, the wait
   * the server named in seconds, follows the sentence as in `loginMessage`. Nothing is
   * published for a code the application does not know.
   * @returns whether the code was known and its sentence published.
   */
  publishLoginError(code: string, retryAfter?: number): boolean {
    const message = this.loginMessage(code, retryAfter);
    if (message === '') {
      return false;
    }
    this.publishError({ message, type: AlertType.ERROR, link: '' });
    return true;
  }

  beginActivity(process: string): void {
    if (!this.loading().includes(process)) {
      this.loadingSignal.update((current) => [...current, process]);
    }
  }

  endActivity(process: string): void {
    this.loadingSignal.update((current) => current.filter((p) => p !== process));
  }

  toggleSidenav(): void {
    this.sidenavOpened.update((opened) => !opened);
  }
}

/**
 * The sentence for a wait the server named, in seconds: "Try again in 14 minutes." from a
 * minute up, the minutes rounded up, "Try again in 45 seconds." below it, the seconds
 * rounded up; empty for no wait, a wait of no time, or anything that is not a number.
 */
function waitSentence(retryAfter: number | undefined): string {
  if (retryAfter === undefined || !Number.isFinite(retryAfter)) {
    return '';
  }
  const seconds = Math.ceil(retryAfter);
  if (seconds < 1) {
    return '';
  }
  if (seconds < 60) {
    return `Try again in ${seconds} ${seconds === 1 ? 'second' : 'seconds'}.`;
  }
  const minutes = Math.ceil(seconds / 60);
  return `Try again in ${minutes} ${minutes === 1 ? 'minute' : 'minutes'}.`;
}
