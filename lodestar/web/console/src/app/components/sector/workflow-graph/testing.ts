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

/**
 * What a drawn workflow graph has wrong with its labels: each label that shares room with
 * another label or with a state's box, and each that leaves the drawing. A graph with
 * nothing wrong gives an empty list: every edge of it can be told by its words.
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
  const states = Array.from(graph.querySelectorAll('.node')).map((node): Room => {
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
  const faults: string[] = [];
  labels.forEach((label, i) => {
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
