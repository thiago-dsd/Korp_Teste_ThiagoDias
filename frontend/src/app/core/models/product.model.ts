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
