import { useMemo, useState, useEffect } from 'react'
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from 'recharts'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Button } from '@/components/ui/button'

interface CostBucket {
  Groups: Array<{
    Keys: string[]
    Metrics: {
      UnblendedCost: {
        Amount: string
        Unit: string
      }
    }
  }>
  TimePeriod: {
    Start: string
    End: string
  }
}

interface CostAnalysisProps {
  costData: CostBucket[]
}

export default function CostAnalysis({ costData }: CostAnalysisProps) {
  const [sortField, setSortField] = useState<'date' | 'service' | 'cost' | 'usage_type'>('date')
  const [sortDirection, setSortDirection] = useState<'asc' | 'desc'>('desc')
  const [isLoading, setIsLoading] = useState(true)
  const [selectedService, setSelectedService] = useState<string>(
    'Amazon Managed Streaming for Apache Kafka'
  )
  // Optimized cost calculations
  const costAnalytics = useMemo(() => {
    const formatCurrency = (amount: number) =>
      new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD' }).format(amount)

    // Single pass through data - much more efficient
    const flatCostData: Array<{
      time_period_start: string
      time_period_end: string
      service: string
      usage_type: string
      cost: number
    }> = []

    let totalCost = 0
    const costByService: Record<string, number> = {}
    const costByMonth: Record<string, number> = {}
    const costByMonthAndService: Record<string, Record<string, number>> = {}

    // Single iteration - calculate everything at once for new data structure
    costData.forEach((result) => {
      const month = new Date(result.TimePeriod?.Start).toLocaleDateString('en-US', {
        year: 'numeric',
        month: 'short',
      })

      result.Groups?.forEach((group) => {
        const service = group.Keys?.[0] || 'Unknown'
        const usageType = group.Keys?.[1] || 'Unknown'
        const cost = parseFloat(group.Metrics?.UnblendedCost?.Amount || '0')

        if (cost > 0) {
          // Add to flat data
          flatCostData.push({
            time_period_start: result.TimePeriod?.Start,
            time_period_end: result.TimePeriod?.End,
            service,
            usage_type: usageType,
            cost,
          })

          // Calculate aggregations in the same loop
          totalCost += cost
          costByService[service] = (costByService[service] || 0) + cost
          costByMonth[month] = (costByMonth[month] || 0) + cost

          if (!costByMonthAndService[month]) {
            costByMonthAndService[month] = {}
          }
          costByMonthAndService[month][service] =
            (costByMonthAndService[month][service] || 0) + cost
        }
      })
    })

    const topServices = Object.entries(costByService)
      .sort(([, a], [, b]) => b - a)
      .slice(0, 5)

    const monthlyData = Object.entries(costByMonth).sort(
      ([a], [b]) => new Date(a + ' 1').getTime() - new Date(b + ' 1').getTime()
    )

    const totalMonths = monthlyData.length
    const avgMonthlyCost = totalCost / (totalMonths || 1)

    // Prepare chart data
    const monthlyChartData = monthlyData.map(([month, cost]) => ({
      month,
      cost,
      formattedCost: formatCurrency(cost),
    }))

    const serviceChartData = topServices.map(([service, cost]) => ({
      service: service.replace('Amazon Elastic Compute Cloud - ', 'EC2 - ').replace(' - Other', ''),
      cost,
      formattedCost: formatCurrency(cost),
    }))

    // Get available services for dropdown
    const availableServices = ['All Services', ...Object.keys(costByService)]

    return {
      totalCost,
      costByService,
      costByMonth,
      costByMonthAndService,
      topServices,
      monthlyData,
      totalMonths,
      avgMonthlyCost,
      monthlyChartData,
      serviceChartData,
      formatCurrency,
      flatCostData,
      availableServices,
    }
  }, [costData])

  // Filter data based on selected service
  const filteredData = useMemo(() => {
    if (selectedService === 'All Services') {
      return {
        chartData: costAnalytics.monthlyChartData,
        tableData: costAnalytics.flatCostData,
      }
    }

    // Filter monthly data for selected service
    const filteredMonthlyData = Object.entries(costAnalytics.costByMonthAndService).map(
      ([month, services]) => ({
        month,
        cost: services[selectedService] || 0,
        formattedCost: costAnalytics.formatCurrency(services[selectedService] || 0),
      })
    )

    // Filter table data for selected service
    const filteredTableData = costAnalytics.flatCostData.filter(
      (item) => item.service === selectedService
    )

    return {
      chartData: filteredMonthlyData,
      tableData: filteredTableData,
    }
  }, [selectedService, costAnalytics])

  // Handle loading state - no artificial delays
  useEffect(() => {
    setIsLoading(true)
    // Use requestAnimationFrame to allow React to render, then immediately finish loading
    const frame = requestAnimationFrame(() => {
      setIsLoading(false)
    })

    return () => cancelAnimationFrame(frame)
  }, [costData])

  if (isLoading) {
    return (
      <div className="space-y-6">
        {/* Loading Summary Cards */}
        <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
          {[1, 2, 3, 4].map((i) => (
            <div
              key={i}
              className="bg-gray-50 border border-gray-200 rounded-lg p-6"
            >
              <div className="animate-pulse">
                <div className="h-8 bg-gray-300 rounded w-20 mb-2"></div>
                <div className="h-4 bg-gray-200 rounded w-16"></div>
              </div>
            </div>
          ))}
        </div>

        {/* Loading Chart */}
        <div className="bg-white rounded-lg border p-6">
          <h3 className="text-xl font-semibold text-gray-900 mb-4">💰 Cost Analysis</h3>
          <div className="flex items-center justify-center h-80">
            <div className="flex flex-col items-center space-y-4">
              <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-500"></div>
              <p className="text-gray-500">Processing cost data...</p>
              <div className="text-xs text-gray-400">Analyzing cost buckets and trends</div>
            </div>
          </div>
        </div>
      </div>
    )
  }

  if (costData.length === 0) {
    return (
      <div className="bg-white rounded-lg border p-6">
        <h3 className="text-xl font-semibold text-gray-900 mb-4">💰 Cost Analysis</h3>
        <p className="text-gray-500">No cost data available for this region.</p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Total Summary Cards */}
      <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
        <div className="bg-white dark:bg-gray-700 border border-gray-200 dark:border-gray-600 rounded-lg p-4 transition-colors">
          <div className="text-xl font-bold text-gray-900 dark:text-gray-100">
            {costAnalytics.formatCurrency(costAnalytics.totalCost)}
          </div>
          <div className="text-gray-600 dark:text-gray-400 text-sm">Total All Services</div>
        </div>
        <div className="bg-white dark:bg-gray-700 border border-gray-200 dark:border-gray-600 rounded-lg p-4 transition-colors">
          <div className="text-xl font-bold text-gray-900 dark:text-gray-100">
            {costAnalytics.formatCurrency(costAnalytics.avgMonthlyCost)}
          </div>
          <div className="text-gray-600 dark:text-gray-400 text-sm">Avg Monthly</div>
        </div>
        <div className="bg-white dark:bg-gray-700 border border-gray-200 dark:border-gray-600 rounded-lg p-4 transition-colors">
          <div className="text-xl font-bold text-gray-900 dark:text-gray-100">
            {costAnalytics.totalMonths}
          </div>
          <div className="text-gray-600 dark:text-gray-400 text-sm">Months</div>
        </div>
        <div className="bg-white dark:bg-gray-700 border border-gray-200 dark:border-gray-600 rounded-lg p-4 transition-colors">
          <div className="text-xl font-bold text-gray-900 dark:text-gray-100">
            {Object.keys(costAnalytics.costByService).length}
          </div>
          <div className="text-gray-600 dark:text-gray-400 text-sm">Services</div>
        </div>
      </div>

      {/* Monthly Cost Trend */}
      <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 mb-4">
          <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
            📈 Monthly Cost Trend
          </h3>
          <div className="flex items-center space-x-2">
            <span className="text-sm text-gray-600 dark:text-gray-400">Service:</span>
            <select
              value={selectedService}
              onChange={(e) => setSelectedService(e.target.value)}
              className="text-sm border border-gray-300 dark:border-gray-600 rounded px-3 py-1 bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 min-w-48 transition-colors"
            >
              {costAnalytics.availableServices.map((service) => (
                <option
                  key={service}
                  value={service}
                >
                  {service === 'Amazon Managed Streaming for Apache Kafka'
                    ? 'MSK Service'
                    : service === 'EC2 - Other'
                    ? 'EC2 Services'
                    : service === 'AWS Certificate Manager'
                    ? 'Certificate Manager'
                    : service}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="h-80">
          <ResponsiveContainer
            width="100%"
            height="100%"
          >
            <LineChart
              data={filteredData.chartData}
              margin={{ top: 5, right: 30, left: 60, bottom: 60 }}
            >
              <CartesianGrid strokeDasharray="3 3" />
              <XAxis
                dataKey="month"
                tick={{ fontSize: 12 }}
                angle={-45}
                textAnchor="end"
                height={60}
              />
              <YAxis
                tick={{ fontSize: 12 }}
                tickFormatter={(value) =>
                  `$${value.toLocaleString('en-US', {
                    minimumFractionDigits: 0,
                    maximumFractionDigits: 0,
                  })}`
                }
                width={50}
              />
              <Tooltip
                formatter={(value: any) => [costAnalytics.formatCurrency(value), 'Cost']}
                labelStyle={{ color: '#374151' }}
              />
              <Line
                type="monotone"
                dataKey="cost"
                stroke="#3B82F6"
                strokeWidth={3}
                dot={{ fill: '#3B82F6', strokeWidth: 2, r: 3 }}
                activeDot={{ r: 5, stroke: '#3B82F6', strokeWidth: 2 }}
              />
            </LineChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* Raw Data Table */}
      <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 mb-4">
          <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
            📋 Raw Cost Data
          </h3>
          <div className="flex items-center space-x-2">
            <span className="text-sm text-gray-600 dark:text-gray-400">Service:</span>
            <select
              value={selectedService}
              onChange={(e) => setSelectedService(e.target.value)}
              className="text-sm border border-gray-300 dark:border-gray-600 rounded px-3 py-1 bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 min-w-48 transition-colors"
            >
              {costAnalytics.availableServices.map((service) => (
                <option
                  key={service}
                  value={service}
                >
                  {service === 'Amazon Managed Streaming for Apache Kafka'
                    ? 'MSK Service'
                    : service === 'EC2 - Other'
                    ? 'EC2 Services'
                    : service === 'AWS Certificate Manager'
                    ? 'Certificate Manager'
                    : service}
                </option>
              ))}
            </select>
          </div>
        </div>

        <div className="space-y-4">
          <div className="flex items-center space-x-4">
            <span className="text-sm text-gray-600 dark:text-gray-400">Sort by:</span>
            <select
              value={sortField}
              onChange={(e) => setSortField(e.target.value as any)}
              className="text-sm border border-gray-300 dark:border-gray-600 rounded px-2 py-1 bg-white dark:bg-gray-700 text-gray-900 dark:text-gray-100 transition-colors"
            >
              <option value="date">Date</option>
              <option value="service">Service</option>
              <option value="cost">Cost</option>
              <option value="usage_type">Usage Type</option>
            </select>
            <Button
              onClick={() => setSortDirection(sortDirection === 'asc' ? 'desc' : 'asc')}
              variant="outline"
              size="sm"
            >
              {sortDirection === 'asc' ? '↑' : '↓'}
            </Button>
          </div>

          <div className="max-h-96 overflow-y-auto">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Date Range</TableHead>
                  <TableHead>Service</TableHead>
                  <TableHead>Usage Type</TableHead>
                  <TableHead className="text-right">Cost</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {filteredData.tableData
                  .sort((a, b) => {
                    let aVal: any, bVal: any
                    switch (sortField) {
                      case 'date':
                        aVal = new Date(a.time_period_start).getTime()
                        bVal = new Date(b.time_period_start).getTime()
                        break
                      case 'service':
                        aVal = a.service
                        bVal = b.service
                        break
                      case 'cost':
                        aVal = a.cost
                        bVal = b.cost
                        break
                      case 'usage_type':
                        aVal = a.usage_type
                        bVal = b.usage_type
                        break
                      default:
                        aVal = a.time_period_start
                        bVal = b.time_period_start
                    }

                    if (sortDirection === 'asc') {
                      return aVal > bVal ? 1 : -1
                    } else {
                      return aVal < bVal ? 1 : -1
                    }
                  })
                  .slice(0, 100) // Limit to first 100 entries for performance
                  .map((item, index) => (
                    <TableRow key={index}>
                      <TableCell>
                        <div className="text-sm">
                          <div>{new Date(item.time_period_start).toLocaleDateString()}</div>
                          <div className="text-xs text-gray-500">
                            to {new Date(item.time_period_end).toLocaleDateString()}
                          </div>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="text-sm font-medium">
                          {item.service
                            .replace('Amazon Elastic Compute Cloud - ', 'EC2 - ')
                            .replace(' - Other', '')}
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="text-sm text-gray-600">{item.usage_type}</div>
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="text-sm font-bold text-gray-900">
                          {costAnalytics.formatCurrency(item.cost)}
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
              </TableBody>
            </Table>
          </div>

          {filteredData.tableData.length > 100 && (
            <div className="text-sm text-gray-500 dark:text-gray-400 text-center">
              Showing first 100 entries of {filteredData.tableData.length} total cost entries
              {selectedService !== 'All Services' &&
                ` for ${
                  selectedService === 'Amazon Managed Streaming for Apache Kafka'
                    ? 'MSK Service'
                    : selectedService
                }`}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
