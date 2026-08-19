/** Lifecycle state of an invoice. */
export type InvoiceStatus = 'OPEN' | 'PRINTING' | 'CLOSED';

/** Reason why the last print attempt did not complete. */
export interface InvoiceFailure {
  code: string;
  message: string;
}

/** A product and the quantity used by an invoice. */
export interface InvoiceItem {
  id: string;
  productId: string;
  productCode: string;
  productDescription: string;
  quantity: number;
}

/**
 * Who did something to an invoice.
 *
 * The email is a snapshot taken when it happened: deleting an account cannot
 * erase who signed a document that was already issued.
 */
export interface InvoiceAuthor {
  id: string;
  email: string;
}

/** Invoice as returned by the billing service. */
export interface Invoice {
  id: string;
  number: number;
  status: InvoiceStatus;
  items: InvoiceItem[];
  failure: InvoiceFailure | null;
  createdAt: string;
  updatedAt: string;
  printedAt: string | null;
  /** Who issued it. Invoices from before authorship was recorded have none. */
  issuedBy: InvoiceAuthor | null;
  /** Who printed it, once it has been printed. */
  printedBy: InvoiceAuthor | null;
}

/** A line requested when creating an invoice. */
export interface NewInvoiceItem {
  productId: string;
  quantity: number;
}

/** A line the assistant suggests for a sentence written by the operator. */
export interface DraftLine {
  productId: string;
  productCode: string;
  productDescription: string;
  quantity: number;
  /** Balance the stock has right now, so the screen can warn straight away. */
  balance: number;
}

/**
 * The listing filters a question was understood as.
 *
 * Named after the query string the screen writes, not after the API: what the
 * assistant produces has to land in the same place a hand-set filter does.
 */
export interface InvoiceSearch {
  filters: {
    status: string;
    number: string;
    from: string;
    to: string;
    product: string;
    attention: boolean;
  };
  /** What part of the question was not used, in plain words. */
  warnings: string[];
}

/** What the assistant made of a sentence. */
export interface InvoiceDraft {
  lines: DraftLine[];
  /** What could not be turned into a line, in plain words. */
  warnings: string[];
  model: string;
}
