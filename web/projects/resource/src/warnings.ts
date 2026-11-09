/**
 * Receives a warning the client raises about the generated API descriptor: a page asked
 * for something the descriptor says the API does not serve, and the client answered
 * without it. The message names the regeneration that would serve it. `console.warn` by
 * default; an application passes its own through `ClientOptions.warn`.
 */
export type Warn = (message: string) => void;

/** The default warn hook: the console, with the message as written. */
export function consoleWarn(message: string): void {
  console.warn(message);
}

/**
 * Wraps a warn hook so each distinct message reaches it once per client: a page that asks
 * again, or a relation walked again, does not repeat an announcement already made.
 */
export function warnOnce(warn: Warn = consoleWarn): Warn {
  const announced = new Set<string>();
  return (message) => {
    if (announced.has(message)) {
      return;
    }
    announced.add(message);
    warn(message);
  };
}
