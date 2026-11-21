import { ApiError } from '@impulseframework/resource';

/**
 * What the server said of a request it refused, as a person reads it: its own message,
 * and never a status code. The client words a refusal that came with no message as
 * "HTTP 409", which says nothing to a person, so that one says the server gave no reason.
 */
export function serverWords(err: unknown): string {
  if (err instanceof ApiError) {
    const body: unknown = err.body;
    if (body && typeof body === 'object' && 'message' in body && typeof body.message === 'string' && body.message) {
      return body.message;
    }
    if (typeof body === 'string' && body !== '') {
      return body;
    }
    return 'The server gave no reason.';
  }
  return err instanceof Error ? err.message : String(err);
}
