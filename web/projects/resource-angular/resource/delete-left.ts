import { DeleteLeft } from '@impulseframework/resource';

/**
 * What a delete's answer says it deleted: a delete too large for one request answers 202
 * with the records it deleted (`DeleteLeft`), and the row stands. Undefined for any other
 * answer, a delete that finished among them.
 */
export function deleteLeftOf(answer: unknown): DeleteLeft | undefined {
  if (typeof answer !== 'object' || answer === null) {
    return undefined;
  }
  const { deleted } = answer as Record<string, unknown>;

  return typeof deleted === 'number' ? { deleted } : undefined;
}
