import { ApplicationRef } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Permission, PermissionDigest, Resource } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { scriptedTransport } from '@impulseframework/resource/testing';
import { firstValueFrom } from 'rxjs';
import { notNamedFor, provideFrameTesting, specTransport } from './frame-testing';
import { Menu, page } from './menu';
import { findFrom, PlaceSwitcherComponent } from './place-switcher.component';

// The place switcher names the place the page is in, captioned with the application's word
// for a tenant, and lists the others: the place outside every tenant apart, then the
// tenants under the word for many. One place has nothing to switch to; a long list opens
// with a box to find one by name.

/** The application's menu file: one page of the place outside every tenant, under a grant there. */
const menus: Menu[] = [
  {
    label: 'System',
    groups: [
      {
        entries: [
          page({
            label: 'Feature flags',
            route: ['feature-flags'],
            permission: { resource: 'FeatureFlags' as Resource, permission: 'List' as Permission },
          }),
        ],
      },
    ],
  },
];

describe('PlaceSwitcherComponent', () => {
  let fixture: ComponentFixture<PlaceSwitcherComponent>;

  const open = async (
    digest: PermissionDigest,
    domains: { domain: string; name: string; unavailable?: string }[],
  ): Promise<void> => {
    const transport = specTransport({ digests: { '': digest }, domains });
    await TestBed.configureTestingModule({
      imports: [PlaceSwitcherComponent],
      providers: [provideFrameTesting(transport, { menus })],
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
    expect(element().querySelector('button.impulse-place-switcher-place')?.getAttribute('aria-label')).toBe(
      'In Configuration. Switch to another customer',
    );
  });

  it('draws nothing until the list of tenants has been read: a list that could not be read is not a person with one place', async () => {
    // The person holds something outside every tenant and is in two tenants; the list fails.
    const played = specTransport({
      digests: { '': { FeatureFlags: { List: 'granted' } } },
      domains: [
        { domain: 'a-id', name: 'Zenith' },
        { domain: 'b-id', name: 'Acme' },
      ],
    });
    let down = true;
    const transport = scriptedTransport((request) =>
      down && request.url.endsWith('/user-domains')
        ? { status: 500, body: { message: 'the list could not be read' } }
        : played(request),
    );
    await TestBed.configureTestingModule({
      imports: [PlaceSwitcherComponent],
      providers: [provideFrameTesting(transport, { menus })],
    }).compileComponents();
    const auth = TestBed.inject(AuthService);
    await firstValueFrom(auth.checkUserSession());
    fixture = TestBed.createComponent(PlaceSwitcherComponent);
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();

    // Neither the lone name of the place outside every tenant, nor a list to open.
    expect(element().querySelector('.impulse-place-switcher-place')).toBeNull();
    expect(fixture.componentInstance.listed()).toBe(false);
    expect(fixture.componentInstance.alone()).toBe(false);

    down = false;
    await firstValueFrom(auth.loadDomains());
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    fixture.detectChanges();
    expect(
      element().querySelector('button.impulse-place-switcher-place .impulse-place-switcher-name')?.textContent,
    ).toBe('Configuration');
    expect(fixture.componentInstance.groups().flatMap((group) => group.entries.map((entry) => entry.label))).toEqual([
      'Configuration',
      'Acme',
      'Zenith',
    ]);
  });

  it('names the one place of a person who has one, with nothing to open', async () => {
    await open({ FeatureFlags: { List: 'granted' } }, []);
    expect(element().querySelector('button.impulse-place-switcher-place')).toBeNull();
    expect(
      element().querySelector('.impulse-place-switcher-place.impulse-place-switcher-alone .impulse-place-switcher-name')
        ?.textContent,
    ).toBe('Configuration');
  });

  it('asks a person on a page in none of their places to choose one, naming none they do not hold', async () => {
    // A tenant's refusal page, for a person who holds one tenant and nothing outside every
    // tenant: the page is outside every tenant, which is not one of their places.
    await open({}, [{ domain: 'acme', name: 'Acme' }]);
    const button = element().querySelector('button.impulse-place-switcher-place');
    expect(button?.getAttribute('aria-label')).toBe('Choose a customer');
    expect(button?.querySelector('.impulse-place-switcher-name')?.textContent).toBe('Choose a customer');
    expect(element().textContent).not.toContain('Configuration');
    expect(
      fixture.componentInstance.groups().map((group) => group.entries.map((entry) => [entry.label, entry.checked])),
    ).toEqual([[['Acme', false]]]);
  });

  it('lists a tenant that is closed to this sign-in under the places, with the reason, and never as a place to open', async () => {
    const reason = 'Bastion admits only people who sign in through its own directory. You signed in with a password.';
    await open({}, [
      { domain: 'anvil', name: 'Anvil' },
      { domain: 'bastion', name: 'Bastion', unavailable: reason },
      { domain: 'cinder', name: 'Cinder' },
    ]);
    expect(fixture.componentInstance.groups().flatMap((group) => group.entries.map((entry) => entry.label))).toEqual([
      'Anvil',
      'Cinder',
    ]);
    expect(fixture.componentInstance.unavailable()).toEqual([{ id: 'bastion', label: 'Bastion', reason }]);

    // The list draws it apart: named, with the reason, and no button to choose.
    (element().querySelector('button.impulse-place-switcher-place') as HTMLButtonElement).click();
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();
    const unavailable = document.querySelector('.impulse-place-switcher-unavailable');
    expect(unavailable?.querySelector('.impulse-place-switcher-unavailable-heading')?.textContent).toBe(
      'Not available',
    );
    expect(unavailable?.querySelector('.impulse-place-switcher-unavailable-name')?.textContent).toBe('Bastion');
    expect(unavailable?.querySelector('.impulse-place-switcher-unavailable-reason')?.textContent).toBe(reason);
    expect(unavailable?.querySelector('button')).toBeNull();
    expect(
      [
        ...document.querySelectorAll('.impulse-menu-list-menu button.impulse-menu-list-item .impulse-menu-list-label'),
      ].map((item) => item.textContent?.trim()),
    ).toEqual(['Anvil', 'Cinder']);
  });

  it('opens the list for a person with one place and a tenant that is closed to this sign-in', async () => {
    await open({ FeatureFlags: { List: 'granted' } }, [
      {
        domain: 'bastion',
        name: 'Bastion',
        unavailable: 'Bastion admits only people who sign in through its own directory.',
      },
    ]);
    expect(fixture.componentInstance.listed()).toBe(true);
    expect(
      element().querySelector('button.impulse-place-switcher-place .impulse-place-switcher-name')?.textContent,
    ).toBe('Configuration');
    expect(element().querySelector('.impulse-place-switcher-place.impulse-place-switcher-alone')).toBeNull();
  });

  it('narrows the tenants listed as not available with the places as the person types', async () => {
    await open({}, [
      { domain: 'anvil', name: 'Anvil' },
      {
        domain: 'bastion',
        name: 'Bastion',
        unavailable: 'Bastion admits only people who sign in through its own directory.',
      },
    ]);
    fixture.componentInstance.filter.set('anv');
    expect(fixture.componentInstance.unavailable()).toEqual([]);
    fixture.componentInstance.filter.set('bas');
    expect(fixture.componentInstance.unavailable().map((place) => place.label)).toEqual(['Bastion']);
    expect(fixture.componentInstance.groups()).toEqual([]);
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

  it("names every element for the switcher, in the bar and in the list it opens: an application's rule for its own .name or .unavailable lands on none", async () => {
    await open({ FeatureFlags: { List: 'granted' } }, [
      { domain: 'anvil', name: 'Anvil' },
      {
        domain: 'bastion',
        name: 'Bastion',
        unavailable: 'Bastion admits only people who sign in through its own directory.',
      },
    ]);
    element().querySelector<HTMLButtonElement>('button')?.click();
    fixture.detectChanges();
    await TestBed.inject(ApplicationRef).whenStable();

    expect(notNamedFor('impulse-place-switcher-', element())).toEqual([]);
    expect(document.querySelector('impulse-menu-list.impulse-place-switcher-places')).not.toBeNull();
    expect(document.querySelector('.impulse-place-switcher-unavailable-name')?.textContent).toBe('Bastion');
  });
});
