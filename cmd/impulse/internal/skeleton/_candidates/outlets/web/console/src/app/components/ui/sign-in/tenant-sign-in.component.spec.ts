import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideNativeDateAdapter } from '@angular/material/core';
import { MatDialog } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { methodMeta } from '@app/service/zz_gen_methods';
import { TenantDirectories } from '@app/service/zz_gen_resources';
import {
  ClientBase,
  DirectoryOnlyCondition,
  Domain,
  MethodWarning,
  PermissionDigest,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { MethodDialogComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { METHOD_META, RESOURCE_DOMAIN, roleInWords } from '@impulseframework/resource-angular/types';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { TenantSignInComponent } from './tenant-sign-in.component';

// The tenant's sign-in page shows who may enter the tenant as its declaration says, and
// lets the holder of each grant change it. A tenant has one directory, which nobody types
// in: the tenant's administrator (List, Create and Delete on TenantDirectories in the
// tenant) who is signed in through an organization's directory is offered to make it the
// tenant's, and the request names no directory; signed in by password they are offered no
// such action and told how a directory is set. They remove the directory. Everything else
// is one of the tenant's own methods, run through the library's method dialog. The
// administrator opens the tenant to its directory's people with a role picked in the
// dialog, behind the server's warning and the word it has them type, and makes it invite
// only again on a plain confirmation; nothing writes the record's role. They make the
// tenant directory only behind the server's warning, its sentence and the people it names
// as a list of their own, and the typed word, and turn it off on a plain confirmation. A
// refusal of either warning is shown on the page in the server's words and no dialog
// opens. The application's administrator, who reads the global record and holds no grant
// on the methods, sees the switch and cannot turn it; a session without the grants sees
// the sentences and nothing is requested.

/** Acme's id: a tenant's key is an id its create generates, and the name is a field. */
const acmeID = 'ac4e0000-0000-4000-8000-000000000001';
const workspace: TenantDirectories = { id: 'd-1', tenantId: acmeID, method: 'google', connection: 'acme.example' };
const seated: TenantDirectories = { ...workspace, defaultSeat: 'TenantAdministrator' };
const tenantRoles = [
  {
    name: 'TenantAdministrator',
    description:
      'Runs one tenant: manages its people, their groups and the roles they hold there, declares the directories they sign in through, and posts its announcements.',
  },
];

/** How the person signed in: through Acme's Workspace, or by password, which reports no directory. */
const throughWorkspace = {
  authenticated: true,
  username: 'someone',
  directory: { method: 'google', connection: 'acme.example' },
};
const byPassword = { authenticated: true, username: 'someone' };

interface Digests {
  global: PermissionDigest;
  acme: PermissionDigest;
}

const administratorGrants: PermissionDigest = {
  TenantDirectories: { List: 'granted', Create: 'granted', Delete: 'granted' },
  StaffTenantDirectoryRoles: { List: 'granted' },
  MakeTenantDirectoryOnly: { Execute: 'granted' },
  EndTenantDirectoryOnly: { Execute: 'granted' },
  OpenTenantToDirectory: { Execute: 'granted' },
  MakeTenantInviteOnly: { Execute: 'granted' },
};
const tenantAdministrator: Digests = { global: {}, acme: administratorGrants };
/** The application's administrator reads the global record and the tenant's directory, and holds no grant on the tenant's method. */
const applicationAdministrator: Digests = {
  global: { Tenants: { Read: 'granted', Update: 'granted' } },
  acme: { TenantDirectories: { List: 'granted' } },
};
/** What the server says making Acme directory only would do: its sentence, the people it names, and the button's words. */
const wouldKeepOut: MethodWarning = {
  consequence:
    'Only people who sign in through acme.example will be able to enter this tenant. 2 people seated here sign in another way and will lose access.',
  names: ['Gale Ferris (gale)', 'hollis'],
  confirm: 'Admit only people who sign in through acme.example',
};
/** What the server says opening Acme to its directory would do, for the role as picked so far. */
const wouldOpen = (role: string): MethodWarning => ({
  consequence: role
    ? `Anyone who signs in through acme.example will be able to enter this tenant without an invitation and gets the role ${roleInWords(role)}.`
    : 'Anyone who signs in through acme.example will be able to enter this tenant without an invitation. Choose the role each of them gets.',
  confirm: 'Open to people who sign in through acme.example',
});

const nobody: Digests = { global: {}, acme: {} };

/**
 * The two things turning directory only takes of the person who turns it, as the server
 * says them for the person viewing the page: each in the sentence its refusal opens
 * with, whether that person meets it, and what they do where they do not.
 */
const throughTheDirectory: DirectoryOnlyCondition = {
  sentence:
    "Directory only is turned on by a person who is signed in through this tenant's directory, Google Workspace acme.example: that shows the directory works, and that you will not shut yourself out.",
  met: true,
};
const hasAPassword: DirectoryOnlyCondition = {
  sentence:
    'Directory only is turned on by a person who has a password: an administrator of this tenant enters it with a password and a code sent to their email, and that is the way back in if the directory has a problem.',
  met: true,
};
const noPassword: DirectoryOnlyCondition = {
  ...hasAPassword,
  met: false,
  toDo: 'Choose a password on Your account first, then try again.',
};

/** What the scripted server holds: the grants, how the person signed in, the tenant's directory, and its answer to a write. */
interface World {
  digests: Digests;
  session?: object;
  directories?: TenantDirectories[];
  write?: TransportResponse;
  /** Whether the tenant is directory only as the page opens. */
  directoryOnly?: boolean;
  /** What the server says turning directory only takes of the person viewing; none unless given. */
  conditions?: DirectoryOnlyCondition[];
  /** The server's answer to a request for a method's warning; the warnings above unless given. */
  warning?: TransportResponse;
}

/** The tenant's own methods, by the last segment of their routes. */
const ownMethods = [
  'make-tenant-directory-only',
  'end-tenant-directory-only',
  'open-tenant-to-directory',
  'make-tenant-invite-only',
];

function serverFor(world: World): (request: TransportRequest) => TransportResponse {
  return (request) => {
    const url = new URL(request.url, 'http://skeleton.test');
    // The tenant's own methods: a request for a method's warning is answered with the
    // server's words and writes nothing, and the call with the answer to a write.
    const own = ownMethods.find((route) => url.pathname === `${environment.apiUrl}/tenants/${acmeID}/${route}`);
    if (own && request.headers?.['X-Warning'] === 'true') {
      const role = (request.body as { role?: string } | undefined)?.role ?? '';
      return (
        world.warning ?? { status: 200, body: own === 'open-tenant-to-directory' ? wouldOpen(role) : wouldKeepOut }
      );
    }
    if (own) {
      return world.write ?? { status: 200, body: undefined };
    }
    switch (url.pathname) {
      case `${environment.apiUrl}/user/session`:
        return { status: 200, body: world.session ?? byPassword };
      case `${environment.apiUrl}/permission-digest`:
        return {
          status: 200,
          body: url.searchParams.get('domain') === acmeID ? world.digests.acme : world.digests.global,
        };
      case `${environment.apiUrl}/user-domains`:
        return { status: 200, body: [{ domain: acmeID, name: 'Acme' }] };
      case `${environment.apiUrl}/features`:
        return { status: 200, body: { enabled: [] } };
      case `${environment.apiUrl}/tenants/${acmeID}`:
        return { status: 200, body: { id: acmeID, name: 'Acme', directoryOnly: world.directoryOnly ?? false } };
      // The switch as it stands is the tenant's own read.
      case `${environment.apiUrl}/tenants/${acmeID}/directory-only`:
        return {
          status: 200,
          body: {
            directoryOnly: world.directoryOnly ?? false,
            ...(world.conditions ? { conditions: world.conditions } : {}),
          },
        };
      case `${environment.apiUrl}/tenants/${acmeID}/tenant-directories`:
        return { status: 200, body: world.directories ?? [] };
      case `${environment.apiUrl}/tenants/${acmeID}/staff-tenant-directory-roles`:
        return { status: 200, body: tenantRoles };
      // Every write rides the consolidated route as one operation.
      case `${environment.apiUrl}/resources`:
        return world.write ?? { status: 200, body: {} };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('TenantSignInComponent', () => {
  let fixture: ComponentFixture<TenantSignInComponent>;
  let transport: ScriptedTransport;

  /** Signs in, loads the tenant list and the global and Acme digests as entering the tenant does, and renders the page for Acme. */
  const pageFor = async (world: World): Promise<void> => {
    transport = scriptedTransport(serverFor(world));
    await TestBed.configureTestingModule({
      imports: [TenantSignInComponent, NoopAnimationsModule],
      providers: [
        provideRouter([]),
        provideNativeDateAdapter(),
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        // The method dialog draws each method from the generated metadata and runs it in
        // the tenant the URL names, as the console's frame provides both.
        { provide: METHOD_META, useValue: methodMeta },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(acmeID as Domain) },
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    const client = TestBed.inject<ClientBase>(RESOURCE_CLIENT);
    await client.permissions.loadDomains();
    await client.permissions.loadDigest();
    await client.permissions.loadDigest(acmeID as Domain);
    await TestBed.inject(ApplicationRef).whenStable();
    fixture = TestBed.createComponent(TenantSignInComponent);
    fixture.componentRef.setInput('tenantID', acmeID);
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();
  };

  const settle = async (): Promise<void> => {
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();
  };
  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const texts = (selector: string): string[] =>
    Array.from(element().querySelectorAll(selector)).map((e) => e.textContent?.replace(/\s+/g, ' ').trim() ?? '');
  /** The operations the page sent, in order: every write is one operation on the consolidated route. */
  const writes = (): unknown[] =>
    transport.requests
      .filter((r) => r.method === 'PATCH' && r.url.replace(/\?.*$/, '') === `${environment.apiUrl}/resources`)
      .flatMap((r) => r.body as unknown[]);
  /** The requests the page and its dialog sent one of the tenant's own methods, in order: `warning` for a request for its warning, `call` for the call, each with its body. */
  const sent = (route: string): { kind: 'warning' | 'call'; body: unknown }[] =>
    transport.requests
      .filter((r) => r.method === 'POST' && r.url.endsWith(`/tenants/${acmeID}/${route}`))
      .map((r) => ({ kind: r.headers?.['X-Warning'] === 'true' ? 'warning' : 'call', body: r.body }));
  const calls = (route: string): unknown[] =>
    sent(route)
      .filter((request) => request.kind === 'call')
      .map((request) => request.body);
  const inDialog = (selector: string): HTMLElement | null =>
    document.querySelector<HTMLElement>(`.cdk-overlay-container ${selector}`);
  const dialogText = (selector: string): string => inDialog(selector)?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
  const dialogTexts = (selector: string): string[] =>
    Array.from(document.querySelectorAll(`.cdk-overlay-container ${selector}`)).map(
      (e) => e.textContent?.replace(/\s+/g, ' ').trim() ?? '',
    );
  /** The server's answer and the dialog's each travel through a timer; a turn of the clock lets them, and the page draws. */
  const turn = async (): Promise<void> => {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await settle();
  };
  /** The dialog's button that runs the method. */
  const runButton = (): HTMLButtonElement => inDialog('button.run') as HTMLButtonElement;
  /** Types the word a method that warns has the person type, as a person does. */
  const typeWord = async (word: string): Promise<void> => {
    const input = inDialog('.typed-word input') as HTMLInputElement;
    input.value = word;
    input.dispatchEvent(new Event('input'));
    await turn();
  };
  const button = (selector: string): HTMLButtonElement => element().querySelector<HTMLButtonElement>(selector)!;

  it("an administrator signed in through a Workspace is offered to make it the tenant's directory, and the request names none", async () => {
    await pageFor({ digests: tenantAdministrator, session: throughWorkspace });

    // The tenant's administrator cannot read the tenant record: the name is the one the
    // session's tenant list carries.
    expect(texts('h1')).toEqual(['Who may enter Acme']);
    expect(texts('.signed-in-through')).toEqual(['You are signed in through Google Workspace acme.example.']);
    expect(texts('.make-directory')).toEqual(["Make acme.example this tenant's directory"]);
    expect(element().querySelector('.how-to-set')).toBeNull();
    // No field takes a method or a connection: a directory is never typed in.
    expect(element().querySelectorAll('input, form').length).toBe(0);

    element().querySelector<HTMLButtonElement>('.make-directory')?.click();
    await settle();
    await settle();
    // The server sets the directory the session came through, so the create carries
    // nothing.
    expect(writes()).toEqual([{ op: 'add', path: `/tenants/${acmeID}/tenant-directories`, value: {} }]);
    expect(texts('.refusal')).toEqual([]);
  });

  it('an administrator signed in with a password is offered no such action, and is told how a directory is set', async () => {
    await pageFor({ digests: tenantAdministrator, session: byPassword });

    expect(element().querySelector('.make-directory')).toBeNull();
    expect(element().querySelector('.signed-in-through')).toBeNull();
    expect(texts('.how-to-set')).toEqual([
      "A directory is set by signing in through it. Sign in with your organization's account (Google Workspace or Microsoft Entra), then come back to this page and make it this tenant's directory.",
    ]);
    expect(element().querySelectorAll('input, form').length).toBe(0);
    expect(writes()).toEqual([]);
  });

  it('a session without the Create grant is offered nothing, whatever it signed in through', async () => {
    await pageFor({
      digests: { global: {}, acme: { TenantDirectories: { List: 'granted' } } },
      session: throughWorkspace,
    });

    expect(element().querySelector('.make-directory')).toBeNull();
    expect(element().querySelector('.how-to-set')).toBeNull();
    expect(texts('.empty')).toContain('This tenant has no directory: its people sign in with a password.');
  });

  it("the server's refusal of the action is shown in its own words", async () => {
    const sentence =
      "Google Workspace acme.example already has a tenant here, and a directory belongs to one tenant. Ask that tenant's administrator to invite you.";
    await pageFor({
      digests: tenantAdministrator,
      session: throughWorkspace,
      write: { status: 409, body: { message: sentence } },
    });

    await fixture.componentInstance.makeDirectory();
    await settle();
    expect(texts('.refusal')).toEqual([sentence]);
  });

  it('a tenant that has a directory shows it, with no offer of another, and its administrator removes it once they confirm', async () => {
    await pageFor({ digests: tenantAdministrator, session: throughWorkspace, directories: [workspace] });

    expect(texts('.directory')).toEqual(['Google Workspace acme.example']);
    // A tenant has one directory: the action is offered only while it has none.
    expect(element().querySelector('.make-directory')).toBeNull();
    // The switch is the tenant's own: its administrator sees it, read in the tenant, and
    // is offered to turn it.
    expect(texts('.directory-only')[0]).toContain('Every sign-in admitted');
    expect(button('.make-directory-only').disabled).toBe(false);

    // Removing the directory asks first, as making the tenant invite only and turning
    // directory only off do: nothing is written until the person confirms.
    button('.remove-directory').click();
    await turn();
    expect(dialogText('[mat-dialog-title]')).toBe('Remove the directory');
    expect(dialogText('.impulse-action-words')).toBe(
      "Google Workspace acme.example will no longer be this tenant's directory. The roles it gave stay in place, and it is free for another tenant.",
    );
    expect(dialogText('.impulse-action-confirm')).toBe('Remove the directory');
    expect(writes()).toEqual([]);

    inDialog('.impulse-action-confirm')?.click();
    await turn();
    await turn();
    expect(writes()).toEqual([{ op: 'remove', path: `/tenants/${acmeID}/tenant-directories/d-1` }]);
  });

  it('a person who cancels the removal of the directory writes nothing, and the directory stays', async () => {
    await pageFor({ digests: tenantAdministrator, session: throughWorkspace, directories: [workspace] });

    button('.remove-directory').click();
    await turn();
    expect(dialogText('[mat-dialog-title]')).toBe('Remove the directory');
    inDialog('.impulse-action-cancel')?.click();
    await turn();
    await turn();
    expect(writes()).toEqual([]);
    expect(texts('.directory')).toEqual(['Google Workspace acme.example']);
  });

  it("the tenant's administrator opens the tenant to its directory's people with a role picked in the dialog, behind the server's warning and the typed word", async () => {
    await pageFor({ digests: tenantAdministrator, directories: [workspace] });

    // A row with no default role is invite only, and offers to open the tenant.
    expect(texts('.seat')).toEqual(['Invite only: a person from the directory enters once they are invited.']);
    expect(texts('.open-to-directory')).toEqual(["Open to the directory's people"]);
    expect(element().querySelector('.make-invite-only')).toBeNull();

    button('.open-to-directory').click();
    await turn();
    await turn();
    // The page asked for the warning before it opened the dialog, and nothing is written.
    expect(sent('open-tenant-to-directory')[0]).toEqual({ kind: 'warning', body: { role: '' } });
    expect(calls('open-tenant-to-directory')).toEqual([]);
    expect(writes()).toEqual([]);
    // The dialog is the method's own: the role is an input, the word is typed, and the
    // button is held.
    expect(dialogText('[mat-dialog-title]')).toBe("Open to the directory's people");
    expect(dialogText('.typed-word mat-label')).toBe('Type open to confirm');
    expect(runButton().disabled).toBe(true);

    // The role is picked: the dialog asks the server what that choice will do and shows
    // its words.
    const dialog = TestBed.inject(MatDialog).openDialogs[0]?.componentInstance as MethodDialogComponent;
    dialog.form.controls['role']?.setValue('TenantAdministrator');
    await new Promise((resolve) => setTimeout(resolve, MethodDialogComponent.rehearsalDelay + 50));
    await turn();
    expect(dialogText('.warning-consequence')).toBe(wouldOpen('TenantAdministrator').consequence);
    expect(runButton().textContent?.trim()).toBe('Open to people who sign in through acme.example');
    // The button is held until the word is typed.
    expect(runButton().disabled).toBe(true);
    await typeWord('open');
    expect(runButton().disabled).toBe(false);

    runButton().click();
    await turn();
    await turn();
    // The tenant's own method names the role: nothing writes the record.
    expect(calls('open-tenant-to-directory')).toEqual([{ role: 'TenantAdministrator' }]);
    expect(writes()).toEqual([]);
    expect(texts('.refusal')).toEqual([]);
  });

  it('a tenant that is open to its directory says the role, and its administrator makes it invite only on a plain confirmation', async () => {
    await pageFor({ digests: tenantAdministrator, directories: [seated] });

    expect(texts('.seat')).toEqual([
      "Open to the directory's people: each is given the role Tenant administrator at their first sign-in through it.",
    ]);
    expect(texts('.open-to-directory')).toEqual(['Change the role']);

    button('.make-invite-only').click();
    await turn();
    // Making it invite only warns of nothing: the server is asked nothing first.
    expect(sent('make-tenant-invite-only')).toEqual([]);
    expect(dialogText('[mat-dialog-title]')).toBe('Make invite only');
    expect(dialogText('.question')).toBe(
      'People who sign in through the directory will need an invitation to enter. The people it already seated keep their roles.',
    );
    expect(inDialog('.typed-word')).toBeNull();

    runButton().click();
    await turn();
    await turn();
    expect(sent('make-tenant-invite-only')).toEqual([{ kind: 'call', body: {} }]);
    expect(writes()).toEqual([]);
  });

  it("the server's refusal to open a tenant is shown in its own words, and no dialog opens", async () => {
    const sentence =
      "This tenant has no directory, so there is nobody to open it to. Set its directory first: sign in through your organization's directory and make it this tenant's directory.";
    await pageFor({
      digests: tenantAdministrator,
      directories: [workspace],
      warning: { status: 409, body: { message: sentence } },
    });

    button('.open-to-directory').click();
    await turn();
    await turn();
    expect(texts('.refusal')).toEqual([sentence]);
    expect(inDialog('[mat-dialog-title]')).toBeNull();
    expect(calls('open-tenant-to-directory')).toEqual([]);
  });

  it('a reader without the methods sees how the tenant admits its directory as a sentence, and no way to change it or to remove it', async () => {
    await pageFor({
      digests: { global: {}, acme: { TenantDirectories: { List: 'granted' } } },
      directories: [seated],
    });

    expect(element().querySelector('.open-to-directory')).toBeNull();
    expect(element().querySelector('.make-invite-only')).toBeNull();
    expect(element().querySelector('.remove-directory')).toBeNull();
    expect(texts('.seat')).toEqual([
      "Open to the directory's people: each is given the role Tenant administrator at their first sign-in through it.",
    ]);
  });

  it("the tenant's administrator makes the tenant directory only behind the server's warning, the people it names as a list, and the typed word", async () => {
    await pageFor({
      digests: tenantAdministrator,
      session: throughWorkspace,
      directories: [workspace],
      conditions: [throughTheDirectory, hasAPassword],
    });

    expect(texts('.directory-only')[0]).toContain('Every sign-in admitted');
    expect(element().querySelector('.needs-directory')).toBeNull();
    // Both things turning it on takes are said before the person tries, in the server's
    // sentences; this person meets both, so nothing more is asked of them.
    expect(texts('.turned-on-by')).toEqual([throughTheDirectory.sentence, hasAPassword.sentence]);
    expect(element().querySelector('.turned-on-by .to-do')).toBeNull();
    expect(texts('.shown-first')).toEqual(['You are shown who it will keep out before it is saved.']);

    button('.make-directory-only').click();
    await turn();
    await turn();
    // The page asked the server first, for this tenant, and has written nothing.
    expect(sent('make-tenant-directory-only').every((request) => request.kind === 'warning')).toBe(true);
    expect(sent('make-tenant-directory-only').length).toBeGreaterThan(0);
    expect(writes()).toEqual([]);
    // The dialog is the server's: its sentence, the people it names as a list of their
    // own, and the words on the button.
    expect(dialogText('[mat-dialog-title]')).toBe('Make Acme directory only');
    expect(dialogText('.warning-consequence')).toBe(wouldKeepOut.consequence);
    expect(dialogTexts('.warning-names li')).toEqual(['Gale Ferris (gale)', 'hollis']);
    expect(runButton().textContent?.trim()).toBe('Admit only people who sign in through acme.example');
    // The button is held until the person has typed the word, a fixed one.
    expect(dialogText('.typed-word mat-label')).toBe('Type close to confirm');
    expect(runButton().disabled).toBe(true);
    await typeWord('close');
    expect(runButton().disabled).toBe(false);

    runButton().click();
    await turn();
    await turn();
    // The tenant's own method turns it: nothing is written to the global record.
    expect(calls('make-tenant-directory-only')).toEqual([{}]);
    expect(writes()).toEqual([]);
    expect(texts('.refusal')).toEqual([]);
  });

  it("a person who does not meet one of the two things is told which and what to do, in the server's sentences, before they press", async () => {
    await pageFor({
      digests: tenantAdministrator,
      session: throughWorkspace,
      directories: [workspace],
      conditions: [throughTheDirectory, noPassword],
    });

    expect(texts('.turned-on-by')).toEqual([throughTheDirectory.sentence, `${noPassword.sentence} ${noPassword.toDo}`]);
    expect(texts('.turned-on-by.unmet')).toEqual([`${noPassword.sentence} ${noPassword.toDo}`]);
    expect(texts('.turned-on-by .to-do')).toEqual([noPassword.toDo]);
    // The button stays: the server refuses the press in the same words.
    expect(button('.make-directory-only').disabled).toBe(false);
  });

  it('the page holds no sentence of its own for what turning directory only takes: where the server names nothing, nothing is shown', async () => {
    await pageFor({ digests: tenantAdministrator, session: throughWorkspace, directories: [workspace] });

    expect(element().querySelector('.turned-on-by')).toBeNull();
    expect(button('.make-directory-only').disabled).toBe(false);
  });

  it('a person who may not list the people is told how many the switch keeps out, and the dialog draws no list', async () => {
    const counted: MethodWarning = { consequence: wouldKeepOut.consequence, confirm: wouldKeepOut.confirm };
    await pageFor({
      digests: tenantAdministrator,
      session: throughWorkspace,
      directories: [workspace],
      warning: { status: 200, body: counted },
    });

    button('.make-directory-only').click();
    await turn();
    await turn();
    expect(dialogText('.warning-consequence')).toBe(wouldKeepOut.consequence);
    expect(inDialog('.warning-names')).toBeNull();
  });

  it("the application's administrator sees the switch as the record shows it and cannot turn it: it is the tenant's own", async () => {
    await pageFor({
      digests: applicationAdministrator,
      session: throughWorkspace,
      directories: [workspace],
      directoryOnly: true,
    });

    expect(texts('.directory-only')[0]).toContain('Directory only: only people who sign in through');
    expect(element().querySelector('.end-directory-only')).toBeNull();
    expect(element().querySelector('.make-directory-only')).toBeNull();
    expect(texts('.declaration-card .empty')).toContain('An administrator of this tenant sets this, in the tenant.');
    // The record showed the flag: the tenant's own read is asked only by a person who may
    // turn the switch.
    expect(
      transport.requests.map((r) => r.url.replace(/\?.*$/, '')).filter((u) => u.endsWith('/directory-only')),
    ).toEqual([]);
  });

  it('a person who cancels the dialog writes nothing, and the switch stays off', async () => {
    await pageFor({ digests: tenantAdministrator, session: throughWorkspace, directories: [workspace] });

    button('.make-directory-only').click();
    await turn();
    await turn();
    (inDialog('mat-dialog-actions button:not(.run)') as HTMLButtonElement).click();
    await turn();
    await turn();
    expect(inDialog('[mat-dialog-title]')).toBeNull();
    expect(writes()).toEqual([]);
    expect(calls('make-tenant-directory-only')).toEqual([]);
    expect(texts('.directory-only')[0]).toContain('Every sign-in admitted');
    expect(button('.make-directory-only').disabled).toBe(false);
  });

  it("the server's refusal to make a tenant directory only is shown in its own words, with no dialog and no write", async () => {
    const sentence =
      "Directory only is turned on by a person who is signed in through this tenant's directory, Google Workspace acme.example: that shows the directory works, and that you will not shut yourself out. You signed in with a password. Sign in through Google Workspace acme.example and try again.";
    await pageFor({
      digests: tenantAdministrator,
      directories: [workspace],
      warning: { status: 409, body: { message: sentence } },
    });

    button('.make-directory-only').click();
    await turn();
    await turn();
    expect(texts('.refusal')).toEqual([sentence]);
    expect(inDialog('[mat-dialog-title]')).toBeNull();
    expect(calls('make-tenant-directory-only')).toEqual([]);
    expect(texts('.directory-only')[0]).toContain('Every sign-in admitted');
  });

  it('a tenant with no directory is told to set one first, and is not offered the button', async () => {
    await pageFor({ digests: tenantAdministrator });

    expect(texts('.needs-directory')).toEqual([
      'Set a directory first: a tenant with no directory cannot be directory only, since nobody could enter it.',
    ]);
    expect(element().querySelector('.turned-on-by')).toBeNull();
    // The button is not offered where a press could only be refused.
    expect(element().querySelector('.make-directory-only')).toBeNull();
  });

  it('turning directory only off is a plain confirmation, and asks the server nothing first', async () => {
    await pageFor({ digests: tenantAdministrator, directories: [workspace], directoryOnly: true });

    expect(texts('.directory-only')[0]).toContain(
      "Directory only: only people who sign in through this tenant's directory enter this tenant.",
    );
    expect(texts('.kept-out')).toEqual([
      'Everyone else seated in this tenant is kept out while this is on, and keeps their roles, but for an administrator of this tenant who has a password: they still get in with it and a code sent to their email. The People page says who is kept out.',
    ]);
    expect(element().querySelector('.make-directory-only')).toBeNull();

    button('.end-directory-only').click();
    await turn();
    expect(sent('end-tenant-directory-only')).toEqual([]);
    expect(dialogText('[mat-dialog-title]')).toBe('Turn directory only off');
    expect(dialogText('.question')).toBe('Everyone seated here will be able to enter again, however they sign in.');
    expect(inDialog('.typed-word')).toBeNull();
    expect(runButton().textContent?.trim()).toBe('Turn directory only off');
    expect(writes()).toEqual([]);

    runButton().click();
    await turn();
    await turn();
    expect(sent('end-tenant-directory-only')).toEqual([{ kind: 'call', body: {} }]);
    expect(writes()).toEqual([]);
  });

  it('a session without the grants sees the sentences, and nothing is requested under the tenant', async () => {
    await pageFor({ digests: nobody, session: throughWorkspace });

    expect(texts('.empty')).toEqual([
      'Your role in this tenant does not cover its directory.',
      'Your role does not cover this setting: an administrator of this tenant sets it.',
    ]);
    expect(element().querySelector('.make-directory')).toBeNull();
    expect(
      transport.requests.map((r) => r.url.replace(/\?.*$/, '')).filter((u) => u.includes(`/tenants/${acmeID}`)),
    ).toEqual([]);
  });

  // Opened by its address (a reload, a bookmark), the page renders before the tenant's
  // digest has loaded. It asks again when the digest arrives, and shows what it grants.
  it('opened before the digests load, it shows the directory once they arrive', async () => {
    transport = scriptedTransport(serverFor({ digests: tenantAdministrator, directories: [workspace] }));
    await TestBed.configureTestingModule({
      imports: [TenantSignInComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    fixture = TestBed.createComponent(TenantSignInComponent);
    fixture.componentRef.setInput('tenantID', acmeID);
    await settle();
    expect(texts('.empty')).toContain('Your role in this tenant does not cover its directory.');

    const client = TestBed.inject<ClientBase>(RESOURCE_CLIENT);
    await client.permissions.loadDigest();
    await client.permissions.loadDigest(acmeID as Domain);
    await settle();
    await settle();

    expect(texts('.empty')).not.toContain('Your role in this tenant does not cover its directory.');
    expect(texts('.directory')).toEqual(['Google Workspace acme.example']);
    expect(element().querySelector('.directory-only')).not.toBeNull();
  });
});
