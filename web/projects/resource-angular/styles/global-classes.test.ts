import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'bun:test';

// The styles sheet gives every page of an application a few classes under plain names, for
// a resource's own pages: `.resource` is a flex row, `.field` is as wide as its line,
// `.row` wraps, `.col-6` is half of it. A class in that sheet is not bound to a component:
// it reaches inside every one. So a component that draws something else and names a class
// the same is laid out by the sheet: a table cell named `resource` becomes a flex row, is
// no longer a cell of its row, and ends on a line of its own, a pixel off the row's.
//
// The permissions card draws a table of its own, so it names none of them. This is held
// from the files themselves (run by `bun test`, which has the node modules the Angular
// spec builder lacks).
const root = join(process.cwd(), 'projects', 'resource-angular');
const read = (path: string): string => String(readFileSync(path, 'utf8'));

/**
 * The classes the styles sheet declares, as the names a page is styled by. A name the
 * sheet writes with a number it counts (`.col-#{$i}`, `.row-height-#{$j*4}`) is every
 * name that begins so, and ends here with its hyphen.
 */
const globalClasses = (): string[] => {
  const names = new Set<string>();
  for (const line of read(join(root, 'styles', '_index.scss')).split('\n')) {
    // A selector's line opens a block or goes on to the next selector; a declaration's
    // line (`flex: 0 0 50%;`) is not one, whatever dots its value holds.
    if (/^\s*\/\//.test(line) || !/[{,]\s*$/.test(line)) continue;
    for (const m of line.matchAll(/\.([a-zA-Z][a-zA-Z0-9_-]*)/g)) names.add(m[1]);
  }
  return [...names];
};

/** The classes a component names: in its templates (`class="…"`, `[class.name]`) and in its own stylesheets. */
const componentClasses = (dir: string): string[] => {
  const names = new Set<string>();
  for (const file of readdirSync(dir)) {
    const text = read(join(dir, file));
    if (file.endsWith('.html')) {
      for (const m of text.matchAll(/\bclass="([^"]*)"/g))
        for (const name of m[1].split(/\s+/)) if (name) names.add(name);
      for (const m of text.matchAll(/\[class\.([a-zA-Z0-9_-]+)\]/g)) names.add(m[1]);
    }
    if (file.endsWith('.scss')) {
      for (const line of text.split('\n')) {
        if (/^\s*\/\//.test(line) || !/[{,]\s*$/.test(line)) continue;
        for (const m of line.matchAll(/\.([a-zA-Z][a-zA-Z0-9_-]*)/g)) names.add(m[1]);
      }
    }
  }
  return [...names];
};

/** Whether the styles sheet styles the class: by its whole name, or by a name the sheet counts out. */
const styledByTheSheet = (name: string, sheet: string[]): boolean =>
  sheet.some((global) => (global.endsWith('-') ? name.startsWith(global) : name === global));

describe("the styles sheet's classes for a resource's pages", () => {
  it('are read from the sheet: a flex row, a field, a wrapping row, a share of twelve columns', () => {
    const sheet = globalClasses();
    for (const name of ['resource', 'field', 'row', 'col-', 'row-height-']) expect(sheet).toContain(name);
  });

  it('are named by no element of the permissions card, which draws a table of its own', () => {
    const sheet = globalClasses();
    const card = componentClasses(join(root, 'ui-permissions-card'));
    // The card is read at all: these are the classes its layout stands on.
    for (const name of ['frame', 'permissions', 'blocks', 'held', 'pill']) expect(card).toContain(name);
    expect(card.filter((name) => styledByTheSheet(name, sheet))).toEqual([]);
  });
});
