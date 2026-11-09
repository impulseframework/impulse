import { describe, expect, it } from 'bun:test';
import { browserTimeZone, createClient } from './client';
import { ApiDescriptor } from './descriptor';
import { apiVersionHeader, timeZoneHeader } from './transport';
import { scriptedTransport } from '@impulseframework/resource/testing';

// The time zone header: the client sends the person's zone on every request, the
// browser's own by default, the option's as given, and none for an empty option, so the
// server reads a date typed with no zone as the whole of the day the browser shows.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {},
  methods: {},
};

describe('the time zone header', () => {
  const cases: { name: string; timeZone: string | undefined; want: string | undefined }[] = [
    { name: "the browser's own zone by default", timeZone: undefined, want: browserTimeZone() },
    { name: 'a zone is sent as given', timeZone: 'Asia/Tokyo', want: 'Asia/Tokyo' },
    { name: 'an empty zone sends nothing', timeZone: '', want: undefined },
    { name: 'a blank zone sends nothing', timeZone: '  ', want: undefined },
  ];

  for (const tt of cases) {
    it(tt.name, async () => {
      const transport = scriptedTransport({ status: 200, body: [] });
      const client = createClient(descriptor, { baseUrl: '/api', transport, timeZone: tt.timeZone });
      await client.request('GET', 'missions');
      expect(transport.requests[0].headers?.[timeZoneHeader]).toBe(tt.want);
    });
  }

  it("rides beside the request's own headers and the release", async () => {
    const transport = scriptedTransport({ status: 200, body: undefined });
    const client = createClient(descriptor, { baseUrl: '/api', transport, apiVersion: '1.5.0', timeZone: 'America/New_York' });
    await client.request('POST', 'complete-mission', { body: {}, headers: { 'X-Dry-Run': 'true' } });
    expect(transport.requests[0].headers).toEqual({
      'X-Dry-Run': 'true',
      [apiVersionHeader]: '1.5.0',
      [timeZoneHeader]: 'America/New_York',
    });
  });

  it('a zone alone leaves a request without its own headers carrying the zone alone', async () => {
    const transport = scriptedTransport({ status: 200, body: [] });
    const client = createClient(descriptor, { baseUrl: '/api', transport, timeZone: 'Europe/Paris' });
    await client.request('GET', 'missions');
    expect(transport.requests[0].headers).toEqual({ [timeZoneHeader]: 'Europe/Paris' });
  });

  it('browserTimeZone names the zone Intl resolves, and none for an unknown one', () => {
    const resolved = Intl.DateTimeFormat().resolvedOptions().timeZone;
    expect(browserTimeZone()).toBe(resolved === 'Etc/Unknown' ? undefined : resolved);
  });
});
