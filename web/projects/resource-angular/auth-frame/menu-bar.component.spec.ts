import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter, Router } from '@angular/router';
import { ShownMenu } from './menu';
import { MenuBarComponent } from './menu-bar.component';

// A menu of the bar shows its entries as they are when it opens and while it is open. The
// menus change under the bar as the person works: a tenant opened moves every page of
// every place under it, so About opened from a tenant goes to the tenant's About even
// when the menu last opened outside every tenant.

/** The bar's one menu, Help, with About going to the address given. */
const help = (about: readonly string[]): ShownMenu[] => [
  { label: 'Help', groups: [{ entries: [{ label: 'About Spec', icon: 'info', link: about, disabled: false }] }] },
];

@Component({
  imports: [MenuBarComponent],
  template: `<impulse-menu-bar [menus]="menus()" />`,
})
class HostComponent {
  readonly menus = signal<readonly ShownMenu[]>(help(['/', 'about']));
}

describe('MenuBarComponent', () => {
  let fixture: ComponentFixture<HostComponent>;
  const top = (label: string): HTMLButtonElement =>
    Array.from((fixture.nativeElement as HTMLElement).querySelectorAll<HTMLButtonElement>('button.top')).find(
      (button) => button.textContent?.trim() === label,
    ) as HTMLButtonElement;
  const entries = (): string[] =>
    Array.from(document.querySelectorAll('.cdk-overlay-container impulse-menu-list .item .label')).map(
      (label) => label.textContent?.trim() ?? '',
    );
  const entry = (label: string): HTMLButtonElement =>
    Array.from(document.querySelectorAll<HTMLButtonElement>('.cdk-overlay-container button.item')).find((button) =>
      button.textContent?.includes(label),
    ) as HTMLButtonElement;

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
    entry('About Spec').click();
    fixture.detectChanges();
    expect(navigate).toHaveBeenLastCalledWith(['/', 'about']);

    // The person opens a tenant: the same menu's About goes under it.
    fixture.componentInstance.menus.set(help(['/', 'tenants', 'north', 'about']));
    fixture.detectChanges();
    top('Help').click();
    fixture.detectChanges();
    entry('About Spec').click();
    fixture.detectChanges();
    expect(navigate).toHaveBeenLastCalledWith(['/', 'tenants', 'north', 'about']);
  });

  it('redraws an open menu when its entries change', () => {
    top('Help').click();
    fixture.detectChanges();
    expect(entries()).toEqual(['About Spec']);

    fixture.componentInstance.menus.set([
      {
        label: 'Help',
        groups: [
          { entries: [{ label: 'Guide', icon: 'menu_book', link: ['/', 'guide'], disabled: false }] },
          { entries: [{ label: 'About Spec', icon: 'info', link: ['/', 'about'], disabled: false }] },
        ],
      },
    ]);
    fixture.detectChanges();
    expect(entries()).toEqual(['Guide', 'About Spec']);
  });
});
