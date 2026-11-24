import { TestBed } from '@angular/core/testing';
import { AlertType, NotificationAction } from '@impulseframework/resource-angular/types';

import { NotificationService } from './notification.service';

// The alert area shows one notice for each sentence. A notice raised while one with the same
// words is shown adds none: the one shown takes what the new one names, in its place and
// under its id, so whoever holds the id can still take the notice down. A persistent notice
// stays persistent whatever is raised over it. How long a notice then shows is the alert's
// to keep, and its spec holds that.

describe('NotificationService', () => {
  it('a notice raised again while its words are shown adds no second one: the one shown takes the new kind and link, in its place and under its id', () => {
    const notifications = TestBed.inject(NotificationService);
    const first = notifications.addGlobalNotification({
      message: 'The server could not be reached.',
      type: AlertType.ERROR,
      link: '',
    });
    notifications.addGlobalNotification({ message: 'Saved.', type: AlertType.SUCCESS, link: '' });
    const second = notifications.notifications()[1];

    const answered = notifications.addGlobalNotification({
      message: 'The server could not be reached.',
      type: AlertType.INFO,
      link: '/status',
    });
    expect(answered).toBe(first);
    expect(notifications.notifications()).toHaveLength(2);
    expect(notifications.notifications()[0]).toMatchObject({
      id: first,
      message: 'The server could not be reached.',
      type: AlertType.INFO,
      link: '/status',
    });
    expect(notifications.notifications()[1]).toBe(second);

    notifications.dismissGlobalNotificationById(first);
    expect(notifications.notifications()).toHaveLength(1);
    expect(notifications.notifications()[0]).toBe(second);
  });

  it('it takes the action and the duration the new one names, and keeps its own where the new one names none', () => {
    const notifications = TestBed.inject(NotificationService);
    const retry: NotificationAction = { label: 'Retry', run: () => undefined };
    const report: NotificationAction = { label: 'Report', run: () => undefined };
    const id = notifications.addGlobalNotification({
      message: 'The report could not be sent.',
      type: AlertType.ERROR,
      link: '',
      duration: 8000,
      action: retry,
    });

    notifications.addGlobalNotification({ message: 'The report could not be sent.', type: AlertType.ERROR, link: '' });
    expect(notifications.notifications()).toHaveLength(1);
    expect(notifications.notifications()[0]).toMatchObject({ id, duration: 8000 });
    expect(notifications.notifications()[0].action).toBe(retry);

    notifications.addGlobalNotification({
      message: 'The report could not be sent.',
      type: AlertType.ERROR,
      link: '',
      duration: 5000,
      action: report,
    });
    expect(notifications.notifications()).toHaveLength(1);
    expect(notifications.notifications()[0]).toMatchObject({ id, duration: 5000 });
    expect(notifications.notifications()[0].action).toBe(report);
  });

  it('an ordinary notice raised again as persistent becomes persistent', () => {
    const notifications = TestBed.inject(NotificationService);
    const id = notifications.addGlobalNotification({ message: 'The server is busy.', type: AlertType.INFO, link: '' });

    notifications.addGlobalNotification({
      message: 'The server is busy.',
      type: AlertType.INFO,
      link: '',
      persistent: true,
    });
    expect(notifications.notifications()).toHaveLength(1);
    expect(notifications.notifications()[0]).toMatchObject({ id, persistent: true });
  });

  it('a persistent notice stays persistent whatever is raised over it', () => {
    const notifications = TestBed.inject(NotificationService);
    const id = notifications.addGlobalNotification({
      message: 'The server is down for maintenance.',
      type: AlertType.INFO,
      link: '',
      persistent: true,
    });

    notifications.addGlobalNotification({
      message: 'The server is down for maintenance.',
      type: AlertType.INFO,
      link: '',
    });
    expect(notifications.notifications()).toHaveLength(1);
    expect(notifications.notifications()[0]).toMatchObject({ id, persistent: true });

    notifications.addGlobalNotification({
      message: 'The server is down for maintenance.',
      type: AlertType.INFO,
      link: '',
      persistent: false,
    });
    expect(notifications.notifications()).toHaveLength(1);
    expect(notifications.notifications()[0]).toMatchObject({ id, persistent: true });
  });
});
