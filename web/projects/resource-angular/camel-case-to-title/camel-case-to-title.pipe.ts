import { Pipe, PipeTransform } from '@angular/core';
import { spellAroundPluralAcronyms } from '@impulseframework/resource-angular/types';

/**
 * A field's name as a title, a word for each capital and the first letter in upper case:
 * `shipName` is "Ship Name". A name all in capitals is kept as written. A plural acronym
 * keeps its letters: `crewIDs` is "Crew IDs".
 */
@Pipe({
  name: 'camelCaseToTitle',
})
export class CamelCaseToTitlePipe implements PipeTransform {
  transform(value: string): string {
    if (!value) {
      return value;
    }

    if (value === value.toUpperCase() && !/[0-9]/.test(value)) {
      return value;
    }

    const transformed = spellAroundPluralAcronyms(value, titleWords);
    return transformed.charAt(0).toUpperCase() + transformed.slice(1);
  }
}

/** A piece of a name with no plural acronym, a word for each capital: `userName` is "user Name". */
function titleWords(piece: string): string {
  return piece
    .replace(/([A-Z])/g, ' $1')
    .replace(/([a-zA-Z])([0-9])/g, '$1 $2')
    .trim();
}
