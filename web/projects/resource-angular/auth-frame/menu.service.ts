import { computed, inject, Injectable } from '@angular/core';
import { MatDialog } from '@angular/material/dialog';
import { Method, Resource } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { openFeatureFlagsDialog } from '@impulseframework/resource-angular/feature-flags';
import { Frame, FrameActions } from './frame';
import { fillRoute, here, Menu, MenuContext, MenuGroup, page, Page, ShownGroup, shownGroups, shownMenus } from './menu';
import { TenantService } from './tenant.service';

/** The library's pages, every place's: where each lives under the place the person is in. */
export const framePages = {
  account: 'account',
  apiKeys: 'api-keys',
  about: 'about',
} as const;

/**
 * MenuService answers the menus as the person may use them, in the place the page is in:
 * the application's menu file, then Help, last on the bar, ending with About and the
 * application's name. The answer follows the session: a tenant opened, a permission
 * digest loaded, a feature flag flipped, and the menus redraw.
 */
@Injectable({ providedIn: 'root' })
export class MenuService {
  private auth = inject(AuthService);
  private tenants = inject(TenantService);
  private dialog = inject(MatDialog);
  private frame = inject(Frame);

  /** What the menus ask of the session. */
  readonly context: MenuContext = {
    allows: (scope) => this.tenants.can(scope.permission, scope.resource as Resource | Method),
    enabled: (feature) => this.auth.featureEnabled(feature),
    inTenant: () => this.tenants.current() !== '',
    link: (route) => fillRoute(route, this.tenants.current(), this.frame.segment, this.frame.param),
  };

  /** What the frame does for an entry of the menu file rather than going somewhere. */
  readonly actions: FrameActions = { featureFlags: () => void openFeatureFlagsDialog(this.dialog) };

  /** About the application: its name and the release, the last entry of Help. */
  readonly about: Page = page({
    label: this.frame.name ? `About ${this.frame.name}` : 'About',
    icon: 'info',
    route: [here, framePages.about],
  });

  /** The menus as declared: the application's menu file, then Help. */
  readonly declared: readonly Menu[] = [...this.applicationMenus(), this.help()];

  /** The menus across the top, or in the phone's panel. */
  readonly menus = computed(() => shownMenus(this.declared, this.context));

  /** Any other menu's groups, as the person may use them. */
  shown(groups: readonly MenuGroup[]): ShownGroup[] {
    return shownGroups(groups, this.context);
  }

  /** The menu file; a function is called here, in the service's injection context. */
  private applicationMenus(): readonly Menu[] {
    const menus = this.frame.config.menus;
    return typeof menus === 'function' ? menus(this.actions) : menus;
  }

  /** Help: the application's own help entries, then About. */
  private help(): Menu {
    return { label: 'Help', groups: [...(this.frame.config.help ?? []), { entries: [this.about] }] };
  }
}
