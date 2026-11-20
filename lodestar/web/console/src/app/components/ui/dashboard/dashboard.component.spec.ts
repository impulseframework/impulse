import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { By } from '@angular/platform-browser';
import { provideRouter } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { resourceMeta } from '@app/service/zz_gen_resources';
import { ClientBase, Domain, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { provideFrame, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { ResourceListComponent } from '@impulseframework/resource-angular/resource';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { RESOURCE_META } from '@impulseframework/resource-angular/types';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { DashboardComponent } from './dashboard.component';

/** Anvil's digest for a marshal: every mission permission, ships read under terms. */
/** Anvil's id: a sector's key is an id its create generates, and the name is a field. */
const anvilID = 'a1111111-1111-4111-8111-111111111111';
const cinderID = 'c2222222-2222-4222-8222-222222222222';
const anvil: PermissionDigest = {
  Missions: { List: 'granted', Read: 'granted', Create: 'granted', Update: 'granted', Delete: 'granted' },
  Ships: { List: 'conditional', Read: 'conditional' },
};

/** The global digest of a person who may list the service ledger, field by field: the Governor's. */
const ledgerGranted: PermissionDigest = Object.fromEntries(
  ['', '.name', '.openMissions', '.feesOutstanding', '.settlements', '.lastReturnAt'].map((field) => [
    `ServiceLedgers${field}`,
    { List: 'granted' },
  ]),
);

/** The ledger as the server answers it: one row per sector. */
const ledger = [
  { sectorId: anvilID, name: 'Anvil', openMissions: 3, feesOutstanding: 450, settlements: 2 },
  { sectorId: cinderID, name: 'Cinder', openMissions: 0, feesOutstanding: 0, settlements: 5 },
];

/**
 * The server as the spec plays it for one person: the session, the global digest (empty
 * for a marshal, who holds nothing outside a sector), the user's domains with their
 * names, the digest of the sector asked for, and the service ledger. Anything else is a
 * 404 the request log would show.
 */
function serverFor(username: string, global: PermissionDigest): (request: TransportRequest) => TransportResponse {
  return (request) => {
    const url = new URL(request.url, 'http://lodestar.test');
    switch (url.pathname) {
      case `${environment.apiUrl}/user/session`:
        return {
          status: 200,
          body: { authenticated: true, username, email: `${username}@lodestar.test`, emailVerified: true },
        };
      case `${environment.apiUrl}/permission-digest`:
        return { status: 200, body: url.searchParams.get('domain') === anvilID ? anvil : global };
      case `${environment.apiUrl}/user-domains`:
        return { status: 200, body: [{ domain: anvilID, name: 'Anvil' }] };
      case `${environment.apiUrl}/service-ledgers`:
        return { status: 200, body: ledger, headers: { 'total-count': String(ledger.length) } };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

/** The dashboard as one person sees it, and what the page asked the server for. */
interface Opened {
  fixture: ComponentFixture<DashboardComponent>;
  transport: ScriptedTransport;
}

/** Signs the person in, opens Anvil, and draws the dashboard there. */
async function openDashboard(username: string, global: PermissionDigest): Promise<Opened> {
  const transport = scriptedTransport(serverFor(username, global));
  await TestBed.configureTestingModule({
    imports: [DashboardComponent],
    providers: [
      // The console's generated client over the scripted transport, so the service card reads
      // the digest as it does in the browser; the impersonation service posts through HttpClient.
      provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
      provideHttpClient(),
      provideHttpClientTesting(),
      // The sector is the frame's: the library's tenant service, which the tenant guard writes.
      provideRouter([]),
      provideFrame({ home: 'dashboard', menus: [] }),
      // What the library's list reads the ledger's fields and keys from.
      { provide: RESOURCE_META, useValue: resourceMeta },
    ],
  }).compileComponents();

  // Signing in loads the session, the global digest, and the domains: what the login page
  // runs. Entering the one lit sector, as the tenant guard does for a page under it, loads
  // its digest.
  await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
  TestBed.inject(TenantService).enter(anvilID);
  await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest(anvilID as Domain);

  const fixture = TestBed.createComponent(DashboardComponent);
  fixture.detectChanges();
  return { fixture, transport };
}

/** The library's list the page draws the ledger with, where it draws one. */
function ledgerList(fixture: ComponentFixture<DashboardComponent>): ResourceListComponent | undefined {
  return fixture.debugElement.query(By.directive(ResourceListComponent))?.componentInstance as
    ResourceListComponent | undefined;
}

describe('DashboardComponent', () => {
  let fixture: ComponentFixture<DashboardComponent>;
  let transport: ScriptedTransport;

  beforeEach(async () => {
    ({ fixture, transport } = await openDashboard('marshal', {}));
  });

  it('renders the service card for the open sector, by its name, from its digest', () => {
    const element: HTMLElement = fixture.nativeElement;
    expect(element.querySelector('h1')?.textContent).toContain('Welcome aboard, marshal');
    expect(element.querySelector('.card-badge mat-card-title')?.textContent).toContain('Service card — Anvil');

    const rows = Array.from(element.querySelectorAll('table.badge tbody tr'));
    const missions = rows.find((row) => row.querySelector('td.label')?.textContent?.trim().startsWith('Missions'));
    const ships = rows.find((row) => row.querySelector('td.label')?.textContent?.trim().startsWith('Ships'));
    // The columns are List, Read, Create, Update, Delete.
    const pills = (row: Element | undefined): string[] =>
      Array.from(row?.querySelectorAll('td:not(.label) .pill') ?? []).map((pill) => pill.textContent?.trim() ?? '');
    expect(pills(missions)).toEqual(['granted', 'granted', 'granted', 'granted', 'granted']);
    expect(pills(ships)).toEqual(['terms apply', 'terms apply', '—', '—', '—']);
    // Each answer carries its permission's name beside it, for a page too narrow for the
    // column headings: the block the badge is drawn as there has no heading over a cell.
    const named = (row: Element | undefined): string[] =>
      Array.from(row?.querySelectorAll('td:not(.label) .permission') ?? []).map((name) => name.textContent ?? '');
    expect(named(ships)).toEqual(['List', 'Read', 'Create', 'Update', 'Delete']);
  });

  it('asked the server for the session, both digests, and the domains, and nothing it was not granted', () => {
    const asked = transport.requests.map((request) => `${request.method} ${request.url}`);
    expect(asked).toContain(`GET ${environment.apiUrl}/user/session`);
    expect(asked).toContain(`GET ${environment.apiUrl}/permission-digest`);
    expect(asked).toContain(`GET ${environment.apiUrl}/user-domains`);
    expect(asked).toContain(`GET ${environment.apiUrl}/permission-digest?domain=${anvilID}`);
    // The service ledger and the pilot cards are global lists the marshal holds no List
    // grant on: never requested, and the page draws no ledger.
    expect(asked.filter((line) => line.includes('service-ledgers') || line.includes('pilot-cards'))).toEqual([]);
    expect(ledgerList(fixture)).toBeUndefined();
    expect((fixture.nativeElement as HTMLElement).textContent).not.toContain('Service ledger');
  });
});

describe('DashboardComponent, for a person who may list the service ledger', () => {
  let fixture: ComponentFixture<DashboardComponent>;
  let transport: ScriptedTransport;

  beforeEach(async () => {
    ({ fixture, transport } = await openDashboard('governor', ledgerGranted));
    // The list's page request is a plain promise the application's stability does not track.
    for (let i = 0; i < 100 && ledgerList(fixture)?.store.pageStatus() !== 'resolved'; i++) {
      fixture.detectChanges();
      await TestBed.inject(ApplicationRef).whenStable();
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    fixture.detectChanges();
  });

  it("draws the ledger with the library's list: one row per sector, under plain headings, and no table of its own", () => {
    const element: HTMLElement = fixture.nativeElement;
    const list = element.querySelector('impulse-resource-list');
    expect(list?.querySelector('h1.title')?.textContent?.trim()).toBe('Service ledger (2)');
    expect(Array.from(list?.querySelectorAll('th .col-header') ?? []).map((h) => h.textContent?.trim())).toEqual([
      'Sector',
      'Open missions',
      'Fees outstanding',
      'Settlements',
    ]);
    expect(
      Array.from(list?.querySelectorAll('tr.impulse-row') ?? []).map((row) =>
        Array.from(row.querySelectorAll('td.data-col:not(.action-col)')).map((cell) => cell.textContent?.trim() ?? ''),
      ),
    ).toEqual([
      ['Anvil', '3', '450', '2'],
      ['Cinder', '0', '0', '5'],
    ]);
    // A row has no page of its own: nothing in the list leads into one.
    expect(list?.querySelectorAll('tr.impulse-row a')).toHaveLength(0);
    // The page draws no table of its own for the ledger: the badge is the only one it draws.
    expect(element.querySelector('table.mat-mdc-table')).toBeNull();
    expect(element.querySelectorAll('mat-card table')).toHaveLength(1);
  });

  it('asks for the ledger once, as the list does', () => {
    const asked = transport.requests.filter((request) => request.url.includes('service-ledgers'));
    expect(asked).toHaveLength(1);
    expect(asked[0]?.method).toBe('GET');
  });
});
