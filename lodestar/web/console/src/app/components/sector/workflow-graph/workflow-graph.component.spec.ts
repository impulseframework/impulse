import { ComponentFixture, TestBed } from '@angular/core/testing';
import { Resources } from '@app/service/zz_gen_constants';
import { Workflows } from '@app/service/zz_gen_resources';
import { Resource } from '@impulseframework/resource';
import { labelFaults } from './testing';
import { WorkflowGraphComponent } from './workflow-graph.component';

// A label is the only way to tell one edge of the graph from another, so no label is
// drawn on another label or on a state, and none leaves the drawing: for the missions'
// workflow and the refits', in each method's name in words (as the Hangar deck draws its
// graph) and in words a page gives (as the Flight deck does).

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

  /** Draws a workflow's graph, a row of it in the given state, and gives the drawing. */
  function draw(root: Resource, current: string, words?: (method: string) => string): Element {
    fixture = TestBed.createComponent(WorkflowGraphComponent);
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

  it('keeps every state inside the drawing, however far the arcs bow', () => {
    for (const root of [Resources.Missions, Resources.Refits]) {
      const graph = draw(root, '');
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
