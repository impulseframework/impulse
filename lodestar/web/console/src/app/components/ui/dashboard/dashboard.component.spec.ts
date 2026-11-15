import { provideHttpClient } from '@angular/common/http';
import { provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { createApi } from '@app/service/zz_gen_api';
import { ClientBase, Domain, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { provideFrame, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { DashboardComponent } from './dashboard.component';

/** Anvil's digest for a marshal: every mission permission, ships read under terms. */
const anvil: PermissionDigest = {
  Missions: { List: 'granted', Read: 'granted', Create: 'granted', Update: 'granted', Delete: 'granted' },
  Ships: { List: 'conditional', Read: 'conditional' },
};

/**
 * The server as the spec plays it: the session, the global digest (empty: a marshal holds
 * nothing outside a sector), the user's domains with their names, and the digest of the
 * sector asked for. Anything else is a 404 the request log would show.
 */
function server(request: TransportRequest): TransportResponse {
  const url = new URL(request.url, 'http://lodestar.test');
  switch (url.pathname) {
    case `${environment.apiUrl}/user/session`:
      return {
        status: 200,
        body: { authenticated: true, username: 'marshal', email: 'marshal@lodestar.test', emailVerified: true },
      };
    case `${environment.apiUrl}/permission-digest`:
      return { status: 200, body: url.searchParams.get('domain') === 'anvil' ? anvil : {} };
    case `${environment.apiUrl}/user-domains`:
      return { status: 200, body: [{ domain: 'anvil', name: 'Anvil' }] };
    default:
      return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
  }
}

describe('DashboardComponent', () => {
  let fixture: ComponentFixture<DashboardComponent>;
  let transport: ScriptedTransport;

  beforeEach(async () => {
    transport = scriptedTransport(server);
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
      ],
    }).compileComponents();

    // Signing in loads the session, the global digest, and the domains: what the login page
    // runs. Entering the one lit sector, as the tenant guard does for a page under it, loads
    // its digest.
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    TestBed.inject(TenantService).enter('anvil');
    await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest('anvil' as Domain);

    fixture = TestBed.createComponent(DashboardComponent);
    fixture.detectChanges();
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
  });

  it('asked the server for the session, both digests, and the domains, and nothing it was not granted', () => {
    const asked = transport.requests.map((request) => `${request.method} ${request.url}`);
    expect(asked).toContain(`GET ${environment.apiUrl}/user/session`);
    expect(asked).toContain(`GET ${environment.apiUrl}/permission-digest`);
    expect(asked).toContain(`GET ${environment.apiUrl}/user-domains`);
    expect(asked).toContain(`GET ${environment.apiUrl}/permission-digest?domain=anvil`);
    // The service ledger and the pilot cards are global lists the marshal holds no List
    // grant on: never requested.
    expect(asked.filter((line) => line.includes('service-ledgers') || line.includes('pilot-cards'))).toEqual([]);
  });
});
