import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'bun:test';

// The theming contract the README states, held from the stylesheets themselves (run by
// `bun test`, which has the node modules the Angular spec builder lacks): every
// color a component draws with is a token the styles sheet declares with a default, the
// theme sheet sets every one of those tokens from the Material system tokens, and no
// component stylesheet carries a literal color of its own (a literal is allowed only as
// the fallback of a Material token, for a page with no theme at all).
const root = join(process.cwd(), 'projects', 'resource-angular');
const sheets = (dir: string): string[] =>
  readdirSync(dir).flatMap((name: string) => {
    const path = join(dir, name);
    if (name === 'node_modules') return [];
    if (statSync(path).isDirectory()) return sheets(path);
    return name.endsWith('.scss') ? [path] : [];
  });
const declared = (text: string): Set<string> =>
  new Set([...text.matchAll(/^\s*(--impulse-[a-z0-9-]+)\s*:/gm)].map((m) => m[1]));
const read = (path: string): string => String(readFileSync(path, 'utf8'));
const styles = read(join(root, 'styles', '_index.scss'));
const theme = read(join(root, 'styles', '_theme.scss'));
const tokens = declared(styles);
const themed = declared(theme);
const older = ['--default-blue', '--app-background-color'];

describe('the theming contract', () => {
  it('declares every token the components read, with a default', () => {
    const used = new Set<string>();
    for (const sheet of sheets(root)) {
      // A token read with a fallback (`var(--impulse-grid-cell-icon-color, inherit)`) is a
      // per-element override an application sets where it wants it, not a theme token.
      for (const m of read(sheet).matchAll(/var\((--impulse-[a-z0-9-]+)\)/g)) used.add(m[1]);
    }
    expect([...used].filter((t) => !tokens.has(t))).toEqual([]);
    expect([...tokens].filter((t) => !used.has(t))).toEqual([]);
  });

  it('sets every token, and the two older variables, from the theme', () => {
    expect([...tokens].filter((t) => !themed.has(t))).toEqual([]);
    expect([...themed].filter((t) => !tokens.has(t))).toEqual([]);
    for (const name of older) expect(theme).toMatch(new RegExp(`^\\s*${name}\\s*:\\s*var\\(--mat-sys-`, 'm'));
    for (const line of theme.split('\n').filter((l) => /^\s*--/.test(l)))
      expect(line).toMatch(/var\(--mat-sys-[a-z0-9-]+\)/);
  });

  it('leaves no literal color in a component stylesheet', () => {
    const literal = /#[0-9a-f]{3,8}\b|rgba?\(|:\s*(?:lightgray|lightgrey|gray|grey|red|green|blue|white|black)\s*[;!]/i;
    const offenders: string[] = [];
    for (const sheet of sheets(root)) {
      // The styles sheet declares the defaults, and the palettes sheet is palettes: literal by nature.
      if (sheet.endsWith(join('styles', '_index.scss')) || sheet.endsWith(join('styles', '_palettes.scss'))) continue;
      // A fallback inside var(--mat-…) may run over several lines; the depth of its
      // parentheses says which lines belong to it.
      let depth = 0;
      const lines = read(sheet).split('\n');
      lines.forEach((line: string, i: number) => {
        const opens = /var\(\s*--mat-/.test(line) || (/var\(\s*$/.test(line) && /^\s*--mat-/.test(lines[i + 1] ?? ''));
        const inFallback = depth > 0 || opens;
        if (literal.test(line) && !inFallback && !/^\s*\/\//.test(line))
          offenders.push(`${sheet.slice(root.length + 1)}:${i + 1}: ${line.trim()}`);
        if (inFallback)
          depth = Math.max(0, depth + (line.match(/\(/g) ?? []).length - (line.match(/\)/g) ?? []).length);
      });
    }
    expect(offenders).toEqual([]);
  });
});
