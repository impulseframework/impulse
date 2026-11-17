import { Component, computed, inject, input, resource, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatSelectModule } from '@angular/material/select';
import { MatSlideToggleModule } from '@angular/material/slide-toggle';
import { injectApi } from '@app/api/api';
import { Permissions, Resources } from '@app/service/zz_gen_constants';
import { StaffTenantRoles, TenantDirectories, Tenants } from '@app/service/zz_gen_resources';
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
 * The tenant's sign-in page: who may enter the tenant, as its declaration says. The
 * declaration is two things on the Tenant record: the tenant's directory (its row of
 * TenantDirectories: the organization its people sign in through, a sign-in method and
 * the connection its identities carry), and whether its people enter through it only
 * (the record's directory-only flag, which closes passwords).
 *
 * A tenant has one directory and a directory belongs to one tenant, and nobody types
 * one in. It is set from a sign-in: a person who holds the tenant's Create grant on
 * TenantDirectories and is signed in through an organization's directory is offered to
 * make it the tenant's, and the request names no directory, since the server sets the
 * one the session came through. A session that came by password or with a personal
 * account is offered no such action, and is told how a directory is set. The directory
 * is removed under the Delete grant, which the server refuses while the tenant is
 * directory only. The flag is set under the Update grant on the global Tenants record,
 * the application's administrator's.
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

  /** The tenant's row on the global record: its name and its directory-only flag. */
  tenant = resource({
    params: () => ({ tenant: this.tenantID(), readable: this.canReadTenant() }),
    loader: ({ params }): Promise<Tenants | undefined> =>
      params.readable ? this.api.tenants.read([params.tenant]) : Promise.resolve(undefined),
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
    return this.api.can(Permissions.Update, Resources.Tenants);
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

  /** A directory as a sentence names it: what its method reaches, and its connection. */
  nameOf(directory: { method: string; connection: string }): string {
    return `${directoryKinds[directory.method] ?? 'Directory'} ${directory.connection}`;
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

  async close(directoryOnly: boolean): Promise<void> {
    await this.write(async () => {
      await this.api.tenants.patch([this.tenantID()], { directoryOnly });
      this.tenant.reload();
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
