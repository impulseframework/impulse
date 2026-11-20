import { computed, DestroyRef, effect, inject, Injectable, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { NavigationEnd, ResolveEnd, Router, UrlTree } from '@angular/router';
import { Domain } from '@impulseframework/resource';
import { AuthService, UnreadPage } from '@impulseframework/resource-angular/auth-service';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { BASE_URL } from '@impulseframework/resource-angular/types';
import { filter, firstValueFrom } from 'rxjs';
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
 * Where a person works is where a page under the top bar was open: a page inside the
 * frame (FrameComponent), once the navigation to it has ended. The sign-in and sign-out
 * pages are outside every tenant by their URLs and are no place anybody works in, so
 * signing out of a tenant and signing in again lands in that tenant.
 *
 * The open place is still the URL's, as TenantService says: a page under the tenant
 * segment is in that tenant, and any other page is outside every tenant.
 *
 * Where a person lands, and whether they hold anything outside every tenant, is read from
 * the global permission digest. A digest that could not be read answers neither: `ready`
 * rejects, the guards that asked send the navigation to the root address with the address
 * that was asked for (unreadPageRedirect), and `unread` is true while that navigation is
 * the one that landed, or while the global digest is unread whoever asked for it (the
 * read at sign-in among them). The frame then draws "This page could not be loaded" with
 * Try again in the page's place, and `tryAgain` reads again.
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

  // The address the landed navigation was asked for and no guard could decide, because
  // what the person holds could not be read: written by the landing navigation, nothing else.
  private unreadUrl = signal('');

  /**
   * Whether the page under the top bar could not be loaded because what the person holds
   * could not be read: a guard sent the navigation that landed to the root address for it,
   * or the global digest is unread. The frame draws that in the page's place, and no page
   * under it, since nothing says what the person may see there.
   */
  readonly unread = computed(() => this.unreadUrl() !== '' || this.auth.digestUnread());

  constructor() {
    // What the frame says is settled by the navigation that lands, as the tenant is: only
    // a navigation a guard sent to the root address because it could not read carries the
    // address that was asked for, and any other landing clears it. A navigation that
    // lands where the router already is reports no ResolveEnd, so the end of every
    // navigation settles it too.
    this.router.events
      .pipe(
        filter((event) => event instanceof ResolveEnd || event instanceof NavigationEnd),
        takeUntilDestroyed(),
      )
      .subscribe(() => {
        const info = this.router.currentNavigation()?.extras.info;
        this.unreadUrl.set(info instanceof UnreadPage ? info.url : '');
      });

    // Remember where the person works, so the next sign-in lands there. The place outside
    // every tenant is remembered only for a person who holds something in it. A navigation
    // under way has settled the tenant before it has drawn its page (the frame of the page
    // being left still stands while a lazy page loads), so nothing is remembered until it
    // ends, and then only under a frame.
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
   * Waits until the person's places are known: the global digest and the tenant list. It
   * rejects where the global digest cannot be read: nothing then says where the person
   * lands or what they hold outside every tenant, which is not a person who holds nothing.
   */
  async ready(): Promise<void> {
    if (!this.auth.hasDigest()) {
      await firstValueFrom(this.auth.loadDigest());
    }
    if (this.frame.tenanted && !this.tenants.tenants().length) {
      await firstValueFrom(this.auth.loadDomains());
    }
  }

  /**
   * tryAgain reads again what could not be read, for the frame's Try again. The global
   * digest is read first where it is not held; a read that fails again leaves everything
   * as it is. Then the page a guard could not decide is asked for again, through its
   * guards (the root address is asked for as where the person lands, since its guard does
   * not run again on the address the router is already at). Where no guard was waiting,
   * the page under the frame draws once the digest is in.
   */
  async tryAgain(): Promise<void> {
    try {
      await this.ready();
    } catch {
      return;
    }
    const asked = this.unreadUrl();
    if (!asked) {
      return;
    }
    const tree = this.router.parseUrl(asked);
    await this.router.navigateByUrl(tree.root.hasChildren() ? tree : this.landing(this.defaultPlace()));
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
