import { CommonModule } from '@angular/common';
import { Component, EventEmitter, Input, OnChanges, Output, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { RouterModule } from '@angular/router';
import { NotificationMessage } from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';

/**
 * One notice in the application's alert area. A notice is dismissed by its timer
 * (`duration`, thirty seconds by default) unless it is persistent, in which case it stays
 * until it is closed or its owner dismisses it by id. The time runs from when the notice was
 * last raised or updated: the notification service hands the alert a new object for a notice
 * raised again or updated by its id, and the alert starts its timer again for it, or starts
 * none where the notice is now persistent. A notice with an `action` renders it as a button
 * beside the close; the click runs the action and nothing else, so the action's owner
 * decides whether the notice goes.
 */
@Component({
  selector: 'impulse-alert',
  templateUrl: './alert.component.html',
  styleUrls: ['./alert.component.scss'],
  imports: [MatIconModule, MatButtonModule, CommonModule, RouterModule],
})
export class AlertComponent implements OnChanges {
  @Input({ required: true }) error!: NotificationMessage;
  @Output() dismiss = new EventEmitter();

  errors = inject(NotificationService);
  private timer: ReturnType<typeof setTimeout> | undefined;

  ngOnChanges(): void {
    if (this.timer !== undefined) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
    if (this.error.persistent) {
      return;
    }

    const duration = this.error.duration === undefined ? 30000 : this.error.duration;
    this.timer = setTimeout(() => {
      this.timer = undefined;
      this.dismissAlert();
    }, duration);
  }

  dismissAlert(): void {
    if (this.error.id !== undefined) {
      this.errors.dismissGlobalNotification(this.error);
    }
  }

  runAction(): void {
    this.error.action?.run();
  }
}
