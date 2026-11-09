/**
 * The release a build names, as the version header carries it, or undefined for a build
 * that names none: an absent or empty value, or `dev`, the local build's name. A release
 * reads as a semantic version with an optional leading `v`.
 */
export function releaseVersion(value: string | undefined): string | undefined {
  const trimmed = value?.trim() ?? '';
  if (trimmed === '' || trimmed === 'dev') {
    return undefined;
  }
  return trimmed;
}

/**
 * Compares two releases as semantic versions: negative when `a` is older than `b`, zero
 * when they are the same release, positive when `a` is newer. A leading `v` is ignored,
 * the numeric parts compare as numbers, and a prerelease (`1.4.0-rc.1`) is older than its
 * release and compares to another prerelease by its identifiers. A value that is not a
 * version compares as `0.0.0`, so an unparseable release never reads as newer than a real one.
 */
export function compareReleases(a: string, b: string): number {
  const left = parseRelease(a);
  const right = parseRelease(b);
  for (let i = 0; i < 3; i++) {
    if (left.numbers[i] !== right.numbers[i]) {
      return left.numbers[i] - right.numbers[i];
    }
  }
  if (left.prerelease === right.prerelease) {
    return 0;
  }
  if (left.prerelease === undefined) {
    return 1;
  }
  if (right.prerelease === undefined) {
    return -1;
  }
  return comparePrerelease(left.prerelease, right.prerelease);
}

interface ParsedRelease {
  numbers: [number, number, number];
  prerelease?: string;
}

function parseRelease(value: string): ParsedRelease {
  const match = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/.exec(value.trim());
  if (!match) {
    return { numbers: [0, 0, 0] };
  }
  const parsed: ParsedRelease = { numbers: [Number(match[1]), Number(match[2]), Number(match[3])] };
  if (match[4] !== undefined) {
    parsed.prerelease = match[4];
  }
  return parsed;
}

function comparePrerelease(a: string, b: string): number {
  const left = a.split('.');
  const right = b.split('.');
  const length = Math.max(left.length, right.length);
  for (let i = 0; i < length; i++) {
    const x = left[i];
    const y = right[i];
    if (x === undefined) {
      return -1;
    }
    if (y === undefined) {
      return 1;
    }
    const xNumber = /^\d+$/.test(x) ? Number(x) : undefined;
    const yNumber = /^\d+$/.test(y) ? Number(y) : undefined;
    if (xNumber !== undefined && yNumber !== undefined) {
      if (xNumber !== yNumber) {
        return xNumber - yNumber;
      }
      continue;
    }
    if (xNumber !== undefined) {
      return -1;
    }
    if (yNumber !== undefined) {
      return 1;
    }
    if (x !== y) {
      return x < y ? -1 : 1;
    }
  }
  return 0;
}
