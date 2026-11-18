import { inject } from '@angular/core';
import { ComponentFixture, TestBed } from '@angular/core/testing';
import { MatDialog } from '@angular/material/dialog';
import { ListPermission, PermissionDigest, Resource } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { firstValueFrom } from 'rxjs';
import { FrameConfig } from './frame';
import { FrameBarComponent } from './frame-bar.component';
import { provideFrameTesting, specTransport } from './frame-testing';
import { ShownMenu } from './menu';
import { MenuService } from './menu.service';

// The bar draws the application's name, the place, the menus and the person's corner. The
// menus hold what the digest grants: an entry of the menu file that opens the feature flags
// dialog shows under List on FeatureFlags and opens the dialog; a person with no grant there
// has no System menu at all. Help comes last on every bar and ends with About.

/** The flags' reader: List on FeatureFlags. */
const flagsReader: PermissionDigest = { FeatureFlags: { List: 'granted', Read: 'granted' } };
/** Someone with no grant at all. */
const nobody: PermissionDigest = {};

/** A menu file with one System menu: Feature flags, behind List on FeatureFlags. */
const menuFile: FrameConfig['menus'] = (actions) => [
  {
    label: 'System',
    groups: [
      {
        entries: [
          {
            label: 'Feature flags…',
            icon: 'flag',
            run: actions.featureFlags,
            place: 'outside',
            permission: { resource: 'FeatureFlags' as Resource, permission: ListPermission },
          },
        ],
      },
    ],
  },
];

describe('FrameBarComponent', () => {
  let fixture: ComponentFixture<FrameBarComponent>;

  const element = (): HTMLElement => fixture.nativeElement as HTMLElement;
  const menu = (label: string): ShownMenu | undefined =>
    TestBed.inject(MenuService)
      .menus()
      .find((m) => m.label === label);

  /** Signs in over the spec's server with the digest and builds the bar. */
  const signIn = async (digest: PermissionDigest, config: Partial<FrameConfig> = {}): Promise<void> => {
    const transport = specTransport({ digests: { '': digest } });
    await TestBed.configureTestingModule({
      imports: [FrameBarComponent],
      providers: [provideFrameTesting(transport, { menus: menuFile, ...config })],
    }).compileComponents();
    await firstValueFrom(TestBed.inject(AuthService).checkUserSession());
    fixture = TestBed.createComponent(FrameBarComponent);
    fixture.detectChanges();
  };

  afterEach(() => {
    TestBed.inject(MatDialog).closeAll();
  });

  it('draws the name, the place, the menus, and the person', async () => {
    await signIn(nobody);
    expect(element().querySelector('a.brand')?.textContent).toContain('Lodestar');
    expect(element().querySelector('impulse-place-switcher')).not.toBeNull();
    expect(element().querySelector('impulse-menu-bar')).not.toBeNull();
    expect(element().querySelector('impulse-person-menu')?.textContent).toContain('gale');
  });

  it('draws no name when the application gives none', async () => {
    await signIn(nobody, { name: undefined });
    expect(element().querySelector('a.brand')).toBeNull();
    expect(
      menu('Help')
        ?.groups.at(-1)
        ?.entries.map((e) => e.label),
    ).toEqual(['About']);
  });

  it('puts Feature flags in the System menu under List on FeatureFlags, and it opens the dialog', async () => {
    await signIn(flagsReader);
    const entry = menu('System')
      ?.groups.flatMap((group) => group.entries)
      .find((e) => e.label === 'Feature flags…');
    expect(entry).toBeDefined();
    entry?.run?.();
    fixture.detectChanges();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(document.querySelector('mat-dialog-container')).not.toBeNull();
  });

  it('draws no System menu for a person with no grant outside the tenants', async () => {
    await signIn(nobody);
    expect(menu('System')).toBeUndefined();
    expect(menu('Help')).toBeDefined();
  });

  it('calls the menu file in an injection context, so its own action may use a service', async () => {
    const menusWithDialog: FrameConfig['menus'] = () => {
      const dialog = inject(MatDialog);
      return [
        { label: 'Records', groups: [{ entries: [{ label: 'Close every dialog', run: () => dialog.closeAll() }] }] },
      ];
    };
    await signIn(nobody, { menus: menusWithDialog });
    const entry = menu('Records')?.groups[0]?.entries[0];
    expect(entry?.label).toBe('Close every dialog');
    const closed = vi.spyOn(TestBed.inject(MatDialog), 'closeAll');
    entry?.run?.();
    expect(closed).toHaveBeenCalled();
  });

  it("puts Help last, ending with About and the name, under the application's own help", async () => {
    await signIn(nobody, { help: [{ entries: [{ label: 'Guides', icon: 'menu_book', run: (): void => undefined }] }] });
    const help = TestBed.inject(MenuService).menus().at(-1);
    expect(help?.label).toBe('Help');
    expect(help?.groups.map((group) => group.entries.map((e) => e.label))).toEqual([['Guides'], ['About Lodestar']]);
    expect(help?.groups.at(-1)?.entries[0]?.link).toEqual(['about']);
  });
});
