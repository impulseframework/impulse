export const concatFunctions = {
  'space-concat': spaceConcat,
  'hyphen-concat': hyphenConcat,
  'space-hyphen-concat': spaceHyphenConcat,
  'hyphen-space-concat': hyphenSpaceConcat,
};

/**
 * Concatenates the strings corresponding to the given keys in the resource,
 * using a space separator.
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
  return args.map((arg) => resource[arg] ?? '').join(' ');
}

/**
 * Concatenates the strings corresponding to the given keys in the resource,
 * using a hyphen separator.
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
  return args.map((arg) => resource[arg] ?? '').join(' - ');
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

  const initialPart = args
    .slice(0, -1)
    .map((arg) => resource[arg] || '')
    .join(' ');

  const lastPart = resource[args[args.length - 1] || ''] || '';

  return `${initialPart} - ${lastPart}`;
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

  const lastPart = args
    .slice(1, args.length)
    .map((arg) => resource[arg] || '')
    .join(' ');

  return `${initialPart} - ${lastPart}`;
}

/**
 * Concatenates the strings using a space separator.
 * @param args Strings to concatenate.
 * @returns Concatenated string.
 * @example
 * spaceConcatWithoutResource(['foo', 'bar', 'baz']); // returns 'foo bar baz'
 */
export function spaceConcatWithoutResource(args: string[]): string {
  if (args.length === 0) return '';
  if (args.length === 1) return args[0] ?? '';
  return args.map((arg) => arg ?? '').join(' ');
}

/**
 * Concatenates the strings using a hyphen separator.
 * @param args Strings to concatenate.
 * @returns Concatenated string.
 * @example
 * hyphenConcatWithoutResource(['foo', 'bar', 'baz']); // returns 'foo - bar - baz'
 */
export function hyphenConcatWithoutResource(args: string[]): string {
  if (args.length === 0) return '';
  if (args.length === 1) return args[0] || '';
  return args.join(' - ');
}

/**
 * Concatenates the given strings so that all but the last are joined with a space,
 * and the last string is appended with a " - " separator.
 * @param args Strings to concatenate.
 * @returns Concatenated string.
 * @example spaceHyphenConcatWithoutResource(['foo', 'bar', 'baz']) => 'foo bar - baz'
 */
export function spaceHyphenConcatWithoutResource(args: string[]): string {
  if (args.length === 0) return '';
  if (args.length === 1) return args[0] || '';

  const initialPart = args
    .slice(0, -1)
    .map((arg) => arg || '')
    .join(' ');

  const lastPart = args[args.length - 1] || '';

  return `${initialPart} - ${lastPart}`;
}

/**
 * Concatenates the given strings so that all but the first are joined with a space,
 * and the first string is appended with a " - " separator.
 * @param args Strings to concatenate.
 * @returns Concatenated string.
 * @example hyphenSpaceConcatWithoutResource(['foo', 'bar', 'baz']) => 'foo - bar baz'
 */
export function hyphenSpaceConcatWithoutResource(args: string[]): string {
  if (args.length === 0) return '';
  if (args.length === 1) return args[0] || '';

  const initialPart = args[0] || '';

  const lastPart = args
    .slice(1, args.length)
    .map((arg) => arg || '')
    .join(' ');

  return `${initialPart} - ${lastPart}`;
}

/**
 * Concatenates the given strings without any space,
 * @param args Strings to concatenate.
 * @returns Concatenated string.
 * @example noSpaceConcatWithoutResource(['foo', 'bar', 'baz']) => 'foobarbaz'
 */
export function noSpaceConcatWithoutResource(args: string[]): string {
  if (args.length === 0) return '';
  if (args.length === 1) return args[0] || '';
  return args.join('');
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
