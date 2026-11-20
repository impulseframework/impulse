import { Workflow } from '@app/service/zz_gen_resources';

/** A state of the workflow, placed on the drawing. */
export interface GraphNode {
  state: string;
  x: number;
  y: number;
  terminal: boolean;
  isDefault: boolean;
}

/** One transition out of one state, drawn: its curve, and where its label sits. */
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

/** A workflow laid out: the drawing's size in its own units, its states and its edges. */
export interface GraphLayout {
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

/** The drawing's width; its height follows from the workflow. */
const width = 620;
/** Half a state's box, across and down. */
const nodeHalfWidth = 36;
const nodeHalfHeight = 14;
/** How far under the route's row the row of terminal states sits. */
const rowGap = 90;
/** How far a transition between neighbors on the route bows over the row, and under it going back. */
const bowOver = 28;
const bowUnder = 34;
/** How much further a transition bows for each state it passes over, so one arc is never drawn on another. */
const bowStep = 22;
/**
 * Where along its line a transition to a terminal state may carry its label, the middle
 * first. Each place is tried on the line, then beside it to the right and to the left:
 * the first place free of every label and state already drawn is taken.
 */
const places = [0.5, 0.42, 0.58, 0.34, 0.66, 0.26, 0.74, 0.2, 0.8];

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

/**
 * Lays a workflow out as a route: the states a transition leaves on one row, the default
 * state first, and the terminal states on a row beneath. A transition along the route is
 * an arc over the row, or under it going back, that bows further the more states it
 * passes, so two arcs over the same stretch are apart and so are their labels. A
 * transition to a terminal state is a straight line down, and its label sits on the line,
 * or just beside it, at the first place where it covers no other label and no state. The
 * shortest lines are labeled first: they have the fewest places to choose from. No two
 * labels share room: a label is the only way to tell which edge is which.
 */
export function layoutWorkflow(workflow: Workflow, words: (method: string) => string): GraphLayout {
  const sources = new Set(workflow.transitions.flatMap((t) => t.from));
  const route = [
    workflow.defaultState,
    ...workflow.states.filter((s) => s !== workflow.defaultState && sources.has(s)),
  ];
  const terminals = workflow.states.filter((s) => !sources.has(s));
  const column = new Map(route.map((state, i) => [state, i]));
  const pairs = workflow.transitions.flatMap((t) => t.from.map((from) => ({ method: t.method, from, to: t.to })));
  /** How many steps of the route a transition takes, where both its ends are on the route. */
  const steps = (from: string, to: string): number => (column.get(to) ?? 0) - (column.get(from) ?? 0);

  // The arcs over the row need room above it: the longest one sets how far down the row sits.
  const longest = Math.max(1, ...pairs.filter((p) => column.has(p.to)).map((p) => steps(p.from, p.to)));
  const top = 45 + bowStep * (longest - 1);
  const bottom = top + rowGap;
  const step = (width - 80) / Math.max(1, route.length - 1);
  const terminalStep = (width - 80) / (terminals.length + 1);
  const nodes: GraphNode[] = [
    ...route.map((state, i) => ({
      state,
      x: 40 + i * step,
      y: top,
      terminal: false,
      isDefault: state === workflow.defaultState,
    })),
    ...terminals.map((state, i) => ({
      state,
      x: 40 + (i + 1) * terminalStep,
      y: bottom,
      terminal: true,
      isDefault: false,
    })),
  ];
  const placed = new Map(nodes.map((node) => [node.state, node]));
  const taken: Box[] = nodes.map(nodeBox);
  const edges = new Array<GraphEdge | undefined>(pairs.length);

  /**
   * Takes the first of the label's possible points that is free: the one it is wanted at,
   * then the others in their order. Where none is free it takes the one it is wanted at.
   */
  const settleLabel = (text: string, wanted: Point, others: Point[] = []): Point => {
    const free = [wanted, ...others].find((point) => {
      const room = labelBox(point.x, point.y, text);
      return room.left >= 0 && room.right <= width && !taken.some((box) => overlap(box, room));
    });
    const point = free ?? wanted;
    taken.push(labelBox(point.x, point.y, text));
    return point;
  };
  /** How long a transition's line is; an arc along the route counts as nothing, so the arcs come first. */
  const reach = (pair: { from: string; to: string }): number => {
    const a = placed.get(pair.from);
    const b = placed.get(pair.to);
    return !a || !b || a.y === b.y ? 0 : Math.hypot(a.x - b.x, a.y - b.y);
  };

  // The arcs first: their labels have one place, at the top of the bow. Then the lines
  // down, the shortest first, whose labels move along the line to stay clear.
  const order = pairs.map((pair, i) => ({ pair, i })).sort((a, b) => reach(a.pair) - reach(b.pair));
  for (const { pair, i } of order) {
    const { method, from, to } = pair;
    const a = placed.get(from);
    const b = placed.get(to);
    if (!a || !b) continue;
    const text = words(method);
    if (a.y === b.y) {
      const passed = Math.abs(steps(from, to)) - 1;
      const bow = a.x < b.x ? -(bowOver + bowStep * passed) : bowUnder + bowStep * passed;
      const mx = (a.x + b.x) / 2;
      const my = a.y + bow;
      const label = settleLabel(text, { x: mx, y: my + (bow < 0 ? -2 : 4) });
      edges[i] = {
        method,
        words: text,
        from,
        to,
        path: `M ${a.x} ${a.y} Q ${mx} ${my} ${b.x} ${b.y}`,
        labelX: label.x,
        labelY: label.y,
      };
      continue;
    }
    const room = labelBox(0, 0, text);
    const beside = (room.right - room.left) / 2 + 3;
    /** The point a share of the way down the line, a little to its side. */
    const at = (aside: number, t: number): Point => ({
      x: a.x + (b.x - a.x) * t + aside,
      y: a.y + (b.y - a.y) * t + 4,
    });
    const label = settleLabel(
      text,
      at(0, 0.5),
      [0, beside, -beside].flatMap((aside) => places.map((t) => at(aside, t))),
    );
    edges[i] = {
      method,
      words: text,
      from,
      to,
      path: `M ${a.x} ${a.y} L ${b.x} ${b.y}`,
      labelX: label.x,
      labelY: label.y,
    };
  }
  return { width, height: bottom + 35, nodes, edges: edges.filter((edge) => edge !== undefined) };
}
