import { Component, computed, input, output } from '@angular/core';
import { Workflows } from '@app/service/zz_gen_resources';
import { Method, Resource } from '@impulseframework/resource';
import { nameInWords } from '@impulseframework/resource-angular/types';
import { GraphEdge, GraphNode, layoutWorkflow } from './workflow-layout';

/** An edge as the graph draws it: where the layout put it, and whether it is lit for this person now. */
interface Edge extends GraphEdge {
  live: boolean;
}

/**
 * The portal draws the same mission graph the flight deck does, from the portal
 * target's own generated Workflows constant. For a client no edge is lit except Stand
 * Down, live only while the mission is open, claimed, or on hold and only on their
 * company's rows — the row's Execute envelope says so; the page never decides.
 *
 * An edge is drawn in words, never as its method's name: the words its page gives it
 * (`words`), and where the page gives none the name in words. No two labels are drawn on
 * one another: the layout (layoutWorkflow) moves a label along its line until it is clear
 * of the others, since a label is the only way to tell which edge is which.
 *
 * Demonstrates: workflow.ts-constant, typescript.second-target.
 */
@Component({
  selector: 'app-tracker-graph',
  templateUrl: './tracker-graph.component.html',
  styleUrl: './tracker-graph.component.scss',
})
export class TrackerGraphComponent {
  root = input.required<Resource>();
  current = input.required<string>();
  executable = input<readonly string[]>([]);
  /** What the page calls a method; a page that gives no words gets each method's name in words. */
  words = input<(method: string) => string>(nameInWords);
  fire = output<Method>();

  private workflow = computed(() => Workflows.find((w) => w.root === this.root()));

  // Where everything sits: the states as a route, the edges and their labels, none on another.
  private layout = computed(() => {
    const workflow = this.workflow();
    return workflow ? layoutWorkflow(workflow, this.words()) : undefined;
  });

  readonly width = computed(() => this.layout()?.width ?? 620);
  readonly height = computed(() => this.layout()?.height ?? 170);

  nodes = computed<GraphNode[]>(() => this.layout()?.nodes ?? []);

  edges = computed<Edge[]>(() => {
    const executable = new Set(this.executable());
    const current = this.current();
    return (this.layout()?.edges ?? []).map((edge) => ({
      ...edge,
      live: executable.has(edge.method) && edge.from === current,
    }));
  });

  label(state: string): string {
    return state.replace('_', ' ');
  }

  emit(edge: Edge): void {
    if (edge.live) {
      this.fire.emit(edge.method as Method);
    }
  }
}
