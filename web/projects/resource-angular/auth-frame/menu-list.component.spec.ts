import { CdkMenuTrigger } from '@angular/cdk/menu';
import { Component, signal } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { notNamedFor } from './frame-testing';
import { ShownGroup } from './menu';
import { MenuListComponent } from './menu-list.component';

// A submenu opens when the pointer rests on its entry; the click a person then gives the
// entry keeps it open, where the trigger alone would close it again. An open submenu shows
// its entries as they are: the chosen appearance moves its check while it is open.

/** The person's menu as far as the spec needs it: Appearance, with the chosen one checked. */
const appearance = (dark: boolean): readonly ShownGroup[] => [
  {
    entries: [
      {
        label: 'Appearance',
        icon: 'contrast',
        disabled: false,
        groups: [
          {
            entries: [
              { label: 'Light', checked: !dark, disabled: false, run: (): void => undefined },
              { label: 'Dark', checked: dark, disabled: false, run: (): void => undefined },
            ],
          },
        ],
      },
      { label: 'Sign out', icon: 'logout', disabled: false, run: (): void => undefined },
    ],
  },
];

@Component({
  imports: [CdkMenuTrigger, MenuListComponent],
  template: `
    <button type="button" class="opener" [cdkMenuTriggerFor]="menu">Open</button>
    <ng-template #menu><impulse-menu-list [groups]="groups()" /></ng-template>
  `,
})
class HostComponent {
  readonly groups = signal(appearance(false));
}

describe('MenuListComponent', () => {
  let fixture: ComponentFixture<HostComponent>;
  const lists = (): number => document.querySelectorAll('.cdk-overlay-container impulse-menu-list').length;
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
    (fixture.nativeElement as HTMLElement).querySelector<HTMLButtonElement>('button.opener')?.click();
    fixture.detectChanges();
  });

  afterEach(() => {
    document.querySelector('.cdk-overlay-container')?.replaceChildren();
  });

  it('keeps a submenu open on a click on its entry, once the pointer opened it', () => {
    expect(lists()).toBe(1);
    entry('Appearance').dispatchEvent(new MouseEvent('mouseenter'));
    fixture.detectChanges();
    expect(lists()).toBe(2);
    entry('Appearance').click();
    fixture.detectChanges();
    expect(lists()).toBe(2);
    expect(entry('Dark')).toBeDefined();
  });

  it('keeps it open on a click that comes before the page draws again', () => {
    entry('Appearance').dispatchEvent(new MouseEvent('mouseenter'));
    entry('Appearance').click();
    fixture.detectChanges();
    expect(lists()).toBe(2);
  });

  it('redraws an open submenu when its entries change', () => {
    const checked = (): string[] => {
      const submenu = Array.from(document.querySelectorAll('.cdk-overlay-container impulse-menu-list')).at(-1);
      return Array.from(submenu?.querySelectorAll('button.impulse-menu-list-item') ?? [])
        .filter((button) => button.querySelector('.impulse-menu-list-end')?.textContent?.trim() === 'check')
        .map((button) => button.querySelector('.impulse-menu-list-label')?.textContent?.trim() ?? '');
    };
    entry('Appearance').dispatchEvent(new MouseEvent('mouseenter'));
    fixture.detectChanges();
    expect(checked()).toEqual(['Light']);

    fixture.componentInstance.groups.set(appearance(true));
    fixture.detectChanges();
    expect(checked()).toEqual(['Dark']);
  });

  it('opens a submenu on a click on its entry', () => {
    entry('Appearance').click();
    fixture.detectChanges();
    expect(lists()).toBe(2);
  });

  it("names every element for the list: an application's rule for its own .item, .label or .group lands on none", () => {
    const host = document.querySelector('.cdk-overlay-container impulse-menu-list') as HTMLElement;

    expect(notNamedFor('impulse-menu-list-', host)).toEqual([]);
    expect(host.querySelector('button.impulse-menu-list-submenu')).not.toBeNull();
  });
});
