import { inject, Injectable, InjectionToken, Type } from '@angular/core';
import { RESOURCE_CLIENT } from '@impulseframework/resource-angular/resource-client';
import { IconName } from '@impulseframework/resource-angular/types';
import { Menu, MenuGroup } from './menu';

/** What the frame does for a menu entry rather than going somewhere. */
export interface FrameActions {
  /** Opens the library's feature flags dialog. */
  featureFlags(): void;
}

/**
 * What an application tells the frame (provideFrame): its name, where every place opens,
 * the place outside every tenant, and its menu file. The tenant word is not here: it is
 * the generated descriptor's (domainRoute.word), the tenant record's name.
 */
export interface FrameConfig {
  /** The application's name at the upper left, a link to where the person works; absent, none is drawn. */
  readonly name?: string;
  /** The first page of every place, a path under the place ('dashboard'). Signing in lands there. */
  readonly home: string;
  /** The place outside every tenant: its name and icon. Absent, Configuration with a gear. */
  readonly outside?: { readonly label: string; readonly icon: IconName };
  /**
   * The application's menu file: the menus across the top, in order. A function is handed
   * the frame's actions, for an entry that opens the feature flags dialog, and is called
   * once, in an injection context, so an action of the application's own may use a
   * service it injects (`inject(MatDialog)` for an entry that opens a dialog).
   */
  readonly menus: readonly Menu[] | ((actions: FrameActions) => readonly Menu[]);
  /** The application's own entries in the Help menu, above About. */
  readonly help?: readonly MenuGroup[];
  /** The application's own entries in the person's menu, above Appearance. */
  readonly person?: readonly MenuGroup[];
  /**
   * A component of the application's drawn above the bar on every signed-in page: a notice
   * that holds for the whole session, such as the one saying whose eyes the person is
   * viewing through, with the way back. It draws nothing when it has nothing to say.
   */
  readonly banner?: Type<unknown>;
}

/** The application's frame configuration (provideFrame). */
export const FRAME_CONFIG = new InjectionToken<FrameConfig>('FRAME_CONFIG');

/** The default name of the place outside every tenant: named for what is there. */
export const defaultOutside = { label: 'Configuration', icon: 'settings' } as const satisfies {
  label: string;
  icon: IconName;
};

/** A word inside a sentence: "a customer", "this customer". */
export function inSentence(word: string): string {
  return word.toLowerCase();
}

/**
 * The tenant word a descriptor without one implies, from its segment: "tenants" is Tenant
 * and Tenants, "localities" Locality and Localities. The generator writes the record's own
 * word (domainRoute.word); this stands in for a descriptor from before it.
 */
export function wordOfSegment(segment: string): { one: string; many: string } {
  const title = (word: string): string => word.charAt(0).toUpperCase() + word.slice(1).replace(/-/g, ' ');
  const one = segment.endsWith('ies') ? segment.slice(0, -3) + 'y' : segment.replace(/s$/, '');
  return { one: title(one), many: title(segment) };
}

/**
 * Frame answers what the frame's pieces share: the configuration, the words, and the
 * tenant segment as the generated descriptor names it.
 */
@Injectable({ providedIn: 'root' })
export class Frame {
  readonly config = inject(FRAME_CONFIG);
  private route = inject(RESOURCE_CLIENT).descriptor.domainRoute;

  /** The application's name at the upper left; undefined draws none. */
  readonly name = this.config.name;
  /** The place outside every tenant. */
  readonly outside = this.config.outside ?? defaultOutside;
  /** Whether the application has tenants at all: its descriptor names a tenant segment. */
  readonly tenanted = this.route !== undefined;
  /** The tenant segment ('tenants') and its parameter ('tenantID'); empty without tenants. */
  readonly segment = this.route?.segment ?? '';
  readonly param = this.route?.param ?? '';
  /** The tenant segment's route path, as the router mounts it: `tenants/:tenantID`. */
  readonly tenantPath = this.route ? `${this.route.segment}/:${this.route.param}` : '';
  /** What the application calls a tenant, one and many. */
  readonly tenantWord = this.route?.word ?? wordOfSegment(this.segment || 'tenants');
}
