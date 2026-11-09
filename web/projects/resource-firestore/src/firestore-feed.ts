import { ChangeEvent, ChangeFeed, LiveIdentity, liveChangeTtl, PageEvents } from '@impulseframework/resource';
import { deleteApp, FirebaseApp, initializeApp } from 'firebase/app';
import { Auth, inMemoryPersistence, initializeAuth, signInWithCustomToken, signOut } from 'firebase/auth';
import {
  collection,
  connectFirestoreEmulator,
  DocumentData,
  Firestore,
  getFirestore,
  onSnapshot,
  orderBy,
  query,
  QuerySnapshot,
  Unsubscribe,
  where,
} from 'firebase/firestore';

/** The document's visibility as the feed reads it; the document, in a browser. */
export interface VisibilityEvents {
  readonly visibilityState: string;
  addEventListener(type: string, listener: () => void): void;
  removeEventListener(type: string, listener: () => void): void;
}

/** The knobs of the feed an application or a spec may turn. Every one has a default. */
export interface FirestoreFeedOptions {
  /**
   * A page that was hidden longer than this resyncs when it becomes visible again, since
   * a browser throttles or drops a background page's connection and the change documents
   * from that time may have expired unseen; the change document's life (liveChangeTtl) by
   * default.
   */
  resyncAfterHidden?: number;
  /** The wait before a failed listener is started again; doubles each failure up to maxRetryDelay. */
  retryDelay?: number;
  maxRetryDelay?: number;
  /** The clock, in milliseconds; Date.now by default. */
  now?: () => number;
  /** Where visibilitychange is heard; the document by default, nothing outside a browser. */
  visibility?: VisibilityEvents;
  /** Where pagehide and pageshow are heard; the window by default, nothing outside a browser. */
  page?: PageEvents;
}

/** A Firestore timestamp as the change document carries it; read structurally, so a spec needs no SDK class. */
interface TimestampLike {
  seconds: number;
  nanoseconds: number;
}

interface Change {
  event: ChangeEvent;
  at: TimestampLike;
}

let instances = 0;

/**
 * The Firestore change feed: one listener per tab over the user's change set,
 * `users/{uid}/changes`, ordered by `at` and bounded below by the last change seen. It
 * connects with the identity the server minted (`GET <prefix>/live/token`): the project,
 * the database, and either the emulator with a mock user token for the uid, or a custom
 * token signed in through Firebase Auth on an auth held in memory only, signed out at
 * stop. Each document becomes one ChangeEvent with `at` as unix microseconds.
 *
 * The first snapshot of a listener primes the last change seen and emits nothing: at
 * start the backlog predates the page's own first fetch, and after an error the
 * `resync` the recovery emits already refetches everything. The listener keeps its
 * connection and resumes after a drop, so a quiet stretch is not evidence of anything
 * missed; a change is lost only when the listener was down or suspended longer than the
 * change documents live. So the feed emits `resync` in two cases and no other: a failed
 * listener (which delivers nothing more) is started again with a growing wait and its
 * first snapshot is a resync, and a page that becomes visible again after being hidden
 * longer than the change document's life is a resync, since browsers throttle or drop a
 * background page's connection. The live session's own start refetches everything too.
 */
export class FirestoreChangeFeed implements ChangeFeed {
  private readonly resyncAfterHidden: number;
  private readonly retryDelay: number;
  private readonly maxRetryDelay: number;
  private readonly now: () => number;
  private readonly visibility: VisibilityEvents | undefined;
  private readonly page: PageEvents | undefined;
  private readonly changeListeners = new Set<(event: ChangeEvent) => void>();
  private readonly resyncListeners = new Set<() => void>();
  private app: FirebaseApp | undefined;
  private auth: Auth | undefined;
  private db: Firestore | undefined;
  private uid = '';
  private unsubscribe: Unsubscribe | undefined;
  private retry: ReturnType<typeof setTimeout> | undefined;
  private failures = 0;
  private lastSeen: TimestampLike | undefined;
  /**
   * The ids of the documents already delivered (or primed) at the `lastSeen` instant,
   * replaced whenever it advances. One commit writes its row and list documents with one
   * server timestamp, and a listener may deliver them in two snapshots, so a document at
   * the last instant seen is new unless its id is here.
   */
  private seenAtLastSeen = new Set<string>();
  /** When the page went hidden, while it is; undefined while visible. */
  private hiddenSince: number | undefined;
  private readonly onVisibilityChange = (): void => {
    if (this.visibility?.visibilityState === 'hidden') {
      this.hidden();
      return;
    }
    this.visible();
  };
  private readonly onPageHide = (): void => this.hidden();
  private readonly onPageShow = (event: { persisted?: boolean }): void => {
    if (event.persisted) {
      this.visible();
    }
  };

  constructor(options: FirestoreFeedOptions = {}) {
    this.resyncAfterHidden = options.resyncAfterHidden ?? liveChangeTtl;
    this.retryDelay = options.retryDelay ?? 1_000;
    this.maxRetryDelay = options.maxRetryDelay ?? 30_000;
    this.now = options.now ?? Date.now;
    this.visibility = options.visibility ?? (typeof document === 'undefined' ? undefined : document);
    this.page = options.page ?? (typeof window === 'undefined' ? undefined : window);
  }

  /** Whether a listener is held: the feed is between start and stop. */
  get started(): boolean {
    return this.app !== undefined;
  }

  async start(identity: LiveIdentity): Promise<void> {
    if (this.app) {
      await this.stop();
    }
    const app = initializeApp(
      { projectId: identity.project, apiKey: identity.apiKey || undefined },
      `impulseframework-resource-live-${++instances}`,
    );
    const db = identity.database ? getFirestore(app, identity.database) : getFirestore(app);
    let auth: Auth | undefined;
    if (identity.emulator) {
      const { host, port } = hostAndPort(identity.emulator);
      connectFirestoreEmulator(db, host, port, { mockUserToken: { sub: identity.uid, user_id: identity.uid } });
    } else {
      // The identity is minted for this session and revoked at stop, so the signed-in user
      // lives in memory only: nothing of it is written to the browser's storage, and a page
      // load starts with no user to reload.
      auth = initializeAuth(app, { persistence: inMemoryPersistence });
      await signInWithCustomToken(auth, identity.token);
    }
    this.app = app;
    this.auth = auth;
    this.db = db;
    this.uid = identity.uid;
    this.lastSeen = undefined;
    this.seenAtLastSeen = new Set();
    this.failures = 0;
    this.hiddenSince = this.visibility?.visibilityState === 'hidden' ? this.now() : undefined;
    this.visibility?.addEventListener('visibilitychange', this.onVisibilityChange);
    this.page?.addEventListener('pagehide', this.onPageHide);
    this.page?.addEventListener('pageshow', this.onPageShow);
    this.listen(false);
  }

  async stop(): Promise<void> {
    if (this.retry !== undefined) {
      clearTimeout(this.retry);
      this.retry = undefined;
    }
    this.visibility?.removeEventListener('visibilitychange', this.onVisibilityChange);
    this.page?.removeEventListener('pagehide', this.onPageHide);
    this.page?.removeEventListener('pageshow', this.onPageShow);
    this.hiddenSince = undefined;
    this.unsubscribe?.();
    this.unsubscribe = undefined;
    const app = this.app;
    const auth = this.auth;
    this.app = undefined;
    this.auth = undefined;
    this.db = undefined;
    this.uid = '';
    this.lastSeen = undefined;
    this.seenAtLastSeen = new Set();
    if (auth) {
      try {
        await signOut(auth);
      } catch {
        // The server revoked the identity before this; the app goes next either way.
      }
    }
    if (app) {
      try {
        await deleteApp(app);
      } catch {
        // The app is being discarded either way.
      }
    }
  }

  onChange(listener: (event: ChangeEvent) => void): () => void {
    this.changeListeners.add(listener);
    return () => {
      this.changeListeners.delete(listener);
    };
  }

  onResync(listener: () => void): () => void {
    this.resyncListeners.add(listener);
    return () => {
      this.resyncListeners.delete(listener);
    };
  }

  /** The page went hidden: the clock starts, once. */
  private hidden(): void {
    this.hiddenSince ??= this.now();
  }

  /** The page is visible again: hidden longer than the change documents live, everything is refetched. */
  private visible(): void {
    const since = this.hiddenSince;
    this.hiddenSince = undefined;
    if (since !== undefined && this.app && this.now() - since > this.resyncAfterHidden) {
      this.emitResync();
    }
  }

  /** Starts the listener over the change set from the last change seen; `recovering` says an error ended the one before. */
  private listen(recovering: boolean): void {
    const db = this.db;
    if (!db) {
      return;
    }
    const changes = collection(db, 'users', this.uid, 'changes');
    const bounded = this.lastSeen
      ? query(changes, where('at', '>', this.lastSeen), orderBy('at'))
      : query(changes, orderBy('at'));
    let primed = false;
    this.unsubscribe = onSnapshot(
      bounded,
      (snapshot) => {
        this.failures = 0;
        if (!primed) {
          primed = true;
          this.prime(snapshot);
          if (recovering) {
            this.emitResync();
          }
          return;
        }
        this.deliver(snapshot);
      },
      () => {
        // A listener that reported an error delivers nothing more: start another after a wait.
        this.unsubscribe = undefined;
        this.scheduleRestart();
      },
    );
  }

  private scheduleRestart(): void {
    if (!this.app || this.retry !== undefined) {
      return;
    }
    const delay = Math.min(this.maxRetryDelay, this.retryDelay * 2 ** this.failures);
    this.failures++;
    this.retry = setTimeout(() => {
      this.retry = undefined;
      this.listen(true);
    }, delay);
  }

  /** The first snapshot of a listener: the latest change in it is the last seen, its documents at that instant are seen, and nothing is emitted. */
  private prime(snapshot: QuerySnapshot<DocumentData>): void {
    for (const doc of snapshot.docs) {
      const change = toChange(doc.data());
      if (change) {
        this.advance(doc.id, change.at);
      }
    }
  }

  /**
   * A later snapshot: the documents added or moved past the last change seen are events,
   * in `at` order, and so is a document at that very instant not delivered yet.
   */
  private deliver(snapshot: QuerySnapshot<DocumentData>): void {
    const changes: { id: string; change: Change }[] = [];
    for (const entry of snapshot.docChanges()) {
      if (entry.type === 'removed') {
        continue;
      }
      const change = toChange(entry.doc.data());
      if (change && this.unseen(entry.doc.id, change.at)) {
        changes.push({ id: entry.doc.id, change });
      }
    }
    changes.sort((a, b) => compare(a.change.at, b.change.at));
    for (const { id, change } of changes) {
      this.advance(id, change.at);
      for (const listener of [...this.changeListeners]) {
        listener(change.event);
      }
    }
  }

  /** Whether a document at `at` has not been delivered: later than the last seen, or at that instant with an id not yet seen. */
  private unseen(id: string, at: TimestampLike): boolean {
    if (this.lastSeen === undefined) {
      return true;
    }
    const order = compare(at, this.lastSeen);
    return order > 0 || (order === 0 && !this.seenAtLastSeen.has(id));
  }

  /** Marks a document seen: a later instant replaces the last seen and its set of ids; the same instant adds the id. */
  private advance(id: string, at: TimestampLike): void {
    const order = this.lastSeen === undefined ? 1 : compare(at, this.lastSeen);
    if (order > 0) {
      this.lastSeen = at;
      this.seenAtLastSeen = new Set([id]);
    } else if (order === 0) {
      this.seenAtLastSeen.add(id);
    }
  }

  private emitResync(): void {
    for (const listener of [...this.resyncListeners]) {
      listener();
    }
  }
}

/** The Firestore change feed over the firebase JS SDK. Hand it to `client.live.start`. */
export function firestoreChangeFeed(options?: FirestoreFeedOptions): ChangeFeed {
  return new FirestoreChangeFeed(options);
}

/** `host:port` as the identity carries the emulator, the host bracketed when it is an IPv6 address. */
export function hostAndPort(address: string): { host: string; port: number } {
  const mark = address.lastIndexOf(':');
  if (mark === -1) {
    throw new Error(`the emulator address "${address}" has no port`);
  }
  const host = address.slice(0, mark).replace(/^\[(.*)\]$/, '$1');
  const port = Number(address.slice(mark + 1));
  if (host === '' || !Number.isInteger(port) || port <= 0) {
    throw new Error(`the emulator address "${address}" is not host:port`);
  }
  return { host, port };
}

/** A Firestore timestamp as unix microseconds in decimal: the version a refetch carries. */
export function microseconds(at: TimestampLike): string {
  return (BigInt(at.seconds) * 1_000_000n + BigInt(Math.floor(at.nanoseconds / 1_000))).toString();
}

function isTimestamp(value: unknown): value is TimestampLike {
  return (
    typeof value === 'object' &&
    value !== null &&
    typeof (value as TimestampLike).seconds === 'number' &&
    typeof (value as TimestampLike).nanoseconds === 'number'
  );
}

function compare(a: TimestampLike, b: TimestampLike): number {
  return a.seconds === b.seconds ? a.nanoseconds - b.nanoseconds : a.seconds - b.seconds;
}

/**
 * One change document as an event: `kind`, `resource`, and a resolved `at` are required
 * (a document whose server timestamp has not resolved, or of a shape this feed does not
 * know, is skipped); `key`, `domain`, and `deleted` ride along when present.
 */
function toChange(data: DocumentData): Change | undefined {
  const kind = data['kind'];
  const resource = data['resource'];
  const at = data['at'];
  if ((kind !== 'row' && kind !== 'list' && kind !== 'resource') || typeof resource !== 'string' || !isTimestamp(at)) {
    return undefined;
  }
  const event: ChangeEvent = { kind, resource, at: microseconds(at) };
  if (typeof data['key'] === 'string') {
    event.key = data['key'];
  }
  if (typeof data['domain'] === 'string') {
    event.domain = data['domain'];
  }
  if (typeof data['deleted'] === 'boolean') {
    event.deleted = data['deleted'];
  }
  return { event, at };
}
