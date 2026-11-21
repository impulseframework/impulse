import { Component } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { provideRouter } from '@angular/router';
import { notNamedFor } from './frame-testing';
import { ShownMenu } from './menu';
import { MenuDrawerComponent } from './menu-drawer.component';

// The phone panel: the first page lists the menus; a menu, then a submenu, slides in with a
// back row naming it; Back returns; the close button and a chosen entry ask the panel to
// close; and reset brings the first page back for next time.

const menus: ShownMenu[] = [
  {
    label: 'Records',
    groups: [
      {
        entries: [
          { label: 'Customers', link: ['customers'], disabled: false },
          {
            label: 'Agents',
            disabled: false,
            groups: [{ entries: [{ label: 'History', link: ['agents-history'], disabled: false }] }],
          },
        ],
      },
    ],
  },
  { label: 'Help', groups: [{ entries: [{ label: 'Guides', link: ['guides'], disabled: false }] }] },
];

@Component({ template: 'any page' })
class AnyPageComponent {}

describe('MenuDrawerComponent', () => {
  let fixture: ComponentFixture<MenuDrawerComponent>;
  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  /** The labels on the page in view. */
  const inView = (): string[] =>
    Array.from(element().querySelectorAll('.impulse-menu-drawer-page:not([inert]) .impulse-menu-drawer-label')).map(
      (l) => l.textContent?.trim() ?? '',
    );
  const tap = (selector: string, text: string): void => {
    const button = Array.from(
      element().querySelectorAll<HTMLButtonElement>(`.impulse-menu-drawer-page:not([inert]) ${selector}`),
    ).find((b) => b.textContent?.includes(text));
    button?.click();
    fixture.detectChanges();
  };

  beforeEach(async () => {
    await TestBed.configureTestingModule({
      imports: [MenuDrawerComponent],
      providers: [provideRouter([{ path: '**', component: AnyPageComponent }])],
    }).compileComponents();
    fixture = TestBed.createComponent(MenuDrawerComponent);
    fixture.componentRef.setInput('menus', menus);
    fixture.componentRef.setInput('name', 'Lodestar');
    fixture.detectChanges();
  });

  it('opens on the menus, under its own bar with the name and the close button', () => {
    expect(inView()).toEqual(['Records', 'Help']);
    expect(element().querySelector('.impulse-menu-drawer-top .impulse-menu-drawer-name')?.textContent).toBe('Lodestar');
    expect(element().querySelector('.impulse-menu-drawer-top button.impulse-menu-drawer-close')).not.toBeNull();
  });

  it('slides into a menu and a submenu, each with a back row naming it, and back again', () => {
    tap('button.impulse-menu-drawer-row', 'Records');
    expect(inView()).toEqual(['Records', 'Customers', 'Agents']);
    tap('button.impulse-menu-drawer-row', 'Agents');
    expect(inView()).toEqual(['Agents', 'History']);
    tap('button.impulse-menu-drawer-back', 'Agents');
    expect(inView()).toEqual(['Records', 'Customers', 'Agents']);
  });

  it('asks to close from the close button and from a chosen entry', () => {
    const closing = vi.fn();
    const chosen = vi.fn();
    fixture.componentInstance.closing.subscribe(closing);
    fixture.componentInstance.chosen.subscribe(chosen);
    (element().querySelector('.impulse-menu-drawer-top button.impulse-menu-drawer-close') as HTMLButtonElement).click();
    tap('button.impulse-menu-drawer-row', 'Help');
    tap('button.impulse-menu-drawer-row', 'Guides');
    expect(closing).toHaveBeenCalledTimes(1);
    expect(chosen).toHaveBeenCalledTimes(1);
  });

  it('reset brings back the first page', () => {
    tap('button.impulse-menu-drawer-row', 'Records');
    fixture.componentInstance.reset();
    fixture.detectChanges();
    expect(inView()).toEqual(['Records', 'Help']);
    expect(element().querySelectorAll('.impulse-menu-drawer-page').length).toBe(1);
  });

  it("names every element for the panel: an application's rule for its own .row, .page or .menu lands on none", () => {
    tap('button.impulse-menu-drawer-row', 'Records');

    expect(notNamedFor('impulse-menu-drawer-', element())).toEqual([]);
    expect(inView()).toEqual(['Records', 'Customers', 'Agents']);
  });

  it('gives the focus to the back row of the page that slides in, and back to the row that opened it', () => {
    tap('button.impulse-menu-drawer-row', 'Records');
    TestBed.tick();
    expect(document.activeElement?.classList.contains('impulse-menu-drawer-back')).toBe(true);

    tap('button.impulse-menu-drawer-back', 'Records');
    TestBed.tick();
    expect(document.activeElement?.classList.contains('impulse-menu-drawer-row')).toBe(true);
    expect(document.activeElement?.textContent).toContain('Records');
  });
});
