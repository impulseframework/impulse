export type OperationType = 'add' | 'patch' | 'remove';

/**
 * The endpoint an operation must be sent to when it is not the consolidated one. A
 * resource the generator left off the consolidated handler (`consolidated: false` in
 * the descriptor) keeps a standalone PATCH route, and its operations carry that route
 * under this symbol so `client.batch` can deliver them there instead. A symbol key
 * survives an object spread and never reaches the wire: JSON.stringify skips it.
 */
export const OperationRoute: unique symbol = Symbol('OperationRoute');

/**
 * One mutation on the consolidated (or per-resource) PATCH endpoint. `path` is the
 * resource route plus key segments, without API prefix: `/work-orders`,
 * `/waystations/ws-alpha/work-order-tasks/{id}/{n}`. Every operation in one request
 * commits in one transaction or not at all. An operation built by a standalone
 * resource's handle carries the route it belongs to under `OperationRoute`, and its
 * path is the key segments alone.
 */
export interface Operation {
  op: OperationType;
  path: string;
  value?: Record<string, unknown>;
  [OperationRoute]?: string;
}

/** The standalone route an operation is bound to; undefined for a consolidated one. */
export function operationRoute(operation: Operation): string | undefined {
  return operation[OperationRoute];
}

/**
 * The mutation response: server-generated ids of created rows, keyed by the
 * camelCase plural resource name on a consolidated route, and under `ids` on a
 * standalone route, which serves one resource (`iDs` on a server from before the key
 * changed, which a client still reads). Resources without a generated key contribute
 * no entry, so an empty object is a normal success.
 */
export type BatchResult = Record<string, string[]> & { ids?: string[]; iDs?: string[] };
