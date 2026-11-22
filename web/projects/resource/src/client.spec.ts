import { describe, expect, it } from 'bun:test';
import { createClient } from './client';
import { ApiDescriptor } from './descriptor';
import {
  ApiError,
  ApiVersionError,
  apiVersionHeader,
  MaintenanceError,
  maintenanceHeader,
  maintenanceHeaderValue,
  TransportResponse,
} from './transport';
import { scriptedTransport } from '@impulseframework/resource/testing';

// A quiet request: a question asked while a person types. An answer that is an error is
// thrown to the caller as any request's is, and is never handed to the application's
// error hook, so a notice raised there is not raised once for every character; the same
// request without quiet hands each to the hook first.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {},
  methods: {},
};

const answers: { name: string; answer: TransportResponse; thrown: new (...args: never[]) => ApiError }[] = [
  {
    name: 'a 500',
    answer: { status: 500, body: { message: 'the server failed' } },
    thrown: ApiError,
  },
  {
    name: 'a version refusal',
    answer: {
      status: 412,
      body: { message: 'this release is no longer answered' },
      headers: { [apiVersionHeader.toLowerCase()]: '1.6.0' },
    },
    thrown: ApiVersionError,
  },
  {
    name: 'a maintenance answer',
    answer: {
      status: 503,
      body: undefined,
      headers: { [maintenanceHeader.toLowerCase()]: maintenanceHeaderValue },
    },
    thrown: MaintenanceError,
  },
];

describe('a quiet request', () => {
  for (const tt of answers) {
    it(`throws ${tt.name} and hands it to no error hook, where the same request without quiet does`, async () => {
      const seen: ApiError[] = [];
      const client = createClient(descriptor, {
        baseUrl: '/api',
        transport: scriptedTransport(tt.answer),
        apiVersion: '1.5.0',
        onError: (error) => seen.push(error),
      });

      const quiet = await client
        .request('POST', 'user/username/free', { body: { username: 'pax' }, quiet: true })
        .catch((e: unknown) => e);
      expect(quiet).toBeInstanceOf(tt.thrown);
      expect(seen).toEqual([]);

      const loud = await client
        .request('POST', 'user/username/free', { body: { username: 'pax' } })
        .catch((e: unknown) => e);
      expect(loud).toBeInstanceOf(tt.thrown);
      expect(seen).toEqual([loud as ApiError]);
    });
  }
});
