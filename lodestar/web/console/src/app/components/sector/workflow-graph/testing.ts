/** The room something takes on a drawn graph, in the drawing's own units. */
interface Room {
  name: string;
  left: number;
  top: number;
  right: number;
  bottom: number;
}

/** Whether two rooms share any of the drawing. */
function shared(a: Room, b: Room): boolean {
  return a.left < b.right && b.left < a.right && a.top < b.bottom && b.top < a.bottom;
}

/** A number an element carries as an attribute. */
function attribute(element: Element, name: string): number {
  return Number(element.getAttribute(name));
}

/** A point of the drawing, in the drawing's own units. */
interface Spot {
  x: number;
  y: number;
}

/**
 * Points along a line as the graph draws one (straight runs from point to point, or one
 * curve bowed toward a point), no more than a unit apart, so no label's room is stepped
 * over.
 */
function pointsAlong(path: string): Spot[] {
  const parts = path.trim().split(/\s+/);
  const spots: Spot[] = [];
  let at: Spot = { x: 0, y: 0 };
  let i = 0;
  while (i < parts.length) {
    const command = parts[i];
    const count = command === 'Q' ? 4 : 2;
    const [a = 0, b = 0, c = 0, d = 0] = parts.slice(i + 1, i + 1 + count).map(Number);
    i += 1 + count;
    if (command === 'M') {
      at = { x: a, y: b };
      spots.push(at);
    } else if (command === 'L') {
      const from = at;
      const steps = Math.max(1, Math.ceil(Math.hypot(a - from.x, b - from.y)));
      for (let step = 1; step <= steps; step++) {
        spots.push({ x: from.x + ((a - from.x) * step) / steps, y: from.y + ((b - from.y) * step) / steps });
      }
      at = { x: a, y: b };
    } else if (command === 'Q') {
      const from = at;
      const steps = Math.max(1, Math.ceil(Math.hypot(a - from.x, b - from.y) + Math.hypot(c - a, d - b)));
      for (let step = 1; step <= steps; step++) {
        const t = step / steps;
        spots.push({
          x: (1 - t) * (1 - t) * from.x + 2 * (1 - t) * t * a + t * t * c,
          y: (1 - t) * (1 - t) * from.y + 2 * (1 - t) * t * b + t * t * d,
        });
      }
      at = { x: c, y: d };
    } else {
      throw new Error(`a line of the graph is drawn in a way the check does not read: ${path}`);
    }
  }
  return spots;
}

/** The room each state's box takes on a drawn graph. */
function stateRooms(graph: Element): Room[] {
  return Array.from(graph.querySelectorAll('.node')).map((node): Room => {
    const box = node.querySelector('rect');
    const left = box ? attribute(box, 'x') : 0;
    const top = box ? attribute(box, 'y') : 0;
    return {
      name: `the state ${(node.textContent ?? '').trim()}`,
      left,
      top,
      right: left + (box ? attribute(box, 'width') : 0),
      bottom: top + (box ? attribute(box, 'height') : 0),
    };
  });
}

/** Whether a point is inside a room grown by a margin on every side (shrunk by a negative one). */
function within(room: Room, spot: Spot, margin: number): boolean {
  return (
    spot.x >= room.left - margin &&
    spot.x <= room.right + margin &&
    spot.y >= room.top - margin &&
    spot.y <= room.bottom + margin
  );
}

/**
 * What a drawn workflow graph has wrong with its arrows: each line that does not start
 * and end on the edge of a state's box. The boxes are drawn over the lines, so a line
 * that runs on inside a box ends under it and its arrowhead is not seen; a line that
 * ends short of every box points at nothing. A graph with nothing wrong gives an empty
 * list. A line's ends are read to half a unit, since a path carries tenths.
 */
export function arrowFaults(graph: Element): string[] {
  const states = stateRooms(graph);
  const faults: string[] = [];
  graph.querySelectorAll('.edge').forEach((edge) => {
    const words = (edge.querySelector('text')?.textContent ?? '').trim();
    const line = pointsAlong(edge.querySelector('path')?.getAttribute('d') ?? '');
    const ends: [string, Spot | undefined][] = [
      ['starts', line[0]],
      ['ends', line[line.length - 1]],
    ];
    for (const [which, spot] of ends) {
      if (!spot) continue;
      const onEdge = states.some((state) => within(state, spot, 0.5) && !within(state, spot, -0.5));
      if (onEdge) continue;
      const inside = states.find((state) => within(state, spot, 0));
      faults.push(`the line of "${words}" ${which} ${inside ? `inside ${inside.name}` : 'at no state'}`);
    }
  });
  return faults;
}

/**
 * What a drawn workflow graph has wrong with its labels: each label that shares room with
 * another label or with a state's box, each that leaves the drawing, and each that
 * another edge's line crosses. A graph with nothing wrong gives an empty list: every edge
 * of it can be told by its words. An edge's own line may pass under its own label.
 *
 * A test has no layout engine to measure text with, so a label's room is reckoned from
 * its words: at the 9 units the graph sets a label in, a letter is 5 wide on average and
 * the line 10 high, around the point the text is centered on.
 */
export function labelFaults(graph: Element): string[] {
  const [, , width = 0, height = 0] = (graph.getAttribute('viewBox') ?? '').split(' ').map(Number);
  const labels = Array.from(graph.querySelectorAll('.edge text')).map((text): Room => {
    const words = (text.textContent ?? '').trim();
    const x = attribute(text, 'x');
    const y = attribute(text, 'y');
    return {
      name: `"${words}"`,
      left: x - words.length * 2.5,
      top: y - 8,
      right: x + words.length * 2.5,
      bottom: y + 2,
    };
  });
  const states = stateRooms(graph);
  const lines = Array.from(graph.querySelectorAll('.edge')).map((edge) =>
    pointsAlong(edge.querySelector('path')?.getAttribute('d') ?? ''),
  );
  const faults: string[] = [];
  labels.forEach((label, i) => {
    lines.forEach((line, j) => {
      const inside = line.filter(
        (spot) => spot.x > label.left && spot.x < label.right && spot.y > label.top && spot.y < label.bottom,
      );
      if (j !== i && inside.length > 0) {
        faults.push(`the line of ${labels[j]?.name ?? 'an edge'} crosses ${label.name}`);
      }
    });
    if (label.left < 0 || label.top < 0 || label.right > width || label.bottom > height) {
      faults.push(`${label.name} leaves the drawing`);
    }
    for (const other of labels.slice(i + 1)) {
      if (shared(label, other)) {
        faults.push(`${label.name} is drawn on ${other.name}`);
      }
    }
    for (const state of states) {
      if (shared(label, state)) {
        faults.push(`${label.name} is drawn on ${state.name}`);
      }
    }
  });
  return faults;
}
