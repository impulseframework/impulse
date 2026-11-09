import { ExecutePermission, Method, Resource } from './brands';
import { MethodDescriptor } from './descriptor';
import { PermissionStore, Requester } from './permissions';
import { Store } from './store';
import { Warn, warnOnce } from './warnings';

/**
 * The name of the generated method that flips a flag, as the descriptor's methods record
 * keys it: every generated API declares it, and the digest's Execute entry for it is what
 * says whether the session user may flip anything.
 */
export const setFeatureMethod = 'SetFeature' as Method;

/** The name of the framework-owned resource that holds every declared flag with its state. */
export const featureFlagsResource = 'FeatureFlags' as Resource;

/** One row of the feature flags resource: a declared flag and its state in this environment. */
export interface FeatureFlag {
  name: string;
  description: string;
  enabled: boolean;
  /** When the flag was last flipped (or first written), as the server wrote it. */
  updatedAt: string;
  updatedBy: string;
}

/** What the server answers a flip with: the flag's new state and when it took effect. */
export interface FeatureFlip {
  name: string;
  enabled: boolean;
  updatedAt: string;
}

/** The body of a flip, as the generated method takes it. */
export interface SetFeatureRequest {
  name: string;
  enabled: boolean;
}

/**
 * The enabled set at one instant. `loaded` says whether a load has answered since the
 * set was last cleared: an unloaded set is empty, and a consumer that needs the answer
 * before rendering (a route guard) loads it first (`ensure`).
 */
export interface FeaturesSnapshot {
  readonly enabled: ReadonlySet<string>;
  readonly loaded: boolean;
}

/** What createClient hands the feature state. */
export interface FeatureStateOptions {
  request: Requester;
  /**
   * The route the enabled set is read from: the descriptor's `features.route`. Absent on
   * an API that serves no feature flags (a descriptor from a generator that predates
   * them): the set stays empty and unloaded, a refresh asks nothing, and the flags can be
   * neither read nor flipped from here.
   */
  route?: string;
  /** The generated flip method's descriptor entry; absent on an API that declares none, where a flip is refused before any request. */
  setFeature?: MethodDescriptor;
  /** The digest cache a flip refreshes afterwards, since a flip changes what the digest carries. */
  permissions: PermissionStore;
  /** Reads every row of the feature flags resource through the client's own handle. */
  flags: () => Promise<FeatureFlag[]>;
  /** Where an ask of an API that serves no feature flags is announced; the client's once-per-message hook, the console by default. */
  warn?: Warn;
}

const nothing: FeaturesSnapshot = { enabled: new Set(), loaded: false };

/** The refusal of a read or a flip on an API that serves no feature flags. */
const notServed =
  'this API serves no feature flags: its descriptor carries no features route, so none can be read or flipped from here';

/**
 * What the state announces, once per message per client, when a flag is asked of an API
 * whose descriptor carries no features route: the flag answers off, and regenerating the
 * client API is what would serve it. With a name the message names the flag; without one
 * (a refresh) it speaks of every flag.
 */
export function noFeaturesRouteWarning(feature?: string): string {
  const asked = feature === undefined ? 'the feature flags were' : `feature flag ${feature} was`;
  const answer = feature === undefined ? 'every flag answers off' : 'it answers off';
  return (
    `${asked} asked of an API that serves no feature flags (the generated descriptor carries no features route): ` +
    `${answer}; regenerate the client API with a resource generator that emits the features route`
  );
}

/**
 * FeatureState owns the enabled set of feature flags: the names the server says are on
 * in this environment. It loads once at sign-in, beside the permission digest, from the
 * generated features route, and `enabled` answers synchronously from the copy and never
 * fetches: a flag the set does not hold is off, and so is every flag before the set has
 * loaded, so consumers fail closed. Nothing polls and nothing is pushed: a flip by
 * someone else is seen at the next sign-in, by design. The one exception is the person
 * flipping, whose `setFeature` refreshes this set and the digest once the write succeeds,
 * so their own pages follow at once.
 *
 * An API that serves no feature flags (its descriptor carries no `features` route, since
 * its generator predates them) keeps the set empty and unloaded: `refresh` resolves to
 * nothing without a request, every flag answers off, and `flags` and `setFeature` refuse
 * before any request, saying so. The absence is announced, not silent: `enabled`,
 * `ensure`, and `refresh` each reach the client's warn hook once per message (the flag's
 * name, or every flag for a refresh), naming the regeneration that would serve them.
 */
export class FeatureState {
  readonly snapshot = new Store<FeaturesSnapshot>(nothing);
  private inflight: Promise<readonly string[]> | undefined;
  private readonly warn: Warn;

  constructor(private options: FeatureStateOptions) {
    this.warn = options.warn ?? warnOnce();
  }

  get(): FeaturesSnapshot {
    return this.snapshot.get();
  }

  subscribe(listener: (snapshot: FeaturesSnapshot) => void): () => void {
    return this.snapshot.subscribe(listener);
  }

  /** Whether the API serves feature flags: its descriptor carries the features route. */
  get served(): boolean {
    return this.options.route !== undefined;
  }

  /** Whether the named flag is on; false before the set has loaded, and off, announced once, on an API that serves no flags. */
  enabled(feature: string): boolean {
    if (!this.served) {
      this.warn(noFeaturesRouteWarning(feature));
      return false;
    }
    return this.snapshot.get().enabled.has(feature);
  }

  /** Whether a load has answered since the set was last cleared. */
  get loaded(): boolean {
    return this.snapshot.get().loaded;
  }

  /** The enabled names, sorted. */
  names(): readonly string[] {
    return [...this.snapshot.get().enabled].sort();
  }

  /** Whether the digest says the session user may flip a flag: Execute on the generated flip method. */
  canSet(): boolean {
    return this.options.permissions.can({ resource: setFeatureMethod, permission: ExecutePermission });
  }

  /**
   * Loads (or reloads) the enabled set from the features route. Concurrent calls share
   * one request. A failed load empties the set and leaves it unloaded, so every flag
   * answers off and the next `ensure` asks again, and rethrows so callers see the failure.
   * On an API that serves no feature flags it announces the absence once and resolves to
   * nothing without a request, the set staying unloaded.
   */
  refresh(): Promise<readonly string[]> {
    const route = this.options.route;
    if (route === undefined) {
      this.warn(noFeaturesRouteWarning());
      return Promise.resolve([]);
    }
    if (this.inflight) {
      return this.inflight;
    }
    const load = this.options
      .request<{ enabled?: string[] | null } | null>('GET', route)
      .then((body) => body?.enabled ?? [])
      .catch((error: unknown) => {
        this.snapshot.set(nothing);
        throw error;
      })
      .then((enabled) => {
        this.snapshot.set({ enabled: new Set(enabled), loaded: true });
        return enabled as readonly string[];
      })
      .finally(() => {
        this.inflight = undefined;
      });
    this.inflight = load;
    return load;
  }

  /**
   * Answers whether a flag is on, loading the set first when it has not loaded; a failed
   * load answers false. On an API that serves no feature flags it answers off at once,
   * announced under the flag's name, and loads nothing.
   */
  async ensure(feature: string): Promise<boolean> {
    if (!this.served) {
      return this.enabled(feature);
    }
    if (!this.loaded) {
      try {
        await this.refresh();
      } catch {
        return false;
      }
    }
    return this.enabled(feature);
  }

  /**
   * Every declared flag with its state, read from the feature flags resource through the
   * client. An API that serves no feature flags refuses before any request.
   */
  async flags(): Promise<FeatureFlag[]> {
    if (!this.served) {
      throw new Error(notServed);
    }
    return this.options.flags();
  }

  /**
   * Flips one flag through the generated method, and once the write succeeds reloads the
   * enabled set and every cached digest and the domains, so the pages of the person
   * flipping follow at once. A refused flip (an unknown name, a missing grant) rejects with
   * the server's ApiError and refreshes nothing. An API that serves no feature flags, or
   * declares no flip method, refuses before any request is sent.
   */
  async setFeature(name: string, enabled: boolean): Promise<FeatureFlip> {
    if (!this.served) {
      throw new Error(notServed);
    }
    const method = this.options.setFeature;
    if (!method) {
      throw new Error(`this API declares no ${setFeatureMethod} method; feature flags cannot be flipped from here`);
    }
    const body: SetFeatureRequest = { name, enabled };
    const flip = await this.options.request<FeatureFlip>('POST', method.route, { body });
    await Promise.all([this.refresh(), this.options.permissions.refresh()]);
    return flip;
  }

  /** Forgets the set — on logout. */
  clear(): void {
    this.snapshot.set(nothing);
  }
}
