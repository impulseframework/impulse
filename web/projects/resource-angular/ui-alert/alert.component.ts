import { CommonModule } from '@angular/common';
import { Component, EventEmitter, Input, OnInit, Output, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { RouterModule } from '@angular/router';
import { NotificationMessage } from '@impulseframework/resource-angular/types';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';

/**
 * One notice in the application's alert area. A notice is dismissed by its timer
 * (`duration`, thirty seconds by default) unless it is persistent, in which case it stays
 * until it is closed or its owner dismisses it by id. A notice with an `action` renders
 * it as a button beside the close; the click runs the action and nothing else, so the
 * action's owner decides whether the notice goes.
 */
@Component({
  selector: 'impulse-alert',
  templateUrl: './alert.component.html',
  styleUrls: ['./alert.component.scss'],
  imports: [MatIconModule, MatButtonModule, CommonModule, RouterModule],
})
export class AlertComponent implements OnInit {
  @Input({ required: true }) error!: NotificationMessage;
  @Output() dismiss = new EventEmitter();

  errors = inject(NotificationService);
  ngOnInit(): void {
    if (this.error.persistent) {
      return;
    }
    if (this.error.duration === undefined) {
      this.error.duration = 30000;
    }

    setTimeout(() => {
      this.dismissAlert();
    }, this.error.duration);
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
