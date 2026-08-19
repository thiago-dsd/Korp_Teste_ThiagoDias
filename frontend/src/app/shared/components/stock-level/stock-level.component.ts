import { Component, computed, inject, input } from '@angular/core';

import { LocaleNumberPipe } from 'src/app/core/i18n/intl.pipe';
import { TranslateService } from 'src/app/core/i18n/translate.service';

/**
 * The balance below which a product is worth acting on before it runs out.
 *
 * A stock screen that only marks what already reached zero warns too late: by
 * then an invoice has already been refused. The threshold is deliberately a
 * plain number rather than a per-product setting, which the catalogue has no
 * field for and nobody asked for.
 */
export const LOW_STOCK_THRESHOLD = 5;

/** How much of a product is left, read as a state rather than as a number. */
export type StockLevel = 'out' | 'low' | 'ok';

/** Classifies a balance so every screen draws the same conclusion from it. */
export function stockLevelOf(balance: number): StockLevel {
  if (balance <= 0) {
    return 'out';
  }
  return balance <= LOW_STOCK_THRESHOLD ? 'low' : 'ok';
}

/**
 * Shows a balance as a badge coloured by how urgent it is.
 *
 * Colour alone would leave the state invisible to anyone who cannot tell these
 * hues apart, so the badge also carries a label a screen reader announces.
 */
@Component({
  selector: 'app-stock-level',
  imports: [LocaleNumberPipe],
  template: `
    <span class="pill" [class]="styles()">
      @if (level() !== 'ok') {
        <span class="h-1.5 w-1.5 rounded-full bg-current" aria-hidden="true"></span>
      }
      {{ balance() | localeNumber }}
      <span class="sr-only">{{ description() }}</span>
    </span>
  `,
})
export class StockLevelComponent {
  private readonly i18n = inject(TranslateService);

  readonly balance = input.required<number>();

  readonly level = computed(() => stockLevelOf(this.balance()));

  readonly styles = computed(() => {
    switch (this.level()) {
      case 'out':
        return 'bg-destructive/10 text-destructive';
      case 'low':
        return 'bg-amber-500/10 text-amber-600';
      case 'ok':
        return 'bg-primary/10 text-primary';
    }
  });

  readonly description = computed(() => {
    switch (this.level()) {
      case 'out':
        return this.i18n.t('stockLevel.outOfStock');
      case 'low':
        return this.i18n.t('stockLevel.runningLow');
      case 'ok':
        return this.i18n.t('stockLevel.inStock');
    }
  });
}
