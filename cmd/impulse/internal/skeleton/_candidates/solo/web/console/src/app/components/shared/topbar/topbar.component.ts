import { Component, inject } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatMenuModule } from '@angular/material/menu';
import { RouterModule } from '@angular/router';
import { HasPermissionDirective } from '@impulseframework/resource-angular/auth-has-permission';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { generatedNavItems } from '@impulseframework/resource-angular/resource-nav';
import { MenuItem } from '@impulseframework/resource-angular/types';

/**
 * The navigation menu over the generated resource routes. Items carry the permission
 * their destination requires; impulseHasPermission answers from the permission digest, so a
 * user sees only the menus they can open. A group is drawn while one of its items is, so
 * nobody is shown a menu that opens on nothing.
 */
@Component({
  selector: 'app-topbar',
  imports: [MatButtonModule, MatMenuModule, RouterModule, HasPermissionDirective],
  templateUrl: './topbar.component.html',
  styleUrl: './topbar.component.scss',
})
export class TopbarComponent {
  private auth = inject(AuthService);

  menuData = generatedNavItems;

  // shown answers whether an item has anything to draw: a permission the user holds, which
  // is what the template asks of the item itself. A group is drawn while one of its items
  // is.
  shown(item: MenuItem): boolean {
    if (item.children?.length) {
      return item.children.some((child) => this.shown(child));
    }
    return this.auth.authenticated() && this.auth.hasPermission(item.permission);
  }
}
