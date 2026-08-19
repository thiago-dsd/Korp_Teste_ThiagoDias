/** Product as returned by the stock service. */
export interface Product {
  id: string;
  code: string;
  description: string;
  balance: number;
  /**
   * Bumped by every change, including the debit of a printed invoice. It is
   * sent back when saving so an edit made from a screen that is out of date is
   * refused instead of overwriting whatever happened since.
   */
  version: number;
  createdAt: string;
  updatedAt: string;
}

/** Fields accepted when creating a product. */
export interface NewProduct {
  code: string;
  description: string;
  balance: number;
}

/** Fields accepted when updating a product. The code is immutable. */
export interface ProductUpdate {
  description: string;
  balance: number;
  /** The version the form was loaded with. */
  version: number;
}

/** What caused a balance to change. */
export type MovementSource = 'registration' | 'edit' | 'adjustment' | 'invoice';

/**
 * One change to the balance of a product.
 *
 * The balance answers "how much is there"; the history answers "why", which is
 * the first question asked whenever a stock count does not match.
 */
export interface StockMovement {
  id: string;
  /** How much the balance moved; negative took stock out. */
  delta: number;
  /** What the balance became, so one row explains itself. */
  balanceAfter: number;
  source: MovementSource;
  /** The note written on a delivery or a correction, when there was one. */
  reason: string;
  /** Set when the cause was an invoice being printed. */
  invoiceId: string;
  /** Who did it, when a person did rather than an invoice. */
  actorEmail: string;
  createdAt: string;
}
