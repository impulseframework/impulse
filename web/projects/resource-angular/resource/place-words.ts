// A place in the words a person reads it by. A field that holds a position carries it as
// GeoJSON (RFC 7946): `{"type":"Point","coordinates":[-118.2437,34.0522]}`, the longitude
// before the latitude. A list's cell, a row page and a form write it as
// `34.0522° N, 118.2437° W`, latitude first as a person says it, and never as the JSON.
//
// The server keeps whatever JSON a client sent, so a shape other than a point can reach a
// field declared as a point. Each one reads as what it is and where it starts:
//
//   Point               34.0522° N, 118.2437° W
//   Point, with height  34.0522° N, 118.2437° W, altitude 120 m
//   MultiPoint          3 points, starting at 34.0522° N, 118.2437° W
//   LineString          A line of 3 points, starting at 34.0522° N, 118.2437° W
//   MultiLineString     2 lines, starting at 34.0522° N, 118.2437° W
//   Polygon             An area with 4 corners, starting at 34.0522° N, 118.2437° W
//   Polygon, with holes An area with 4 corners and 1 hole, starting at 34.0522° N, 118.2437° W
//   MultiPolygon        2 areas, starting at 34.0522° N, 118.2437° W
//   GeometryCollection  3 shapes, starting at 34.0522° N, 118.2437° W
//   Feature             its geometry's words
//   FeatureCollection   3 places, starting at 34.0522° N, 118.2437° W
//
// A collection of one reads as the one it holds, and a shape that holds no coordinates
// reads `No position`. A value that is not GeoJSON a person could be shown as a place (a
// longitude past 180, a latitude past 90, a coordinate that is not a number, another
// object altogether) is not a place: placeWords answers undefined, and the caller writes
// the value by its own rule.

/** The most decimal places a coordinate is written with: five is about a meter on the ground. */
export const COORDINATE_DECIMALS = 5;

/** What a shape that holds no coordinates reads as. */
export const NO_POSITION = 'No position';

/**
 * One shape read. A shape that holds no coordinates is empty; any other has the first
 * position it holds, and, unless it is a point, what it is in words (`A line of 3 points`).
 */
type Reading = { empty: true } | { empty: false; words?: string; start: number[] };

/** The reading of a shape that holds no coordinates. */
const EMPTY: Reading = { empty: true };

/**
 * The place a GeoJSON value names, in words, or undefined for a value that is not
 * GeoJSON. The table at the head of this file is what each shape reads as.
 */
export function placeWords(value: unknown): string | undefined {
  const reading = read(value);
  if (reading === undefined) {
    return undefined;
  }
  if (reading.empty) {
    return NO_POSITION;
  }
  const start = positionWords(reading.start);
  return reading.words === undefined ? start : `${reading.words}, starting at ${start}`;
}

/** One position as a person says it: the latitude, the longitude, and the altitude where it has one. */
function positionWords(position: number[]): string {
  const [longitude, latitude, altitude] = position;
  const place = `${degrees(latitude, 'N', 'S')}, ${degrees(longitude, 'E', 'W')}`;
  return altitude === undefined ? place : `${place}, altitude ${Number(altitude.toFixed(1))} m`;
}

/** A coordinate as a size and the side of the equator or the meridian it is on: `118.2437° W`. */
function degrees(value: number, positive: string, negative: string): string {
  const size = Number(Math.abs(value).toFixed(COORDINATE_DECIMALS));
  return `${size}° ${value < 0 && size !== 0 ? negative : positive}`;
}

/** A count with its noun: `1 point`, `3 points`. */
function counted(count: number, noun: string): string {
  return `${count} ${noun}${count === 1 ? '' : 's'}`;
}

/** Whether the value is an object with members, the only thing a GeoJSON value is. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Whether the value is a position on the globe: a longitude, a latitude, and perhaps an altitude. */
function isPosition(value: unknown): value is number[] {
  if (!Array.isArray(value) || value.length < 2) {
    return false;
  }
  if (!value.every((member) => typeof member === 'number' && Number.isFinite(member))) {
    return false;
  }
  const [longitude, latitude] = value as number[];
  return Math.abs(longitude) <= 180 && Math.abs(latitude) <= 90;
}

/** Whether the value is a list of positions: a line, a ring of an area, or loose points. */
function isPositions(value: unknown): value is number[][] {
  return Array.isArray(value) && value.every(isPosition);
}

/** Whether the value is the rings of one area: its outline, then its holes. */
function isRings(value: unknown): value is number[][][] {
  return Array.isArray(value) && value.every(isPositions);
}

/** Reads any GeoJSON value; undefined for anything else. */
function read(value: unknown): Reading | undefined {
  if (!isRecord(value)) {
    return undefined;
  }
  const coordinates = value['coordinates'];
  switch (value['type']) {
    case 'Point':
      return readPoint(coordinates);
    case 'MultiPoint':
      return Array.isArray(coordinates) ? readSeveral(coordinates.map(readPoint), 'point') : undefined;
    case 'LineString':
      return readLine(coordinates);
    case 'MultiLineString':
      return Array.isArray(coordinates) ? readSeveral(coordinates.map(readLine), 'line') : undefined;
    case 'Polygon':
      return readArea(coordinates);
    case 'MultiPolygon':
      return Array.isArray(coordinates) ? readSeveral(coordinates.map(readArea), 'area') : undefined;
    case 'GeometryCollection': {
      const geometries = value['geometries'];
      return Array.isArray(geometries) ? readSeveral(geometries.map(read), 'shape') : undefined;
    }
    case 'Feature':
      return value['geometry'] === null ? EMPTY : read(value['geometry']);
    case 'FeatureCollection': {
      const features = value['features'];
      return Array.isArray(features) ? readSeveral(features.map(read), 'place') : undefined;
    }
    default:
      return undefined;
  }
}

/** Reads a point's coordinates: a point is its position and has no other words. */
function readPoint(coordinates: unknown): Reading | undefined {
  if (Array.isArray(coordinates) && coordinates.length === 0) {
    return EMPTY;
  }
  return isPosition(coordinates) ? { empty: false, start: coordinates } : undefined;
}

/** Reads a line's coordinates: how many points it runs through, and its first. */
function readLine(coordinates: unknown): Reading | undefined {
  if (!isPositions(coordinates)) {
    return undefined;
  }
  if (coordinates.length === 0) {
    return EMPTY;
  }
  return { empty: false, words: `A line of ${counted(coordinates.length, 'point')}`, start: coordinates[0] };
}

/**
 * Reads an area's rings: the corners of its outline, how many holes it has, and its
 * first corner. A ring ends on the position it began on, which is one corner, not two.
 */
function readArea(coordinates: unknown): Reading | undefined {
  if (!isRings(coordinates)) {
    return undefined;
  }
  const outline = coordinates[0];
  if (outline === undefined || outline.length === 0) {
    return EMPTY;
  }
  const first = outline[0];
  const last = outline[outline.length - 1];
  const closed = outline.length > 1 && first.every((member, index) => member === last[index]);
  const corners = counted(closed ? outline.length - 1 : outline.length, 'corner');
  const holes = coordinates.length - 1;
  return {
    empty: false,
    words: holes > 0 ? `An area with ${corners} and ${counted(holes, 'hole')}` : `An area with ${corners}`,
    start: first,
  };
}

/**
 * Reads a collection from the readings of its members: one member reads as itself, and
 * several as their count and the first position any of them holds. A collection with no
 * member, or whose members hold no coordinates, is empty; a member that could not be
 * read makes the whole collection unreadable.
 */
function readSeveral(members: (Reading | undefined)[], noun: string): Reading | undefined {
  let start: number[] | undefined;
  for (const member of members) {
    if (member === undefined) {
      return undefined;
    }
    if (!member.empty) {
      start ??= member.start;
    }
  }
  if (start === undefined) {
    return EMPTY;
  }
  if (members.length === 1) {
    return members[0];
  }
  return { empty: false, words: counted(members.length, noun), start };
}
