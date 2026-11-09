// Minimal ambient types for bun's test runner: enough to type-check this project's
// specs with `tsc -p projects/resource/tsconfig.spec.json` where bun's own types are
// not installed. The specs run under `bun test`; the type-check is what makes a
// `@ts-expect-error` assertion in a spec (keyless.types.spec.ts) a test.
declare module 'bun:test' {
  export function describe(name: string, fn: () => void): void;
  export function it(name: string, fn: () => void | Promise<void>): void;

  export interface Matchers<T> {
    toBe(expected: unknown): void;
    toEqual(expected: unknown): void;
    toBeDefined(): void;
    toBeUndefined(): void;
    toBeInstanceOf(expected: abstract new (...args: never[]) => unknown): void;
    toContain(expected: unknown): void;
    toBeGreaterThanOrEqual(expected: number): void;
    toBeLessThanOrEqual(expected: number): void;
    toHaveLength(expected: number): void;
    toStartWith(expected: string): void;
    toThrow(expected?: string | RegExp | Error | (abstract new (...args: never[]) => unknown)): void;
    readonly not: Matchers<T>;
    readonly rejects: Matchers<Awaited<T>>;
    readonly resolves: Matchers<Awaited<T>>;
  }

  export function expect<T>(value: T): Matchers<T>;
}
