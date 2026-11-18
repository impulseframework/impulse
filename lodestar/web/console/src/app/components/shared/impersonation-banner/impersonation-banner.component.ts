/** Demonstrates: impersonation.end, impersonation.max-duration. */
import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { Router } from '@angular/router';
import { FRONTEND_SIGN_OUT_PATH } from '@impulseframework/resource-angular/types';
import { ImpersonationService } from '@components/sector/impersonation.service';

/**
 * The banner above the frame's bar while the session was minted through the impersonation
 * route: whom the actor views or acts as and what the session may do, in the server's
 * words ("Viewing as cadet: reading only. You are marshal.", "Acting as marshal:
 * everything except credential actions. You are governor."), the time left under the
 * minted session's two-hour hard cap, and the way back. The frame draws it on every
 * signed-in page (provideFrame's banner); outside an impersonated session it draws
 * nothing.
 */
@Component({
  selector: 'app-impersonation-banner',
  imports: [MatButtonModule, MatIconModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  templateUrl: './impersonation-banner.component.html',
  styleUrl: './impersonation-banner.component.scss',
})
export class ImpersonationBannerComponent {
  private router = inject(Router);
  private impersonation = inject(ImpersonationService);
  private signOutPath = inject(FRONTEND_SIGN_OUT_PATH);

  /** The session's impersonation record, present exactly while the session was minted through the route. */
  readonly banner = this.impersonation.banner;

  /**
   * Return to yourself: end the minted session and land back in the actor's own session.
   * When that session is gone (expired, or past the hard cap) there is nothing to return
   * to, so the actor signs in again, through the sign-out route (the one the frame's Sign
   * out opens).
   */
  async endImpersonation(): Promise<void> {
    const restored = await this.impersonation.end();
    if (!restored) {
      void this.router.navigateByUrl(this.signOutPath);
    }
  }
}
