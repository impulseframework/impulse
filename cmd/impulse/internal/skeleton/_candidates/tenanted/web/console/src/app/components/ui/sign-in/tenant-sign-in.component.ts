import { Component, computed, inject, input, resource, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatCardModule } from '@angular/material/card';
import { MatDialog } from '@angular/material/dialog';
import { injectApi } from '@app/api/api';
import { Methods, Permissions } from '@app/service/zz_gen_constants';
import { TenantDirectories, Tenants } from '@app/service/zz_gen_resources';
import { DirectoryOnlyState, directoryOnlyState, Domain, Method, Permission } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { openMethodDialog } from '@impulseframework/resource-angular/resource';
import { storeSignal } from '@impulseframework/resource-angular/resource-client';
import { METHOD_META, MethodPresentation } from '@impulseframework/resource-angular/types';
import { firstValueFrom } from 'rxjs';

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
 * directory only.
 *
 * Everything else the page changes is one of the tenant's own methods, run through the
 * library's method dialog, which draws each from its generated metadata. Two say how the
 * tenant admits its directory's people: OpenTenantToDirectory names the role each of them
 * is given at their first sign-in through the directory, picked in the dialog from the
 * tenant's roles with what each role is for, and MakeTenantInviteOnly clears it, after
 * which a person from the directory enters once they are invited. Two turn directory
 * only: MakeTenantDirectoryOnly and EndTenantDirectoryOnly; the switch is read as it
 * stands under the grant of either, since a tenant's administrator does not read the
 * global Tenants record, which shows the flag to the application's administrator and
 * never writes it.
 *
 * Opening the tenant to its directory and making it directory only each change who
 * enters the tenant for people nobody named, so each warns first: the dialog shows what
 * the server says the call will do, in the server's words, with the people it names as
 * a list of their own, and holds its button until the person has typed the word the
 * method declares. The page asks for the warning before it opens the dialog, so a
 * refusal (a tenant with no directory, a person who is not signed in through the
 * tenant's directory) is shown on the page in the server's words and no dialog opens.
 * Making the tenant invite only and turning directory only off are plain confirmations.
 * The server reloads what every instance knows after each write, so a change decides
 * the next request at once, and each write leaves a change event.
 */
@Component({
  selector: 'app-tenant-sign-in',
  imports: [MatButtonModule, MatCardModule],
  templateUrl: './tenant-sign-in.component.html',
  styleUrl: './tenant-sign-in.component.scss',
})
export class TenantSignInComponent {
  private api = injectApi();
  private auth = inject(AuthService);
  private dialog = inject(MatDialog);
  private metaOf = inject(METHOD_META);
  /**
   * The client's digest cache as a signal. The questions below read it, so they are asked
   * again when a digest loads: a page opened by its address asks before the tenant's
   * digest has arrived, and would keep that first "no" otherwise.
   */
  private permissions = storeSignal(this.api.permissions.snapshot);

  /** The tenant the URL names, bound from the route. */
  tenantID = input.required<string>();

  /** The tenant's own methods, as the client holds them for this tenant. */
  private own = computed(() => this.api.domain(this.tenantID() as Domain));

  /** The tenant's row on the global record, for a person who reads it: its name and its directory-only flag. */
  tenant = resource({
    params: () => ({ tenant: this.tenantID(), readable: this.canReadTenant() }),
    loader: ({ params }): Promise<Tenants | undefined> =>
      params.readable ? this.api.tenants.read([params.tenant]) : Promise.resolve(undefined),
  });

  /**
   * The tenant's directory-only switch as it stands, read in the tenant under the grant
   * of either method that turns it: what the tenant's administrator, who does not read
   * the global record, sees.
   */
  switchState = resource({
    params: () => ({ tenant: this.tenantID(), readable: this.canMakeOnly() || this.canEndOnly() }),
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

  canList = computed(() => this.directoriesAllow(Permissions.List));
  canSet = computed(() => this.directoriesAllow(Permissions.Create));
  canRemove = computed(() => this.directoriesAllow(Permissions.Delete));
  /** Whether the digest grants each of the tenant's own methods, asked again when a digest loads. */
  canOpen = computed(() => this.permissions() && this.own().openTenantToDirectory.can());
  canInviteOnly = computed(() => this.permissions() && this.own().makeTenantInviteOnly.can());
  canMakeOnly = computed(() => this.permissions() && this.own().makeTenantDirectoryOnly.can());
  canEndOnly = computed(() => this.permissions() && this.own().endTenantDirectoryOnly.can());
  private canReadTenant = computed(() => {
    this.permissions();
    return this.api.tenants.can(Permissions.Read);
  });

  /** Whether the digest grants permission on the tenant's directory, asked again when a digest loads. */
  private directoriesAllow(permission: Permission): boolean {
    this.permissions();
    return this.own().tenantDirectories.can(permission);
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
      await this.own().tenantDirectories.create({});
      this.directories.reload();
    });
  }

  /** How the tenant admits its directory's people, as the row says. */
  seatOf(directory: TenantDirectories): string {
    return directory.defaultSeat
      ? `Open to the directory's people: each is given the role ${directory.defaultSeat} at their first sign-in through it.`
      : 'Invite only: a person from the directory enters once they are invited.';
  }

  /** Removes the tenant's directory, which the server refuses while the tenant is directory only. */
  async remove(directory: TenantDirectories): Promise<void> {
    await this.write(async () => {
      await this.own().tenantDirectories.remove([directory.id]);
      this.directories.reload();
    });
  }

  /**
   * Opens the tenant to its directory's people, or changes the role they are given: the
   * role is picked in the dialog, each with what it is for, and the server says what the
   * choice will do before the person types the word and confirms. A tenant with no
   * directory is refused before any dialog opens.
   */
  async open(): Promise<void> {
    await this.run(
      Methods.OpenTenantToDirectory,
      "Open to the directory's people",
      { labels: { role: 'The role each of them gets' }, shows: { role: ['name', 'description'] } },
      () => this.own().openTenantToDirectory.warning({ role: '' }),
    );
  }

  /** Makes the tenant invite only for its directory's people, once the person confirms. */
  async inviteOnly(): Promise<void> {
    await this.run(Methods.MakeTenantInviteOnly, 'Make invite only', {
      confirm:
        'People who sign in through the directory will need an invitation to enter. The people it already seated keep their roles.',
    });
  }

  /**
   * Makes the tenant directory only. The server says what it will do, who will be able
   * to enter and who of the people seated here will not, before the person types the
   * word and confirms; it refuses a tenant that has no directory and a person who is not
   * signed in through the tenant's directory, and its sentence is then shown on the page
   * in place of a dialog.
   */
  async makeOnly(): Promise<void> {
    const name = this.tenantName();
    await this.run(
      Methods.MakeTenantDirectoryOnly,
      name ? `Make ${name} directory only` : 'Make directory only',
      {},
      () => this.own().makeTenantDirectoryOnly.warning({}),
    );
  }

  /** Turns directory only off, once the person confirms: every sign-in is admitted again. */
  async endOnly(): Promise<void> {
    await this.run(Methods.EndTenantDirectoryOnly, 'Turn directory only off', {
      confirm: 'Everyone seated here will be able to enter again, however they sign in.',
    });
  }

  /**
   * Runs one of the tenant's own methods through the library's method dialog, which
   * draws it from the method's metadata and calls it in the tenant. A method that warns
   * is asked for its warning first (`refusedBefore`): the server's refusal is shown on
   * the page in its own words and no dialog opens. Once the method has run the page
   * reads the tenant's declaration again.
   */
  private async run(
    method: Method,
    label: string,
    presentation: Omit<MethodPresentation, 'method'>,
    refusedBefore?: () => Promise<unknown>,
  ): Promise<void> {
    await this.write(async () => {
      if (refusedBefore) {
        await refusedBefore();
      }
      const done = await firstValueFrom(
        openMethodDialog(this.dialog, {
          method,
          meta: this.metaOf(method),
          label,
          presentation: { method, ...presentation },
        }),
      );
      if (done) {
        this.switchState.reload();
        this.tenant.reload();
        this.directories.reload();
      }
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
