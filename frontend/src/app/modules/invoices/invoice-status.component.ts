import { Component, computed, inject, input } from '@angular/core';

import { InvoiceStatus } from 'src/app/core/models/invoice.model';
import { TranslateService } from 'src/app/core/i18n/translate.service';

/** Shows the state of an invoice as a badge, with a spinner while printing. */
@Component({
  selector: 'app-invoice-status',
  template: `
    <span
      class="pill"
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
  private readonly i18n = inject(TranslateService);

  readonly status = input.required<InvoiceStatus>();

  readonly label = computed(() => {
    switch (this.status()) {
      case 'OPEN':
        return this.i18n.t('invoiceStatus.open');
      case 'PRINTING':
        return this.i18n.t('invoiceStatus.printing');
      case 'CLOSED':
        return this.i18n.t('invoiceStatus.closed');
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
