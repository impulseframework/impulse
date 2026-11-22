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
  const words = nameInLowerWords(name);
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * A name in words inside a sentence, all in lower case but for a plural acronym:
 * `CostCenter` is "cost center", `PartnerAPIs` is "partner APIs".
 */
export function nameInLowerWords(name: string): string {
  return spellAroundPluralAcronyms(name.replace(/_+/g, ' '), lowerWords);
}

/**
 * A name spelled around its plural acronyms: each piece between them is spelled by `spell`,
 * each plural acronym is kept as written, and the pieces that are not empty are joined with
 * one space. It is the one place the browser keeps a plural acronym whole: a name in words
 * and a column's title both spell through it.
 */
export function spellAroundPluralAcronyms(name: string, spell: (piece: string) => string): string {
  const parts: string[] = [];
  let start = 0;
  for (const match of name.matchAll(pluralAcronym)) {
    parts.push(spell(name.slice(start, match.index)), match[0]);
    start = match.index + match[0].length;
  }
  parts.push(spell(name.slice(start)));
  return parts.filter((part) => part !== '').join(' ');
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
