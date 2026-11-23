import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideNativeDateAdapter } from '@angular/material/core';
import { MatDialog } from '@angular/material/dialog';
import { provideNoopAnimations } from '@angular/platform-browser/animations';
import { ActivatedRoute, Router } from '@angular/router';
import {
  ApiDescriptor,
  ClientBase,
  createClient,
  DirectoryOnlyCount,
  DirectorySecondFactor,
  Domain,
  Method,
  MethodMeta,
  MethodWarning,
  PermissionDigest,
  PermissionDigestState,
  Resource,
  TenantSignInCondition,
  TenantSignInSettings,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { MethodDialogComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { registerResourcePage } from '@impulseframework/resource-angular/resource-nav';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { METHOD_META, RecordData, RESOURCE_DOMAIN } from '@impulseframework/resource-angular/types';
import { firstValueFrom } from 'rxjs';
import { notNamedFor } from '../auth-frame/frame-testing';
import { TenantSignInPageComponent } from './tenant-sign-in-page.component';
import { TenantSignIn, tenantSignInKey, tenantSignInRoute } from './tenant-sign-in';

// A tenant's Sign-in settings page says how people enter the tenant and lets the holder
// of each grant change it. A tenant has one directory, which nobody types in: a person
// who may create it and is signed in through an organization's directory is offered to
// make that one the tenant's, and the request names none; signed in by password they are
// offered no such action and told how a directory is set. They remove the directory once
// they confirm. Everything else is one of the tenant's own methods, run through the
// library's method dialog: the role a person gets the first time they sign in through the
// directory is picked in the dialog behind the server's warning and the word it has them
// type, and the directory's people are made to need an invitation again on a plain
// confirmation; the tenant is made directory only behind the server's warning and the
// typed word, and the switch is turned off on a plain confirmation. A refusal of a warning
// is shown on the page in the server's words and no dialog opens. A reader with the list
// alone is told whether there is a directory; a reader without the grants is told their
// role does not cover the page, and nothing is requested.

const team = 'team-1' as Domain;
/** The tenant's directory as people know it, which the server's read says. */
const directory = 'Google Workspace riverside.example';
/** The directory's connection, which is never on the page. */
const connection = 'c0nn-7f3a';
const workspace: RecordData = { id: 'd-1', teamId: team, method: 'google', connection };
const roles = [
  { name: 'Reviewer', description: "Reads the team's work." },
  { name: 'Editor', description: "Changes the team's work." },
];

const descriptor: ApiDescriptor = {
  resources: {
    TeamDirectories: {
      resource: 'TeamDirectories' as Resource,
      property: 'teamDirectories',
      route: 'team-directories',
      scope: 'domain',
      consolidated: true,
      keys: ['id'],
      operations: ['list', 'read', 'create', 'remove', 'batch'],
    },
    StaffTeamDirectoryRoles: {
      resource: 'StaffTeamDirectoryRoles' as Resource,
      property: 'staffTeamDirectoryRoles',
      route: 'staff-team-directory-roles',
      scope: 'domain',
      consolidated: false,
      keys: ['name'],
      operations: ['list'],
    },
  },
  methods: {
    OpenTeamToDirectory: {
      method: 'OpenTeamToDirectory' as Method,
      property: 'openTeamToDirectory',
      route: 'open-team-to-directory',
      scope: 'domain',
    },
    MakeTeamInviteOnly: {
      method: 'MakeTeamInviteOnly' as Method,
      property: 'makeTeamInviteOnly',
      route: 'make-team-invite-only',
      scope: 'domain',
    },
    MakeTeamDirectoryOnly: {
      method: 'MakeTeamDirectoryOnly' as Method,
      property: 'makeTeamDirectoryOnly',
      route: 'make-team-directory-only',
      scope: 'domain',
    },
    EndTeamDirectoryOnly: {
      method: 'EndTeamDirectoryOnly' as Method,
      property: 'endTeamDirectoryOnly',
      route: 'end-team-directory-only',
      scope: 'domain',
    },
    VouchForTeamDirectorySecondFactor: {
      method: 'VouchForTeamDirectorySecondFactor' as Method,
      property: 'vouchForTeamDirectorySecondFactor',
      route: 'vouch-for-team-directory-second-factor',
      scope: 'domain',
    },
    StopVouchingForTeamDirectorySecondFactor: {
      method: 'StopVouchingForTeamDirectorySecondFactor' as Method,
      property: 'stopVouchingForTeamDirectorySecondFactor',
      route: 'stop-vouching-for-team-directory-second-factor',
      scope: 'domain',
    },
  },
  domainRoute: { segment: 'teams', param: 'teamID' },
  consolidatedRoute: 'resources',
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  identity: [
    {
      auth: 'Staff',
      label: 'Staff',
      tenant: 'Team',
      tenantResource: 'Teams',
      sides: [
        {
          scope: 'domain',
          lists: { users: 'StaffTeamUsers', directories: 'TeamDirectories' },
          methods: {
            openToDirectory: 'OpenTeamToDirectory',
            makeDirectoryInviteOnly: 'MakeTeamInviteOnly',
            makeDirectoryOnly: 'MakeTeamDirectoryOnly',
            endDirectoryOnly: 'EndTeamDirectoryOnly',
            vouchForDirectorySecondFactor: 'VouchForTeamDirectorySecondFactor',
            stopVouchingForDirectorySecondFactor: 'StopVouchingForTeamDirectorySecondFactor',
          },
        },
      ],
    },
  ],
};

/** The names the page's route carries, as the library places it. */
const signIn = tenantSignInRoute(descriptor, { auth: 'Staff' }).data?.[tenantSignInKey] as TenantSignIn;

/** The tenant's methods as the generator describes them: two warn and have the person type a word. */
const methodMeta: Record<string, MethodMeta> = {
  OpenTeamToDirectory: {
    route: 'open-team-to-directory',
    dryRun: true,
    warning: { typed: 'open' },
    fields: [
      {
        fieldName: 'role',
        displayType: 'enumerated',
        enumeratedResource: 'StaffTeamDirectoryRoles' as Resource,
        required: true,
      },
    ],
  },
  MakeTeamInviteOnly: { route: 'make-team-invite-only', dryRun: true, fields: [] },
  MakeTeamDirectoryOnly: { route: 'make-team-directory-only', dryRun: true, warning: { typed: 'close' }, fields: [] },
  EndTeamDirectoryOnly: { route: 'end-team-directory-only', dryRun: true, fields: [] },
  VouchForTeamDirectorySecondFactor: {
    route: 'vouch-for-team-directory-second-factor',
    dryRun: true,
    warning: { typed: 'vouch' },
    fields: [],
  },
  StopVouchingForTeamDirectorySecondFactor: {
    route: 'stop-vouching-for-team-directory-second-factor',
    dryRun: true,
    fields: [],
  },
};

/** How the person signed in: through the team's Workspace, or by password, which reports no directory. */
const throughWorkspace = { authenticated: true, username: 'someone', directory: { method: 'google', connection } };
const byPassword = { authenticated: true, username: 'someone' };

/** Every grant the page reads, in the team. */
const administrator: PermissionDigest = {
  TeamDirectories: { List: 'granted', Create: 'granted', Delete: 'granted' },
  StaffTeamDirectoryRoles: { List: 'granted' },
  OpenTeamToDirectory: { Execute: 'granted' },
  MakeTeamInviteOnly: { Execute: 'granted' },
  MakeTeamDirectoryOnly: { Execute: 'granted' },
  EndTeamDirectoryOnly: { Execute: 'granted' },
  VouchForTeamDirectorySecondFactor: { Execute: 'granted' },
  StopVouchingForTeamDirectorySecondFactor: { Execute: 'granted' },
};
/** A reader who may list the team's directory and nothing more. */
const lister: PermissionDigest = { TeamDirectories: { List: 'granted' } };

/** What turning directory only takes of the person reading, as the server says it. */
const throughTheDirectory: TenantSignInCondition = {
  sentence:
    "Directory only is turned on by a person who is signed in through this team's directory, Google Workspace riverside.example: that shows the directory works, and that you will not shut yourself out.",
  met: true,
};
const hasAPassword: TenantSignInCondition = {
  sentence:
    'Directory only is turned on by a person who has a password: an administrator of this team enters it with a password and a code sent to their email, and that is the way back in if the directory has a problem.',
  met: true,
};
const notThroughTheDirectory: TenantSignInCondition = {
  ...throughTheDirectory,
  met: false,
  toDo: 'Sign in through Google Workspace riverside.example, then try again.',
};

/** The read of the sign-in settings for a team with no directory, by a person signed in through Workspace. */
const noDirectoryThroughWorkspace: TenantSignInSettings = {
  directoryOnly: false,
  passwords: true,
  signedIn: 'You signed in through Google Workspace riverside.example.',
  sessionDirectory: directory,
};
/** The same, by a person signed in with a password. */
const noDirectoryByPassword: TenantSignInSettings = {
  directoryOnly: false,
  passwords: true,
  signedIn: 'You signed in with a password.',
};
/** The read of the sign-in settings for a team with its directory, invitation only, off, by a person who meets both conditions. */
const withDirectory: TenantSignInSettings = {
  directoryOnly: false,
  directory,
  passwords: true,
  signedIn: 'You signed in through Google Workspace riverside.example.',
  sessionDirectory: directory,
  people: { seated: 4, outside: 2 },
  conditions: [throughTheDirectory, hasAPassword],
};

/** The same, by a person signed in with a password, who is not signed in through the directory. */
const withDirectoryByPassword: TenantSignInSettings = {
  directoryOnly: false,
  directory,
  passwords: true,
  signedIn: 'You signed in with a password.',
  people: { seated: 4, outside: 2 },
  conditions: [notThroughTheDirectory, hasAPassword],
};

/** What saying the team's word for its directory's second factor takes of the person reading, as the server says it. */
const throughTheDirectoryToVouch: TenantSignInCondition = {
  sentence:
    "Only a person who is signed in through this team's directory, Google Workspace riverside.example, can say that it asks its people for a second factor.",
  met: true,
};
/** The second factor's part of the read where the application asks everyone, for a person signed in through the directory. */
const asksEveryone: DirectorySecondFactor = {
  vouched: false,
  asked: true,
  says: 'People who sign in through Google Workspace riverside.example are asked for a code mailed to their address, as everyone is.',
  conditions: [throughTheDirectoryToVouch],
};
/** The same, for a person signed in with a password, who does not meet the condition. */
const asksEveryoneByPassword: DirectorySecondFactor = {
  ...asksEveryone,
  conditions: [
    {
      ...throughTheDirectoryToVouch,
      met: false,
      toDo: 'You signed in with a password. Sign in through Google Workspace riverside.example and try again.',
    },
  ],
};
/** The part where the application asks nobody for a second factor now. */
const asksNobody: DirectorySecondFactor = {
  vouched: false,
  asked: false,
  says: 'This application asks nobody for a second factor now, so there is nothing to turn off for people who sign in through Google Workspace riverside.example.',
};
/** The part where the team says its directory asks for a second factor. */
const vouched: DirectorySecondFactor = {
  vouched: true,
  asked: true,
  says: 'This team says that Google Workspace riverside.example asks its people for a second factor when they sign in, so they are not asked for a code.',
};

/** What the server says the team's word for its directory's second factor would do. */
const wouldVouch: MethodWarning = {
  consequence:
    'You are saying that Google Workspace riverside.example asks its people for a second factor when they sign in. This application will stop asking them for a code.',
  confirm: 'Stop asking people who sign in through Google Workspace riverside.example for a code',
};
/** What the server says making the team directory only would do. */
const wouldKeepOut: MethodWarning = {
  consequence:
    'Only people who sign in through Google Workspace riverside.example will be able to enter this team. 2 people who hold a role here sign in another way and will lose access.',
  names: ['Ada Byrne (ada)', 'quill'],
  confirm: 'Admit only people who sign in through Google Workspace riverside.example',
};
/** What the server says opening the team to its directory would do, for the role as picked so far. */
const wouldOpen = (role: string): MethodWarning => ({
  consequence: role
    ? `Anyone who signs in through Google Workspace riverside.example will be able to enter this team without an invitation and gets the role ${role}.`
    : 'Anyone who signs in through Google Workspace riverside.example will be able to enter this team without an invitation. Choose the role each of them gets.',
  confirm: 'Open to people who sign in through Google Workspace riverside.example',
});

/** What the scripted server holds. */
interface World {
  /** The grants in the team. */
  digest: PermissionDigest;
  session?: object;
  /** The read of the sign-in settings; a team with no directory, off, unless given. */
  state?: TenantSignInSettings;
  directories?: RecordData[];
  /** The server's answer to a write. */
  write?: TransportResponse;
  /** The server's answer to a request for a method's warning; the warnings above unless given. */
  warning?: TransportResponse;
  /** Where given, the server answers a read of the sign-in settings once this settles. */
  held?: Promise<void>;
  /** The names the page's route carries; the library's placing unless given. */
  route?: TenantSignIn;
}

/** The team's own methods, by their routes. */
const ownMethods = [
  'open-team-to-directory',
  'make-team-invite-only',
  'make-team-directory-only',
  'end-team-directory-only',
  'vouch-for-team-directory-second-factor',
  'stop-vouching-for-team-directory-second-factor',
];

function serverFor(world: World): (request: TransportRequest) => TransportResponse | Promise<TransportResponse> {
  return (request) => {
    const url = new URL(request.url, 'http://library.test');
    const own = ownMethods.find((route) => url.pathname === `/api/teams/${team}/${route}`);
    if (own && request.headers?.['X-Warning'] === 'true') {
      const role = (request.body as { role?: string } | undefined)?.role ?? '';
      const warned =
        own === 'open-team-to-directory'
          ? wouldOpen(role)
          : own === 'vouch-for-team-directory-second-factor'
            ? wouldVouch
            : wouldKeepOut;
      return world.warning ?? { status: 200, body: warned };
    }
    if (own) {
      return world.write ?? { status: 200, body: undefined };
    }
    switch (url.pathname) {
      case '/api/user/session':
        return { status: 200, body: world.session ?? byPassword };
      case '/api/permission-digest':
        return { status: 200, body: url.searchParams.get('domain') === team ? world.digest : {} };
      case '/api/user-domains':
        return { status: 200, body: [{ domain: team, name: 'Riverside' }] };
      case `/api/teams/${team}/sign-in-settings`: {
        const read: TransportResponse = { status: 200, body: world.state ?? { directoryOnly: false } };
        return world.held ? world.held.then(() => read) : read;
      }
      case `/api/teams/${team}/team-directories`:
        return { status: 200, body: world.directories ?? [] };
      case `/api/teams/${team}/staff-team-directory-roles`:
        return { status: 200, body: roles };
      case '/api/resources':
        return world.write ?? { status: 200, body: {} };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('TenantSignInPageComponent', () => {
  let fixture: ComponentFixture<TenantSignInPageComponent>;
  let transport: ScriptedTransport;

  const configure = async (world: World): Promise<ClientBase> => {
    transport = scriptedTransport(serverFor(world));
    await TestBed.configureTestingModule({
      imports: [TenantSignInPageComponent],
      providers: [
        provideNoopAnimations(),
        provideNativeDateAdapter(),
        provideResourceTesting({
          transport,
          client: (t) => createClient(descriptor, { baseUrl: '/api', transport: t }),
        }),
        { provide: ActivatedRoute, useValue: { snapshot: { data: { [tenantSignInKey]: world.route ?? signIn } } } },
        { provide: TenantService, useValue: { current: signal<Domain | undefined>(team) } },
        // The method dialog draws each method from its metadata and runs it in the tenant
        // the frame provides.
        { provide: METHOD_META, useValue: (method: string) => methodMeta[method] },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(team) },
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    return TestBed.inject<ClientBase>(RESOURCE_CLIENT);
  };

  /** Signs in, loads the tenant list and the team's digest as entering the tenant does, and renders the page. */
  const pageFor = async (world: World): Promise<void> => {
    const client = await configure(world);
    await client.permissions.loadDomains();
    await client.permissions.loadDigest();
    await client.permissions.loadDigest(team);
    fixture = TestBed.createComponent(TenantSignInPageComponent);
    await settle();
    await settle();
  };

  const settle = async (): Promise<void> => {
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();
  };
  /** The server's answer and the dialog's each travel through a timer; a turn of the clock lets them, and the page draws. */
  const turn = async (): Promise<void> => {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await settle();
  };
  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const said = (e: Element): string => e.textContent?.replace(/\s+/g, ' ').trim() ?? '';
  const texts = (selector: string): string[] => Array.from(element().querySelectorAll(selector)).map(said);
  const button = (name: string): HTMLButtonElement | null =>
    element().querySelector<HTMLButtonElement>(`.impulse-tenant-sign-in-${name}`);
  const inDialog = (selector: string): HTMLElement | null =>
    document.querySelector<HTMLElement>(`.cdk-overlay-container ${selector}`);
  const dialogText = (selector: string): string => {
    const found = inDialog(selector);
    return found ? said(found) : '';
  };
  const dialogTexts = (selector: string): string[] =>
    Array.from(document.querySelectorAll(`.cdk-overlay-container ${selector}`)).map(said);
  const runButton = (): HTMLButtonElement => inDialog('button.run') as HTMLButtonElement;
  /** Types the word a method that warns has the person type, as a person does. */
  const typeWord = async (word: string): Promise<void> => {
    const input = inDialog('.typed-word input') as HTMLInputElement;
    input.value = word;
    input.dispatchEvent(new Event('input'));
    await turn();
  };
  /** The operations the page sent: every write to the directory is one operation on the consolidated route. */
  const writes = (): unknown[] =>
    transport.requests
      .filter((r) => r.method === 'PATCH' && r.url.replace(/\?.*$/, '') === '/api/resources')
      .flatMap((r) => r.body as unknown[]);
  /** The requests sent one of the team's own methods, in order: `warning` for a request for its warning, `call` for the call. */
  const sent = (route: string): { kind: 'warning' | 'call'; body: unknown }[] =>
    transport.requests
      .filter((r) => r.method === 'POST' && r.url.endsWith(`/teams/${team}/${route}`))
      .map((r) => ({ kind: r.headers?.['X-Warning'] === 'true' ? 'warning' : 'call', body: r.body }));
  const calls = (route: string): unknown[] =>
    sent(route)
      .filter((request) => request.kind === 'call')
      .map((request) => request.body);
  /** How many times the page read the sign-in settings. */
  const readsOfTheSettings = (): number =>
    transport.requests.filter((r) => r.url.replace(/\?.*$/, '') === `/api/teams/${team}/sign-in-settings`).length;

  it('is headed Sign-in settings, with a heading for each part, and names its elements for the page', async () => {
    await pageFor({
      digest: administrator,
      session: throughWorkspace,
      state: { ...withDirectory, secondFactor: asksEveryone },
      directories: [workspace],
    });

    expect(texts('h1')).toEqual(['Sign-in settings']);
    expect(texts('h2')).toEqual([
      'Directory',
      'The role a person gets the first time they sign in through it',
      'Second factor',
      'Directory only',
    ]);
    expect(notNamedFor('impulse-tenant-sign-in-', element())).toEqual([]);
  });

  it("a person signed in through an organization's directory is offered to make it the team's directory, and the request names none", async () => {
    await pageFor({ digest: administrator, session: throughWorkspace, state: noDirectoryThroughWorkspace });

    expect(texts('.impulse-tenant-sign-in-stands')).toEqual([
      'People enter Riverside with a password or by invitation. No directory is set.',
    ]);
    expect(texts('.impulse-tenant-sign-in-signed-in')).toEqual([
      'You are signed in through Google Workspace riverside.example.',
    ]);
    expect(texts('.impulse-tenant-sign-in-make-directory')).toEqual(["Make it this team's directory"]);
    expect(element().querySelector('.impulse-tenant-sign-in-how-to-set')).toBeNull();
    // No field takes a method or a connection: a directory is never typed in.
    expect(element().querySelectorAll('input, form').length).toBe(0);
    expect(readsOfTheSettings()).toBe(1);

    button('make-directory')?.click();
    await turn();
    await turn();
    // The server sets the directory the session came through, so the create carries nothing.
    expect(writes()).toEqual([{ op: 'add', path: `/teams/${team}/team-directories`, value: {} }]);
    expect(texts('.impulse-tenant-sign-in-refusal')).toEqual([]);
    // After the write the page reads the sign-in settings again.
    expect(readsOfTheSettings()).toBe(2);
  });

  it('stays drawn while it reads again after a write, and the new answer replaces the old one', async () => {
    const world: World = { digest: administrator, session: throughWorkspace, state: noDirectoryThroughWorkspace };
    await pageFor(world);
    let answer: () => void = () => undefined;
    world.held = new Promise((resolve) => {
      answer = resolve;
    });
    world.state = withDirectory;
    world.directories = [workspace];

    // A read held by the server is a task the page waits on, so the page is not stable
    // until it answers: the clock turns and the page draws without waiting for it.
    const drawn = async (): Promise<void> => {
      await new Promise((resolve) => setTimeout(resolve, 0));
      fixture.detectChanges();
    };
    button('make-directory')?.click();
    await drawn();
    await drawn();
    await drawn();
    // The write succeeded and the sign-in settings are read again; until the server answers, the page says what it said.
    expect(writes()).toEqual([{ op: 'add', path: `/teams/${team}/team-directories`, value: {} }]);
    expect(readsOfTheSettings()).toBe(2);
    expect(texts('.impulse-tenant-sign-in-stands')).toEqual([
      'People enter Riverside with a password or by invitation. No directory is set.',
    ]);
    expect(texts('.impulse-tenant-sign-in-make-directory')).toEqual(["Make it this team's directory"]);

    answer();
    await turn();
    await turn();
    expect(texts('.impulse-tenant-sign-in-stands')).toEqual([
      'People enter Riverside through Google Workspace riverside.example, with a password or by invitation.',
    ]);
    expect(texts('.impulse-tenant-sign-in-directory')).toEqual([directory]);
  });

  it("the server's refusal of a write is shown in its own words", async () => {
    const sentence =
      "Google Workspace riverside.example already has a team here, and a directory belongs to one team. Ask that team's administrator to invite you.";
    await pageFor({
      digest: administrator,
      session: throughWorkspace,
      state: noDirectoryThroughWorkspace,
      write: { status: 409, body: { message: sentence } },
    });

    button('make-directory')?.click();
    await turn();
    await turn();
    expect(texts('.impulse-tenant-sign-in-refusal[role=alert]')).toEqual([sentence]);
  });

  it('a person signed in with a password is offered no way to set a directory, and is told how one is set', async () => {
    await pageFor({ digest: administrator, session: byPassword, state: noDirectoryByPassword });

    expect(button('make-directory')).toBeNull();
    expect(element().querySelector('.impulse-tenant-sign-in-signed-in')).toBeNull();
    expect(texts('.impulse-tenant-sign-in-how-to-set')).toEqual([
      "You signed in with a password. Sign in through your organization's directory to set it.",
    ]);
    // With no directory the switch cannot be turned on, and its button is not offered.
    expect(texts('.impulse-tenant-sign-in-directory-only')).toEqual([
      'Off: a person who holds a role here enters however they signed in.',
    ]);
    expect(texts('.impulse-tenant-sign-in-note')).toContain(
      'Set a directory first: a team with no directory cannot be directory only, since nobody could enter it.',
    );
    expect(button('make-only')).toBeNull();
    expect(writes()).toEqual([]);
  });

  it('a team that has a directory shows it by its name, with no offer of another, and the person removes it once they confirm', async () => {
    await pageFor({ digest: administrator, session: throughWorkspace, state: withDirectory, directories: [workspace] });

    expect(texts('.impulse-tenant-sign-in-stands')).toEqual([
      'People enter Riverside through Google Workspace riverside.example, with a password or by invitation.',
    ]);
    expect(texts('.impulse-tenant-sign-in-directory')).toEqual([directory]);
    expect(button('make-directory')).toBeNull();
    expect(texts('.impulse-tenant-sign-in-remove-directory + .impulse-tenant-sign-in-note')).toEqual([
      'Removing the directory leaves the roles it gave in place and frees it for another team. It cannot be removed while this team is directory only.',
    ]);

    button('remove-directory')?.click();
    await turn();
    expect(dialogText('[mat-dialog-title]')).toBe('Remove the directory');
    expect(dialogText('.impulse-action-words')).toBe(
      "Google Workspace riverside.example will no longer be this team's directory. The roles it gave stay in place, and it is free for another team.",
    );
    expect(dialogText('.impulse-action-confirm')).toBe('Remove the directory');
    expect(writes()).toEqual([]);

    inDialog('.impulse-action-confirm')?.click();
    await turn();
    await turn();
    expect(writes()).toEqual([{ op: 'remove', path: `/teams/${team}/team-directories/d-1` }]);
  });

  it("a directory's name that opens a line starts with a capital, and inside a sentence it is as the server says it", async () => {
    const unnamed = "your organization's Microsoft directory";
    await pageFor({
      digest: administrator,
      session: throughWorkspace,
      state: { ...withDirectory, directory: unnamed },
      directories: [workspace],
    });

    expect(texts('.impulse-tenant-sign-in-stands')).toEqual([
      "People enter Riverside through your organization's Microsoft directory, with a password or by invitation.",
    ]);
    expect(texts('.impulse-tenant-sign-in-directory')).toEqual(["Your organization's Microsoft directory"]);

    button('remove-directory')?.click();
    await turn();
    expect(dialogText('.impulse-action-words')).toBe(
      "Your organization's Microsoft directory will no longer be this team's directory. The roles it gave stay in place, and it is free for another team.",
    );
  });

  it('a person who cancels the removal of the directory writes nothing, and the directory stays', async () => {
    await pageFor({ digest: administrator, session: throughWorkspace, state: withDirectory, directories: [workspace] });

    button('remove-directory')?.click();
    await turn();
    expect(dialogText('[mat-dialog-title]')).toBe('Remove the directory');
    inDialog('.impulse-action-cancel')?.click();
    await turn();
    await turn();
    expect(writes()).toEqual([]);
    expect(texts('.impulse-tenant-sign-in-directory')).toEqual([directory]);
  });

  it("a directory's connection is never on the page, nor in the dialog that removes it, and nothing is drawn as a table", async () => {
    await pageFor({ digest: administrator, session: throughWorkspace, state: withDirectory, directories: [workspace] });

    expect(element().textContent).not.toContain(connection);
    expect(element().querySelector('table')).toBeNull();
    button('remove-directory')?.click();
    await turn();
    expect(dialogText('.impulse-action-words')).not.toBe('');
    expect(document.querySelector('.cdk-overlay-container')?.textContent).not.toContain(connection);
  });

  it("the role a person gets is picked in the method's dialog, behind the server's warning and the typed word", async () => {
    await pageFor({ digest: administrator, state: withDirectory, directories: [workspace] });

    // A directory with no default role admits its people by invitation only, and offers to choose one.
    expect(texts('.impulse-tenant-sign-in-default-role')).toEqual(['Nobody: by invitation only']);
    expect(texts('.impulse-tenant-sign-in-change-role')).toEqual(['Choose a role']);
    expect(button('invite-only')).toBeNull();
    expect(texts('.impulse-tenant-sign-in-change-role ~ .impulse-tenant-sign-in-note')).toEqual([
      'Someone whose role you remove afterwards is not given it back by signing in.',
    ]);

    button('change-role')?.click();
    await turn();
    await turn();
    // The page asked for the warning before it opened the dialog, and nothing is written.
    expect(sent('open-team-to-directory')[0]).toEqual({ kind: 'warning', body: { role: '' } });
    expect(calls('open-team-to-directory')).toEqual([]);
    expect(dialogText('[mat-dialog-title]')).toBe('Choose the role');
    expect(dialogText('.typed-word mat-label')).toBe('Type open to confirm');
    expect(runButton().disabled).toBe(true);
    const dialog = TestBed.inject(MatDialog).openDialogs[0]?.componentInstance as MethodDialogComponent;
    // The role's input says what it sets, and lists each role by its name and description.
    expect(dialog.elements.map((e) => e.label)).toEqual([
      'The role a person gets the first time they sign in through it',
    ]);
    expect(dialog.elements[0]?.enumeratedConfig.listDisplay).toEqual(['name', 'description']);

    // The role is picked: the dialog asks the server what that choice will do and shows its words.
    dialog.form.controls['role']?.setValue('Editor');
    await new Promise((resolve) => setTimeout(resolve, MethodDialogComponent.rehearsalDelay + 50));
    await turn();
    expect(dialogText('.warning-consequence')).toBe(wouldOpen('Editor').consequence);
    expect(runButton().textContent?.trim()).toBe(wouldOpen('Editor').confirm);
    expect(runButton().disabled).toBe(true);
    await typeWord('open');
    expect(runButton().disabled).toBe(false);

    runButton().click();
    await turn();
    await turn();
    expect(calls('open-team-to-directory')).toEqual([{ role: 'Editor' }]);
    expect(writes()).toEqual([]);
    expect(texts('.impulse-tenant-sign-in-refusal')).toEqual([]);
  });

  it('a directory with a default role says it, and its people are made to need an invitation on a plain confirmation', async () => {
    await pageFor({
      digest: administrator,
      state: { ...withDirectory, defaultRole: 'Editor' },
      directories: [workspace],
    });

    expect(texts('.impulse-tenant-sign-in-default-role')).toEqual(['Editor']);
    expect(texts('.impulse-tenant-sign-in-change-role')).toEqual(['Change the role']);

    button('invite-only')?.click();
    await turn();
    // Making it invite only warns of nothing: the server is asked nothing first.
    expect(sent('make-team-invite-only')).toEqual([]);
    expect(dialogText('[mat-dialog-title]')).toBe('Nobody: by invitation only');
    expect(dialogText('.question')).toBe(
      'People who sign in through the directory will need an invitation to enter. The people it already gave a role keep it.',
    );
    expect(inDialog('.typed-word')).toBeNull();

    runButton().click();
    await turn();
    await turn();
    expect(sent('make-team-invite-only')).toEqual([{ kind: 'call', body: {} }]);
    expect(writes()).toEqual([]);
  });

  it("the server's refusal of a warning is shown in its own words, and no dialog opens", async () => {
    const sentence =
      "This team has no directory, so there is nobody to open it to. Set its directory first: sign in through your organization's directory and make it this team's directory.";
    await pageFor({
      digest: administrator,
      state: withDirectory,
      directories: [workspace],
      warning: { status: 409, body: { message: sentence } },
    });

    button('change-role')?.click();
    await turn();
    await turn();
    expect(texts('.impulse-tenant-sign-in-refusal')).toEqual([sentence]);
    expect(inDialog('[mat-dialog-title]')).toBeNull();
    expect(calls('open-team-to-directory')).toEqual([]);
  });

  it("the team is made directory only behind the server's warning, the people it names as a list, and the typed word; the people line says who it would keep out", async () => {
    await pageFor({ digest: administrator, session: throughWorkspace, state: withDirectory, directories: [workspace] });

    expect(texts('.impulse-tenant-sign-in-directory-only')).toEqual([
      'Off: a person who holds a role here enters however they signed in.',
    ]);
    expect(texts('.impulse-tenant-sign-in-people')).toEqual([
      '2 of 4 people here have never signed in through the directory and would be kept out.',
    ]);
    // The person meets both conditions, so none is said and the button is offered.
    expect(element().querySelector('.impulse-tenant-sign-in-condition')).toBeNull();
    expect(texts('.impulse-tenant-sign-in-make-only')).toEqual(['Make this team directory only']);
    expect(texts('.impulse-tenant-sign-in-make-only + .impulse-tenant-sign-in-note')).toEqual([
      'You are shown who it will keep out before it is saved.',
    ]);

    button('make-only')?.click();
    await turn();
    await turn();
    // The page asked the server first, and nothing has run.
    expect(sent('make-team-directory-only')[0]).toEqual({ kind: 'warning', body: {} });
    expect(calls('make-team-directory-only')).toEqual([]);
    expect(dialogText('[mat-dialog-title]')).toBe('Make Riverside directory only');
    expect(dialogText('.warning-consequence')).toBe(wouldKeepOut.consequence);
    expect(dialogTexts('.warning-names li')).toEqual(['Ada Byrne (ada)', 'quill']);
    expect(runButton().textContent?.trim()).toBe(wouldKeepOut.confirm);
    expect(dialogText('.typed-word mat-label')).toBe('Type close to confirm');
    expect(runButton().disabled).toBe(true);
    await typeWord('close');
    expect(runButton().disabled).toBe(false);

    runButton().click();
    await turn();
    await turn();
    expect(calls('make-team-directory-only')).toEqual([{}]);
    expect(writes()).toEqual([]);
    expect(texts('.impulse-tenant-sign-in-refusal')).toEqual([]);
  });

  it("a person signed in with a password is told the condition they do not meet and what to do, in the server's sentences, and offered no button", async () => {
    await pageFor({
      digest: administrator,
      session: byPassword,
      state: withDirectoryByPassword,
      directories: [workspace],
    });

    expect(texts('.impulse-tenant-sign-in-condition')).toEqual([
      `${notThroughTheDirectory.sentence} ${notThroughTheDirectory.toDo}`,
    ]);
    expect(texts('.impulse-tenant-sign-in-condition strong')).toEqual([notThroughTheDirectory.toDo ?? '']);
    expect(button('make-only')).toBeNull();
  });

  it('a person who cancels the dialog writes nothing, and the switch stays off', async () => {
    await pageFor({ digest: administrator, session: throughWorkspace, state: withDirectory, directories: [workspace] });

    button('make-only')?.click();
    await turn();
    await turn();
    (inDialog('mat-dialog-actions button:not(.run)') as HTMLButtonElement).click();
    await turn();
    await turn();
    expect(inDialog('[mat-dialog-title]')).toBeNull();
    expect(calls('make-team-directory-only')).toEqual([]);
    expect(button('make-only')?.disabled).toBe(false);
  });

  it("the server's refusal to make the team directory only is shown in its own words, with no dialog and no call", async () => {
    const sentence =
      "Directory only is turned on by a person who is signed in through this team's directory, Google Workspace riverside.example: that shows the directory works, and that you will not shut yourself out. You signed in with a password. Sign in through Google Workspace riverside.example and try again.";
    await pageFor({
      digest: administrator,
      state: withDirectory,
      directories: [workspace],
      warning: { status: 409, body: { message: sentence } },
    });

    button('make-only')?.click();
    await turn();
    await turn();
    expect(texts('.impulse-tenant-sign-in-refusal')).toEqual([sentence]);
    expect(inDialog('[mat-dialog-title]')).toBeNull();
    expect(calls('make-team-directory-only')).toEqual([]);
  });

  it('directory only is turned off on a plain confirmation; the people line says who it keeps out', async () => {
    await pageFor({
      digest: administrator,
      state: { directoryOnly: true, directory, passwords: true, people: { seated: 4, outside: 1 } },
      directories: [workspace],
    });

    expect(texts('.impulse-tenant-sign-in-stands')).toEqual([
      'People enter Riverside through Google Workspace riverside.example only.',
    ]);
    expect(texts('.impulse-tenant-sign-in-directory-only')).toEqual([
      "On: only people who sign in through this team's directory enter it.",
    ]);
    expect(texts('.impulse-tenant-sign-in-people')).toEqual([
      '1 of 4 people here has never signed in through the directory and is kept out.',
    ]);
    expect(texts('.impulse-tenant-sign-in-end-only + .impulse-tenant-sign-in-note')).toEqual([
      'Everyone else who holds a role here is kept out while this is on, and keeps their roles. An administrator of this team who has a password still gets in with it and a code sent to their email. The People page says who is kept out.',
    ]);
    expect(button('make-only')).toBeNull();

    button('end-only')?.click();
    await turn();
    expect(sent('end-team-directory-only')).toEqual([]);
    expect(dialogText('[mat-dialog-title]')).toBe('Turn directory only off');
    expect(dialogText('.question')).toBe(
      'Everyone who holds a role here will be able to enter again, however they sign in.',
    );
    expect(inDialog('.typed-word')).toBeNull();
    expect(runButton().textContent?.trim()).toBe('Turn directory only off');

    runButton().click();
    await turn();
    await turn();
    expect(sent('end-team-directory-only')).toEqual([{ kind: 'call', body: {} }]);
    expect(writes()).toEqual([]);
  });

  describe("says whether the team vouches for its directory's second factor", () => {
    const line = (): string[] => texts('.impulse-tenant-sign-in-second-factor');
    const headed = (): boolean => texts('h2').includes('Second factor');

    it('not vouched, where the application asks: a holder of the method who meets its condition is offered the button', async () => {
      await pageFor({
        digest: administrator,
        session: throughWorkspace,
        state: { ...withDirectory, secondFactor: asksEveryone },
        directories: [workspace],
      });

      expect(headed()).toBe(true);
      expect(line()).toEqual([asksEveryone.says]);
      expect(texts('.impulse-tenant-sign-in-vouch')).toEqual(['Say the directory asks for a second factor']);
      expect(texts('.impulse-tenant-sign-in-vouch + .impulse-tenant-sign-in-note')).toEqual([
        'You are shown what this does before it is saved.',
      ]);
      expect(element().querySelector('.impulse-tenant-sign-in-condition')).toBeNull();
      expect(button('stop-vouching')).toBeNull();
    });

    it('not vouched, where the application asks: a holder who does not meet the condition is told it and what to do, with no button', async () => {
      await pageFor({
        digest: { VouchForTeamDirectorySecondFactor: { Execute: 'granted' } },
        session: byPassword,
        state: { ...withDirectoryByPassword, secondFactor: asksEveryoneByPassword },
      });

      expect(headed()).toBe(true);
      expect(line()).toEqual([asksEveryoneByPassword.says]);
      const [unmet] = asksEveryoneByPassword.conditions ?? [];
      expect(texts('.impulse-tenant-sign-in-condition')).toEqual([`${unmet?.sentence} ${unmet?.toDo}`]);
      expect(texts('.impulse-tenant-sign-in-condition strong')).toEqual([unmet?.toDo ?? '']);
      expect(button('vouch')).toBeNull();
    });

    it('not vouched, where the application asks: a reader who does not hold the method is told how it stands, and nothing more', async () => {
      await pageFor({
        digest: { MakeTeamDirectoryOnly: { Execute: 'granted' } },
        session: throughWorkspace,
        state: { ...withDirectory, secondFactor: asksEveryone },
      });

      expect(headed()).toBe(true);
      expect(line()).toEqual([asksEveryone.says]);
      expect(button('vouch')).toBeNull();
      expect(element().querySelector('.impulse-tenant-sign-in-condition')).toBeNull();
    });

    it('not vouched, where the application asks nobody: the line says there is nothing to turn off, with no button and no condition', async () => {
      await pageFor({
        digest: administrator,
        session: throughWorkspace,
        state: { ...withDirectory, secondFactor: asksNobody },
        directories: [workspace],
      });

      expect(headed()).toBe(true);
      expect(line()).toEqual([asksNobody.says]);
      expect(button('vouch')).toBeNull();
      expect(button('stop-vouching')).toBeNull();
      expect(element().querySelector('.impulse-tenant-sign-in-condition')).toBeNull();
    });

    it('vouched: a reader who holds only the method that takes it back is offered it', async () => {
      await pageFor({
        digest: { StopVouchingForTeamDirectorySecondFactor: { Execute: 'granted' } },
        session: byPassword,
        state: { ...withDirectoryByPassword, secondFactor: vouched },
      });

      expect(headed()).toBe(true);
      expect(line()).toEqual([vouched.says]);
      expect(texts('.impulse-tenant-sign-in-stop-vouching')).toEqual(['Ask them for a code again']);
      expect(button('vouch')).toBeNull();
    });

    it('vouched: a reader who does not hold the method that takes it back is told how it stands, and nothing more', async () => {
      await pageFor({
        digest: { MakeTeamDirectoryOnly: { Execute: 'granted' } },
        session: throughWorkspace,
        state: { ...withDirectory, secondFactor: vouched },
      });

      expect(headed()).toBe(true);
      expect(line()).toEqual([vouched.says]);
      expect(button('stop-vouching')).toBeNull();
    });

    it('where the read says no second factor part, the page draws no such part', async () => {
      await pageFor({
        digest: administrator,
        session: throughWorkspace,
        state: withDirectory,
        directories: [workspace],
      });

      expect(headed()).toBe(false);
      expect(element().querySelector('.impulse-tenant-sign-in-second-factor')).toBeNull();
      expect(button('vouch')).toBeNull();
      expect(button('stop-vouching')).toBeNull();
    });

    it("the team's word is said behind the server's warning and the typed word, and the page then says it stands", async () => {
      const world: World = {
        digest: administrator,
        session: throughWorkspace,
        state: { ...withDirectory, secondFactor: asksEveryone },
        directories: [workspace],
      };
      await pageFor(world);
      world.state = { ...withDirectory, secondFactor: vouched };

      button('vouch')?.click();
      await turn();
      await turn();
      // The page asked the server first, and nothing has run.
      expect(sent('vouch-for-team-directory-second-factor')[0]).toEqual({ kind: 'warning', body: {} });
      expect(calls('vouch-for-team-directory-second-factor')).toEqual([]);
      expect(dialogText('[mat-dialog-title]')).toBe('Say the directory asks for a second factor');
      expect(dialogText('.warning-consequence')).toBe(wouldVouch.consequence);
      expect(runButton().textContent?.trim()).toBe(wouldVouch.confirm);
      expect(dialogText('.typed-word mat-label')).toBe('Type vouch to confirm');
      expect(runButton().disabled).toBe(true);
      await typeWord('vouch');
      expect(runButton().disabled).toBe(false);
      const reads = readsOfTheSettings();

      runButton().click();
      await turn();
      await turn();
      expect(calls('vouch-for-team-directory-second-factor')).toEqual([{}]);
      expect(readsOfTheSettings()).toBe(reads + 1);
      expect(line()).toEqual([vouched.says]);
      expect(texts('.impulse-tenant-sign-in-stop-vouching')).toEqual(['Ask them for a code again']);
      expect(button('vouch')).toBeNull();
    });

    it("a person who cancels the warning's dialog says nothing", async () => {
      await pageFor({
        digest: administrator,
        session: throughWorkspace,
        state: { ...withDirectory, secondFactor: asksEveryone },
        directories: [workspace],
      });

      button('vouch')?.click();
      await turn();
      await turn();
      (inDialog('mat-dialog-actions button:not(.run)') as HTMLButtonElement).click();
      await turn();
      await turn();
      expect(inDialog('[mat-dialog-title]')).toBeNull();
      expect(calls('vouch-for-team-directory-second-factor')).toEqual([]);
      expect(button('vouch')?.disabled).toBe(false);
    });

    it("the server's refusal of the warning is shown in its own words, with no dialog and no call", async () => {
      const sentence =
        'This application asks nobody for a second factor now, so there is nothing to turn off for people who sign in through Google Workspace riverside.example.';
      await pageFor({
        digest: administrator,
        session: throughWorkspace,
        state: { ...withDirectory, secondFactor: asksEveryone },
        directories: [workspace],
        warning: { status: 409, body: { message: sentence } },
      });

      button('vouch')?.click();
      await turn();
      await turn();
      expect(texts('.impulse-tenant-sign-in-refusal')).toEqual([sentence]);
      expect(inDialog('[mat-dialog-title]')).toBeNull();
      expect(calls('vouch-for-team-directory-second-factor')).toEqual([]);
    });

    it('the word is taken back on a plain confirmation, and the page reads again; a person who cancels takes nothing back', async () => {
      await pageFor({
        digest: administrator,
        session: throughWorkspace,
        state: { ...withDirectory, secondFactor: vouched },
        directories: [workspace],
      });

      button('stop-vouching')?.click();
      await turn();
      expect(sent('stop-vouching-for-team-directory-second-factor')).toEqual([]);
      expect(dialogText('[mat-dialog-title]')).toBe('Ask them for a code again');
      expect(dialogText('.question')).toBe(
        'People who sign in through the directory will be asked for a mailed code again, from their next sign-in.',
      );
      expect(inDialog('.typed-word')).toBeNull();
      (inDialog('mat-dialog-actions button:not(.run)') as HTMLButtonElement).click();
      await turn();
      await turn();
      expect(sent('stop-vouching-for-team-directory-second-factor')).toEqual([]);

      button('stop-vouching')?.click();
      await turn();
      const reads = readsOfTheSettings();
      runButton().click();
      await turn();
      await turn();
      expect(sent('stop-vouching-for-team-directory-second-factor')).toEqual([{ kind: 'call', body: {} }]);
      expect(readsOfTheSettings()).toBe(reads + 1);
    });

    it("nothing of the directory's connection is on the page, nor in the dialog that says the word", async () => {
      await pageFor({
        digest: administrator,
        session: throughWorkspace,
        state: { ...withDirectory, secondFactor: asksEveryone },
        directories: [workspace],
      });

      expect(element().textContent).not.toContain(connection);
      button('vouch')?.click();
      await turn();
      await turn();
      expect(dialogText('.warning-consequence')).not.toBe('');
      expect(document.querySelector('.cdk-overlay-container')?.textContent).not.toContain(connection);
    });

    it("nothing of the directory's connection is in the dialog that takes the word back", async () => {
      await pageFor({
        digest: administrator,
        session: throughWorkspace,
        state: { ...withDirectory, secondFactor: vouched },
        directories: [workspace],
      });

      expect(element().textContent).not.toContain(connection);
      button('stop-vouching')?.click();
      await turn();
      expect(dialogText('.question')).not.toBe('');
      expect(document.querySelector('.cdk-overlay-container')?.textContent).not.toContain(connection);
    });
  });

  describe("links the count of the people directory only keeps out to the team's People page, filtered to them", () => {
    // The application mounts the list of the team's people at its People page; a record of
    // people it mounts no page for is the second.
    const people = 'StaffTeamUsers' as Resource;
    const unmounted = 'StaffTeamGuests' as Resource;
    registerResourcePage(people, 'teams/:teamID/people');
    const listsPeople: PermissionDigest = { ...administrator, StaffTeamUsers: { List: 'granted' } };
    /** The count with one share, the page's own people's, which is the whole of it. */
    const own = (record: Resource, seated: number, outside: number): DirectoryOnlyCount => ({
      seated,
      outside,
      shares: [{ label: 'Staff', people: record, seated, outside }],
    });
    const counted: TenantSignInSettings = { ...withDirectory, people: own(people, 4, 2) };
    const link = (): HTMLAnchorElement | null =>
      element().querySelector<HTMLAnchorElement>('.impulse-tenant-sign-in-people a.impulse-tenant-sign-in-people-link');
    /** The link's path and its filters, as the router reads its address. */
    const opens = (): { path: string; filters: string[] } => {
      const href = link()?.getAttribute('href') ?? '';
      return {
        path: href.replace(/\?.*$/, ''),
        filters: TestBed.inject(Router).parseUrl(href).queryParamMap.getAll('filter'),
      };
    };

    it('with the switch off, the sentence is the link, and the words are the same', async () => {
      await pageFor({ digest: listsPeople, state: counted, directories: [workspace] });

      expect(texts('.impulse-tenant-sign-in-people')).toEqual([
        '2 of 4 people here have never signed in through the directory and would be kept out.',
      ]);
      expect(link() ? said(link() as HTMLAnchorElement) : '').toBe(
        '2 of 4 people here have never signed in through the directory and would be kept out.',
      );
      expect(opens()).toEqual({ path: `/teams/${team}/people`, filters: ['outsideDirectory:eq:true'] });
      // The page's own people are everyone kept out: no share has a line of its own.
      expect(element().querySelector('.impulse-tenant-sign-in-share')).toBeNull();
    });

    it('with the switch on, the sentence is the link, and the words are the same', async () => {
      await pageFor({
        digest: listsPeople,
        state: { directoryOnly: true, directory, passwords: true, people: own(people, 4, 1) },
        directories: [workspace],
      });

      expect(texts('.impulse-tenant-sign-in-people')).toEqual([
        '1 of 4 people here has never signed in through the directory and is kept out.',
      ]);
      expect(link() ? said(link() as HTMLAnchorElement) : '').toBe(
        '1 of 4 people here has never signed in through the directory and is kept out.',
      );
      expect(opens()).toEqual({ path: `/teams/${team}/people`, filters: ['outsideDirectory:eq:true'] });
    });

    it('where nobody is kept out, the sentence links nowhere', async () => {
      await pageFor({
        digest: listsPeople,
        state: { ...withDirectory, people: own(people, 4, 0) },
        directories: [workspace],
      });

      expect(texts('.impulse-tenant-sign-in-people')).toEqual(['Nobody here would be kept out.']);
      expect(link()).toBeNull();
    });

    it('where the application mounts no page of its people, the sentence links nowhere', async () => {
      await pageFor({
        digest: { ...administrator, StaffTeamGuests: { List: 'granted' } },
        state: { ...withDirectory, people: own(unmounted, 4, 2) },
        directories: [workspace],
        route: { ...signIn, people: unmounted },
      });

      expect(texts('.impulse-tenant-sign-in-people')).toEqual([
        '2 of 4 people here have never signed in through the directory and would be kept out.',
      ]);
      expect(link()).toBeNull();
    });

    it('where the reader may not list the people, the sentence links nowhere', async () => {
      await pageFor({ digest: administrator, state: counted, directories: [workspace] });

      expect(texts('.impulse-tenant-sign-in-people')).toEqual([
        '2 of 4 people here have never signed in through the directory and would be kept out.',
      ]);
      expect(link()).toBeNull();
    });

    // The link opens the People page filtered by the mark, so it is drawn only where the
    // list will filter by it: where the reader's List on the people names no field (nothing
    // narrows the list), or names the mark as granted or conditional on time.
    const listing = (fields: Record<string, PermissionDigestState>): PermissionDigest => {
      const digest: PermissionDigest = { ...listsPeople };
      for (const [field, state] of Object.entries(fields)) {
        digest[`${people}.${field}`] = { List: state };
      }
      return digest;
    };
    const sentence = '2 of 4 people here have never signed in through the directory and would be kept out.';

    it('where the List on the people names no field, the sentence is the link', async () => {
      await pageFor({ digest: listing({}), state: counted, directories: [workspace] });

      expect(link() ? said(link() as HTMLAnchorElement) : '').toBe(sentence);
    });

    it('where the List on the people names the mark as granted, the sentence is the link', async () => {
      await pageFor({
        digest: listing({ username: 'granted', outsideDirectory: 'granted' }),
        state: counted,
        directories: [workspace],
      });

      expect(link() ? said(link() as HTMLAnchorElement) : '').toBe(sentence);
      expect(opens()).toEqual({ path: `/teams/${team}/people`, filters: ['outsideDirectory:eq:true'] });
    });

    it('where the List on the people names the mark as conditional on time, the sentence is the link', async () => {
      await pageFor({
        digest: listing({ username: 'granted', outsideDirectory: 'conditionalOnTime' }),
        state: counted,
        directories: [workspace],
      });

      expect(link() ? said(link() as HTMLAnchorElement) : '').toBe(sentence);
    });

    it('where the List on the people names other fields and not the mark, the sentence links nowhere', async () => {
      await pageFor({
        digest: listing({ username: 'granted', cannotEnter: 'granted' }),
        state: counted,
        directories: [workspace],
      });

      expect(texts('.impulse-tenant-sign-in-people')).toEqual([sentence]);
      expect(link()).toBeNull();
    });

    it('where the List on the people names the mark as conditional, the sentence links nowhere', async () => {
      await pageFor({
        digest: listing({ username: 'granted', outsideDirectory: 'conditional' }),
        state: counted,
        directories: [workspace],
      });

      expect(texts('.impulse-tenant-sign-in-people')).toEqual([sentence]);
      expect(link()).toBeNull();
    });

    // Where another auth's people are among those kept out, the sentence opens no page: a
    // link never says more people than it opens. Each auth's share has a line of its own,
    // a link to that auth's people on the same four conditions.
    const members = 'MembersTeamUsers' as Resource;
    registerResourcePage(members, 'teams/:teamID/members');
    const shared = (mine: number, theirs: number, record: Resource = members): TenantSignInSettings => ({
      ...withDirectory,
      people: {
        seated: 12,
        outside: mine + theirs,
        shares: [
          { label: 'Staff', people, seated: 8, outside: mine },
          { label: 'Members', people: record, seated: 4, outside: theirs },
        ],
      },
    });
    const shareLink = (index: number): string =>
      element()
        .querySelectorAll('.impulse-tenant-sign-in-share')
        [index]?.querySelector('a.impulse-tenant-sign-in-share-link')
        ?.getAttribute('href') ?? '';
    const withMembers: PermissionDigest = { ...listsPeople, MembersTeamUsers: { List: 'granted' } };

    it("where another auth's people are among those kept out, the sentence is plain, and each share is a line that opens its own people", async () => {
      await pageFor({ digest: withMembers, state: shared(3, 5), directories: [workspace] });

      expect(texts('.impulse-tenant-sign-in-people')).toEqual([
        '8 of 12 people here have never signed in through the directory and would be kept out.',
      ]);
      expect(link()).toBeNull();
      expect(texts('.impulse-tenant-sign-in-share')).toEqual(['Staff: 3 of them', 'Members: 5 of them']);
      const router = TestBed.inject(Router);
      expect(shareLink(0).replace(/\?.*$/, '')).toBe(`/teams/${team}/people`);
      expect(router.parseUrl(shareLink(0)).queryParamMap.getAll('filter')).toEqual(['outsideDirectory:eq:true']);
      expect(shareLink(1).replace(/\?.*$/, '')).toBe(`/teams/${team}/members`);
      expect(router.parseUrl(shareLink(1)).queryParamMap.getAll('filter')).toEqual(['outsideDirectory:eq:true']);
    });

    it("a share's line is plain where no page of its people is mounted", async () => {
      await pageFor({
        digest: { ...listsPeople, StaffTeamGuests: { List: 'granted' } },
        state: shared(3, 5, unmounted),
        directories: [workspace],
      });

      expect(texts('.impulse-tenant-sign-in-share')).toEqual(['Staff: 3 of them', 'Members: 5 of them']);
      expect(shareLink(0)).not.toBe('');
      expect(shareLink(1)).toBe('');
    });

    it("a share's line is plain where the reader may not list its people", async () => {
      await pageFor({ digest: listsPeople, state: shared(3, 5), directories: [workspace] });

      expect(texts('.impulse-tenant-sign-in-share')).toEqual(['Staff: 3 of them', 'Members: 5 of them']);
      expect(shareLink(0)).not.toBe('');
      expect(shareLink(1)).toBe('');
    });

    it("a share's line is plain where the List on its people names fields and not the mark", async () => {
      await pageFor({
        digest: { ...withMembers, [`${members}.username`]: { List: 'granted' } },
        state: shared(3, 5),
        directories: [workspace],
      });

      expect(texts('.impulse-tenant-sign-in-share')).toEqual(['Staff: 3 of them', 'Members: 5 of them']);
      expect(shareLink(0)).not.toBe('');
      expect(shareLink(1)).toBe('');
    });

    it("where another auth's people are everyone kept out, the sentence is plain and only their share has a line", async () => {
      await pageFor({ digest: withMembers, state: shared(0, 5), directories: [workspace] });

      expect(texts('.impulse-tenant-sign-in-people')).toEqual([
        '5 of 12 people here have never signed in through the directory and would be kept out.',
      ]);
      expect(link()).toBeNull();
      expect(texts('.impulse-tenant-sign-in-share')).toEqual(['Members: 5 of them']);
    });

    it('a count that says no shares is the link, as it always was', async () => {
      await pageFor({ digest: listsPeople, state: withDirectory, directories: [workspace] });

      expect(link() ? said(link() as HTMLAnchorElement) : '').toBe(sentence);
      expect(element().querySelector('.impulse-tenant-sign-in-share')).toBeNull();
    });

    it('both states of the switch draw the same lines', async () => {
      const on = shared(3, 5);
      await pageFor({
        digest: withMembers,
        state: { ...on, directoryOnly: true, conditions: undefined },
        directories: [workspace],
      });

      expect(texts('.impulse-tenant-sign-in-people')).toEqual([
        '8 of 12 people here have never signed in through the directory and are kept out.',
      ]);
      expect(link()).toBeNull();
      expect(texts('.impulse-tenant-sign-in-share')).toEqual(['Staff: 3 of them', 'Members: 5 of them']);
      expect(shareLink(1).replace(/\?.*$/, '')).toBe(`/teams/${team}/members`);
    });
  });

  it('a reader with the list alone is told whether there is a directory, and gets no Directory only part and no button', async () => {
    await pageFor({ digest: lister, session: throughWorkspace, directories: [workspace] });

    expect(texts('.impulse-tenant-sign-in-stands')).toEqual(['This team has a directory.']);
    // Nothing would stand under a heading: the list does not name the directory, and the reader may not remove it.
    expect(texts('h2')).toEqual([]);
    expect(element().querySelectorAll('button').length).toBe(0);
    // The read of the sign-in settings is asked only under the grant of one of the team's own sign-in methods.
    expect(readsOfTheSettings()).toBe(0);
  });

  it('a reader who may not set a directory, of a team with none, gets no Directory heading', async () => {
    await pageFor({
      digest: { MakeTeamDirectoryOnly: { Execute: 'granted' } },
      session: byPassword,
      state: noDirectoryByPassword,
    });

    expect(texts('.impulse-tenant-sign-in-stands')).toEqual([
      'People enter Riverside with a password or by invitation. No directory is set.',
    ]);
    expect(texts('h2')).toEqual(['Directory only']);
  });

  it('a reader who holds only the method that opens the team to its directory is served the read once, and sees the role that stands', async () => {
    await pageFor({
      digest: { OpenTeamToDirectory: { Execute: 'granted' } },
      session: throughWorkspace,
      state: { ...withDirectory, defaultRole: 'Editor' },
    });

    expect(readsOfTheSettings()).toBe(1);
    expect(texts('.impulse-tenant-sign-in-directory')).toEqual([directory]);
    expect(texts('.impulse-tenant-sign-in-default-role')).toEqual(['Editor']);
    expect(texts('.impulse-tenant-sign-in-change-role')).toEqual(['Change the role']);
    // The Directory only part says how the team stands, with no button to turn it.
    expect(texts('.impulse-tenant-sign-in-directory-only')).toEqual([
      'Off: a person who holds a role here enters however they signed in.',
    ]);
    expect(button('make-only')).toBeNull();
    expect(button('end-only')).toBeNull();
  });

  it("a reader who holds none of the team's own sign-in methods is served no read of the sign-in settings", async () => {
    await pageFor({
      digest: {
        TeamDirectories: { List: 'granted', Create: 'granted', Delete: 'granted' },
        StaffTeamDirectoryRoles: { List: 'granted' },
      },
      session: throughWorkspace,
      state: withDirectory,
      directories: [workspace],
    });

    expect(readsOfTheSettings()).toBe(0);
    expect(texts('.impulse-tenant-sign-in-stands')).toEqual(['This team has a directory.']);
    expect(element().querySelector('.impulse-tenant-sign-in-default-role')).toBeNull();
  });

  it('where the route names neither method of the switch, no part is headed Directory only, though the read answers', async () => {
    await pageFor({
      digest: administrator,
      session: throughWorkspace,
      state: withDirectory,
      directories: [workspace],
      route: {
        ...signIn,
        methods: {
          openToDirectory: signIn.methods.openToDirectory,
          makeDirectoryInviteOnly: signIn.methods.makeDirectoryInviteOnly,
        },
      },
    });

    expect(readsOfTheSettings()).toBe(1);
    expect(texts('.impulse-tenant-sign-in-directory')).toEqual([directory]);
    expect(texts('h2')).toEqual(['Directory', 'The role a person gets the first time they sign in through it']);
    expect(element().querySelector('.impulse-tenant-sign-in-directory-only')).toBeNull();
  });

  it('a reader without the grants is told their role does not cover the page, and nothing is requested under the team', async () => {
    await pageFor({ digest: {}, session: throughWorkspace });

    expect(texts('.impulse-tenant-sign-in-stands')).toEqual([
      'Your role in this team does not cover its sign-in settings.',
    ]);
    expect(element().querySelectorAll('h2, button').length).toBe(0);
    expect(transport.requests.map((r) => r.url).filter((u) => u.includes(`/teams/${team}`))).toEqual([]);
  });

  // Opened by its address (a reload, a bookmark), the page renders before the team's digest
  // has loaded. It draws nothing until it has, then asks and shows what the digest grants.
  it('opened before the digests load, it says nothing until they arrive, then shows the directory', async () => {
    const client = await configure({
      digest: administrator,
      session: throughWorkspace,
      state: withDirectory,
      directories: [workspace],
    });
    fixture = TestBed.createComponent(TenantSignInPageComponent);
    await settle();
    expect(texts('h1')).toEqual(['Sign-in settings']);
    expect(element().querySelector('.impulse-tenant-sign-in-stands')).toBeNull();

    await client.permissions.loadDomains();
    await client.permissions.loadDigest(team);
    await settle();
    await settle();

    expect(texts('.impulse-tenant-sign-in-stands')).toEqual([
      'People enter Riverside through Google Workspace riverside.example, with a password or by invitation.',
    ]);
    expect(texts('.impulse-tenant-sign-in-directory')).toEqual([directory]);
  });
});
