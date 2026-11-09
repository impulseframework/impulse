import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { AlertType, CreateNotificationMessage } from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';

import { AlertComponent } from './alert.component';

// The alert renders a notice from the notification service: a plain notice is dismissed by
// its timer, a persistent one stays, and a notice with an action renders it as a button
// whose click runs the action and nothing else.

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
