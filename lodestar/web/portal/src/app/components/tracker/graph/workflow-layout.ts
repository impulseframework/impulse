import { Workflow } from '@app/service/zz_gen_resources';

/** Which way a workflow is drawn: its route from left to right, or from top to bottom where there is little width. */
export type GraphDirection = 'across' | 'down';

/** A state of the workflow, placed on the drawing. */
export interface GraphNode {
  state: string;
  x: number;
  y: number;
  terminal: boolean;
  isDefault: boolean;
}

/** One transition out of one state, drawn: its line, and where its label sits. */
export interface GraphEdge {
  method: string;
  /** What the edge is called on the graph, and what a screen reader says of it. */
  words: string;
  from: string;
  to: string;
  path: string;
  labelX: number;
  labelY: number;
}

/** A workflow laid out: which way, the drawing's size in its own units, its states and its edges. */
export interface GraphLayout {
  direction: GraphDirection;
  width: number;
  height: number;
  nodes: GraphNode[];
  edges: GraphEdge[];
}

/** A point of the drawing, in the drawing's own units. */
interface Point {
  x: number;
  y: number;
}

/** A rectangle of the drawing, in the drawing's own units. */
export interface Box {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

/** A line as it is drawn: what the path says, and points along it, close enough that no label's room is stepped over. */
interface Line {
  path: string;
  along: Point[];
}

/** One transition out of one state, before it is drawn, with its place among the workflow's edges. */
interface Pair {
  method: string;
  from: string;
  to: string;
  i: number;
}

/**
 * The width, in pixels, under which a graph is drawn top to bottom. Across, the drawing is
 * 620 units wide and its labels 9 units high, so in less than 560 pixels the labels would
 * be drawn under 8 pixels high, too small to read. Down, the drawing is about half as
 * wide, and keeps its full size on a card 320 pixels wide.
 */
export const downUnder = 560;

/** Which way a graph is drawn in the width it has, in pixels. A width not known yet (0) draws it across. */
export function directionFor(width: number): GraphDirection {
  return width > 0 && width < downUnder ? 'down' : 'across';
}

/** Half a state's box, across and down. */
const nodeHalfWidth = 36;
const nodeHalfHeight = 14;

/** The drawing's width when the route goes across; its height follows from the workflow. */
const acrossWidth = 620;
/** How far under the route's row the row of terminal states sits, at the least. */
const rowGap = 90;
/** How far a transition between neighbors on the route bows over the row, and under it going back. */
const bowOver = 28;
const bowUnder = 34;
/** How much further a transition bows for each state it passes over, so one arc is never drawn on another. */
const bowStep = 22;

/** Where the route's column stands when the route goes down. */
const columnX = 40;
/** How far right of the column the labels of the lines that leave it sideways begin. */
const besideColumn = 14;
/** How far apart two lines run where they go up or down side by side. */
const railStep = 8;
/** How far under a state its first line out of the underside turns, and how far under that one the next. */
const firstRow = 12;
const rowStep = 16;
/** How far above and below a state's middle a line comes in and another leaves, where its side takes both. */
const sideShift = 7;

/**
 * The room a label takes, around the point its text is centered on: the label is set at
 * 9 units, where a letter is at most 5.6 wide on average, bold included, and the room has
 * a little air around it.
 */
export function labelBox(x: number, y: number, words: string): Box {
  const half = (words.length * 5.6 + 6) / 2;
  return { left: x - half, top: y - 9, right: x + half, bottom: y + 3 };
}

/** The room a state's box takes. */
export function nodeBox(node: { x: number; y: number }): Box {
  return {
    left: node.x - nodeHalfWidth,
    top: node.y - nodeHalfHeight,
    right: node.x + nodeHalfWidth,
    bottom: node.y + nodeHalfHeight,
  };
}

/** Whether two rectangles share any room. */
export function overlap(a: Box, b: Box): boolean {
  return a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom;
}

/** A number as a path carries it: to a tenth of a unit. */
function tenth(n: number): number {
  return Math.round(n * 10) / 10;
}

/** A line drawn straight from point to point. */
function straight(points: Point[]): Line {
  const along: Point[] = [];
  points.slice(1).forEach((b, i) => {
    const a = points[i];
    if (!a) return;
    const steps = Math.max(1, Math.ceil(Math.hypot(b.x - a.x, b.y - a.y)));
    for (let s = i === 0 ? 0 : 1; s <= steps; s++) {
      along.push({ x: a.x + ((b.x - a.x) * s) / steps, y: a.y + ((b.y - a.y) * s) / steps });
    }
  });
  return {
    path: points.map((p, i) => `${i === 0 ? 'M' : 'L'} ${tenth(p.x)} ${tenth(p.y)}`).join(' '),
    along,
  };
}

/** A line drawn as one curve from a to b, bowed toward a point. */
function curve(a: Point, toward: Point, b: Point): Line {
  const steps = Math.max(
    1,
    Math.ceil(Math.hypot(toward.x - a.x, toward.y - a.y) + Math.hypot(b.x - toward.x, b.y - toward.y)),
  );
  const along: Point[] = [];
  for (let s = 0; s <= steps; s++) {
    const t = s / steps;
    along.push({
      x: (1 - t) * (1 - t) * a.x + 2 * (1 - t) * t * toward.x + t * t * b.x,
      y: (1 - t) * (1 - t) * a.y + 2 * (1 - t) * t * toward.y + t * t * b.y,
    });
  }
  return {
    path: `M ${tenth(a.x)} ${tenth(a.y)} Q ${tenth(toward.x)} ${tenth(toward.y)} ${tenth(b.x)} ${tenth(b.y)}`,
    along,
  };
}

/**
 * The room kept clear of other lines around a label: its own room and a little more,
 * since the letters are drawn with the ground's color around them.
 */
function clearOf(point: Point, words: string): Box {
  const room = labelBox(point.x, point.y, words);
  return { left: room.left - 1, top: room.top - 2, right: room.right + 1, bottom: room.bottom + 2 };
}

/** How many of a line's points are inside a room. */
function struck(room: Box, line: Line): number {
  return line.along.filter((p) => p.x > room.left && p.x < room.right && p.y > room.top && p.y < room.bottom).length;
}

/**
 * The order the states are walked in, and the states nothing leaves. The route starts at
 * the default state and follows the transitions, the nearest states first, so it reads
 * in the order the work is done; a state no transition reaches from there comes after.
 */
function routeOf(workflow: Workflow): { route: string[]; terminals: string[] } {
  const sources = new Set(workflow.transitions.flatMap((t) => t.from));
  const route = [workflow.defaultState];
  for (let i = 0; i < route.length; i++) {
    for (const transition of workflow.transitions) {
      if (transition.from.includes(route[i] ?? '') && sources.has(transition.to) && !route.includes(transition.to)) {
        route.push(transition.to);
      }
    }
    if (i === route.length - 1) {
      const unreached = workflow.states.find((s) => sources.has(s) && !route.includes(s));
      if (unreached !== undefined) route.push(unreached);
    }
  }
  return { route, terminals: workflow.states.filter((s) => !route.includes(s)) };
}

/** Every transition out of every state it leaves, in the workflow's order. */
function pairsOf(workflow: Workflow): Pair[] {
  return workflow.transitions
    .flatMap((t) => t.from.map((from) => ({ method: t.method, from, to: t.to })))
    .map((pair, i) => ({ ...pair, i }));
}

/**
 * Lays a workflow out, its route across or down. Either way no label shares room with
 * another label or with a state, and no line crosses the room of another line's label: a
 * label is the only way to tell which edge is which, and a line through it strikes it out.
 * Down, that holds for any workflow, by the way the drawing is made. Across, each label
 * looks for a clear place, and a workflow that leaves one of its labels none is drawn
 * down instead, whatever was asked: the result says which way it went.
 */
export function layoutWorkflow(
  workflow: Workflow,
  words: (method: string) => string,
  direction: GraphDirection = 'across',
): GraphLayout {
  return (direction === 'across' ? layoutAcross(workflow, words) : undefined) ?? layoutDown(workflow, words);
}

/**
 * The route across: the states a transition leaves on one row, the default state first,
 * and the terminal states on a row beneath, each as near under the states that reach it
 * as the others allow. A transition along the route is an arc over the row, or under it
 * going back, that bows further the more states it passes, so two arcs over the same
 * stretch are apart and so are their labels. A transition to a terminal state is a
 * straight line down. Every line is settled first; then each label takes the first place
 * on its own line, or just beside it, that covers no other label, no state and no part of
 * another line. The arcs are labeled first, then the shortest lines: they have the fewest
 * places to choose from. Where a label finds no such place, there is no drawing across.
 */
function layoutAcross(workflow: Workflow, words: (method: string) => string): GraphLayout | undefined {
  const width = acrossWidth;
  const { route, terminals } = routeOf(workflow);
  const column = new Map(route.map((state, i) => [state, i]));
  const pairs = pairsOf(workflow);
  /** How many steps of the route a transition takes, where both its ends are on the route. */
  const steps = (pair: Pair): number => (column.get(pair.to) ?? 0) - (column.get(pair.from) ?? 0);
  const onRoute = pairs.filter((p) => column.has(p.from) && column.has(p.to));

  // The arcs over the row need room above it, and the arcs under it room above the row beneath.
  const over = Math.max(1, ...onRoute.map((p) => steps(p)));
  const under = Math.max(0, ...onRoute.map((p) => -steps(p)));
  const top = 45 + bowStep * (over - 1);
  const bottom = top + Math.max(rowGap, under === 0 ? 0 : bowUnder + bowStep * (under - 1) + 46);
  const step = (width - 80) / Math.max(1, route.length - 1);
  const terminalStep = (width - 80) / (terminals.length + 1);
  /** Where a terminal state would sit to be under the states that reach it: the middle of their columns. */
  const wantedColumn = (terminal: string): number => {
    const from = pairs.filter((p) => p.to === terminal).map((p) => column.get(p.from) ?? 0);
    return from.length === 0 ? route.length : from.reduce((sum, c) => sum + c, 0) / from.length;
  };
  const ordered = terminals
    .map((state, i) => ({ state, i, wanted: wantedColumn(state) }))
    .sort((a, b) => a.wanted - b.wanted || a.i - b.i);
  const nodes: GraphNode[] = [
    ...route.map((state, i) => ({
      state,
      x: 40 + i * step,
      y: top,
      terminal: false,
      isDefault: state === workflow.defaultState,
    })),
    ...ordered.map(({ state }, i) => ({
      state,
      x: 40 + (i + 1) * terminalStep,
      y: bottom,
      terminal: true,
      isDefault: false,
    })),
  ];
  const height = bottom + 35;
  const placed = new Map(nodes.map((node) => [node.state, node]));

  // Every line first: a label is placed against all of them.
  const lines = new Map<number, Line>();
  for (const pair of pairs) {
    const a = placed.get(pair.from);
    const b = placed.get(pair.to);
    if (!a || !b) continue;
    if (a.y === b.y) {
      const passed = Math.abs(steps(pair)) - 1;
      const bow = a.x < b.x ? -(bowOver + bowStep * passed) : bowUnder + bowStep * passed;
      lines.set(pair.i, curve(a, { x: (a.x + b.x) / 2, y: a.y + bow }, b));
    } else {
      lines.set(pair.i, straight([a, b]));
    }
  }

  const taken: Box[] = nodes.map(nodeBox);
  /** Whether every label so far has a place clear of everything else. */
  let clear = true;
  /** How much of the other lines a label at a point would cover. */
  const covered = (pair: Pair, text: string, point: Point): number => {
    const room = clearOf(point, text);
    let points = 0;
    lines.forEach((line, i) => {
      if (i !== pair.i) points += struck(room, line);
    });
    return points;
  };
  /**
   * Takes the first of the label's possible points that is free of every label, state and
   * other line, and notes it where there is none.
   */
  const settleLabel = (pair: Pair, text: string, points: Point[]): Point => {
    const free = points.find((point) => {
      const room = labelBox(point.x, point.y, text);
      return (
        room.left >= 0 &&
        room.right <= width &&
        room.top >= 0 &&
        room.bottom <= height &&
        !taken.some((box) => overlap(box, room)) &&
        covered(pair, text, point) === 0
      );
    });
    const best = free ?? points[0] ?? { x: 0, y: 0 };
    clear = clear && free !== undefined;
    taken.push(labelBox(best.x, best.y, text));
    return best;
  };
  /** How long a transition's line is; an arc along the route counts as nothing, so the arcs come first. */
  const reach = (pair: Pair): number => {
    const a = placed.get(pair.from);
    const b = placed.get(pair.to);
    return !a || !b || a.y === b.y ? 0 : Math.hypot(a.x - b.x, a.y - b.y);
  };

  const edges = new Array<GraphEdge | undefined>(pairs.length);
  for (const pair of [...pairs].sort((a, b) => reach(a) - reach(b) || a.i - b.i)) {
    const a = placed.get(pair.from);
    const b = placed.get(pair.to);
    const line = lines.get(pair.i);
    if (!a || !b || !line) continue;
    const text = words(pair.method);
    const room = labelBox(0, 0, text);
    const half = (room.right - room.left) / 2;
    let points: Point[];
    if (a.y === b.y) {
      // Beyond the top of the bow, or along from there, or further out still.
      const passed = Math.abs(steps(pair)) - 1;
      const bow = a.x < b.x ? -(bowOver + bowStep * passed) : bowUnder + bowStep * passed;
      const mx = (a.x + b.x) / 2;
      const my = a.y + bow + (bow < 0 ? -2 : 4);
      const reachAcross = Math.max(0, Math.abs(a.x - b.x) / 2 - half);
      const aside = [0, ...[0.25, 0.5, 0.75, 1].flatMap((share) => [reachAcross * share, -reachAcross * share])];
      points = [0, bow < 0 ? -12 : 12].flatMap((out) => aside.map((dx) => ({ x: mx + dx, y: my + out })));
      // An arc back to the state before has one place more: on the row, between the two states, where it fits.
      if (bow > 0 && passed === 0 && Math.abs(a.x - b.x) - 2 * nodeHalfWidth >= 2 * half + 4) {
        points.push({ x: mx, y: a.y + 4 });
      }
    } else {
      // On the line, the middle first, then just beside it to the right and to the left.
      const shares = [0.5];
      for (let d = 0.04; d <= 0.36; d += 0.04) shares.push(0.5 - d, 0.5 + d);
      points = [0, half + 3, -(half + 3)].flatMap((dx) =>
        shares.map((t) => ({ x: a.x + (b.x - a.x) * t + dx, y: a.y + (b.y - a.y) * t + 4 })),
      );
    }
    const label = settleLabel(pair, text, points);
    edges[pair.i] = {
      method: pair.method,
      words: text,
      from: pair.from,
      to: pair.to,
      path: line.path,
      labelX: tenth(label.x),
      labelY: tenth(label.y),
    };
  }
  if (!clear) return undefined;
  return { direction: 'across', width, height, nodes, edges: edges.filter((edge) => edge !== undefined) };
}

/**
 * The route down: the states a transition leaves in one column, the default state on
 * top, and the terminal states in a column to the right. A transition to the next state
 * of the route is a line straight down, its label on it. Every other transition leaves
 * its state sideways on a row of its own, the first from the state's side and the others
 * from its underside, carries its label on that row, and only past the labels turns up
 * or down, to a terminal state or back to the column. So a row holds one line and its
 * label, the lines that go up and down run where no label is, and nothing is drawn on
 * anything else whatever the workflow.
 */
function layoutDown(workflow: Workflow, words: (method: string) => string): GraphLayout {
  const { route, terminals } = routeOf(workflow);
  const pairs = pairsOf(workflow);
  /** The transition from a state of the route to the next one, drawn straight down. */
  const nextOf = (i: number): Pair | undefined => pairs.find((p) => p.from === route[i] && p.to === route[i + 1]);
  /** The transitions that leave a state sideways: to the terminal states first, so they are the shortest. */
  const sidesOf = (i: number): Pair[] => {
    const sides = pairs.filter((p) => p.from === route[i] && p !== nextOf(i));
    return [...sides.filter((p) => terminals.includes(p.to)), ...sides.filter((p) => !terminals.includes(p.to))];
  };
  const sideways = route.flatMap((_, i) => sidesOf(i));
  const roomOf = (pair: Pair): number => {
    const room = labelBox(0, 0, words(pair.method));
    return room.right - room.left;
  };

  // Across: the column, the labels of the sideways lines, the lines up and down, the terminal states.
  const labelsLeft = columnX + nodeHalfWidth + besideColumn;
  const labelsRight = labelsLeft + Math.max(40, ...sideways.map(roomOf));
  const labelX = (labelsLeft + labelsRight) / 2;
  const targets = [...new Set(sideways.map((p) => p.to))];
  const railOf = (state: string): number => labelsRight + railStep * (targets.indexOf(state) + 1);
  const terminalX = labelsRight + railStep * targets.length + 12 + nodeHalfWidth;
  /** Where the label of a line straight down is centered: on the line, or as far right of it as keeps it in the drawing. */
  const downLabelX = (pair: Pair): number => Math.max(columnX, roomOf(pair) / 2 + 2);
  const width = Math.max(
    terminals.length > 0 ? terminalX + nodeHalfWidth + 4 : labelsRight + railStep * targets.length + 8,
    ...route.flatMap((_, i) => {
      const next = nextOf(i);
      return next ? [downLabelX(next) + roomOf(next) / 2 + 2] : [];
    }),
  );
  const entered = new Set(sideways.filter((p) => route.includes(p.to)).map((p) => p.to));

  // Down: each state of the route, the rows of the lines out of its underside, the label of its line down.
  const nodes: GraphNode[] = [];
  const stateY = new Map<string, number>();
  /** Where each sideways line starts, and the row it runs on. */
  const runs = new Map<number, { start: Point[]; y: number }>();
  const downLabelY = new Map<number, number>();
  let y = 6 + nodeHalfHeight;
  let floor = y;
  route.forEach((state, i) => {
    nodes.push({ state, x: columnX, y, terminal: false, isDefault: state === workflow.defaultState });
    stateY.set(state, y);
    floor = y + nodeHalfHeight;
    const sides = sidesOf(i);
    const under = sides.length - 1;
    const portStep = Math.min(10, 24 / Math.max(1, under - 1));
    sides.forEach((pair, k) => {
      if (k === 0) {
        const row = y + (entered.has(state) ? sideShift : 0);
        runs.set(pair.i, { start: [{ x: columnX + nodeHalfWidth, y: row }], y: row });
        return;
      }
      // The line nearest the state's middle turns lowest, so no line out of the underside crosses another.
      const row = y + nodeHalfHeight + firstRow + rowStep * (k - 1);
      const port = columnX + nodeHalfWidth - 10 - portStep * (k - 1);
      runs.set(pair.i, {
        start: [
          { x: port, y: y + nodeHalfHeight },
          { x: port, y: row },
        ],
        y: row,
      });
      floor = row + 7;
    });
    const next = nextOf(i);
    if (next) {
      downLabelY.set(next.i, floor + 14);
      y = floor + 36 + nodeHalfHeight;
    } else {
      y = floor + 14 + nodeHalfHeight;
    }
  });

  // The terminal states: each level with the first line that reaches it, and under the one before.
  let terminalFloor = 0;
  for (const pair of sideways) {
    const run = runs.get(pair.i);
    if (!run || !terminals.includes(pair.to) || stateY.has(pair.to)) continue;
    const at = Math.max(run.y, terminalFloor + 8 + nodeHalfHeight);
    stateY.set(pair.to, at);
    terminalFloor = at + nodeHalfHeight;
  }
  for (const state of terminals) {
    if (stateY.has(state)) continue;
    const at = terminalFloor + 8 + nodeHalfHeight;
    stateY.set(state, at);
    terminalFloor = at + nodeHalfHeight;
  }
  for (const state of terminals) {
    nodes.push({ state, x: terminalX, y: stateY.get(state) ?? 0, terminal: true, isDefault: false });
  }

  const edges: GraphEdge[] = [];
  for (const pair of pairs) {
    const text = words(pair.method);
    const from = stateY.get(pair.from);
    const to = stateY.get(pair.to);
    const run = runs.get(pair.i);
    if (from === undefined || to === undefined) continue;
    let line: Line;
    let label: Point;
    if (run) {
      const rail = railOf(pair.to);
      const end = terminals.includes(pair.to)
        ? { x: terminalX - nodeHalfWidth, y: to }
        : { x: columnX + nodeHalfWidth, y: to - sideShift };
      const turn =
        run.y === end.y && end.x > rail
          ? []
          : [
              { x: rail, y: run.y },
              { x: rail, y: end.y },
            ];
      line = straight([...run.start, ...turn, end]);
      label = { x: labelX, y: run.y + 3 };
    } else {
      line = straight([
        { x: columnX, y: from + nodeHalfHeight },
        { x: columnX, y: to - nodeHalfHeight },
      ]);
      label = { x: downLabelX(pair), y: downLabelY.get(pair.i) ?? from };
    }
    edges.push({
      method: pair.method,
      words: text,
      from: pair.from,
      to: pair.to,
      path: line.path,
      labelX: tenth(label.x),
      labelY: tenth(label.y),
    });
  }
  return {
    direction: 'down',
    width: Math.ceil(width),
    height: Math.ceil(Math.max(floor, terminalFloor) + 8),
    nodes,
    edges,
  };
}
