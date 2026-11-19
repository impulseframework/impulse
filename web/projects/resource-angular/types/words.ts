/**
 * A name in words: `DisableCrewSectorUser` is "Disable crew sector user", `userId` is
 * "User id", `cargo_manifest` is "Cargo manifest". What a method, a request field or a
 * feature flag is called on a page that gives it no words of its own.
 */
export function nameInWords(name: string): string {
  const words = name
    .replace(/_+/g, ' ')
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
    .trim()
    .toLowerCase();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * How the library writes a moment a person reads, to the minute: "Nov 17, 2026, 3:42 PM",
 * in the viewer's time zone. A date-fns format, which a column takes as its `formatType`.
 */
export const dayAndMinuteFormat = 'MMM d, yyyy, h:mm a';
