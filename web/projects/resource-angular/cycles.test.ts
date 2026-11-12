import { existsSync, readdirSync, readFileSync } from 'node:fs';
import { dirname, join, relative } from 'node:path';
import { describe, expect, it } from 'bun:test';
import { ESLint } from 'eslint';

// The import cycle rule (web/eslint.cycles.js), held from the lint of each project that
// takes it. A case finds two files of the project, the first importing the second, and
// lints an import of the first as if it were the whole text of the second: the two then
// import each other, and the rule must say so. The rule once judged nothing in any of the
// three projects, because the plugin read JavaScript files alone and never opened the
// file an import led to, and the lint of the real files could not show that: a rule that
// refuses nothing passes every file.
const projects = join(process.cwd(), 'projects');
const limit = 60_000;

// sources lists the source files under a directory, the specs and the tests left out.
const sources = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      return sources(path);
    }

    return entry.name.endsWith('.ts') && !/\.(spec|test|d)\.ts$/.test(entry.name) ? [path] : [];
  });

// importPair returns two files of a project, the first importing or exporting again a
// value of the second by a relative path. An import of types alone is left out, since the
// rule lets those pass.
const from = /^(?:import|export) (?:\*|\{[^}]*\}) from '(\.{1,2}\/[^']+)';$/gm;
const importPair = (project: string): [string, string] => {
  for (const importer of sources(join(projects, project))) {
    for (const [line, source] of readFileSync(importer, 'utf8').matchAll(from)) {
      const imported = join(dirname(importer), `${source}.ts`);
      if (!/\btype\b/.test(line) && existsSync(imported)) {
        return [importer, imported];
      }
    }
  }

  throw new Error(`${project} has no file that imports another by a relative path`);
};

// cycles lints an import of one file as the whole text of another and returns how many
// times the project's lint reports a cycle.
const cycles = async (project: string, file: string, imports: string): Promise<number> => {
  const lint = new ESLint({ cwd: process.cwd(), overrideConfigFile: join(projects, project, 'eslint.config.js') });
  const source = relative(dirname(file), imports).replace(/\.ts$/, '');
  const code = `import { probe } from '${source.startsWith('.') ? source : `./${source}`}';\n\nexport const held = probe;\n`;
  const [result] = await lint.lintText(code, { filePath: file });

  return result.messages.filter((m) => m.ruleId === 'import-x/no-cycle').length;
};

describe('the import cycle rule', () => {
  for (const project of ['resource', 'resource-firestore', 'resource-angular']) {
    it(
      `refuses two files of ${project} that import each other, and lets an import in one direction pass`,
      async () => {
        const [importer, imported] = importPair(project);
        expect([project, await cycles(project, imported, importer)]).toEqual([project, 1]);
        expect([project, await cycles(project, join(dirname(imported), 'probe.ts'), importer)]).toEqual([project, 0]);
      },
      limit,
    );
  }
});
