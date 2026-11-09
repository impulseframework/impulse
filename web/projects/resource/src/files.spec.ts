import { describe, expect, it } from 'bun:test';
import { Resource } from './brands';
import { createClient } from './client';
import { ApiDescriptor } from './descriptor';
import { scriptedTransport } from '@impulseframework/resource/testing';

// The @file route as the client addresses it: the descriptor lists a resource's
// segments, and the handle's fileUrl builds the file's URL under the row's read route,
// in the row's scope, without fetching anything. A segment the descriptor does not list
// is an error, since the server generated no such route.

const descriptor: ApiDescriptor = {
  permissionDigestRoute: 'permission-digest',
  userDomainsRoute: 'user-domains',
  features: { route: 'features' },
  domainRoute: { segment: 'sectors', param: 'sectorID' },
  resources: {
    MissionDocuments: {
      resource: 'MissionDocuments' as Resource,
      property: 'missionDocuments',
      route: 'mission-documents',
      scope: 'domain',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 25, max: 200 },
      files: ['content', 'thumbnail'],
    },
    Statements: {
      resource: 'Statements' as Resource,
      property: 'statements',
      route: 'statements',
      scope: 'global',
      consolidated: false,
      keys: ['clientId', 'period'],
      operations: ['list', 'read'],
      page: { default: 50 },
      files: ['sheet'],
    },
    Sectors: {
      resource: 'Sectors' as Resource,
      property: 'sectors',
      route: 'sectors',
      scope: 'global',
      consolidated: false,
      keys: ['id'],
      operations: ['list', 'read'],
      page: { default: 50 },
    },
  },
  methods: {},
};

interface Row {
  id: string;
}

interface Api {
  sectors: ReturnType<ReturnType<typeof createClient>['define']>;
  statements: ReturnType<ReturnType<typeof createClient>['define']>;
}

interface SectorApi {
  missionDocuments: ReturnType<ReturnType<typeof createClient>['define']>;
}

function api() {
  return createClient<Api, SectorApi>(descriptor, {
    baseUrl: '/api',
    transport: scriptedTransport(() => ({ status: 404, body: { message: 'nothing is fetched' } })),
  });
}

describe('fileUrl', () => {
  it('addresses a row file under the read route, in the row scope, content by default', () => {
    const documents = api().domain('anvil').missionDocuments;
    expect(documents.fileUrl(['0193e2a7'])).toBe('/api/sectors/anvil/mission-documents/0193e2a7/content');
    expect(documents.fileUrl(['0193e2a7'], 'thumbnail')).toBe('/api/sectors/anvil/mission-documents/0193e2a7/thumbnail');
    expect(documents.url(['0193e2a7'])).toBe('/api/sectors/anvil/mission-documents/0193e2a7');
  });

  it('spells a compound key as the read route does', () => {
    expect(api().statements.fileUrl(['c1', '2026-09'], 'sheet')).toBe('/api/statements/c1/2026-09/sheet');
  });

  it('refuses a segment the descriptor does not list', () => {
    expect(() => api().domain('anvil').missionDocuments.fileUrl(['0193e2a7'], 'cover')).toThrow(
      'MissionDocuments serves no file under cover: its files are [content, thumbnail]',
    );
    expect(() => api().sectors.fileUrl(['anvil'])).toThrow('Sectors serves no file under content: its files are []');
  });

  it('encodes the key and leaves the descriptor as the generator wrote it', () => {
    const documents = api().domain('anvil').missionDocuments;
    expect(documents.fileUrl(['a b'])).toBe('/api/sectors/anvil/mission-documents/a%20b/content');
    expect(documents.descriptor.files).toEqual(['content', 'thumbnail']);
    expect(api().sectors.descriptor.files).toBeUndefined();
    void ({} as Row);
  });
});
