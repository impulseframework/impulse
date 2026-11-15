import { computed, effect, inject, Injectable } from '@angular/core';
import { Router, UrlTree } from '@angular/router';
import { Domain } from '@impulseframework/resource';
import { AuthService } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { BASE_URL } from '@impulseframework/resource-angular/types';
import { firstValueFrom } from 'rxjs';
import { Frame } from './frame';
import { MenuService } from './menu.service';
import { TenantService } from './tenant.service';

/** A place a person works in: a tenant, or the place outside every tenant (the empty id). */
export interface Place {
  /** The tenant's id; empty for the place outside every tenant. */
  readonly id: string;
  readonly label: string;
}

/** What the remembered value says for the place outside every tenant. */
const rememberedOutside = '@outside';

/**
 * PlaceService answers where a person can work and where they land. The places are the
 * place outside every tenant, for a person the application's menus show something there,
 * then every tenant the session reaches, by name. A person lands where they last worked; the first time, a
 * person who holds something outside every tenant lands there, and anyone else lands in
 * their first tenant by name. Nobody lands on an empty page outside every tenant.
 *
 * The open place is still the URL's, as TenantService says: a page under the tenant
 * segment is in that tenant, and any other page is outside every tenant.
 */
@Injectable({ providedIn: 'root' })
export class PlaceService {
  private auth = inject(AuthService);
  private client = inject(RESOURCE_CLIENT);
  private tenants = inject(TenantService);
  private menus = inject(MenuService);
  private router = inject(Router);
  private frame = inject(Frame);
  private base = inject(BASE_URL);

  /**
   * Whether the person holds anything outside every tenant: the application's menu file
   * shows them a page that lives there, or an action kept to it. A grant there that no
   * menu entry answers to (a record the application reads behind a tenant's page) is not
   * a place to work in, so it is no place of theirs and they never land on it.
   */
  readonly holdsOutside = computed(() => this.menus.showsOutside());

  /** Every place the person can work in: outside every tenant first when they hold something there, then the tenants by name. */
  readonly places = computed<readonly Place[]>(() => {
    const tenants = this.tenants
      .tenants()
      .map((id) => ({ id, label: this.client.permissions.domainName(id as Domain) }))
      .sort((a, b) => a.label.localeCompare(b.label));
    return this.holdsOutside() ? [this.outside(), ...tenants] : tenants;
  });

  /** The place the page is in. */
  readonly current = computed<Place>(() => {
    const id = this.tenants.current();
    return id ? { id, label: this.client.permissions.domainName(id as Domain) } : this.outside();
  });

  constructor() {
    // Remember where the person works, so the next sign-in lands there. The place outside
    // every tenant is remembered only for a person who holds something in it.
    effect(() => {
      const place = this.current();
      const username = this.auth.sessionInfo().username;
      if (!this.auth.authenticated() || !username) return;
      if (!place.id && !this.holdsOutside()) return;
      try {
        localStorage.setItem(this.rememberKey(username), place.id || rememberedOutside);
      } catch {
        // A browser that refuses storage lands the person by the first-time rule.
      }
    });
  }

  /** Waits until the person's places are known: the global digest and the tenant list. */
  async ready(): Promise<void> {
    if (!this.auth.hasDigest()) {
      await firstValueFrom(this.auth.loadDigest());
    }
    if (this.frame.tenanted && !this.tenants.tenants().length) {
      await firstValueFrom(this.auth.loadDomains());
    }
  }

  /** Where the person lands: where they last worked if they still can, else the first place. */
  defaultPlace(): Place | undefined {
    const places = this.places();
    let remembered: string | null = null;
    try {
      remembered = localStorage.getItem(this.rememberKey(this.auth.sessionInfo().username));
    } catch {
      remembered = null;
    }
    return places.find((place) => (place.id || rememberedOutside) === remembered) ?? places[0];
  }

  /** The first page of a place. */
  landing(place: Place | undefined): UrlTree {
    if (!place?.id) {
      return this.router.createUrlTree(['/', this.frame.config.home]);
    }
    return this.router.createUrlTree(['/', this.frame.segment, place.id, this.frame.config.home]);
  }

  /**
   * Opens a place. Between tenants the person stays on the same page; the place outside
   * every tenant, and a tenant opened from it, open on their first page, since the pages
   * differ between the two.
   */
  open(place: Place): Promise<boolean> {
    if (place.id && this.tenants.current()) {
      return this.tenants.open(place.id);
    }
    return this.router.navigateByUrl(this.landing(place));
  }

  /** The place outside every tenant, by the name the application gives it. */
  private outside(): Place {
    return { id: '', label: this.frame.outside.label };
  }

  /** The key the last place is remembered under: per application (its base) and per login. */
  private rememberKey(username: string): string {
    return `impulse-place:${this.base}:${username}`;
  }
}
