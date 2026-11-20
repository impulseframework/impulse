import { HttpClient } from '@angular/common/http';
import { DatePipe } from '@angular/common';
import { Component, computed, inject, resource, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatTableModule } from '@angular/material/table';
import { Methods, Permissions } from '@app/service/zz_gen_constants';
import { API_URL } from '@impulseframework/resource-angular/types';
import { firstValueFrom } from 'rxjs';
import { SectorService } from '../sector.service';

/** One live impersonated session as the watch desk lists it. */
interface ActiveImpersonation {
  sessionId: string;
  actor: string;
  principal: string;
  kind: 'user' | 'role';
  /**
   * What the session may do, in the server's words: "reading only", or for a session that
   * acts as a person "everything except access actions, credential actions, writing
   * ClientContacts, writing HangarPasses, writing PilotCertifications, writing Pilots,
   * writing SquadronMemberships and changing Squadrons".
   */
  mask: string;
  reason: string;
  startedAt: string;
  expiresAt: string;
}

/**
 * The Governor's watch desk: every live impersonated session in the service
 * (ActiveImpersonations), who established it, as whom, what it may do (its mask, in
 * the server's words), why, and when its two-hour cap ends it, with a Revoke control
 * (DestroyImpersonatedSession). The revoked console's
 * next request is refused and its banner explains. The desk is gated by the same manual
 * Execute registration the mint route checks, ViewAsUser.
 *
 * Demonstrates: impersonation.active-list, impersonation.revoke, impersonation.max-duration.
 */
@Component({
  selector: 'app-watch-desk',
  imports: [DatePipe, MatButtonModule, MatCardModule, MatTableModule],
  templateUrl: './watch-desk.component.html',
  styleUrl: './watch-desk.component.scss',
})
export class WatchDeskComponent {
  private http = inject(HttpClient);
  private apiUrl = inject(API_URL);
  sectors = inject(SectorService);

  canOperate = computed(() => this.sectors.can(Permissions.Execute, Methods.ViewAsUser));
  columns = ['actor', 'principal', 'mask', 'reason', 'startedAt', 'expiresAt', 'actions'];
  refusal = signal<string | undefined>(undefined);

  sessions = resource({
    params: () => ({ allowed: this.canOperate() }),
    loader: ({ params }) =>
      params.allowed
        ? firstValueFrom(this.http.get<ActiveImpersonation[]>(`${this.apiUrl}/impersonations`))
        : Promise.resolve([]),
    defaultValue: [] as ActiveImpersonation[],
  });

  async revoke(session: ActiveImpersonation): Promise<void> {
    this.refusal.set(undefined);
    await firstValueFrom(this.http.delete(`${this.apiUrl}/impersonations/${session.sessionId}`));
    this.sessions.reload();
  }
}
