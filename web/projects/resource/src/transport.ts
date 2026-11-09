export type HttpMethod = 'GET' | 'POST' | 'PATCH' | 'DELETE';

export interface TransportRequest {
  method: HttpMethod;
  /** The absolute URL, query string included. */
  url: string;
  /**
   * A JSON-serializable body, a FormData for a multipart upload (sent as-is, the
   * platform writing the boundary), or undefined for none.
   */
  body?: unknown;
  /** Request headers this one request carries beyond the transport's own. */
  headers?: Record<string, string>;
}

/** The header that asks a transaction-form RPC method to run and roll back instead of committing. */
export const dryRunHeader = 'X-Dry-Run';

/**
 * The header that carries the release the application was built from on every request
 * (`ClientOptions.apiVersion`), and, on the server's refusal of it, the release the server
 * runs. A build that names no release, or names `dev`, sends nothing and is never refused.
 */
export const apiVersionHeader = 'X-Api-Version';

/**
 * The person's time zone, an IANA name such as America/New_York, which the client sends on
 * every request (`ClientOptions.timeZone`, the browser's own zone by default). The server
 * reads a filter value written with no zone of its own, `2026-12-15` or
 * `2026-12-15T09:30`, as the whole of that day or minute in it, so a list filters by the
 * day the browser shows.
 */
export const timeZoneHeader = 'Time-Zone';

/**
 * The marker every answer of a server down for maintenance carries, with
 * `maintenanceHeaderValue`: `X-Maintenance: 1`. It tells a maintenance 503 from an
 * ordinary 503 (an overloaded or failing server), which stays an ordinary ApiError.
 */
export const maintenanceHeader = 'X-Maintenance';
export const maintenanceHeaderValue = '1';

/** The header a maintenance answer names its check-back wait in: seconds, or an HTTP date. */
export const retryAfterHeader = 'Retry-After';

export interface TransportResponse {
  status: number;
  /** The decoded JSON body; undefined for an empty body. */
  body: unknown;
  /** Response headers by lower-cased name. A paged list positions its walk through them (Link, Total-Count). */
  headers?: Record<string, string>;
}

/**
 * Transport carries one request to the server and nothing more. It resolves for every
 * HTTP status and rejects only when no response could be obtained at all; it judges
 * nothing. The client is the one place a response becomes an error: a 4xx or 5xx that
 * is not a declared answer becomes ApiError, observed by `ClientOptions.onError` before
 * it is thrown. A framework swaps the transport to ride its own HTTP stack (a cookie
 * echo, a loading indicator) and renders the client's events: the 401 the hook
 * reports, the ApiError nobody caught.
 */
export type Transport = (request: TransportRequest) => Promise<TransportResponse>;

/** A non-2xx response, carrying the server's decoded body. */
export class ApiError extends Error {
  constructor(
    readonly method: HttpMethod,
    readonly url: string,
    readonly status: number,
    readonly body: unknown,
  ) {
    super(messageOf(body, status));
    this.name = 'ApiError';
  }
}

/**
 * The server's refusal of this build's release: a 412 carrying the release the server runs
 * in the version header. It is recognized before a method's declared answers, so a method
 * that declares 412 never takes it for its own. `serverOlder` says which way the two
 * releases differ: true when the server runs an older release than this build (a rollback,
 * or a deploy still shifting traffic), false when this build is older than the server.
 * The framework over the client decides what the application does about it.
 */
export class ApiVersionError extends ApiError {
  constructor(
    method: HttpMethod,
    url: string,
    body: unknown,
    /** The release the server runs, from its refusal. */
    readonly serverVersion: string,
    /** The release this build sent. */
    readonly appVersion: string,
    /** True when the server's release is older than this build's. */
    readonly serverOlder: boolean,
  ) {
    super(method, url, 412, body);
    this.name = 'ApiVersionError';
  }
}

/**
 * The server's answer while it is down for maintenance: a 503 carrying the maintenance
 * marker. It is recognized before a method's declared answers, so a method that declares
 * 503 never takes it for its own. `retryAfter` is the server's Retry-After in seconds, or
 * undefined when it sent none the client could read. The framework over the client
 * decides what the application shows and how it checks back.
 */
export class MaintenanceError extends ApiError {
  constructor(
    method: HttpMethod,
    url: string,
    body: unknown,
    /** The server's Retry-After, in seconds; undefined when it sent none the client could read. */
    readonly retryAfter: number | undefined,
  ) {
    super(method, url, 503, body);
    this.name = 'MaintenanceError';
  }
}

/**
 * The seconds a Retry-After header asks for: a whole number of seconds as given, an HTTP
 * date as the seconds from now until it (never negative), and undefined for anything else.
 */
export function retryAfterSeconds(value: string | undefined): number | undefined {
  if (value === undefined) {
    return undefined;
  }
  const text = value.trim();
  if (/^\d+$/.test(text)) {
    return Number(text);
  }
  const at = Date.parse(text);
  if (Number.isNaN(at)) {
    return undefined;
  }
  return Math.max(0, Math.ceil((at - Date.now()) / 1000));
}

function messageOf(body: unknown, status: number): string {
  if (body && typeof body === 'object' && 'message' in body && typeof body.message === 'string') {
    return body.message;
  }
  if (typeof body === 'string' && body !== '') {
    return body;
  }
  return `HTTP ${status}`;
}

/**
 * The cookie-to-header XSRF handshake the generated server enforces on mutating
 * requests: the token cookie's value is echoed in a request header. The defaults are
 * the server's; pass `false` to disable (a service account outlet, or a non-browser
 * runtime that manages cookies itself).
 */
export interface XsrfOptions {
  cookieName: string;
  headerName: string;
}

export const defaultXsrf: XsrfOptions = { cookieName: 'XSRF-TOKEN', headerName: 'X-XSRF-TOKEN' };

export interface FetchTransportOptions {
  /** The fetch implementation; defaults to the global one. */
  fetch?: typeof fetch;
  /** Defaults to `same-origin`, which sends the session cookie to the API host that set it. */
  credentials?: RequestCredentials;
  headers?: Record<string, string>;
  /** XSRF cookie-to-header echo for non-GET requests, when a document is available. Defaults to the server's names. */
  xsrf?: XsrfOptions | false;
}

/** The default transport: the platform fetch with JSON encoding on both sides. */
export function fetchTransport(options: FetchTransportOptions = {}): Transport {
  const doFetch = options.fetch ?? globalThis.fetch.bind(globalThis);
  const xsrf = options.xsrf === undefined ? defaultXsrf : options.xsrf;
  return async (request) => {
    const headers: Record<string, string> = { Accept: 'application/json', ...options.headers, ...request.headers };
    let body: string | FormData | undefined;
    if (request.body instanceof FormData) {
      // The platform writes the multipart content type with its boundary.
      body = request.body;
    } else if (request.body !== undefined) {
      headers['Content-Type'] = 'application/json';
      body = JSON.stringify(request.body);
    }
    if (xsrf && request.method !== 'GET') {
      const token = readCookie(xsrf.cookieName);
      if (token !== undefined) {
        headers[xsrf.headerName] = token;
      }
    }
    const response = await doFetch(request.url, {
      method: request.method,
      headers,
      body,
      credentials: options.credentials ?? 'same-origin',
    });
    const responseHeaders: Record<string, string> = {};
    response.headers.forEach((value, name) => {
      responseHeaders[name.toLowerCase()] = value;
    });
    return { status: response.status, body: await decodeBody(response), headers: responseHeaders };
  };
}

async function decodeBody(response: Response): Promise<unknown> {
  const text = await response.text();
  if (text === '') {
    return undefined;
  }
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

/** Reads one cookie from the document, when there is a document; undefined otherwise. */
export function readCookie(name: string): string | undefined {
  const cookie = (globalThis as { document?: { cookie?: string } }).document?.cookie;
  if (!cookie) {
    return undefined;
  }
  for (const part of cookie.split(';')) {
    const [key, ...rest] = part.trim().split('=');
    if (key === name) {
      return decodeURIComponent(rest.join('='));
    }
  }
  return undefined;
}
