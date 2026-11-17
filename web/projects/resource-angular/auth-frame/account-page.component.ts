import { ChangeDetectionStrategy, Component, computed, effect, inject, signal } from '@angular/core';
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
 * What removing their password leaves a person with, said before they confirm it: the
 * other ways in their sign-in offers, each by the words its button on the login page
 * carries. Where the page knows of none, the person's organization signs them in.
 */
export function passwordRemovalConsequence(otherSignIns: readonly string[]): string {
  if (otherSignIns.length === 0) {
    return 'You will sign in through your organization only.';
  }
  const last = otherSignIns[otherSignIns.length - 1];
  const through = otherSignIns.length === 1 ? last : `${otherSignIns.slice(0, -1).join(', ')} or ${last}`;
  return `You will sign in through ${through} only.`;
}

/** The longest name the server keeps, in characters; the box holds no more. */
export const nameMaxLength = 200;

/**
 * The person's account page, every place's: their name, their email address, their
 * password and this browser, each section only where their sign-in offers it (Account). Managing a second
 * factor joins the page as the library offers factors a person enrolls.
 *
 * Where the server serves it, the password section also removes the password: the person
 * proves it with the password, is asked once what that leaves them to sign in through,
 * and is told the server's words when it refuses (a person no directory sign-in is linked
 * to keeps the password, their only way in). Where the server says what the person signs
 * in with, the page offers the removal only to a person a directory sign-in is linked to,
 * names that directory in its question, and offers neither the change nor the removal to
 * a person who holds no password.
 */
@Component({
  selector: 'impulse-account-page',
  imports: [EmailAddressComponent, FormsModule, MatButtonModule, MatFormFieldModule, MatInputModule],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <section class="account">
      <h1>Your account</h1>

      @if (account.name()) {
        <section class="part name">
          <h2>Your name</h2>
          <p>
            Shown for you in place of your username, <span class="username">{{ username() }}</span
            >, which you still sign in with.
          </p>
          <form class="change" (ngSubmit)="changeName()">
            <mat-form-field appearance="outline">
              <mat-label>Name</mat-label>
              <input
                matInput
                name="personName"
                [ngModel]="name()"
                (ngModelChange)="name.set($event)"
                autocomplete="name"
                [attr.maxlength]="nameMaxLength" />
            </mat-form-field>
            <button mat-flat-button type="submit" class="name-submit" [disabled]="busy()">Save name</button>
          </form>
          @if (nameMessage(); as message) {
            <p class="message" role="status">{{ message }}</p>
          }
        </section>
      }

      @if (account.email()) {
        <section class="part email">
          <h2>Your email address</h2>
          <impulse-email-address />
        </section>
      }

      @if (passwordRemoved()) {
        <section class="part password">
          <h2>Your password</h2>
          <p class="message" role="status">Your password is removed. {{ removalConsequence() }}</p>
        </section>
      } @else if (account.password()) {
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

        @if (account.removePassword()) {
          <section class="part remove-password">
            <h2>Remove your password</h2>
            <p>If you also sign in another way, you can do without a password.</p>
            @if (confirmingRemoval()) {
              <p class="question">Remove your password? {{ removalConsequence() }}</p>
              <div class="answers">
                <button
                  mat-flat-button
                  type="button"
                  class="remove-confirm"
                  [disabled]="busy()"
                  (click)="removePassword()">
                  Remove password
                </button>
                <button
                  mat-stroked-button
                  type="button"
                  class="remove-keep"
                  [disabled]="busy()"
                  (click)="keepPassword()">
                  Keep password
                </button>
              </div>
            } @else {
              <form class="change" (ngSubmit)="askToRemovePassword()">
                <mat-form-field appearance="outline">
                  <mat-label>Current password</mat-label>
                  <input
                    matInput
                    name="removalPassword"
                    type="password"
                    [(ngModel)]="removalPassword"
                    autocomplete="current-password"
                    required />
                </mat-form-field>
                <button mat-stroked-button type="submit" class="remove-submit" [disabled]="busy()">
                  Remove password
                </button>
              </form>
            }
            @if (removeMessage(); as message) {
              <p class="message" role="status">{{ message }}</p>
            }
          </section>
        }
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

      @if (!account.any() && !forgotten() && !passwordRemoved()) {
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
    .answers {
      display: flex;
      gap: 12px;
    }
    .username {
      font-weight: 500;
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

  readonly nameMaxLength = nameMaxLength;
  readonly username = (): string => this.auth.sessionInfo().username ?? '';
  /** The name the session carries, none as the empty string. */
  private sessionName = computed(() => this.auth.sessionInfo().name ?? '');
  /** The name in the box: the person's own when the session answers, then what they type. */
  readonly name = signal('');
  /** What the last change of the name came to, in the application's words or the server's. */
  readonly nameMessage = signal<string | undefined>(undefined);

  oldPassword = '';
  newPassword = '';
  newPasswordAgain = '';
  /** A request is in flight; the controls are disabled meanwhile. */
  readonly busy = signal(false);
  /** What the last password change came to, in the application's words or the server's. */
  readonly changeMessage = signal<string | undefined>(undefined);
  /** The browser was forgotten on this page. */
  readonly forgotten = signal(false);

  /** The password given as proof for its removal. */
  removalPassword = '';
  /** The person asked to remove the password and has not answered the question yet. */
  readonly confirmingRemoval = signal(false);
  /** What the server said when it refused the removal, in its words. */
  readonly removeMessage = signal<string | undefined>(undefined);
  /** The password was removed on this page. */
  readonly passwordRemoved = signal(false);
  /**
   * What the removal leaves the person to sign in through, as the question says it, and
   * as the notice goes on saying it once the password is gone.
   */
  readonly removalConsequence = computed(() => passwordRemovalConsequence(this.account.otherSignIns()));

  constructor() {
    // The box starts as the name the session carries and follows it when that name
    // changes. A read of the session that answers the same name, the keepalive's among
    // them, leaves what the person is typing alone.
    effect(() => this.name.set(this.sessionName()));
  }

  async changeName(): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true);
    // What an earlier change came to is not said of this one.
    this.nameMessage.set(undefined);
    try {
      const result = await this.client.auth.changeName(this.name());
      if (result.outcome === 'changed') {
        this.nameMessage.set(
          result.name ? 'Your name is saved.' : 'Your name is removed. Your username is shown instead.',
        );
        // The top bar and the menu show the name the session carries: read it again.
        await firstValueFrom(this.auth.checkUserSession());
      } else {
        this.nameMessage.set(result.message);
      }
    } finally {
      this.busy.set(false);
    }
  }

  async changePassword(): Promise<void> {
    if (this.busy()) return;
    if (this.newPassword !== this.newPasswordAgain) {
      this.changeMessage.set(passwordsDiffer);
      return;
    }
    this.busy.set(true);
    // What an earlier change came to is not said of this one.
    this.changeMessage.set(undefined);
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

  /** Puts the question before the password is removed; nothing is asked of the server yet. */
  askToRemovePassword(): void {
    if (this.busy()) return;
    // What an earlier removal came to is not said of this one.
    this.removeMessage.set(undefined);
    this.confirmingRemoval.set(true);
  }

  /** The person keeps the password: the question goes, and nothing was asked of the server. */
  keepPassword(): void {
    this.confirmingRemoval.set(false);
  }

  async removePassword(): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true);
    this.removeMessage.set(undefined);
    try {
      const result = await this.client.auth.removePassword(this.removalPassword);
      if (result.outcome === 'removed') {
        this.removalPassword = '';
        this.passwordRemoved.set(true);
        // The person holds no password now: the sections that change and remove one go,
        // here and wherever else the page is opened.
        this.account.rereadSignInMeans();
      } else {
        this.removeMessage.set(result.message);
      }
    } finally {
      this.confirmingRemoval.set(false);
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
