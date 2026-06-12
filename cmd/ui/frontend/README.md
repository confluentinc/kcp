# KCP Frontend

React + TypeScript frontend for the KCP web UI.

## Development

### Prerequisites

- Node.js 18+
- Yarn

### Setup

```bash
yarn install
```

### Development Server

```bash
yarn dev
```

Frontend runs on `http://localhost:5173` with hot reload.

### Build

```bash
yarn build
```

Built assets are embedded into the Go binary via `frontend.go`.

### Type Checking

```bash
yarn type-check
yarn type-check:watch
```

### Linting

```bash
yarn lint
```

## Testing

### E2E Tests with Playwright

Playwright tests run against the full Go backend + frontend.

**Run all tests:**

```bash
yarn test:e2e
```

**Run with UI mode (interactive):**

```bash
yarn test:e2e:ui
```

**Debug specific test:**

```bash
yarn test:e2e:debug osk-sidebar.spec.ts
```

**Run with browser visible:**

```bash
yarn test:e2e:headed
```

### Test Fixtures

Test fixtures are in `tests/fixtures/`:
- `state-osk-only.json` - OSK clusters only
- `state-both.json` - Both MSK and OSK clusters

## Architecture

### Source Types

The UI supports two Kafka source types:

**MSK (AWS Managed Streaming for Kafka)**
- Regions → Clusters hierarchy
- Displays: ARN, VPC, instance type, CloudWatch metrics, costs
- Tabs: Cluster, Metrics, Topics, ACLs, Connectors, Clients

**OSK (Open Source Kafka)**
- Flat cluster list
- Displays: Bootstrap servers, metadata, labels
- Tabs: Cluster, Topics, ACLs, Connectors, Clients (no Metrics)

### State Management

- Zustand for global state
- `useAppStore` hook provides access to:
  - `processedState` - unified source data
  - `selectedSourceType` - 'msk' | 'osk'
  - `selectOSKCluster(clusterId)` - navigate to OSK cluster
  - `selectCluster(region, arn)` - navigate to MSK cluster

### Type Guards

Use type guards from `lib/sourceUtils.ts` to safely access source-specific data:

```typescript
import { isMSKSource, isOSKSource } from '@/lib/sourceUtils'

const mskSource = processedState?.sources.find(isMSKSource)
const oskSource = processedState?.sources.find(isOSKSource)
```

## Tech Stack

- React 19
- TypeScript 5.8
- Vite 7
- Zustand (state management)
- Recharts (charts)
- Tailwind CSS 4
- Playwright (E2E testing)
