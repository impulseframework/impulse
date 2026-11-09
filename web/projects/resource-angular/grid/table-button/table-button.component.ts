import { Component, computed, input, signal } from '@angular/core';
import { MatButtonModule } from '@angular/material/button';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule, TooltipPosition } from '@angular/material/tooltip';
import { RouterModule } from '@angular/router';
import { ActionButtonConfig } from '@impulseframework/resource-angular/types';

@Component({
  selector: 'impulse-table-button',
  standalone: true,
  imports: [MatButtonModule, MatIconModule, MatTooltipModule, RouterModule],
  templateUrl: './table-button.component.html',
  styleUrl: './table-button.component.scss',
})
export class TableButtonComponent<T> {
  config = input.required<ActionButtonConfig>();

  rowData = input.required<T>();
  tooltipPosition = input<TooltipPosition>('above' as TooltipPosition);
  color = input<string>('');
  disabled = signal<boolean>(false);
  viewRoute = input<string>('');
  id = input<string>('');

  link = computed(() => {
    const viewRoute = this.viewRoute();
    const id = this.id();

    if (!viewRoute || !id) {
      return '';
    }

    if (!viewRoute.startsWith('/')) {
      return `${viewRoute}/${id}`;
    }

    return `${viewRoute}/${id}`;
  });

  callAction(): void {
    const rowData = this.rowData();
    const action = this.config().action;
    if (rowData && action !== undefined) {
      // The whole row travels, so an action can lift a compound key the row carries;
      // id stays first for the callers that read only it.
      const id = 'id' as keyof T;
      const idVal = rowData[id] as string;
      action({ ...rowData, id: idVal });
    }
  }
}
