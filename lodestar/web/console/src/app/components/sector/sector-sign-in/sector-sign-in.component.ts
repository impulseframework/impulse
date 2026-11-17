import { Component, computed, inject, input, resource, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { injectApi } from '@app/api/api';
import { Permissions, Resources } from '@app/service/zz_gen_constants';
import { CrewSectorRoles, SectorDirectories, Sectors } from '@app/service/zz_gen_resources';
import { Domain, Permission } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { storeSignal } from '@impulseframework/resource-angular/resource-client';

/**
 * What the directory a sign-in method reaches is called, by the method as the session
 * library spells it. The connection that follows names the one organization: a Google
 * Workspace by its domain, a Microsoft Entra tenant by its tenant id.
 */
export const directoryKinds: Readonly<Record<string, string>> = {
  google: 'Google Workspace',
  azure: 'Microsoft Entra tenant',
  workos: 'WorkOS connection',
};

/**
 * The sector's sign-in page: who may enter the sector, as its declaration says. The
 * declaration is two things on the Sector record: the sector's directory (its row of
 * SectorDirectories: the organization its people sign in through, a sign-in method and
 * the connection its identities carry), and whether its people enter through it only
 * (the record's directory-only flag, which closes passwords).
 *
 * A sector has one directory and a directory belongs to one sector, and nobody types
 * one in. It is set from a sign-in: a person who holds the sector's Create grant on
 * SectorDirectories and is signed in through an organization's directory is offered to
 * make it the sector's, and the request names no directory, since the server sets the
 * one the session came through. A session that came by password or with a personal
 * account is offered no such action, and is told how a directory is set. The directory
 * is removed under the Delete grant, which the server refuses while the sector is
 * directory only. The flag is set under the Update grant on the global Sectors record,
 * headquarters'.
 *
 * The directory carries a default role, the role a person is given in the sector at
 * their first sign-in through it: the sector's marshal picks it from the sector's
 * roles, or none, under the Update grant on the row, and with none the directory's
 * people are told the sector admits them by invitation only. The server reloads what
 * every instance knows after each write, so a change decides the next request at once,
 * and each write leaves a change event.
 *
 * Demonstrates: tenancy.declaration, tenancy.directory-only, tenancy.default-seat.
 */
@Component({
  selector: 'app-sector-sign-in',
  imports: [MatButtonModule, MatCardModule, MatSelectModule, MatSlideToggleModule],
  templateUrl: './sector-sign-in.component.html',
  styleUrl: './sector-sign-in.component.scss',
})
export class SectorSignInComponent {
  private api = injectApi();
  private auth = inject(AuthService);
  /**
   * The client's digest cache as a signal. The questions below read it, so they are asked
   * again when a digest loads: a page opened by its address asks before the sector's
   * digest has arrived, and would keep that first "no" otherwise.
   */
  private permissions = storeSignal(this.api.permissions.snapshot);

  /** The sector the URL names, bound from the route. */
  sectorID = input.required<string>();

  /** The picker's choice for a directory whose people are admitted by invitation only. */
  readonly invitationOnly = '';

  /** The sector's row on the global record: its name and its directory-only flag. */
  sector = resource({
    params: () => ({ sector: this.sectorID(), readable: this.canReadSector() }),
    loader: ({ params }): Promise<Sectors | undefined> =>
      params.readable ? this.api.sectors.read([params.sector]) : Promise.resolve(undefined),
  });

  /**
   * The sector's name: the record's where the role reads it, else the name the session's
   * sector list carries, which the sector's own marshal, who cannot read the
   * record, still has. A sector's key is an id nobody reads.
   */
  sectorName = computed(() => {
    this.permissions();
    return this.sector.value()?.name || this.api.permissions.domainName(this.sectorID() as Domain);
  });

  /** The sector's rows of the directories record, listed under the sector where the digest grants List. */
  directories = resource({
    params: () => ({ sector: this.sectorID(), listed: this.canList() }),
    loader: ({ params }): Promise<SectorDirectories[]> =>
      params.listed ? this.api.domain(params.sector as Domain).sectorDirectories.list() : Promise.resolve([]),
    defaultValue: [],
  });

  /** The sector's directory: a sector has one, so the one row the list holds, or none. */
  directory = computed((): SectorDirectories | undefined => this.directories.value()[0]);

  /**
   * The organization's directory the person is signed in through, as the session read
   * reports it: none for a session that came by password or with a personal account.
   */
  sessionDirectory = computed(() => this.auth.sessionInfo().directory);

  /** The sector's roles, the default role's choices, listed where the digest grants List on them. */
  roles = resource({
    params: () => ({ sector: this.sectorID(), listed: this.canListRoles() }),
    loader: ({ params }): Promise<CrewSectorRoles[]> =>
      params.listed ? this.api.domain(params.sector as Domain).crewSectorRoles.list() : Promise.resolve([]),
    defaultValue: [],
  });

  canList = computed(() => this.directoriesAllow(Permissions.List));
  canSeat = computed(() => this.directoriesAllow(Permissions.Update));
  canSet = computed(() => this.directoriesAllow(Permissions.Create));
  canRemove = computed(() => this.directoriesAllow(Permissions.Delete));
  canClose = computed(() => {
    this.permissions();
    return this.api.can(Permissions.Update, Resources.Sectors);
  });
  private canReadSector = computed(() => {
    this.permissions();
    return this.api.sectors.can(Permissions.Read);
  });
  private canListRoles = computed(() => {
    this.permissions();
    return this.api.domain(this.sectorID() as Domain).crewSectorRoles.can(Permissions.List);
  });

  /** Whether the digest grants permission on the sector's directory, asked again when a digest loads. */
  private directoriesAllow(permission: Permission): boolean {
    this.permissions();
    return this.api.domain(this.sectorID() as Domain).sectorDirectories.can(permission);
  }

  /** True while a write is in flight. */
  readonly busy = signal(false);
  /** The server's refusal of the last write, in its words, or empty. */
  readonly error = signal('');

  /**
   * A directory as a sentence names it: what its method reaches, and its connection. A
   * row lists either only where the reader's grants show it, so both may be absent.
   */
  nameOf(directory: { method?: string; connection?: string }): string {
    return `${directoryKinds[directory.method ?? ''] ?? 'Directory'} ${directory.connection ?? ''}`.trim();
  }

  /**
   * Makes the directory the person is signed in through this sector's directory. The
   * request names no directory: the server sets the one the session came through, and
   * refuses a session that came through none, a directory another sector has and a
   * sector that has one already, each in its own words.
   */
  async makeDirectory(): Promise<void> {
    await this.write(async () => {
      await this.api.domain(this.sectorID() as Domain).sectorDirectories.create({});
      this.directories.reload();
    });
  }

  /** What the directory's people are given, as the row says. */
  seatOf(directory: SectorDirectories): string {
    return directory.defaultSeat || 'Invitation only';
  }

  /** Sets the directory's default role: a role of the sector, or none (null clears the column). */
  async seat(directory: SectorDirectories, role: string): Promise<void> {
    await this.write(async () => {
      await this.api
        .domain(this.sectorID() as Domain)
        .sectorDirectories.patch([directory.id], { defaultSeat: role || null });
      this.directories.reload();
    });
  }

  /** Removes the sector's directory, which the server refuses while the sector is directory only. */
  async remove(directory: SectorDirectories): Promise<void> {
    await this.write(async () => {
      await this.api.domain(this.sectorID() as Domain).sectorDirectories.remove([directory.id]);
      this.directories.reload();
    });
  }

  async close(directoryOnly: boolean): Promise<void> {
    await this.write(async () => {
      await this.api.sectors.patch([this.sectorID()], { directoryOnly });
      this.sector.reload();
    });
  }

  private async write(act: () => Promise<void>): Promise<void> {
    this.busy.set(true);
    this.error.set('');
    try {
      await act();
    } catch (failure) {
      this.error.set(refusal(failure));
    } finally {
      this.busy.set(false);
    }
  }
}

/** The sentence a failed write is reported with: the server's own words, which say what to do. */
function refusal(failure: unknown): string {
  return failure instanceof Error ? failure.message : String(failure);
}
