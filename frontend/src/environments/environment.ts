export const environment = {
  production: false,
  /** Base URL of the stock service, which owns products and balances. */
  stockApiUrl: 'http://localhost:8081',
  /** Base URL of the identity service, which owns accounts and tokens. */
  identityApiUrl: 'http://localhost:8083',
  /** Base URL of the billing service, which owns invoices and printing. */
  billingApiUrl: 'http://localhost:8082',
};
