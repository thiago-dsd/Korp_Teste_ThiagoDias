import { DatePipe } from '@angular/common';
import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { Subject, catchError, of, startWith, switchMap, tap } from 'rxjs';

import { ApiError } from 'src/app/core/models/api-error.model';
import { Invoice, InvoiceStatus } from 'src/app/core/models/invoice.model';
import { InvoiceService } from 'src/app/core/services/invoice.service';
import { InvoiceStatusComponent } from './invoice-status.component';

/** Filters offered above the list. */
const STATUS_FILTERS: readonly (InvoiceStatus | '')[] = ['', 'OPEN', 'PRINTING', 'CLOSED'];

/** Lists the invoices and their current state. */
@Component({
  selector: 'app-invoices',
  imports: [RouterLink, DatePipe, AngularSvgIconModule, InvoiceStatusComponent],
  templateUrl: './invoices.component.html',
})
export class InvoicesComponent implements OnInit {
  private readonly invoices = inject(InvoiceService);
  private readonly destroyRef = inject(DestroyRef);

  readonly filters = STATUS_FILTERS;
  readonly activeFilter = signal<InvoiceStatus | ''>('');

  readonly items = signal<Invoice[]>([]);
  readonly loading = signal(false);
  readonly loadingMore = signal(false);
  readonly loadFailure = signal<ApiError | null>(null);

  /** Cursor of the next page, empty when the listing ended. */
  private readonly nextCursor = signal('');
  readonly hasMore = computed(() => this.nextCursor() !== '');

  /** True while at least one invoice is waiting for the stock service. */
  readonly hasPrinting = computed(() => this.items().some((invoice) => invoice.status === 'PRINTING'));

  private readonly reload = new Subject<void>();

  ngOnInit(): void {
    this.reload
      .pipe(
        startWith(undefined),
        switchMap(() => this.fetch()),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe();
  }

  private fetch() {
    this.loading.set(true);
    this.loadFailure.set(null);

    return this.invoices.list(this.activeFilter()).pipe(
      tap((page) => {
        this.items.set(page.items);
        this.nextCursor.set(page.nextCursor);
        this.loading.set(false);
      }),
      catchError((error: ApiError) => {
        this.loading.set(false);
        this.loadFailure.set(error);
        return of({ items: [], nextCursor: '' });
      }),
    );
  }

  /** Appends the next page to what is already on screen. */
  loadMore(): void {
    const cursor = this.nextCursor();
    if (!cursor || this.loadingMore()) {
      return;
    }

    this.loadingMore.set(true);

    this.invoices
      .list(this.activeFilter(), cursor)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (page) => {
          this.items.update((invoices) => [...invoices, ...page.items]);
          this.nextCursor.set(page.nextCursor);
          this.loadingMore.set(false);
        },
        error: (error: ApiError) => {
          this.loadingMore.set(false);
          this.loadFailure.set(error);
        },
      });
  }

  selectFilter(status: InvoiceStatus | ''): void {
    this.activeFilter.set(status);
    this.reload.next();
  }

  refresh(): void {
    this.reload.next();
  }

  labelFor(status: InvoiceStatus | ''): string {
    return status === '' ? 'All' : status.charAt(0) + status.slice(1).toLowerCase();
  }
}
