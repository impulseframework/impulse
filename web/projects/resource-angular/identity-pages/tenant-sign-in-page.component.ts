import { NgTemplateOutlet } from '@angular/common';
import { ChangeDetectionStrategy, Component, computed, inject, resource, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatDialog } from '@angular/material/dialog';
import { ActivatedRoute, RouterLink } from '@angular/router';
import {
  AnyResourceHandle,
  DeletePermission,
  DirectorySecondFactor,
  Domain,
  ExecutePermission,
  ListPermission,
  Method,
  methodHandle,
  Permission,
  ReadPermission,
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
import {
  TenantSignIn,
  tenantSignInKey,
  tenantPeopleLine,
  tenantShareLine,
  tenantStandsLine,
} from './tenant-sign-in-says';

/** What each of the tenant's methods on the page is, by its name in the route's list. */
type SignInMethod = keyof TenantSignIn['methods'];

/**
 * A tenant's Sign-in settings page: how people enter the tenant, and what the person
 * reading can do about it right now. It says, in one line, how the tenant stands; then the
 * tenant's directory by the name people know it by, the role a person gets the first time
 * they sign in through it in plain words, and whether the tenant is directory only, with
 * how many of its people that keeps out, a count that opens those people on the People page.
 * Where the application's directories carry it, the page also says whether the tenant
 * vouches for its directory's own second factor, with the control that says so or takes it
 * back.
 *
 * What it says comes from two reads: the read of the tenant's sign-in settings, asked
 * where the digest grants any of the tenant's own sign-in methods the route names, which
 * says it all in the server's words for the person reading; and the list of the tenant's
 * directories, asked where the digest grants List on it, which says only whether there is
 * one. A reader who holds none of the tenant's sign-in methods is served neither count nor
 * control; where the digest lets them read the tenant record's directory-only field, the
 * page reads the tenant's own row and says whether the tenant is directory only, and no
 * more. A tenant has one directory, and nobody types one in: a person who holds the
 * tenant's method that takes it and is signed in through an organization's directory makes
 * that one the tenant's, behind the statement that they are authorized to act for the
 * organization, and the request names none. That and everything else the page changes but
 * the directory's removal is one of the tenant's own methods, run through the library's
 * method dialog; a method that warns is asked for its warning first, so a refusal is shown
 * on the page in the server's words and no dialog opens.
 * After a write the page reads both again.
 *
 * The route carries the names (tenantSignInRoute); the tenant is the one the page is open
 * in. The page draws nothing until the tenant's digest has loaded and each read it asks
 * has answered, stays drawn while it reads again, and asks again when a digest loads.
 */
@Component({
  selector: 'impulse-tenant-sign-in-page',
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [MatButtonModule, NgTemplateOutlet, RouterLink],
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
  readonly canDelete = computed(() => this.allows(DeletePermission, this.signIn.directories));
  readonly canTake = computed(() => this.grants('takeDirectory'));
  readonly canOpen = computed(() => this.grants('openToDirectory'));
  readonly canInviteOnly = computed(() => this.grants('makeDirectoryInviteOnly'));
  readonly canMakeOnly = computed(() => this.grants('makeDirectoryOnly'));
  readonly canEndOnly = computed(() => this.grants('endDirectoryOnly'));
  readonly canVouch = computed(() => this.grants('vouchForDirectorySecondFactor'));
  readonly canStopVouching = computed(() => this.grants('stopVouchingForDirectorySecondFactor'));

  /** Whether the reader is served the read of the sign-in settings: the digest grants one of the tenant's own sign-in methods the route names. */
  private readonly readsSettings = computed(
    () =>
      this.canTake() ||
      this.canOpen() ||
      this.canInviteOnly() ||
      this.canMakeOnly() ||
      this.canEndOnly() ||
      this.canVouch() ||
      this.canStopVouching(),
  );

  /**
   * The tenant's sign-in settings as they stand, read under the grant of any of the
   * tenant's own sign-in methods the route names: everything the page says in the server's
   * words for the person reading.
   */
  private readonly read = resource({
    params: () => ({ tenant: this.tenant(), readable: this.readsSettings() }),
    loader: ({ params }): Promise<TenantSignInSettings | undefined> =>
      params.readable && params.tenant ? tenantSignInSettings(this.client, params.tenant) : Promise.resolve(undefined),
  });

  /**
   * Whether the tenant's own row of the tenant record is read, for what it says of the
   * switch: the reader is served no read of the sign-in settings, the route names the record
   * and its directory-only field, the digest grants Read on the record, and its Read names
   * no field at all or names that field as granted or conditional on time.
   */
  readonly readsRecord = computed(() => {
    const record = this.signIn.tenantRecord;
    if (this.readsSettings() || !record || !this.allows(ReadPermission, record.resource)) {
      return false;
    }
    const states = this.auth.fieldPermissionStates({ resource: record.resource, permission: ReadPermission });
    if (Object.keys(states).length === 0) {
      return true;
    }
    const state = states[record.directoryOnlyField];
    return state === 'granted' || state === 'conditionalOnTime';
  });

  /** The tenant's own row of the tenant record, read by the tenant's key where readsRecord holds. */
  private readonly recordRow = resource({
    params: () => ({ tenant: this.tenant(), reads: this.readsRecord() }),
    loader: ({ params }): Promise<Record<string, unknown> | undefined> => {
      const record = this.signIn.tenantRecord;
      if (!params.reads || !params.tenant || !record) {
        return Promise.resolve(undefined);
      }
      const handle: AnyResourceHandle = this.client.define(this.client.descriptor.resources[record.resource]);
      return handle.read([params.tenant]) as Promise<Record<string, unknown>>;
    },
  });

  /**
   * What the tenant's own row says of the switch: true or false, or undefined where the row
   * is not read, is refused, or carries no such field.
   */
  readonly recordSays = computed((): boolean | undefined => {
    const field = this.signIn.tenantRecord?.directoryOnlyField;
    if (!field || !this.recordRow.hasValue()) {
      return undefined;
    }
    const value = this.recordRow.value()?.[field];
    return typeof value === 'boolean' ? value : undefined;
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
   * The read, where the part headed Directory only says more than its one sentence: it has
   * answered and the route names a method of the switch. An application whose tenant has no
   * switch draws no such part.
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
      this.rows.status() !== 'loading' &&
      this.recordRow.status() !== 'loading'
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
  readonly reads = computed(() => this.facts() !== undefined || this.canList() || this.recordSays() !== undefined);

  /**
   * Whether the tenant is directory only, as far as the reader may know it: from the read of
   * the sign-in settings where the part is drawn from it (switchFacts), else from the
   * tenant's own row (recordSays); undefined where neither says, and no part is drawn.
   */
  readonly switchOn = computed((): boolean | undefined => this.switchFacts()?.directoryOnly ?? this.recordSays());

  /** Whether the tenant has a directory, as far as the reader may know it. */
  readonly hasDirectory = computed(() => {
    const facts = this.facts();
    return facts ? !!facts.directory : this.row() !== undefined;
  });

  /**
   * Whether the part headed Directory is drawn: only where something stands under the
   * heading, the directory's name or the button that removes it, or, with no directory,
   * what setting one takes of a reader who holds the method that takes it.
   */
  readonly showsDirectory = computed(() =>
    this.hasDirectory() ? !!this.facts()?.directory || (this.canDelete() && this.row() !== undefined) : this.canTake(),
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
   * the list will filter by the mark the link opens it at (the List names no field, so
   * nothing narrows the list, or names the mark as granted or conditional on time), and the
   * page's own people are everyone the read counts as kept out: where another auth's people
   * are among them, each auth's share has a line and a link of its own (shareLines). A link
   * is drawn only where it will open what it says.
   */
  readonly peopleLink = computed((): string | undefined => {
    const people = this.signIn.people;
    if (!people || !this.facts()?.people?.outside || !this.ownShareIsAll()) {
      return undefined;
    }
    return this.keptOutPage(people);
  });

  /**
   * Whether the people line may be the link to the page's own people: the read says no
   * share of its count, or the share of the page's own people is everyone it counts as kept
   * out.
   */
  readonly ownShareIsAll = computed(() => {
    const people = this.facts()?.people;
    if (!people?.shares || people.shares.length === 0) {
      return true;
    }
    const mine = people.shares.find((share) => share.people === this.signIn.people);
    return mine !== undefined && mine.outside === people.outside;
  });

  /**
   * A line for each auth's share of the people directory only keeps out, in the read's
   * order, where the people line is not the whole of the page's own people
   * (ownShareIsAll); none otherwise. A share with nobody kept out has no line. Each line is
   * a link to that auth's people where the people line's link would be for them: the
   * application mounts a page of the share's record, the digest grants List on it in the
   * tenant, and the list will filter by the mark.
   */
  readonly shareLines = computed((): { text: string; link?: string }[] => {
    const people = this.facts()?.people;
    if (!people?.outside || this.ownShareIsAll()) {
      return [];
    }
    return (people.shares ?? [])
      .filter((share) => share.outside > 0)
      .map((share) => {
        const link = this.keptOutPage(share.people as Resource);
        return link ? { text: tenantShareLine(share), link } : { text: tenantShareLine(share) };
      });
  });

  /** The People page's filter the people line opens it at: the people directory only keeps out. */
  readonly keptOutFilter = filterQueryParams([{ field: 'outsideDirectory', operator: 'eq', value: 'true' }]);

  /** What turning directory only takes of the person reading that they do not meet, in the server's sentences. */
  readonly unmet = computed((): TenantSignInCondition[] =>
    (this.facts()?.conditions ?? []).filter((condition) => !condition.met),
  );

  /**
   * The second factor's part of the read: whether the tenant vouches for its directory's
   * own second factor, in the server's words. Absent for a tenant with no directory and
   * where the application's directories carry no word for it, and the page then draws no
   * such part.
   */
  readonly secondFactor = computed((): DirectorySecondFactor | undefined => this.facts()?.secondFactor);

  /** What saying the directory asks for a second factor takes of the person reading that they do not meet, in the server's sentences. */
  readonly unmetToVouch = computed((): TenantSignInCondition[] =>
    (this.secondFactor()?.conditions ?? []).filter((condition) => !condition.met),
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
   * Makes the directory the person is signed in through this tenant's directory, through
   * the tenant's method behind its statement: the dialog shows what the server says taking
   * it does and holds its button until the phrase is typed. The request names no
   * directory: the server takes the one the session came through.
   */
  async makeDirectory(): Promise<void> {
    await this.run('takeDirectory', `Make it this ${this.word}'s directory`, {}, {});
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

  /** Says the tenant's directory asks its people for a second factor of its own, behind the method's warning. */
  async vouch(): Promise<void> {
    await this.run('vouchForDirectorySecondFactor', 'Say the directory asks for a second factor', {}, {});
  }

  /** Takes back the tenant's word for its directory's second factor, once the person confirms. */
  async stopVouching(): Promise<void> {
    await this.run('stopVouchingForDirectorySecondFactor', 'Ask them for a code again', {
      confirm:
        'People who sign in through the directory will be asked for a mailed code again, from their next sign-in.',
    });
  }

  /**
   * Runs one of the tenant's methods through the library's method dialog, which draws it
   * from the method's metadata and calls it in the tenant. A method that warns is asked
   * for its warning first, with `warnedWith` as the body: the server's refusal is shown on
   * the page in its own words and no dialog opens. Once the method has run the page reads
   * the sign-in settings and the list again.
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

  /** Reads the sign-in settings, the list and the tenant's own row again. */
  private reload(): void {
    this.read.reload();
    this.rows.reload();
    this.recordRow.reload();
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

  /**
   * The address of a page of the tenant's people that the kept-out filter opens, or
   * undefined: the application mounts a page of the record, the digest grants List on it in
   * the tenant, and the reader's list of it filters by the mark.
   */
  private keptOutPage(people: Resource): string | undefined {
    const route = resourcePageRoute(people);
    if (route === undefined || !this.allows(ListPermission, people) || !this.filtersByMark(people)) {
      return undefined;
    }
    return '/' + this.resolvePage(route);
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
