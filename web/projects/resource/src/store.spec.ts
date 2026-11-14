import { describe, expect, it } from 'bun:test';
import { setStoreReadHook, Store } from './store';

describe('the store read hook', () => {
  it('tells the hook of every read, with the store read', () => {
    const store = new Store(1);
    const read: Store<unknown>[] = [];
    setStoreReadHook((s) => read.push(s));
    expect(store.get()).toBe(1);
    expect(store.get()).toBe(1);
    setStoreReadHook(undefined);
    expect(read).toEqual([store as Store<unknown>, store as Store<unknown>]);
  });

  it('is not told of a write, and a read without a hook is a read', () => {
    const store = new Store(1);
    const read: Store<unknown>[] = [];
    setStoreReadHook((s) => read.push(s));
    store.set(2);
    store.update((n) => n + 1);
    expect(read).toEqual([]);
    setStoreReadHook(undefined);
    expect(store.get()).toBe(3);
    expect(read).toEqual([]);
  });
});
