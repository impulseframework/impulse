import { Component, inject, signal } from '@angular/core';
import { MatProgressBarModule } from '@angular/material/progress-bar';
import { RouterOutlet } from '@angular/router';
import { FrameComponent } from '@impulseframework/resource-angular/auth-frame';
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

  title = 'Tenanted';

  /** Whether the page on the screen sits in the frame: the notices go under the frame's bar while it does, and at the top of the screen on a page with no bar. */
  readonly framed = signal(false);

  constructor() {
    this.idle.start();
  }

  /** The root outlet shows a page: the frame, which every signed-in page sits in, or a page routed outside it, as the sign-in and sign-out pages are. */
  shown(page: unknown): void {
    this.framed.set(page instanceof FrameComponent);
  }
}
