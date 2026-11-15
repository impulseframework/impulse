export type Listener<T> = (value: T) => void;

/** What a UI framework is told each time a store's value is read (setStoreReadHook). */
export type StoreReadHook = (store: Store<unknown>) => void;

let readHook: StoreReadHook | undefined;

/**
 * Sets the hook every store read calls, or clears it with undefined. A UI framework sets
 * it once, so that a read made inside its reactive code registers the store there: the
 * Angular client (provideResourceClient) makes a permission question, a feature flag or
 * the session asked inside computed(), effect() or a template ask again when the store
 * changes. The client itself stays framework-agnostic: without a hook a read is a read.
 */
export function setStoreReadHook(hook: StoreReadHook | undefined): void {
  readHook = hook;
}

/**
 * A minimal observable value: the seam a UI framework hooks its reactivity onto
 * (a signal, a hook, a subscription) without the client depending on any of them.
 */
export class Store<T> {
  private listeners = new Set<Listener<T>>();

  constructor(private value: T) {}

  /** The value now. The read is told to the hook a UI framework set (setStoreReadHook). */
  get(): T {
    readHook?.(this as Store<unknown>);
    return this.value;
  }

  set(value: T): void {
    if (Object.is(value, this.value)) {
      return;
    }
    this.value = value;
    for (const listener of [...this.listeners]) {
      listener(value);
    }
  }

  update(fn: (value: T) => T): void {
    this.set(fn(this.value));
  }

  /** Registers a listener for future values; returns the unsubscribe function. */
  subscribe(listener: Listener<T>): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }
}
