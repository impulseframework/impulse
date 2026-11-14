import { DestroyRef, inject, signal } from '@angular/core';

/** The code the server refuses mail to an address with while it holds it. */
const tooManyEmails = 'too_many_emails';

/**
 * A component's hold on asking for mail while the server holds its mail to an address:
 * the address was sent as many sign-in codes and links as the server allows, none of them
 * used, and the server refuses another with `too_many_emails` and the wait in seconds
 * (`retryAfter`). `held` is true until that wait has passed, and the buttons that ask for
 * mail read it to stay disabled. A refusal without a wait holds nothing, since nothing
 * says when to let go, and any other refusal is ignored.
 *
 * Create it in an injection context, a component's field initializer: its timer is
 * cleared when the component is destroyed.
 *
 *     private mailHold = new MailHold();
 *     mailHeld = this.mailHold.held;
 *     ...
 *     if (result.outcome === 'refused') this.mailHold.hold(result);
 */
export class MailHold {
  private readonly heldSignal = signal(false);
  /** The server holds its mail to the address: the wait it named has not passed. */
  readonly held = this.heldSignal.asReadonly();
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor() {
    inject(DestroyRef).onDestroy(() => clearTimeout(this.timer));
  }

  /** Holds the mail for the refusal's wait when the refusal is the mail limit's. */
  hold(refusal: { readonly code?: string; readonly retryAfter?: number }): void {
    const wait = refusal.retryAfter;
    if (refusal.code !== tooManyEmails || wait === undefined || !Number.isFinite(wait) || wait <= 0) {
      return;
    }
    clearTimeout(this.timer);
    this.heldSignal.set(true);
    this.timer = setTimeout(
      () => {
        this.timer = undefined;
        this.heldSignal.set(false);
      },
      Math.ceil(wait) * 1000,
    );
  }
}
