import { ApplicationRef, Component } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { RouterTestingHarness } from '@angular/router/testing';
import { TransportResponse } from '@impulseframework/resource';
import { ScriptedTransport } from '@impulseframework/resource/testing';
import { provideFrameTesting, specTransport } from './frame-testing';
import { TenantRefusedComponent } from './tenant-refused.component';

// The refusal page asks the server what a page under the tenant would have asked (one row
// of the first tenant's resource the API lists) and shows the answer in the server's words:
// the concealing 404 for a tenant the session cannot reach, and the way in when the server
// serves the tenant after all. Every word on it is the application's word for a tenant.

/** Everything under tenant zzz answers the domain guard's 404; acme is served. */
function other(url: URL): TransportResponse | undefined {
  if (url.pathname.startsWith('/api/tenants/zzz/')) {
    return { status: 404, body: { message: 'unknown domain "zzz"' } };
  }
  if (url.pathname.startsWith('/api/tenants/acme/')) {
    return { status: 200, body: [] };
  }
  return undefined;
}

@Component({ template: 'the page' })
class PageComponent {}

describe('TenantRefusedComponent', () => {
  let transport: ScriptedTransport;
  let element: HTMLElement;

  const pageFor = async (tenant: string): Promise<void> => {
    transport = specTransport({ other });
    TestBed.configureTestingModule({
      providers: [
        provideFrameTesting(transport, {}, [
          { path: 'tenants/:tenantID/refused', component: TenantRefusedComponent },
          { path: 'tenants/:tenantID/dashboard', component: PageComponent },
        ]),
      ],
    });
    const harness = await RouterTestingHarness.create();
    await harness.navigateByUrl(`/tenants/${tenant}/refused`);
    await TestBed.inject(ApplicationRef).whenStable();
    harness.detectChanges();
    element = harness.routeNativeElement as HTMLElement;
  };

  const text = (selector: string): string | undefined =>
    element.querySelector(selector)?.textContent?.replace(/\s+/g, ' ').trim();

  const cases: {
    name: string;
    tenant: string;
    wantAsked: string;
    wantRefusal?: string;
    wantServed?: string;
  }[] = [
    {
      name: "a tenant the session cannot reach shows the server's answer, in its words",
      tenant: 'zzz',
      wantAsked: '/api/tenants/zzz/users?limit=1',
      wantRefusal: 'The server answered: 404: unknown domain "zzz"',
    },
    {
      name: 'a tenant the server serves after all offers the way in',
      tenant: 'acme',
      wantAsked: '/api/tenants/acme/users?limit=1',
      wantServed: 'You can open this customer now. Open it.',
    },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      await pageFor(tt.tenant);

      expect(text('h1')).toBe(`Customer ${tt.tenant}`);
      expect(transport.requests.map((r) => r.url)).toEqual([tt.wantAsked]);
      expect(text('.refusal')).toBe(tt.wantRefusal);
      expect(text('.served')).toBe(tt.wantServed);
    });
  }

  it("links the way in to the tenant's first page", async () => {
    await pageFor('acme');
    expect(element.querySelector('.served a')?.getAttribute('href')).toBe('/tenants/acme/dashboard');
  });
});
