import { DatePipe } from '@angular/common';
import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed, toSignal } from '@angular/core/rxjs-interop';
import { ReactiveFormsModule, FormControl } from '@angular/forms';
import { RouterLink } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { Subject, catchError, of, startWith, switchMap, tap } from 'rxjs';

import { ApiError } from 'src/app/core/models/api-error.model';
import { Invoice, InvoiceStatus } from 'src/app/core/models/invoice.model';
import { InvoiceFilters, InvoiceService } from 'src/app/core/services/invoice.service';
import { InvoiceStatusComponent } from './invoice-status.component';

/** Filters offered above the list. */
const STATUS_FILTERS: readonly (InvoiceStatus | '')[] = ['', 'OPEN', 'PRINTING', 'CLOSED'];

/** Lists the invoices and their current state. */
@Component({
  selector: 'app-invoices',
  imports: [RouterLink, DatePipe, ReactiveFormsModule, AngularSvgIconModule, InvoiceStatusComponent],
  templateUrl: './invoices.component.html',
})
export class InvoicesComponent implements OnInit {
  private readonly invoices = inject(InvoiceService);
  private readonly destroyRef = inject(DestroyRef);

  readonly filters = STATUS_FILTERS;
  readonly activeFilter = signal<InvoiceStatus | ''>('');

  /** Free filters offered above the listing. */
  readonly numberControl = new FormControl('', { nonNullable: true });
  readonly fromControl = new FormControl('', { nonNullable: true });
  readonly toControl = new FormControl('', { nonNullable: true });
  readonly productCodeControl = new FormControl('', { nonNullable: true });
  readonly needsAttentionOnly = signal(false);

  // Reading the controls as signals keeps the "clear" button in step with what
  // is typed, without the template asking for it on every change detection.
  private readonly typedFilters = [
    toSignal(this.numberControl.valueChanges, { initialValue: '' }),
    toSignal(this.fromControl.valueChanges, { initialValue: '' }),
    toSignal(this.toControl.valueChanges, { initialValue: '' }),
    toSignal(this.productCodeControl.valueChanges, { initialValue: '' }),
  ];

  /** True when anything is filtering the listing. */
  readonly hasActiveFilters = computed(
    () =>
      this.needsAttentionOnly() ||
      this.activeFilter() !== '' ||
      this.typedFilters.some((value) => value() !== ''),
  );

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

  /** The filters currently applied to the listing. */
  private currentFilters(): InvoiceFilters {
    const number = Number(this.numberControl.value.trim());

    return {
      statuses: this.activeFilter() ? [this.activeFilter() as InvoiceStatus] : undefined,
      number: this.numberControl.value.trim() && Number.isFinite(number) ? number : undefined,
      createdFrom: this.fromControl.value || undefined,
      createdTo: this.toControl.value || undefined,
      productCode: this.productCodeControl.value || undefined,
      hasFailure: this.needsAttentionOnly() ? true : undefined,
    };
  }

  private fetch() {
    this.loading.set(true);
    this.loadFailure.set(null);

    return this.invoices.list(this.currentFilters()).pipe(
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
      .list(this.currentFilters(), cursor)
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

  /** Applies the free filters. */
  applyFilters(): void {
    this.reload.next();
  }

  /** Shows only the invoices whose last print attempt did not go through. */
  toggleNeedsAttention(): void {
    this.needsAttentionOnly.update((only) => !only);
    this.reload.next();
  }

  /** Clears every filter and reads the listing from the top. */
  clearFilters(): void {
    this.activeFilter.set('');
    this.needsAttentionOnly.set(false);
    this.numberControl.setValue('');
    this.fromControl.setValue('');
    this.toControl.setValue('');
    this.productCodeControl.setValue('');
    this.reload.next();
  }

  refresh(): void {
    this.reload.next();
  }

  labelFor(status: InvoiceStatus | ''): string {
    return status === '' ? 'All' : status.charAt(0) + status.slice(1).toLowerCase();
  }
}
