import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/common/ui/table'

interface ProcessedData {
  tableData: Array<{
    metric: string
    values: (number | null)[]
  }>
  uniqueDates: string[]
}

interface MetricsTableTabProps {
  processedData: ProcessedData
  metricsResponse: {
    aggregates?: Record<string, { min?: number; avg?: number; max?: number }>
  }
}

export const MetricsTableTab = ({ processedData, metricsResponse }: MetricsTableTabProps) => {
  return (
    <div className="space-y-4 min-w-0">
      <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border min-w-0 max-w-full">
        <div className="w-full overflow-hidden rounded-lg">
          <div className="overflow-x-auto max-h-96 overflow-y-auto">
            <Table className="min-w-full">
              <TableHeader>
                <TableRow>
                  <TableHead className="sticky left-0 bg-white dark:bg-card z-10 w-[200px] max-w-[200px] border-r border-gray-200 dark:border-border">
                    Metric
                  </TableHead>
                  <TableHead className="text-center w-[100px] min-w-[100px] max-w-[100px] border-r border-gray-200 dark:border-border">
                    <div className="text-blue-600 dark:text-accent font-semibold">Min</div>
                  </TableHead>
                  <TableHead className="text-center w-[100px] min-w-[100px] max-w-[100px] border-r border-gray-200 dark:border-border">
                    <div className="text-green-600 dark:text-green-400 font-semibold">Avg</div>
                  </TableHead>
                  <TableHead className="text-center w-[100px] min-w-[100px] max-w-[100px] border-r border-gray-200 dark:border-border">
                    <div className="text-red-600 dark:text-red-400 font-semibold">Max</div>
                  </TableHead>
                  {processedData.uniqueDates.map((date, index) => (
                    <TableHead
                      key={index}
                      className="text-center w-[120px] min-w-[120px] max-w-[120px] border-r border-gray-200 dark:border-border"
                    >
                      <div className="truncate">{date}</div>
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {processedData.tableData.map((row, rowIndex) => {
                  // Get aggregate data for this metric
                  const cleanMetricName = row.metric.replace('Cluster Aggregate - ', '')
                  const metricAggregate = metricsResponse?.aggregates?.[cleanMetricName]

                  return (
                    <TableRow
                      key={rowIndex}
                      className="hover:bg-gray-50 dark:hover:bg-gray-700"
                    >
                      <TableCell className="sticky left-0 bg-white dark:bg-card z-10 font-medium border-r border-gray-200 dark:border-border w-[200px] max-w-[200px]">
                        <div
                          className="truncate pr-2"
                          title={row.metric}
                        >
                          {cleanMetricName}
                        </div>
                      </TableCell>

                      {/* Min column */}
                      <TableCell className="text-center border-r border-gray-200 dark:border-border w-[100px] min-w-[100px] max-w-[100px]">
                        <div className="font-mono text-sm truncate text-blue-600 dark:text-accent font-semibold">
                          {metricAggregate?.min?.toFixed(2) ?? '-'}
                        </div>
                      </TableCell>

                      {/* Avg column */}
                      <TableCell className="text-center border-r border-gray-200 dark:border-border w-[100px] min-w-[100px] max-w-[100px]">
                        <div className="font-mono text-sm truncate text-green-600 dark:text-green-400 font-semibold">
                          {metricAggregate?.avg?.toFixed(2) ?? '-'}
                        </div>
                      </TableCell>

                      {/* Max column */}
                      <TableCell className="text-center border-r border-gray-200 dark:border-border w-[100px] min-w-[100px] max-w-[100px]">
                        <div className="font-mono text-sm truncate text-red-600 dark:text-red-400 font-semibold">
                          {metricAggregate?.max?.toFixed(2) ?? '-'}
                        </div>
                      </TableCell>

                      {/* Existing date columns */}
                      {row.values.map((value, valueIndex) => (
                        <TableCell
                          key={valueIndex}
                          className="text-center border-r border-gray-200 dark:border-border w-[120px] min-w-[120px] max-w-[120px]"
                        >
                          <div className="font-mono text-sm truncate">
                            {value !== null ? value.toFixed(2) : '-'}
                          </div>
                        </TableCell>
                      ))}
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        </div>
      </div>
    </div>
  )
}

