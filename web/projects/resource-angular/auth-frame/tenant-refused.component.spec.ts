import { ApplicationRef } from '@angular/core';
import { TestBed } from '@angular/core/testing';
import { RouterTestingHarness } from '@angular/router/testing';
import { ScriptedTransport } from '@impulseframework/resource/testing';
import { notNamedFor, provideFrameTesting, specTransport } from './frame-testing';
import { TenantRefusedComponent } from './tenant-refused.component';

// The refusal page says 404 Not Found and nothing more: the tenant guard has already asked
// the server, a tenant that does not exist and one the session holds nothing in look the
// same, and the tenant's id, which the address holds, is shown nowhere.

/** A tenant's key is an id. */
const tenant = '7b0c1f4e-5d2a-4c3b-9e8f-0a1b2c3d4e5f';

describe('TenantRefusedComponent', () => {
  let transport: ScriptedTransport;
  let element: HTMLElement;

  beforeEach(async () => {
    transport = specTransport({});
    TestBed.configureTestingModule({
      providers: [
        provideFrameTesting(transport, {}, [{ path: 'tenants/:tenantID/refused', component: TenantRefusedComponent }]),
      ],
    });
    const harness = await RouterTestingHarness.create();
    await harness.navigateByUrl(`/tenants/${tenant}/refused`);
    await TestBed.inject(ApplicationRef).whenStable();
    harness.detectChanges();
    element = harness.routeNativeElement as HTMLElement;
  });

  it('says 404 Not Found and nothing more', () => {
    expect(element.textContent?.replace(/\s+/g, ' ').trim()).toBe('404 Not Found');
    expect(element.querySelector('h1')?.textContent?.trim()).toBe('404 Not Found');
  });

  it("names its one element for the page: an application's rule for its own .refused lands on none", () => {
    expect(notNamedFor('impulse-tenant-refused-', element)).toEqual([]);
    expect(element.querySelector('div.impulse-tenant-refused-content')).not.toBeNull();
  });

  it("shows no tenant's id and asks the server nothing of the tenant", () => {
    expect(element.innerHTML).not.toContain(tenant);
    expect(transport.requests.map((r) => r.url).filter((url) => url.includes(tenant))).toEqual([]);
  });
});
