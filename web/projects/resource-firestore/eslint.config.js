// @ts-check
const eslint = require('@eslint/js');
const importPlugin = require('eslint-plugin-import');
const tseslint = require('typescript-eslint');

// The feed is framework-neutral like the client it serves: no Angular presets, no Angular imports.
module.exports = tseslint.config({
  files: ['**/*.ts'],
  plugins: {
    import: importPlugin,
  },
  extends: [eslint.configs.recommended, ...tseslint.configs.recommended, ...tseslint.configs.stylistic],
  languageOptions: {
    parserOptions: {
      tsconfigRootDir: __dirname,
    },
  },
  rules: {
    'import/no-cycle': ['error', { maxDepth: 1 }],
    '@typescript-eslint/no-namespace': 'off',
    'no-restricted-imports': [
      'error',
      {
        patterns: [
          {
            group: ['@angular/*', 'rxjs', 'rxjs/*', '@impulseframework/resource-angular', '@impulseframework/resource-angular/*'],
            message:
              '@impulseframework/resource-firestore is framework-neutral: it must not depend on Angular, RxJS, or @impulseframework/resource-angular.',
          },
        ],
      },
    ],
  },
});
