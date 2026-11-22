/**
 * A plural acronym: a run, as long as it goes, of two or more capital letters, followed at
 * once by a lower-case s that ends the name or is followed by anything but a lower-case
 * letter.
 */
const pluralAcronym = /(?<![A-Z])[A-Z]{2,}s(?![a-z])/g;

/**
 * A name in words: `DisableCrewSectorUser` is "Disable crew sector user", `userId` is
 * "User id", `cargo_manifest` is "Cargo manifest". What a method, a request field or a
 * feature flag is called on a page that gives it no words of its own. A plural acronym
 * keeps its letters: `crewIDs` is "Crew IDs".
 */
export function nameInWords(name: string): string {
  const spaced = name.replace(/_+/g, ' ');
  const parts: string[] = [];
  let start = 0;
  for (const match of spaced.matchAll(pluralAcronym)) {
    parts.push(lowerWords(spaced.slice(start, match.index)), match[0]);
    start = match.index + match[0].length;
  }
  parts.push(lowerWords(spaced.slice(start)));
  const words = parts.filter((part) => part !== '').join(' ');
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/** A name that holds no plural acronym in words, all in lower case. */
function lowerWords(name: string): string {
  return name
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
    .trim()
    .toLowerCase();
}

/**
 * How the library writes a moment a person reads, to the minute: "Nov 17, 2026, 3:42 PM",
 * in the viewer's time zone. A date-fns format, which a column takes as its `formatType`.
 */
export const dayAndMinuteFormat = 'MMM d, yyyy, h:mm a';
