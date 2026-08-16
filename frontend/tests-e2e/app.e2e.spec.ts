import { test, expect } from '@playwright/test';

test('the home page introduces the invoice system', async ({ page }) => {
  await page.goto('/');

  await expect(page).toHaveTitle('Invoice System');
  await expect(page.locator('h1')).toContainText('Invoice System');
});

test('the sidebar links to the application pages', async ({ page }) => {
  await page.goto('/home');

  await expect(page.locator('nav')).toContainText('Home');
});
