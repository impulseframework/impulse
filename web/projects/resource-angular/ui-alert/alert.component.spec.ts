import { Component, inject } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { AlertType, CreateNotificationMessage } from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';

import { AlertComponent } from './alert.component';

// The alert renders a notice from the notification service: a plain notice is dismissed by
// its timer, a persistent one stays, and a notice with an action renders it as a button
// whose click runs the action and nothing else. A notice raised again while it shows, or
// updated by its id, reaches the same alert as a new object: its time starts again from
// then, and none runs where the notice is now persistent.

/** The alert area as an application draws it: one alert for each notice of the service, tracked by its id. */
@Component({
  imports: [AlertComponent],
  template: `
    @for (notification of notifications.notifications(); track notification.id) {
      <impulse-alert [error]="notification" />
    }
  `,
})
class AlertAreaComponent {
  notifications = inject(NotificationService);
}

describe('AlertComponent', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  /** Adds the notice to the service and renders the alert over it, as the application's alert area does. */
  async function render(
    notice: CreateNotificationMessage,
  ): Promise<{ fixture: ComponentFixture<AlertComponent>; notifications: NotificationService; id: number }> {
    await TestBed.configureTestingModule({ imports: [AlertComponent], providers: [provideRouter([])] }).compileComponents();
    const notifications = TestBed.inject(NotificationService);
    const id = notifications.addGlobalNotification(notice);
    const fixture = TestBed.createComponent(AlertComponent);
    fixture.componentRef.setInput(
      'error',
      notifications.notifications().find((n) => n.id === id),
    );
    fixture.detectChanges();
    return { fixture, notifications, id };
  }

  function actionButton(fixture: ComponentFixture<AlertComponent>): HTMLButtonElement | null {
    return (fixture.nativeElement as HTMLElement).querySelector('button.action');
  }

  describe('the timer', () => {
    const cases: { name: string; notice: CreateNotificationMessage; wantKept: boolean }[] = [
      {
        name: 'a plain notice is dismissed when its time is up',
        notice: { message: 'Saved.', link: '', type: AlertType.SUCCESS },
        wantKept: false,
      },
      {
        name: 'a notice with its own duration is dismissed at that time',
        notice: { message: 'Saved.', link: '', type: AlertType.SUCCESS, duration: 5000 },
        wantKept: false,
      },
      {
        name: 'a persistent notice stays past the default time',
        notice: { message: 'A new version of this application is ready.', link: '', type: AlertType.INFO, persistent: true },
        wantKept: true,
      },
    ];

    for (const tt of cases) {
      it(tt.name, async () => {
        const { notifications, id } = await render(tt.notice);
        expect(notifications.notifications().some((n) => n.id === id)).toBe(true);
        vi.advanceTimersByTime(30000);
        expect(notifications.notifications().some((n) => n.id === id)).toBe(tt.wantKept);
      });
    }
  });

  describe('a notice that changes while it shows', () => {
    const cases: {
      name: string;
      first: CreateNotificationMessage;
      at: number;
      then: (notifications: NotificationService, id: number) => void;
      shownAt: number;
      reads?: string;
      goneAt?: number;
    }[] = [
      {
        name: 'raised again, its time starts again',
        first: { message: 'Saved.', link: '', type: AlertType.SUCCESS },
        at: 20000,
        then: (notifications) =>
          notifications.addGlobalNotification({ message: 'Saved.', link: '', type: AlertType.SUCCESS }),
        shownAt: 40000,
        goneAt: 50000,
      },
      {
        name: 'raised again, a notice with its own duration goes that long after the repeat',
        first: { message: 'Saved.', link: '', type: AlertType.SUCCESS, duration: 5000 },
        at: 4000,
        then: (notifications) =>
          notifications.addGlobalNotification({ message: 'Saved.', link: '', type: AlertType.SUCCESS, duration: 5000 }),
        shownAt: 6000,
        goneAt: 9000,
      },
      {
        name: 'raised again with a duration, it goes by the new one',
        first: { message: 'Saved.', link: '', type: AlertType.SUCCESS },
        at: 10000,
        then: (notifications) =>
          notifications.addGlobalNotification({ message: 'Saved.', link: '', type: AlertType.SUCCESS, duration: 5000 }),
        shownAt: 14000,
        goneAt: 15000,
      },
      {
        name: 'an ordinary notice raised again as persistent stays',
        first: { message: 'The server is busy.', link: '', type: AlertType.INFO },
        at: 10000,
        then: (notifications) =>
          notifications.addGlobalNotification({
            message: 'The server is busy.',
            link: '',
            type: AlertType.INFO,
            persistent: true,
          }),
        shownAt: 70000,
      },
      {
        name: 'a persistent notice raised again as an ordinary one stays',
        first: { message: 'The server is busy.', link: '', type: AlertType.INFO, persistent: true },
        at: 10000,
        then: (notifications) =>
          notifications.addGlobalNotification({
            message: 'The server is busy.',
            link: '',
            type: AlertType.INFO,
            persistent: false,
          }),
        shownAt: 70000,
      },
      {
        name: 'updated by its id, its time starts again',
        first: { message: 'Sending the report.', link: '', type: AlertType.INFO },
        at: 20000,
        then: (notifications, id) =>
          notifications.updateNotification({ id, message: 'The report is sent.', link: '', type: AlertType.SUCCESS }),
        shownAt: 40000,
        reads: 'The report is sent.',
        goneAt: 50000,
      },
    ];

    /** Draws the alert area; draw() checks it again after a call on the service, as Angular does by itself in an application. */
    async function area(): Promise<{ notifications: NotificationService; draw: () => void }> {
      await TestBed.configureTestingModule({
        imports: [AlertAreaComponent],
        providers: [provideRouter([])],
      }).compileComponents();
      const fixture = TestBed.createComponent(AlertAreaComponent);
      return { notifications: TestBed.inject(NotificationService), draw: () => fixture.detectChanges() };
    }

    for (const tt of cases) {
      it(tt.name, async () => {
        const { notifications, draw } = await area();
        const id = notifications.addGlobalNotification(tt.first);
        draw();

        vi.advanceTimersByTime(tt.at);
        tt.then(notifications, id);
        draw();

        vi.advanceTimersByTime(tt.shownAt - tt.at);
        const shown = notifications.notifications().find((n) => n.id === id);
        expect(shown).toBeDefined();
        if (tt.reads !== undefined) {
          expect(shown?.message).toBe(tt.reads);
        }
        if (tt.goneAt !== undefined) {
          vi.advanceTimersByTime(tt.goneAt - tt.shownAt);
          expect(notifications.notifications().some((n) => n.id === id)).toBe(false);
        }
      });
    }

    it('a notice that names no duration is left without one', async () => {
      const { notifications, draw } = await area();
      const id = notifications.addGlobalNotification({ message: 'Saved.', link: '', type: AlertType.SUCCESS });
      draw();
      expect(notifications.notifications().find((n) => n.id === id)?.duration).toBeUndefined();
    });
  });

  describe('the action', () => {
    it('renders nothing for a notice without one', async () => {
      const { fixture } = await render({ message: 'Saved.', link: '', type: AlertType.SUCCESS });
      expect(actionButton(fixture)).toBeNull();
    });

    it('renders the label and a click runs the action, keeping the notice', async () => {
      let runs = 0;
      const { fixture, notifications, id } = await render({
        message: 'A new version of this application is ready.',
        link: '',
        type: AlertType.INFO,
        persistent: true,
        action: { label: 'Reload', run: () => runs++ },
      });
      const button = actionButton(fixture);
      expect(button?.textContent?.trim()).toBe('Reload');
      button?.click();
      expect(runs).toBe(1);
      expect(notifications.notifications().some((n) => n.id === id)).toBe(true);
    });

    it('the close still dismisses a persistent notice', async () => {
      const { fixture, notifications, id } = await render({
        message: 'A new version of this application is ready.',
        link: '',
        type: AlertType.INFO,
        persistent: true,
        action: { label: 'Reload', run: () => undefined },
      });
      (fixture.nativeElement as HTMLElement).querySelector<HTMLButtonElement>('button[aria-label="Close Alert"]')?.click();
      expect(notifications.notifications().some((n) => n.id === id)).toBe(false);
    });
  });
});
