import { provideHttpClient } from '@angular/common/http';
import { HttpTestingController, provideHttpClientTesting } from '@angular/common/http/testing';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog, MatDialogRef } from '@angular/material/dialog';
import { provideRouter } from '@angular/router';
import { RouterTestingHarness } from '@angular/router/testing';
import { apiDescriptor, createApi, createTenantRoute } from '@app/service/zz_gen_api';
import { DomainRoutePath } from '@app/service/zz_gen_resources';
import { ClientBase, Domain, PermissionDigest, TransportRequest, TransportResponse } from '@impulseframework/resource';
import { provideFrame, tenantGuard, TenantService } from '@impulseframework/resource-angular/auth-frame';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { CreateTenantDialogComponent } from '@impulseframework/resource-angular/identity-pages';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { provideResourceTesting } from '@impulseframework/resource-angular/testing';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { environment } from '@env';
import { firstValueFrom } from 'rxjs';
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

  /** What each cell of a row says: the columns are List, Read, Create, Update, Delete, Execute. */
  function answers(row: Element | undefined): (string | null)[] {
    return Array.from(row?.querySelectorAll('td.held .pill') ?? []).map((pill) => pill.textContent);
  }

  it('renders who is signed in and the digest per resource, tenant-scoped ones in the open tenant', async () => {
    const fixture = await render(member);
    const element: HTMLElement = fixture.nativeElement;

    expect(element.querySelector('h1')?.textContent).toContain('Signed in as member');
    // The address and the look are the person's: the account page and the person's menu.
    expect(element.querySelector('impulse-email-address')).toBeNull();
    expect(element.querySelector('impulse-theme-picker')).toBeNull();
    expect(element.querySelector('p strong')?.textContent).toBe('North');
    // The card is the library's: one table, titled, with a row per resource.
    const card = element.querySelector('impulse-permissions-card');
    expect(card?.querySelector('mat-card-title')?.textContent).toBe('Your permissions');
    expect(element.querySelectorAll('table')).toHaveLength(1);
    const rows = Array.from(card?.querySelectorAll('table.permissions tbody tr') ?? []);
    const labels = rows.map((row) => row.querySelector('td.resource-name')?.textContent?.replace(/\s+/g, ' ').trim());
    expect(labels).toEqual(['Announcements North', 'Tenants global']);
    expect(answers(rows[0])).toEqual(['granted', 'granted', '—', 'terms apply', '—', '—']);
    expect(answers(rows[1])).toEqual(['granted', 'granted', '—', '—', '—', '—']);
    // Each answer names its permission beside it, for a card too narrow for the column
    // headings, and the table sits in no frame that scrolls sideways.
    expect(Array.from(rows[0]?.querySelectorAll('td.held .permission') ?? []).map((name) => name.textContent)).toEqual([
      'List',
      'Read',
      'Create',
      'Update',
      'Delete',
      'Execute',
    ]);
    expect(element.querySelector('.table-scroll')).toBeNull();

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
    // The dialog itself is the library's spec's; here only that the card opens it.
    const opened = vi.spyOn(TestBed.inject(MatDialog), 'open').mockReturnValue({} as MatDialogRef<unknown>);

    expect(element.querySelector('p strong')?.textContent).toBe('no tenant');
    expect(element.querySelectorAll('tbody tr')).toHaveLength(1);
    expect(element.querySelector('td.nothing-held')?.textContent).toContain('You hold nothing here');
    // Nothing is offered on a guess: the card waits for the server's answer.
    expect(element.querySelector('.create-tenant-card')).toBeNull();
    const asked = http.expectOne(offerUrl);
    expect(asked.request.method).toBe('GET');
    asked.flush({ offered: true });
    fixture.detectChanges();

    const offer = element.querySelector<HTMLButtonElement>('.create-tenant-card button');
    expect(offer?.textContent).toContain('Create your tenant');
    offer?.click();
    // The library's dialog: the person names their tenant and becomes its administrator.
    expect(opened).toHaveBeenCalledWith(
      CreateTenantDialogComponent,
      expect.objectContaining({ width: '480px', data: apiDescriptor }),
    );
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

describe('DashboardComponent, opened by its address under a tenant', () => {
  it("draws no card until the tenant's digest is in, and never says a person who holds something holds nothing", async () => {
    // A member who holds nothing outside the tenant: every row of theirs is the tenant's.
    // The read of the tenant's digest is answered only when the spec lets it go.
    const played = server({ ...member, global: {} });
    let release = (): void => undefined;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    const transport = scriptedTransport(async (request) => {
      if (request.url.endsWith(`/permission-digest?domain=${north}`)) {
        await held;
      }
      return played(request);
    });
    await TestBed.configureTestingModule({
      imports: [DashboardComponent],
      providers: [
        provideResourceTesting({ transport, client: (t) => createApi({ baseUrl: environment.apiUrl, transport: t }) }),
        // The dashboard under the tenant segment, behind the library's guard, as the application routes it.
        provideRouter([
          {
            path: DomainRoutePath,
            canActivate: [tenantGuard],
            children: [{ path: 'dashboard', component: DashboardComponent }],
          },
        ]),
        provideFrame({ home: 'dashboard', menus: [] }),
        provideHttpClient(),
        provideHttpClientTesting(),
      ],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    const harness = await RouterTestingHarness.create();
    const settle = async (): Promise<void> => {
      for (let turn = 0; turn < 3; turn++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
        harness.detectChanges();
      }
    };

    const landing = harness.navigateByUrl(`/${DomainRoutePath.replace(/:[^/]+/, north)}/dashboard`);
    await settle();
    // The digest is asked for and not yet answered: nothing of the page is drawn, so the
    // card's line for a person who holds nothing is not drawn either.
    expect(transport.requests.map((request) => request.url)).toContain(
      `${environment.apiUrl}/permission-digest?domain=${north}`,
    );
    expect(harness.routeNativeElement?.querySelector('impulse-permissions-card') ?? null).toBeNull();
    expect(harness.routeNativeElement?.textContent ?? '').not.toContain('You hold nothing here');

    release();
    await landing;
    harness.detectChanges();
    // The first card drawn holds the member's rows, and no line for holding nothing.
    const page = harness.routeNativeElement;
    expect(page?.querySelector('td.nothing-held') ?? null).toBeNull();
    expect(
      Array.from(page?.querySelectorAll('impulse-permissions-card table.permissions tbody tr') ?? []).map((row) =>
        row.querySelector('td.resource-name')?.textContent?.replace(/\s+/g, ' ').trim(),
      ),
    ).toEqual(['Announcements North']);
  });
});
