import { Component, computed, inject, input, resource, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatDialog } from '@angular/material/dialog';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { injectApi } from '@app/api/api';
import { Permissions } from '@app/service/zz_gen_constants';
import { StaffTenantRoles, TenantDirectories, Tenants } from '@app/service/zz_gen_resources';
import {
  DirectoryOnlyConsequence,
  directoryOnlyConsequence,
  DirectoryOnlyState,
  directoryOnlyState,
  Domain,
  Permission,
} from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { askInDialog } from '@impulseframework/resource-angular/dialogs';
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
  oidc: 'OpenID Connect provider',
};

/**
 * What the confirmation of directory only says: the server's sentence, then the people
 * it names, each by their name where the account holds one. The words are the server's;
 * the page adds only the list. The server names a person only to someone who may list
 * the tenant's people, so the list can be shorter than the count the sentence gives, or
 * empty: the page then says so, and never passes a part of them off as all of them.
 */
export function consequenceWords(consequence: DirectoryOnlyConsequence): string {
  const people = consequence.people.map((person) =>
    person.name ? `${person.name} (${person.username})` : person.username,
  );
  if (people.length === 0) {
    return consequence.sentence;
  }
  if (people.length < consequence.count) {
    return `${consequence.sentence} Of them, you may list: ${people.join(', ')}.`;
  }
  return `${consequence.sentence} They are: ${people.join(', ')}.`;
}

/**
 * The tenant's sign-in page: who may enter the tenant, as its declaration says. The
 * declaration is two things on the Tenant record: the tenant's directory (its row of
 * TenantDirectories: the organization its people sign in through, a sign-in method and
 * the connection its identities carry), and whether its people enter through it only
 * (the record's directory-only flag, which closes the tenant to every sign-in but one
 * through that directory, and closes nothing else: a person it keeps out still enters
 * every other tenant that seats them).
 *
 * A tenant has one directory and a directory belongs to one tenant, and nobody types
 * one in. It is set from a sign-in: a person who holds the tenant's Create grant on
 * TenantDirectories and is signed in through an organization's directory is offered to
 * make it the tenant's, and the request names no directory, since the server sets the
 * one the session came through. A session that came by password or with a personal
 * account is offered no such action, and is told how a directory is set. The directory
 * is removed under the Delete grant, which the server refuses while the tenant is
 * directory only. The switch is the tenant's own: the tenant's administrator turns it,
 * with the method SetTenantDirectoryOnly in the tenant's scope, and reads it as it
 * stands under the same grant, since a tenant's administrator does not read the global
 * Tenants record, which shows the flag to the application's administrator and never
 * writes it. The server allows turning it on only once the tenant has its directory, and
 * only to a person who is signed in through that directory themselves; before the page
 * turns it on, it asks the server what it would do and shows the server's sentence and
 * the people it names for the person to confirm. Turning it off is a plain confirmation.
 *
 * The directory carries a default role, the role a person is given in the tenant at
 * their first sign-in through it: the tenant's administrator picks it from the tenant's
 * roles, or none, under the Update grant on the row, and with none the directory's
 * people are told the tenant admits them by invitation only. The server reloads what
 * every instance knows after each write, so a change decides the next request at once,
 * and each write leaves a change event.
 */
@Component({
  selector: 'app-tenant-sign-in',
  imports: [MatButtonModule, MatCardModule, MatSelectModule, MatSlideToggleModule],
  templateUrl: './tenant-sign-in.component.html',
  styleUrl: './tenant-sign-in.component.scss',
})
export class TenantSignInComponent {
  private api = injectApi();
  private auth = inject(AuthService);
  private dialog = inject(MatDialog);
  /**
   * The client's digest cache as a signal. The questions below read it, so they are asked
   * again when a digest loads: a page opened by its address asks before the tenant's
   * digest has arrived, and would keep that first "no" otherwise.
   */
  private permissions = storeSignal(this.api.permissions.snapshot);

  /** The tenant the URL names, bound from the route. */
  tenantID = input.required<string>();

  /** The picker's choice for a directory whose people are admitted by invitation only. */
  readonly invitationOnly = '';

  /** The tenant's row on the global record, for a person who reads it: its name and its directory-only flag. */
  tenant = resource({
    params: () => ({ tenant: this.tenantID(), readable: this.canReadTenant() }),
    loader: ({ params }): Promise<Tenants | undefined> =>
      params.readable ? this.api.tenants.read([params.tenant]) : Promise.resolve(undefined),
  });

  /**
   * The tenant's directory-only switch as it stands, read in the tenant under the grant
   * of the method that turns it: what the tenant's administrator, who does not read the
   * global record, sees.
   */
  switchState = resource({
    params: () => ({ tenant: this.tenantID(), readable: this.canClose() }),
    loader: ({ params }): Promise<DirectoryOnlyState | undefined> =>
      params.readable ? directoryOnlyState(this.api, params.tenant as Domain) : Promise.resolve(undefined),
  });

  /**
   * Whether the tenant is directory only, as far as this person may know: the switch
   * read in the tenant, else the flag on the record, else unknown.
   */
  directoryOnly = computed((): boolean | undefined => {
    const state = this.switchState.value();
    return state ? state.directoryOnly : this.tenant.value()?.directoryOnly;
  });

  /**
   * The tenant's name: the record's where the role reads it, else the name the session's
   * tenant list carries, which the tenant's own administrator, who cannot read the
   * record, still has. A tenant's key is an id nobody reads.
   */
  tenantName = computed(() => {
    this.permissions();
    return this.tenant.value()?.name || this.api.permissions.domainName(this.tenantID() as Domain);
  });

  /** The tenant's rows of the directories record, listed under the tenant where the digest grants List. */
  directories = resource({
    params: () => ({ tenant: this.tenantID(), listed: this.canList() }),
    loader: ({ params }): Promise<TenantDirectories[]> =>
      params.listed ? this.api.domain(params.tenant as Domain).tenantDirectories.list() : Promise.resolve([]),
    defaultValue: [],
  });

  /** The tenant's directory: a tenant has one, so the one row the list holds, or none. */
  directory = computed((): TenantDirectories | undefined => this.directories.value()[0]);

  /**
   * The organization's directory the person is signed in through, as the session read
   * reports it: none for a session that came by password or with a personal account.
   */
  sessionDirectory = computed(() => this.auth.sessionInfo().directory);

  /** The tenant's roles, the default role's choices, listed where the digest grants List on them. */
  roles = resource({
    params: () => ({ tenant: this.tenantID(), listed: this.canListRoles() }),
    loader: ({ params }): Promise<StaffTenantRoles[]> =>
      params.listed ? this.api.domain(params.tenant as Domain).staffTenantRoles.list() : Promise.resolve([]),
    defaultValue: [],
  });

  canList = computed(() => this.directoriesAllow(Permissions.List));
  canSeat = computed(() => this.directoriesAllow(Permissions.Update));
  canSet = computed(() => this.directoriesAllow(Permissions.Create));
  canRemove = computed(() => this.directoriesAllow(Permissions.Delete));
  canClose = computed(() => {
    this.permissions();
    return this.api.domain(this.tenantID() as Domain).setTenantDirectoryOnly.can();
  });
  private canReadTenant = computed(() => {
    this.permissions();
    return this.api.tenants.can(Permissions.Read);
  });
  private canListRoles = computed(() => {
    this.permissions();
    return this.api.domain(this.tenantID() as Domain).staffTenantRoles.can(Permissions.List);
  });

  /** Whether the digest grants permission on the tenant's directory, asked again when a digest loads. */
  private directoriesAllow(permission: Permission): boolean {
    this.permissions();
    return this.api.domain(this.tenantID() as Domain).tenantDirectories.can(permission);
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
   * Makes the directory the person is signed in through this tenant's directory. The
   * request names no directory: the server sets the one the session came through, and
   * refuses a session that came through none, a directory another tenant has and a
   * tenant that has one already, each in its own words.
   */
  async makeDirectory(): Promise<void> {
    await this.write(async () => {
      await this.api.domain(this.tenantID() as Domain).tenantDirectories.create({});
      this.directories.reload();
    });
  }

  /** What the directory's people are given, as the row says. */
  seatOf(directory: TenantDirectories): string {
    return directory.defaultSeat || 'Invitation only';
  }

  /** Sets the directory's default role: a role of the tenant, or none (null clears the column). */
  async seat(directory: TenantDirectories, role: string): Promise<void> {
    await this.write(async () => {
      await this.api
        .domain(this.tenantID() as Domain)
        .tenantDirectories.patch([directory.id], { defaultSeat: role || null });
      this.directories.reload();
    });
  }

  /** Removes the tenant's directory, which the server refuses while the tenant is directory only. */
  async remove(directory: TenantDirectories): Promise<void> {
    await this.write(async () => {
      await this.api.domain(this.tenantID() as Domain).tenantDirectories.remove([directory.id]);
      this.directories.reload();
    });
  }

  /**
   * Turns directory only on or off, once the person confirms, with the tenant's own
   * method. Turning it on asks the server first what it would do, and the confirmation is
   * the server's: its sentence, the people it names, and the words on the button. The
   * server refuses a tenant that has no directory and a person who is not signed in
   * through the tenant's directory, and its sentence is shown in place of a
   * confirmation. Nothing is written until the person confirms.
   */
  async close(directoryOnly: boolean): Promise<void> {
    await this.write(async () => {
      const confirmed = directoryOnly ? await this.confirmOnly() : await this.confirmEverySignIn();
      if (!confirmed) {
        return;
      }
      await this.api.domain(this.tenantID() as Domain).setTenantDirectoryOnly.execute({ directoryOnly });
      this.switchState.reload();
      this.tenant.reload();
    });
  }

  /** Asks the server what directory only would do to this tenant, and the person whether to do it. */
  private async confirmOnly(): Promise<boolean> {
    const consequence = await directoryOnlyConsequence(this.api, this.tenantID() as Domain);
    const name = this.tenantName();
    const answer = await askInDialog(this.dialog, {
      title: name ? `Make ${name} directory only?` : 'Turn directory only on?',
      words: consequenceWords(consequence),
      confirm: consequence.confirm,
    });
    return answer === true;
  }

  /** Asks the person whether this tenant should admit every sign-in again. */
  private async confirmEverySignIn(): Promise<boolean> {
    const name = this.tenantName();
    const answer = await askInDialog(this.dialog, {
      title: name ? `Turn directory only off for ${name}?` : 'Turn directory only off?',
      words: 'Everyone seated here will be able to enter again, however they sign in.',
      confirm: 'Turn directory only off',
    });
    return answer === true;
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
