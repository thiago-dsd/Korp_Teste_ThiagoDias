import { DatePipe } from '@angular/common';
import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { toast } from 'ngx-sonner';
import { catchError, of, switchMap, tap } from 'rxjs';

import { ApiError } from 'src/app/core/models/api-error.model';
import { Invoice } from 'src/app/core/models/invoice.model';
import { InvoiceService } from 'src/app/core/services/invoice.service';
import { InvoiceStatusComponent } from './invoice-status.component';

/**
 * A single invoice, with the button that prints it.
 *
 * Printing is asynchronous, so the screen shows that the request is being
 * processed and follows the invoice until the stock service answers: the
 * invoice either closes or comes back open explaining what went wrong.
 */
@Component({
  selector: 'app-invoice-detail',
  imports: [RouterLink, DatePipe, AngularSvgIconModule, InvoiceStatusComponent],
  templateUrl: './invoice-detail.component.html',
})
export class InvoiceDetailComponent implements OnInit {
  private readonly invoices = inject(InvoiceService);
  private readonly route = inject(ActivatedRoute);
  private readonly destroyRef = inject(DestroyRef);

  readonly invoice = signal<Invoice | null>(null);
  readonly loading = signal(true);
  readonly loadFailure = signal<ApiError | null>(null);
  readonly printFailure = signal<ApiError | null>(null);

  /** True from the click until the stock service has answered. */
  readonly printing = computed(() => this.invoice()?.status === 'PRINTING');
  readonly canPrint = computed(() => this.invoice()?.status === 'OPEN');

  readonly totalQuantity = computed(() =>
    (this.invoice()?.items ?? []).reduce((total, item) => total + item.quantity, 0),
  );

  ngOnInit(): void {
    this.route.paramMap
      .pipe(
        switchMap((params) => this.fetch(params.get('id') ?? '')),
        takeUntilDestroyed(this.destroyRef),
      )
      .subscribe();
  }

  private fetch(id: string) {
    this.loading.set(true);
    this.loadFailure.set(null);

    return this.invoices.get(id).pipe(
      tap((invoice) => {
        this.invoice.set(invoice);
        this.loading.set(false);

        // An invoice can already be printing when the page opens, for example
        // after a reload, so following it is not tied to the click.
        if (invoice.status === 'PRINTING') {
          this.follow(invoice.id);
        }
      }),
      catchError((error: ApiError) => {
        this.loading.set(false);
        this.loadFailure.set(error);
        return of(null);
      }),
    );
  }

  /** Asks for the invoice to be printed. */
  print(): void {
    const invoice = this.invoice();
    if (!invoice || !this.canPrint()) {
      return;
    }

    this.printFailure.set(null);

    this.invoices
      .requestPrint(invoice.id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (accepted) => {
          this.invoice.set(accepted);
          this.follow(accepted.id);
        },
        error: (error: ApiError) => {
          this.printFailure.set(error);
          toast.error(error.message, { position: 'bottom-right' });

          // A conflict means the invoice is not open anymore; showing its real
          // state is more useful than the error alone.
          if (error.isConflict) {
            this.fetch(invoice.id).subscribe();
          }
        },
      });
  }

  /** Follows the invoice until the stock service has answered. */
  private follow(id: string): void {
    this.invoices
      .watchUntilPrinted(id)
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (invoice) => {
          this.invoice.set(invoice);

          if (invoice.status === 'CLOSED') {
            toast.success(`Invoice #${invoice.number} printed. Stock balances were updated.`, {
              position: 'bottom-right',
            });
          } else if (invoice.status === 'OPEN' && invoice.failure) {
            toast.error(invoice.failure.message, { position: 'bottom-right' });
          }
        },
        error: (error: ApiError) => {
          // Losing track of the invoice does not undo the printing itself.
          this.printFailure.set(error);
        },
      });
  }
}
