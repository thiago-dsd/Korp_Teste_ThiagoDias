import { Component, DestroyRef, OnInit, computed, inject, signal } from '@angular/core';
import { takeUntilDestroyed } from '@angular/core/rxjs-interop';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { AngularSvgIconModule } from 'angular-svg-icon';
import { toast } from 'ngx-sonner';
import { catchError, of, switchMap, tap } from 'rxjs';

import { ApiError } from 'src/app/core/models/api-error.model';
import { Invoice } from 'src/app/core/models/invoice.model';
import { InvoiceService } from 'src/app/core/services/invoice.service';
import { ApiErrorPipe } from 'src/app/core/i18n/api-error.pipe';
import { translateErrorCode } from 'src/app/core/i18n/error-translation';
import { LocaleDatePipe } from 'src/app/core/i18n/intl.pipe';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { TranslateService } from 'src/app/core/i18n/translate.service';
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
  imports: [RouterLink, AngularSvgIconModule, InvoiceStatusComponent, TranslatePipe, ApiErrorPipe, LocaleDatePipe],
  templateUrl: './invoice-detail.component.html',
})
export class InvoiceDetailComponent implements OnInit {
  private readonly invoices = inject(InvoiceService);
  private readonly route = inject(ActivatedRoute);
  private readonly destroyRef = inject(DestroyRef);
  private readonly i18n = inject(TranslateService);

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

  /**
   * The life of the invoice as a list of things that happened.
   *
   * Printing runs across two services and can fail halfway, so the facts were
   * scattered over the screen: a date here, a badge there, a red box somewhere
   * else. Read in order they answer the question the operator actually has,
   * which is what happened to this document and when.
   *
   * Built with {@link TranslateService} directly rather than with translation
   * keys the template resolves later: `computed()` tracks every signal read
   * during evaluation, including the ones `t()` makes, so this still
   * recomputes in the new language the moment the operator switches it.
   */
  readonly timeline = computed<TimelineEntry[]>(() => {
    const invoice = this.invoice();
    if (!invoice) {
      return [];
    }

    const entries: TimelineEntry[] = [
      {
        title: this.i18n.t('invoiceDetail.issued'),
        detail: invoice.issuedBy?.email
          ? this.i18n.t('invoiceDetail.byEmail', { email: invoice.issuedBy.email })
          : this.i18n.t('invoiceDetail.authorNotRecorded'),
        at: invoice.createdAt,
        tone: 'neutral',
      },
    ];

    if (invoice.status === 'PRINTING') {
      entries.push({
        title: this.i18n.t('invoiceDetail.printing'),
        detail: this.i18n.t('invoiceDetail.waitingStock'),
        at: null,
        tone: 'pending',
      });
    }

    // A failure and a closure are mutually exclusive: an invoice that came
    // back open carries the reason, and one that closed no longer does.
    if (invoice.failure) {
      entries.push({
        title: this.i18n.t('invoiceDetail.printingFailedTitle'),
        detail: translateErrorCode(this.i18n, invoice.failure.code),
        at: invoice.updatedAt,
        tone: 'failed',
      });
    }

    if (invoice.status === 'CLOSED') {
      entries.push({
        title: this.i18n.t('invoiceDetail.printed'),
        detail: invoice.printedBy?.email
          ? this.i18n.t('invoiceDetail.printingInProgress', { email: invoice.printedBy.email })
          : this.i18n.t('invoiceDetail.stockDebited'),
        at: invoice.printedAt,
        tone: 'done',
      });
    }

    return entries;
  });

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
          toast.error(translateErrorCode(this.i18n, error.code), { position: 'bottom-right' });

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
            toast.success(this.i18n.t('toasts.invoicePrinted', { number: invoice.number }), {
              position: 'bottom-right',
            });
          } else if (invoice.status === 'OPEN' && invoice.failure) {
            toast.error(translateErrorCode(this.i18n, invoice.failure.code), { position: 'bottom-right' });
          }
        },
        error: (error: ApiError) => {
          // Losing track of the invoice does not undo the printing itself.
          this.printFailure.set(error);
        },
      });
  }
}

/** One thing that happened to the invoice. */
export interface TimelineEntry {
  title: string;
  detail: string;
  /** Null while it is still happening. */
  at: string | null;
  tone: 'neutral' | 'pending' | 'done' | 'failed';
}
