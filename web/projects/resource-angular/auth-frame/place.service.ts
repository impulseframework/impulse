import { computed, DestroyRef, effect, inject, Injectable, signal } from '@angular/core';
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

/**
 * A tenant the person holds a seat in and this session cannot enter: its name, and the
 * server's reason in a sentence ("Acme admits only people who sign in through its own
 * directory. You signed in with a password.").
 */
export interface ClosedPlace {
  readonly id: string;
  readonly label: string;
  readonly reason: string;
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
 * A person who has just accepted an invitation into a tenant lands in that tenant,
 * whatever else they hold and wherever they last worked: the server names the tenant on
 * the first session read after the acceptance (AuthService.invitedTenant). It is where
 * they land until a page under the top bar is open, and from then on they land where they
 * last worked, as everyone does.
 *
 * Where a person works is where a page under the top bar was open: a page inside the
 * frame (FrameComponent), once the navigation to it has ended. The sign-in and sign-out
 * pages are outside every tenant by their URLs and are no place anybody works in, so
 * signing out of a tenant and signing in again lands in that tenant.
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

  // How many frames are on the screen: one while a page under the top bar is drawn, none
  // on the sign-in and sign-out pages.
  private frames = signal(0);

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

  /**
   * The tenants that are closed to this sign-in, by name: the person holds a seat in each
   * and the session cannot enter it, for the reason the server gives. They are no places
   * to work in or to land on; the place switcher lists them under the person's places,
   * marked, so a seat is never missing without a word.
   */
  readonly closed = computed<readonly ClosedPlace[]>(() =>
    [...this.auth.closedDomains()]
      .map(([id, reason]) => ({ id, label: this.client.permissions.domainName(id), reason }))
      .sort((a, b) => a.label.localeCompare(b.label)),
  );

  /** The place the page is in. */
  readonly current = computed<Place>(() => {
    const id = this.tenants.current();
    return id ? { id, label: this.client.permissions.domainName(id as Domain) } : this.outside();
  });

  constructor() {
    // Remember where the person works, so the next sign-in lands there. The place outside
    // every tenant is remembered only for a person who holds something in it. A navigation
    // under way has settled the tenant before it has drawn its page (the frame of the page
    // being left still stands while a lazy page loads), so nothing is remembered until it
    // ends, and then only under a frame. A person who works somewhere has landed: the
    // tenant an invitation they accepted led to is no longer where they land.
    effect(() => {
      const place = this.current();
      const username = this.auth.sessionInfo().username;
      if (this.router.currentNavigation() || !this.frames()) return;
      if (!this.auth.authenticated() || !username) return;
      if (!place.id && !this.holdsOutside()) return;
      try {
        localStorage.setItem(this.rememberKey(username), place.id || rememberedOutside);
      } catch {
        // A browser that refuses storage lands the person by the first-time rule.
      }
      this.auth.invitationLanded();
    });
  }

  /**
   * drawn says a frame is on the screen until the given DestroyRef runs. FrameComponent
   * calls it with its own; a component an application draws its signed-in pages under in
   * the frame's stead does the same, or no place is remembered.
   */
  drawn(until: DestroyRef): void {
    this.frames.update((count) => count + 1);
    until.onDestroy(() => this.frames.update((count) => count - 1));
  }

  /**
   * Waits until the person's places are known: the global digest and the tenant list. The
   * list is read again when it does not hold the tenant an invitation the person has just
   * accepted seated them in: a person who accepts as they are was signed in, and had the
   * list, before the seat was made.
   */
  async ready(): Promise<void> {
    if (!this.auth.hasDigest()) {
      await firstValueFrom(this.auth.loadDigest());
    }
    if (!this.frame.tenanted) return;
    const tenants = this.tenants.tenants();
    const invited = this.auth.invitedTenant();
    if (!tenants.length || (invited && !tenants.includes(invited))) {
      await firstValueFrom(this.auth.loadDomains());
    }
  }

  /**
   * Where the person lands: the tenant an invitation they have just accepted seated them
   * in; else where they last worked if they still can, else the first place. An
   * invitation's tenant that is no place of theirs (it is closed to this sign-in) is
   * passed over.
   */
  defaultPlace(): Place | undefined {
    const places = this.places();
    const invited = this.auth.invitedTenant();
    const seat = invited ? places.find((place) => place.id === invited) : undefined;
    if (seat) return seat;
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
