// @ts-check
const path = require('node:path');
const eslint = require('@eslint/js');
const importPlugin = require('eslint-plugin-import');
const tseslint = require('typescript-eslint');
const angular = require('angular-eslint');
const boundaries = require('eslint-plugin-boundaries');

module.exports = tseslint.config(
  {
    files: ['**/*.ts'],
    plugins: {
      import: importPlugin,
      boundaries,
    },
    extends: [
      eslint.configs.recommended,
      ...tseslint.configs.recommended,
      ...tseslint.configs.stylistic,
      ...angular.configs.tsRecommended,
    ],
    processor: angular.processInlineTemplates,
    languageOptions: {
      parserOptions: {
        tsconfigRootDir: __dirname,
      },
    },
    rules: {
      'import/no-cycle': ['error', { maxDepth: 1 }],
      // Which entry points of the Angular package may import which. The auth entry points
      // (auth-*) build on the UI entry points (ui-*), and both read the shared types. A UI
      // entry point imports no auth entry point, and the types import neither, so an entry
      // point that needs the session carries the auth name (the idle service and the
      // sidenav do). An entry point outside the three groups is not judged, as the importer
      // or as the imported. boundaries.test.ts holds that the rule refuses what it says here.
      'boundaries/dependencies': [
        2,
        {
          default: 'disallow',
          policies: [
            {
              from: { element: { type: 'auth' } },
              allow: { to: { element: { type: ['auth', 'ui', 'types'] } } },
            },
            {
              from: { element: { type: 'ui' } },
              allow: { to: { element: { type: ['ui', 'types'] } } },
            },
          ],
        },
      ],
    },
    settings: {
      // An entry point imports another by its package path
      // (@impulseframework/resource-angular/auth-service), which tsconfig.json maps to the
      // directory. The lint resolves an import through the same map, so the rule above
      // knows the directory an import lands in. Resolved through node_modules, every such
      // import counted as another package's and none was judged.
      'import/resolver': {
        typescript: { project: path.join(__dirname, 'tsconfig.json') },
      },
      'boundaries/root-path': __dirname,
      'boundaries/elements': [
        { type: 'auth', pattern: 'projects/resource-angular/auth-*', partialMatch: false },
        { type: 'ui', pattern: 'projects/resource-angular/ui-*', partialMatch: false },
        { type: 'types', pattern: 'projects/resource-angular/types', partialMatch: false },
      ],
    },
  },
  {
    files: ['**/*.html'],
    extends: [...angular.configs.templateRecommended, ...angular.configs.templateAccessibility],
    rules: {},
  },
);
