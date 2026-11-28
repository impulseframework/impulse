export const concatFunctions = {
  'space-concat': spaceConcat,
  'hyphen-concat': hyphenConcat,
  'space-hyphen-concat': spaceHyphenConcat,
  'hyphen-space-concat': hyphenSpaceConcat,
};

/**
 * The values a row has for the given keys, in the keys' order, without the ones it has
 * none for: a key the row leaves empty joins nothing, so no separator is left standing
 * beside a value that is not there ("marshal", never " - marshal").
 */
function present(resource: Record<string, string>, args: string[]): string[] {
  return args.map((arg) => resource[arg] ?? '').filter((part) => part !== '');
}

/**
 * Concatenates the strings corresponding to the given keys in the resource,
 * using a space separator. A key the resource has no value for is left out.
 * @param resource A record mapping keys to string values.
 * @param args Keys of the resource to concatenate.
 * @returns Concatenated string.
 * @example
 * const resource = { foo: 'foo', bar: 'bar', baz: 'baz' };
 * spaceConcat(resource, 'foo', 'bar', 'baz'); // returns 'foo bar baz'
 */
export function spaceConcat(resource: Record<string, string>, ...args: string[]): string {
  if (args.length === 0) return '';
  if (args.length === 1) return resource[args[0] ?? ''] ?? '';
  return present(resource, args).join(' ');
}

/**
 * Concatenates the strings corresponding to the given keys in the resource,
 * using a hyphen separator. A key the resource has no value for is left out, with its
 * hyphen: a person with a login and no name reads 'marshal', never ' - marshal'.
 * @param resource A record mapping keys to string values.
 * @param args Keys of the resource to concatenate.
 * @returns Concatenated string.
 * @example
 * const resource = { foo: 'foo', bar: 'bar', baz: 'baz' };
 * hyphenConcat(resource, 'foo', 'bar', 'baz'); // returns 'foo - bar - baz'
 */
export function hyphenConcat(resource: Record<string, string>, ...args: string[]): string {
  if (args.length === 0) return '';
  if (args.length === 1) return resource[args[0] ?? ''] ?? '';
  return present(resource, args).join(' - ');
}

/**
 * Concatenates the given strings so that all but the last are joined with a space,
 * and the last string is appended with a " - " separator.
 * @param resource A mapping of keys to string values.
 * @param args Keys of strings to concatenate.
 * @returns Concatenated string.
 * @example spaceHyphenConcat(resource, 'foo', 'bar', 'baz') => 'foo bar - baz'
 */
export function spaceHyphenConcat(resource: Record<string, string>, ...args: string[]): string {
  if (args.length === 0) return '';
  if (args.length === 1) return resource[args[0] || ''] || '';

  const initialPart = present(resource, args.slice(0, -1)).join(' ');

  const lastPart = resource[args[args.length - 1] || ''] || '';

  // The hyphen stands between two parts that are both there.
  return [initialPart, lastPart].filter((part) => part !== '').join(' - ');
}

/**
 * Concatenates the given strings so that all but the first are joined with a space,
 * and the first string is appended with a " - " separator.
 * @param resource A mapping of keys to string values.
 * @param args Keys of strings to concatenate.
 * @returns Concatenated string.
 * @example spaceHyphenConcat(resource, 'foo', 'bar', 'baz') => 'foo - bar baz'
 */
export function hyphenSpaceConcat(resource: Record<string, string>, ...args: string[]): string {
  if (args.length === 0) return '';
  if (args.length === 1) return resource[args[0] || ''] || '';

  const initialPart = args[0] !== undefined ? resource[args[0]] || '' : '';

  const lastPart = present(resource, args.slice(1, args.length)).join(' ');

  // The hyphen stands between two parts that are both there.
  return [initialPart, lastPart].filter((part) => part !== '').join(' - ');
}

/**
 * Converts a string to camelCase.
 * @param str The string to convert.
 * @returns The camelCase version of the string.
 * @example camelCase('hello-world') => 'helloWorld'
 * @example camelCase('HelloWorld') => 'helloWorld'
 * @example camelCase('hello_world') => 'helloWorld'
 */
export function camelCase(str: string): string {
  if (!str) return '';
  return str.replace(/[-_\s]+(.)?/g, (_, c) => (c ? c.toUpperCase() : '')).replace(/^[A-Z]/, (c) => c.toLowerCase());
}
