import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/common/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/common/ui/table'
import { formatCostTypeLabel } from '@/lib/costTypeUtils'

interface ProcessedData {
  filteredTableData: Array<{
    service: string
    usageType: string
    values: number[]
    total: number
  }>
  uniqueDates: string[]
  services: string[]
}

interface RegionCostsTableTabProps {
  processedData: ProcessedData
  selectedCostType: string
  selectedTableService: string
  setSelectedTableService: (service: string) => void
}

export const RegionCostsTableTab = ({
  processedData,
  selectedCostType,
  selectedTableService,
  setSelectedTableService,
}: RegionCostsTableTabProps) => {
  return (
    <>
      {/* Service Filter for Table */}
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-4">
          <label className="text-sm font-medium text-gray-700 dark:text-gray-300">
            Filter by Service:
          </label>
          <Select
            value={selectedTableService}
            onValueChange={setSelectedTableService}
          >
            <SelectTrigger className="w-[300px]">
              <SelectValue placeholder="Choose a service to filter" />
            </SelectTrigger>
            <SelectContent>
              {processedData.services.map((service) => (
                <SelectItem
                  key={service}
                  value={service}
                >
                  {service}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>

        <div className="flex items-center gap-6">
          <div className="flex items-center gap-2">
            <span className="text-sm text-gray-500 dark:text-gray-400">
              Total ({formatCostTypeLabel(selectedCostType)}):
            </span>
            <span className="text-lg font-bold text-green-600 dark:text-green-400">
              $
              {(
                processedData.filteredTableData?.reduce((sum, row) => {
                  return sum + (row.total || 0)
                }, 0) || 0
              ).toFixed(2)}
            </span>
          </div>
        </div>
      </div>

      <div className="bg-white dark:bg-card rounded-lg border border-gray-200 dark:border-border min-w-0 max-w-full">
        <div className="w-full overflow-hidden rounded-lg">
          <div className="overflow-x-auto max-h-96 overflow-y-auto">
            <Table className="min-w-full">
              <TableHeader>
                <TableRow>
                  <TableHead className="sticky left-0 bg-white dark:bg-card z-10 w-[150px] max-w-[150px] border-r border-gray-200 dark:border-border">
                    Service
                  </TableHead>
                  <TableHead className="sticky left-[150px] bg-white dark:bg-card z-10 w-[250px] max-w-[250px] border-r border-gray-200 dark:border-border">
                    Usage Type
                  </TableHead>
                  <TableHead className="text-center w-[120px] min-w-[120px] max-w-[120px] border-r border-gray-200 dark:border-border">
                    <div className="text-green-600 dark:text-green-400 font-semibold">
                      Total ({formatCostTypeLabel(selectedCostType)})
                    </div>
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
                {(processedData.filteredTableData || []).map((row, rowIndex) => (
                  <TableRow
                    key={rowIndex}
                    className="hover:bg-gray-50 dark:hover:bg-gray-700"
                  >
                    <TableCell className="sticky left-0 bg-white dark:bg-card z-10 font-medium border-r border-gray-200 dark:border-border w-[150px] max-w-[150px]">
                      <div
                        className="truncate pr-2"
                        title={row.service}
                      >
                        {row.service}
                      </div>
                    </TableCell>

                    <TableCell className="sticky left-[150px] bg-white dark:bg-card z-10 border-r border-gray-200 dark:border-border w-[250px] max-w-[250px]">
                      <div
                        className="truncate pr-2 text-sm"
                        title={row.usageType}
                      >
                        {row.usageType}
                      </div>
                    </TableCell>

                    {/* Total column */}
                    <TableCell className="text-center border-r border-gray-200 dark:border-border w-[120px] min-w-[120px] max-w-[120px]">
                      <div className="font-mono text-sm truncate text-green-600 dark:text-green-400 font-semibold">
                        ${row.total.toFixed(2)}
                      </div>
                    </TableCell>

                    {/* Daily cost columns */}
                    {row.values.map((value: number, valueIndex: number) => (
                      <TableCell
                        key={valueIndex}
                        className="text-center border-r border-gray-200 dark:border-border w-[120px] min-w-[120px] max-w-[120px]"
                      >
                        <div className="font-mono text-sm truncate">
                          ${value.toFixed(2)}
                        </div>
                      </TableCell>
                    ))}
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </div>
      </div>
    </>
  )
}


