import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatSelect } from '@angular/material/select';
import { By } from '@angular/platform-browser';
import { createApi } from '@app/service/zz_gen_api';
import { TenantDirectories } from '@app/service/zz_gen_resources';
import { ClientBase, Domain, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
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
// application's administrator (Update on the global Tenants record too) flips the flag;
// a session without the grants sees the sentences and nothing is requested.

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
const nobody: Digests = { global: {}, acme: {} };

/** What the scripted server holds: the grants, how the person signed in, the tenant's directory, and its answer to a write. */
interface World {
  digests: Digests;
  session?: object;
  directories?: TenantDirectories[];
  write?: TransportResponse;
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
        return { status: 200, body: { id: acmeID, name: 'Acme', directoryOnly: false } };
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
      imports: [TenantSignInComponent],
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

  it("the application's administrator flips directory only on the tenant record", async () => {
    await pageFor({ digests: applicationAdministrator, directories: [workspace] });

    const toggle = element().querySelector<HTMLElement>('.directory-only');
    expect(toggle).not.toBeNull();
    expect(toggle?.textContent?.replace(/\s+/g, ' ').trim()).toContain('Passwords admitted');
    expect(texts('.empty')).not.toContain(
      'Set a directory first: directory only with no directory admits nobody but staff.',
    );

    await fixture.componentInstance.close(true);
    await settle();
    expect(writes()).toEqual([{ op: 'patch', path: `/tenants/${acmeID}`, value: { directoryOnly: true } }]);
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
