import { Injectable, signal } from '@angular/core';
import { CreateNotificationMessage, NotificationMessage } from '@impulseframework/resource-angular/types';

@Injectable({
  providedIn: 'root',
})
export class NotificationService {
  private notificationId = signal(0);
  private _notifications = signal<NotificationMessage[]>([]);
  notifications = this._notifications.asReadonly();

  /**
   * Raises a global notification. The alert area shows one notice for each sentence: when a
   * notice with the same message is already shown none is added, and the one shown is the
   * notice raised again. It takes what this one names (its type and link, and its action and
   * duration where it names them), keeps its id and its place, and its time starts again. A
   * persistent notice stays persistent whatever is raised over it, since whoever raised it
   * holds its id and takes it down.
   * @param notification The notification message to raise.
   * @returns The id of the notice that now shows the message: a new id, or the shown notice's.
   */
  addGlobalNotification(notification: CreateNotificationMessage): number {
    const shown = this._notifications().find((n) => n.message === notification.message);
    if (shown) {
      const raised: NotificationMessage = { ...shown, ...notification, id: shown.id };
      if (shown.persistent) {
        raised.persistent = true;
      }
      this.updateNotification(raised);
      return shown.id;
    }
    this.notificationId.update((id) => id + 1);
    const newNotification: NotificationMessage = {
      ...notification,
      id: this.notificationId(),
    };
    this._notifications.update((current) => [...current, newNotification]);
    return newNotification.id;
  }

  /**
   * Dismisses a global notification by its ID.
   * @param notificationId The ID of the notification to dismiss.
   */
  dismissGlobalNotificationById(notificationId: number): void {
    this._notifications.update((current) => current.filter((notification) => notification.id !== notificationId));
  }

  /**
   * Dismisses a specific global notification.
   * @param notification The notification to dismiss.
   */
  dismissGlobalNotification(notification: NotificationMessage): void {
    this.dismissGlobalNotificationById(notification.id);
  }

  /**
   * Changes the notification with this id in place: it takes every field given and keeps the
   * rest, and the alert starts its time again. An id no notice has changes nothing.
   * @param updatedNotification The notification's id and the fields it takes.
   */
  updateNotification(updatedNotification: NotificationMessage): void {
    this._notifications.update((current) =>
      current.map((notification) =>
        notification.id === updatedNotification.id ? { ...notification, ...updatedNotification } : notification,
      ),
    );
  }
}
