import { HttpErrorResponse } from '@angular/common/http';
import { signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { NoopAnimationsModule } from '@angular/platform-browser/animations';
import { provideRouter } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { provideFrame, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { ImpersonationRecord, ImpersonationService } from '../impersonation.service';
import { RosterComponent } from './roster.component';

// Demonstrates: impersonation.view-as, impersonation.act-as-role.
//
// The roster draws the sector's squadrons as a table of its own and no form. Viewing as a
// crew member and acting as a role are two buttons, each opening its picker in a dialog
// over the page, as the library's lists open their create: the dialog is titled with the
// button's words and carries Cancel and those words as its buttons; Cancel and Escape
// close it with nothing minted; the mint names the person by the id of their account, or
// the role by its name, and the dialog closes once the session is minted; a mint the
// server refuses is shown in the dialog in the server's words. A session that holds
// neither grant, or one that is already minted, is offered neither button.

/** Anvil's id: a sector's key is an id its create generates, and the name is a field. */
const anvilID = 'a1111111-1111-4111-8111-111111111111';

/** The grants the roster's lists need in the sector. */
const reads: PermissionDigest = {
  Wings: { List: 'granted' },
  Squadrons: { List: 'granted' },
  SquadronMemberships: { List: 'granted' },
};
/** The Marshal: the lists, the sector's people, and both mints. */
const marshal: PermissionDigest = {
  ...reads,
  CrewSectorUsers: { List: 'granted' },
  ViewAsUser: { Execute: 'granted' },
  AssumeRole: { Execute: 'granted' },
};

/** What the page is drawn over: the grants in Anvil, and the mint route as the spec plays it. */
interface World {
  grants: PermissionDigest;
  /** The record of a session that is already minted, as the session endpoint reports it. */
  minted?: ImpersonationRecord;
  /** What the mint route answers with instead of a session. */
  refuses?: HttpErrorResponse;
}

function server(grants: PermissionDigest): (request: TransportRequest) => TransportResponse {
  return (request) => {
    const url = new URL(request.url, 'http://lodestar.test');
    switch (url.pathname) {
      case `${environment.apiUrl}/user/session`:
        return { status: 200, body: { authenticated: true, username: 'marshal' } };
      case `${environment.apiUrl}/permission-digest`:
        return {
          status: 200,
          body: url.searchParams.get('domain') === anvilID ? grants : { Pilots: { List: 'granted' } },
        };
      case `${environment.apiUrl}/user-domains`:
        return { status: 200, body: [{ domain: anvilID, name: 'Anvil' }] };
      case `${environment.apiUrl}/sectors/${anvilID}/wings`:
        return { status: 200, body: [{ id: 'w-1', name: 'Forge Wing' }] };
      case `${environment.apiUrl}/sectors/${anvilID}/squadrons`:
        return { status: 200, body: [{ id: 's-1', name: 'Kestrel Flight', wingId: 'w-1' }] };
      case `${environment.apiUrl}/sectors/${anvilID}/squadron-memberships`:
        return { status: 200, body: [{ squadronId: 's-1', userId: 'u-cadet' }] };
      case `${environment.apiUrl}/sectors/${anvilID}/crew-sector-users`:
        return {
          status: 200,
          body: [
            { id: 'u-cadet', username: 'cadet' },
            { id: 'u-guest', username: 'guest' },
          ],
        };
      case `${environment.apiUrl}/pilots`:
        return { status: 200, body: [{ id: 'p-1', userId: 'u-cadet', displayName: 'Cass' }] };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('RosterComponent', () => {
  let fixture: ComponentFixture<RosterComponent>;
  let transport: ScriptedTransport;
  /** The sessions the page asked the mint route for, in order. */
  let mints: { kind: 'user' | 'role'; principal: string }[];

  /** Draws the page and lets pending answers land until `done` answers true, for at most `rounds` rounds. */
  const settle = async (done: () => boolean = () => false, rounds = 10): Promise<void> => {
    for (let i = 0; i < rounds && !done(); i++) {
      fixture.detectChanges();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  };

  /** Signs in, opens the roster in Anvil as the tenant guard does, and waits for the squadrons. */
  const pageFor = async (world: World): Promise<void> => {
    transport = scriptedTransport(server(world.grants));
    mints = [];
    // The mint route is hand-written and asked through HttpClient: the spec plays it at
    // the service, which records what the page asked for and refuses as the world says.
    const mint = (kind: 'user' | 'role', principal: string): Promise<void> => {
      mints.push({ kind, principal });
      return world.refuses ? Promise.reject(world.refuses) : Promise.resolve();
    };
    const impersonation: Pick<ImpersonationService, 'record' | 'viewAs' | 'assumeRole'> = {
      record: signal(world.minted),
      viewAs: (userId) => mint('user', userId),
      assumeRole: (role) => mint('role', role),
    };
    await TestBed.configureTestingModule({
      imports: [RosterComponent, NoopAnimationsModule],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideRouter([]),
        provideFrame({ home: 'dashboard', menus: [] }),
        { provide: ImpersonationService, useValue: impersonation },
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    TestBed.inject(TenantService).enter(anvilID);
    fixture = TestBed.createComponent(RosterComponent);
    await settle(() => rows().length > 0, 50);
  };

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const text = (e: Element | null): string => e?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
  const texts = (selector: string): string[] => Array.from(element().querySelectorAll(selector)).map(text);
  /** The table's rows, each as its cells' text. */
  const rows = (): string[][] =>
    Array.from(element().querySelectorAll('tr[mat-row]')).map((row) =>
      Array.from(row.querySelectorAll('td')).map(text),
    );
  const inDialog = <E extends HTMLElement>(selector: string): E | null =>
    document.querySelector<E>(`.cdk-overlay-container mat-dialog-container ${selector}`);
  const dialogTexts = (selector: string): string[] =>
    Array.from(document.querySelectorAll(`.cdk-overlay-container mat-dialog-container ${selector}`)).map(text);
  const dialogOpen = (): boolean => document.querySelector('.cdk-overlay-container mat-dialog-container') !== null;
  /** Opens a dialog by its button on the page, as a person does. */
  const open = async (button: string): Promise<void> => {
    element().querySelector<HTMLButtonElement>(button)?.click();
    await settle();
  };
  /** Opens the dialog's picker and picks the option that reads `label`, as a person does. */
  const pick = async (label: string): Promise<void> => {
    inDialog('.mat-mdc-select-trigger')?.click();
    await settle();
    const options = Array.from(document.querySelectorAll<HTMLElement>('.cdk-overlay-container mat-option'));
    options.find((option) => text(option) === label)?.click();
    await settle();
  };
  /** Sends the dialog's form by its button. */
  const send = async (): Promise<void> => {
    inDialog<HTMLButtonElement>('button.send')?.click();
    await settle(() => false, 5);
  };

  it('draws the squadrons as its own table and no form: the two mints are buttons', async () => {
    await pageFor({ grants: marshal });

    expect(texts('th')).toEqual(['Squadron', 'Wing', 'Flies with']);
    expect(rows()).toEqual([['Kestrel Flight', 'Forge Wing', 'Cass']]);
    expect(element().querySelectorAll('form, input, mat-select').length).toBe(0);
    // The two buttons sit in a card named for what it holds, not for the form it once drew.
    expect(texts('.impersonation-card mat-card-title')).toEqual(['Viewing as, acting as']);
    expect(texts('.impersonation-card .actions button')).toEqual(['View as', 'Act as role']);
    expect(element().querySelector('.create-card')).toBeNull();
    expect(dialogOpen()).toBe(false);
    expect(mints).toEqual([]);
  });

  it('offers neither button to a session without the two grants', async () => {
    await pageFor({ grants: reads });

    expect(rows().length).toBe(1);
    expect(element().querySelector('.impersonation-card')).toBeNull();
    expect(element().querySelector('.actions')).toBeNull();
  });

  it('offers neither button to a session that is already minted: chaining is refused', async () => {
    await pageFor({
      grants: marshal,
      minted: { actor: 'marshal', principalKind: 'Role', principal: 'Cadet', expiresAt: '2030-01-01T00:00:00Z' },
    });

    expect(element().querySelector('.impersonation-card')).toBeNull();
    expect(element().querySelector('.actions')).toBeNull();
  });

  it("View as opens its picker in a dialog under the button's words, and Cancel mints nothing", async () => {
    await pageFor({ grants: marshal });
    await open('button.view-as');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['View as']);
    expect(dialogTexts('mat-label')).toEqual(['Crew member']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'View as']);

    await pick('Cadet Cass (cadet)');
    inDialog<HTMLButtonElement>('button.cancel')?.click();
    await settle(() => !dialogOpen());

    expect(dialogOpen()).toBe(false);
    expect(mints).toEqual([]);
  });

  it('Escape closes the dialog with nothing minted', async () => {
    await pageFor({ grants: marshal });
    await open('button.assume-role');
    expect(dialogOpen()).toBe(true);

    document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
    await settle(() => !dialogOpen());

    expect(dialogOpen()).toBe(false);
    expect(mints).toEqual([]);
  });

  it('View as mints a session as the person picked, named by the id of their account, and the dialog closes', async () => {
    await pageFor({ grants: marshal });
    await open('button.view-as');
    // A person of the cast reads by the persona's name, anyone else by their username.
    await pick('guest');
    await send();
    await settle(() => !dialogOpen());

    expect(mints).toEqual([{ kind: 'user', principal: 'u-guest' }]);
    expect(dialogOpen()).toBe(false);
  });

  it("Act as role opens its picker in a dialog under the button's words and mints a session under the role picked", async () => {
    await pageFor({ grants: marshal });
    await open('button.assume-role');

    expect(dialogTexts('[mat-dialog-title]')).toEqual(['Act as role']);
    expect(dialogTexts('mat-label')).toEqual(['Role']);
    expect(dialogTexts('mat-dialog-actions button')).toEqual(['Cancel', 'Act as role']);

    await pick('Dispatcher');
    await send();
    await settle(() => !dialogOpen());

    expect(mints).toEqual([{ kind: 'role', principal: 'Dispatcher' }]);
    expect(dialogOpen()).toBe(false);
  });

  it('sends nothing while nothing is picked', async () => {
    await pageFor({ grants: marshal });
    await open('button.view-as');
    await send();

    expect(mints).toEqual([]);
    expect(dialogOpen()).toBe(true);
  });

  it("a mint the server refuses is shown in the dialog in the server's words, and the picker stays", async () => {
    const sentence = 'user marshal does not have Execute on AssumeRole';
    await pageFor({
      grants: marshal,
      refuses: new HttpErrorResponse({ status: 403, error: { message: sentence } }),
    });
    await open('button.assume-role');
    await pick('Cadet');
    await send();
    await settle(() => dialogTexts('.refusal').length > 0, 50);

    expect(mints).toEqual([{ kind: 'role', principal: 'Cadet' }]);
    expect(dialogOpen()).toBe(true);
    expect(dialogTexts('.refusal')).toEqual([sentence]);
    expect(element().querySelector('.refusal')).toBeNull();
  });
});
