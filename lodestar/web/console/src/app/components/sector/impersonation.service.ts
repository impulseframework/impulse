import { HttpClient } from '@angular/common/http';
import { computed, inject, Injectable, signal } from '@angular/core';
import { Router } from '@angular/router';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { API_URL, SessionInfo } from '@impulseframework/resource-angular/types';
import { firstValueFrom } from 'rxjs';

/** The impersonation record the session endpoint reports for a minted session. */
export interface ImpersonationRecord {
  actor: string;
  principalKind: 'User' | 'Role';
  /** The role's name, or the id of the account of the person the session acts as. */
  principal: string;
  /** The permission kinds the session keeps; absent when it keeps every kind. */
  mask?: string[];
  /** The classes of action the session is refused: access and credential for one that acts as a person. */
  refusedClasses?: string[];
  /**
   * The session's whole mask in the server's words: "reading only", or for a session that
   * acts as a person "everything except access actions and credential actions".
   */
  maskWords: string;
  reason?: string;
  expiresAt: string;
}

/** The kinds of impersonation the console offers, as the mint route names them. */
type ImpersonationKind = 'view' | 'act' | 'role';

/** The session endpoint's answer as the crew auth writes it: the library's SessionInfo plus the record. */
type SessionResponse = SessionInfo & { impersonation?: ImpersonationRecord };

interface EndImpersonationResponse {
  /** Whether the actor's own session was still live and the browser is back in it. */
  restored: boolean;
}

/**
 * ImpersonationService drives the three kinds of impersonation the console offers.
 * "View as" mints a session that reads what another person reads and writes nothing;
 * "act as" mints one that does what the person may do, except change how they sign in
 * or who may do what;
 * "act as a role" mints one that holds what a role holds, with subject still bound to
 * the actor. Each kind is a grant of its own (ViewAsUser, ActAsUser, AssumeRole), and
 * the server gives each its mask: the session endpoint reports the record back with the
 * mask in words, which the frame draws above its bar as the persistent banner
 * (ImpersonationBannerComponent).
 *
 * Demonstrates: impersonation.view-as, impersonation.act-as-user, impersonation.act-as-role, impersonation.end, impersonation.mask.
 */
@Injectable({ providedIn: 'root' })
export class ImpersonationService {
  private http = inject(HttpClient);
  private auth = inject(AuthService);
  private router = inject(Router);
  private apiUrl = inject(API_URL);

  /**
   * The current session's impersonation record, if the session was minted. It is read
   * off the library's session signal rather than fetched on its own: checkUserSession
   * stores the endpoint's whole answer, and login, logout, the route guard, and a
   * rebind all run it, so the record follows the cookie. A record kept in a separate
   * signal outlived a revoked session — the banner stayed up through the forced
   * re-login until a hard reload.
   */
  readonly record = computed<ImpersonationRecord | undefined>(() => {
    const session = this.auth.sessionInfo() as SessionResponse;
    return session.authenticated ? session.impersonation : undefined;
  });

  /** A clock the banner's countdown ticks on. */
  private readonly now = signal(Date.now());

  readonly banner = computed(() => {
    const record = this.record();
    if (!record) return undefined;
    const remaining = this.remaining(record.expiresAt);
    if (record.principalKind === 'Role') {
      return {
        kind: 'Role' as const,
        text: `Acting as role ${record.principal}. You are ${record.actor}; subject still binds to you.`,
        remaining,
      };
    }
    // The record names the person by the id of their account. The minted session is
    // theirs, so the session's own username is the name a person reads. A session that
    // keeps nothing beyond reading views as the person; any other acts as them.
    const person = (this.auth.sessionInfo() as SessionResponse).username;
    const views = !!record.mask?.length && record.mask.every((kind) => kind === 'List' || kind === 'Read');
    return {
      kind: 'User' as const,
      text: `${views ? 'Viewing' : 'Acting'} as ${person}: ${record.maskWords}. You are ${record.actor}.`,
      remaining,
    };
  });

  /** The time left before the session's hard cap (MaxDuration, two hours) ends it. */
  private remaining(expiresAt: string): string {
    const ms = new Date(expiresAt).getTime() - this.now();
    if (Number.isNaN(ms)) return '';
    if (ms <= 0) return 'expired';
    const h = Math.floor(ms / 3600000);
    const m = Math.floor((ms % 3600000) / 60000);
    const s = Math.floor((ms % 60000) / 1000);
    return h > 0 ? `${h}h ${m}m left` : `${m}m ${s}s left`;
  }

  constructor() {
    setInterval(() => this.now.set(Date.now()), 1000);
  }

  /** Whether the session mask removes the permission (the service card's stripe). */
  masked(permission: string): boolean {
    const mask = this.record()?.mask;
    return !!mask?.length && !mask.includes(permission);
  }

  /** Mints a read-only session as another person, named by the id of their account, and reloads the console as them. */
  async viewAs(userId: string): Promise<void> {
    await this.mint({ kind: 'view', principal: userId, reason: 'crew roster: view as' });
  }

  /**
   * Mints a session that acts as another person, named by the id of their account: what
   * they may do, except change how they sign in or who may do what. The server's mask
   * says so, and the banner repeats it.
   */
  async actAs(userId: string): Promise<void> {
    await this.mint({ kind: 'act', principal: userId, reason: 'crew roster: act as' });
  }

  /** Mints a session that acts as a role and reloads the console under it. */
  async assumeRole(role: string): Promise<void> {
    await this.mint({ kind: 'role', principal: role, reason: 'crew roster: assume role' });
  }

  private async mint(body: { kind: ImpersonationKind; principal: string; reason: string }): Promise<void> {
    await firstValueFrom(this.http.post(`${this.apiUrl}/impersonate`, body));
    await this.rebind();
  }

  /**
   * Ends the minted session (its record ends Released) and returns to the actor's own
   * session when that session is still live. Resolves true when the browser is back in
   * the actor's session; false when it is not (the source session expired or the hard
   * cap passed), in which case the caller sends the actor to login.
   */
  async end(): Promise<boolean> {
    const { restored } = await firstValueFrom(
      this.http.post<EndImpersonationResponse>(`${this.apiUrl}/impersonate/end`, {}),
    );
    if (restored) {
      await this.rebind();
    }
    return restored;
  }

  /**
   * The cookie now names a different session: re-establish the client-side session and
   * its permission cache as the new principal, then start over at the dashboard.
   */
  private async rebind(): Promise<void> {
    this.auth.permissions.clear();
    await firstValueFrom(this.auth.checkUserSession());
    await this.auth.permissions.refresh();
    await this.router.navigateByUrl('/dashboard');
  }
}
