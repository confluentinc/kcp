import type { RegionBreakdown } from '@/lib/costAggregationUtils'

interface SummaryRegionTableProps {
  regionBreakdown: RegionBreakdown[]
}

const formatCurrencyDetailed = (amount: number) =>
  new Intl.NumberFormat('en-US', {
    style: 'currency',
    currency: 'USD',
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(amount)

export const SummaryRegionTable = ({ regionBreakdown }: SummaryRegionTableProps) => {
  return (
    <div className="w-full">
      <div className="bg-white dark:bg-card rounded-xl p-6 shadow-lg border border-gray-200 dark:border-border">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-6">
          MSK Cost by Region
        </h3>
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead>
              <tr className="border-b border-gray-200 dark:border-border">
                <th className="text-left py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                  Region
                </th>
                <th className="text-right py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                  Unblended Cost
                </th>
                <th className="text-right py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                  Blended Cost
                </th>
                <th className="text-right py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                  Amortized Cost
                </th>
                <th className="text-right py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                  Net Amortized Cost
                </th>
                <th className="text-right py-3 px-2 text-sm font-medium text-gray-700 dark:text-gray-300">
                  Net Unblended Cost
                </th>
              </tr>
            </thead>
            <tbody>
              {regionBreakdown.map((region) => (
                <tr
                  key={region.region}
                  className="border-b border-gray-100 dark:border-border/50"
                >
                  <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 font-medium">
                    {region.region}
                  </td>
                  <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 text-right font-mono">
                    {formatCurrencyDetailed(region.unblended_cost)}
                  </td>
                  <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 text-right font-mono">
                    {formatCurrencyDetailed(region.blended_cost)}
                  </td>
                  <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 text-right font-mono">
                    {formatCurrencyDetailed(region.amortized_cost)}
                  </td>
                  <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 text-right font-mono">
                    {formatCurrencyDetailed(region.net_amortized_cost)}
                  </td>
                  <td className="py-3 px-2 text-sm text-gray-900 dark:text-gray-100 text-right font-mono">
                    {formatCurrencyDetailed(region.net_unblended_cost)}
                  </td>
                </tr>
              ))}
              {/* Total Row */}
              <tr className="border-t-2 border-gray-300 dark:border-border bg-gray-50 dark:bg-card/50">
                <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100">
                  Total
                </td>
                <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100 text-right font-mono">
                  {formatCurrencyDetailed(
                    regionBreakdown.reduce((sum, region) => sum + region.unblended_cost, 0)
                  )}
                </td>
                <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100 text-right font-mono">
                  {formatCurrencyDetailed(
                    regionBreakdown.reduce((sum, region) => sum + region.blended_cost, 0)
                  )}
                </td>
                <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100 text-right font-mono">
                  {formatCurrencyDetailed(
                    regionBreakdown.reduce((sum, region) => sum + region.amortized_cost, 0)
                  )}
                </td>
                <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100 text-right font-mono">
                  {formatCurrencyDetailed(
                    regionBreakdown.reduce((sum, region) => sum + region.net_amortized_cost, 0)
                  )}
                </td>
                <td className="py-3 px-2 text-sm font-bold text-gray-900 dark:text-gray-100 text-right font-mono">
                  {formatCurrencyDetailed(
                    regionBreakdown.reduce((sum, region) => sum + region.net_unblended_cost, 0)
                  )}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}

