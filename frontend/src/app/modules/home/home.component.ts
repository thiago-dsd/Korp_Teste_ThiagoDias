import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { forkJoin, of } from 'rxjs';
import { catchError } from 'rxjs/operators';

import { ApiErrorPipe } from 'src/app/core/i18n/api-error.pipe';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { Invoice } from 'src/app/core/models/invoice.model';
import { Product } from 'src/app/core/models/product.model';
import { AuthService } from 'src/app/core/services/auth.service';
import { InvoiceService } from 'src/app/core/services/invoice.service';
import { ProductService } from 'src/app/core/services/product.service';
import { InvoiceStatusComponent } from 'src/app/modules/invoices/invoice-status.component';
import { LOW_STOCK_THRESHOLD, StockLevelComponent } from 'src/app/shared/components/stock-level/stock-level.component';

/** A number the operator may need to act on, and where acting on it happens. */
export interface Tile {
  /** Translation key under `home.tiles`. */
  labelKey: string;
  /** Shown as "20+" when the listing had another page. */
  value: string;
  hintKey: string;
  /** Params for `hintKey`, when it takes any. */
  hintParams?: Record<string, string | number>;
  route: string;
  queryParams: Record<string, string>;
  tone: 'neutral' | 'warning' | 'danger';
}

/**
 * What needs attention today.
 *
 * The landing page used to explain what the application is for, which is worth
 * reading exactly once. An operator opening this system already knows; what
 * they do not know is which invoices failed overnight and what is about to run
 * out, and both were three clicks and two filters away.
 */
@Component({
  selector: 'app-home',
  imports: [RouterLink, AngularSvgIconModule, InvoiceStatusComponent, StockLevelComponent, TranslatePipe, ApiErrorPipe],
  templateUrl: './home.component.html',
})
export class HomeComponent implements OnInit {
  private readonly products = inject(ProductService);
  private readonly invoices = inject(InvoiceService);
  private readonly auth = inject(AuthService);
  private readonly destroyRef = inject(DestroyRef);

  readonly lowStockThreshold = LOW_STOCK_THRESHOLD;
  readonly canManageCatalogue = this.auth.isAdmin;
  readonly userName = computed(() => this.auth.currentUser()?.name ?? '');

  readonly loading = signal(true);
  /** True when at least one panel could not be read; the rest still shows. */
  readonly partial = signal(false);

  readonly failedInvoices = signal<Invoice[]>([]);
  readonly openInvoices = signal<Invoice[]>([]);
  readonly lowStock = signal<Product[]>([]);
  readonly outOfStock = signal<Product[]>([]);

  private readonly more = signal<Record<string, boolean>>({});

  readonly tiles = computed((): Tile[] => [
    {
      labelKey: 'home.tiles.failedLabel',
      value: this.count('failed', this.failedInvoices().length),
      hintKey: 'home.tiles.failedHint',
      route: '/invoices',
      queryParams: { attention: 'true' },
      tone: 'danger',
    },
    {
      labelKey: 'home.tiles.openLabel',
      value: this.count('open', this.openInvoices().length),
      hintKey: 'home.tiles.openHint',
      route: '/invoices',
      queryParams: { status: 'OPEN' },
      tone: 'neutral',
    },
    {
      labelKey: 'home.tiles.outLabel',
      value: this.count('out', this.outOfStock().length),
      hintKey: 'home.tiles.outHint',
      route: '/products',
      queryParams: { stock: 'out' },
      tone: 'danger',
    },
    {
      labelKey: 'home.tiles.lowLabel',
      value: this.count('low', this.lowStock().length),
      hintKey: 'home.tiles.lowHint',
      hintParams: { threshold: LOW_STOCK_THRESHOLD },
      route: '/products',
      queryParams: { stock: 'low' },
      tone: 'warning',
    },
  ]);

  /** True when nothing at all needs attention, which is worth saying plainly. */
  readonly allClear = computed(
    () =>
      !this.loading() &&
      this.failedInvoices().length === 0 &&
      this.outOfStock().length === 0 &&
      this.lowStock().length === 0,
  );

  ngOnInit(): void {
    this.load();
  }

  load(): void {
    this.loading.set(true);
    this.partial.set(false);

    // The four panels are independent: one service being slow or down must not
    // leave the page blank, so each failure degrades to an empty panel and the
    // page says so once.
    forkJoin({
      failed: this.invoices.list({ hasFailure: true }).pipe(catchError(() => this.degrade())),
      open: this.invoices.list({ statuses: ['OPEN'] }).pipe(catchError(() => this.degrade())),
      low: this.products
        .list({ maxBalance: LOW_STOCK_THRESHOLD, sort: 'balance' })
        .pipe(catchError(() => this.degrade())),
      out: this.products.list({ maxBalance: 0 }).pipe(catchError(() => this.degrade())),
    })
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe((pages) => {
        this.failedInvoices.set(pages.failed.items);
        this.openInvoices.set(pages.open.items);
        // What is out of stock is already listed on its own, so the "running
        // out" panel shows what can still be sold and is worth reordering.
        this.lowStock.set(pages.low.items.filter((product) => product.balance > 0));
        this.outOfStock.set(pages.out.items);

        this.more.set({
          failed: pages.failed.nextCursor !== '',
          open: pages.open.nextCursor !== '',
          low: pages.low.nextCursor !== '',
          out: pages.out.nextCursor !== '',
        });
        this.loading.set(false);
      });
  }

  private degrade() {
    this.partial.set(true);
    return of({ items: [], nextCursor: '' });
  }

  /**
   * The size of the page, marked with a "+" when there was another one.
   *
   * The listings are paged by cursor and carry no total, deliberately: a count
   * over every invoice on every page load is a scan nobody asked for. Saying
   * "20+" is honest and still answers whether today needs attention; claiming
   * 20 when there are 400 would not.
   */
  private count(panel: string, size: number): string {
    return this.more()[panel] ? `${size}+` : String(size);
  }

  /** The few worth showing under a tile; the rest is one click away. */
  topOf<T>(items: T[]): T[] {
    return items.slice(0, 5);
  }
}
