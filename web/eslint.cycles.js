// @ts-check
const path = require('node:path');
const importX = require('eslint-plugin-import-x');
const { createTypeScriptImportResolver } = require('eslint-import-resolver-typescript');

// The import cycle rule, as each project's lint settings take it: two files that import
// each other are refused. With the rule come the two things the plugin needs to follow an
// import into a TypeScript file: the resolver, which finds a file imported with no
// extension or by a path of tsconfig.json, and the extensions it reads. It reads
// JavaScript files alone unless told otherwise, so without them the rule ran and judged
// nothing. cycles.test.ts holds that it refuses a cycle in each project.
module.exports = {
  plugins: { 'import-x': importX },
  rules: { 'import-x/no-cycle': ['error', { maxDepth: 1 }] },
  settings: {
    'import-x/extensions': ['.ts'],
    'import-x/resolver-next': [createTypeScriptImportResolver({ project: path.join(__dirname, 'tsconfig.json') })],
  },
};
