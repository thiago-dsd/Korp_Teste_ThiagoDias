import { Component, computed, input } from '@angular/core';

import { InvoiceStatus } from 'src/app/core/models/invoice.model';

/** Shows the state of an invoice as a badge, with a spinner while printing. */
@Component({
  selector: 'app-invoice-status',
  template: `
    <span
      class="inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-semibold"
      [class]="styles()"
      role="status">
      @if (status() === 'PRINTING') {
        <span
          class="border-current h-3 w-3 animate-spin rounded-full border-2 border-t-transparent"
          aria-hidden="true"></span>
      }
      {{ label() }}
    </span>
  `,
})
export class InvoiceStatusComponent {
  readonly status = input.required<InvoiceStatus>();

  readonly label = computed(() => {
    switch (this.status()) {
      case 'OPEN':
        return 'Open';
      case 'PRINTING':
        return 'Printing';
      case 'CLOSED':
        return 'Closed';
    }
  });

  readonly styles = computed(() => {
    switch (this.status()) {
      case 'OPEN':
        return 'bg-primary/10 text-primary';
      case 'PRINTING':
        return 'bg-amber-500/10 text-amber-600';
      case 'CLOSED':
        return 'bg-emerald-500/10 text-emerald-600';
    }
  });
}
