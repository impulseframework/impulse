export type Listener<T> = (value: T) => void;

/**
 * A minimal observable value: the seam a UI framework hooks its reactivity onto
 * (a signal, a hook, a subscription) without the client depending on any of them.
 */
export class Store<T> {
  private listeners = new Set<Listener<T>>();

  constructor(private value: T) {}

  get(): T {
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
