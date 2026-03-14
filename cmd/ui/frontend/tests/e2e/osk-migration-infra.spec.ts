import { test, expect } from '@playwright/test'

test.describe('OSK Migration Infrastructure Wizard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    // Wait for pre-loaded state to be processed
    await page.waitForTimeout(2000)
    // Navigate to Migrate tab
    await page.click('button:has-text("Migrate")')
    await page.waitForTimeout(500)
  })

  test('Public path - generates Terraform for OSK cluster', async ({ page }) => {
    // Verify OSK section heading appears
    await expect(page.locator('h2:has-text("Open Source Kafka")')).toBeVisible()

    // Expand the OSK cluster accordion
    await page.click('h3:has-text("production-kafka-us-east")')
    await page.waitForTimeout(300)

    // Click the "Generate Terraform" button for Migration Infrastructure (phase 2)
    // Phase cards: 1=Confluent Cloud Infrastructure, 2=Migration Infrastructure, 3=Migration Assets
    const migrationInfraCard = page.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()
    await page.waitForTimeout(500)

    // Wizard opens - should see public/private networking question
    await expect(page.locator('h2:has-text("Kafka Migration - Public or Private Networking")')).toBeVisible()

    // Select "Yes" for public brokers
    await page.locator('label:has-text("Yes")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Public cluster link inputs step
    await expect(page.locator('h2:has-text("Public Migration | Cluster Link Configuration")')).toBeVisible()

    // Source fields should be pre-populated (if cluster data exists in state)
    await expect(page.locator('label:has-text("Source Kafka Cluster ID")')).toBeVisible()
    await expect(page.locator('label:has-text("Source Kafka Bootstrap Servers")')).toBeVisible()

    // Fill in the editable target fields
    await page.fill('#root_target_cluster_id', 'lkc-test123')
    await page.fill('#root_target_rest_endpoint', 'https://test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'osk-to-cc-test-link')

    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Confirmation step
    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible()
    await expect(page.locator('text=lkc-test123')).toBeVisible()

    // Generate terraform
    await page.click('button:has-text("Generate Terraform Files")')
    await page.waitForTimeout(2000)

    // Verify terraform files are shown in file viewer
    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })

  test('External Outbound path - generates Terraform for OSK cluster', async ({ page }) => {
    await expect(page.locator('h2:has-text("Open Source Kafka")')).toBeVisible()

    // Expand OSK cluster
    await page.click('h3:has-text("production-kafka-us-east")')
    await page.waitForTimeout(300)

    // Click Generate Terraform for Migration Infrastructure
    const migrationInfraCard = page.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()
    await page.waitForTimeout(500)

    // Select "No" for private networking
    await page.locator('label:has-text("No")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Private migration method question
    await expect(page.locator('h2:has-text("Private Migration | Method")')).toBeVisible()

    // Select external outbound (No, use external outbound cluster linking)
    await page.locator('label:has-text("No, use external outbound cluster linking")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // External outbound cluster linking inputs
    await expect(page.locator('h2:has-text("Private Migration | External Outbound Cluster Linking")')).toBeVisible()

    // Fill visible/editable fields for OSK external outbound
    await page.fill('#root_target_cluster_id', 'lkc-test123')
    await page.fill('#root_target_rest_endpoint', 'https://test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'osk-ext-outbound-link')
    await page.fill('#root_target_environment_id', 'env-test123')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-test.confluent.cloud:9092')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')
    await page.fill('#root_ext_outbound_subnet_id', 'subnet-test123')
    await page.fill('#root_ext_outbound_security_group_id', 'sg-test123')
    await page.fill('#root_source_region', 'us-east-1')
    await page.fill('#root_vpc_id', 'vpc-test123')

    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Confirmation
    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible()
    await page.click('button:has-text("Generate Terraform Files")')
    await page.waitForTimeout(2000)

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })

  test('Jump Cluster path - no IAM option, goes directly to SASL/SCRAM', async ({ page }) => {
    await expect(page.locator('h2:has-text("Open Source Kafka")')).toBeVisible()

    // Expand OSK cluster
    await page.click('h3:has-text("production-kafka-us-east")')
    await page.waitForTimeout(300)

    // Click Generate Terraform for Migration Infrastructure
    const migrationInfraCard = page.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()
    await page.waitForTimeout(500)

    // Select private (No)
    await page.locator('label:has-text("No")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Select jump cluster (Yes)
    await page.locator('label:has-text("Yes")').first().click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Internet gateway question
    await expect(page.locator('h2:has-text("Private Migration | Private Link - Internet Gateway")')).toBeVisible()
    await page.locator('label:has-text("No")').click()
    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Jump cluster networking inputs
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Configuration")')).toBeVisible()
    await page.fill('#root_vpc_id', 'vpc-test123')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')
    await page.fill('#root_jump_cluster_instance_type', 'm5.xlarge')
    await page.fill('#root_jump_cluster_broker_storage', '100')
    await page.fill('#root_jump_cluster_setup_host_subnet_cidr', '10.0.255.0/24')

    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Should go DIRECTLY to SASL/SCRAM auth step - NO auth type selection
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Authentication (SASL/SCRAM)")')).toBeVisible()

    // Verify IAM is NOT shown as a selectable option
    await expect(page.locator('label:has-text("IAM")')).not.toBeVisible()

    // Fill target cluster details
    await page.fill('#root_source_region', 'us-east-1')
    await page.fill('#root_target_environment_id', 'env-test123')
    await page.fill('#root_target_cluster_id', 'lkc-test123')
    await page.fill('#root_target_rest_endpoint', 'https://test.confluent.cloud:443')
    await page.fill('#root_target_bootstrap_endpoint', 'pkc-test.confluent.cloud:9092')
    await page.fill('#root_cluster_link_name', 'osk-jump-cluster-link')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test456')

    await page.click('button[type="submit"]:has-text("Next")')
    await page.waitForTimeout(500)

    // Confirmation
    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible()
    await page.click('button:has-text("Generate Terraform Files")')
    await page.waitForTimeout(2000)

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })
})
