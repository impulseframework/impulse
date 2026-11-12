import { Component, inject } from '@angular/core';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { RouterOutlet } from '@angular/router';
import { AlertComponent } from '@impulseframework/resource-angular/ui-alert';
import { UiCoreService } from '@impulseframework/resource-angular/ui-core-service';
import { IdleService } from '@impulseframework/resource-angular/auth-idle-service';
import { NotificationService } from '@impulseframework/resource-angular/ui-notification-service';

@Component({
  selector: 'app-root',
  imports: [RouterOutlet, MatProgressBarModule, AlertComponent],
  templateUrl: './app.component.html',
  styleUrl: './app.component.scss',
})
export class AppComponent {
  core = inject(UiCoreService);
  notifications = inject(NotificationService);
  private idle = inject(IdleService);

  title = 'Outlets';

  constructor() {
    this.idle.start();
  }
}
