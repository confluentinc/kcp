import { useMemo } from 'react'
import CostAnalysis from './CostAnalysis'

interface RegionReportProps {
  region: {
    name: string
    costs?: {
      buckets: Array<{
        time_period_start: string
        time_period_end: string
        data: Record<string, Record<string, number>>
      }>
      metadata: any
    }
    clusters?: Array<{
      name: string
      aws_client_information?: any
    }>
  }
}

export default function RegionReport({ region }: RegionReportProps) {
  // Calculate total region cost
  const totalRegionCost = useMemo(() => {
    const costBuckets = region.costs?.buckets || []
    return costBuckets.reduce((sum: number, bucket: any) => {
      const bucketTotal = Object.values(bucket.data || {}).reduce(
        (bucketSum: number, service: any) => {
          return (
            bucketSum +
            Object.values(service || {}).reduce(
              (serviceSum: number, cost: any) => serviceSum + (typeof cost === 'number' ? cost : 0),
              0
            )
          )
        },
        0
      )
      return sum + bucketTotal
    }, 0)
  }, [region.costs?.buckets])

  // Calculate MSK-only cost
  const mskCost = useMemo(() => {
    const costBuckets = region.costs?.buckets || []
    return costBuckets.reduce((sum: number, bucket: any) => {
      const mskService = bucket.data?.['Amazon Managed Streaming for Apache Kafka'] || {}
      const mskTotal = Object.values(mskService).reduce(
        (serviceSum: number, cost: any) => serviceSum + (typeof cost === 'number' ? cost : 0),
        0
      )
      return sum + mskTotal
    }, 0)
  }, [region.costs?.buckets])

  const formatCurrency = (amount: number) =>
    new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(amount)

  return (
    <div className="max-w-7xl mx-auto space-y-6">
      {/* Page Title */}
      <div className="mb-6">
        <h1 className="text-3xl font-bold text-gray-900 dark:text-gray-100">Region Summary</h1>
        <p className="text-gray-600 dark:text-gray-400 mt-2">
          Cost analysis and cluster overview for the selected region
        </p>
      </div>

      {/* Region Header */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-3xl font-bold text-gray-900 dark:text-gray-100">{region.name}</h1>
            <p className="text-lg text-gray-600 dark:text-gray-300 mt-1">
              Region Cost Analysis • {region.clusters?.length || 0} clusters
            </p>
            <p className="text-sm text-gray-500 dark:text-gray-400 mt-1">
              Total MSK Cost: {formatCurrency(mskCost)} • Total All Services:{' '}
              {formatCurrency(totalRegionCost)}
            </p>
          </div>
          <div className="text-right">
            <div className="text-2xl font-bold text-green-600 dark:text-green-400">
              {formatCurrency(totalRegionCost)}
            </div>
            <p className="text-sm text-gray-500 dark:text-gray-400">Total Region Cost</p>
          </div>
        </div>
      </div>

      {/* Cost Analysis */}
      <div className="bg-white dark:bg-gray-800 rounded-lg shadow-sm border border-gray-200 dark:border-gray-700 transition-colors">
        <div className="border-b border-gray-200 dark:border-gray-700">
          <div className="px-6 py-4">
            <h2 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
              💰 Regional Cost Analysis
            </h2>
            <p className="text-sm text-gray-600 dark:text-gray-400 mt-1">
              Cost breakdown for all services in {region.name}
            </p>
          </div>
        </div>
        <div className="p-6">
          <CostAnalysis costData={region.costs?.buckets || []} />
        </div>
      </div>
    </div>
  )
}
