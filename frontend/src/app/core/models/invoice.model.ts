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
}

/** A line requested when creating an invoice. */
export interface NewInvoiceItem {
  productId: string;
  quantity: number;
}
