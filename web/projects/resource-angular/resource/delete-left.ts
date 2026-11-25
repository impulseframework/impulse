import { DeleteLeft } from '@impulseframework/resource';

/**
 * What a delete's answer says it left: a delete too large for one request answers 202
 * with the records it deleted and the records still under the row (`DeleteLeft`), and
 * the row stands. Undefined for any other answer, a delete that finished among them.
 */
export function deleteLeftOf(answer: unknown): DeleteLeft | undefined {
  if (typeof answer !== 'object' || answer === null) {
    return undefined;
  }
  const { deleted, left } = answer as Record<string, unknown>;

  return typeof deleted === 'number' && typeof left === 'number' ? { deleted, left } : undefined;
}
