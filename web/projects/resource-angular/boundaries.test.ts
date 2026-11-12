import { readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'bun:test';
import { ESLint } from 'eslint';

// The boundary rule of the workspace's lint (web/eslint.config.js), held from the lint
// itself: each case lints one import as if it stood in a file of an entry point and
// counts what the rule says about it (run by `bun test`, which has the node modules the
// Angular spec builder lacks). The rule once judged nothing, because no import between
// entry points resolved to a directory it knew, and the lint of the real files could not
// show that: a rule that refuses nothing passes every file.
const root = join(process.cwd(), 'projects', 'resource-angular');
const scope = '@impulseframework/resource-angular';
const lint = new ESLint({ cwd: process.cwd(), overrideConfigFile: join(root, 'eslint.config.js') });
const limit = 60_000;

// refusals lints `import … from source` as the file of the entry point named and returns
// how many times the boundary rule refuses it.
const refusals = async (file: string, source: string): Promise<number> => {
  const code = `import { probe } from '${source}';\n\nexport const held = probe;\n`;
  const [result] = await lint.lintText(code, { filePath: join(root, file) });
  return result.messages.filter((m) => m.ruleId === 'boundaries/dependencies').length;
};
const entryPoints = (prefix: string): string[] =>
  readdirSync(root, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && entry.name.startsWith(prefix))
    .map((entry) => entry.name);

describe('the boundary rule between entry points', () => {
  it(
    'refuses a UI entry point that imports an auth entry point, by package path and by relative path',
    async () => {
      expect(await refusals('ui-alert/probe.ts', `${scope}/auth-service`)).toBe(1);
      expect(await refusals('ui-alert/probe.ts', '../auth-service/auth.service')).toBe(1);
      expect(await refusals('ui-alert/nested/probe.ts', '../../auth-forms')).toBe(1);
    },
    limit,
  );

  it(
    'refuses the auth service in every UI entry point, in a spec as in a source file',
    async () => {
      const names = entryPoints('ui-');
      expect(names).toContain('ui-alert');
      expect(names.length).toBeGreaterThanOrEqual(2);
      for (const name of names) {
        expect([name, await refusals(`${name}/probe.ts`, `${scope}/auth-service`)]).toEqual([name, 1]);
        expect([name, await refusals(`${name}/probe.spec.ts`, `${scope}/auth-service`)]).toEqual([name, 1]);
      }
    },
    limit,
  );

  it(
    'lets a UI entry point import a UI entry point and the types',
    async () => {
      expect(await refusals('ui-alert/probe.ts', `${scope}/ui-notification-service`)).toBe(0);
      expect(await refusals('ui-alert/probe.ts', `${scope}/types`)).toBe(0);
    },
    limit,
  );

  it(
    'lets every auth entry point import an auth entry point, a UI entry point and the types',
    async () => {
      const names = entryPoints('auth-');
      expect(names).toContain('auth-service');
      for (const name of names) {
        for (const target of ['auth-service', 'auth-forms', 'ui-core-service', 'types']) {
          expect([name, target, await refusals(`${name}/probe.ts`, `${scope}/${target}`)]).toEqual([name, target, 0]);
        }
      }
    },
    limit,
  );

  it(
    'refuses the types an auth entry point and a UI entry point',
    async () => {
      expect(await refusals('types/probe.ts', `${scope}/auth-service`)).toBe(1);
      expect(await refusals('types/probe.ts', `${scope}/ui-core-service`)).toBe(1);
    },
    limit,
  );

  it(
    'counts the idle service and the sidenav among the auth entry points, which a UI entry point may not import',
    async () => {
      expect(entryPoints('auth-')).toEqual(expect.arrayContaining(['auth-idle-service', 'auth-sidenav']));
      expect(await refusals('auth-idle-service/probe.ts', `${scope}/auth-service`)).toBe(0);
      expect(await refusals('auth-sidenav/probe.ts', `${scope}/auth-has-permission`)).toBe(0);
      expect(await refusals('ui-alert/probe.ts', `${scope}/auth-idle-service`)).toBe(1);
      expect(await refusals('ui-core-service/probe.ts', `${scope}/auth-sidenav`)).toBe(1);
    },
    limit,
  );

  it(
    'judges no entry point outside the three groups, as the importer or as the imported',
    async () => {
      expect(await refusals('grid/probe.ts', `${scope}/auth-service`)).toBe(0);
      expect(await refusals('ui-alert/probe.ts', `${scope}/resource-client`)).toBe(0);
      expect(await refusals('types/probe.ts', '@impulseframework/resource')).toBe(0);
      expect(await refusals('ui-alert/probe.ts', '@angular/core')).toBe(0);
    },
    limit,
  );
});
