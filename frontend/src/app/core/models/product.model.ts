/** Product as returned by the stock service. */
export interface Product {
  id: string;
  code: string;
  description: string;
  balance: number;
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
}
