import { effect, inject, Injectable, untracked } from '@angular/core';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { ThemeChoice, ThemeService } from '@impulseframework/resource-angular/ui-theme';

/**
 * AccountLook keeps the look a person chose with their account, so it follows them to
 * every browser they sign in from. When a person's session is first checked on a page,
 * the look the account holds is applied and remembered in the browser, which stays the
 * login page's copy and the one a page starts with. It is taken once for each sign-in:
 * a later read of the session, the keepalive's among them, takes nothing back, so a
 * change made on another device is seen at the next visit. When the person changes the
 * look, the whole look as the page shows it is written to the account. An account that
 * holds no look takes none from the browser: a browser several people share would hand
 * one person's look to the next. An impersonated session neither takes the look of the
 * person it acts as nor writes one for them.
 *
 * provideFrame starts it with the application. Changes are written one at a time, in
 * the order they were made, and a change made while one is on its way replaces any that
 * waits. A write the server refuses, or that does not reach it, is dropped: the page and
 * the browser's copy keep the look, and the next change is written.
 */
@Injectable({ providedIn: 'root' })
export class AccountLook {
  private client = inject(RESOURCE_CLIENT);
  private auth = inject(AuthService);
  private theme = inject(ThemeService);

  /** Whose look this page has taken; nobody's while signed out. */
  private takenFor: string | undefined;
  /** The change that waits for the write on its way; a later change replaces it. */
  private waiting: ThemeChoice | undefined;
  private writing = false;

  constructor() {
    effect(() => {
      const session = this.auth.sessionInfo();
      if (!this.auth.authenticated()) {
        this.takenFor = undefined;
        return;
      }
      const person = session.username ?? '';
      if (session.impersonation !== undefined || person === this.takenFor) {
        return;
      }
      this.takenFor = person;
      const look = session.look;
      if (look) {
        untracked(() => this.theme.adopt(look));
      }
    });
    effect(() => {
      const chosen = this.theme.chosen();
      if (chosen === undefined) {
        return;
      }
      untracked(() => void this.keep(chosen));
    });
  }

  /** Has the person's change written, after the write on its way and in place of one that waits. */
  private async keep(choice: ThemeChoice): Promise<void> {
    this.waiting = choice;
    if (this.writing) {
      return;
    }
    this.writing = true;
    try {
      while (this.waiting !== undefined) {
        const next = this.waiting;
        this.waiting = undefined;
        await this.write(next);
      }
    } finally {
      this.writing = false;
    }
  }

  /** Writes the look to the account, where the session is the person's own and the server keeps a look. */
  private async write(choice: ThemeChoice): Promise<void> {
    if (!this.auth.authenticated() || this.auth.sessionInfo().impersonation !== undefined) {
      return;
    }
    try {
      const methods = await this.client.auth.methods();
      if (methods.changeLook === undefined) {
        return;
      }
      await this.client.auth.changeLook(choice);
    } catch {
      // The server did not answer: the page keeps the look, and the next change is written.
    }
  }
}
