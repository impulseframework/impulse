import { NO_POSITION, placeWords } from './place-words';

// A place is written the way a person reads one: the latitude and the longitude with the
// side of the globe each is on, never the GeoJSON the field carries. Every shape that can
// reach a field reads as what it is and where it starts; a value that is not a place is
// left to its own rule.

/** The first position the cases share: a longitude, then a latitude, as GeoJSON orders them. */
const here = [-118.2437, 34.0522];
const there = [-115.1398, 36.1699];
const beyond = [-112.074, 33.4484];

/** A square the cases use as an area's outline: four corners, closed on the first. */
const square = [here, there, beyond, [-117.1611, 32.7157], here];

describe('placeWords', () => {
  const places: { name: string; value: unknown; want: string }[] = [
    {
      name: 'a point is its latitude then its longitude, each with its side of the globe',
      value: { type: 'Point', coordinates: here },
      want: '34.0522° N, 118.2437° W',
    },
    {
      name: 'a point south of the equator and east of the meridian reads S and E',
      value: { type: 'Point', coordinates: [151.2093, -33.8688] },
      want: '33.8688° S, 151.2093° E',
    },
    {
      name: 'a coordinate is written to five decimal places at most, about a meter',
      value: { type: 'Point', coordinates: [-118.24368312, 34.05223419] },
      want: '34.05223° N, 118.24368° W',
    },
    {
      name: 'a point on the equator and the meridian reads north and east',
      value: { type: 'Point', coordinates: [0, 0] },
      want: '0° N, 0° E',
    },
    {
      name: 'a coordinate that rounds to nothing is not written as south or west of it',
      value: { type: 'Point', coordinates: [-0.000001, -0.000001] },
      want: '0° N, 0° E',
    },
    {
      name: 'a point with a height says its altitude in meters',
      value: { type: 'Point', coordinates: [...here, 120.25] },
      want: '34.0522° N, 118.2437° W, altitude 120.3 m',
    },
    {
      name: 'several points are counted, from the first',
      value: { type: 'MultiPoint', coordinates: [here, there, beyond] },
      want: '3 points, starting at 34.0522° N, 118.2437° W',
    },
    {
      name: 'a collection of one point is the point',
      value: { type: 'MultiPoint', coordinates: [here] },
      want: '34.0522° N, 118.2437° W',
    },
    {
      name: 'a line is the points it runs through, from the first',
      value: { type: 'LineString', coordinates: [here, there, beyond] },
      want: 'A line of 3 points, starting at 34.0522° N, 118.2437° W',
    },
    {
      name: 'several lines are counted, from where the first starts',
      value: {
        type: 'MultiLineString',
        coordinates: [
          [there, beyond],
          [here, there],
        ],
      },
      want: '2 lines, starting at 36.1699° N, 115.1398° W',
    },
    {
      name: "an area is its outline's corners, the closing position counted once",
      value: { type: 'Polygon', coordinates: [square] },
      want: 'An area with 4 corners, starting at 34.0522° N, 118.2437° W',
    },
    {
      name: 'an area says how many holes it has',
      value: {
        type: 'Polygon',
        coordinates: [square, [here, there, beyond, here]],
      },
      want: 'An area with 4 corners and 1 hole, starting at 34.0522° N, 118.2437° W',
    },
    {
      name: 'several areas are counted, from where the first starts',
      value: { type: 'MultiPolygon', coordinates: [[square], [square], [square]] },
      want: '3 areas, starting at 34.0522° N, 118.2437° W',
    },
    {
      name: 'a mix of shapes is counted, from where the first starts',
      value: {
        type: 'GeometryCollection',
        geometries: [
          { type: 'Point', coordinates: there },
          { type: 'LineString', coordinates: [here, beyond] },
        ],
      },
      want: '2 shapes, starting at 36.1699° N, 115.1398° W',
    },
    {
      name: 'a feature is the place its geometry names',
      value: { type: 'Feature', properties: { name: 'Relay 7' }, geometry: { type: 'Point', coordinates: here } },
      want: '34.0522° N, 118.2437° W',
    },
    {
      name: 'several features are counted as places',
      value: {
        type: 'FeatureCollection',
        features: [
          { type: 'Feature', properties: null, geometry: { type: 'Point', coordinates: here } },
          { type: 'Feature', properties: null, geometry: { type: 'Point', coordinates: there } },
        ],
      },
      want: '2 places, starting at 34.0522° N, 118.2437° W',
    },
    {
      name: 'a point with no coordinates has no position',
      value: { type: 'Point', coordinates: [] },
      want: NO_POSITION,
    },
    {
      name: 'a feature with no geometry has no position',
      value: { type: 'Feature', geometry: null },
      want: NO_POSITION,
    },
    {
      name: 'an empty collection has no position',
      value: { type: 'GeometryCollection', geometries: [] },
      want: NO_POSITION,
    },
  ];

  for (const tt of places) {
    it(tt.name, () => {
      expect(placeWords(tt.value)).toBe(tt.want);
    });
  }

  const others: { name: string; value: unknown }[] = [
    { name: 'null', value: null },
    { name: 'a string', value: 'free text' },
    { name: 'an array', value: here },
    { name: 'an object of another kind', value: { vessel: 'Kestrel', decks: 3 } },
    { name: 'an object whose type is no shape', value: { type: 'Circle', coordinates: here } },
    { name: 'a point whose coordinates are not numbers', value: { type: 'Point', coordinates: ['34', '-118'] } },
    { name: 'a point with one coordinate', value: { type: 'Point', coordinates: [34] } },
    { name: 'a point whose latitude is past the pole', value: { type: 'Point', coordinates: [34.0522, -118.2437] } },
    { name: 'a point whose longitude is past 180', value: { type: 'Point', coordinates: [181, 0] } },
    { name: 'a line with a position that is none', value: { type: 'LineString', coordinates: [here, 'there'] } },
    {
      name: 'a mix of shapes with one that is none',
      value: { type: 'GeometryCollection', geometries: [{ type: 'Point', coordinates: here }, { kind: 'other' }] },
    },
  ];

  for (const tt of others) {
    it(`${tt.name} is not a place`, () => {
      expect(placeWords(tt.value)).toBeUndefined();
    });
  }
});
