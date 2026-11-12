import { Component, inject, OnInit, PendingTasks } from '@angular/core';
import { Router } from '@angular/router';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { FRONTEND_LOGIN_PATH } from '@impulseframework/resource-angular/types';
import { IdleService } from '@impulseframework/resource-angular/auth-idle-service';
import { firstValueFrom } from 'rxjs';

/**
 * The sign-out page: the one place a navigation ends the session. On arrival it stops the
 * idle watch, ends the session through the AuthService (the live session first, then the
 * session's DELETE, then LOGOUT_ACTION) and forwards the browser to the login page. A
 * sign-out control is a link to the route this page is served at (FRONTEND_SIGN_OUT_PATH,
 * `/logout` by default), the idle watch sends the browser there when the session times out,
 * and the login page ends nothing on arrival, so a link to it signs nobody out. A session
 * the server no longer holds forwards all the same.
 *
 *     { path: 'logout', loadComponent: () => import('@impulseframework/resource-angular/auth-sign-out').then((m) => m.SignOutComponent) }
 *
 *     <a mat-stroked-button routerLink="/logout">Sign out</a>
 */
@Component({
  selector: 'impulse-sign-out',
  template: `<p class="impulse-sign-out-note">Signing out…</p>`,
  styles: `
    :host {
      display: block;
    }
    .impulse-sign-out-note {
      margin: 24px;
      text-align: center;
    }
  `,
})
export class SignOutComponent implements OnInit {
  private auth = inject(AuthService);
  private idle = inject(IdleService);
  private router = inject(Router);
  private loginPath = inject(FRONTEND_LOGIN_PATH);
  /** The sign-out is registered as pending work, so the application is stable only once the browser has moved on. */
  private pendingTasks = inject(PendingTasks);

  ngOnInit(): void {
    void this.signOut();
  }

  private async signOut(): Promise<void> {
    const release = this.pendingTasks.add();
    try {
      this.idle.stop();
      try {
        await firstValueFrom(this.auth.logout());
      } catch {
        // A refusal here is a session the server no longer held: the browser moves on all the same.
      }
      await this.router.navigateByUrl(this.loginPath);
    } finally {
      release();
    }
  }
}
