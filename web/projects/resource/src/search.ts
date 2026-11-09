// The browser's half of the search rules: the limits the server puts on the box's text,
// and the folding and the match the server's in-memory evaluator applies, for a picker
// that narrows a source read whole in the browser.

/** The server's limits on the search box's text: terms, characters per term, and characters in all. */
export const searchLimits = { terms: 8, termLength: 64, textLength: 200 } as const;

/**
 * Text as the search compares it, the way the server's in-memory evaluator folds a cell
 * and a typed term alike: lowercased, apostrophes removed, and every other run of
 * punctuation or space one space, so "O'Brien" is "obrien" and "Pat  O'Brien-Nguyen" is
 * "pat obrien nguyen". Spanner's substring tokenizer folds the same way on every case the
 * proof exercised. Accents stay: a field folds them only where its schema says so, and
 * the browser does not know which.
 */
export function foldSearchText(text: string): string {
  let out = '';
  let space = false;
  for (const ch of text) {
    if (ch === "'" || ch === '’') {
      continue;
    }
    if (/[\p{L}\p{N}]/u.test(ch)) {
      if (space && out !== '') {
        out += ' ';
      }
      space = false;
      out += ch.toLowerCase();
    } else {
      space = true;
    }
  }
  return out;
}

/** The terms of a box's text: split at whitespace; none for blank text. */
export function searchTerms(text: string): string[] {
  return text.split(/\s+/).filter((term) => term !== '');
}

/**
 * Whether a row answers the box: every term of the text is found, folded, somewhere in
 * one of the values, folded. Blank text matches every row. This is the server's rule for
 * a resource it searches in memory, applied in the browser to a picker over a source read
 * whole; a digits or date field meets it as text, since the browser does not know a
 * field's role.
 */
export function matchesSearch(
  values: readonly (string | number | boolean | null | undefined)[],
  text: string,
): boolean {
  const terms = searchTerms(text)
    .map(foldSearchText)
    .filter((term) => term !== '');
  if (terms.length === 0) {
    return true;
  }
  const folded = values.map((value) => foldSearchText(value === null || value === undefined ? '' : String(value)));
  return terms.every((term) => folded.some((value) => value.includes(term)));
}
