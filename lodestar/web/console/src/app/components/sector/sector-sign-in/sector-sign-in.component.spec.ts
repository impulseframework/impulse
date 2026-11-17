import { ApplicationRef, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideNativeDateAdapter } from '@angular/material/core';
import { MatDialog } from '@angular/material/dialog';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { methodMeta } from '@app/service/zz_gen_methods';
import { SectorDirectories } from '@app/service/zz_gen_resources';
import {
  ClientBase,
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
import { METHOD_META, RESOURCE_DOMAIN } from '@impulseframework/resource-angular/types';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { SectorSignInComponent } from './sector-sign-in.component';

// Demonstrates: tenancy.declaration, tenancy.directory-only, tenancy.default-seat.
//
// The sector's sign-in page shows who may enter the sector as its declaration says, and
// lets the holder of each grant change it. A sector has one directory, which nobody
// types in: the sector's marshal (List, Create and Delete on SectorDirectories in the
// sector) who is signed in through an organization's directory is offered to make it
// the sector's, and the request names no directory; signed in by password they are
// offered no such action and told how a directory is set. They remove the directory.
// Everything else is one of the sector's own methods, run through the library's method
// dialog. The marshal opens the sector to its directory's people with a role picked in
// the dialog, behind the server's warning and the word it has them type, and makes it
// invite only again on a plain confirmation; nothing writes the record's role. They
// make the sector directory only behind the server's warning, its sentence and the
// people it names as a list of their own, and the typed word, and turn it off on a
// plain confirmation. A refusal of either warning is shown on the page in the server's
// words and no dialog opens. Headquarters, who reads the global record and holds no
// grant on the methods, sees the switch and cannot turn it; a session without the
// grants sees the sentences and nothing is requested.

/** Anvil's id: a sector's key is an id its create generates, and the name is a field. */
const anvilID = 'a1111111-1111-4111-8111-111111111111';
const workspace: SectorDirectories = { id: 'd-1', sectorId: anvilID, method: 'google', connection: 'example' };
const seated: SectorDirectories = { ...workspace, defaultSeat: 'SectorCrew' };
const sectorRoles = [
  { name: 'SectorCrew', description: 'Sees the wings and hangars of a sector.' },
  { name: 'Pilot', description: 'Flies the missions of a sector.' },
];

/** How the person signed in: through the fleet's Workspace, or by password, which reports no directory. */
const throughWorkspace = {
  authenticated: true,
  username: 'someone',
  directory: { method: 'google', connection: 'example' },
};
const byPassword = { authenticated: true, username: 'someone' };

interface Digests {
  global: PermissionDigest;
  anvil: PermissionDigest;
}

const marshalGrants: PermissionDigest = {
  SectorDirectories: { List: 'granted', Create: 'granted', Delete: 'granted' },
  CrewSectorRoles: { List: 'granted' },
  MakeSectorDirectoryOnly: { Execute: 'granted' },
  EndSectorDirectoryOnly: { Execute: 'granted' },
  OpenSectorToDirectory: { Execute: 'granted' },
  MakeSectorInviteOnly: { Execute: 'granted' },
};
const marshal: Digests = { global: {}, anvil: marshalGrants };
/** Headquarters reads the global record and the sector's directory, and holds no grant on the sector's method. */
const headquarters: Digests = {
  global: { Sectors: { Read: 'granted', Update: 'granted' } },
  anvil: { SectorDirectories: { List: 'granted' } },
};
/** What the server says making Anvil directory only would do: its sentence, the people it names, and the button's words. */
const wouldKeepOut: MethodWarning = {
  consequence:
    'Only people who sign in through example will be able to enter this sector. 2 people seated here sign in another way and will lose access.',
  names: ['Gale Ferris (gale)', 'hollis'],
  confirm: 'Admit only people who sign in through example',
};
/** What the server says opening Anvil to its directory would do, for the role as picked so far. */
const wouldOpen = (role: string): MethodWarning => ({
  consequence: role
    ? `Anyone who signs in through example will be able to enter this sector without an invitation and gets the role ${role}.`
    : 'Anyone who signs in through example will be able to enter this sector without an invitation. Choose the role each of them gets.',
  confirm: 'Open to people who sign in through example',
});

const nobody: Digests = { global: {}, anvil: {} };

/** What the scripted server holds: the grants, how the person signed in, the sector's directory, and its answer to a write. */
interface World {
  digests: Digests;
  session?: object;
  directories?: SectorDirectories[];
  write?: TransportResponse;
  /** Whether the sector is directory only as the page opens. */
  directoryOnly?: boolean;
  /** The server's answer to a request for a method's warning; the warnings above unless given. */
  warning?: TransportResponse;
}

/** The sector's own methods, by the last segment of their routes. */
const ownMethods = [
  'make-sector-directory-only',
  'end-sector-directory-only',
  'open-sector-to-directory',
  'make-sector-invite-only',
];

function serverFor(world: World): (request: TransportRequest) => TransportResponse {
  return (request) => {
    const url = new URL(request.url, 'http://lodestar.test');
    // The sector's own methods: a request for a method's warning is answered with the
    // server's words and writes nothing, and the call with the answer to a write.
    const own = ownMethods.find((route) => url.pathname === `${environment.apiUrl}/sectors/${anvilID}/${route}`);
    if (own && request.headers?.['X-Warning'] === 'true') {
      const role = (request.body as { role?: string } | undefined)?.role ?? '';
      return (
        world.warning ?? { status: 200, body: own === 'open-sector-to-directory' ? wouldOpen(role) : wouldKeepOut }
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
          body: url.searchParams.get('domain') === anvilID ? world.digests.anvil : world.digests.global,
        };
      case `${environment.apiUrl}/user-domains`:
        return { status: 200, body: [{ domain: anvilID, name: 'Anvil' }] };
      case `${environment.apiUrl}/features`:
        return { status: 200, body: { enabled: [] } };
      case `${environment.apiUrl}/sectors/${anvilID}`:
        return { status: 200, body: { id: anvilID, name: 'Anvil', directoryOnly: world.directoryOnly ?? false } };
      // The switch as it stands is the sector's own read.
      case `${environment.apiUrl}/sectors/${anvilID}/directory-only`:
        return { status: 200, body: { directoryOnly: world.directoryOnly ?? false } };
      case `${environment.apiUrl}/sectors/${anvilID}/sector-directories`:
        return { status: 200, body: world.directories ?? [] };
      case `${environment.apiUrl}/sectors/${anvilID}/crew-sector-roles`:
        return { status: 200, body: sectorRoles };
      // Every write rides the consolidated route as one operation.
      case `${environment.apiUrl}/resources`:
        return world.write ?? { status: 200, body: {} };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('SectorSignInComponent', () => {
  let fixture: ComponentFixture<SectorSignInComponent>;
  let transport: ScriptedTransport;

  /** Signs in, loads the sector list and the global and Anvil digests as entering the sector does, and renders the page for Anvil. */
  const pageFor = async (world: World): Promise<void> => {
    transport = scriptedTransport(serverFor(world));
    await TestBed.configureTestingModule({
      imports: [SectorSignInComponent, NoopAnimationsModule],
      providers: [
        provideRouter([]),
        provideNativeDateAdapter(),
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        // The method dialog draws each method from the generated metadata and runs it in
        // the sector the URL names, as the console's frame provides both.
        { provide: METHOD_META, useValue: methodMeta },
        { provide: RESOURCE_DOMAIN, useValue: signal<Domain | undefined>(anvilID as Domain) },
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    const client = TestBed.inject<ClientBase>(RESOURCE_CLIENT);
    await client.permissions.loadDomains();
    await client.permissions.loadDigest();
    await client.permissions.loadDigest(anvilID as Domain);
    await TestBed.inject(ApplicationRef).whenStable();
    fixture = TestBed.createComponent(SectorSignInComponent);
    fixture.componentRef.setInput('sectorID', anvilID);
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
  /** The requests the page and its dialog sent one of the sector's own methods, in order: `warning` for a request for its warning, `call` for the call, each with its body. */
  const sent = (route: string): { kind: 'warning' | 'call'; body: unknown }[] =>
    transport.requests
      .filter((r) => r.method === 'POST' && r.url.endsWith(`/sectors/${anvilID}/${route}`))
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

  it("a marshal signed in through a Workspace is offered to make it the sector's directory, and the request names none", async () => {
    await pageFor({ digests: marshal, session: throughWorkspace });

    // The sector's marshal cannot read the sector record: the name is the one the session's sector list carries.
    expect(texts('h1')).toEqual(['Who may enter Anvil']);
    expect(texts('.signed-in-through')).toEqual(['You are signed in through Google Workspace example.']);
    expect(texts('.make-directory')).toEqual(["Make example this sector's directory"]);
    expect(element().querySelector('.how-to-set')).toBeNull();
    // No field takes a method or a connection: a directory is never typed in.
    expect(element().querySelectorAll('input, form').length).toBe(0);

    element().querySelector<HTMLButtonElement>('.make-directory')?.click();
    await settle();
    await settle();
    // The server sets the directory the session came through, so the create carries nothing.
    expect(writes()).toEqual([{ op: 'add', path: `/sectors/${anvilID}/sector-directories`, value: {} }]);
    expect(texts('.refusal')).toEqual([]);
  });

  it('a marshal signed in with a password is offered no such action, and is told how a directory is set', async () => {
    await pageFor({ digests: marshal, session: byPassword });

    expect(element().querySelector('.make-directory')).toBeNull();
    expect(element().querySelector('.signed-in-through')).toBeNull();
    expect(texts('.how-to-set')).toEqual([
      "A directory is set by signing in through it. Sign in with your organization's account (Google Workspace or Microsoft Entra), then come back to this page and make it this sector's directory.",
    ]);
    expect(element().querySelectorAll('input, form').length).toBe(0);
    expect(writes()).toEqual([]);
  });

  it('a session without the Create grant is offered nothing, whatever it signed in through', async () => {
    await pageFor({
      digests: { global: {}, anvil: { SectorDirectories: { List: 'granted' } } },
      session: throughWorkspace,
    });

    expect(element().querySelector('.make-directory')).toBeNull();
    expect(element().querySelector('.how-to-set')).toBeNull();
    expect(texts('.empty')).toContain('This sector has no directory: its people sign in with a password.');
  });

  it("the server's refusal of the action is shown in its own words", async () => {
    const sentence =
      "Google Workspace example already has a sector here, and a directory belongs to one sector. Ask that sector's administrator to invite you.";
    await pageFor({
      digests: marshal,
      session: throughWorkspace,
      write: { status: 409, body: { message: sentence } },
    });

    await fixture.componentInstance.makeDirectory();
    await settle();
    expect(texts('.refusal')).toEqual([sentence]);
  });

  it('a sector that has a directory shows it, with no offer of another, and its marshal removes it', async () => {
    await pageFor({ digests: marshal, session: throughWorkspace, directories: [workspace] });

    expect(texts('.directory')).toEqual(['Google Workspace example']);
    // A sector has one directory: the action is offered only while it has none.
    expect(element().querySelector('.make-directory')).toBeNull();
    // The switch is the sector's own: its marshal sees it, read in the sector, and is offered to turn it.
    expect(texts('.directory-only')[0]).toContain('Every sign-in admitted');
    expect(button('.make-directory-only').disabled).toBe(false);

    element().querySelector<HTMLButtonElement>('.remove-directory')?.click();
    await settle();
    await settle();
    expect(writes()).toEqual([{ op: 'remove', path: `/sectors/${anvilID}/sector-directories/d-1` }]);
  });

  it("the sector's marshal opens the sector to its directory's people with a role picked in the dialog, behind the server's warning and the typed word", async () => {
    await pageFor({ digests: marshal, directories: [workspace] });

    // A row with no default role is invite only, and offers to open the sector.
    expect(texts('.seat')).toEqual(['Invite only: a person from the directory enters once they are invited.']);
    expect(texts('.open-to-directory')).toEqual(["Open to the directory's people"]);
    expect(element().querySelector('.make-invite-only')).toBeNull();

    button('.open-to-directory').click();
    await turn();
    await turn();
    // The page asked for the warning before it opened the dialog, and nothing is written.
    expect(sent('open-sector-to-directory')[0]).toEqual({ kind: 'warning', body: { role: '' } });
    expect(calls('open-sector-to-directory')).toEqual([]);
    expect(writes()).toEqual([]);
    // The dialog is the method's own: the role is an input, the word is typed, and the button is held.
    expect(dialogText('[mat-dialog-title]')).toBe("Open to the directory's people");
    expect(dialogText('.typed-word mat-label')).toBe('Type open to confirm');
    expect(runButton().disabled).toBe(true);

    // The role is picked: the dialog asks the server what that choice will do and shows its words.
    const dialog = TestBed.inject(MatDialog).openDialogs[0]?.componentInstance as MethodDialogComponent;
    dialog.form.controls['role']?.setValue('Pilot');
    await new Promise((resolve) => setTimeout(resolve, MethodDialogComponent.rehearsalDelay + 50));
    await turn();
    expect(dialogText('.warning-consequence')).toBe(wouldOpen('Pilot').consequence);
    expect(runButton().textContent?.trim()).toBe('Open to people who sign in through example');
    // The button is held until the word is typed.
    expect(runButton().disabled).toBe(true);
    await typeWord('open');
    expect(runButton().disabled).toBe(false);

    runButton().click();
    await turn();
    await turn();
    // The sector's own method names the role: nothing writes the record.
    expect(calls('open-sector-to-directory')).toEqual([{ role: 'Pilot' }]);
    expect(writes()).toEqual([]);
    expect(texts('.refusal')).toEqual([]);
  });

  it('a sector that is open to its directory says the role, and its marshal makes it invite only on a plain confirmation', async () => {
    await pageFor({ digests: marshal, directories: [seated] });

    expect(texts('.seat')).toEqual([
      "Open to the directory's people: each is given the role SectorCrew at their first sign-in through it.",
    ]);
    expect(texts('.open-to-directory')).toEqual(['Change the role']);

    button('.make-invite-only').click();
    await turn();
    // Making it invite only warns of nothing: the server is asked nothing first.
    expect(sent('make-sector-invite-only')).toEqual([]);
    expect(dialogText('[mat-dialog-title]')).toBe('Make invite only');
    expect(dialogText('.question')).toBe(
      'People who sign in through the directory will need an invitation to enter. The people it already seated keep their roles.',
    );
    expect(inDialog('.typed-word')).toBeNull();

    runButton().click();
    await turn();
    await turn();
    expect(sent('make-sector-invite-only')).toEqual([{ kind: 'call', body: {} }]);
    expect(writes()).toEqual([]);
  });

  it("the server's refusal to open a sector is shown in its own words, and no dialog opens", async () => {
    const sentence =
      "This sector has no directory, so there is nobody to open it to. Set its directory first: sign in through your organization's directory and make it this sector's directory.";
    await pageFor({
      digests: marshal,
      directories: [workspace],
      warning: { status: 409, body: { message: sentence } },
    });

    button('.open-to-directory').click();
    await turn();
    await turn();
    expect(texts('.refusal')).toEqual([sentence]);
    expect(inDialog('[mat-dialog-title]')).toBeNull();
    expect(calls('open-sector-to-directory')).toEqual([]);
  });

  it('a reader without the methods sees how the sector admits its directory as a sentence, and no way to change it or to remove it', async () => {
    await pageFor({
      digests: { global: {}, anvil: { SectorDirectories: { List: 'granted' } } },
      directories: [seated],
    });

    expect(element().querySelector('.open-to-directory')).toBeNull();
    expect(element().querySelector('.make-invite-only')).toBeNull();
    expect(element().querySelector('.remove-directory')).toBeNull();
    expect(texts('.seat')).toEqual([
      "Open to the directory's people: each is given the role SectorCrew at their first sign-in through it.",
    ]);
  });

  it("the sector's marshal makes the sector directory only behind the server's warning, the people it names as a list, and the typed word", async () => {
    await pageFor({ digests: marshal, session: throughWorkspace, directories: [workspace] });

    expect(texts('.directory-only')[0]).toContain('Every sign-in admitted');
    expect(element().querySelector('.needs-directory')).toBeNull();
    expect(texts('.turned-on-by')).toEqual([
      "Directory only is turned on by a person who is signed in through this sector's directory. You are shown who it will keep out before it is saved.",
    ]);

    button('.make-directory-only').click();
    await turn();
    await turn();
    // The page asked the server first, for this sector, and has written nothing.
    expect(sent('make-sector-directory-only').every((request) => request.kind === 'warning')).toBe(true);
    expect(sent('make-sector-directory-only').length).toBeGreaterThan(0);
    expect(writes()).toEqual([]);
    // The dialog is the server's: its sentence, the people it names as a list of their own, and the words on the button.
    expect(dialogText('[mat-dialog-title]')).toBe('Make Anvil directory only');
    expect(dialogText('.warning-consequence')).toBe(wouldKeepOut.consequence);
    expect(dialogTexts('.warning-names li')).toEqual(['Gale Ferris (gale)', 'hollis']);
    expect(runButton().textContent?.trim()).toBe('Admit only people who sign in through example');
    // The button is held until the person has typed the word, a fixed one.
    expect(dialogText('.typed-word mat-label')).toBe('Type close to confirm');
    expect(runButton().disabled).toBe(true);
    await typeWord('close');
    expect(runButton().disabled).toBe(false);

    runButton().click();
    await turn();
    await turn();
    // The sector's own method turns it: nothing is written to the global record.
    expect(calls('make-sector-directory-only')).toEqual([{}]);
    expect(writes()).toEqual([]);
    expect(texts('.refusal')).toEqual([]);
  });

  it('a person who may not list the people is told how many the switch keeps out, and the dialog draws no list', async () => {
    const counted: MethodWarning = { consequence: wouldKeepOut.consequence, confirm: wouldKeepOut.confirm };
    await pageFor({
      digests: marshal,
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

  it("headquarters sees the switch as the record shows it and cannot turn it: it is the sector's own", async () => {
    await pageFor({ digests: headquarters, session: throughWorkspace, directories: [workspace], directoryOnly: true });

    expect(texts('.directory-only')[0]).toContain('Directory only: only people who sign in through');
    expect(element().querySelector('.end-directory-only')).toBeNull();
    expect(element().querySelector('.make-directory-only')).toBeNull();
    expect(texts('.declaration-card .empty')).toContain('A marshal of this sector sets this, in the sector.');
    // The record showed the flag: the sector's own read is asked only by a person who may turn the switch.
    expect(
      transport.requests.map((r) => r.url.replace(/\?.*$/, '')).filter((u) => u.endsWith('/directory-only')),
    ).toEqual([]);
  });

  it('a person who cancels the dialog writes nothing, and the switch stays off', async () => {
    await pageFor({ digests: marshal, session: throughWorkspace, directories: [workspace] });

    button('.make-directory-only').click();
    await turn();
    await turn();
    (inDialog('mat-dialog-actions button:not(.run)') as HTMLButtonElement).click();
    await turn();
    await turn();
    expect(inDialog('[mat-dialog-title]')).toBeNull();
    expect(writes()).toEqual([]);
    expect(calls('make-sector-directory-only')).toEqual([]);
    expect(texts('.directory-only')[0]).toContain('Every sign-in admitted');
    expect(button('.make-directory-only').disabled).toBe(false);
  });

  it("the server's refusal to make a sector directory only is shown in its own words, with no dialog and no write", async () => {
    const sentence =
      "Directory only is turned on by a person who is signed in through this sector's directory, Google Workspace example: that shows the directory works, and that you will not shut yourself out. You signed in with a password. Sign in through Google Workspace example and try again.";
    await pageFor({
      digests: marshal,
      directories: [workspace],
      warning: { status: 409, body: { message: sentence } },
    });

    button('.make-directory-only').click();
    await turn();
    await turn();
    expect(texts('.refusal')).toEqual([sentence]);
    expect(inDialog('[mat-dialog-title]')).toBeNull();
    expect(calls('make-sector-directory-only')).toEqual([]);
    expect(texts('.directory-only')[0]).toContain('Every sign-in admitted');
  });

  it('a sector with no directory is told to set one first', async () => {
    await pageFor({ digests: marshal });

    expect(texts('.needs-directory')).toEqual([
      'Set a directory first: a sector with no directory cannot be directory only, since nobody could enter it.',
    ]);
    expect(element().querySelector('.turned-on-by')).toBeNull();
  });

  it('turning directory only off is a plain confirmation, and asks the server nothing first', async () => {
    await pageFor({ digests: marshal, directories: [workspace], directoryOnly: true });

    expect(texts('.directory-only')[0]).toContain(
      "Directory only: only people who sign in through this sector's directory enter this sector.",
    );
    expect(texts('.kept-out')).toEqual([
      'Everyone else seated in this sector is kept out while this is on, and keeps their roles. The People page says who they are.',
    ]);
    expect(element().querySelector('.make-directory-only')).toBeNull();

    button('.end-directory-only').click();
    await turn();
    expect(sent('end-sector-directory-only')).toEqual([]);
    expect(dialogText('[mat-dialog-title]')).toBe('Turn directory only off');
    expect(dialogText('.question')).toBe('Everyone seated here will be able to enter again, however they sign in.');
    expect(inDialog('.typed-word')).toBeNull();
    expect(runButton().textContent?.trim()).toBe('Turn directory only off');
    expect(writes()).toEqual([]);

    runButton().click();
    await turn();
    await turn();
    expect(sent('end-sector-directory-only')).toEqual([{ kind: 'call', body: {} }]);
    expect(writes()).toEqual([]);
  });

  it('a session without the grants sees the sentences, and nothing is requested under the sector', async () => {
    await pageFor({ digests: nobody, session: throughWorkspace });

    expect(texts('.empty')).toEqual([
      'Your role in this sector does not cover its directory.',
      'Your role does not cover this setting: a marshal of this sector sets it.',
    ]);
    expect(element().querySelector('.make-directory')).toBeNull();
    expect(
      transport.requests.map((r) => r.url.replace(/\?.*$/, '')).filter((u) => u.includes(`/sectors/${anvilID}`)),
    ).toEqual([]);
  });

  // Opened by its address (a reload, a bookmark), the page renders before the sector's
  // digest has loaded. It asks again when the digest arrives, and shows what it grants.
  it('opened before the digests load, it shows the directory once they arrive', async () => {
    transport = scriptedTransport(serverFor({ digests: marshal, directories: [workspace] }));
    await TestBed.configureTestingModule({
      imports: [SectorSignInComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    fixture = TestBed.createComponent(SectorSignInComponent);
    fixture.componentRef.setInput('sectorID', anvilID);
    await settle();
    expect(texts('.empty')).toContain('Your role in this sector does not cover its directory.');

    const client = TestBed.inject<ClientBase>(RESOURCE_CLIENT);
    await client.permissions.loadDigest();
    await client.permissions.loadDigest(anvilID as Domain);
    await settle();
    await settle();

    expect(texts('.empty')).not.toContain('Your role in this sector does not cover its directory.');
    expect(texts('.directory')).toEqual(['Google Workspace example']);
    expect(element().querySelector('.directory-only')).not.toBeNull();
  });
});
