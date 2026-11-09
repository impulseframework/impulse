import { defineConfig } from 'vitest/config';

// The framework's web packages are this checkout's own builds, linked from web/dist by the
// link: dependencies in package.json. Resolved by their real path they would take Angular
// and the client from the framework workspace's node_modules, a second copy beside this
// application's; the symlinked path stays under this node_modules, where there is one. The
// build does the same through preserveSymlinks in angular.json. The packages are inlined so
// Vite's resolver, not Node's, resolves their imports.
export default defineConfig({
  resolve: { preserveSymlinks: true },
  test: { server: { deps: { inline: [/\/@impulseframework\//] } } },
});
