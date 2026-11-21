import { HttpErrorResponse } from '@angular/common/http';
import { Component, computed, DestroyRef, inject, signal, TemplateRef } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatDialog, MatDialogModule, MatDialogRef } from '@angular/material/dialog';
import { MatFormFieldModule } from '@angular/material/form-field';
import { MatSelectModule } from '@angular/material/select';
import { MatTableModule } from '@angular/material/table';
import { Methods, Permissions, Resources } from '@app/service/zz_gen_constants';
import { CrewSectorUsers } from '@app/service/zz_gen_resources';
import { nameInWords } from '@impulseframework/resource-angular/types';
import { openFormDialog } from '../form-dialog';
import { ImpersonationService } from '../impersonation.service';
import { SectorService } from '../sector.service';

/**
 * The squadron roster: the sector's wings and squadrons (the Wing Commander sees only
 * Forge Wing's, through subject.wings), the crew, and the three kinds of impersonation.
 * "View as" mints a read-only session as another crew member; "Act as" mints one that
 * does what they may do, except change how they sign in or who may do what; "Act as
 * role" mints a session under a role with subject still bound to you. Each control
 * renders only when the digest carries its own manual Execute registration, checked by
 * the generated Methods constants, never a hand-typed string: the grant to view is not
 * the grant to act.
 *
 * The page draws no form. Each of the three is a button that opens its picker in a dialog
 * over the page, as the library's lists open their create: the dialog is titled with the
 * button's words and carries Cancel and those words as its buttons, Cancel and Escape
 * close it with nothing minted, and a click outside does nothing. A mint the server
 * refuses is shown in the dialog, in the server's words. The table of squadrons is the
 * page's own and reads only.
 *
 * The page draws once it knows: nothing until the grants and its lists are in, then the
 * whole page, "not for your posting" only where the grants say so and "no squadrons" only
 * where the sector has none. Nothing is drawn and then taken away. A role is offered by
 * its name in words ("Sector marshal"), and sent to the server by its name. A person is
 * offered by the name their account carries with the username beside it ("Cadet Cass
 * (cadet)"), by the username alone where the account has no name, and sent by the id of
 * the account.
 *
 * Demonstrates: impersonation.view-as, impersonation.act-as-user, impersonation.act-as-role, @manualAddResource.execute, paging.limit-all, @subjectSet.dotted-value.
 */
@Component({
  selector: 'app-roster',
  imports: [
    FormsModule,
    MatButtonModule,
    MatCardModule,
    MatDialogModule,
    MatFormFieldModule,
    MatSelectModule,
    MatTableModule,
  ],
  templateUrl: './roster.component.html',
  styleUrl: './roster.component.scss',
})
export class RosterComponent {
  sectors = inject(SectorService);
  impersonation = inject(ImpersonationService);
  private dialog = inject(MatDialog);

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
  // The people of the open sector, for "view as" and "act as". The server names a person
  // by the id of their account, which the sector's people list carries beside the
  // username; a caller who cannot list the sector's people has nobody to pick, so neither
  // button is offered.
  people = this.sectors.sectorList((sector) => sector.crewSectorUsers, { limit: 200 });
  canViewAs = computed(
    () =>
      this.sectors.can(Permissions.Execute, Methods.ViewAsUser) &&
      this.sectors.can(Permissions.List, Resources.CrewSectorUsers),
  );
  canActAs = computed(
    () =>
      this.sectors.can(Permissions.Execute, Methods.ActAsUser) &&
      this.sectors.can(Permissions.List, Resources.CrewSectorUsers),
  );
  canAssumeRole = computed(() => this.sectors.can(Permissions.Execute, Methods.AssumeRole));
  alreadyImpersonating = computed(() => this.impersonation.record() !== undefined);
  /** Whether the page knows what it will draw: the grants are in, and each list it reads has answered. */
  ready = computed(() => this.sectors.settled(this.wings, this.squadrons, this.memberships, this.pilots, this.people));

  readonly roles = ['Dispatcher', 'Cadet', 'Pilot', 'Engineer', 'Archivist', 'BookingAgent', 'SectorMarshal'];

  /** The id of the account of the person to view as, and of the person to act as. */
  viewAsUserId = '';
  actAsUserId = '';
  assumeRoleName = '';
  // The picker that is open over the page, if one is.
  private opened: MatDialogRef<unknown> | undefined;
  // The server's refusal of the session the open picker asked for, shown in the dialog.
  refusal = signal<string | undefined>(undefined);

  constructor() {
    // A picker left open goes with the page: a minted session lands on the dashboard.
    inject(DestroyRef).onDestroy(() => this.close());
  }

  /** A role as the picker names it: its name in words, "Booking agent" for BookingAgent. */
  roleWords(role: string): string {
    return nameInWords(role);
  }

  /**
   * A person of the sector as the picker names them: the name their account carries with
   * the username beside it, and the username alone where the account has no name.
   */
  personLabel(person: CrewSectorUsers): string {
    const name = person.name?.trim() ?? '';
    const username = person.username ?? '';
    if (name !== '' && username !== '') {
      return `${name} (${username})`;
    }
    return name || username || 'No name to show';
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

  /** Opens one of the three pickers, with nothing picked, in a dialog over the page. */
  open(form: TemplateRef<unknown>): void {
    this.viewAsUserId = '';
    this.actAsUserId = '';
    this.assumeRoleName = '';
    this.refusal.set(undefined);
    this.opened = openFormDialog(this.dialog, form);
  }

  /** Closes the open picker; nothing is minted. */
  close(): void {
    this.opened?.close();
    this.opened = undefined;
  }

  async viewAs(): Promise<void> {
    if (!this.viewAsUserId) return;
    await this.mint(() => this.impersonation.viewAs(this.viewAsUserId));
  }

  async actAs(): Promise<void> {
    if (!this.actAsUserId) return;
    await this.mint(() => this.impersonation.actAs(this.actAsUserId));
  }

  async assumeRole(): Promise<void> {
    if (!this.assumeRoleName) return;
    await this.mint(() => this.impersonation.assumeRole(this.assumeRoleName));
  }

  /**
   * Asks for the session and closes the picker once it is minted. The mint route is
   * hand-written and asked through HttpClient, so its refusal is an HttpErrorResponse:
   * the dialog shows the server's message, and the picker stays as it was.
   */
  private async mint(ask: () => Promise<void>): Promise<void> {
    this.refusal.set(undefined);
    try {
      await ask();
    } catch (e) {
      if (e instanceof HttpErrorResponse && e.status > 0) {
        this.refusal.set((e.error as { message?: string } | null)?.message || 'The server gave no reason.');
        return;
      }
      throw e;
    }
    this.close();
  }
}
