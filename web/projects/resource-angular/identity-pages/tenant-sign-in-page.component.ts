import { ChangeDetectionStrategy, Component, computed, inject, resource, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { ActivatedRoute, RouterLink } from '@angular/router';
import {
  AnyResourceHandle,
  CreatePermission,
  DeletePermission,
  Domain,
  ExecutePermission,
  ListPermission,
  Method,
  methodHandle,
  Permission,
  Resource,
  TenantSignInCondition,
  TenantSignInSettings,
  tenantSignInSettings,
} from '@impulseframework/resource';
import { TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { askInDialog } from '@impulseframework/resource-angular/dialogs';
import { filterQueryParams, openMethodDialog } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT, storeSignal } from '@impulseframework/resource-angular/resource-client';
import { pageRouteResolver, resourcePageRoute } from '@impulseframework/resource-angular/resource-nav';
import { METHOD_META, MethodPresentation } from '@impulseframework/resource-angular/types';
import { firstValueFrom } from 'rxjs';
import { TenantSignIn, tenantSignInKey, tenantPeopleLine, tenantStandsLine } from './tenant-sign-in-says';

/** What each of the tenant's methods on the page is, by its name in the route's list. */
type SignInMethod = keyof TenantSignIn['methods'];

/**
 * A tenant's Sign-in settings page: how people enter the tenant, and what the person
 * reading can do about it right now. It says, in one line, how the tenant stands; then the
 * tenant's directory by the name people know it by, the role a person gets the first time
 * they sign in through it in plain words, and whether the tenant is directory only, with
 * how many of its people that keeps out, a count that opens those people on the People page.
 *
 * What it says comes from two reads: the read of the tenant's sign-in settings, asked
 * where the digest grants any of the tenant's own sign-in methods the route names, which
 * says it all in the server's words for the person reading; and the list of the tenant's
 * directories, asked where the digest grants List on it, which says only whether there is
 * one. A tenant has one directory, and nobody types one in: a person who may create it and
 * is signed in through an organization's directory makes that one the tenant's, and the
 * request names none. Everything else the page changes is one of the tenant's own methods, run through
 * the library's method dialog; a method that warns is asked for its warning first, so a
 * refusal is shown on the page in the server's words and no dialog opens. After a write
 * the page reads both again.
 *
 * The route carries the names (tenantSignInRoute); the tenant is the one the page is open
 * in. The page draws nothing until the tenant's digest has loaded and each read it asks
 * has answered, stays drawn while it reads again, and asks again when a digest loads.
 */
@Component({
  selector: 'impulse-tenant-sign-in-page',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [MatButtonModule, RouterLink],
  templateUrl: './tenant-sign-in-page.component.html',
  styleUrl: './tenant-sign-in-page.component.scss',
})
export class TenantSignInPageComponent {
  private readonly client = inject(RESOURCE_CLIENT);
  private readonly tenants = inject(TenantService);
  private readonly auth = inject(AuthService);
  private readonly dialog = inject(MatDialog);
  private readonly metaOf = inject(METHOD_META);
  private readonly resolvePage = pageRouteResolver();
  /** The names the route carries: the tenant word, the directories record, the record of the tenant's people and the tenant's methods. */
  readonly signIn = inject(ActivatedRoute).snapshot.data[tenantSignInKey] as TenantSignIn;
  /**
   * The client's digest cache as a signal. The questions below read it, so they are asked
   * again when a digest loads: a page opened by its address asks before the tenant's
   * digest has arrived, and would keep that first "no" otherwise.
   */
  private readonly permissions = storeSignal(this.client.permissions.snapshot);

  /** The tenant the page is open in. */
  private readonly tenant = computed(() => this.tenants.current() as Domain);

  /** The tenant record in words, lowercase: "this team", "this tenant". */
  readonly word = this.signIn.word;

  /** The tenant's name as the session's tenant list carries it; empty where the list names it not. */
  private readonly knownName = computed(() => {
    this.permissions();
    const tenant = this.tenant();
    const name = tenant ? this.client.permissions.domainName(tenant) : '';
    return name === tenant ? '' : name;
  });

  /** The tenant as a sentence names it: its name, else "this team". */
  private readonly name = computed(() => this.knownName() || `this ${this.word}`);

  readonly canList = computed(() => this.allows(ListPermission, this.signIn.directories));
  readonly canCreate = computed(() => this.allows(CreatePermission, this.signIn.directories));
  readonly canDelete = computed(() => this.allows(DeletePermission, this.signIn.directories));
  readonly canOpen = computed(() => this.grants('openToDirectory'));
  readonly canInviteOnly = computed(() => this.grants('makeDirectoryInviteOnly'));
  readonly canMakeOnly = computed(() => this.grants('makeDirectoryOnly'));
  readonly canEndOnly = computed(() => this.grants('endDirectoryOnly'));

  /**
   * The tenant's sign-in settings as they stand, read under the grant of any of the
   * tenant's own sign-in methods the route names: everything the page says in the server's
   * words for the person reading.
   */
  private readonly read = resource({
    params: () => ({
      tenant: this.tenant(),
      readable: this.canOpen() || this.canInviteOnly() || this.canMakeOnly() || this.canEndOnly(),
    }),
    loader: ({ params }): Promise<TenantSignInSettings | undefined> =>
      params.readable && params.tenant ? tenantSignInSettings(this.client, params.tenant) : Promise.resolve(undefined),
  });

  /** The tenant's rows of the directories record, listed where the digest grants List. */
  private readonly rows = resource({
    params: () => ({ tenant: this.tenant(), listed: this.canList() }),
    loader: ({ params }): Promise<Record<string, unknown>[]> =>
      params.listed && params.tenant ? this.directories(params.tenant).list() : Promise.resolve([]),
    defaultValue: [],
  });

  /** The read of the tenant's sign-in settings, where it has answered. */
  readonly facts = computed((): TenantSignInSettings | undefined =>
    this.read.hasValue() ? this.read.value() : undefined,
  );

  /**
   * The read, where the part headed Directory only is drawn: it has answered and the route
   * names a method of the switch. An application whose tenant has no switch draws no such
   * part.
   */
  readonly switchFacts = computed((): TenantSignInSettings | undefined =>
    this.signIn.methods.makeDirectoryOnly !== undefined || this.signIn.methods.endDirectoryOnly !== undefined
      ? this.facts()
      : undefined,
  );

  /** The tenant's directory: a tenant has one, so the one row the list holds, or none. */
  readonly row = computed((): Record<string, unknown> | undefined =>
    this.rows.hasValue() ? this.rows.value()[0] : undefined,
  );

  /**
   * Whether the page knows what to say: the tenant's digest has loaded and each read the
   * digest grants has answered for the first time. Nothing is drawn before. A read asked
   * again, as after a write, leaves the page drawn, and its answer replaces the old one.
   */
  readonly known = computed(() => {
    this.permissions();
    const tenant = this.tenant();
    return (
      !!tenant &&
      this.client.permissions.digest(tenant) !== undefined &&
      this.read.status() !== 'loading' &&
      this.rows.status() !== 'loading'
    );
  });

  /** The one line on how the tenant stands. */
  readonly standsLine = computed(() =>
    tenantStandsLine({
      word: this.word,
      name: this.name(),
      facts: this.facts(),
      lists: this.canList(),
      row: this.row() !== undefined,
    }),
  );

  /** Whether the reader may know anything here: the read of the sign-in settings or the list. */
  readonly reads = computed(() => this.facts() !== undefined || this.canList());

  /** Whether the tenant has a directory, as far as the reader may know it. */
  readonly hasDirectory = computed(() => {
    const facts = this.facts();
    return facts ? !!facts.directory : this.row() !== undefined;
  });

  /**
   * Whether the part headed Directory is drawn: only where something stands under the
   * heading, the directory's name or the button that removes it, or, with no directory,
   * what setting one takes of a reader who may create it.
   */
  readonly showsDirectory = computed(() =>
    this.hasDirectory()
      ? !!this.facts()?.directory || (this.canDelete() && this.row() !== undefined)
      : this.canCreate(),
  );

  /** The directory's name on a line of its own, opening with a capital; empty where the read names none. */
  readonly directoryLine = computed(() => openingCapital(this.facts()?.directory ?? ''));

  /**
   * The organization's directory the person is signed in through, as a sentence names it;
   * empty for a session that came by password or with a personal account.
   */
  readonly sessionDirectory = computed(() => {
    const facts = this.facts();
    if (facts) {
      return facts.sessionDirectory ?? '';
    }
    return this.auth.sessionInfo()?.directory ? "your organization's directory" : '';
  });

  /** The sentence that says how a directory is set, for a person not signed in through one. */
  readonly howToSet = computed(() => {
    const said = "Sign in through your organization's directory to set it.";
    const signedIn = this.facts()?.signedIn;
    return signedIn ? `${signedIn} ${said}` : said;
  });

  /** Whether the section on the default role is drawn. */
  readonly showsDefaultRole = computed(
    () =>
      this.hasDirectory() &&
      (this.signIn.methods.openToDirectory !== undefined ||
        this.signIn.methods.makeDirectoryInviteOnly !== undefined) &&
      (this.facts() !== undefined || this.canOpen() || this.canInviteOnly()),
  );

  /** The role a person gets the first time they sign in through the directory, in words. */
  readonly defaultRole = computed(() => this.facts()?.defaultRole || 'Nobody: by invitation only');

  /** Whether the button that opens the dialog with the role says the role is changed rather than chosen. */
  readonly changesRole = computed(() => !!this.facts()?.defaultRole);

  /** Whether the button that makes the directory's people need an invitation is offered. */
  readonly offersInviteOnly = computed(() => {
    const facts = this.facts();
    return this.canInviteOnly() && (facts === undefined || !!facts.defaultRole);
  });

  /** The people line, where the read counts the tenant's people. */
  readonly peopleLine = computed(() => {
    const facts = this.facts();
    return facts?.people ? tenantPeopleLine(facts.people, facts.directoryOnly) : '';
  });

  /**
   * The address of the tenant's People page, which the people line opens, or undefined: it
   * is drawn only where the read counts somebody directory only keeps out, the application
   * mounts a page of the tenant's people, the digest grants List on them in the tenant, and
   * the list will filter by the mark the link opens it at: the List names no field (nothing
   * narrows the list) or names the mark as granted or conditional on time. A link is drawn
   * only where it will open what it says.
   */
  readonly peopleLink = computed((): string | undefined => {
    const people = this.signIn.people;
    if (!people || !this.facts()?.people?.outside) {
      return undefined;
    }
    const route = resourcePageRoute(people);
    if (route === undefined || !this.allows(ListPermission, people) || !this.filtersByMark(people)) {
      return undefined;
    }
    return '/' + this.resolvePage(route);
  });

  /** The People page's filter the people line opens it at: the people directory only keeps out. */
  readonly keptOutFilter = filterQueryParams([{ field: 'outsideDirectory', operator: 'eq', value: 'true' }]);

  /** What turning directory only takes of the person reading that they do not meet, in the server's sentences. */
  readonly unmet = computed((): TenantSignInCondition[] =>
    (this.facts()?.conditions ?? []).filter((condition) => !condition.met),
  );

  /** What the sentence beside the button that turns directory only off says of who is kept out. */
  readonly keptOut = computed(
    () =>
      'Everyone else who holds a role here is kept out while this is on, and keeps their roles.' +
      (this.facts()?.passwords
        ? ` An administrator of this ${this.word} who has a password still gets in with it and a code sent to their email.`
        : '') +
      ' The People page says who is kept out.',
  );

  /** True while a write is in flight. */
  readonly busy = signal(false);
  /** The server's refusal of the last write, in its words, or empty. */
  readonly refusal = signal('');

  /**
   * Makes the directory the person is signed in through this tenant's directory. The
   * request names no directory: the server sets the one the session came through.
   */
  async makeDirectory(): Promise<void> {
    await this.write(async () => {
      await this.directories(this.tenant()).create({});
      this.reload();
    });
  }

  /**
   * Removes the tenant's directory, once the person confirms: the dialog names the
   * directory and says what removing it does, and a person who cancels writes nothing.
   */
  async remove(): Promise<void> {
    const row = this.row();
    if (!row) {
      return;
    }
    const directory = this.directoryLine() || 'The directory';
    const confirmed = await askInDialog(this.dialog, {
      title: 'Remove the directory',
      words: `${directory} will no longer be this ${this.word}'s directory. The roles it gave stay in place, and it is free for another ${this.word}.`,
      confirm: 'Remove the directory',
    });
    if (confirmed !== true) {
      return;
    }
    await this.write(async () => {
      const handle = this.directories(this.tenant());
      await handle.remove(handle.keyOf(row));
      this.reload();
    });
  }

  /** Opens the dialog that sets the role, behind the method's warning. */
  async chooseRole(): Promise<void> {
    await this.run(
      'openToDirectory',
      'Choose the role',
      {
        labels: { role: 'The role a person gets the first time they sign in through it' },
        shows: { role: ['name', 'description'] },
      },
      { role: '' },
    );
  }

  /** Makes the directory's people need an invitation, once the person confirms. */
  async inviteOnly(): Promise<void> {
    await this.run('makeDirectoryInviteOnly', 'Nobody: by invitation only', {
      confirm:
        'People who sign in through the directory will need an invitation to enter. The people it already gave a role keep it.',
    });
  }

  /** Makes the tenant directory only, behind the method's warning. */
  async makeOnly(): Promise<void> {
    const name = this.knownName();
    await this.run('makeDirectoryOnly', name ? `Make ${name} directory only` : 'Make directory only', {}, {});
  }

  /** Turns directory only off, once the person confirms. */
  async endOnly(): Promise<void> {
    await this.run('endDirectoryOnly', 'Turn directory only off', {
      confirm: 'Everyone who holds a role here will be able to enter again, however they sign in.',
    });
  }

  /**
   * Runs one of the tenant's methods through the library's method dialog, which draws it
   * from the method's metadata and calls it in the tenant. A method that warns is asked
   * for its warning first, with `warnedWith` as the body: the server's refusal is shown on
   * the page in its own words and no dialog opens. Once the method has run the page reads
   * the switch and the list again.
   */
  private async run(
    kind: SignInMethod,
    label: string,
    presentation: Omit<MethodPresentation, 'method'>,
    warnedWith?: Record<string, unknown>,
  ): Promise<void> {
    const method = this.signIn.methods[kind];
    if (!method) {
      return;
    }
    await this.write(async () => {
      if (warnedWith) {
        await methodHandle(this.client, method, this.tenant()).warning(warnedWith);
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
        this.reload();
      }
    });
  }

  /** Reads the switch and the list again. */
  private reload(): void {
    this.read.reload();
    this.rows.reload();
  }

  private async write(act: () => Promise<void>): Promise<void> {
    this.busy.set(true);
    this.refusal.set('');
    try {
      await act();
    } catch (failure) {
      this.refusal.set(failure instanceof Error ? failure.message : String(failure));
    } finally {
      this.busy.set(false);
    }
  }

  /** A handle on the tenant's directories, bound to the tenant. */
  private directories(tenant: Domain): AnyResourceHandle {
    return this.client.define(this.client.descriptor.resources[this.signIn.directories], tenant);
  }

  /** Whether the digest grants the permission in the tenant, asked again when a digest loads. */
  private allows(permission: Permission, target: Resource | Method): boolean {
    this.permissions();
    const tenant = this.tenant();
    return !!tenant && this.client.can(permission, target, tenant);
  }

  /**
   * Whether the reader's list of the tenant's people filters by the mark of the people
   * directory only keeps out: the digest's List on them names no field at all, or names the
   * mark as granted or conditional on time, the states the server filters by.
   */
  private filtersByMark(people: Resource): boolean {
    const states = this.auth.fieldPermissionStates({
      resource: people,
      permission: ListPermission,
      domain: this.tenant(),
    });
    if (Object.keys(states).length === 0) {
      return true;
    }
    const mark = states['outsideDirectory'];
    return mark === 'granted' || mark === 'conditionalOnTime';
  }

  /** Whether the description names the tenant's method and the digest grants it. */
  private grants(kind: SignInMethod): boolean {
    const method = this.signIn.methods[kind];
    return method !== undefined && this.allows(ExecutePermission, method);
  }
}

/**
 * A name that opens a line, with its first letter in upper case: the server says a
 * directory nobody named as "your organization's Microsoft directory", which inside a
 * sentence stays as it is said.
 */
function openingCapital(name: string): string {
  return name.charAt(0).toUpperCase() + name.slice(1);
}
