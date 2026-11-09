import { Injectable } from '@angular/core';

@Injectable({ providedIn: 'root' })
export class SwrCacheService {
  private cache = new Map<string, unknown>();

  private readonly MAX_CACHE_SIZE = 1000;

  get<T>(key: string): T | undefined {
    return this.cache.get(key) as T | undefined;
  }

  /** Set or update a cache entry
   *
   * Insertion order is preserved, so deletion of entries is needed
   * both to ensure that existing entries are moved to the 'front' of the cache
   * and to ensure new entries remove the oldest entries to maintain cache size
   */
  set<T>(key: string, value: T): void {
    if (this.cache.has(key)) {
      // Moves the entry to 'front' of cache when set
      this.cache.delete(key);
    }

    if (this.cache.size >= this.MAX_CACHE_SIZE) {
      // Deletes 'oldest' entry
      this.cache.delete(this.cache.keys().next().value!);
    }

    this.cache.set(key, value);
  }

  delete(key: string): void {
    this.cache.delete(key);
  }

  /** Remove all cache entries */
  clear(): void {
    this.cache.clear();
  }
}
