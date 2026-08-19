import { expect, test } from '@playwright/test';

/**
 * Walks the whole flow against the running services: register a product,
 * issue an invoice for it and print it, checking that the balance drops.
 *
 * Each run works on its own product code, so runs never collide.
 */
const runId = Date.now().toString(36).toUpperCase();

// These assertions are written in English; pin the locale so a pt-BR default
// in the app itself never turns this suite into a false failure.
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('locale', 'en-US'));
});

test('registers a product, issues an invoice and prints it', async ({ page }) => {
  const code = `E2E-${runId}`;

  await page.goto('/products');

  await page.getByRole('button', { name: 'New product' }).click();
  await page.getByLabel('Code').fill(code);
  await page.getByLabel('Description').fill('End to end product');
  await page.getByLabel('Balance').fill('10');
  await page.getByRole('button', { name: 'Register product' }).click();

  const productRow = page.getByRole('row', { name: new RegExp(code) });
  await expect(productRow).toBeVisible();
  await expect(productRow).toContainText('10');

  // Issue an invoice using two units of the product.
  await page.goto('/invoices/new');
  await page.getByLabel('Product').selectOption({ label: new RegExp(`^${code}`) as unknown as string });
  await page.getByLabel('Quantity').fill('2');
  await page.getByRole('button', { name: 'Add product' }).click();
  await page.getByRole('button', { name: 'Create invoice' }).click();

  await expect(page.getByRole('heading', { level: 1 })).toContainText('Invoice #');
  await expect(page.getByRole('status').first()).toContainText('Open');

  // Printing shows progress and ends with the invoice closed.
  await page.getByRole('button', { name: 'Print invoice' }).click();
  await expect(page.getByText('Invoice printed')).toBeVisible({ timeout: 20_000 });
  await expect(page.getByRole('button', { name: 'Print invoice' })).toBeDisabled();

  // The balance dropped by the quantity used by the invoice.
  await page.goto('/products');
  await expect(page.getByRole('row', { name: new RegExp(code) })).toContainText('8');
});

test('refuses to print an invoice whose quantity is above the balance', async ({ page }) => {
  const code = `E2E-LOW-${runId}`;

  await page.goto('/products');
  await page.getByRole('button', { name: 'New product' }).click();
  await page.getByLabel('Code').fill(code);
  await page.getByLabel('Description').fill('Scarce product');
  await page.getByLabel('Balance').fill('1');
  await page.getByRole('button', { name: 'Register product' }).click();
  await expect(page.getByRole('row', { name: new RegExp(code) })).toBeVisible();

  await page.goto('/invoices/new');
  await page.getByLabel('Product').selectOption({ label: new RegExp(`^${code}`) as unknown as string });
  await page.getByLabel('Quantity').fill('5');
  await page.getByRole('button', { name: 'Add product' }).click();
  await expect(page.getByText('above the current balance')).toBeVisible();
  await page.getByRole('button', { name: 'Create invoice' }).click();

  await page.getByRole('button', { name: 'Print invoice' }).click();

  await expect(page.getByText('Printing did not go through')).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText('Product balance is not enough.')).toBeVisible();
  // The invoice is open again, so another attempt is possible.
  await expect(page.getByRole('button', { name: 'Print invoice' })).toBeEnabled();
});
