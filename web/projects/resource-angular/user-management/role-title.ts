import { Pipe, PipeTransform } from '@angular/core';

/**
 * The words a role is shown by: its name split at the capitals, so `FeatureAdministrator`
 * reads "Feature Administrator" and `SectorCrew` reads "Sector Crew". A run of capitals
 * stays one word (`HTTPAdmin` reads "HTTP Admin"), a digit stays with the letters before
 * it (`Level2Admin` reads "Level2 Admin"), an underscore or hyphen is a space, and the
 * first letter is a capital. The name is what the server knows the role by; the title is
 * what a person reads on the page.
 */
export function roleTitle(name: string): string {
  const spaced = name
    .replace(/[_-]+/g, ' ')
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/([A-Z]+)([A-Z][a-z])/g, '$1 $2')
    .replace(/\s+/g, ' ')
    .trim();
  return spaced === '' ? '' : spaced[0].toUpperCase() + spaced.slice(1);
}

/**
 * `roleTitle` as a pipe, for a template that shows a role's name as its title:
 *
 *     {{ role.name | roleTitle }}
 */
@Pipe({ name: 'roleTitle' })
export class RoleTitlePipe implements PipeTransform {
  transform(name: string | null | undefined): string {
    return roleTitle(name ?? '');
  }
}
