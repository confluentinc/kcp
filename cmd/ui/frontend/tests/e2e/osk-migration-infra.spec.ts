import { test, expect } from '@playwright/test'

test.describe('OSK Migration Infrastructure Wizard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await page.waitForSelector('nav button', { timeout: 10000 })
    await page.locator('nav button:has-text("Migrate")').click()
    await page.waitForSelector('text=Open Source Kafka', { timeout: 10000 })
  })

  test('Public path - generates Terraform for OSK cluster', async ({ page }) => {
    await page.locator('text=production-kafka-us-east').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    // Select "Yes" for public brokers (index 0 = Yes)
    await page.locator('#root_has_public_brokers-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Fill target cluster details
    await page.fill('#root_target_cluster_id', 'lkc-test123')
    await page.fill('#root_target_rest_endpoint', 'https://test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'osk-to-cc-test-link')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Confirmation
    await expect(page.locator('text=Review Your Configuration')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf').first()).toBeVisible({ timeout: 10000 })
  })

  test('External Outbound path - generates Terraform for OSK cluster', async ({ page }) => {
    await page.locator('text=production-kafka-us-east').click()
    await page.waitForTimeout(500)

    await page.locator('button:has-text("Generate Terraform")').nth(1).click()
    await page.waitForTimeout(500)

    // Select "No" for private networking (index 1 = No)
    await page.locator('#root_has_public_brokers-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Select External Outbound (index 1 = "No, use external outbound cluster linking")
    await page.locator('#root_use_jump_clusters-1').click()
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

    // Select private (index 1 = No)
    await page.locator('#root_has_public_brokers-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Select Jump Cluster (index 0 = "Yes")
    await page.locator('#root_use_jump_clusters-0').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Internet gateway - select No (index 1)
    await page.locator('#root_has_existing_internet_gateway-1').click()
    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Jump cluster networking - fill ALL required fields (no defaults for OSK)
    await page.fill('#root_vpc_id', 'vpc-test123')
    await page.fill('#root_target_environment_id', 'env-test123')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-test.confluent.cloud:9092')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')
    await page.fill('#root_jump_cluster_instance_type', 'm5.xlarge')
    await page.fill('#root_jump_cluster_broker_storage', '100')
    // Fill broker subnet CIDR array items (3 items by default)
    await page.fill('#root_jump_cluster_broker_subnet_cidr_0', '10.0.1.0/24')
    await page.fill('#root_jump_cluster_broker_subnet_cidr_1', '10.0.2.0/24')
    await page.fill('#root_jump_cluster_broker_subnet_cidr_2', '10.0.3.0/24')
    await page.fill('#root_jump_cluster_setup_host_subnet_cidr', '10.0.255.0/24')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Should go DIRECTLY to SASL/SCRAM config - NO IAM auth question
    await expect(page.locator('label:has-text("IAM")')).not.toBeVisible()

    // Auth step - fill all required fields (source cluster ID and bootstrap are pre-populated)
    await page.fill('#root_source_region', 'us-east-1')
    await page.fill('#root_target_cluster_id', 'lkc-test123')
    await page.fill('#root_target_rest_endpoint', 'https://test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'osk-jump-cluster-link')
    await page.fill('#root_target_environment_id', 'env-test123')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-test.confluent.cloud:9092')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test456')

    await page.locator('button[type="submit"]').click()
    await page.waitForTimeout(500)

    // Confirmation
    await expect(page.locator('text=Review Your Configuration')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf').first()).toBeVisible({ timeout: 10000 })
  })
})
