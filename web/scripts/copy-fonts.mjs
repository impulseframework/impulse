// Copies the fonts @impulseframework/resource-angular ships (projects/resource-angular/styles/_fonts.scss)
// from the two font packages the library builds with into the package's fonts folder, which
// ng-packagr carries into the package as assets: Roboto in the three weights the Material
// typography uses, in its latin and latin-ext subsets, and the filled Material Icons, each
// with its licence. The root build script runs it before `ng build resource-angular`; the
// folder is generated, never committed. An application gains no dependency on the font
// packages; a font update is a release of this library.
import { copyFileSync, mkdirSync, rmSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const dest = join(root, 'projects', 'resource-angular', 'fonts');
const roboto = join(root, 'node_modules', '@fontsource', 'roboto');
const icons = join(root, 'node_modules', 'material-icons');

const files = [
  ...['latin', 'latin-ext'].flatMap((subset) =>
    [300, 400, 500].map((weight) => {
      const name = `roboto-${subset}-${weight}-normal.woff2`;
      return [join(roboto, 'files', name), name];
    }),
  ),
  [join(roboto, 'LICENSE'), 'LICENSE-roboto'],
  [join(icons, 'iconfont', 'material-icons.woff2'), 'material-icons.woff2'],
  [join(icons, 'LICENSE'), 'LICENSE-material-icons'],
];

rmSync(dest, { recursive: true, force: true });
mkdirSync(dest, { recursive: true });
for (const [from, to] of files) {
  copyFileSync(from, join(dest, to));
}
console.log(`fonts: ${files.length} files copied into ${dest}`);
