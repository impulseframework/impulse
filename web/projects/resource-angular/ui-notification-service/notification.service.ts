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
   * Adds a new global notification.
   * @param notification The notification message to add.
   * @returns The unique ID assigned to the notification.
   */
  addGlobalNotification(notification: CreateNotificationMessage): number {
    this.notificationId.update((id) => id + 1);
    const newNotification: NotificationMessage = {
      ...notification,
      id: this.notificationId(),
    };
    const existingNotification = this._notifications().find((n) => n.message === newNotification.message);
    if (existingNotification) {
      this.updateNotification({ ...existingNotification, ...newNotification });
      return existingNotification.id;
    }
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
   * Updates an existing notification.
   * @param updatedNotification The notification with updated information.
   */
  updateNotification(updatedNotification: NotificationMessage): void {
    this._notifications.update((current) =>
      current.map((notification) =>
        notification.id === updatedNotification.id ? { ...notification, ...updatedNotification } : notification,
      ),
    );
  }
}
