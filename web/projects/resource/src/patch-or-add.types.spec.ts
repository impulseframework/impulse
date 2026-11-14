import { describe, expect, it } from 'bun:test';
import { PatchOrAddValue, ResourceHandle } from './client';

// The type-level half of patch-or-add: the value is written whether the server finds the
// row or not, so it carries what a create must have and what a patch may change, and a
// field only a create takes does not compile. The `@ts-expect-error` lines are the
// assertions: `tsc -p projects/resource/tsconfig.spec.json` fails if one of them stops
// being an error. The runtime half is in batch.spec.ts.

interface Seat {
  cabinId: string;
  number: number;
  label: string;
  note: string | null;
  bookedBy: string;
}

/** The shapes the generator emits: a create with the key and a field only a create takes, a patch without either. */
interface SeatCreate {
  cabinId: string;
  number: number;
  label: string;
  note?: string | null;
  bookedBy?: string;
}
interface SeatPatch {
  label?: string;
  note?: string | null;
}
type SeatKey = [cabinId: string, number: number];

/** A resource whose key the client supplies, with both operations. */
type SeatHandle = ResourceHandle<Seat, SeatKey, 'create' | 'patch' | 'batch' | 'patchOrAdd' | 'addOrSkip', SeatCreate, SeatPatch>;
/** One whose create must carry a field no patch may carry: the generator leaves `patchOrAdd` out. */
type CreateOnlyHandle = ResourceHandle<Seat, SeatKey, 'create' | 'patch' | 'batch' | 'addOrSkip', SeatCreate, SeatPatch>;
/** One whose id the server makes: neither operation. */
type ServerKeyedHandle = ResourceHandle<Seat, [id: string], 'create' | 'patch' | 'batch', SeatCreate, SeatPatch>;

type Value = PatchOrAddValue<SeatCreate, SeatPatch>;

describe('patch-or-add value type', () => {
  it('takes what a create must have and what a patch may change, and no field only a create takes', () => {
    const whole: Value = { cabinId: 'c1', number: 4, label: 'Window', note: null };
    const least: Value = { cabinId: 'c1', number: 4, label: 'Window' };
    // @ts-expect-error a patch refuses bookedBy, so the operation would fail on the day the row is there
    const createOnly: Value = { cabinId: 'c1', number: 4, label: 'Window', bookedBy: 'u1' };
    // @ts-expect-error the row may have to be created: a field a create must have cannot be left out
    const short: Value = { cabinId: 'c1', number: 4 };
    // @ts-expect-error nor a key field: the operation would name no row
    const keyless: Value = { number: 4, label: 'Window' };
    expect([whole, least, createOnly, short, keyless]).toHaveLength(5);
  });

  it('exists on the handle only where the generator names the operation', () => {
    const named = (handle: SeatHandle) => [handle.ops.patchOrAdd, handle.ops.addOrSkip];
    // An add-or-skip is a create, so it takes the field only a create takes.
    const skip = (handle: SeatHandle) => handle.ops.addOrSkip({ cabinId: 'c1', number: 4, label: 'Window', bookedBy: 'u1' });
    // @ts-expect-error no one value serves both the add and the patch of this resource
    const refused = (handle: CreateOnlyHandle) => handle.ops.patchOrAdd;
    const kept = (handle: CreateOnlyHandle) => handle.ops.addOrSkip;
    // @ts-expect-error a server-made id names no row that may or may not be there
    const unnamed = (handle: ServerKeyedHandle) => handle.ops.patchOrAdd;
    // @ts-expect-error nor does an add-or-skip
    const unskipped = (handle: ServerKeyedHandle) => handle.ops.addOrSkip;
    expect([named, skip, refused, kept, unnamed, unskipped]).toHaveLength(6);
  });
});
