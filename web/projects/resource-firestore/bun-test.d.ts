// Minimal ambient types for bun's test runner: enough to type-check this project's
// specs with `tsc -p projects/resource-firestore/tsconfig.spec.json` where bun's own
// types are not installed. The specs run under `bun test`; the type-check keeps them
// honest against the feed's signatures and the SDK's.
declare module 'bun:test' {
  export function describe(name: string, fn: () => void): void;
  export function it(name: string, fn: () => void | Promise<void>): void;
  export function beforeEach(fn: () => void | Promise<void>): void;
  export function afterEach(fn: () => void | Promise<void>): void;

  export interface Matchers<T> {
    toBe(expected: unknown): void;
    toEqual(expected: unknown): void;
    toBeDefined(): void;
    toBeUndefined(): void;
    toBeInstanceOf(expected: abstract new (...args: never[]) => unknown): void;
    toContain(expected: unknown): void;
    toHaveLength(expected: number): void;
    toStartWith(expected: string): void;
    toThrow(expected?: string | RegExp | Error | (abstract new (...args: never[]) => unknown)): void;
    readonly not: Matchers<T>;
    readonly rejects: Matchers<Awaited<T>>;
    readonly resolves: Matchers<Awaited<T>>;
  }

  export function expect<T>(value: T): Matchers<T>;

  /** Replaces a module for every importer with what the factory returns; live bindings follow. */
  export const mock: {
    module(specifier: string, factory: () => unknown): void;
  };
}
