import { beforeEach, describe, expect, it, mock } from 'bun:test';
import { ChangeEvent, LiveIdentity, PageEvents } from '@impulseframework/resource';

// The feed over a mocked SDK: it connects as the identity says (emulator with a mock
// user token, or a custom token signed in), listens over users/{uid}/changes ordered by
// `at` and bounded by the last change seen, primes on the first snapshot and emits each
// later document as one event with `at` in microseconds, restarts a failed listener and
// emits resync when it recovers, emits resync when the page becomes visible after being
// hidden longer than the change documents live and on no other occasion, and signs its
// identity out and tears the app down on stop.

interface FakeTimestamp {
  seconds: number;
  nanoseconds: number;
}

interface FakeDoc {
  id: string;
  data(): Record<string, unknown>;
}

interface FakeChange {
  type: 'added' | 'modified' | 'removed';
  doc: FakeDoc;
}

interface FakeSnapshot {
  docs: FakeDoc[];
  docChanges(): FakeChange[];
}

interface Listener {
  query: unknown;
  next: (snapshot: FakeSnapshot) => void;
  error: (error: Error) => void;
  unsubscribed: number;
}

/** What the mocked SDK was asked, in order, and the listeners it holds. */
const sdk = {
  apps: [] as { options: Record<string, unknown>; name: string }[],
  deleted: [] as string[],
  firestores: [] as { app: string; database: string | undefined }[],
  emulators: [] as { host: string; port: number; options: unknown }[],
  auths: [] as { app: string; persistence: unknown }[],
  signIns: [] as string[],
  /** Sign-outs and app deletions in the order they happened, as `signOut name` and `delete name`. */
  teardown: [] as string[],
  collections: [] as string[],
  queries: [] as unknown[][],
  listeners: [] as Listener[],
  signInFails: false,
};

function reset(): void {
  sdk.apps.length = 0;
  sdk.deleted.length = 0;
  sdk.firestores.length = 0;
  sdk.emulators.length = 0;
  sdk.auths.length = 0;
  sdk.signIns.length = 0;
  sdk.teardown.length = 0;
  sdk.collections.length = 0;
  sdk.queries.length = 0;
  sdk.listeners.length = 0;
  sdk.signInFails = false;
}

mock.module('firebase/app', () => ({
  initializeApp: (options: Record<string, unknown>, name: string) => {
    const app = { options, name };
    sdk.apps.push(app);
    return app;
  },
  deleteApp: async (app: { name: string }) => {
    sdk.deleted.push(app.name);
    sdk.teardown.push(`delete ${app.name}`);
  },
}));

mock.module('firebase/auth', () => ({
  inMemoryPersistence: 'in-memory',
  initializeAuth: (app: { name: string }, deps?: { persistence?: unknown }) => {
    sdk.auths.push({ app: app.name, persistence: deps?.persistence });
    return { app };
  },
  signOut: async (auth: { app: { name: string } }) => {
    sdk.teardown.push(`signOut ${auth.app.name}`);
  },
  signInWithCustomToken: async (_auth: unknown, token: string) => {
    if (sdk.signInFails) {
      throw new Error('auth/invalid-custom-token');
    }
    sdk.signIns.push(token);
    return {};
  },
}));

mock.module('firebase/firestore', () => ({
  getFirestore: (app: { name: string }, database?: string) => {
    const firestore = { app: app.name, database };
    sdk.firestores.push(firestore);
    return firestore;
  },
  connectFirestoreEmulator: (_db: unknown, host: string, port: number, options: unknown) => {
    sdk.emulators.push({ host, port, options });
  },
  collection: (_db: unknown, ...segments: string[]) => {
    const path = segments.join('/');
    sdk.collections.push(path);
    return { path };
  },
  query: (ref: unknown, ...constraints: unknown[]) => {
    const q = [ref, ...constraints];
    sdk.queries.push(q);
    return q;
  },
  where: (field: string, op: string, value: unknown) => ({ where: [field, op, value] }),
  orderBy: (field: string) => ({ orderBy: field }),
  onSnapshot: (query: unknown, next: (snapshot: FakeSnapshot) => void, error: (error: Error) => void) => {
    const listener: Listener = { query, next, error, unsubscribed: 0 };
    sdk.listeners.push(listener);
    return () => {
      listener.unsubscribed++;
    };
  },
}));

import {
  FirestoreChangeFeed,
  firestoreChangeFeed,
  hostAndPort,
  microseconds,
  VisibilityEvents,
} from './firestore-feed';

const emulated: LiveIdentity = {
  uid: 'u-7',
  token: '',
  project: 'lab',
  database: 'live',
  apiKey: '',
  emulator: 'localhost:8080',
};
const production: LiveIdentity = {
  uid: 'u-7',
  token: 'custom.jwt',
  project: 'prod',
  database: '(default)',
  apiKey: 'web-key',
  emulator: '',
};

function at(seconds: number, nanoseconds = 0): FakeTimestamp {
  return { seconds, nanoseconds };
}

function doc(id: string, data: Record<string, unknown>): FakeDoc {
  return { id, data: () => data };
}

function snapshot(changes: FakeChange[], docs: FakeDoc[] = changes.map((c) => c.doc)): FakeSnapshot {
  return { docs, docChanges: () => changes };
}

function added(...docs: FakeDoc[]): FakeSnapshot {
  return snapshot(docs.map((d) => ({ type: 'added' as const, doc: d })));
}

/** A window stand-in: the listeners the feed attached, fired by the spec. */
class FakePage implements PageEvents {
  listeners = new Map<string, Set<(event: { persisted?: boolean }) => void>>();

  addEventListener(type: string, listener: (event: { persisted?: boolean }) => void): void {
    let held = this.listeners.get(type);
    if (!held) {
      held = new Set();
      this.listeners.set(type, held);
    }
    held.add(listener);
  }

  removeEventListener(type: string, listener: (event: { persisted?: boolean }) => void): void {
    this.listeners.get(type)?.delete(listener);
  }

  fire(type: string, event: { persisted?: boolean } = {}): void {
    for (const listener of [...(this.listeners.get(type) ?? [])]) {
      listener(event);
    }
  }

  get listening(): number {
    let n = 0;
    for (const held of this.listeners.values()) {
      n += held.size;
    }
    return n;
  }
}

/** A document stand-in: its visibility, set by the spec, and the visibilitychange listeners. */
class FakeVisibility implements VisibilityEvents {
  visibilityState = 'visible';
  listeners = new Set<() => void>();

  addEventListener(_type: string, listener: () => void): void {
    this.listeners.add(listener);
  }

  removeEventListener(_type: string, listener: () => void): void {
    this.listeners.delete(listener);
  }

  become(state: 'visible' | 'hidden'): void {
    this.visibilityState = state;
    for (const listener of [...this.listeners]) {
      listener();
    }
  }
}

/** A feed with its events and resyncs recorded, a clock the spec moves, and the page it lives in. */
function harness(options: { resyncAfterHidden?: number; retryDelay?: number } = {}): {
  feed: FirestoreChangeFeed;
  events: ChangeEvent[];
  resyncs: number[];
  clock: { now: number };
  page: FakePage;
  visibility: FakeVisibility;
} {
  const clock = { now: 1_700_000_000_000 };
  const page = new FakePage();
  const visibility = new FakeVisibility();
  const feed = new FirestoreChangeFeed({ ...options, now: () => clock.now, page, visibility });
  const events: ChangeEvent[] = [];
  const resyncs: number[] = [];
  feed.onChange((event) => events.push(event));
  feed.onResync(() => resyncs.push(clock.now));
  return { feed, events, resyncs, clock, page, visibility };
}

function listener(): Listener {
  const last = sdk.listeners[sdk.listeners.length - 1];
  if (!last) {
    throw new Error('no listener');
  }
  return last;
}

beforeEach(reset);

describe('microseconds', () => {
  const cases: { name: string; at: FakeTimestamp; want: string }[] = [
    { name: 'whole seconds', at: at(1_700_000_000), want: '1700000000000000' },
    { name: 'nanoseconds floor to microseconds', at: at(1_700_000_000, 123_456_789), want: '1700000000123456' },
    { name: 'the epoch', at: at(0), want: '0' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      expect(microseconds(tt.at)).toBe(tt.want);
    });
  }
});

describe('hostAndPort', () => {
  const cases: { name: string; address: string; want?: { host: string; port: number }; throws?: string }[] = [
    { name: 'a host and a port', address: 'localhost:8080', want: { host: 'localhost', port: 8080 } },
    { name: 'an IPv6 host loses its brackets', address: '[::1]:8080', want: { host: '::1', port: 8080 } },
    { name: 'no port is refused', address: 'localhost', throws: 'has no port' },
    { name: 'a port that is not a number is refused', address: 'localhost:http', throws: 'is not host:port' },
  ];

  for (const tt of cases) {
    it(tt.name, () => {
      if (tt.throws) {
        expect(() => hostAndPort(tt.address)).toThrow(tt.throws);
        return;
      }
      expect(hostAndPort(tt.address)).toEqual(tt.want);
    });
  }
});

describe('start', () => {
  it('against the emulator: the project, the database, a mock user token for the uid, no auth, one listener over the change set', async () => {
    const { feed } = harness();
    await feed.start(emulated);
    expect(sdk.apps.map((a) => a.options)).toEqual([{ projectId: 'lab', apiKey: undefined }]);
    expect(sdk.firestores).toEqual([{ app: sdk.apps[0].name, database: 'live' }]);
    expect(sdk.emulators).toEqual([
      { host: 'localhost', port: 8080, options: { mockUserToken: { sub: 'u-7', user_id: 'u-7' } } },
    ]);
    expect(sdk.auths).toEqual([]);
    expect(sdk.signIns).toEqual([]);
    expect(sdk.collections).toEqual(['users/u-7/changes']);
    // No change seen yet: ordered by at, no lower bound.
    expect(sdk.queries).toEqual([[{ path: 'users/u-7/changes' }, { orderBy: 'at' }]]);
    expect(sdk.listeners).toHaveLength(1);
    expect(feed.started).toBe(true);
    await feed.stop();
  });

  it('in production: the web API key, the default database, a custom token signed in on an auth held in memory only; stop signs it out before the app goes', async () => {
    const { feed } = harness();
    await feed.start(production);
    expect(sdk.apps.map((a) => a.options)).toEqual([{ projectId: 'prod', apiKey: 'web-key' }]);
    expect(sdk.firestores).toEqual([{ app: sdk.apps[0].name, database: '(default)' }]);
    expect(sdk.emulators).toEqual([]);
    expect(sdk.auths).toEqual([{ app: sdk.apps[0].name, persistence: 'in-memory' }]);
    expect(sdk.signIns).toEqual(['custom.jwt']);
    expect(sdk.listeners).toHaveLength(1);
    await feed.stop();
    expect(sdk.teardown).toEqual([`signOut ${sdk.apps[0].name}`, `delete ${sdk.apps[0].name}`]);
  });

  it('an empty database id asks for the project default', async () => {
    const { feed } = harness();
    await feed.start({ ...emulated, database: '' });
    expect(sdk.firestores).toEqual([{ app: sdk.apps[0].name, database: undefined }]);
    await feed.stop();
  });

  it('a refused sign-in rejects and holds no listener', async () => {
    const { feed } = harness();
    sdk.signInFails = true;
    await expect(feed.start(production)).rejects.toThrow('auth/invalid-custom-token');
    expect(sdk.listeners).toEqual([]);
    expect(feed.started).toBe(false);
  });

  it('a second start ends the first: its listener is released and its app deleted', async () => {
    const { feed } = harness();
    await feed.start(emulated);
    const first = listener();
    await feed.start(emulated);
    expect(first.unsubscribed).toBe(1);
    expect(sdk.deleted).toEqual([sdk.apps[0].name]);
    expect(sdk.apps).toHaveLength(2);
    expect(sdk.apps[0].name).not.toBe(sdk.apps[1].name);
    await feed.stop();
  });

  it('firestoreChangeFeed builds the feed', () => {
    expect(firestoreChangeFeed()).toBeInstanceOf(FirestoreChangeFeed);
  });
});

describe('events', () => {
  it('the first snapshot primes and emits nothing; each later document is one event with at in microseconds, in at order', async () => {
    const { feed, events, resyncs } = harness();
    await feed.start(emulated);
    const l = listener();

    // The backlog: a change from before the page loaded.
    l.next(
      added(
        doc('row|Ships|s0|1', {
          kind: 'row',
          resource: 'Ships',
          key: 's0',
          deleted: false,
          at: at(1_700_000_000, 500_000),
        }),
      ),
    );
    expect(events).toEqual([]);

    l.next(
      snapshot([
        {
          type: 'added',
          doc: doc('list|Ships|anvil|2', { kind: 'list', resource: 'Ships', domain: 'anvil', at: at(1_700_000_002) }),
        },
        {
          type: 'added',
          doc: doc('row|Ships|s1|1', {
            kind: 'row',
            resource: 'Ships',
            key: 's1',
            deleted: true,
            at: at(1_700_000_001, 250_000),
          }),
        },
        { type: 'added', doc: doc('resource|Ships|3', { kind: 'resource', resource: 'Ships', at: at(1_700_000_003) }) },
      ]),
    );
    expect(events).toEqual([
      { kind: 'row', resource: 'Ships', key: 's1', deleted: true, at: '1700000001000250' },
      { kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000002000000' },
      { kind: 'resource', resource: 'Ships', at: '1700000003000000' },
    ]);
    expect(resyncs).toEqual([]);
    await feed.stop();
  });

  it('a document modified within its second moves forward and is one more event; removals, stale and unresolved documents are not', async () => {
    const { feed, events } = harness();
    await feed.start(emulated);
    const l = listener();
    l.next(added());
    l.next(added(doc('row|Ships|s1|1', { kind: 'row', resource: 'Ships', key: 's1', at: at(1_700_000_001) })));
    l.next(
      snapshot([
        // The same document, coalesced: a later server timestamp.
        {
          type: 'modified',
          doc: doc('row|Ships|s1|1', { kind: 'row', resource: 'Ships', key: 's1', at: at(1_700_000_001, 900_000) }),
        },
        // A document expiring out of the result set.
        {
          type: 'removed',
          doc: doc('row|Ships|s0|1', { kind: 'row', resource: 'Ships', key: 's0', at: at(1_699_999_000) }),
        },
        // One not past the last seen.
        {
          type: 'added',
          doc: doc('row|Ships|s2|1', { kind: 'row', resource: 'Ships', key: 's2', at: at(1_700_000_000) }),
        },
        // One whose server timestamp has not resolved, and one of a shape the feed does not know.
        { type: 'added', doc: doc('row|Ships|s3|1', { kind: 'row', resource: 'Ships', key: 's3', at: null }) },
        { type: 'added', doc: doc('odd', { kind: 'column', resource: 'Ships', at: at(1_700_000_005) }) },
      ]),
    );
    expect(events.map((e) => [e.key, e.at])).toEqual([
      ['s1', '1700000001000000'],
      ['s1', '1700000001000900'],
    ]);
    await feed.stop();
  });

  it("one commit's row and list documents, one timestamp, two snapshots: two events; the same two replayed: none", async () => {
    const { feed, events } = harness();
    await feed.start(emulated);
    const l = listener();
    l.next(added());

    // The server writes both with one server timestamp; the emulator delivered them 6 ms apart.
    const instant = at(1_700_000_010, 250_000);
    const row = doc('row|Ships|s1|1700000010', {
      kind: 'row',
      resource: 'Ships',
      key: 's1',
      deleted: false,
      at: instant,
    });
    const list = doc('list|Ships|anvil|1700000010', { kind: 'list', resource: 'Ships', domain: 'anvil', at: instant });
    l.next(added(list));
    l.next(added(row));
    expect(events).toEqual([
      { kind: 'list', resource: 'Ships', domain: 'anvil', at: '1700000010000250' },
      { kind: 'row', resource: 'Ships', key: 's1', deleted: false, at: '1700000010000250' },
    ]);

    // The same two again, as a replay would deliver them: nothing more.
    l.next(
      snapshot([
        { type: 'modified', doc: list },
        { type: 'added', doc: row },
      ]),
    );
    expect(events).toHaveLength(2);

    // A later instant replaces the set: a document there is new, and the earlier ids no longer matter.
    const later = at(1_700_000_011);
    l.next(added(doc('row|Ships|s2|1700000011', { kind: 'row', resource: 'Ships', key: 's2', at: later })));
    l.next(added(doc('list|Ships|anvil|1700000011', { kind: 'list', resource: 'Ships', domain: 'anvil', at: later })));
    expect(events.map((e) => [e.kind, e.key ?? e.domain, e.at])).toEqual([
      ['list', 'anvil', '1700000010000250'],
      ['row', 's1', '1700000010000250'],
      ['row', 's2', '1700000011000000'],
      ['list', 'anvil', '1700000011000000'],
    ]);
    await feed.stop();
  });

  it('a document at the primed instant that the priming snapshot held is not an event; one it did not hold is', async () => {
    const { feed, events } = harness();
    await feed.start(emulated);
    const l = listener();
    const instant = at(1_700_000_020);
    const held = doc('list|Ships|anvil|1700000020', { kind: 'list', resource: 'Ships', domain: 'anvil', at: instant });
    l.next(added(held));
    l.next(
      snapshot([
        { type: 'modified', doc: held },
        {
          type: 'added',
          doc: doc('row|Ships|s1|1700000020', { kind: 'row', resource: 'Ships', key: 's1', at: instant }),
        },
      ]),
    );
    expect(events.map((e) => [e.kind, e.key ?? e.domain])).toEqual([['row', 's1']]);
    await feed.stop();
  });

  it('a quiet stretch longer than the change documents live is not a resync: the next change is one event', async () => {
    const { feed, events, resyncs, clock } = harness({ resyncAfterHidden: 600_000 });
    await feed.start(emulated);
    const l = listener();
    l.next(added());

    clock.now += 3_600_000;
    l.next(added(doc('a', { kind: 'row', resource: 'Ships', key: 's1', at: at(1_700_000_001) })));
    expect(events.map((e) => e.key)).toEqual(['s1']);
    expect(resyncs).toEqual([]);
    await feed.stop();
  });
});

describe('a hidden page', () => {
  it("hidden and visible again within the change documents' life: nothing", async () => {
    const { feed, resyncs, clock, visibility } = harness({ resyncAfterHidden: 600_000 });
    await feed.start(emulated);
    visibility.become('hidden');
    clock.now += 600_000;
    visibility.become('visible');
    expect(resyncs).toEqual([]);
    await feed.stop();
  });

  it('hidden longer than the change documents live, then visible: one resync, and the clock starts again', async () => {
    const { feed, resyncs, clock, visibility } = harness({ resyncAfterHidden: 600_000 });
    await feed.start(emulated);
    visibility.become('hidden');
    clock.now += 600_001;
    visibility.become('visible');
    expect(resyncs).toEqual([clock.now]);

    // Visible again without having been hidden: nothing more.
    visibility.become('visible');
    expect(resyncs).toHaveLength(1);

    // Hidden a short while this time: nothing.
    visibility.become('hidden');
    clock.now += 1_000;
    visibility.become('visible');
    expect(resyncs).toHaveLength(1);
    await feed.stop();
  });

  it('restored from the back-forward cache after longer than the change documents live: one resync, not two', async () => {
    const { feed, resyncs, clock, page, visibility } = harness({ resyncAfterHidden: 600_000 });
    await feed.start(emulated);
    // Leaving: the document goes hidden, then the page hides. One clock.
    visibility.become('hidden');
    clock.now += 5_000;
    page.fire('pagehide');
    clock.now += 600_000;
    // Coming back: the page shows, then the document is visible. One resync.
    page.fire('pageshow', { persisted: true });
    visibility.become('visible');
    expect(resyncs).toEqual([clock.now]);
    await feed.stop();
  });

  it('a page shown fresh, not from the cache, resyncs nothing', async () => {
    const { feed, resyncs, clock, page } = harness({ resyncAfterHidden: 600_000 });
    await feed.start(emulated);
    page.fire('pagehide');
    clock.now += 600_001;
    page.fire('pageshow', { persisted: false });
    expect(resyncs).toEqual([]);
    await feed.stop();
  });

  it('started while hidden: the clock runs from the start', async () => {
    const { feed, resyncs, clock, visibility } = harness({ resyncAfterHidden: 600_000 });
    visibility.visibilityState = 'hidden';
    await feed.start(emulated);
    clock.now += 600_001;
    visibility.become('visible');
    expect(resyncs).toEqual([clock.now]);
    await feed.stop();
  });

  it('after stop the page is not listened to', async () => {
    const { feed, resyncs, clock, page, visibility } = harness({ resyncAfterHidden: 600_000 });
    await feed.start(emulated);
    expect(page.listening).toBe(2);
    expect(visibility.listeners.size).toBe(1);
    visibility.become('hidden');
    await feed.stop();
    expect(page.listening).toBe(0);
    expect(visibility.listeners.size).toBe(0);
    clock.now += 600_001;
    visibility.become('visible');
    expect(resyncs).toEqual([]);
  });
});

describe('recovery', () => {
  it('a failed listener is started again after a wait, bounded by the last change seen, and its first snapshot is a resync', async () => {
    const { feed, events, resyncs } = harness({ retryDelay: 1 });
    await feed.start(emulated);
    const first = listener();
    first.next(added(doc('a', { kind: 'row', resource: 'Ships', key: 's0', at: at(1_700_000_000) })));
    first.next(added(doc('b', { kind: 'row', resource: 'Ships', key: 's1', at: at(1_700_000_001) })));
    expect(events).toHaveLength(1);

    first.error(new Error('permission-denied'));
    expect(sdk.listeners).toHaveLength(1);
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(sdk.listeners).toHaveLength(2);
    const second = listener();
    expect(second.query).toEqual([
      { path: 'users/u-7/changes' },
      { where: ['at', '>', at(1_700_000_001)] },
      { orderBy: 'at' },
    ]);

    // What arrived during the outage primes the new listener; the resync covers it.
    second.next(added(doc('c', { kind: 'row', resource: 'Ships', key: 's2', at: at(1_700_000_002) })));
    expect(resyncs).toHaveLength(1);
    expect(events).toHaveLength(1);

    // And it delivers from there.
    second.next(added(doc('d', { kind: 'row', resource: 'Ships', key: 's3', at: at(1_700_000_003) })));
    expect(events.map((e) => e.key)).toEqual(['s1', 's3']);
    await feed.stop();
  });

  it('the wait doubles with each failure and resets on a snapshot', async () => {
    const { feed } = harness({ retryDelay: 4 });
    await feed.start(emulated);
    listener().error(new Error('one'));
    const firstFail = Date.now();
    await new Promise((resolve) => setTimeout(resolve, 2));
    expect(sdk.listeners).toHaveLength(1);
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(sdk.listeners).toHaveLength(2);
    listener().error(new Error('two'));
    const secondFail = Date.now();
    await new Promise((resolve) => setTimeout(resolve, 5));
    expect(sdk.listeners).toHaveLength(2);
    await new Promise((resolve) => setTimeout(resolve, 12));
    expect(sdk.listeners).toHaveLength(3);
    expect(secondFail - firstFail >= 4).toBe(true);
    // A snapshot resets the wait: the next failure waits the first delay again.
    listener().next(added());
    listener().error(new Error('three'));
    await new Promise((resolve) => setTimeout(resolve, 8));
    expect(sdk.listeners).toHaveLength(4);
    await feed.stop();
  });

  it('a failure after stop starts nothing', async () => {
    const { feed } = harness({ retryDelay: 1 });
    await feed.start(emulated);
    const l = listener();
    await feed.stop();
    l.error(new Error('late'));
    await new Promise((resolve) => setTimeout(resolve, 5));
    expect(sdk.listeners).toHaveLength(1);
  });
});

describe('stop', () => {
  it('releases the listener, deletes the app, and forgets the last change seen; listeners stay registered', async () => {
    const { feed, events } = harness();
    await feed.start(emulated);
    const l = listener();
    l.next(added(doc('a', { kind: 'row', resource: 'Ships', key: 's0', at: at(1_700_000_000) })));
    await feed.stop();
    expect(l.unsubscribed).toBe(1);
    expect(sdk.deleted).toEqual([sdk.apps[0].name]);
    // Against the emulator no identity was signed in, so none is signed out.
    expect(sdk.teardown).toEqual([`delete ${sdk.apps[0].name}`]);
    expect(feed.started).toBe(false);

    // Started again, the listener is unbounded: nothing is remembered across a stop.
    await feed.start(emulated);
    expect(sdk.queries[sdk.queries.length - 1]).toEqual([{ path: 'users/u-7/changes' }, { orderBy: 'at' }]);
    listener().next(added());
    listener().next(added(doc('b', { kind: 'row', resource: 'Ships', key: 's1', at: at(1_700_000_001) })));
    expect(events.map((e) => e.key)).toEqual(['s1']);
    await feed.stop();
  });

  it('stop before start does nothing', async () => {
    const { feed } = harness();
    await feed.stop();
    expect(sdk.deleted).toEqual([]);
  });

  it('a released listener hears nothing more', async () => {
    const { feed } = harness();
    const heard: string[] = [];
    const release = feed.onChange((event) => heard.push(event.key ?? ''));
    await feed.start(emulated);
    listener().next(added());
    listener().next(added(doc('a', { kind: 'row', resource: 'Ships', key: 's1', at: at(1_700_000_001) })));
    release();
    listener().next(added(doc('b', { kind: 'row', resource: 'Ships', key: 's2', at: at(1_700_000_002) })));
    expect(heard).toEqual(['s1']);
    await feed.stop();
  });
});
