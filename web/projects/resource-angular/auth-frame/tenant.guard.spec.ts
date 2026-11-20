import { Component, inject } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { RedirectCommand, Router, Routes } from '@angular/router';
import { RouterTestingHarness } from '@angular/router/testing';
import { Permission, Resource, TransportResponse } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { firstValueFrom } from 'rxjs';
import { provideFrameTesting, SpecServer, specTransport } from './frame-testing';
import { tenantGuard } from './tenant.guard';
import { refusedSegment, TenantService } from './tenant.service';

// The tenant guard on the tenant segment opens the tenant the pages read: a tenant the
// session holds opens, with the tenant written once before the page loads; a tenant the
// session cannot reach is refused onto the refusal page under its own URL, and nothing is
// written; on a deep link before the session's tenant list has landed the guard asks for
// the list first. The navigation that lands settles the rest: landing outside the segment
// clears the tenant, moving to another tenant writes the new one, and a navigation out of
// the segment that a guard sends back into the same tenant clears nothing.
//
// The guard reads what the session holds in the tenant before it opens the tenant, so a
// page under the segment knows its permissions the first time it draws: it is not drawn
// while the tenant's digest is on its way, and a page open in another tenant keeps that
// tenant until the new tenant's digest is in.

@Component({ template: 'the page' })
class PageComponent {
  /** What the page could say of the tenant's users when it was made, before it drew anything. */
  readonly knewAtFirst = inject(TenantService).state('List' as Permission, 'Users' as Resource);
}

@Component({ template: 'refused' })
class RefusedComponent {}

@Component({ template: 'elsewhere' })
class ElsewhereComponent {}

/** The tenant segment as the spec's descriptor names it, the generated DomainRoutePath. */
const tenantPath = 'tenants/:tenantID';

const testRoutes: Routes = [
  { path: 'elsewhere', component: ElsewhereComponent },
  // An address outside the segment whose guard sends the browser back into acme, as the
  // application's root address sends a person to where they work.
  {
    path: 'back',
    canActivate: [(): RedirectCommand => new RedirectCommand(inject(Router).parseUrl('/tenants/acme/page'))],
    children: [],
  },
  { path: `${tenantPath}/${refusedSegment}`, component: RefusedComponent },
  {
    path: tenantPath,
    canActivate: [tenantGuard],
    children: [{ path: 'page', component: PageComponent }],
  },
];

describe('tenantGuard', () => {
  let transport: ScriptedTransport;

  const listLoads = (): number => transport.requests.filter((r) => r.url.endsWith('/user-domains')).length;

  /** Configures the app over a scripted server; with `preload`, the session's tenant list has landed. */
  const appOver = async (tenants: string[], preload: boolean, closed: string[] = []): Promise<void> => {
    transport = specTransport({
      domains: [
        ...tenants.map((domain) => ({ domain, name: domain })),
        ...closed.map((domain) => ({ domain, name: domain, closed: `${domain} admits only its own directory.` })),
      ],
    });
    TestBed.configureTestingModule({ providers: [provideFrameTesting(transport, {}, testRoutes)] });
    if (preload) {
      await firstValueFrom(TestBed.inject(AuthService).loadDomains());
    }
  };

  const cases: {
    name: string;
    tenants: string[];
    closed?: string[];
    preload: boolean;
    navigate: string[];
    want: 'page' | 'refused' | 'elsewhere';
    wantTenant: string;
    wantEntered: string[];
    wantLeft: number;
    wantListLoads: number;
  }[] = [
    {
      name: 'a tenant the session holds opens, written once before the page loads',
      tenants: ['acme'],
      preload: true,
      navigate: ['/tenants/acme/page'],
      want: 'page',
      wantTenant: 'acme',
      wantEntered: ['acme'],
      wantLeft: 0,
      wantListLoads: 1,
    },
    {
      name: 'a tenant the session cannot reach is refused under its own URL, and nothing is written',
      tenants: ['acme'],
      preload: true,
      navigate: ['/tenants/zzz/page'],
      want: 'refused',
      wantTenant: '',
      wantEntered: [],
      wantLeft: 0,
      wantListLoads: 2,
    },
    {
      name: 'a tenant that is closed to this sign-in is refused as one the session cannot reach, though the list names it',
      tenants: ['acme'],
      closed: ['zzz'],
      preload: true,
      navigate: ['/tenants/zzz/page'],
      want: 'refused',
      wantTenant: '',
      wantEntered: [],
      wantLeft: 0,
      wantListLoads: 2,
    },
    {
      name: 'a deep link before the tenant list has landed asks for the list first, then opens',
      tenants: ['acme'],
      preload: false,
      navigate: ['/tenants/acme/page'],
      want: 'page',
      wantTenant: 'acme',
      wantEntered: ['acme'],
      wantLeft: 0,
      wantListLoads: 1,
    },
    {
      name: 'leaving the tenant segment clears the tenant',
      tenants: ['acme'],
      preload: true,
      navigate: ['/tenants/acme/page', '/elsewhere'],
      want: 'elsewhere',
      wantTenant: '',
      wantEntered: ['acme'],
      wantLeft: 1,
      wantListLoads: 1,
    },
    {
      name: 'a navigation out of the segment that a guard sends back into the same tenant clears nothing',
      tenants: ['acme'],
      preload: true,
      navigate: ['/tenants/acme/page', '/back'],
      want: 'page',
      wantTenant: 'acme',
      wantEntered: ['acme'],
      wantLeft: 0,
      wantListLoads: 1,
    },
    {
      name: 'moving to another tenant writes the new one, and clears nothing in between',
      tenants: ['acme', 'beta'],
      preload: true,
      navigate: ['/tenants/acme/page', '/tenants/beta/page'],
      want: 'page',
      wantTenant: 'beta',
      wantEntered: ['acme', 'beta'],
      wantLeft: 0,
      wantListLoads: 1,
    },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      await appOver(tt.tenants, tt.preload, tt.closed);
      const tenants = TestBed.inject(TenantService);
      const entered = vi.spyOn(tenants, 'enter');
      const left = vi.spyOn(tenants, 'leave');
      const harness = await RouterTestingHarness.create();

      let activated: unknown;
      for (const url of tt.navigate) {
        activated = await harness.navigateByUrl(url);
      }

      const components = { page: PageComponent, refused: RefusedComponent, elsewhere: ElsewhereComponent };
      expect(activated).toBeInstanceOf(components[tt.want]);
      expect(tenants.current()).toBe(tt.wantTenant);
      expect(entered.mock.calls.map(([tenant]) => tenant)).toEqual(tt.wantEntered);
      expect(left).toHaveBeenCalledTimes(tt.wantLeft);
      expect(listLoads()).toBe(tt.wantListLoads);
    });
  }

  describe("reads the tenant's digest before the tenant opens", () => {
    /** The two tenants the session holds, and what it holds in each. */
    const server: SpecServer = {
      domains: [
        { domain: 'acme', name: 'acme' },
        { domain: 'beta', name: 'beta' },
      ],
      digests: { acme: { Users: { List: 'granted' } }, beta: { Users: { List: 'conditional' } } },
    };

    const digestReads = (tenant: string): number =>
      transport.requests.filter((r) => r.url.endsWith(`/permission-digest?domain=${tenant}`)).length;

    /** Everything that can run without another answer from the server runs. */
    const settle = async (harness: RouterTestingHarness): Promise<void> => {
      for (let turn = 0; turn < 3; turn++) {
        await new Promise((resolve) => setTimeout(resolve, 0));
        harness.detectChanges();
      }
    };

    /**
     * Configures the app over the server, with the session's tenant list landed. The read
     * of one tenant's digest is answered only when the spec lets it go, or answered as the
     * response given.
     */
    const appHolding = async (tenant: string, answer?: TransportResponse): Promise<() => void> => {
      const played = specTransport(server);
      let release = (): void => undefined;
      const held = new Promise<void>((resolve) => {
        release = resolve;
      });
      transport = scriptedTransport(async (request) => {
        if (request.url.endsWith(`/permission-digest?domain=${tenant}`)) {
          if (answer) {
            return answer;
          }
          await held;
        }
        return played(request);
      });
      TestBed.configureTestingModule({ providers: [provideFrameTesting(transport, {}, testRoutes)] });
      await firstValueFrom(TestBed.inject(AuthService).loadDomains());
      return release;
    };

    it('draws no page under the segment until the digest is in, and the page then knows what the session holds', async () => {
      const release = await appHolding('acme');
      const tenants = TestBed.inject(TenantService);
      const harness = await RouterTestingHarness.create();

      const landing = harness.navigateByUrl('/tenants/acme/page');
      await settle(harness);
      // The digest is asked for and not yet answered: no tenant is open, and nothing is drawn.
      expect(digestReads('acme')).toBe(1);
      expect(tenants.current()).toBe('');
      expect(harness.routeNativeElement?.textContent ?? '').toBe('');

      release();
      const page = await landing;
      expect(page).toBeInstanceOf(PageComponent);
      expect((page as PageComponent).knewAtFirst).toBe('granted');
      expect(tenants.current()).toBe('acme');
      // Entering reads nothing again: the digest the guard read is the one the page asks.
      await settle(harness);
      expect(digestReads('acme')).toBe(1);
    });

    it("keeps a page open in another tenant on that tenant until the new tenant's digest is in", async () => {
      const release = await appHolding('beta');
      const tenants = TestBed.inject(TenantService);
      const harness = await RouterTestingHarness.create();
      await harness.navigateByUrl('/tenants/acme/page');
      expect(tenants.state('List' as Permission, 'Users' as Resource)).toBe('granted');

      const landing = harness.navigateByUrl('/tenants/beta/page');
      await settle(harness);
      expect(digestReads('beta')).toBe(1);
      expect(tenants.current()).toBe('acme');
      expect(tenants.state('List' as Permission, 'Users' as Resource)).toBe('granted');

      release();
      await landing;
      expect(tenants.current()).toBe('beta');
      expect(tenants.state('List' as Permission, 'Users' as Resource)).toBe('conditional');
    });

    it('opens the page holding nothing where the digest cannot be read, and does not ask again', async () => {
      await appHolding('acme', { status: 500, body: { message: 'the digest is not to be had' } });
      const tenants = TestBed.inject(TenantService);
      const harness = await RouterTestingHarness.create();

      const page = await harness.navigateByUrl('/tenants/acme/page');

      expect(page).toBeInstanceOf(PageComponent);
      expect((page as PageComponent).knewAtFirst).toBeUndefined();
      expect(tenants.current()).toBe('acme');
      await settle(harness);
      expect(digestReads('acme')).toBe(1);
    });

    it('loads the digest of a tenant entered with none in the cache, as a spec or a reused segment enters one', async () => {
      await appHolding('beta');
      const tenants = TestBed.inject(TenantService);
      const harness = await RouterTestingHarness.create();

      tenants.enter('acme');
      await settle(harness);

      expect(digestReads('acme')).toBe(1);
      expect(tenants.state('List' as Permission, 'Users' as Resource)).toBe('granted');
    });
  });
});
