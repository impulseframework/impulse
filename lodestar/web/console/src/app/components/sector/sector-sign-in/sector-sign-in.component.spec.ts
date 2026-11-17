import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatSelect } from '@angular/material/select';
import { By } from '@angular/platform-browser';
import { createApi } from '@app/service/zz_gen_api';
import { SectorDirectories } from '@app/service/zz_gen_resources';
import { ClientBase, Domain, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { SectorSignInComponent } from './sector-sign-in.component';

// Demonstrates: tenancy.declaration, tenancy.directory-only, tenancy.default-seat.
//
// The sector's sign-in page shows who may enter the sector as its declaration says, and
// lets the holder of each grant change it. A sector has one directory, which nobody
// types in: the sector's marshal (List, Create, Update and Delete on SectorDirectories
// in the sector) who is signed in through an organization's directory is offered to
// make it the sector's, and the request names no directory; signed in by password they
// are offered no such action and told how a directory is set. They pick the directory's
// default role from the sector's roles or clear it, remove the directory, and are told
// headquarters sets directory only; headquarters (Update on the global Sectors record
// too) flips the flag; a session without the grants sees the sentences and nothing is
// requested.

/** Anvil's id: a sector's key is an id its create generates, and the name is a field. */
const anvilID = 'a1111111-1111-4111-8111-111111111111';
const workspace: SectorDirectories = { id: 'd-1', sectorId: anvilID, method: 'google', connection: 'example' };
const seated: SectorDirectories = { ...workspace, defaultSeat: 'SectorCrew' };
const sectorRoles = [{ name: 'SectorCrew' }, { name: 'SectorMarshal' }];

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
  SectorDirectories: { List: 'granted', Create: 'granted', Update: 'granted', Delete: 'granted' },
  CrewSectorRoles: { List: 'granted' },
};
const marshal: Digests = { global: {}, anvil: marshalGrants };
const headquarters: Digests = {
  global: { Sectors: { Read: 'granted', Update: 'granted' } },
  anvil: marshalGrants,
};
const nobody: Digests = { global: {}, anvil: {} };

/** What the scripted server holds: the grants, how the person signed in, the sector's directory, and its answer to a write. */
interface World {
  digests: Digests;
  session?: object;
  directories?: SectorDirectories[];
  write?: TransportResponse;
}

function serverFor(world: World): (request: TransportRequest) => TransportResponse {
  return (request) => {
    const url = new URL(request.url, 'http://lodestar.test');
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
        return { status: 200, body: { id: anvilID, name: 'Anvil', directoryOnly: false } };
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
      imports: [SectorSignInComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
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
    expect(element().querySelector('.directory-only')).toBeNull();
    expect(texts('.empty')).toContain('Your role does not cover the sector record, which carries this setting.');

    element().querySelector<HTMLButtonElement>('.remove-directory')?.click();
    await settle();
    await settle();
    expect(writes()).toEqual([{ op: 'remove', path: `/sectors/${anvilID}/sector-directories/d-1` }]);
  });

  it("the sector's marshal picks the directory's default role from the sector's roles, and clears it", async () => {
    await pageFor({ digests: marshal, directories: [workspace] });

    // The picker offers invitation only and every role of the sector; a row with no
    // default role stands at invitation only (the empty choice).
    const picker = fixture.debugElement.query(By.css('.seat-picker')).componentInstance as MatSelect;
    expect(picker.value).toBe('');
    expect(picker.options.map((o) => o.viewValue)).toEqual(['Invitation only', 'SectorCrew', 'SectorMarshal']);

    await fixture.componentInstance.seat(workspace, 'SectorCrew');
    await settle();
    expect(writes()).toEqual([
      { op: 'patch', path: `/sectors/${anvilID}/sector-directories/d-1`, value: { defaultSeat: 'SectorCrew' } },
    ]);

    // Clearing the default role sends an explicit null: the column is cleared, not left alone.
    await fixture.componentInstance.seat(seated, '');
    await settle();
    expect(writes()).toEqual([
      { op: 'patch', path: `/sectors/${anvilID}/sector-directories/d-1`, value: { defaultSeat: 'SectorCrew' } },
      { op: 'patch', path: `/sectors/${anvilID}/sector-directories/d-1`, value: { defaultSeat: null } },
    ]);
  });

  it('a reader without Update on the directory sees its default role as a sentence, and no way to remove it', async () => {
    await pageFor({
      digests: { global: {}, anvil: { SectorDirectories: { List: 'granted' } } },
      directories: [seated],
    });

    expect(element().querySelector('.seat-picker')).toBeNull();
    expect(element().querySelector('.remove-directory')).toBeNull();
    expect(texts('.seat')).toEqual(['SectorCrew']);
    expect(
      transport.requests.map((r) => r.url.replace(/\?.*$/, '')).filter((u) => u.includes('crew-sector-roles')),
    ).toEqual([]);
  });

  it('headquarters flips directory only on the sector record', async () => {
    await pageFor({ digests: headquarters, directories: [workspace] });

    const toggle = element().querySelector<HTMLElement>('.directory-only');
    expect(toggle).not.toBeNull();
    expect(toggle?.textContent?.replace(/\s+/g, ' ').trim()).toContain('Passwords admitted');
    expect(texts('.empty')).not.toContain(
      'Set a directory first: directory only with no directory admits nobody but staff.',
    );

    await fixture.componentInstance.close(true);
    await settle();
    expect(writes()).toEqual([{ op: 'patch', path: `/sectors/${anvilID}`, value: { directoryOnly: true } }]);
  });

  it('a session without the grants sees the sentences, and nothing is requested under the sector', async () => {
    await pageFor({ digests: nobody, session: throughWorkspace });

    expect(texts('.empty')).toEqual([
      'Your role in this sector does not cover its directory.',
      'Your role does not cover the sector record, which carries this setting.',
    ]);
    expect(element().querySelector('.make-directory')).toBeNull();
    expect(
      transport.requests.map((r) => r.url.replace(/\?.*$/, '')).filter((u) => u.includes(`/sectors/${anvilID}`)),
    ).toEqual([]);
  });

  // Opened by its address (a reload, a bookmark), the page renders before the sector's
  // digest has loaded. It asks again when the digest arrives, and shows what it grants.
  it('opened before the digests load, it shows the directory once they arrive', async () => {
    transport = scriptedTransport(serverFor({ digests: headquarters, directories: [workspace] }));
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
