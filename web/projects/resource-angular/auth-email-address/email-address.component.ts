import { Component, computed, inject, OnInit, PendingTasks, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { SignInRefusal } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT, storeSignal } from '@impulseframework/resource-angular/resource-client';
import { UiCoreService } from '@impulseframework/resource-angular/ui-core-service';

/**
 * The signed-in person's email address, for an account page. The component reads the
 * address from the session the AuthService last checked (the address, whether the person
 * has confirmed it, and the address a directory sign-in asserted, offered as a prefill
 * while they hold none) and offers to add or change it where the server sends mail: the
 * methods list names the email routes. The person types an address and the server mails
 * it a confirmation link; nothing is written until the link is followed on the login page,
 * so an address never attaches to an account its follower does not control, and the
 * screen says the link is on its way whatever the address's state (an address another
 * account holds is told so by mail). A refusal (not an address; too many links to one
 * mailbox, with the wait) is rendered in place.
 *
 *     <impulse-email-address />
 */
@Component({
  selector: 'impulse-email-address',
  imports: [FormsModule, MatButtonModule, MatFormFieldModule, MatInputModule],
  templateUrl: './email-address.component.html',
  styleUrl: './email-address.component.scss',
})
export class EmailAddressComponent implements OnInit {
  private client = inject(RESOURCE_CLIENT);
  private auth = inject(AuthService);
  private ui = inject(UiCoreService);
  /** Every request is registered as pending work, so the application is stable only once it has answered. */
  private pendingTasks = inject(PendingTasks);

  /** The address the form holds. */
  email = '';
  /** The form is shown. */
  editing = signal(false);
  /** The address a confirmation link was sent to; the note takes the form's place. */
  sent = signal<string | undefined>(undefined);
  /** A request is in flight; the controls are disabled meanwhile. */
  busy = signal(false);
  /** The refusal of the last request, in the application's sentence or the server's words. */
  message = signal<string | undefined>(undefined);

  /** The session as the service last checked it: the address and its state. */
  session = this.auth.sessionInfo;
  /** The methods list as the client last read it; the email routes say whether the server sends mail. */
  private list = storeSignal(this.client.auth.snapshot);
  /** The server sends mail: an address may be added or changed here. */
  offered = computed(() => this.list()?.email !== undefined);
  /** The words for the address on the account: confirmed, unconfirmed, or none. */
  current = computed(() => {
    const session = this.session();
    if (!session.email) {
      return 'No email address is on your account.';
    }
    return session.emailVerified
      ? `${session.email} is your email address.`
      : `${session.email} is on your account but not confirmed yet.`;
  });
  /** The words on the button: what the form does to the account. */
  action = computed(() => {
    const session = this.session();
    if (!session.email) {
      return 'Add an address';
    }
    return session.emailVerified ? 'Change the address' : 'Confirm the address';
  });

  ngOnInit(): void {
    if (this.client.auth.get() === undefined) {
      void this.tracked(async () => {
        try {
          await this.client.auth.methods();
        } catch (error) {
          this.message.set(messageOf(error));
        }
      });
    }
  }

  /** Shows the form, prefilled with the address on the account, else the one a directory asserted. */
  edit(): void {
    if (this.busy()) {
      return;
    }
    const session = this.session();
    this.email = session.email ?? session.emailHint ?? '';
    this.message.set(undefined);
    this.sent.set(undefined);
    this.editing.set(true);
  }

  /** Posts the address: a confirmation link is mailed to it, and the note says so whatever the address's state. */
  send(): Promise<void> {
    if (this.busy() || !this.editing()) {
      return Promise.resolve();
    }
    return this.tracked(async () => {
      this.busy.set(true);
      this.message.set(undefined);
      try {
        const result = await this.client.auth.addEmail(this.email);
        if (result.outcome === 'refused') {
          this.message.set(this.sentenceOf(result));
          return;
        }
        this.sent.set(this.email);
        this.editing.set(false);
      } catch (error) {
        this.message.set(messageOf(error));
      } finally {
        this.busy.set(false);
      }
    });
  }

  /** Leaves the form, or the note, and shows the address as it is. */
  done(): void {
    if (this.busy()) {
      return;
    }
    this.editing.set(false);
    this.sent.set(undefined);
    this.message.set(undefined);
  }

  /** The sentence for a refusal: the one the application holds for its code, with the wait the server named, else the server's words. */
  private sentenceOf(refusal: SignInRefusal): string {
    const held = refusal.code === undefined ? '' : this.ui.loginMessage(refusal.code, refusal.retryAfter);
    return held || refusal.message;
  }

  /** Runs one piece of work as a pending task of the application, released when it settles. */
  private async tracked<T>(work: () => Promise<T>): Promise<T> {
    const release = this.pendingTasks.add();
    try {
      return await work();
    } finally {
      release();
    }
  }
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
