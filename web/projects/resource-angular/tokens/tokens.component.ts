import { DatePipe } from '@angular/common';
import { Component, inject, OnInit, PendingTasks, signal } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatIconModule } from '@angular/material/icon';
import { MatInputModule } from '@angular/material/input';
import { MatSelectModule } from '@angular/material/select';
import { MatTooltipModule } from '@angular/material/tooltip';
import { IssuedToken, isTokenRefusal, MachineOutletDescriptor, TokenRow, userTokens } from '@impulseframework/resource';
import { askInDialog } from '@impulseframework/resource-angular/dialogs';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';

/** The longest name a token takes; the server refuses a longer one with its own words. */
export const tokenNameMaxLength = 200;

/** The sentence a page shows where the descriptor carries no tokens block: the Auth issues none. */
export const noTokensSentence = 'This application issues no tokens for the signed-in person.';

/** The sentence over the issue form: what a token is, and what to name it. */
export const tokenIntro =
  'A token lets a device or a program of yours use this application as you. Name it for what it is for, so you know it in the list.';

/**
 * The signed-in person's tokens for the application's machine outlets, for an account
 * page. The component configures itself from the descriptor's `tokens` block: the routes,
 * and the machine outlets a token can be issued for. It lists the person's live tokens as
 * the server serves them, newest first: an application token by its name, a device grant
 * (the pair a command-line client holds after the person approved its request) by the
 * client's name and the kind of each row, access or refresh, with the outlet, when it was
 * created, when it expires and when it was last used; the outlet column shows only where
 * the application has several outlets. The page that composes the component names it (a
 * heading of its own would repeat the page's), and one sentence over the form says what a
 * token is for. The form issues an application token by name for one outlet, chosen where
 * there are several and fixed where there is one; the token's value is shown once, in a
 * field with a Copy button, and never again. A row's trash can asks in a dialog that names
 * the token and says what happens, then revokes in one call; revoking either row of a
 * device grant revokes the pair, and the dialog says so. A refusal, by the server or by the
 * network, is shown in place in the server's words, and the list is read again after every
 * write. Where the descriptor carries no block, the page says in one sentence that the
 * application issues none.
 *
 *     <impulse-tokens />
 */
@Component({
  selector: 'impulse-tokens',
  imports: [
    DatePipe,
    FormsModule,
    MatButtonModule,
    MatFormFieldModule,
    MatIconModule,
    MatInputModule,
    MatSelectModule,
    MatTooltipModule,
  ],
  templateUrl: './tokens.component.html',
  styleUrl: './tokens.component.scss',
})
export class TokensComponent implements OnInit {
  private client = inject(RESOURCE_CLIENT);
  private dialog = inject(MatDialog);
  /** Every request is registered as pending work, so the application is stable only once it has answered. */
  private pendingTasks = inject(PendingTasks);

  /** The person's tokens over the client; undefined where the descriptor carries no tokens block. */
  readonly tokens = userTokens(this.client);
  /** The machine outlets a token can be issued for. */
  readonly outlets: readonly MachineOutletDescriptor[] = this.tokens?.outlets ?? [];
  /** The sentence shown instead of the page where the application issues no tokens. */
  readonly noTokensSentence = noTokensSentence;
  readonly intro = tokenIntro;
  readonly nameMaxLength = tokenNameMaxLength;
  /** The dates as the table shows them, to the minute: "Oct 28, 2026, 6:34 PM". */
  readonly dateFormat = 'MMM d, y, h:mm a';

  /** The person's live tokens, newest first as served. */
  rows = signal<readonly TokenRow[]>([]);
  /** True until the first read answers. */
  loading = signal(true);
  /** The refusal of the list itself, in the server's words. */
  error = signal<string | undefined>(undefined);
  /** The refusal of the last action, in the server's words; cleared by the next action. */
  notice = signal<string | undefined>(undefined);
  /** An action is in flight; the controls are disabled meanwhile. */
  busy = signal(false);
  /** The token just issued, its value shown this once; cleared when the person is done with it. */
  issued = signal<IssuedToken | undefined>(undefined);
  /** The value was put on the clipboard. */
  copied = signal(false);

  /** The issue form: the name, and the outlet, preset where there is one. */
  name = '';
  outlet = this.outlets.length === 1 ? this.outlets[0].name : '';

  ngOnInit(): void {
    void this.tracked(() => this.load());
  }

  /** Whether the issue form is offered: the Auth has an outlet to issue for. */
  canIssue(): boolean {
    return this.outlets.length > 0;
  }

  /** Whether the form offers a choice of outlet and the table shows the column: there are several. */
  severalOutlets(): boolean {
    return this.outlets.length > 1;
  }

  /** The words for a row: an application token's name, a device grant's client. */
  labelOf(row: TokenRow): string {
    return row.kind === 'application' ? (row.name ?? '') : (row.clientId ?? '');
  }

  /** The words for a row's kind. */
  kindOf(row: TokenRow): string {
    switch (row.kind) {
      case 'application':
        return 'Application token';
      case 'access':
        return 'Access token of a program';
      case 'refresh':
        return 'Refresh token of a program';
      default:
        return row.kind;
    }
  }

  /** What a revoke does, under the dialog's question: for a device grant, that the pair goes together. */
  revokeWords(row: TokenRow): string {
    if (row.kind === 'application') {
      return 'A device or a program using it stops working at once.';
    }
    return 'Its access and refresh tokens go together, and the program holding them stops working at once.';
  }

  /** Issues the token the form describes; its value is shown once, and the list is read again. */
  issue(): Promise<void> {
    const tokens = this.tokens;
    const name = this.name.trim();
    if (!tokens || !this.canIssue() || name === '' || this.outlet === '') {
      return Promise.resolve();
    }
    const outlet = this.outlet;
    return this.act(async () => {
      const result = await tokens.issue(name, outlet);
      if (isTokenRefusal(result)) {
        this.notice.set(result.message);
        return;
      }
      this.issued.set(result);
      this.copied.set(false);
      this.name = '';
    });
  }

  /**
   * Puts the issued value on the clipboard where the browser offers one, and selects the
   * field's text either way, so the person can copy it by hand where it does not.
   */
  async copy(field: HTMLInputElement): Promise<void> {
    const value = this.issued()?.token;
    if (value === undefined) {
      return;
    }
    field.focus();
    field.select();
    const clipboard = globalThis.navigator?.clipboard;
    if (!clipboard) {
      return;
    }
    try {
      await clipboard.writeText(value);
      this.copied.set(true);
    } catch {
      // The clipboard refused (a document out of focus, a permission withheld); the selected text is the way to copy it.
      this.copied.set(false);
    }
  }

  /** Leaves the issued value: it is not shown again. */
  dismissIssued(): void {
    this.issued.set(undefined);
    this.copied.set(false);
  }

  /** Asks in a dialog that names the token; on Revoke, revokes it in one call and reads the list again. */
  async revoke(row: TokenRow): Promise<void> {
    const tokens = this.tokens;
    if (!tokens || this.busy()) {
      return;
    }
    this.notice.set(undefined);
    const confirmed = await askInDialog(this.dialog, {
      title: `Revoke ${this.labelOf(row)}?`,
      words: this.revokeWords(row),
      confirm: 'Revoke',
    });
    if (confirmed !== true) {
      return;
    }
    await this.act(async () => {
      const result = await tokens.revoke(row.id);
      if (isTokenRefusal(result)) {
        this.notice.set(result.message);
      }
    });
  }

  /** The list again, on request. */
  reload(): Promise<void> {
    return this.act(() => Promise.resolve());
  }

  /** Runs one action as pending work, busy meanwhile, then reads the list again whatever happened. */
  private act(work: () => Promise<void>): Promise<void> {
    if (this.busy()) {
      return Promise.resolve();
    }
    return this.tracked(async () => {
      this.busy.set(true);
      this.notice.set(undefined);
      try {
        await work();
      } catch (error) {
        this.notice.set(messageOf(error));
      } finally {
        await this.refresh();
        this.busy.set(false);
      }
    });
  }

  /** The first read: the person's tokens, or the sentence where the application issues none. */
  private async load(): Promise<void> {
    if (!this.tokens) {
      this.loading.set(false);
      return;
    }
    this.loading.set(true);
    this.error.set(undefined);
    try {
      this.rows.set(await this.tokens.list());
    } catch (error) {
      this.error.set(messageOf(error));
    } finally {
      this.loading.set(false);
    }
  }

  /** The list again; a failed refresh is a notice, not a blank page. */
  private async refresh(): Promise<void> {
    if (!this.tokens) {
      return;
    }
    try {
      this.rows.set(await this.tokens.list());
    } catch (error) {
      this.notice.set(this.notice() ?? messageOf(error));
    }
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
