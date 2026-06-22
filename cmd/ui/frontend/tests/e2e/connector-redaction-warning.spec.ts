import { test, expect } from '@playwright/test'
import stateOSKOnly from '../fixtures/state-osk-only.json' with { type: 'json' }

// Build a state file with a single self-managed connector whose config is the
// caller-supplied map, injected into the known-valid OSK fixture so the rest of
// the cluster shape stays realistic.
function stateWithConnectorConfig(config: Record<string, string>) {
  const state = structuredClone(stateOSKOnly) as Record<string, any>
  state.osk_sources.clusters[0].kafka_admin_client_information.self_managed_connectors = {
    connectors: [
      {
        name: 'pg-sink',
        config,
        state: 'RUNNING',
        connect_host: 'http://connect.example.com:8083',
      },
    ],
  }
  return state
}

async function openConnectorsView(page: import('@playwright/test').Page, state: unknown) {
  await page.goto('/')
  await page.click('button:has-text("Upload KCP State File")')
  await page.locator('input[type="file"]').setInputFiles({
    name: 'state.json',
    mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify(state)),
  })
  await page.waitForTimeout(500)
  await page.click('text=prod-kafka-cluster')
  await page.click('nav button:has-text("Connectors")')
}

test.describe('Connector redaction warning banner', () => {
  test('shows the warning when a connector config contains the redaction placeholder', async ({
    page,
  }) => {
    await openConnectorsView(
      page,
      stateWithConnectorConfig({
        'connector.class': 'io.confluent.connect.jdbc.JdbcSourceConnector',
        'database.password': '<kcp-redacted>',
      })
    )

    await expect(page.getByTestId('redacted-config-warning')).toBeVisible()
  })

  test('hides the warning when no connector config is redacted', async ({ page }) => {
    await openConnectorsView(
      page,
      stateWithConnectorConfig({
        'connector.class': 'io.confluent.connect.jdbc.JdbcSourceConnector',
        'tasks.max': '3',
      })
    )

    await expect(page.getByTestId('redacted-config-warning')).toHaveCount(0)
  })
})
