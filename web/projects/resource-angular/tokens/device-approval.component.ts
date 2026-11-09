import { DatePipe } from '@angular/common';
import { Component, computed, effect, inject, input, PendingTasks, signal, untracked } from '@angular/core';
import { toSignal } from '@angular/core/rxjs-interop';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatInputModule } from '@angular/material/input';
import { ActivatedRoute } from '@angular/router';
import { DeviceRequest, isTokenRefusal, userTokens } from '@impulseframework/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { map } from 'rxjs';

import { noTokensSentence } from './tokens.component';

/** The sentence shown when no request waits on the code: the server says the same for an unknown, expired, decided or consumed one. */
export const noRequestSentence = 'No request is waiting for that code. It may have expired or was already answered.';

/** What the person decided about the request. */
export type DeviceDecision = 'approved' | 'denied';

/**
 * The page where the signed-in person answers a command-line client's request to act as
 * them: the program printed a code and a link, and the person approves or denies the
 * request the code stands for. The component takes the code as `userCode`, else reads it
 * from the `userCode` query parameter of the route, the link the program printed; with a
 * code it looks the request up and shows which program asks, for which outlet and until
 * when, with Approve and Deny; without one it asks for the code the program printed, in
 * any case and spacing, and looks it up on Continue. After a decision it says so; when no
 * request waits on the code, expired or already answered, it says so and asks for a code
 * again. A refusal of a decision is shown in place in the server's words. Where the
 * descriptor carries no tokens block, the page says in one sentence that the application
 * issues none.
 *
 * The page belongs at the application's device verification path (`/device` by default,
 * what the program prints), behind the sign-in guard, so a signed-out person returns to
 * it, code and all, after signing in.
 *
 *     <impulse-device-approval />
 *     <impulse-device-approval [userCode]="code" />
 */
@Component({
  selector: 'impulse-device-approval',
  imports: [DatePipe, FormsModule, MatButtonModule, MatFormFieldModule, MatInputModule],
  templateUrl: './device-approval.component.html',
  styleUrl: './device-approval.component.scss',
})
export class DeviceApprovalComponent {
  private client = inject(RESOURCE_CLIENT);
  private route = inject(ActivatedRoute);
  /** Every request is registered as pending work, so the application is stable only once it has answered. */
  private pendingTasks = inject(PendingTasks);

  /** The code the program printed, when the page is given it; else the route's `userCode` query parameter. */
  userCode = input<string | undefined>();

  /** The person's tokens over the client; undefined where the descriptor carries no tokens block. */
  readonly tokens = userTokens(this.client);
  /** The sentence shown instead of the page where the application issues no tokens. */
  readonly noTokensSentence = noTokensSentence;

  /** The code the route's query carries, or undefined. */
  private fromQuery = toSignal(this.route.queryParamMap.pipe(map((params) => params.get('userCode') ?? undefined)), {
    initialValue: undefined,
  });
  /** The code the page arrived with: the input, else the query parameter; undefined for the form. */
  private given = computed(() => this.userCode() ?? this.fromQuery());

  /** The code field of the form. */
  code = '';
  /** The code the request on screen was looked up by, trimmed; what a decision posts. */
  current = signal<string | undefined>(undefined);
  /** The request waiting on the code, once looked up. */
  request = signal<DeviceRequest | undefined>(undefined);
  /** What the person decided, once they have. */
  decision = signal<DeviceDecision | undefined>(undefined);
  /** No request waits on the code the person gave: the sentence shows over the form. */
  refused = signal(false);
  /** A look-up or a decision is in flight; the controls are disabled meanwhile. */
  busy = signal(false);
  /** The refusal of the last decision, or a failure, in the server's words. */
  notice = signal<string | undefined>(undefined);

  constructor() {
    effect(() => {
      const code = this.given();
      untracked(() => {
        if (code !== undefined && code.trim() !== '') {
          this.code = code;
          void this.tracked(() => this.find(code));
        }
      });
    });
  }

  /** The words for what was decided. */
  decided(): string {
    return this.decision() === 'approved' ? 'Approved. The program can continue.' : 'Denied. The program was told no.';
  }

  /** The sentence shown when no request waits on the code. */
  noRequestSentence(): string {
    return noRequestSentence;
  }

  /** Looks up the request the code in the form stands for, the code trimmed; the server reads it in any case and spacing. */
  lookUp(): Promise<void> {
    if (this.busy()) {
      return Promise.resolve();
    }
    return this.tracked(() => this.find(this.code));
  }

  /** Approves the request on screen: the program's next poll receives its tokens. */
  approve(): Promise<void> {
    return this.decide('approved');
  }

  /** Denies the request on screen: the program's next poll is told no. */
  deny(): Promise<void> {
    return this.decide('denied');
  }

  /** Posts the decision for the code the request was looked up by; a refusal is shown in place. */
  private decide(decision: DeviceDecision): Promise<void> {
    const tokens = this.tokens;
    const code = this.current();
    if (!tokens || code === undefined || this.busy() || !this.request()) {
      return Promise.resolve();
    }
    return this.tracked(async () => {
      this.busy.set(true);
      this.notice.set(undefined);
      try {
        const result = decision === 'approved' ? await tokens.approve(code) : await tokens.deny(code);
        if (isTokenRefusal(result)) {
          this.request.set(undefined);
          this.current.set(undefined);
          this.refused.set(true);
          return;
        }
        this.decision.set(decision);
      } catch (error) {
        this.notice.set(messageOf(error));
      } finally {
        this.busy.set(false);
      }
    });
  }

  /** The look-up itself: the request on screen when one waits, the sentence and the form when none does. */
  private async find(code: string): Promise<void> {
    const tokens = this.tokens;
    const trimmed = code.trim();
    if (!tokens || trimmed === '') {
      return;
    }
    this.busy.set(true);
    this.notice.set(undefined);
    this.decision.set(undefined);
    this.request.set(undefined);
    this.refused.set(false);
    try {
      const result = await tokens.device(trimmed);
      if (isTokenRefusal(result)) {
        this.current.set(undefined);
        this.refused.set(true);
        return;
      }
      this.current.set(trimmed);
      this.request.set(result);
    } catch (error) {
      this.notice.set(messageOf(error));
    } finally {
      this.busy.set(false);
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
