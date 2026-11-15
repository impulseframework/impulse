import { Component, computed, inject } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatSelectModule } from '@angular/material/select';
import { MatTableModule } from '@angular/material/table';
import { Methods, Permissions, Resources } from '@app/service/zz_gen_constants';
import { CREW_MANIFEST } from '@components/login/login.component';
import { ImpersonationService } from '../impersonation.service';
import { SectorService } from '../sector.service';

/**
 * The squadron roster: the sector's wings and squadrons (the Wing Commander sees only
 * Forge Wing's, through subject.wings), the crew, and the two impersonation moments.
 * "View as" mints a read-only session as another crew member; "Act as role" mints a
 * session under a role with subject still bound to you. Both controls render only
 * when the digest carries the manual Execute registrations — checked by the generated
 * Methods constants, never a hand-typed string.
 *
 * Demonstrates: impersonation.view-as, impersonation.act-as-role, @manualAddResource.execute, paging.limit-all, @subjectSet.dotted-value.
 */
@Component({
  selector: 'app-roster',
  imports: [FormsModule, MatButtonModule, MatCardModule, MatFormFieldModule, MatSelectModule, MatTableModule],
  templateUrl: './roster.component.html',
  styleUrl: './roster.component.scss',
})
export class RosterComponent {
  sectors = inject(SectorService);
  impersonation = inject(ImpersonationService);

  wings = this.sectors.sectorList((sector) => sector.wings);
  squadrons = this.sectors.sectorList((sector) => sector.squadrons);
  memberships = this.sectors.sectorList((sector) => sector.squadronMemberships);
  // The crew roster declares a default page but no maximum, so limit: 'all' reads it
  // whole in one request, the explicit spelling of a whole read.
  pilots = this.sectors.globalList((api) => api.pilots, { limit: 'all' });
  columns = ['name', 'wing', 'members'];

  /** The open sector's name, as the session's sector list carries it. */
  sectorName = this.sectors.placeName;
  canListSquadrons = computed(() => this.sectors.can(Permissions.List, Resources.Squadrons));
  // The people of the open sector, for "view as". The server names a person by the id of
  // their account, which the sector's people list carries beside the username; a caller
  // who cannot list the sector's people has nobody to pick, so the form is not offered.
  people = this.sectors.sectorList((sector) => sector.crewSectorUsers, { limit: 200 });
  canViewAs = computed(
    () =>
      this.sectors.can(Permissions.Execute, Methods.ViewAsUser) &&
      this.sectors.can(Permissions.List, Resources.CrewSectorUsers),
  );
  canAssumeRole = computed(() => this.sectors.can(Permissions.Execute, Methods.AssumeRole));
  alreadyImpersonating = computed(() => this.impersonation.record() !== undefined);

  readonly roles = ['Dispatcher', 'Cadet', 'Pilot', 'Engineer', 'Archivist', 'BookingAgent', 'SectorMarshal'];

  /** The id of the account of the person to view as. */
  viewAsUserId = '';
  assumeRoleName = '';

  /** A person of the sector as the picker names them: the persona's name where the login is one of the cast's. */
  personLabel(username: string | undefined): string {
    const persona = CREW_MANIFEST.find((p) => p.login === username);
    return persona ? `${persona.name} (${username})` : (username ?? 'No name to show');
  }

  wingName(id: string | undefined): string {
    return this.wings.value().find((w) => w.id === id)?.name ?? '—';
  }

  /**
   * The members of a squadron by name. A membership names its person by the id of their
   * account, and the personnel registry holds the name under the same id.
   */
  members(squadronId: string): string {
    const pilots = this.pilots.value();
    return this.memberships
      .value()
      .filter((m) => m.squadronId === squadronId)
      .map((m) => pilots.find((p) => p.userId === m.userId)?.displayName ?? 'not in the registry')
      .join(', ');
  }

  async viewAs(): Promise<void> {
    if (!this.viewAsUserId) return;
    await this.impersonation.viewAs(this.viewAsUserId);
  }

  async assumeRole(): Promise<void> {
    if (!this.assumeRoleName) return;
    await this.impersonation.assumeRole(this.assumeRoleName);
  }
}
