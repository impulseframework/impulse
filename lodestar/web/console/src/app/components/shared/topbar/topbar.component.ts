import { Component, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatMenuModule } from '@angular/material/menu';
import { RouterModule } from '@angular/router';
import { FeatureDirective } from '@impulseframework/resource-angular/auth-feature';
import { HasPermissionDirective } from '@impulseframework/resource-angular/auth-has-permission';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { generatedNavItems } from '@impulseframework/resource-angular/resource-nav';
import { PermissionScopes } from '@app/service/zz_gen_constants';
import { DomainRouteParam, ResourceScopes } from '@app/service/zz_gen_resources';
import { Domain, MenuItem, PermissionScope, Resource } from '@impulseframework/resource-angular/types';
import { SectorService } from '@components/sector/sector.service';

export type { MenuItem };

@Component({
  selector: 'app-topbar',
  imports: [MatButtonModule, MatMenuModule, RouterModule, FeatureDirective, HasPermissionDirective],
  templateUrl: './topbar.component.html',
  styleUrl: './topbar.component.scss',
})
export class TopbarComponent {
  private sectors = inject(SectorService);
  private auth = inject(AuthService);

  menuData = generatedNavItems;

  // scopeFor completes an item's permission question: a sector-scoped resource is
  // asked in the open sector's partition, a global one as is.
  scopeFor(item: MenuItem): PermissionScope | undefined {
    const permission = item.permission;
    if (!permission || ResourceScopes[permission.resource as Resource] !== PermissionScopes.domain) {
      return permission;
    }
    const sector = this.sectors.current();
    return sector ? { ...permission, domain: sector as Domain } : permission;
  }

  // linkFor completes an item's route: the sector segment's parameter, where the route
  // names it (the decks' items, and the config-driven pages' routes as resourceRoutes
  // registered them), is the open sector; undefined while no sector is open, so the item
  // is not drawn and the star chart is the way in.
  linkFor(item: MenuItem): string[] | undefined {
    const route = item.route;
    const parameter = `:${DomainRouteParam}`;
    if (!route?.some((part) => part.includes(parameter))) {
      return route;
    }
    const sector = this.sectors.current();
    return sector ? route.map((part) => part.replace(parameter, sector)) : undefined;
  }

  // shown answers whether an item has anything to draw: a link, a feature flag that is
  // on, and a permission the persona holds, which is what the template asks of the item
  // itself. A group is drawn while one of its items is, so nobody is shown a menu that
  // opens on nothing: a Sector Marshal holds none of the fleet-wide lists of logins, and
  // sees no Crew Logins group.
  shown(item: MenuItem): boolean {
    if (item.children?.length) {
      return item.children.some((child) => this.shown(child));
    }
    return (
      this.linkFor(item) !== undefined &&
      this.auth.authenticated() &&
      this.auth.featureEnabled(item.feature) &&
      this.auth.hasPermission(this.scopeFor(item))
    );
  }
}
