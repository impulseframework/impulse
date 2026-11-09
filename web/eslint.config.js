// @ts-check
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
      ...boundaries.configs.recommended.rules,
      'import/no-cycle': ['error', { maxDepth: 1 }],
      'boundaries/element-types': [
        2,
        {
          default: 'disallow',
          rules: [
            {
              from: 'auth',
              allow: ['auth', 'ui', 'util', 'types'],
            },
            {
              from: 'ui',
              allow: ['ui', 'util', 'types'],
            },
            {
              from: 'util',
              allow: ['util', 'types'],
            },
          ],
        },
      ],
    },
    settings: {
      'boundaries/elements': [
        {
          type: 'root',
          mode: 'file',
          pattern: '**src/public-api.ts',
        },
        {
          type: 'auth',
          pattern: 'auth-*',
          capture: ['name'],
        },
        {
          type: 'ui',
          pattern: 'ui-*',
          capture: ['name'],
        },
        {
          type: 'util',
          pattern: 'util-*',
          capture: ['name'],
        },
        {
          type: 'types',
          pattern: 'types/*',
          capture: ['name'],
        },
      ],
    },
  },
  {
    files: ['**/*.html'],
    extends: [...angular.configs.templateRecommended, ...angular.configs.templateAccessibility],
    rules: {},
  },
);
