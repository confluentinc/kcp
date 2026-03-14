import { test, expect } from '@playwright/test'

test.describe('OSK Migration Infrastructure Wizard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    // Wait for pre-loaded state - tabs only render once kcpState is loaded
    await page.waitForSelector('nav button', { timeout: 10000 })
    // Navigate to Migrate tab
    await page.locator('nav button:has-text("Migrate")').click()
    // Wait for the Migration Assets page to render with OSK section
    await page.waitForSelector('text=Open Source Kafka', { timeout: 10000 })
  })

  test('Public path - generates Terraform for OSK cluster', async ({ page }) => {
    // Click OSK cluster to expand it
    await page.locator('text=production-kafka-us-east').click()
    await page.waitForTimeout(500)

    // Find and click the Generate Terraform button for Migration Infrastructure
    // There are multiple "Generate Terraform" buttons (one per phase card), we want the 2nd one
    const generateButtons = page.locator('button:has-text("Generate Terraform")')
    await generateButtons.nth(1).click()

    // Wizard opens - should see public/private networking question
    await expect(page.locator('h2').first()).toBeVisible({ timeout: 5000 })

    // Select "Yes" for public brokers
    await page.locator('label:has-text("Yes")').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Public cluster link inputs step - fill target details
    await page.fill('#root_target_cluster_id', 'lkc-test123')
    await page.fill('#root_target_rest_endpoint', 'https://test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'osk-to-cc-test-link')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Confirmation step
    await expect(page.locator('text=Review Your Configuration')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=lkc-test123')).toBeVisible()

    // Generate terraform
    await page.locator('button:has-text("Generate Terraform Files")').click()

    // Verify terraform files are shown
    await expect(page.locator('text=main.tf').first()).toBeVisible({ timeout: 10000 })
  })

  test('External Outbound path - generates Terraform for OSK cluster', async ({ page }) => {
    await page.locator('text=production-kafka-us-east').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    // Select "No" for private networking
    await page.locator('label:has-text("No")').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Select External Outbound Cluster Link
    await page.locator('label:has-text("External Outbound")').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Fill external outbound fields
    await page.fill('#root_target_cluster_id', 'lkc-test123')
    await page.fill('#root_target_rest_endpoint', 'https://test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'osk-ext-outbound-link')
    await page.fill('#root_target_environment_id', 'env-test123')
    await page.fill('#root_source_region', 'us-east-1')
    await page.fill('#root_vpc_id', 'vpc-test123')
    await page.fill('#root_ext_outbound_subnet_id', 'subnet-test123')
    await page.fill('#root_ext_outbound_security_group_id', 'sg-test123')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Confirmation
    await expect(page.locator('text=Review Your Configuration')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf').first()).toBeVisible({ timeout: 10000 })
  })

  test('Jump Cluster path - no IAM option, goes directly to SASL/SCRAM', async ({ page }) => {
    await page.locator('text=production-kafka-us-east').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    // Select private (No)
    await page.locator('label:has-text("No")').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Select Jump Cluster
    await page.locator('label:has-text("Jump Cluster")').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Internet gateway question - select No
    await page.locator('label:has-text("No")').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Jump cluster networking - fill required fields
    await page.fill('#root_vpc_id', 'vpc-test123')
    await page.fill('#root_source_region', 'us-east-1')
    await page.fill('#root_jump_cluster_instance_type', 'm5.xlarge')
    await page.fill('#root_jump_cluster_broker_storage', '100')
    await page.fill('#root_jump_cluster_setup_host_subnet_cidr', '10.0.255.0/24')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Should go DIRECTLY to SASL/SCRAM config - NO auth question
    // Verify IAM is NOT shown as a selectable option
    await expect(page.locator('label:has-text("IAM")')).not.toBeVisible()

    // Fill target cluster details
    await page.fill('#root_target_environment_id', 'env-test123')
    await page.fill('#root_target_cluster_id', 'lkc-test123')
    await page.fill('#root_target_rest_endpoint', 'https://test.confluent.cloud:443')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-test.confluent.cloud:9092')
    await page.fill('#root_cluster_link_name', 'osk-jump-cluster-link')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Confirmation
    await expect(page.locator('text=Review Your Configuration')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf').first()).toBeVisible({ timeout: 10000 })
  })
})
