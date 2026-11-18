import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { createApi, createTenantRoute } from '@app/service/zz_gen_api';
import { ClientBase, Domain, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { provideFrame, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
import { NewTenantDialogComponent } from '../new-tenant/new-tenant-dialog.component';
import { DashboardComponent } from './dashboard.component';

/** The tenant the session holds: its key is an id, and the page shows its name. */
const north = '11111111-1111-4111-8111-111111111111';

/** What the server answers for one session: its tenants, the global digest, and each tenant's digest. */
interface Grants {
  domains: { domain: string; name: string }[];
  global: PermissionDigest;
  tenants: Record<string, PermissionDigest>;
}

/**
 * A member of the north tenant: Announcements are north's to list and read, and to update
 * under a condition; Tenants, a global resource, may be listed and read.
 */
const member: Grants = {
  domains: [{ domain: north, name: 'North' }],
  global: { Tenants: { List: 'granted', Read: 'granted' } },
  tenants: { [north]: { Announcements: { List: 'granted', Read: 'granted', Update: 'conditional' } } },
};

/** A login that holds nothing anywhere. */
const stranger: Grants = { domains: [], global: {}, tenants: {} };

/** Where the page asks whether the person may create a tenant of their own now. */
const offerUrl = `${environment.apiUrl}/${createTenantRoute}`;

/**
 * The server as the spec plays it: the session, the digest of the tenant asked for (the
 * global one when none is), the user's domains and the enabled feature flags (none here).
 * Anything else is a 404 the request log would show. Whether a tenant can be created is
 * asked through HttpClient, which the testing controller answers in each spec.
 */
function server(grants: Grants): (request: TransportRequest) => TransportResponse {
  return (request) => {
    const url = new URL(request.url, 'http://skeleton.test');
    switch (url.pathname) {
      case `${environment.apiUrl}/user/session`:
        return {
          status: 200,
          body: { authenticated: true, username: 'member', email: 'member@skeleton.test', emailVerified: true },
        };
      case `${environment.apiUrl}/permission-digest`: {
        const domain = url.searchParams.get('domain');
        return { status: 200, body: domain ? (grants.tenants[domain] ?? {}) : grants.global };
      }
      case `${environment.apiUrl}/user-domains`:
        return { status: 200, body: grants.domains };
      case `${environment.apiUrl}/features`:
        return { status: 200, body: { enabled: [] } };
      default:
        return { status: 404, body: { message: `unscripted ${request.method} ${request.url}` } };
    }
  };
}

describe('DashboardComponent', () => {
  let transport: ScriptedTransport;
  let http: HttpTestingController;

  /**
   * Signs in against the scripted server and renders the dashboard. The client is the
   * application's generated one over the scripted transport, so the component reads the
   * digests exactly as it does in the browser: checkUserSession is what a login runs once
   * it is accepted (the session, the global digest, the domains, the enabled feature flags),
   * and entering a tenant loads its digest, here loaded ahead so the card renders from it
   * at once. The tenant is entered as the tenant guard does for a page under it; a session
   * with no tenant renders the dashboard outside one, and its question of whether a tenant
   * can be created is left for the spec to answer.
   */
  async function render(grants: Grants): Promise<ComponentFixture<DashboardComponent>> {
    transport = scriptedTransport(server(grants));
    await TestBed.configureTestingModule({
      imports: [DashboardComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        provideFrame({ home: 'dashboard', menus: [] }),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    http = TestBed.inject(HttpTestingController);
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    for (const { domain } of grants.domains) {
      await TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.loadDigest(domain as Domain);
    }
    const tenant = grants.domains[0]?.domain;
    if (tenant) {
      TestBed.inject(TenantService).enter(tenant);
    }
    const fixture = TestBed.createComponent(DashboardComponent);
    fixture.detectChanges();
    return fixture;
  }

  afterEach(() => http.verify());

  /** The state class of each cell in a row: the columns are List, Read, Create, Update, Delete, Execute. */
  function states(row: Element | undefined): (string | undefined)[] {
    return Array.from(row?.querySelectorAll('td.state') ?? []).map((cell) =>
      Array.from(cell.classList).find((name) => name !== 'state'),
    );
  }

  it('renders who is signed in and the digest per resource, tenant-scoped ones in the open tenant', async () => {
    const fixture = await render(member);
    const element: HTMLElement = fixture.nativeElement;

    expect(element.querySelector('h1')?.textContent).toContain('Signed in as member');
    // The address and the look are the person's: the account page and the person's menu.
    expect(element.querySelector('impulse-email-address')).toBeNull();
    expect(element.querySelector('impulse-theme-picker')).toBeNull();
    expect(element.querySelector('p strong')?.textContent).toBe('North');
    const rows = Array.from(element.querySelectorAll('tbody tr'));
    const labels = rows.map((row) => row.querySelector('td')?.textContent?.replace(/\s+/g, ' ').trim());
    expect(labels).toEqual(['Announcements North', 'Tenants global']);
    expect(states(rows[0])).toEqual(['granted', 'granted', 'absent', 'conditional', 'absent', 'absent']);
    expect(states(rows[1])).toEqual(['granted', 'granted', 'absent', 'absent', 'absent', 'absent']);

    const asked = transport.requests.map((request) => `${request.method} ${request.url}`);
    expect(asked.slice(0, 5)).toEqual([
      `GET ${environment.apiUrl}/user/session`,
      `GET ${environment.apiUrl}/permission-digest`,
      `GET ${environment.apiUrl}/user-domains`,
      `GET ${environment.apiUrl}/features`,
      `GET ${environment.apiUrl}/permission-digest?domain=${north}`,
    ]);
  });

  it('says so when the session holds nothing, and offers to create their own tenant once the server says one can be created', async () => {
    const fixture = await render(stranger);
    const element: HTMLElement = fixture.nativeElement;
    // The dialog itself is the new-tenant spec's; here only that the card opens it.
    const opened = vi.spyOn(TestBed.inject(MatDialog), 'open').mockReturnValue({} as MatDialogRef<unknown>);

    expect(element.querySelector('p strong')?.textContent).toBe('no tenant');
    expect(element.querySelectorAll('tbody tr')).toHaveLength(1);
    expect(element.querySelector('td.empty')?.textContent).toContain('You hold nothing here');
    // Nothing is offered on a guess: the card waits for the server's answer.
    expect(element.querySelector('.create-tenant-card')).toBeNull();
    const asked = http.expectOne(offerUrl);
    expect(asked.request.method).toBe('GET');
    asked.flush({ offered: true });
    fixture.detectChanges();

    const offer = element.querySelector<HTMLButtonElement>('.create-tenant-card button');
    expect(offer?.textContent).toContain('Create your tenant');
    offer?.click();
    // The new-tenant dialog: the person names their tenant and becomes its administrator.
    expect(opened).toHaveBeenCalledWith(NewTenantDialogComponent, expect.objectContaining({ width: '480px' }));
  });

  it('offers no tenant of their own while the server would refuse to create one', async () => {
    // Every sign-in method is invite only: the server says no tenant can be created now.
    const fixture = await render(stranger);
    http.expectOne(offerUrl).flush({ offered: false });
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).querySelector('.create-tenant-card')).toBeNull();
  });

  it('offers no tenant of their own where the server does not answer', async () => {
    const fixture = await render(stranger);
    http.expectOne(offerUrl).flush({ message: 'unavailable' }, { status: 503, statusText: 'Service Unavailable' });
    fixture.detectChanges();
    expect((fixture.nativeElement as HTMLElement).querySelector('.create-tenant-card')).toBeNull();
  });

  it('offers no tenant of their own to a session holding a tenant, and does not ask the server', async () => {
    const fixture = await render(member);
    http.expectNone(offerUrl);
    expect((fixture.nativeElement as HTMLElement).querySelector('.create-tenant-card')).toBeNull();
  });
});
