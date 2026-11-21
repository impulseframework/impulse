import { Location } from '@angular/common';
import { Component, effect, inject } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { RedirectCommand, Router, Routes } from '@angular/router';
import { RouterTestingHarness } from '@angular/router/testing';
import {
  ApiError,
  ClientBase,
  Domain,
  Permission,
  PermissionDigest,
  Resource,
  TransportResponse,
} from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';
import { firstValueFrom } from 'rxjs';
import { provideFrameTesting, SpecServer, specTransport } from './frame-testing';
import { TenantRefusedComponent } from './tenant-refused.component';
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
// tenant until the new tenant's digest is in. A digest that could not be read opens
// nothing: the refusal page says the page could not be loaded and offers to try again, the
// tenant is not entered, and the page that was asked for is not drawn as a page of a
// person who holds nothing. A digest the server refuses to read (its 403 or 404) is the
// refusal itself: 404 Not Found and nothing more.
//
// A tenant that has left the person's list since the list was read (a seat taken away
// while the person is signed in) is the same refusal. The server answers its digest with
// no entries, as it does for a tenant where the person holds nothing, so the client reads
// the list again for an empty digest: a tenant the list names no more says 404 Not Found,
// and one it still names opens as a tenant where the person holds nothing.
//
// The same holds for a read that fails once the tenant is open, whoever asked for it: the
// read on entering a tenant with no digest in the cache, and the reload of every digest
// (AuthService.refreshPermissions). The page is never drawn again as the page of a person
// who holds nothing, and the refusal page takes its place.

@Component({ template: 'the page' })
class PageComponent {
  private tenants = inject(TenantService);

  /** What the page could say of the tenant's users when it was made, before it drew anything. */
  readonly knewAtFirst = this.tenants.state('List' as Permission, 'Users' as Resource);

  /** What the page could say of the tenant's users, each time that changed while its tenant was open. */
  readonly said: string[] = [];

  constructor() {
    effect(() => {
      if (!this.tenants.current()) {
        return;
      }
      const now = this.tenants.state('List' as Permission, 'Users' as Resource) ?? 'nothing';
      if (now !== this.said.at(-1)) {
        this.said.push(now);
      }
    });
  }
}

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
  { path: `${tenantPath}/${refusedSegment}`, component: TenantRefusedComponent },
  {
    path: tenantPath,
    canActivate: [tenantGuard],
    children: [{ path: 'page', component: PageComponent }],
  },
];

/** The same segment with no guard on it: a tenant is entered by the navigation that lands, with no digest read. */
const unguardedRoutes: Routes = [
  { path: `${tenantPath}/${refusedSegment}`, component: TenantRefusedComponent },
  { path: tenantPath, children: [{ path: 'page', component: PageComponent }] },
];

describe('tenantGuard', () => {
  let transport: ScriptedTransport;

  const listLoads = (): number => transport.requests.filter((r) => r.url.endsWith('/user-domains')).length;

  /**
   * Configures the app over a scripted server, where the session holds something in each
   * of its tenants; with `preload`, the session's tenant list has landed.
   */
  const appOver = async (tenants: string[], preload: boolean, closed: string[] = []): Promise<void> => {
    transport = specTransport({
      domains: [
        ...tenants.map((domain) => ({ domain, name: domain })),
        ...closed.map((domain) => ({ domain, name: domain, closed: `${domain} admits only its own directory.` })),
      ],
      digests: Object.fromEntries(tenants.map((domain) => [domain, { Users: { List: 'granted' } }])),
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

      const components = { page: PageComponent, refused: TenantRefusedComponent, elsewhere: ElsewhereComponent };
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
     * of one tenant's digest is answered only when the spec lets it go, or, with `answer`,
     * as that says each time it is asked: a response, or nothing for the server's own.
     */
    const appHolding = async (
      tenant: string,
      answer?: () => TransportResponse | undefined,
      routes: Routes = testRoutes,
    ): Promise<() => void> => {
      const played = specTransport(server);
      let release = (): void => undefined;
      const held = new Promise<void>((resolve) => {
        release = resolve;
      });
      transport = scriptedTransport(async (request) => {
        if (request.url.endsWith(`/permission-digest?domain=${tenant}`)) {
          if (answer) {
            return answer() ?? played(request);
          }
          await held;
        }
        return played(request);
      });
      TestBed.configureTestingModule({ providers: [provideFrameTesting(transport, {}, routes)] });
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

    /** What the page in the outlet shows, as a person reads it. */
    const shown = (harness: RouterTestingHarness): string =>
      ((harness.routeNativeElement as HTMLElement).textContent ?? '').replace(/\s+/g, ' ').trim();

    const tryAgain = async (harness: RouterTestingHarness): Promise<void> => {
      (harness.routeNativeElement as HTMLElement).querySelector<HTMLButtonElement>('button.try-again')!.click();
      await settle(harness);
    };

    /** What the server answers for a digest it will not read, and for one it cannot. */
    const refusing = (status: number): TransportResponse => ({
      status,
      body: { message: 'the digest is not to be had' },
    });

    describe('where the tenant list cannot be read', () => {
      /**
       * Configures the app over the server, whose list of tenants fails while `now.down`;
       * with `preload`, the session's tenant list has landed before it went down.
       */
      const appListing = async (now: { down: boolean }, preload: boolean): Promise<void> => {
        const played = specTransport(server);
        transport = scriptedTransport((request) =>
          now.down && request.url.endsWith('/user-domains')
            ? { status: 500, body: { message: 'the list is not to be had' } }
            : played(request),
        );
        TestBed.configureTestingModule({ providers: [provideFrameTesting(transport, {}, testRoutes)] });
        if (preload) {
          const down = now.down;
          now.down = false;
          await firstValueFrom(TestBed.inject(AuthService).loadDomains());
          now.down = down;
        }
      };

      it('says the page could not be loaded, never 404 Not Found, for a tenant the person is in, and opens it on Try again', async () => {
        const now = { down: true };
        await appListing(now, false);
        const tenants = TestBed.inject(TenantService);
        const entered = vi.spyOn(tenants, 'enter');
        const harness = await RouterTestingHarness.create();

        const landed = await harness.navigateByUrl('/tenants/acme/page');
        harness.detectChanges();

        // Nothing says whether the person is in the tenant: it is not refused, and not opened.
        expect(landed).toBeInstanceOf(TenantRefusedComponent);
        expect(shown(harness)).toBe('This page could not be loaded Try again');
        expect(entered).not.toHaveBeenCalled();
        expect(tenants.current()).toBe('');
        expect(tenants.unread()).toBe('/tenants/acme/page');
        expect(TestBed.inject(Location).path()).not.toContain(refusedSegment);
        // The tenant's digest is not asked for: the list comes first.
        expect(digestReads('acme')).toBe(0);
        expect(shown(harness)).not.toContain('not to be had');

        // A second failure leaves the sentence.
        await tryAgain(harness);
        expect(shown(harness)).toBe('This page could not be loaded Try again');

        now.down = false;
        await tryAgain(harness);
        expect(shown(harness)).toBe('the page');
        expect(tenants.current()).toBe('acme');
        expect(tenants.unread()).toBe('');
      });

      it('says the page could not be loaded for a tenant the list it holds does not name, where the list cannot be read again', async () => {
        const now = { down: true };
        await appListing(now, true);
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();
        expect(tenants.tenants()).toEqual(['acme', 'beta']);

        // A seat granted since sign-in is not on the list the session holds, so the list is
        // read again; a read that fails does not say the person is not in the tenant.
        await harness.navigateByUrl('/tenants/gamma/page');
        harness.detectChanges();
        expect(shown(harness)).toBe('This page could not be loaded Try again');
        expect(tenants.unread()).toBe('/tenants/gamma/page');
        expect(tenants.tenants()).toEqual(['acme', 'beta']);

        // The list answers, and does not name the tenant: that is the refusal.
        now.down = false;
        await tryAgain(harness);
        expect(shown(harness)).toBe('404 Not Found');
        expect(tenants.unread()).toBe('');
      });
    });

    describe('where the digest cannot be read', () => {
      it('says the page could not be loaded, with a way to try again, and nothing else', async () => {
        await appHolding('acme', () => ({ status: 500, body: { message: 'the digest is not to be had' } }));
        const tenants = TestBed.inject(TenantService);
        const entered = vi.spyOn(tenants, 'enter');
        const harness = await RouterTestingHarness.create();

        const landed = await harness.navigateByUrl('/tenants/acme/page');
        harness.detectChanges();

        // No page of the tenant is drawn and the tenant is not open: the person is not told
        // they hold nothing, and not told the tenant does not exist.
        expect(landed).toBeInstanceOf(TenantRefusedComponent);
        expect(shown(harness)).toBe('This page could not be loaded Try again');
        expect(entered).not.toHaveBeenCalled();
        expect(tenants.current()).toBe('');
        expect(tenants.unread()).toBe('/tenants/acme/page');
        // The address is not moved to the refusal page, so a reload asks for the page again.
        expect(TestBed.inject(Location).path()).not.toContain(refusedSegment);
        // The server's own words are shown nowhere.
        expect(shown(harness)).not.toContain('not to be had');
      });

      it('asks again on Try again: a second failure leaves the sentence, and an answer opens the page', async () => {
        let down = true;
        await appHolding('acme', () =>
          down ? { status: 500, body: { message: 'the digest is not to be had' } } : undefined,
        );
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();
        await harness.navigateByUrl('/tenants/acme/page');
        harness.detectChanges();
        expect(digestReads('acme')).toBe(1);

        await tryAgain(harness);
        expect(digestReads('acme')).toBe(2);
        expect(shown(harness)).toBe('This page could not be loaded Try again');
        expect(tenants.current()).toBe('');

        down = false;
        await tryAgain(harness);
        expect(digestReads('acme')).toBe(3);
        expect(shown(harness)).toBe('the page');
        expect((harness.routeDebugElement?.componentInstance as PageComponent).knewAtFirst).toBe('granted');
        expect(tenants.current()).toBe('acme');
        expect(tenants.unread()).toBe('');
      });

      it('says 404 Not Found on Try again once the server refuses the read, and offers nothing more to try', async () => {
        let status = 500;
        await appHolding('acme', () => refusing(status));
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();
        await harness.navigateByUrl('/tenants/acme/page');
        harness.detectChanges();
        expect(shown(harness)).toBe('This page could not be loaded Try again');

        // The refusal lands on the refusal page that is already on the screen: the same
        // page changes what it says, and the address moves to the refusal's.
        status = 403;
        await tryAgain(harness);
        expect(shown(harness)).toBe('404 Not Found');
        expect((harness.routeNativeElement as HTMLElement).querySelector('button')).toBeNull();
        expect(tenants.unread()).toBe('');
        expect(TestBed.inject(Location).path()).toBe(`/tenants/acme/${refusedSegment}`);
      });

      it('says the page could not be loaded when a tenant the server refused is asked for again and the read fails', async () => {
        let status = 403;
        await appHolding('acme', () => (status ? refusing(status) : undefined));
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();
        await harness.navigateByUrl('/tenants/acme/page');
        harness.detectChanges();
        expect(shown(harness)).toBe('404 Not Found');

        // The failure lands on the refusal page that is already on the screen.
        status = 500;
        await harness.navigateByUrl('/tenants/acme/page');
        harness.detectChanges();
        expect(shown(harness)).toBe('This page could not be loaded Try again');
        expect(tenants.unread()).toBe('/tenants/acme/page');

        status = 0;
        await tryAgain(harness);
        expect(shown(harness)).toBe('the page');
        expect(tenants.current()).toBe('acme');
        expect(tenants.unread()).toBe('');
      });

      for (const status of [403, 404]) {
        it(`says 404 Not Found and nothing else where the server refuses the read with a ${status}`, async () => {
          await appHolding('acme', () => refusing(status));
          const tenants = TestBed.inject(TenantService);
          const entered = vi.spyOn(tenants, 'enter');
          const harness = await RouterTestingHarness.create();

          const landed = await harness.navigateByUrl('/tenants/acme/page');
          harness.detectChanges();

          // The refusal, not a read that failed: nothing to try again, none of the
          // server's words, and the tenant is not open.
          expect(landed).toBeInstanceOf(TenantRefusedComponent);
          expect(shown(harness)).toBe('404 Not Found');
          expect((harness.routeNativeElement as HTMLElement).querySelector('button')).toBeNull();
          expect(entered).not.toHaveBeenCalled();
          expect(tenants.current()).toBe('');
          expect(tenants.unread()).toBe('');
          // The address is the refusal page's, as for a tenant the session cannot reach.
          expect(TestBed.inject(Location).path()).toBe(`/tenants/acme/${refusedSegment}`);
          expect(digestReads('acme')).toBe(1);
        });
      }

      it('says 404 Not Found, and offers nothing to try, for a tenant the session cannot reach', async () => {
        await appHolding('acme', () => ({ status: 500, body: { message: 'the digest is not to be had' } }));
        const harness = await RouterTestingHarness.create();
        await harness.navigateByUrl('/tenants/acme/page');

        // A refusal that lands after a failed read is the refusal, not the failed read.
        await harness.navigateByUrl('/tenants/zzz/page');
        harness.detectChanges();

        expect(shown(harness)).toBe('404 Not Found');
        expect(TestBed.inject(TenantService).unread()).toBe('');
      });
    });

    describe('where a read fails while the tenant is open', () => {
      const users = { resource: 'Users' as Resource, permission: 'List' as Permission };

      /** What the client's cache answers of a tenant's users, whichever tenant is open. */
      const cached = (tenant: string): string | undefined =>
        TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.state({ ...users, domain: tenant as Domain });

      const refresh = async (harness: RouterTestingHarness): Promise<void> => {
        await firstValueFrom(TestBed.inject(AuthService).refreshPermissions());
        await settle(harness);
      };

      it('says the page could not be loaded when the digest of a tenant entered with none in the cache cannot be read', async () => {
        let down = true;
        await appHolding('acme', () => (down ? refusing(500) : undefined), unguardedRoutes);
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();

        await harness.navigateByUrl('/tenants/acme/page');
        await settle(harness);

        // Not the page of a person who holds nothing: the page is gone, the tenant is not
        // open, and the cache holds no answer for it.
        expect(shown(harness)).toBe('This page could not be loaded Try again');
        expect(tenants.current()).toBe('');
        expect(tenants.unread()).toBe('/tenants/acme/page');
        expect(TestBed.inject<ClientBase>(RESOURCE_CLIENT).permissions.hasDigest('acme' as Domain)).toBe(false);
        expect(TestBed.inject(Location).path()).toBe('/tenants/acme/page');

        // Trying again enters the tenant again, which reads again.
        down = false;
        await tryAgain(harness);
        expect(digestReads('acme')).toBe(2);
        expect(shown(harness)).toBe('the page');
        expect(tenants.state(users.permission, users.resource)).toBe('granted');
        expect(tenants.unread()).toBe('');
      });

      it('says 404 Not Found where the server refuses the digest of a tenant entered with none in the cache', async () => {
        await appHolding('acme', () => refusing(403), unguardedRoutes);
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();

        await harness.navigateByUrl('/tenants/acme/page');
        await settle(harness);

        expect(shown(harness)).toBe('404 Not Found');
        expect(tenants.current()).toBe('');
        expect(tenants.unread()).toBe('');
        expect(TestBed.inject(Location).path()).toBe(`/tenants/acme/${refusedSegment}`);
      });

      it('says the page could not be loaded when a refresh cannot read the open tenant, never that the person holds nothing', async () => {
        let down = false;
        await appHolding('acme', () => (down ? refusing(500) : undefined));
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();
        const page = (await harness.navigateByUrl('/tenants/acme/page')) as PageComponent;
        await settle(harness);
        expect(page.said).toEqual(['granted']);

        down = true;
        await refresh(harness);

        // The page was never drawn again as holding nothing: the client kept what it knew.
        expect(page.said).toEqual(['granted']);
        expect(cached('acme')).toBe('granted');
        // The refusal page stands in for it, under the page's own address.
        expect(shown(harness)).toBe('This page could not be loaded Try again');
        expect(tenants.current()).toBe('');
        expect(tenants.unread()).toBe('/tenants/acme/page');
        expect(TestBed.inject(Location).path()).toBe('/tenants/acme/page');
        expect(shown(harness)).not.toContain('not to be had');

        // Trying again asks through the guard: a second failure leaves the sentence, an
        // answer opens the page.
        await tryAgain(harness);
        expect(shown(harness)).toBe('This page could not be loaded Try again');
        down = false;
        await tryAgain(harness);
        expect(shown(harness)).toBe('the page');
        expect((harness.routeDebugElement?.componentInstance as PageComponent).said).toEqual(['granted']);
        expect(tenants.current()).toBe('acme');
      });

      for (const status of [403, 404]) {
        it(`says 404 Not Found and nothing else when a refresh is refused the open tenant's digest with a ${status}`, async () => {
          let refused = false;
          await appHolding('acme', () => (refused ? refusing(status) : undefined));
          const tenants = TestBed.inject(TenantService);
          const harness = await RouterTestingHarness.create();
          await harness.navigateByUrl('/elsewhere');
          const page = (await harness.navigateByUrl('/tenants/acme/page')) as PageComponent;
          await settle(harness);

          refused = true;
          await refresh(harness);

          // The page was never drawn again, though what the person held in the tenant is forgotten.
          expect(page.said).toEqual(['granted']);
          expect(TestBed.inject(AuthService).hasDigest('acme' as Domain)).toBe(false);
          expect(shown(harness)).toBe('404 Not Found');
          expect((harness.routeNativeElement as HTMLElement).querySelector('button')).toBeNull();
          expect(tenants.current()).toBe('');
          expect(tenants.unread()).toBe('');
          // The refusal page takes the refused page's place in the history: going back
          // leaves the tenant, and does not ask for the refused page again.
          const location = TestBed.inject(Location);
          expect(location.path()).toBe(`/tenants/acme/${refusedSegment}`);
          location.back();
          await settle(harness);
          expect(location.path()).toBe('/elsewhere');
        });
      }

      it('leaves the open page as it is when a refresh cannot read another tenant, or what the person holds outside every tenant', async () => {
        let down = false;
        const played = specTransport(server);
        transport = scriptedTransport((request) => {
          const url = new URL(request.url, 'http://spec.test');
          const elsewhere = url.pathname === '/api/permission-digest' && url.searchParams.get('domain') !== 'acme';
          return down && elsewhere ? refusing(500) : played(request);
        });
        TestBed.configureTestingModule({ providers: [provideFrameTesting(transport, {}, testRoutes)] });
        const auth = TestBed.inject(AuthService);
        await firstValueFrom(auth.loadDomains());
        await firstValueFrom(auth.loadDigest());
        await firstValueFrom(auth.loadDigest('beta' as Domain));
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();
        const page = (await harness.navigateByUrl('/tenants/acme/page')) as PageComponent;
        await settle(harness);

        down = true;
        await refresh(harness);

        expect(shown(harness)).toBe('the page');
        expect(page.said).toEqual(['granted']);
        expect(tenants.current()).toBe('acme');
        expect(tenants.unread()).toBe('');
        // The tenant that could not be read keeps its last answer too.
        expect(cached('beta')).toBe('conditional');
      });
    });

    describe('where the digest reads empty', () => {
      /** What the server answers now: the tenants the list names, what the session holds in acme, whether the list can be read. */
      interface Now {
        listed: string[];
        acme: PermissionDigest;
        listDown?: boolean;
      }

      /** The server once the person's seat in acme is taken away: the list names it no more, and its digest reads empty. */
      const seatTaken = (now: Now): void => {
        now.listed = now.listed.filter((tenant) => tenant !== 'acme');
        now.acme = {};
      };

      /** Configures the app over a server that answers as `now` says each time it is asked, with the tenant list landed. */
      const appServing = async (now: Now): Promise<void> => {
        const played = specTransport(server);
        transport = scriptedTransport((request) => {
          const url = new URL(request.url, 'http://spec.test');
          if (url.pathname === '/api/user-domains') {
            return now.listDown
              ? refusing(500)
              : { status: 200, body: now.listed.map((domain) => ({ domain, name: domain })) };
          }
          if (url.pathname === '/api/permission-digest' && url.searchParams.get('domain') === 'acme') {
            return { status: 200, body: now.acme };
          }
          return played(request);
        });
        TestBed.configureTestingModule({ providers: [provideFrameTesting(transport, {}, testRoutes)] });
        await firstValueFrom(TestBed.inject(AuthService).loadDomains());
      };

      const held: PermissionDigest = { Users: { List: 'granted' } };

      it('says 404 Not Found on entering a tenant that has left the list since the list was read', async () => {
        const now: Now = { listed: ['acme', 'beta'], acme: held };
        await appServing(now);
        const tenants = TestBed.inject(TenantService);
        const entered = vi.spyOn(tenants, 'enter');
        const harness = await RouterTestingHarness.create();

        seatTaken(now);
        const landed = await harness.navigateByUrl('/tenants/acme/page');
        harness.detectChanges();

        // The refusal, as for a tenant the session never reached: not a page of the tenant
        // drawn for a person who holds nothing, and nothing to try again.
        expect(landed).toBeInstanceOf(TenantRefusedComponent);
        expect(shown(harness)).toBe('404 Not Found');
        expect((harness.routeNativeElement as HTMLElement).querySelector('button')).toBeNull();
        expect(entered).not.toHaveBeenCalled();
        expect(tenants.current()).toBe('');
        expect(tenants.unread()).toBe('');
        expect(TestBed.inject(Location).path()).toBe(`/tenants/acme/${refusedSegment}`);
        // The list that was read again is the one every picker now shows.
        expect(tenants.tenants()).toEqual(['beta']);
      });

      it('says 404 Not Found when a refresh finds the open tenant has left the list, and never draws its page as holding nothing', async () => {
        const now: Now = { listed: ['acme', 'beta'], acme: held };
        await appServing(now);
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();
        await harness.navigateByUrl('/elsewhere');
        const page = (await harness.navigateByUrl('/tenants/acme/page')) as PageComponent;
        await settle(harness);
        expect(page.said).toEqual(['granted']);

        seatTaken(now);
        await firstValueFrom(TestBed.inject(AuthService).refreshPermissions());
        await settle(harness);

        // The page was never drawn again, though what the person held in the tenant is forgotten.
        expect(page.said).toEqual(['granted']);
        expect(TestBed.inject(AuthService).hasDigest('acme' as Domain)).toBe(false);
        expect(shown(harness)).toBe('404 Not Found');
        expect((harness.routeNativeElement as HTMLElement).querySelector('button')).toBeNull();
        expect(tenants.current()).toBe('');
        expect(tenants.unread()).toBe('');
        // The refusal page takes the page's place in the history, as for a read the server refuses.
        const location = TestBed.inject(Location);
        expect(location.path()).toBe(`/tenants/acme/${refusedSegment}`);
        location.back();
        await settle(harness);
        expect(location.path()).toBe('/elsewhere');
      });

      /** What a page's own request under acme ended as: the status of the server's refusal. */
      const asked = async (path: string): Promise<number | undefined> => {
        try {
          await TestBed.inject(RESOURCE_CLIENT).request('GET', path);
          return undefined;
        } catch (error) {
          return error instanceof ApiError ? error.status : undefined;
        }
      };

      it("says 404 Not Found when the open tenant's own request is answered 404 and the tenant has left the list", async () => {
        const now: Now = { listed: ['acme', 'beta'], acme: held };
        await appServing(now);
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();
        await harness.navigateByUrl('/elsewhere');
        const page = (await harness.navigateByUrl('/tenants/acme/page')) as PageComponent;
        await settle(harness);
        const digestsBefore = digestReads('acme');
        const listsBefore = listLoads();

        // The seat is taken away while the page is open, and the person moves inside the
        // tenant: no guard runs, and the first to hear is the page's own request, which the
        // server answers 404 for a tenant the session does not reach.
        seatTaken(now);
        expect(await asked('tenants/acme/users')).toBe(404);
        await settle(harness);

        // One read of the digest and one of the list said the tenant is gone.
        expect(digestReads('acme') - digestsBefore).toBe(1);
        expect(listLoads() - listsBefore).toBe(1);
        expect(page.said).toEqual(['granted']);
        expect(TestBed.inject(AuthService).hasDigest('acme' as Domain)).toBe(false);
        expect(shown(harness)).toBe('404 Not Found');
        expect((harness.routeNativeElement as HTMLElement).querySelector('button')).toBeNull();
        expect(tenants.current()).toBe('');
        expect(tenants.unread()).toBe('');
        const location = TestBed.inject(Location);
        expect(location.path()).toBe(`/tenants/acme/${refusedSegment}`);
        location.back();
        await settle(harness);
        expect(location.path()).toBe('/elsewhere');
      });

      it("keeps the open page when the tenant's own request is answered 404 and the person is still in the tenant", async () => {
        const now: Now = { listed: ['acme', 'beta'], acme: held };
        await appServing(now);
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();
        const page = (await harness.navigateByUrl('/tenants/acme/page')) as PageComponent;
        await settle(harness);
        const digestsBefore = digestReads('acme');

        // A record that is not there: the same 404, in a tenant the person is still in.
        expect(await asked('tenants/acme/users/u404')).toBe(404);
        await settle(harness);

        expect(digestReads('acme') - digestsBefore).toBe(1);
        expect(shown(harness)).toBe('the page');
        expect(page.said).toEqual(['granted']);
        expect(tenants.current()).toBe('acme');
        expect(TestBed.inject(Location).path()).toBe('/tenants/acme/page');
      });

      it('opens a tenant the list still names as one where the person holds nothing, once the list has been read again', async () => {
        const now: Now = { listed: ['acme', 'beta'], acme: {} };
        await appServing(now);
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();

        const page = await harness.navigateByUrl('/tenants/acme/page');

        expect(page).toBeInstanceOf(PageComponent);
        expect((page as PageComponent).knewAtFirst).toBeUndefined();
        expect(tenants.current()).toBe('acme');
        // Once when the app was configured, and once for the empty digest.
        expect(listLoads()).toBe(2);
      });

      it('keeps the open page, drawn as holding nothing, when a refresh reads its digest empty and the list still names the tenant', async () => {
        const now: Now = { listed: ['acme', 'beta'], acme: held };
        await appServing(now);
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();
        const page = (await harness.navigateByUrl('/tenants/acme/page')) as PageComponent;
        await settle(harness);

        now.acme = {};
        await firstValueFrom(TestBed.inject(AuthService).refreshPermissions());
        await settle(harness);

        expect(shown(harness)).toBe('the page');
        expect(page.said).toEqual(['granted', 'nothing']);
        expect(tenants.current()).toBe('acme');
        expect(tenants.unread()).toBe('');
      });

      it('says the page could not be loaded where the list cannot be read again: nothing says whether the tenant is still there', async () => {
        const now: Now = { listed: ['acme', 'beta'], acme: {} };
        await appServing(now);
        const tenants = TestBed.inject(TenantService);
        const harness = await RouterTestingHarness.create();

        now.listDown = true;
        const landed = await harness.navigateByUrl('/tenants/acme/page');
        harness.detectChanges();

        expect(landed).toBeInstanceOf(TenantRefusedComponent);
        expect(shown(harness)).toBe('This page could not be loaded Try again');
        expect(tenants.current()).toBe('');
        expect(tenants.unread()).toBe('/tenants/acme/page');

        now.listDown = false;
        await tryAgain(harness);
        expect(shown(harness)).toBe('the page');
        expect(tenants.current()).toBe('acme');
      });
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
