import { ResourceMeta, RowWords } from '@impulseframework/resource-angular/types';

/** What was done to a row, as the message after it says. */
export type RowChange = 'added' | 'saved' | 'deleted';

/** The message for a resource whose metadata carries no word for a row: the resource's name and what was done. */
const withoutWord: Record<RowChange, string> = {
  added: 'created successfully',
  saved: 'updated successfully',
  deleted: 'deleted successfully',
};

/** The opening letters of a word that is written with a vowel and said with a consonant ("a user", "a one-time code"). */
const saidWithConsonant = /^(uni([^nm]|$)|us[aeu]|uti|ute|eu|one|once)/;
/** The opening letters of a word that is written with an h nobody says ("an hour"). */
const saidWithVowel = /^(hour|honest|honor|heir)/;
/** The letters whose names open with a vowel sound, for a word said letter by letter ("an HQ unit", "a URL"). */
const lettersSaidWithVowel = 'AEFHILMNORSX';

/**
 * The words a resource's rows go by, read off its generated metadata; nothing for a
 * resource the application's metadata does not hold, or one that carries no word.
 */
export function rowWordsOf(meta: ResourceMeta | undefined): RowWords | undefined {
  return meta?.rowWords;
}

/**
 * "a" or "an" for the word that follows, by how the word is said aloud: "a hangar pass",
 * "an assignment", "a user", "an hour", and a first word in capitals letter by letter
 * ("an HQ unit", "a URL").
 */
export function articleFor(word: string): 'a' | 'an' {
  const first = word.trim().split(/\s+/)[0] ?? '';
  if (/^[A-Z]{2,}$/.test(first)) {
    return lettersSaidWithVowel.includes(first[0]) ? 'an' : 'a';
  }
  const lower = first.toLowerCase();
  if (saidWithVowel.test(lower)) {
    return 'an';
  }
  if (saidWithConsonant.test(lower)) {
    return 'a';
  }

  return /^[aeiou]/.test(lower) ? 'an' : 'a';
}

/**
 * The label of the button that adds a row to a list, and the heading of the dialog it
 * opens: the label the list's config sets where it sets one, else "Add a hangar pass"
 * from the word the resource's rows go by, else "Create" for a resource whose metadata
 * carries no word.
 */
export function addRowLabel(configured: string | undefined, words: RowWords | undefined): string {
  if (configured) {
    return configured;
  }
  const word = words?.one.trim() ?? '';

  return word !== '' ? `Add ${articleFor(word)} ${word}` : 'Create';
}

/**
 * The message after a row is added, saved or deleted: "Hangar pass deleted." from the
 * word the resource's rows go by, the word capitalized since it opens the sentence. A
 * resource whose metadata carries no word is named as it was before, by `resourceName`
 * ("HangarPasses deleted successfully").
 */
export function rowMessage(words: RowWords | undefined, change: RowChange, resourceName: string): string {
  const word = words?.one.trim() ?? '';
  if (word === '') {
    return `${resourceName} ${withoutWord[change]}`;
  }

  return `${word.charAt(0).toUpperCase()}${word.slice(1)} ${change}.`;
}
