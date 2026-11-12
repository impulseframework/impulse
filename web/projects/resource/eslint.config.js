// @ts-check
const eslint = require('@eslint/js');
const cycles = require('../../eslint.cycles.js');
const tseslint = require('typescript-eslint');

// The client is framework-neutral: no Angular presets, no Angular imports.
module.exports = tseslint.config({
  files: ['**/*.ts'],
  plugins: cycles.plugins,
  extends: [eslint.configs.recommended, ...tseslint.configs.recommended, ...tseslint.configs.stylistic],
  languageOptions: {
    parserOptions: {
      tsconfigRootDir: __dirname,
    },
  },
  settings: cycles.settings,
  rules: {
    ...cycles.rules,
    '@typescript-eslint/no-namespace': 'off',
    'no-restricted-imports': [
      'error',
      {
        patterns: [
          {
            group: ['@angular/*', 'rxjs', 'rxjs/*', '@impulseframework/resource-angular', '@impulseframework/resource-angular/*'],
            message: '@impulseframework/resource is framework-neutral: it must not depend on Angular, RxJS, or @impulseframework/resource-angular.',
          },
        ],
      },
    ],
  },
});
