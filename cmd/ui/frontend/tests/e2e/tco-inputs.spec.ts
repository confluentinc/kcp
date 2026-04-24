import { test, expect } from '@playwright/test'

test.describe('TCO Inputs Page', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await page.waitForSelector('text=AWS MSK', { timeout: 10000 })
    await page.locator('button:has-text("TCO Inputs")').click()
    await page.waitForSelector('text=Workload Assumptions', { timeout: 5000 })
  })

  test('shows MSK and OSK tabs', async ({ page }) => {
    await expect(page.locator('button:has-text("MSK")')).toBeVisible()
    await expect(page.locator('button:has-text("OSK")')).toBeVisible()
  })

  test('MSK tab shows MSK cluster columns', async ({ page }) => {
    await expect(page.locator('th:has-text("kcp-playground")')).toBeVisible()
  })

  test('OSK tab shows OSK cluster with metadata', async ({ page }) => {
    await page.locator('button:has-text("OSK")').click()
    await expect(page.locator('th:has-text("production-kafka-us-east")')).toBeVisible()
    await expect(page.locator('text=env:')).toBeVisible()
    await expect(page.getByText('env: production')).toBeVisible()
  })

  test('tab switching preserves input values', async ({ page }) => {
    const mskInput = page.locator('input[type="number"]').first()
    await mskInput.fill('42.5')

    await page.locator('button:has-text("OSK")').click()
    await page.locator('button:has-text("MSK")').click()

    await expect(mskInput).toHaveValue('42.5')
  })

  test('CSV preview shows active tab clusters only', async ({ page }) => {
    const csvPreview = page.locator('pre')
    await expect(csvPreview).toContainText('kcp-playground')

    await page.locator('button:has-text("OSK")').click()
    await expect(csvPreview).toContainText('production-kafka-us-east')
    await expect(csvPreview).not.toContainText('kcp-playground')
  })

  test('tabs with data do not show empty state message', async ({ page }) => {
    await page.locator('button:has-text("MSK")').click()
    await expect(page.locator('text=No MSK clusters found')).not.toBeVisible()
    await page.locator('button:has-text("OSK")').click()
    await expect(page.locator('text=No OSK clusters found')).not.toBeVisible()
  })

  test('OSK label pills are rendered', async ({ page }) => {
    await page.locator('button:has-text("OSK")').click()
    await expect(page.locator('text=team: platform')).toBeVisible()
    await expect(page.locator('text=cost-center: engineering')).toBeVisible()
  })
})
