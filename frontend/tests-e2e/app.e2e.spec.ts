import { test, expect } from '@playwright/test';

// These assertions are written in English; pin the locale so a pt-BR default
// in the app itself never turns this suite into a false failure.
test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('locale', 'en-US'));
});

test('the home page introduces the invoice system', async ({ page }) => {
  await page.goto('/');

  await expect(page).toHaveTitle('Stockly');
  await expect(page.locator('h1')).toContainText('Stockly');
});

test('the sidebar links to the application pages', async ({ page }) => {
  await page.goto('/home');

  await expect(page.locator('nav')).toContainText('Home');
});
