import type { Transport, TransportRequest, TransportResponse } from '@impulseframework/resource';

/**
 * Answers one request. The default is the fixed response `{ status: 200, body: undefined }`;
 * a spec that answers by URL, by method, or by count passes a function.
 */
export type Responder = (request: TransportRequest) => TransportResponse | Promise<TransportResponse>;

/** A Transport that remembers what was asked of it: every request, in the order it was made. */
export interface ScriptedTransport extends Transport {
  readonly requests: TransportRequest[];
}

/**
 * A Transport for a spec: it records every request in `requests` and answers each from
 * the script. The script is a fixed response, answered to every request, or a responder
 * `(request) => TransportResponse` that decides per request; with no script, every
 * request is a 200 with no body. The client over it judges the answers as it judges a
 * server's: a 4xx or 5xx becomes ApiError, a declared answer resolves, and `Link` and
 * `Total-Count` headers position a page. Nothing is asserted here; the spec reads
 * `requests` and asserts what was sent.
 *
 *     const transport = scriptedTransport({ status: 200, body: [{ id: 'a' }] });
 *     const api = createApi({ baseUrl: '/api', transport });
 *     await api.missions.list();
 *     expect(transport.requests[0].url).toBe('/api/missions');
 *
 *     const byUrl = scriptedTransport((request) =>
 *       request.url === '/api/missions?limit=all'
 *         ? { status: 200, body: rows }
 *         : { status: 404, body: { message: `unscripted ${request.url}` } },
 *     );
 */
export function scriptedTransport(
  script: TransportResponse | Responder = { status: 200, body: undefined },
): ScriptedTransport {
  const requests: TransportRequest[] = [];
  const respond: Responder = typeof script === 'function' ? script : () => script;
  const transport = async (request: TransportRequest): Promise<TransportResponse> => {
    requests.push(request);
    return respond(request);
  };
  return Object.assign(transport, { requests });
}
