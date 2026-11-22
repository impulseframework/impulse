import { ChangeDetectionStrategy, Component, computed, effect, inject, signal, viewChild } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { EmailAddressComponent } from '@impulseframework/resource-angular/auth-email-address';
import { NewPasswordComponent } from '@impulseframework/resource-angular/auth-new-password';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { firstValueFrom } from 'rxjs';
import { Account } from './account';

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
 *
 * A person who holds no password, because they have only ever signed in through a
 * directory, chooses one here where the server serves it: they ask for a code, which the
 * server mails to the address their account holds confirmed, and set the password with
 * the code. The code ties the password to that mailbox: whoever asks must read it to
 * finish. The server's words are shown when it refuses, among them that the account has
 * no confirmed address yet, which the email section above adds.
 */
@Component({
  selector: 'impulse-account-page',
  imports: [
    EmailAddressComponent,
    FormsModule,
    MatButtonModule,
    MatFormFieldModule,
    MatInputModule,
    NewPasswordComponent,
  ],
  changeDetection: ChangeDetectionStrategy.OnPush,
  template: `
    <section class="impulse-account-page-content">
      <h1>Your account</h1>

      @if (account.name()) {
        <section class="impulse-account-page-part impulse-account-page-name">
          <h2>Your name</h2>
          <p>
            Shown for you in place of your username,
            <span class="impulse-account-page-username">{{ username() }}</span>, which you still sign in with.
          </p>
          <form class="impulse-account-page-change" (ngSubmit)="changeName()">
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
            <button mat-flat-button type="submit" class="impulse-account-page-name-submit" [disabled]="busy()">
              Save name
            </button>
          </form>
          @if (nameMessage(); as message) {
            <p class="impulse-account-page-message" role="status">{{ message }}</p>
          }
        </section>
      }

      @if (account.email()) {
        <section class="impulse-account-page-part impulse-account-page-email">
          <h2>Your email address</h2>
          <impulse-email-address />
        </section>
      }

      @if (passwordRemoved()) {
        <section class="impulse-account-page-part impulse-account-page-password">
          <h2>Your password</h2>
          <p class="impulse-account-page-message" role="status">Your password is removed. {{ removalConsequence() }}</p>
        </section>
      } @else if (passwordChosen()) {
        <section class="impulse-account-page-part impulse-account-page-password">
          <h2>Your password</h2>
          <p class="impulse-account-page-message" role="status">
            Your password is set. You can sign in with it from now on.
          </p>
        </section>
      } @else if (account.firstPassword()) {
        <section class="impulse-account-page-part impulse-account-page-first-password">
          <h2>Choose a password</h2>
          <p>
            You have no password here. To choose one, ask for a code first: we email it to the address on your account.
          </p>
          <button
            mat-stroked-button
            type="button"
            class="impulse-account-page-first-code"
            [disabled]="busy()"
            (click)="mailFirstPasswordCode()">
            {{ codeSentTo() === undefined ? 'Email me a code' : 'Email me a new code' }}
          </button>
          @if (codeSentTo(); as address) {
            <p class="impulse-account-page-message impulse-account-page-sent" role="status">
              We sent a code to {{ address }}. Enter it with your password.
            </p>
          }
          @if (codeSentTo() !== undefined) {
            <form class="impulse-account-page-change" (ngSubmit)="setFirstPassword()">
              <mat-form-field appearance="outline">
                <mat-label>Code</mat-label>
                <input
                  matInput
                  name="firstPasswordCode"
                  [(ngModel)]="firstPasswordCode"
                  inputmode="numeric"
                  autocomplete="one-time-code"
                  required />
              </mat-form-field>
              <impulse-new-password
                label="Password"
                [username]="sessionDetails().username"
                [personName]="sessionDetails().name"
                [email]="sessionDetails().email"
                [(password)]="firstPassword" />
              <button
                mat-flat-button
                type="submit"
                class="impulse-account-page-first-submit"
                [disabled]="busy() || !strongEnough()">
                Set the password
              </button>
            </form>
          }
          @if (firstPasswordMessage(); as message) {
            <p class="impulse-account-page-message impulse-account-page-refusal" role="status">{{ message }}</p>
          }
        </section>
      } @else if (account.password()) {
        <section class="impulse-account-page-part impulse-account-page-password">
          <h2>Change your password</h2>
          <form class="impulse-account-page-change" (ngSubmit)="changePassword()">
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
            <impulse-new-password
              label="New password"
              [username]="sessionDetails().username"
              [personName]="sessionDetails().name"
              [email]="sessionDetails().email"
              [(password)]="newPassword" />
            <button
              mat-flat-button
              type="submit"
              class="impulse-account-page-change-submit"
              [disabled]="busy() || oldPassword === '' || !strongEnough()">
              Change password
            </button>
          </form>
          @if (changeMessage(); as message) {
            <p class="impulse-account-page-message" role="status">{{ message }}</p>
          }
        </section>

        @if (account.removePassword()) {
          <section class="impulse-account-page-part impulse-account-page-remove-password">
            <h2>Remove your password</h2>
            <p>If you also sign in another way, you can do without a password.</p>
            @if (confirmingRemoval()) {
              <p class="impulse-account-page-question">Remove your password? {{ removalConsequence() }}</p>
              <div class="impulse-account-page-answers">
                <button
                  mat-flat-button
                  type="button"
                  class="impulse-account-page-remove-confirm"
                  [disabled]="busy()"
                  (click)="removePassword()">
                  Remove password
                </button>
                <button
                  mat-stroked-button
                  type="button"
                  class="impulse-account-page-remove-keep"
                  [disabled]="busy()"
                  (click)="keepPassword()">
                  Keep password
                </button>
              </div>
            } @else {
              <form class="impulse-account-page-change" (ngSubmit)="askToRemovePassword()">
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
                <button mat-stroked-button type="submit" class="impulse-account-page-remove-submit" [disabled]="busy()">
                  Remove password
                </button>
              </form>
            }
            @if (removeMessage(); as message) {
              <p class="impulse-account-page-message" role="status">{{ message }}</p>
            }
          </section>
        }
      }

      @if (account.browser() || forgotten()) {
        <section class="impulse-account-page-part impulse-account-page-browser">
          <h2>This browser</h2>
          @if (forgotten()) {
            <p class="impulse-account-page-message" role="status">
              This browser will ask for the emailed code the next time you sign in here.
            </p>
          } @else {
            <p>This browser is remembered for you, so signing in here skips the emailed code.</p>
            <button
              mat-stroked-button
              type="button"
              class="impulse-account-page-forget"
              [disabled]="busy()"
              (click)="forgetBrowser()">
              Forget this browser
            </button>
          }
        </section>
      }

      @if (!account.any() && !forgotten() && !passwordRemoved() && !passwordChosen()) {
        <p class="impulse-account-page-none">
          Your sign-in is managed by your organization, so there is nothing to change here.
        </p>
      }
    </section>
  `,
  styles: `
    .impulse-account-page-content {
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
    .impulse-account-page-part p {
      margin: 0 0 12px;
    }
    .impulse-account-page-first-code {
      margin-bottom: 12px;
    }
    .impulse-account-page-change {
      display: flex;
      flex-direction: column;
      align-items: flex-start;
      max-width: 360px;
    }
    .impulse-account-page-change mat-form-field,
    .impulse-account-page-change impulse-new-password {
      width: 100%;
    }
    .impulse-account-page-answers {
      display: flex;
      gap: 12px;
    }
    .impulse-account-page-username {
      font-weight: 500;
    }
    .impulse-account-page-message,
    .impulse-account-page-none {
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
  /** What the session holds of the person, sent with the question about a new password; empty for what it does not hold. */
  protected sessionDetails = computed(() => {
    const info = this.auth.sessionInfo();
    return { username: info.username ?? '', name: info.name ?? '', email: info.email ?? '' };
  });
  private newPasswordBox = viewChild(NewPasswordComponent);
  /**
   * The server called the new password now in the drawn form's box strong enough: the
   * change's button and the first password's stay disabled until it does. False while no
   * such box is drawn; only one of the two forms is ever drawn.
   */
  readonly strongEnough = computed(() => this.newPasswordBox()?.strongEnough() ?? false);
  /** The name in the box: the person's own when the session answers, then what they type. */
  readonly name = signal('');
  /** What the last change of the name came to, in the application's words or the server's. */
  readonly nameMessage = signal<string | undefined>(undefined);

  oldPassword = '';
  newPassword = '';
  /** A request is in flight; the controls are disabled meanwhile. */
  readonly busy = signal(false);
  /** What the last password change came to, in the application's words or the server's. */
  readonly changeMessage = signal<string | undefined>(undefined);
  /** The browser was forgotten on this page. */
  readonly forgotten = signal(false);

  /** The code the person was mailed for their first password, as they type it. */
  firstPasswordCode = '';
  firstPassword = '';
  /** Where the code of a first password went, masked as the server masks it; undefined until one is sent. */
  readonly codeSentTo = signal<string | undefined>(undefined);
  /** What the last step of choosing a password came to, in the application's words or the server's. */
  readonly firstPasswordMessage = signal<string | undefined>(undefined);
  /** The person chose their first password on this page. */
  readonly passwordChosen = signal(false);

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

  /** Changes the password; nothing is asked, and nothing said, while the form's button is disabled. */
  async changePassword(): Promise<void> {
    if (this.busy() || this.oldPassword === '' || !this.strongEnough()) return;
    this.busy.set(true);
    // What an earlier change came to is not said of this one.
    this.changeMessage.set(undefined);
    try {
      const result = await this.client.auth.changePassword(this.oldPassword, this.newPassword);
      if (result.outcome === 'changed') {
        this.oldPassword = this.newPassword = '';
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

  /** Asks the server to mail the code of a first password, in place of any it sent before. */
  async mailFirstPasswordCode(): Promise<void> {
    if (this.busy()) return;
    this.busy.set(true);
    // What an earlier step came to is not said of this one.
    this.firstPasswordMessage.set(undefined);
    try {
      const result = await this.client.auth.sendFirstPasswordCode();
      if (result.outcome === 'sent') {
        this.codeSentTo.set(result.codeEmail);
      } else {
        this.firstPasswordMessage.set(result.message);
      }
    } finally {
      this.busy.set(false);
    }
  }

  /** Sets the first password with the code; nothing is asked, and nothing said, while the form's button is disabled. */
  async setFirstPassword(): Promise<void> {
    if (this.busy() || !this.strongEnough()) return;
    this.busy.set(true);
    this.firstPasswordMessage.set(undefined);
    try {
      const result = await this.client.auth.setFirstPassword(this.firstPasswordCode, this.firstPassword);
      if (result.outcome === 'set') {
        this.firstPasswordCode = this.firstPassword = '';
        this.codeSentTo.set(undefined);
        this.passwordChosen.set(true);
        // The person holds a password now: the choice of a first one goes, here and
        // wherever else the page is opened, and its change takes its place.
        this.account.rereadSignInMeans();
      } else {
        this.firstPasswordMessage.set(result.message);
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
