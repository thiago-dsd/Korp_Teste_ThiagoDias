import { Component, DestroyRef, computed, inject, output, signal } from '@angular/core';
import { FormControl, ReactiveFormsModule } from '@angular/forms';
import { takeUntilDestroyed, toSignal } from '@angular/core/rxjs-interop';

import { ApiError } from 'src/app/core/models/api-error.model';
import { BULK_MAX_ITEMS, BulkResponse } from 'src/app/core/models/bulk.model';
import { NewProduct } from 'src/app/core/models/product.model';
import { ProductService } from 'src/app/core/services/product.service';
import { ApiErrorPipe } from 'src/app/core/i18n/api-error.pipe';
import { TranslatePipe } from 'src/app/core/i18n/translate.pipe';
import { TranslateService } from 'src/app/core/i18n/translate.service';
import { BulkResultComponent } from 'src/app/shared/components/bulk-result/bulk-result.component';

/** A reason a line could not be read, keyed under `productImport.parseErrors`. */
type ParseErrorKey = 'needsCodeAndDescription' | 'invalidBalance';

/** One line of the pasted text, either understood or rejected with a reason. */
interface ParsedLine {
  line: number;
  product?: NewProduct;
  /**
   * A key, not a message: `parseCatalogue` is a plain function with no access
   * to the current language, so it names what went wrong and leaves wording
   * it to whoever renders the line.
   */
  error?: ParseErrorKey;
}

/**
 * Brings a catalogue in from a spreadsheet.
 *
 * Registering a few hundred products one form at a time is the reason a real
 * catalogue never makes it into a system. The service already accepts them a
 * hundred at a time and reports each row separately; this is the screen that
 * was missing.
 */
@Component({
  selector: 'app-product-import',
  imports: [ReactiveFormsModule, BulkResultComponent, TranslatePipe, ApiErrorPipe],
  templateUrl: './product-import.component.html',
})
export class ProductImportComponent {
  private readonly products = inject(ProductService);
  private readonly destroyRef = inject(DestroyRef);
  readonly i18n = inject(TranslateService);

  /** Emitted when at least one product was registered, so the list reloads. */
  readonly imported = output<void>();
  readonly closed = output<void>();

  readonly maxItems = BULK_MAX_ITEMS;
  readonly textControl = new FormControl('', { nonNullable: true });
  private readonly text = toSignal(this.textControl.valueChanges, { initialValue: '' });

  readonly importing = signal(false);
  readonly result = signal<BulkResponse | null>(null);
  readonly failure = signal<ApiError | null>(null);

  /** What the pasted text was understood to mean, read back before sending. */
  readonly parsed = computed(() => parseCatalogue(this.text()));
  readonly valid = computed(() => this.parsed().filter((line) => line.product !== undefined));
  readonly rejected = computed(() => this.parsed().filter((line) => line.error !== undefined));
  readonly tooMany = computed(() => this.valid().length > BULK_MAX_ITEMS);

  readonly canImport = computed(() => this.valid().length > 0 && !this.tooMany() && !this.importing());

  /**
   * The key that makes a retry safe: kept while the pasted text is unchanged,
   * so resending after a lost answer cannot register the catalogue twice, and
   * replaced as soon as a line is corrected.
   */
  private readonly idempotency = signal<{ key: string; fingerprint: string } | null>(null);

  submit(): void {
    const products = this.valid().map((line) => line.product as NewProduct);
    if (products.length === 0 || this.tooMany() || this.importing()) {
      return;
    }

    this.importing.set(true);
    this.result.set(null);
    this.failure.set(null);

    this.products
      .createMany(products, this.idempotencyKeyFor(products))
      .pipe(takeUntilDestroyed(this.destroyRef))
      .subscribe({
        next: (response) => {
          this.importing.set(false);
          this.result.set(this.withCodes(response, products));

          if (response.summary.succeeded > 0) {
            this.imported.emit();
          }
          if (response.summary.failed === 0) {
            // Everything landed: the key must not be reused for the next paste.
            this.idempotency.set(null);
            this.textControl.setValue('');
          }
        },
        error: (error: ApiError) => {
          this.importing.set(false);
          this.failure.set(error);
        },
      });
  }

  close(): void {
    this.closed.emit();
  }

  /** The full "Line N: reason" sentence for one rejected line. */
  lineErrorMessage(rejectedLine: ParsedLine): string {
    const reason = rejectedLine.error ? this.i18n.t(`productImport.parseErrors.${rejectedLine.error}`) : '';
    return this.i18n.t('productImport.lineError', { line: rejectedLine.line, error: reason });
  }

  dismissResult(): void {
    this.result.set(null);
  }

  /**
   * Names each result by the code that was sent.
   *
   * A row rejected before it reached the database has no code of its own in
   * the answer, and "item 34" is not something an operator can look up.
   */
  private withCodes(response: BulkResponse, products: NewProduct[]): BulkResponse {
    return {
      ...response,
      results: response.results.map((result) => ({
        ...result,
        reference: result.reference || products[result.index]?.code || '',
      })),
    };
  }

  private idempotencyKeyFor(products: NewProduct[]): string {
    const fingerprint = JSON.stringify(products);
    const current = this.idempotency();
    if (current?.fingerprint === fingerprint) {
      return current.key;
    }

    const key = crypto.randomUUID();
    this.idempotency.set({ key, fingerprint });
    return key;
  }
}

/**
 * Reads pasted spreadsheet rows as products.
 *
 * Comma and semicolon and tab all appear depending on where the file came
 * from, so all three are accepted rather than asking the operator to convert
 * the file first. Parsing here rather than on the service keeps a malformed
 * paste from spending a request, and lets the screen show what it understood
 * before anything is written.
 */
export function parseCatalogue(text: string): ParsedLine[] {
  const lines: ParsedLine[] = [];

  text.split(/\r?\n/).forEach((raw, index) => {
    const line = index + 1;
    const trimmed = raw.trim();
    if (trimmed === '') {
      return;
    }

    const cells = splitCells(trimmed);
    // A header row is common when the text comes out of a spreadsheet, and
    // refusing it would read as if the file were wrong.
    if (index === 0 && /^(code|c[oó]digo)$/i.test(cells[0] ?? '')) {
      return;
    }

    const [code, description, balance] = cells;
    if (!code || !description) {
      lines.push({ line, error: 'needsCodeAndDescription' });
      return;
    }

    const quantity = balance === undefined || balance === '' ? 0 : Number(balance);
    if (!Number.isInteger(quantity) || quantity < 0) {
      lines.push({ line, error: 'invalidBalance' });
      return;
    }

    lines.push({ line, product: { code, description, balance: quantity } });
  });

  return lines;
}

/** Splits a row on whichever separator it uses, honouring quoted cells. */
function splitCells(row: string): string[] {
  const separator = row.includes('\t') ? '\t' : row.includes(';') ? ';' : ',';
  const cells: string[] = [];
  let current = '';
  let quoted = false;

  for (let index = 0; index < row.length; index++) {
    const character = row[index];

    if (character === '"') {
      // A doubled quote inside a quoted cell is a literal quote.
      if (quoted && row[index + 1] === '"') {
        current += '"';
        index++;
        continue;
      }
      quoted = !quoted;
      continue;
    }
    if (character === separator && !quoted) {
      cells.push(current.trim());
      current = '';
      continue;
    }
    current += character;
  }
  cells.push(current.trim());

  return cells;
}
