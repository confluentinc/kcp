import type { ChartDataPoint } from '@/components/common/DateRangeChart'

/**
 * Reduces chart data to at most maxPoints entries using every-nth-point sampling.
 * Always preserves the first and last data points.
 * Returns the original array unchanged if it has fewer than maxPoints entries.
 */
export function downsampleChartData(
  data: ChartDataPoint[],
  maxPoints: number = 150
): ChartDataPoint[] {
  if (data.length <= maxPoints) return data

  const result: ChartDataPoint[] = [data[0]]
  const step = (data.length - 1) / (maxPoints - 1)

  for (let i = 1; i < maxPoints - 1; i++) {
    const index = Math.round(i * step)
    result.push(data[index])
  }

  result.push(data[data.length - 1])
  return result
}
