import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatSelect } from '@angular/material/select';
import { By } from '@angular/platform-browser';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { createApi } from '@app/service/zz_gen_api';
import { TenantDirectories } from '@app/service/zz_gen_resources';
import {
  ClientBase,
  DirectoryOnlyConsequence,
  Domain,
  PermissionDigest,
  TransportRequest,
  TransportResponse,
} from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { TenantSignInComponent } from './tenant-sign-in.component';

// The tenant's sign-in page shows who may enter the tenant as its declaration says, and
// lets the holder of each grant change it. A tenant has one directory, which nobody
// types in: the tenant's administrator (List, Create, Update and Delete on
// TenantDirectories in the tenant) who is signed in through an organization's directory
// is offered to make it the tenant's, and the request names no directory; signed in by
// password they are offered no such action and told how a directory is set. They pick
// the directory's default role from the tenant's roles or clear it, remove the
// directory, and are told the application's administrator sets directory only; the
// application's administrator (Update on the global Tenants record too) turns it on once
// they confirm what the server says it would do, its sentence and the people it names,
// and off on a plain confirmation; the server's refusal to turn it on is shown in its own
// words and nothing is written; a session without the grants sees the sentences and
// nothing is requested.

/** Acme's id: a tenant's key is an id its create generates, and the name is a field. */
const acmeID = 'ac4e0000-0000-4000-8000-000000000001';
const workspace: TenantDirectories = { id: 'd-1', tenantId: acmeID, method: 'google', connection: 'acme.example' };
const seated: TenantDirectories = { ...workspace, defaultSeat: 'TenantAdministrator' };
const tenantRoles = [{ name: 'TenantAdministrator' }];

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
  TenantDirectories: { List: 'granted', Create: 'granted', Update: 'granted', Delete: 'granted' },
  StaffTenantRoles: { List: 'granted' },
};
const tenantAdministrator: Digests = { global: {}, acme: administratorGrants };
const applicationAdministrator: Digests = {
  global: { Tenants: { Read: 'granted', Update: 'granted' } },
  acme: administratorGrants,
};
/** What the server says turning directory only on would do to Acme: its sentence, the people it names, and the button's words. */
const wouldKeepOut: DirectoryOnlyConsequence = {
  directory: 'Google Workspace acme.example',
  connection: 'acme.example',
  sentence:
    'Only people who sign in through acme.example will be able to enter this tenant. 2 people seated here sign in another way and will lose access.',
  people: [
    { userId: 'u-1', username: 'gale', name: 'Gale Ferris' },
    { userId: 'u-2', username: 'hollis' },
  ],
  confirm: 'Admit only people who sign in through acme.example',
};

const nobody: Digests = { global: {}, acme: {} };

/** What the scripted server holds: the grants, how the person signed in, the tenant's directory, and its answer to a write. */
interface World {
  digests: Digests;
  session?: object;
  directories?: TenantDirectories[];
  write?: TransportResponse;
  /** Whether the tenant is directory only as the page opens. */
  directoryOnly?: boolean;
  /** The server's answer to what turning directory only on would do; the two people above unless given. */
  consequence?: TransportResponse;
}

function serverFor(world: World): (request: TransportRequest) => TransportResponse {
  return (request) => {
    const url = new URL(request.url, 'http://skeleton.test');
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
      case `${environment.apiUrl}/directory-only`:
        return world.consequence ?? { status: 200, body: wouldKeepOut };
      case `${environment.apiUrl}/tenants/${acmeID}/tenant-directories`:
        return { status: 200, body: world.directories ?? [] };
      case `${environment.apiUrl}/tenants/${acmeID}/staff-tenant-roles`:
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
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
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
  /** What the page asked the server about directory only, in order. */
  const asked = (): string[] =>
    transport.requests.map((r) => r.url).filter((url) => url.includes('/directory-only'));
  const inDialog = (selector: string): HTMLElement | null =>
    document.querySelector<HTMLElement>(`.cdk-overlay-container ${selector}`);
  const dialogText = (selector: string): string => inDialog(selector)?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
  /** The server's answer and the dialog's each travel through a timer; a turn of the clock lets them, and the page draws. */
  const turn = async (): Promise<void> => {
    await new Promise((resolve) => setTimeout(resolve, 0));
    await settle();
  };
  /** The switch itself, as a person presses it. */
  const toggle = (): HTMLButtonElement => element().querySelector<HTMLButtonElement>('.directory-only button')!;

  it("an administrator signed in through a Workspace is offered to make it the tenant's directory, and the request names none", async () => {
    await pageFor({ digests: tenantAdministrator, session: throughWorkspace });

    // The tenant's administrator cannot read the tenant record: the name is the one the session's tenant list carries.
    expect(texts('h1')).toEqual(['Who may enter Acme']);
    expect(texts('.signed-in-through')).toEqual(['You are signed in through Google Workspace acme.example.']);
    expect(texts('.make-directory')).toEqual(["Make acme.example this tenant's directory"]);
    expect(element().querySelector('.how-to-set')).toBeNull();
    // No field takes a method or a connection: a directory is never typed in.
    expect(element().querySelectorAll('input, form').length).toBe(0);

    element().querySelector<HTMLButtonElement>('.make-directory')?.click();
    await settle();
    await settle();
    // The server sets the directory the session came through, so the create carries nothing.
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

  it('a tenant that has a directory shows it, with no offer of another, and its administrator removes it', async () => {
    await pageFor({ digests: tenantAdministrator, session: throughWorkspace, directories: [workspace] });

    expect(texts('.directory')).toEqual(['Google Workspace acme.example']);
    // A tenant has one directory: the action is offered only while it has none.
    expect(element().querySelector('.make-directory')).toBeNull();
    expect(element().querySelector('.directory-only')).toBeNull();
    expect(texts('.empty')).toContain('Your role does not cover the tenant record, which carries this setting.');

    element().querySelector<HTMLButtonElement>('.remove-directory')?.click();
    await settle();
    await settle();
    expect(writes()).toEqual([{ op: 'remove', path: `/tenants/${acmeID}/tenant-directories/d-1` }]);
  });

  it("the tenant's administrator picks the directory's default role from the tenant's roles, and clears it", async () => {
    await pageFor({ digests: tenantAdministrator, directories: [workspace] });

    // The picker offers invitation only and every role of the tenant; a row with no
    // default role stands at invitation only (the empty choice).
    const picker = fixture.debugElement.query(By.css('.seat-picker')).componentInstance as MatSelect;
    expect(picker.value).toBe('');
    expect(picker.options.map((o) => o.viewValue)).toEqual(['Invitation only', 'TenantAdministrator']);

    await fixture.componentInstance.seat(workspace, 'TenantAdministrator');
    await settle();
    expect(writes()).toEqual([
      { op: 'patch', path: `/tenants/${acmeID}/tenant-directories/d-1`, value: { defaultSeat: 'TenantAdministrator' } },
    ]);

    // Clearing the default role sends an explicit null: the column is cleared, not left alone.
    await fixture.componentInstance.seat(seated, '');
    await settle();
    expect(writes()).toEqual([
      { op: 'patch', path: `/tenants/${acmeID}/tenant-directories/d-1`, value: { defaultSeat: 'TenantAdministrator' } },
      { op: 'patch', path: `/tenants/${acmeID}/tenant-directories/d-1`, value: { defaultSeat: null } },
    ]);
  });

  it('a reader without Update on the directory sees its default role as a sentence, and no way to remove it', async () => {
    await pageFor({ digests: { global: {}, acme: { TenantDirectories: { List: 'granted' } } }, directories: [seated] });

    expect(element().querySelector('.seat-picker')).toBeNull();
    expect(element().querySelector('.remove-directory')).toBeNull();
    expect(texts('.seat')).toEqual(['TenantAdministrator']);
    expect(
      transport.requests.map((r) => r.url.replace(/\?.*$/, '')).filter((u) => u.includes('staff-tenant-roles')),
    ).toEqual([]);
  });

  it("the application's administrator turns directory only on once they confirm what the server says it would do", async () => {
    await pageFor({ digests: applicationAdministrator, session: throughWorkspace, directories: [workspace] });

    expect(toggle().getAttribute('aria-checked')).toBe('false');
    expect(texts('.directory-only')[0]).toContain('Every sign-in admitted');
    expect(element().querySelector('.needs-directory')).toBeNull();
    expect(texts('.turned-on-by')).toEqual([
      "Directory only is turned on by a person who is signed in through this tenant's directory. You are shown who it will keep out before it is saved.",
    ]);

    toggle().click();
    await turn();
    // The page asked the server first, for this tenant, and has written nothing.
    expect(asked()).toEqual([`${environment.apiUrl}/directory-only?domain=${acmeID}`]);
    expect(writes()).toEqual([]);
    // The confirmation is the server's: its sentence, the people it names, and the words on the button.
    expect(dialogText('.impulse-action-title')).toBe('Make Acme directory only?');
    expect(dialogText('.impulse-action-words')).toBe(`${wouldKeepOut.sentence} They are: Gale Ferris (gale), hollis.`);
    expect(dialogText('.impulse-action-confirm')).toBe('Admit only people who sign in through acme.example');
    // Until the write lands the switch stands where the record says.
    expect(toggle().getAttribute('aria-checked')).toBe('false');

    inDialog('.impulse-action-confirm')!.click();
    await turn();
    await turn();
    expect(writes()).toEqual([{ op: 'patch', path: `/tenants/${acmeID}`, value: { directoryOnly: true } }]);
    expect(texts('.refusal')).toEqual([]);
  });

  it('a person who cancels the confirmation writes nothing, and the switch stays off', async () => {
    await pageFor({ digests: applicationAdministrator, session: throughWorkspace, directories: [workspace] });

    toggle().click();
    await turn();
    inDialog('.impulse-action-cancel')!.click();
    await turn();
    await turn();
    expect(inDialog('.impulse-action-title')).toBeNull();
    expect(writes()).toEqual([]);
    expect(toggle().getAttribute('aria-checked')).toBe('false');
    expect(toggle().disabled).toBe(false);
  });

  it("the server's refusal to turn directory only on is shown in its own words, with no confirmation and no write", async () => {
    const sentence =
      "Directory only is turned on by a person who is signed in through this tenant's directory, Google Workspace acme.example: that shows the directory works, and that you will not shut yourself out. You signed in with a password. Sign in through Google Workspace acme.example and try again.";
    await pageFor({
      digests: applicationAdministrator,
      directories: [workspace],
      consequence: { status: 409, body: { message: sentence } },
    });

    toggle().click();
    await turn();
    await turn();
    expect(texts('.refusal')).toEqual([sentence]);
    expect(inDialog('.impulse-action-title')).toBeNull();
    expect(writes()).toEqual([]);
    expect(toggle().getAttribute('aria-checked')).toBe('false');
  });

  it('a tenant with no directory is told to set one first', async () => {
    await pageFor({ digests: applicationAdministrator });

    expect(texts('.needs-directory')).toEqual([
      'Set a directory first: a tenant with no directory cannot be directory only, since nobody could enter it.',
    ]);
    expect(element().querySelector('.turned-on-by')).toBeNull();
  });

  it('turning directory only off is a plain confirmation, and asks the server nothing first', async () => {
    await pageFor({ digests: applicationAdministrator, directories: [workspace], directoryOnly: true });

    expect(toggle().getAttribute('aria-checked')).toBe('true');
    expect(texts('.directory-only')[0]).toContain(
      "Directory only: only people who sign in through this tenant's directory enter this tenant.",
    );
    expect(texts('.kept-out')).toEqual([
      'Everyone else seated in this tenant is kept out while this is on, and keeps their roles. The People page says who they are.',
    ]);

    toggle().click();
    await turn();
    expect(asked()).toEqual([]);
    expect(dialogText('.impulse-action-title')).toBe('Turn directory only off for Acme?');
    expect(dialogText('.impulse-action-words')).toBe(
      'Everyone seated here will be able to enter again, however they sign in.',
    );
    expect(dialogText('.impulse-action-confirm')).toBe('Turn directory only off');
    expect(writes()).toEqual([]);

    inDialog('.impulse-action-confirm')!.click();
    await turn();
    await turn();
    expect(writes()).toEqual([{ op: 'patch', path: `/tenants/${acmeID}`, value: { directoryOnly: false } }]);
  });

  it('a session without the grants sees the sentences, and nothing is requested under the tenant', async () => {
    await pageFor({ digests: nobody, session: throughWorkspace });

    expect(texts('.empty')).toEqual([
      'Your role in this tenant does not cover its directory.',
      'Your role does not cover the tenant record, which carries this setting.',
    ]);
    expect(element().querySelector('.make-directory')).toBeNull();
    expect(
      transport.requests.map((r) => r.url.replace(/\?.*$/, '')).filter((u) => u.includes(`/tenants/${acmeID}`)),
    ).toEqual([]);
  });

  // Opened by its address (a reload, a bookmark), the page renders before the tenant's
  // digest has loaded. It asks again when the digest arrives, and shows what it grants.
  it('opened before the digests load, it shows the directory once they arrive', async () => {
    transport = scriptedTransport(serverFor({ digests: applicationAdministrator, directories: [workspace] }));
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
