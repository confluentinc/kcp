import { test, expect } from '@playwright/test'

test.describe('OSK Migration Infrastructure Wizard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    // Wait for pre-loaded state to be processed - tabs only render once kcpState is loaded
    await page.waitForSelector('nav button', { timeout: 10000 })
    // Navigate to Migrate tab (scoped to nav to avoid matching other buttons)
    await page.locator('nav button:has-text("Migrate")').click()
    // Wait for the Migration Assets page to render with OSK section
    await page.waitForSelector('h2:has-text("Open Source Kafka")', { timeout: 10000 })
  })

  test('Public path - generates Terraform for OSK cluster', async ({ page }) => {
    // Verify OSK section heading appears
    await expect(page.locator('h2:has-text("Open Source Kafka")')).toBeVisible()

    // Expand the OSK cluster accordion (OSK is not auto-expanded when MSK clusters exist)
    await page.locator('h3:has-text("production-kafka-us-east")').click()

    // Wait for phase cards to be visible inside the expanded OSK accordion
    const oskSection = page.locator('h3:has-text("production-kafka-us-east")').locator('..').locator('..').locator('..')
    await expect(oskSection.locator('h4:has-text("Migration Infrastructure")')).toBeVisible({ timeout: 5000 })

    // Click the "Generate Terraform" button for Migration Infrastructure (phase 2)
    // Phase cards: 1=Confluent Cloud Infrastructure, 2=Migration Infrastructure, 3=Migration Assets
    const migrationInfraCard = oskSection.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()

    // Wizard opens in modal - should see public/private networking question
    await expect(page.locator('h2:has-text("Kafka Migration - Public or Private Networking")')).toBeVisible({ timeout: 5000 })

    // Select "Yes" for public brokers
    await page.locator('label:has-text("Yes")').click()
    await page.locator('button[type="submit"]').click()

    // Public cluster link inputs step
    await expect(page.locator('h2:has-text("Public Migration | Cluster Link Configuration")')).toBeVisible({ timeout: 5000 })

    // Source fields should be pre-populated (if cluster data exists in state)
    await expect(page.locator('label:has-text("Source Kafka Cluster ID")')).toBeVisible()
    await expect(page.locator('label:has-text("Source Kafka Bootstrap Servers")')).toBeVisible()

    // Fill in the editable target fields
    await page.fill('#root_target_cluster_id', 'lkc-test123')
    await page.fill('#root_target_rest_endpoint', 'https://test.confluent.cloud:443')
    await page.fill('#root_cluster_link_name', 'osk-to-cc-test-link')

    await page.locator('button[type="submit"]').click()

    // Confirmation step
    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible({ timeout: 5000 })
    await expect(page.locator('text=lkc-test123')).toBeVisible()

    // Generate terraform
    await page.locator('button:has-text("Generate Terraform Files")').click()

    // Verify terraform files are shown in file viewer
    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })

  test('External Outbound path - generates Terraform for OSK cluster', async ({ page }) => {
    await expect(page.locator('h2:has-text("Open Source Kafka")')).toBeVisible()

    // Expand OSK cluster
    await page.locator('h3:has-text("production-kafka-us-east")').click()

    // Wait for phase cards to be visible inside the expanded OSK accordion
    const oskSection = page.locator('h3:has-text("production-kafka-us-east")').locator('..').locator('..').locator('..')
    await expect(oskSection.locator('h4:has-text("Migration Infrastructure")')).toBeVisible({ timeout: 5000 })

    // Click Generate Terraform for Migration Infrastructure
    const migrationInfraCard = oskSection.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()

    // Wizard opens - select "No" for private networking
    await expect(page.locator('h2:has-text("Kafka Migration - Public or Private Networking")')).toBeVisible({ timeout: 5000 })
    await page.locator('label:has-text("No")').click()
    await page.locator('button[type="submit"]').click()

    // Private migration method question
    await expect(page.locator('h2:has-text("Private Migration | Method")')).toBeVisible({ timeout: 5000 })

    // Select external outbound (No, use external outbound cluster linking)
    await page.locator('label:has-text("No, use external outbound cluster linking")').click()
    await page.locator('button[type="submit"]').click()

    // External outbound cluster linking inputs
    await expect(page.locator('h2:has-text("Private Migration | External Outbound Cluster Linking")')).toBeVisible({ timeout: 5000 })

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

    await page.locator('button[type="submit"]').click()

    // Confirmation
    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })

  test('Jump Cluster path - no IAM option, goes directly to SASL/SCRAM', async ({ page }) => {
    await expect(page.locator('h2:has-text("Open Source Kafka")')).toBeVisible()

    // Expand OSK cluster
    await page.locator('h3:has-text("production-kafka-us-east")').click()

    // Wait for phase cards to be visible inside the expanded OSK accordion
    const oskSection = page.locator('h3:has-text("production-kafka-us-east")').locator('..').locator('..').locator('..')
    await expect(oskSection.locator('h4:has-text("Migration Infrastructure")')).toBeVisible({ timeout: 5000 })

    // Click Generate Terraform for Migration Infrastructure
    const migrationInfraCard = oskSection.locator('h4:has-text("Migration Infrastructure")').locator('..')
    await migrationInfraCard.locator('button:has-text("Generate Terraform")').click()

    // Select private (No)
    await expect(page.locator('h2:has-text("Kafka Migration - Public or Private Networking")')).toBeVisible({ timeout: 5000 })
    await page.locator('label:has-text("No")').click()
    await page.locator('button[type="submit"]').click()

    // Select jump cluster (Yes)
    await expect(page.locator('h2:has-text("Private Migration | Method")')).toBeVisible({ timeout: 5000 })
    await page.locator('label:has-text("Yes")').first().click()
    await page.locator('button[type="submit"]').click()

    // Internet gateway question
    await expect(page.locator('h2:has-text("Private Migration | Private Link - Internet Gateway")')).toBeVisible({ timeout: 5000 })
    await page.locator('label:has-text("No")').click()
    await page.locator('button[type="submit"]').click()

    // Jump cluster networking inputs
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Configuration")')).toBeVisible({ timeout: 5000 })
    await page.fill('#root_vpc_id', 'vpc-test123')
    await page.fill('#root_existing_private_link_vpce_id', 'vpce-test123')
    await page.fill('#root_jump_cluster_instance_type', 'm5.xlarge')
    await page.fill('#root_jump_cluster_broker_storage', '100')
    await page.fill('#root_jump_cluster_setup_host_subnet_cidr', '10.0.255.0/24')

    await page.locator('button[type="submit"]').click()

    // Should go DIRECTLY to SASL/SCRAM auth step - NO auth type selection
    await expect(page.locator('h2:has-text("Private Migration | Jump Cluster - Authentication (SASL/SCRAM)")')).toBeVisible({ timeout: 5000 })

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

    await page.locator('button[type="submit"]').click()

    // Confirmation
    await expect(page.locator('h2:has-text("Review Your Configuration")')).toBeVisible({ timeout: 5000 })
    await page.locator('button:has-text("Generate Terraform Files")').click()

    await expect(page.locator('text=main.tf')).toBeVisible({ timeout: 10000 })
  })
})
