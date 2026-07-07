import { expect, test } from '@playwright/test';

test('admin SPA loads and protects API fallback', async ({ page, request }) => {
  await page.goto('/admin');
  await expect(page.getByText(/GuDa Gateway Admin|Admin UI assets not built/)).toBeVisible();
  const api404 = await request.get('/admin/api/does-not-exist');
  expect([401, 404]).toContain(api404.status());
  expect(await api404.text()).not.toContain('<!doctype html');
});

test('admin login and key workflows are reachable when token is provided', async ({ page }) => {
  const token = process.env.GUDA_ADMIN_E2E_TOKEN;
  test.skip(!token, 'set GUDA_ADMIN_E2E_TOKEN to run authenticated admin smoke');
  await page.goto('/admin');
  await page.getByLabel(/admin token/i).fill(token!);
  await page.getByRole('button', { name: /log in/i }).click();
  await expect(page.getByText(/Overview|Provider health|Gateway keys/i)).toBeVisible();
  await page.getByRole('link', { name: /Gateway Keys/i }).click();
  await expect(page.getByRole('heading', { name: /Gateway Keys/i })).toBeVisible();
  await page.getByRole('link', { name: /Provider Keys/i }).click();
  await expect(page.getByRole('heading', { name: /Provider Keys/i })).toBeVisible();
  await page.getByRole('link', { name: /Usage/i }).click();
  await expect(page.getByRole('heading', { name: /Usage/i })).toBeVisible();
  await page.getByRole('link', { name: /Audit/i }).click();
  await expect(page.getByRole('heading', { name: /Audit/i })).toBeVisible();
});
