import { CalendarIcon, X } from 'lucide-react'
import { format } from 'date-fns'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { cn } from '@/lib/utils'

interface DateRangePickerProps {
  label?: string
  startDate: Date | undefined
  endDate: Date | undefined
  onStartDateChange: (date: Date | undefined) => void
  onEndDateChange: (date: Date | undefined) => void
  onResetStartDate?: () => void
  onResetEndDate?: () => void
  onResetBoth?: () => void
  showResetButtons?: boolean
  showResetBothButton?: boolean
  className?: string
}

export default function DateRangePicker({
  label,
  startDate,
  endDate,
  onStartDateChange,
  onEndDateChange,
  onResetStartDate,
  onResetEndDate,
  onResetBoth,
  showResetButtons = true,
  showResetBothButton = false,
  className = '',
}: DateRangePickerProps) {
  return (
    <div className={cn('flex flex-col sm:flex-row gap-4', className)}>
      {/* Start Date Picker */}
      <div className="flex flex-col space-y-2">
        {label && (
          <label className="text-sm font-medium text-gray-700 dark:text-gray-300">Start Date</label>
        )}
        <div className="relative">
          <Popover>
            <PopoverTrigger asChild>
              <Button
                variant="outline"
                className={cn(
                  'w-[240px] justify-start text-left font-normal pr-10',
                  !startDate && 'text-muted-foreground'
                )}
              >
                <CalendarIcon className="mr-2 h-4 w-4" />
                {startDate ? format(startDate, 'PPP') : 'Pick a start date'}
              </Button>
            </PopoverTrigger>
            <PopoverContent
              className="w-auto p-0"
              align="start"
            >
              <Calendar
                mode="single"
                selected={startDate}
                onSelect={onStartDateChange}
              />
            </PopoverContent>
          </Popover>
          {showResetButtons && startDate && onResetStartDate && (
            <Button
              variant="ghost"
              size="sm"
              className="absolute right-2 top-1/2 -translate-y-1/2 h-7 w-7 p-0 z-10 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700 shadow-sm"
              onClick={(e) => {
                e.preventDefault()
                e.stopPropagation()
                onResetStartDate()
              }}
              title="Reset to default start date"
            >
              <X className="h-3 w-3 text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200" />
            </Button>
          )}
        </div>
      </div>

      {/* End Date Picker */}
      <div className="flex flex-col space-y-2">
        {label && (
          <label className="text-sm font-medium text-gray-700 dark:text-gray-300">End Date</label>
        )}
        <div className="relative">
          <Popover>
            <PopoverTrigger asChild>
              <Button
                variant="outline"
                className={cn(
                  'w-[240px] justify-start text-left font-normal pr-10',
                  !endDate && 'text-muted-foreground'
                )}
              >
                <CalendarIcon className="mr-2 h-4 w-4" />
                {endDate ? format(endDate, 'PPP') : 'Pick an end date'}
              </Button>
            </PopoverTrigger>
            <PopoverContent
              className="w-auto p-0"
              align="start"
            >
              <Calendar
                mode="single"
                selected={endDate}
                onSelect={onEndDateChange}
              />
            </PopoverContent>
          </Popover>
          {showResetButtons && endDate && onResetEndDate && (
            <Button
              variant="ghost"
              size="sm"
              className="absolute right-2 top-1/2 -translate-y-1/2 h-7 w-7 p-0 z-10 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-600 hover:bg-gray-100 dark:hover:bg-gray-700 shadow-sm"
              onClick={(e) => {
                e.preventDefault()
                e.stopPropagation()
                onResetEndDate()
              }}
              title="Reset to default end date"
            >
              <X className="h-3 w-3 text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200" />
            </Button>
          )}
        </div>
      </div>

      {/* Reset Both Button */}
      {showResetBothButton && startDate && endDate && onResetBoth && (
        <div className="flex flex-col justify-end">
          <Button
            variant="outline"
            onClick={onResetBoth}
            className="w-full sm:w-auto"
          >
            Reset
          </Button>
        </div>
      )}
    </div>
  )
}
