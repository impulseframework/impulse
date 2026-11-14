import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'bun:test';
import ts from 'typescript';
// @ts-expect-error the script is plain JavaScript, run by bun
import { iconNamesSource, shippedIconNames } from '../../../scripts/icon-names.mjs';

// The icon name type is the font's list (scripts/icon-names.mjs), held from two sides: the
// file is what the script writes from the font the library ships, so a font update fails
// here until `bun run icons` is run; and the compiler refuses a name outside it, the
// misspelling the type exists to stop (run by `bun test`, as the lint rules' tests are).
const here = join(process.cwd(), 'projects', 'resource-angular', 'types');

describe('the icon name type', () => {
  it('is the list of the font the library ships', () => {
    expect(readFileSync(join(here, 'icon-names.ts'), 'utf8')).toBe(iconNamesSource(shippedIconNames()));
  });

  it('takes a name the font has and refuses a misspelled one, naming it', () => {
    const probe = join(here, 'probe.ts');
    const source = [
      "import type { IconName } from './icon-names';",
      "export const held: IconName = 'account_circle';",
      "export const misspelled: IconName = 'acount_circle';",
    ].join('\n');
    const options: ts.CompilerOptions = { strict: true, noEmit: true, target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext };
    const host = ts.createCompilerHost(options);
    const read = host.getSourceFile.bind(host);
    host.getSourceFile = (file, language, ...rest) =>
      file === probe ? ts.createSourceFile(file, source, language) : read(file, language, ...rest);
    const program = ts.createProgram([probe], options, host);
    const messages = ts
      .getPreEmitDiagnostics(program)
      .map((d) => ts.flattenDiagnosticMessageText(d.messageText, '\n'));
    expect(messages.length).toBe(1);
    expect(messages[0]).toContain('"acount_circle"');
    expect(messages[0]).toContain("'IconName'");
  });
});
