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
// A token that holds a length (how large a title is drawn) is declared with its default
// like every other, and is not the theme's to set: the theme hands over colors.
const isLength = (token: string): boolean => token.endsWith('-size');
// The variables the sheet exposed before the tokens that hold a color: the two the
// components read, and the section label's presets that name a surface (the label reads
// the preset the application picks, white unless it picks another).
const older = [
  '--default-blue',
  '--app-background-color',
  '--layout-label-background-white',
  '--layout-label-background-off-white',
  '--layout-label-background-gray',
];

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

  it('sets every token that holds a color, and the older variables that hold one, from the theme', () => {
    expect([...tokens].filter((t) => !isLength(t) && !themed.has(t))).toEqual([]);
    expect([...themed].filter(isLength)).toEqual([]);
    expect([...themed].filter((t) => !tokens.has(t))).toEqual([]);
    for (const name of older) expect(theme).toMatch(new RegExp(`^\\s*${name}\\s*:\\s*var\\(--mat-sys-`, 'm'));
    for (const line of theme.split('\n').filter((l) => /^\s*--/.test(l)))
      expect(line).toMatch(/var\(--mat-sys-[a-z0-9-]+\)/);
  });

  // The gray preset was first named in another spelling, and an application's stylesheet
  // may still say that name: it stays declared, as the preset's value, so it follows the
  // theme by following the preset and the theme sheet sets the one name.
  it('keeps the gray preset under the name it had first, as the value of the name it has now', () => {
    expect(styles).toMatch(/^\s*--layout-label-background-grey\s*:\s*var\(--layout-label-background-gray\)\s*;/m);
    expect(theme).not.toContain('--layout-label-background-grey');
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

// How large the titles are drawn is declared once, in the styles sheet: a page's title and
// a section's, for a wide screen and for a phone, at the width the frame's bar folds its
// menus away. A section's title is under its page's at both.
describe('the sizes of the titles', () => {
  const sizes = (text: string): Record<string, number> =>
    Object.fromEntries(
      [...text.matchAll(/^\s*(--impulse-(?:page|section)-title-size)\s*:\s*(\d+)px\s*;/gm)].map((m) => [
        m[1],
        Number(m[2]),
      ]),
    );
  const phoneAt = styles.indexOf('@media (max-width: 599.98px)');

  it("declares a page's title at 32 pixels and a section's at 24 on a wide screen", () => {
    expect(phoneAt).toBeGreaterThan(0);
    expect(sizes(styles.slice(0, phoneAt))).toEqual({
      '--impulse-page-title-size': 32,
      '--impulse-section-title-size': 24,
    });
  });

  it('declares them at 24 and 20 on a phone, at the width the frame calls a phone', () => {
    expect(sizes(styles.slice(phoneAt))).toEqual({
      '--impulse-page-title-size': 24,
      '--impulse-section-title-size': 20,
    });
    expect(read(join(root, 'auth-frame', 'layout.ts'))).toContain("phoneWidth = '(max-width: 599.98px)'");
  });

  it('keeps a section under its page at both, and both inside the layer an application overrides', () => {
    const wide = sizes(styles.slice(0, phoneAt));
    const phone = sizes(styles.slice(phoneAt));
    expect(wide['--impulse-section-title-size']).toBeLessThan(wide['--impulse-page-title-size']);
    expect(phone['--impulse-section-title-size']).toBeLessThan(phone['--impulse-page-title-size']);
    // The layer runs from its opening brace to the brace that closes it.
    const layer = styles.indexOf('@layer impulse-defaults');
    let depth = 0;
    let end = -1;
    for (let i = styles.indexOf('{', layer); i < styles.length && end < 0; i++) {
      if (styles[i] === '{') depth++;
      if (styles[i] === '}' && --depth === 0) end = i;
    }
    expect(layer).toBeGreaterThanOrEqual(0);
    expect(layer).toBeLessThan(styles.indexOf('--impulse-page-title-size'));
    expect(styles.lastIndexOf('--impulse-section-title-size')).toBeLessThan(end);
  });

  it('reads a size nowhere but through its token: no stylesheet of a title names a size of its own', () => {
    const headings = read(join(root, 'styles', '_headings.scss'));
    expect(headings).toContain('font-size: var(--impulse-page-title-size);');
    expect(headings).toContain('font-size: var(--impulse-section-title-size);');
    const offenders: string[] = [];
    for (const sheet of sheets(join(root, 'resource'))) {
      if (!read(sheet).includes('headings.')) continue;
      read(sheet)
        .split('\n')
        .forEach((line: string, i: number) => {
          if (/^\s*(\.title|\.page-title|h1)\b/.test(line)) {
            const rule = read(sheet)
              .split('\n')
              .slice(i, i + 12)
              .join('\n')
              .split('}')[0];
            if (/font-size\s*:/.test(rule)) offenders.push(`${sheet.slice(root.length + 1)}:${i + 1}`);
          }
        });
    }
    expect(offenders).toEqual([]);
  });
});

// An icon is never drawn as the letters of its name. The fonts sheet declares the icons'
// font with nothing drawn in its place at first, and draws no icon at all while the root
// element carries the class IconFontService marks it with; the sheet and the service name
// the same family and the same class.
describe('the icons while their font is on its way', () => {
  const fonts = read(join(root, 'styles', '_fonts.scss'));
  const service = read(join(root, 'ui-theme', 'icon-font.service.ts'));
  const named = (constant: string): string => new RegExp(`${constant} = '([^']+)'`).exec(service)?.[1] ?? '';

  it('draws no icon under the class the service marks the root element with', () => {
    const mark = named('AWAITS_ICON_FONT');
    expect(mark).not.toBe('');
    const rule = new RegExp(`\\.${mark} \\.material-icons \\{([^}]*)\\}`).exec(fonts)?.[1] ?? '';
    expect(rule).toMatch(/visibility:\s*hidden;/);
  });

  it('declares the font under the family the service asks for, and draws the icons in it', () => {
    const family = named('ICON_FONT_FAMILY');
    expect(family).not.toBe('');
    const faces = [...fonts.matchAll(/@font-face \{([^}]*)\}/g)]
      .map((m) => m[1])
      .filter((face) => face.includes(`font-family: '${family}';`));
    expect(faces).toHaveLength(1);
    expect(faces[0]).toMatch(/font-display:\s*block;/);
    expect(/\.material-icons \{([^}]*)\}/.exec(fonts)?.[1] ?? '').toContain(`font-family: '${family}';`);
  });
});
