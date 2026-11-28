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
 * A role's name in words, as the server's sentences name a role: `NightShiftLead` is
 * "Night shift lead". A role's name holds no space, so what follows one, the words a line
 * says where the role is held in, is kept as written. The one place the browser spells a
 * role.
 */
export function roleInWords(text: string): string {
  const space = text.indexOf(' ');
  if (space < 0) {
    return nameInWords(text);
  }
  return nameInWords(text.slice(0, space)) + text.slice(space);
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

/**
 * How a cell writes a moment (a `date`, a `time.Time` on the server): the day and the time
 * of day, in the viewer's time zone (`11/23/26 3:15 PM`). A day alone (a `civildate`) is
 * written M/d/yyyy. The year is written with two digits so that the longest moment is
 * seventeen characters, which a column at the narrowest a column of text goes shows whole
 * on a phone. A moment whose year two digits would not tell is written with
 * FAR_MOMENT_CELL_FORMAT (momentFormat).
 */
export const MOMENT_CELL_FORMAT = 'M/d/yy h:mm a';

/**
 * How a list writes a moment whose year two digits would not tell: with the whole year
 * (`11/23/2126 3:15 PM`).
 */
export const FAR_MOMENT_CELL_FORMAT = 'M/d/yyyy h:mm a';

/**
 * How many years from this one two digits still tell a year. A moment whose year is this far
 * off or further, before or after, is written with its whole year.
 */
export const TWO_DIGIT_YEAR_REACH = 50;

/**
 * The date-fns format a list writes the moment in, as read at `now`: MOMENT_CELL_FORMAT, or
 * FAR_MOMENT_CELL_FORMAT for a moment whose year is TWO_DIGIT_YEAR_REACH or more from the year
 * of `now`, both on the viewer's calendar. A lock that no time ends is kept as a moment a
 * hundred years off, and two digits would write its year as this one's.
 */
export function momentFormat(moment: Date, now: number): string {
  if (Math.abs(moment.getFullYear() - new Date(now).getFullYear()) >= TWO_DIGIT_YEAR_REACH) {
    return FAR_MOMENT_CELL_FORMAT;
  }
  return MOMENT_CELL_FORMAT;
}
