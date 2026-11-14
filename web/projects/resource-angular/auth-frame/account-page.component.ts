import { ChangeDetectionStrategy, Component, inject, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { EmailAddressComponent } from '@impulseframework/resource-angular/auth-email-address';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { firstValueFrom } from 'rxjs';
import { Account } from './account';

/** What the password change says when the two new passwords differ, before asking the server. */
export const passwordsDiffer = 'The two new passwords are not the same.';

/**
 * The person's account page, every place's: their email address, their password and this
 * browser, each section only where their sign-in offers it (Account). Managing a second
 * factor joins the page as the library offers factors a person enrolls.
 */
@Component({
  selector: 'impulse-account-page',
  imports: [EmailAddressComponent, FormsModule, MatButtonModule, MatFormFieldModule, MatInputModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <section class="account">
      <h1>Your account</h1>

      @if (account.email()) {
        <section class="part email">
          <h2>Your email address</h2>
          <impulse-email-address />
        </section>
      }

      @if (account.password()) {
        <section class="part password">
          <h2>Change your password</h2>
          <form class="change" (ngSubmit)="changePassword()">
            <mat-form-field appearance="outline">
              <mat-label>Current password</mat-label>
              <input
                matInput
                name="oldPassword"
                type="password"
                [(ngModel)]="oldPassword"
                autocomplete="current-password"
                required />
            </mat-form-field>
            <mat-form-field appearance="outline">
              <mat-label>New password</mat-label>
              <input
                matInput
                name="newPassword"
                type="password"
                [(ngModel)]="newPassword"
                autocomplete="new-password"
                required />
            </mat-form-field>
            <mat-form-field appearance="outline">
              <mat-label>New password again</mat-label>
              <input
                matInput
                name="newPasswordAgain"
                type="password"
                [(ngModel)]="newPasswordAgain"
                autocomplete="new-password"
                required />
            </mat-form-field>
            <button mat-flat-button type="submit" class="change-submit" [disabled]="busy()">Change password</button>
          </form>
          @if (changeMessage(); as message) {
            <p class="message" role="status">{{ message }}</p>
          }
        </section>
      }

      @if (account.browser() || forgotten()) {
        <section class="part browser">
          <h2>This browser</h2>
          @if (forgotten()) {
            <p class="message" role="status">
              This browser will ask for the emailed code the next time you sign in here.
            </p>
          } @else {
            <p>This browser is remembered for you, so signing in here skips the emailed code.</p>
            <button mat-stroked-button type="button" class="forget" [disabled]="busy()" (click)="forgetBrowser()">
              Forget this browser
            </button>
          }
        </section>
      }

      @if (!account.any() && !forgotten()) {
        <p class="none">Your sign-in is managed by your organization, so there is nothing to change here.</p>
      }
    </section>
  `,
  styles: `
    .account {
      display: flex;
      flex-direction: column;
      gap: 24px;
      max-width: 640px;
      padding: 24px 8px;
    }
    h1 {
      margin: 0;
      font-size: 24px;
      font-weight: 500;
    }
    h2 {
      margin: 0 0 8px;
      font-size: 16px;
      font-weight: 500;
    }
    .part p {
      margin: 0 0 12px;
    }
    .change {
      display: flex;
      flex-direction: column;
      align-items: flex-start;
      max-width: 360px;
    }
    .change mat-form-field {
      width: 100%;
    }
    .message,
    .none {
      color: var(--mat-sys-on-surface-variant);
    }
  `,
})
export class AccountPageComponent {
  readonly account = inject(Account);
  private client = inject(RESOURCE_CLIENT);
  private auth = inject(AuthService);

  oldPassword = '';
  newPassword = '';
  newPasswordAgain = '';
  /** A request is in flight; the controls are disabled meanwhile. */
  readonly busy = signal(false);
  /** What the last password change came to, in the application's words or the server's. */
  readonly changeMessage = signal<string | undefined>(undefined);
  /** The browser was forgotten on this page. */
  readonly forgotten = signal(false);

  async changePassword(): Promise<void> {
    if (this.busy()) return;
    if (this.newPassword !== this.newPasswordAgain) {
      this.changeMessage.set(passwordsDiffer);
      return;
    }
    this.busy.set(true);
    try {
      const result = await this.client.auth.changePassword(this.oldPassword, this.newPassword);
      if (result.outcome === 'changed') {
        this.oldPassword = this.newPassword = this.newPasswordAgain = '';
        this.changeMessage.set('Your password is changed.');
        // The server regenerated the session: read it again.
        await firstValueFrom(this.auth.checkUserSession());
      } else {
        this.changeMessage.set(result.message);
      }
    } finally {
      this.busy.set(false);
    }
  }

  async forgetBrowser(): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true);
    try {
      await this.client.auth.forgetBrowser();
      this.forgotten.set(true);
      await firstValueFrom(this.auth.checkUserSession());
    } finally {
      this.busy.set(false);
    }
  }
}
