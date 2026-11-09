# @impulseframework/resource-firestore

The Firestore change feed for live pages over [`@impulseframework/resource`](../resource/README.md).
A page that asks for a live list or a live row stays current without polling: the server
writes one change document per subscribed user into that user's change set, and this feed
is the one listener per tab that reads it and hands each change to the client's live
session, which refetches the row or list the change names. It is the only package of the
three that depends on the firebase JS SDK; the client stays free of it.

What a page opts into, what it costs, and what it never does are in the client's README
under "Live pages". This package adds nothing to that contract; it is one way of delivering
the changes, and the client is written so another could replace it.

```bash
npm install @impulseframework/resource @impulseframework/resource-firestore firebase
```

`firebase` is a peer dependency: the application installs it once, so one copy of the SDK
serves the feed and anything else the application does with Firebase.

## Use

Build the feed and hand it to the client's live session once the session is authenticated;
stop the session at logout, before the session itself is logged out.

```ts
import { firestoreChangeFeed } from '@impulseframework/resource-firestore';

const api = createApi({ baseUrl: '/api' });
await api.live.start(firestoreChangeFeed());
// ...
await api.live.stop();
```

An Angular application provides it instead, and `AuthService` starts and stops it:

```ts
import { CHANGE_FEED } from '@impulseframework/resource-angular/types';
import { firestoreChangeFeed } from '@impulseframework/resource-firestore';

providers: [{ provide: CHANGE_FEED, useFactory: () => firestoreChangeFeed() }];
```

## What the feed does

`start(identity)` receives what the server minted for the session's principal at
`GET <prefix>/live/token`: the GCP project, the Firestore database, and either the emulator
or a custom token. It initializes a Firebase app of its own for the project (and the web API
key, in production), opens the database, and connects as the user: against the emulator,
with the SDK's mock user token for the uid (`connectFirestoreEmulator(db, host, port,
{ mockUserToken: { sub: uid, user_id: uid } })`); in production, by signing the custom token
in through Firebase Auth on an auth held in memory only: the identity is minted for the
session and revoked at stop, so nothing of it is written to the browser's storage, and a
page load starts with no user to reload. Then it listens to `users/{uid}/changes`, ordered
by `at` and, once a change has been seen, bounded to the ones after it.

The first snapshot of a listener primes the last change seen and emits nothing. At start, the
documents already there predate the page's own first fetch; after an error, the resync the
recovery emits already refetches everything. Each later document becomes one `ChangeEvent`:
its `kind` (`row`, `list`, or `resource`), `resource`, `key`, `domain`, and `deleted` as the
document carries them, and `at` as unix microseconds in decimal, the version the refetch
carries. A document rewritten within its second (the server coalesces writes to one target
per second) moves forward and is one more event; a document expiring out of the result set
is not.

A quiet stretch is not evidence that anything was missed: the listener keeps its connection
and resumes after a drop, and the change documents live ten minutes, so a change is lost only
when the listener was down or suspended for longer than that. The feed therefore emits
`resync` on two occasions and no other. When a listener reports an error it delivers nothing
more, so the feed starts another after a wait that doubles with each failure (one second, up
to thirty), bounded by the last change seen, and the first snapshot of that listener is a
resync. And when the page becomes visible again (`visibilitychange` to visible, or `pageshow`
from the back-forward cache) after having been hidden longer than the change documents live,
since browsers throttle or drop a background page's connection, the feed emits a resync. The
live session refetches every live row and list by a fresh seed either way, and does the same
itself when it starts.

`stop()` releases the listener, signs the identity out, and deletes the app it initialized.
Nothing is remembered across a stop: a later start listens from the beginning again.

The security rules the server leg ships let a user read their own change set and nothing
else, and let no client write; the feed never writes.

## What is configurable

`firestoreChangeFeed(options)` takes `resyncAfterHidden` (how long a page may be hidden before
its return is a resync; the change document's life by default), `retryDelay` and
`maxRetryDelay` (the restart wait), `now` (the clock), and `visibility` and `page` (where the
document's visibility and the page's hide and show are heard; the document and the window by
default). Every one has a default, and a spec is the usual reason to turn one.

## Testing

The specs are written against `bun:test` and run with `bun test projects/resource-firestore/`
(the trailing slash keeps bun off the Angular library beside it); `bun run typecheck:firestore`
compiles them with tsc over `projects/resource-firestore/tsconfig.spec.json`.
`bun run test:firestore` runs both. The specs replace the three SDK modules (`firebase/app`,
`firebase/auth`, `firebase/firestore`) with recording stand-ins through `mock.module`, drive
the listener's snapshots and errors by hand, stand in for the document and the window to hide
and show the page, and read the events and resyncs back; no Firestore, emulated or real, is
involved.
