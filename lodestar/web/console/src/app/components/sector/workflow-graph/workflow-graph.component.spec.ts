import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Resources } from '@app/service/zz_gen_constants';
import { Workflows } from '@app/service/zz_gen_resources';
import { Resource } from '@impulseframework/resource';
import { arrowFaults, labelFaults } from './testing';
import { WorkflowGraphComponent } from './workflow-graph.component';

// A label is the only way to tell one edge of the graph from another, so no label is
// drawn on another label or on a state, none leaves the drawing, and no other edge's line
// crosses one: for the missions' workflow and the refits', in each method's name in words
// (as the Hangar deck draws its graph) and in words a page gives (as the Flight deck
// does), drawn across and drawn top to bottom.

/** The words the Flight deck gives the missions' edges. */
const deckWords: Readonly<Record<string, string>> = {
  ClaimMission: 'Claim',
  LaunchMission: 'Launch',
  HoldMission: 'Put on hold',
  ResumeMission: 'Resume',
  CompleteMission: 'Complete',
  FailMission: 'Mark as failed',
  StandDownMission: 'Stand down',
};

describe('WorkflowGraphComponent', () => {
  let fixture: ComponentFixture<WorkflowGraphComponent>;

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  /** Gives the graph a width of its own, in pixels, as the card it sits in would. */
  function giveWidth(width: number): void {
    vi.spyOn(fixture.nativeElement as HTMLElement, 'getBoundingClientRect').mockReturnValue({ width } as DOMRect);
  }

  /**
   * Draws a workflow's graph, a row of it in the given state, and gives the drawing. A
   * graph given no width of its own is drawn as one with all the width it wants.
   */
  function draw(root: Resource, current: string, words?: (method: string) => string, width?: number): Element {
    fixture = TestBed.createComponent(WorkflowGraphComponent);
    if (width !== undefined) {
      giveWidth(width);
    }
    fixture.componentRef.setInput('root', root);
    fixture.componentRef.setInput('current', current);
    if (words) {
      fixture.componentRef.setInput('words', words);
    }
    fixture.detectChanges();
    const graph = (fixture.nativeElement as HTMLElement).querySelector('svg.graph');
    if (!graph) {
      throw new Error('the graph is not drawn');
    }
    return graph;
  }

  /** The label of each edge drawn, in the order drawn. */
  function labels(graph: Element): string[] {
    return Array.from(graph.querySelectorAll('.edge text')).map((text) => (text.textContent ?? '').trim());
  }

  /** How wide the drawing is, in its own units. */
  function drawnWidth(graph: Element): number {
    return Number((graph.getAttribute('viewBox') ?? '').split(' ')[2]);
  }

  /** Where the states a transition leaves stand, left to right: one number where they stand in a column. */
  function routeColumns(graph: Element): number[] {
    const boxes = Array.from(graph.querySelectorAll('.node:not(.terminal) rect'));
    return [...new Set(boxes.map((box) => Number(box.getAttribute('x'))))];
  }

  /** How many edges a workflow has: one for each state each of its transitions leaves. */
  function edgeCount(root: Resource): number {
    const workflow = Workflows.find((w) => w.root === root);
    return (workflow?.transitions ?? []).reduce((count, transition) => count + transition.from.length, 0);
  }

  it('draws no label of the missions graph on another, in the words a page gives', () => {
    const graph = draw(Resources.Missions, 'open', (method) => deckWords[method] ?? method);

    expect(labels(graph)).toHaveLength(edgeCount(Resources.Missions));
    expect(labels(graph)).toContain('Stand down');
    expect(labels(graph)).toContain('Complete');
    expect(labelFaults(graph)).toEqual([]);
  });

  it('draws no label of the missions graph on another, in each method’s name in words', () => {
    const graph = draw(Resources.Missions, 'underway');

    expect(labels(graph)).toHaveLength(edgeCount(Resources.Missions));
    expect(labels(graph)).toContain('Stand down mission');
    expect(labelFaults(graph)).toEqual([]);
  });

  it('draws no label of the refits graph on another, as the Hangar deck draws it', () => {
    const graph = draw(Resources.Refits, 'docked');

    expect(labels(graph)).toHaveLength(edgeCount(Resources.Refits));
    expect(labels(graph)).toContain('Inspect ship');
    expect(labels(graph)).toContain('Fail flight test');
    expect(labelFaults(graph)).toEqual([]);
  });

  it('finds a line drawn through another edge’s label, and lets an edge’s line under its own', () => {
    const graph = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    graph.setAttribute('viewBox', '0 0 200 100');
    graph.innerHTML =
      '<g class="edge"><path d="M 10 50 L 190 50" /><text x="100" y="53">Complete</text></g>' +
      '<g class="edge"><path d="M 100 10 L 100 90" /><text x="150" y="20">Stand down</text></g>' +
      '<g class="edge"><path d="M 10 90 Q 100 0 190 90" /><text x="40" y="95">Resume</text></g>';

    expect(labelFaults(graph)).toEqual([
      'the line of "Stand down" crosses "Complete"',
      'the line of "Resume" crosses "Complete"',
    ]);
  });

  it('finds a line that ends inside a state or at none, and lets one end on a state’s edge', () => {
    const graph = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
    graph.setAttribute('viewBox', '0 0 300 100');
    graph.innerHTML =
      '<g class="edge"><path d="M 86 50 L 214 50" /><text x="150" y="46">Claim</text></g>' +
      '<g class="edge"><path d="M 250 50 Q 150 110 50 50" /><text x="150" y="90">Release</text></g>' +
      '<g class="edge"><path d="M 50 36 L 50 10" /><text x="80" y="20">Stand down</text></g>' +
      '<g class="node"><rect x="14" y="36" width="72" height="28" /><text x="50" y="54">open</text></g>' +
      '<g class="node"><rect x="214" y="36" width="72" height="28" /><text x="250" y="54">claimed</text></g>';

    expect(arrowFaults(graph)).toEqual([
      'the line of "Release" starts inside the state claimed',
      'the line of "Release" ends inside the state open',
      'the line of "Stand down" ends at no state',
    ]);
  });

  it('ends every line on the edge of a state, across or top to bottom, so its arrowhead shows', () => {
    for (const width of [1000, 400]) {
      for (const root of [Resources.Missions, Resources.Refits]) {
        const graph = draw(root, '', undefined, width);

        expect(graph.classList.contains('down')).toBe(width === 400);
        expect(arrowFaults(graph)).toEqual([]);
      }
    }
  });

  it('draws no line through another edge’s label, across or top to bottom', () => {
    for (const width of [1000, 400]) {
      for (const words of [undefined, (method: string): string => deckWords[method] ?? method]) {
        for (const root of [Resources.Missions, Resources.Refits]) {
          const graph = draw(root, '', words, width);

          expect(labels(graph)).toHaveLength(edgeCount(root));
          expect(labelFaults(graph)).toEqual([]);
        }
      }
    }
  });

  it('draws the graph top to bottom where its own width is little, and across where it is not', () => {
    const wide = draw(Resources.Missions, 'open', undefined, 560);

    expect(wide.classList.contains('down')).toBe(false);
    expect(drawnWidth(wide)).toBe(620);
    expect(routeColumns(wide)).toHaveLength(4);
    expect((wide as HTMLElement).style.maxWidth).toBe('');

    const narrow = draw(Resources.Missions, 'open', undefined, 559);

    expect(narrow.classList.contains('down')).toBe(true);
    expect(drawnWidth(narrow)).toBeLessThanOrEqual(330);
    expect(routeColumns(narrow)).toHaveLength(1);
    // Top to bottom it is never drawn larger than its own size: its letters stay the size they are set in.
    expect((narrow as HTMLElement).style.maxWidth).toBe(`${drawnWidth(narrow)}px`);
  });

  it('turns when the width it has changes, with the window as wide as it was', () => {
    const changed: (() => void)[] = [];
    vi.stubGlobal(
      'ResizeObserver',
      class {
        observe = vi.fn();
        disconnect = vi.fn();
        constructor(callback: () => void) {
          changed.push(callback);
        }
      },
    );
    const graph = draw(Resources.Refits, 'docked', undefined, 900);
    expect(routeColumns(graph)).toHaveLength(4);

    giveWidth(320);
    changed.forEach((callback) => callback());
    fixture.detectChanges();

    expect(routeColumns(graph)).toHaveLength(1);
    expect(labelFaults(graph)).toEqual([]);
  });

  it('keeps every state inside the drawing, however far the arcs bow and whichever way it goes', () => {
    for (const [root, card] of [
      [Resources.Missions, 1000],
      [Resources.Refits, 1000],
      [Resources.Missions, 400],
      [Resources.Refits, 400],
    ] as const) {
      const graph = draw(root, '', undefined, card);
      const [, , width = 0, height = 0] = (graph.getAttribute('viewBox') ?? '').split(' ').map(Number);

      for (const box of Array.from(graph.querySelectorAll('.node rect'))) {
        const left = Number(box.getAttribute('x'));
        const top = Number(box.getAttribute('y'));
        expect(left).toBeGreaterThanOrEqual(0);
        expect(top).toBeGreaterThanOrEqual(0);
        expect(left + 72).toBeLessThanOrEqual(width);
        expect(top + 28).toBeLessThanOrEqual(height);
      }
    }
  });

  it('lights the edges the row may take from its state and fires one at a click', () => {
    const graph = draw(Resources.Missions, 'open');
    fixture.componentRef.setInput('executable', ['ClaimMission', 'StandDownMission', 'LaunchMission']);
    const fired: string[] = [];
    fixture.componentInstance.fire.subscribe((method) => fired.push(method));
    fixture.detectChanges();

    const live = Array.from(graph.querySelectorAll('.edge.live'));
    expect(live.map((edge) => edge.getAttribute('aria-label'))).toEqual(['Claim mission', 'Stand down mission']);
    live[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    graph.querySelector('.edge:not(.live)')?.dispatchEvent(new MouseEvent('click', { bubbles: true }));

    expect(fired).toEqual(['ClaimMission']);
  });
});
