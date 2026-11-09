import { describe, expect, it } from 'bun:test';
import { Method } from './brands';
import { createClient, formatByteSize, UploadMethodHandle, uploadFilePart, uploadRequestPart } from './client';
import { ApiDescriptor } from './descriptor';
import { ApiError, fetchTransport, Transport } from './transport';
import { ScriptedTransport, scriptedTransport } from '@impulseframework/resource/testing';

// The upload handle's contract: the body travels as the request part first with a
// JSON type, each file as a file part, the local size check refuses over the
// declared maximum with the server's message shape, and the fetch transport sends a
// FormData without a JSON content type.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  resources: {},
  methods: {
    AttachMissionDocument: {
      method: 'AttachMissionDocument' as Method,
      property: 'attachMissionDocument',
      route: 'attach-mission-document',
      scope: 'global',
      answers: true,
      upload: { maxBytes: 1024 },
    },
  },
};

interface Attached {
  documentIds: string[];
}

interface Api {
  attachMissionDocument: UploadMethodHandle<{ missionId: string; title: string }, Attached>;
}

/** The server's answer to the upload: two documents attached. */
function attaching(): ScriptedTransport {
  return scriptedTransport({ status: 200, body: { documentIds: ['d1', 'd2'] } });
}

function api(transport: Transport): Api {
  return createClient<Api, Record<string, never>>(descriptor, { baseUrl: '/api', transport }) as unknown as Api;
}

describe('an upload method', () => {
  it('sends the request part first as JSON, then one file part per file', async () => {
    const transport = attaching();
    const brief = new File(['%PDF-1.7 brief'], 'brief.pdf', { type: 'application/pdf' });
    const chart = new Blob(['PNG']);
    const attached = await api(transport).attachMissionDocument.upload({ missionId: 'm1', title: 'Brief' }, [
      brief,
      chart,
    ]);
    expect(attached).toEqual({ documentIds: ['d1', 'd2'] });
    expect(transport.requests[0].url).toBe('/api/attach-mission-document');
    const form = transport.requests[0].body as FormData;
    expect(form).toBeInstanceOf(FormData);
    const entries = [...form.entries()];
    expect(entries.map(([name]) => name)).toEqual([uploadRequestPart, uploadFilePart, uploadFilePart]);
    const request = entries[0][1] as Blob;
    expect(request.type).toStartWith('application/json');
    expect(JSON.parse(await request.text())).toEqual({ missionId: 'm1', title: 'Brief' });
    const first = entries[1][1] as File;
    expect(first.name).toBe('brief.pdf');
    expect(first.type).toBe('application/pdf');
  });

  it('refuses locally over the declared maximum, in the shape of the server 413', async () => {
    const transport = attaching();
    const big = new Blob([new Uint8Array(1025)]);
    const call = () => api(transport).attachMissionDocument.upload({ missionId: 'm1', title: 'Big' }, [big]);
    expect(call).toThrow(ApiError);
    try {
      call();
    } catch (e) {
      const error = e as ApiError;
      expect(error.status).toBe(413);
      expect(error.message).toBe('the upload exceeds the declared maximum of 1KB');
    }
    expect(transport.requests).toHaveLength(0);
  });

  it('still executes as JSON through execute', async () => {
    const transport = attaching();
    await api(transport).attachMissionDocument.execute({ missionId: 'm1', title: 'Brief' });
    expect(transport.requests[0].body).toEqual({ missionId: 'm1', title: 'Brief' });
  });
});

describe('fetchTransport with a FormData body', () => {
  it('sends the form as-is without a JSON content type', async () => {
    let seen: { headers: Record<string, string>; body: unknown } | undefined;
    const doFetch = (async (_url: string | URL | Request, init?: RequestInit) => {
      seen = { headers: init?.headers as Record<string, string>, body: init?.body };
      return new Response('{}', { status: 200, headers: { 'Content-Type': 'application/json' } });
    }) as unknown as typeof fetch;
    const transport = fetchTransport({ fetch: doFetch, xsrf: false });
    const form = new FormData();
    form.append(uploadRequestPart, new Blob(['{}'], { type: 'application/json' }));
    await transport({ method: 'POST', url: '/api/attach-mission-document', body: form });
    expect(seen?.body).toBe(form);
    expect(seen?.headers['Content-Type']).toBeUndefined();
  });
});

describe('formatByteSize', () => {
  it('renders even units and bytes otherwise', () => {
    expect(formatByteSize(5 * 1024 * 1024)).toBe('5MB');
    expect(formatByteSize(512 * 1024)).toBe('512KB');
    expect(formatByteSize(1024 * 1024 * 1024)).toBe('1GB');
    expect(formatByteSize(300)).toBe('300 bytes');
  });
});
