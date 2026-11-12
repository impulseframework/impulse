import { computed, inject, Injectable, OnDestroy, signal, WritableSignal } from '@angular/core';
import { Router } from '@angular/router';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import {
  AlertType,
  FRONTEND_SIGN_OUT_PATH,
  IDLE_KEEPALIVE_DURATION,
  IDLE_LOGOUT_ACTION,
  IDLE_SESSION_DURATION,
  IDLE_TIMEOUT_REQUIRE_CONFIRMATION,
  IDLE_WARNING_DURATION,
} from '@impulseframework/resource-angular/types';
import { UiCoreService } from '@impulseframework/resource-angular/ui-core-service';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';
import { interval, Subscription } from 'rxjs';

@Injectable({
  providedIn: 'root',
})
export class IdleService implements OnDestroy {
  private auth = inject(AuthService);
  private core = inject(UiCoreService);
  private router = inject(Router);
  private notifications = inject(NotificationService);
  private readonly signOutPath = inject(FRONTEND_SIGN_OUT_PATH);

  private readonly sessionDuration = inject(IDLE_SESSION_DURATION);
  private readonly logoutAction = inject(IDLE_LOGOUT_ACTION);
  private readonly warningDuration = inject(IDLE_WARNING_DURATION);
  private readonly keepAliveDuration = inject(IDLE_KEEPALIVE_DURATION);
  private readonly timeoutRequireConfirmation = inject(IDLE_TIMEOUT_REQUIRE_CONFIRMATION);
  private confirmationWarned = false;
  private readonly idleCheckFrequency = 1000;
  private readonly warningThreshold = this.sessionDuration - this.warningDuration;

  public readonly isActive = signal(false);
  private lastActivityTimestamp: WritableSignal<number> = signal(0);
  private tick = signal(Date.now());

  public readonly secondsIdle = computed(() => {
    if (!this.isActive() || this.lastActivityTimestamp() === 0) {
      return 0;
    }
    // Depends on tick() to force re-evaluation as time passes
    return Math.floor((this.tick() - this.lastActivityTimestamp()) / 1000);
  });

  public readonly isWarning = computed(() => this.secondsIdle() >= this.warningThreshold);
  public readonly countdown = computed(() => {
    if (!this.isWarning()) {
      return 0;
    }
    const remaining = this.sessionDuration - this.secondsIdle();
    return Math.max(0, remaining);
  });

  private alertId: number | undefined;
  private mainTickerSubscription: Subscription | undefined;
  private keepAliveSubscription: Subscription | undefined;

  private readonly activityEvents = ['mousedown', 'mousemove', 'keypress', 'scroll', 'touchstart', 'click'];

  ngOnDestroy(): void {
    this.stop();
  }

  /**
   * Starts the idle monitoring service: the idle ticker, the activity listeners, and the
   * keep-alive, which asks the server for the session every IDLE_KEEPALIVE_DURATION seconds
   * while the session is authenticated. Nothing runs before this call.
   */
  start(): void {
    if (this.isActive()) {
      return;
    }
    this.warnIfConfirmationRequested();
    this.isActive.set(true);
    this.lastActivityTimestamp.set(Date.now());
    this.addActivityListeners();
    this.startMainTicker();
    this.startKeepAlive();
  }

  /**
   * Stops the idle monitoring service and cleans up timers and alerts. The keep-alive ends
   * with the ticker, so a logged-out application has no timer due and can settle.
   */
  stop(): void {
    this.isActive.set(false);
    this.mainTickerSubscription?.unsubscribe();
    this.keepAliveSubscription?.unsubscribe();
    this.keepAliveSubscription = undefined;
    this.removeActivityListeners();
    this.dismissWarningAlert();
  }

  /**
   * Logs the user out due to inactivity: runs the configured idle logout action, stops the
   * service and sends the browser to the sign-out route (FRONTEND_SIGN_OUT_PATH), whose page
   * ends the session and forwards to the login page.
   */
  logoutAndStop(): void {
    try {
      this.logoutAction();
    } catch (error) {
      console.error('Error during idle logout action (IDLE_LOGOUT_ACTION token):', error);
    }
    this.stop();

    this.router.navigate([this.signOutPath]);
    this.core.publishError({
      message: 'You have been logged out due to inactivity.',
      type: AlertType.IDLE,
      link: '',
    });
  }

  /**
   * IDLE_TIMEOUT_REQUIRE_CONFIRMATION is retired: activity always ends the warning, since
   * the application knows the person is back from it and asks for nothing more. An
   * application that still provides the token true is told so once, in the console, and
   * is otherwise unaffected.
   */
  private warnIfConfirmationRequested(): void {
    if (!this.timeoutRequireConfirmation || this.confirmationWarned) {
      return;
    }
    this.confirmationWarned = true;
    console.warn(
      'IDLE_TIMEOUT_REQUIRE_CONFIRMATION is retired and has no effect: any activity ends the idle warning. Remove the provider.',
    );
  }

  private startMainTicker(): void {
    this.mainTickerSubscription = interval(this.idleCheckFrequency).subscribe(() => {
      this.tick.set(Date.now());

      if (this.secondsIdle() >= this.sessionDuration) {
        this.logoutAndStop();
        return;
      }

      if (this.isWarning()) {
        this.showOrUpdateWarningAlert(this.countdown());
      } else {
        this.dismissWarningAlert();
      }
    });
  }

  private startKeepAlive(): void {
    this.keepAliveSubscription = interval(this.keepAliveDuration * 1000).subscribe(() => this.checkSession());
  }

  private checkSession(): void {
    if (this.auth.authenticated()) {
      this.auth.checkUserSession().subscribe({
        next: () => {
          if (!this.auth.authenticated()) {
            this.logoutAndStop();
          }
        },
      });
    }
  }

  /**
   * Resets the idle timer and ends the warning at once, as any activity does on the next
   * tick. An application that offers a button for it calls this; none needs to, since a
   * click is activity.
   */
  stayLoggedIn(): void {
    this.setLastActivity();
    this.dismissWarningAlert();
  }

  setLastActivity(): void {
    this.lastActivityTimestamp.set(Date.now());
  }
  boundActivity = this.setLastActivity.bind(this);

  private addActivityListeners(): void {
    this.activityEvents.forEach((event) => {
      document.addEventListener(event, this.boundActivity, true);
    });
  }

  private removeActivityListeners(): void {
    this.activityEvents.forEach((event) => {
      document.removeEventListener(event, this.boundActivity, true);
    });
  }

  /**
   * The warning is one persistent notice, counted down in place: persistent so the alert's
   * own auto-dismiss never hides it before the sign-out, and one so a second never stacks
   * on the first. The notice's close is a click, which is activity, so closing it ends the
   * warning the way moving the mouse does.
   */
  private showOrUpdateWarningAlert(countdown: number): void {
    const message = `You will be logged out in ${countdown} seconds due to inactivity.`;
    if (this.alertId !== undefined) {
      this.notifications.updateNotification({
        id: this.alertId,
        type: AlertType.IDLE,
        link: '',
        message,
        persistent: true,
      });
    } else {
      this.alertId = this.notifications.addGlobalNotification({
        message,
        type: AlertType.IDLE,
        link: '',
        persistent: true,
      });
    }
  }

  private dismissWarningAlert(): void {
    if (this.alertId !== undefined) {
      this.notifications.dismissGlobalNotificationById(this.alertId);
      this.alertId = undefined;
    }
  }
}
