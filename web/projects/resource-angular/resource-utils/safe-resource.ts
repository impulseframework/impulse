import { httpResource, HttpResourceOptions, HttpResourceRef } from '@angular/common/http';
import {
  computed,
  effect,
  inject,
  linkedSignal,
  ResourceRef,
  ResourceSnapshot,
  Signal,
  untracked,
} from '@angular/core';
import { rxResource, RxResourceOptions } from '@angular/core/rxjs-interop';
import { SwrCacheService } from './swr-cache.service';

export interface SafeResourceRef<T> {
  safeValue: Signal<T | undefined>;
  resource: ResourceRef<T | undefined>;
}

/**
 * Creates a {@link SafeResourceRef} that wraps an HTTP resource.
 *
 *
 * The purpose of this utility function is to prevent the resource from dropping the entire application
 * into an exception state when the resource.value() is read after it fails to load.
 *
 * If it does not have a value, it will return `undefined` (or the provided `defaultValue` if specified).
 */
export function safeHttpResource<T>(
  url: () => string | undefined,
  options?: HttpResourceOptions<T, unknown> | undefined,
  defaultValue?: T,
): SafeResourceRef<T> {
  const resource = httpResource<T>(url, options);
  const safeValue = computed(() => {
    if (!resource.hasValue()) {
      return defaultValue;
    }
    return resource.value();
  });
  return { safeValue, resource: resource as HttpResourceRef<T | undefined> };
}

/**
 * Creates a {@link SafeResourceRef} that wraps an RxJS resource.
 *
 * The purpose of this utility function is to prevent the resource from dropping the entire application
 * into an exception state when the resource.value() is read after it fails to load.
 *
 * If it does not have a value, it will return `undefined` (or the provided `defaultValue` if specified).
 */
export function safeRxResource<T, A = unknown>(options: RxResourceOptions<T, A>, defaultValue?: T): SafeResourceRef<T> {
  const resource = rxResource<T, A>(options);

  const safeValue = computed(() => {
    if (!resource.hasValue()) {
      return defaultValue;
    }
    return resource.value();
  });
  return { safeValue, resource: resource as ResourceRef<T | undefined> };
}

/**
 * Creates a {@link SafeResourceRef} that wraps an HTTP resource and preserves its previous value when it enters a loading state.
 *
 * The purpose of this utility function is to prevent the resource from dropping the entire application
 * into an exception state when the resource.value() is read after it fails to load, while also preserving the previous value during loading.
 *
 */
export function staleHttpResource<T>(
  url: () => string | undefined,
  options?: HttpResourceOptions<T, unknown> | undefined,
  defaultValue?: T,
): SafeResourceRef<T> {
  const resource = httpResource<T>(url, options);
  const safeValue = linkedSignal<ResourceSnapshot<T | undefined>, T | undefined>({
    source: resource.snapshot as Signal<ResourceSnapshot<T | undefined>>,
    computation: (snap, previous) => {
      if (snap.status === 'resolved' || snap.status === 'local') {
        return (snap.value as T) ?? defaultValue;
      }
      // Hold the last good value (SWR)
      if (snap.status === 'loading' || snap.status === 'reloading') {
        return previous?.value ?? defaultValue;
      }
      return defaultValue;
    },
  });
  return { safeValue, resource: resource as HttpResourceRef<T | undefined> };
}

/**
 * Creates a {@link SafeResourceRef} that wraps an RxJS resource and preserves its previous value when it enters a loading state.
 *
 * The purpose of this utility function is to prevent the resource from dropping the entire application
 * into an exception state when the resource.value() is read after it fails to load, while also preserving the previous value during loading.
 *
 */
export function staleRxResource<T, A = unknown>(
  options: RxResourceOptions<T, A>,
  defaultValue?: T,
): SafeResourceRef<T> {
  const resource = rxResource<T, A>(options);

  const safeValue = linkedSignal<ResourceSnapshot<T | undefined>, T | undefined>({
    source: resource.snapshot as Signal<ResourceSnapshot<T | undefined>>,
    computation: (snap, previous) => {
      if (snap.status === 'resolved' || snap.status === 'local') {
        return (snap.value as T) ?? defaultValue;
      }
      // Hold the last good value (SWR)
      if (snap.status === 'loading' || snap.status === 'reloading') {
        return previous?.value ?? defaultValue;
      }
      return defaultValue;
    },
  });
  return { safeValue, resource: resource as ResourceRef<T | undefined> };
}

/**
 * Creates a {@link SafeResourceRef} that wraps an HTTP resource and implements a
 * 'stale while revalidate' caching strategy
 *
 * This wrapper exhibits the following behaviors
 *
 * When the primary resource's `hasValue()` is false
 * - If the global cache service contains this resource's data, the cached data is immediately returned.
 * - If not in cache, the input `defaultValue` is returned (may be `undefined`)
 *
 * When the primary resource's `hasValue()` is true
 * Once the HTTP resource has data, the `safeValue` signal updates with
 * the new value. Any consumers of the `safeValue` computed() will receive fresh data.
 * A side effect updates the global cache with the fresh value
 */
export function swrHttpResource<T>(
  urlFn: () => string | undefined,
  options?: HttpResourceOptions<T, unknown> | undefined,
  defaultValue?: T,
): SafeResourceRef<T> {
  const cache = inject(SwrCacheService);
  const resource = httpResource<T>(urlFn, options);

  effect(() => {
    const url = urlFn();
    if (url !== undefined && resource.hasValue()) {
      const value = resource.value();
      untracked(() => cache.set(url, value));
    }
  });

  const safeValue = computed(() => {
    const url = urlFn();
    if (url === undefined) {
      return defaultValue;
    }

    if (resource.hasValue()) {
      return resource.value();
    }

    const cached = cache.get(url) as T | undefined;
    if (cached !== undefined) {
      return cached;
    }

    return defaultValue;
  });
  return { safeValue, resource: resource as HttpResourceRef<T | undefined> };
}

/**
 * Creates a {@link SafeResourceRef} that wraps an RxJS resource and implements a
 * 'stale while revalidate' caching strategy.
 *
 * An input cache key is required since data cannot be inferred from input parameters alone
 *
 * This wrapper exhibits the following behaviors
 *
 * When the primary resource's `hasValue()` is false
 * - If the global cache service contains this resource's data, the cached data is immediately returned.
 * - If not in cache, the input `defaultValue` is returned (may be `undefined`)
 *
 * When the primary resource's `hasValue()` is true
 * Once the RxJS resource has data, the `safeValue` signal updates with
 * the new value. Any consumers of the `safeValue` computed() will receive fresh data.
 * A side effect updates the global cache with the fresh value
 */
export function swrRxResource<T, A = unknown>(
  cacheKey: string,
  options: RxResourceOptions<T, A>,
  defaultValue?: T,
): SafeResourceRef<T> {
  const cache = inject(SwrCacheService);
  const resource = rxResource<T, A>(options);
  const fullCacheKey = computed(() => {
    if (options.params) {
      const params = options.params();
      return params !== undefined ? cacheKey + JSON.stringify(params) : cacheKey;
    }
    return cacheKey;
  });

  effect(() => {
    if (resource.hasValue()) {
      const key = fullCacheKey();
      const value = resource.value();
      untracked(() => cache.set(key, value));
    }
  });

  const safeValue = computed(() => {
    if (resource.hasValue()) {
      return resource.value();
    }

    const cached = cache.get(fullCacheKey()) as T | undefined;
    if (cached !== undefined) {
      return cached;
    }

    return defaultValue;
  });

  return { safeValue, resource: resource as ResourceRef<T | undefined> };
}
