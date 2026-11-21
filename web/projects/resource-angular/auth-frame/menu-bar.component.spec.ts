import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { notNamedFor } from './frame-testing';
import { ShownMenu } from './menu';
import { MenuBarComponent } from './menu-bar.component';

// A menu of the bar shows its entries as they are when it opens and while it is open. The
// menus change under the bar as the person works: a tenant opened moves every page of
// every place under it, so a page of every place opened from a tenant goes to the
// tenant's own even when the menu last opened outside every tenant.

/** The bar's one menu, Help, with its Handbook page going to the address given. */
const help = (handbook: readonly string[]): ShownMenu[] => [
  { label: 'Help', groups: [{ entries: [{ label: 'Handbook', icon: 'info', link: handbook, disabled: false }] }] },
];

@Component({
  imports: [MenuBarComponent],
  template: `<impulse-menu-bar [menus]="menus()" />`,
})
class HostComponent {
  readonly menus = signal<readonly ShownMenu[]>(help(['/', 'handbook']));
}

describe('MenuBarComponent', () => {
  let fixture: ComponentFixture<HostComponent>;
  const top = (label: string): HTMLButtonElement =>
    Array.from(
      (fixture.nativeElement as HTMLElement).querySelectorAll<HTMLButtonElement>('button.impulse-menu-bar-top'),
    ).find((button) => button.textContent?.trim() === label) as HTMLButtonElement;
  const entries = (): string[] =>
    Array.from(
      document.querySelectorAll(
        '.cdk-overlay-container impulse-menu-list .impulse-menu-list-item .impulse-menu-list-label',
      ),
    ).map((label) => label.textContent?.trim() ?? '');
  const entry = (label: string): HTMLButtonElement =>
    Array.from(
      document.querySelectorAll<HTMLButtonElement>('.cdk-overlay-container button.impulse-menu-list-item'),
    ).find((button) => button.textContent?.includes(label)) as HTMLButtonElement;

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [HostComponent],
      providers: [provideRouter([])],
    }).compileComponents();
    fixture = TestBed.createComponent(HostComponent);
    fixture.detectChanges();
  });

  afterEach(() => {
    document.querySelector('.cdk-overlay-container')?.replaceChildren();
  });

  it('opens a menu as it is now, when its entries changed since it last opened', () => {
    const navigate = vi.spyOn(TestBed.inject(Router), 'navigate').mockResolvedValue(true);
    top('Help').click();
    fixture.detectChanges();
    entry('Handbook').click();
    fixture.detectChanges();
    expect(navigate).toHaveBeenLastCalledWith(['/', 'handbook']);

    // The person opens a tenant: the same menu's page goes under it.
    fixture.componentInstance.menus.set(help(['/', 'tenants', 'north', 'handbook']));
    fixture.detectChanges();
    top('Help').click();
    fixture.detectChanges();
    entry('Handbook').click();
    fixture.detectChanges();
    expect(navigate).toHaveBeenLastCalledWith(['/', 'tenants', 'north', 'handbook']);
  });

  it('redraws an open menu when its entries change', () => {
    top('Help').click();
    fixture.detectChanges();
    expect(entries()).toEqual(['Handbook']);

    fixture.componentInstance.menus.set([
      {
        label: 'Help',
        groups: [
          { entries: [{ label: 'Guide', icon: 'menu_book', link: ['/', 'guide'], disabled: false }] },
          { entries: [{ label: 'Handbook', icon: 'info', link: ['/', 'handbook'], disabled: false }] },
        ],
      },
    ]);
    fixture.detectChanges();
    expect(entries()).toEqual(['Guide', 'Handbook']);
  });

  it("names every element for the menus: an application's rule for its own .bar or .top lands on none", () => {
    const host = (fixture.nativeElement as HTMLElement).querySelector('impulse-menu-bar') as HTMLElement;

    expect(notNamedFor('impulse-menu-bar-', host)).toEqual([]);
    expect(host.querySelector('button.impulse-menu-bar-top')).not.toBeNull();
  });
});
