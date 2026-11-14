import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { PermissionDigest } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { firstValueFrom } from 'rxjs';
import { provideFrameTesting, specTransport } from './frame-testing';
import { findFrom, PlaceSwitcherComponent } from './place-switcher.component';

// The place switcher names the place the page is in, captioned with the application's word
// for a tenant, and lists the others: the place outside every tenant apart, then the
// tenants under the word for many. One place has nothing to switch to; a long list opens
// with a box to find one by name.

describe('PlaceSwitcherComponent', () => {
  let fixture: ComponentFixture<PlaceSwitcherComponent>;

  const open = async (digest: PermissionDigest, domains: { domain: string; name: string }[]): Promise<void> => {
    const transport = specTransport({ digests: { '': digest }, domains });
    await TestBed.configureTestingModule({
      imports: [PlaceSwitcherComponent],
      providers: [provideFrameTesting(transport)],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    fixture = TestBed.createComponent(PlaceSwitcherComponent);
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();
  };
  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;

  it('lists the place outside every tenant apart, then the tenants under the word for many', async () => {
    await open({ FeatureFlags: { List: 'granted' } }, [
      { domain: 'a-id', name: 'Zenith' },
      { domain: 'b-id', name: 'Acme' },
    ]);
    const groups = fixture.componentInstance.groups();
    expect(groups.map((group) => [group.label, group.entries.map((entry) => entry.label)])).toEqual([
      [undefined, ['Configuration']],
      ['Customers', ['Acme', 'Zenith']],
    ]);
    expect(groups[0]?.entries[0]?.checked).toBe(true);
    expect(element().querySelector('button.switcher')?.getAttribute('aria-label')).toBe(
      'In Configuration. Switch to another customer',
    );
  });

  it('names the one place of a person who has one, with nothing to open', async () => {
    await open({ FeatureFlags: { List: 'granted' } }, []);
    expect(element().querySelector('button.switcher')).toBeNull();
    expect(element().querySelector('.switcher.alone .name')?.textContent).toBe('Configuration');
  });

  it('asks a person on a page in none of their places to choose one, naming none they do not hold', async () => {
    // A tenant's refusal page, for a person who holds one tenant and nothing outside every
    // tenant: the page is outside every tenant, which is not one of their places.
    await open({}, [{ domain: 'acme', name: 'Acme' }]);
    const button = element().querySelector('button.switcher');
    expect(button?.getAttribute('aria-label')).toBe('Choose a customer');
    expect(button?.querySelector('.name')?.textContent).toBe('Choose a customer');
    expect(element().textContent).not.toContain('Configuration');
    expect(
      fixture.componentInstance.groups().map((group) => group.entries.map((entry) => [entry.label, entry.checked])),
    ).toEqual([[['Acme', false]]]);
  });

  it(`opens with a box to find a place from ${findFrom} places`, async () => {
    const many = Array.from({ length: findFrom }, (_, i) => ({ domain: `t${i}`, name: `Customer ${i}` }));
    await open({}, many);
    expect(fixture.componentInstance.findable()).toBe(true);
    fixture.componentInstance.filter.set('7');
    expect(fixture.componentInstance.groups().flatMap((group) => group.entries.map((entry) => entry.label))).toEqual([
      'Customer 7',
    ]);
  });
});
