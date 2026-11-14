import { CommonModule } from '@angular/common';
import { Component, computed, inject, Injector, Input, Signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatExpansionModule } from '@angular/material/expansion';
import { MatIconModule } from '@angular/material/icon';
import { MatSidenavModule } from '@angular/material/sidenav';
import { RouterModule } from '@angular/router';

import { FeatureDirective } from '@impulseframework/resource-angular/auth-feature';
import { HasPermissionDirective } from '@impulseframework/resource-angular/auth-has-permission';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { PermissionScope } from '@impulseframework/resource-angular/types';
import { UiCoreService } from '@impulseframework/resource-angular/ui-core-service';

export interface NavItem {
  type: 'link' | 'header' | 'expandable';
  routerLink?: string[];
  label: string;
  icon?: string;
  permission?: PermissionScope;
  /** The feature flag a link sits behind, by name; the link is hidden while the flag is off. */
  feature?: string;
  children?: NavItem[];
  isExpanded?: boolean;
  attentionCount?: Signal<number>;
}

export type NavGroups = Record<string, NavItem[]>;

@Component({
  selector: 'impulse-sidenav',
  templateUrl: './sidenav.component.html',
  styleUrls: ['./sidenav.component.scss'],
  imports: [
    MatIconModule,
    RouterModule,
    CommonModule,
    MatExpansionModule,
    MatButtonModule,
    MatSidenavModule,
    HasPermissionDirective,
    FeatureDirective,
  ],
})
export class SidenavComponent {
  ui = inject(UiCoreService);
  private injector = inject(Injector);

  @Input() navGroups?: NavGroups;
  currentNav = computed(() => {
    return this.updateNavItems(this.ui.currentSidenavIdentifier());
  });

  /**
   * Updates the currentNav based on the identifier
   * @param identifier
   * @returns NavItem[]
   * @memberof SidenavComponent
   */
  updateNavItems(identifier: string): NavItem[] {
    if (!this.navGroups) {
      return [];
    }
    if (!this.navGroups[identifier]) {
      // pick the first one if the identifier is not found so we don't break the UI
      identifier = Object.keys(this.navGroups)[0];
    }
    return this.navGroups[identifier];
  }

  /**
   * Whether an item has anything to draw. A link has when the session is authenticated,
   * its feature flag is on and the caller holds its permission: what the two directives
   * on the link ask. A group that expands has when one of its items has, so a group
   * whose every link is hidden is not drawn: a heading that opens on nothing offers
   * nothing. A header is always drawn. Signal-backed, so the answer follows the digest
   * and the flags as they load. The session is asked for when the first link is judged,
   * as the directives ask for it when the first link is drawn.
   */
  shown(navItem: NavItem): boolean {
    switch (navItem.type) {
      case 'link': {
        const auth = this.injector.get(AuthService);
        return auth.authenticated() && auth.featureEnabled(navItem.feature) && auth.hasPermission(navItem.permission);
      }
      case 'expandable':
        return (navItem.children ?? []).some((child) => this.shown(child));
      default:
        return true;
    }
  }
}
