import { useMemo, useState } from 'react'
import type { MirrorTopic } from '@/types/api'

interface LagMonitorTableProps {
  mirrorTopics: MirrorTopic[]
  lagHistory: Map<string, number[]>
}

// Simple sparkline component
const Sparkline = ({ data }: { data: number[] }) => {
  if (data.length === 0) {
    return (
      <div className="w-32 h-8 flex items-center justify-center text-xs text-gray-400 dark:text-gray-500">
        No data
      </div>
    )
  }

  const max = Math.max(...data, 1)
  const min = Math.min(...data, 0)
  const range = max - min || 1
  const width = 128
  const height = 32
  const padding = 2

  const points = data
    .map((value, index) => {
      const x = (index / Math.max(data.length - 1, 1)) * (width - padding * 2) + padding
      const y = height - padding - ((value - min) / range) * (height - padding * 2)
      return `${x},${y}`
    })
    .join(' ')

  return (
    <svg
      width={width}
      height={height}
      className="inline-block"
    >
      <polyline
        points={points}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        className="text-blue-500 dark:text-blue-400"
      />
    </svg>
  )
}

export const LagMonitorTable = ({ mirrorTopics, lagHistory }: LagMonitorTableProps) => {
  const [expandedTopics, setExpandedTopics] = useState<Set<string>>(new Set())

  const toggleExpand = (topicName: string) => {
    const newExpanded = new Set(expandedTopics)
    if (newExpanded.has(topicName)) {
      newExpanded.delete(topicName)
    } else {
      newExpanded.add(topicName)
    }
    setExpandedTopics(newExpanded)
  }

  const sortedTopics = useMemo(() => {
    const sorted = [...mirrorTopics].sort((a, b) => {
      // First, sort by status: ACTIVE topics first
      const isAActive = a.mirror_status === 'ACTIVE'
      const isBActive = b.mirror_status === 'ACTIVE'

      if (isAActive && !isBActive) {
        return -1
      }
      if (!isAActive && isBActive) {
        return 1
      }

      // If both have same status (both ACTIVE or both not ACTIVE), sort alphabetically by name
      return a.mirror_topic_name.localeCompare(b.mirror_topic_name)
    })

    return sorted
  }, [mirrorTopics])

  const getStatusColor = (status: string) => {
    switch (status.toUpperCase()) {
      case 'ACTIVE':
        return 'text-green-700 dark:text-green-400 bg-green-50 dark:bg-green-900/20 border-green-200 dark:border-green-800'
      case 'PAUSED':
        return 'text-yellow-700 dark:text-yellow-400 bg-yellow-50 dark:bg-yellow-900/20 border-yellow-200 dark:border-yellow-800'
      case 'FAILED':
        return 'text-red-700 dark:text-red-400 bg-red-50 dark:bg-red-900/20 border-red-200 dark:border-red-800'
      default:
        return 'text-gray-700 dark:text-gray-400 bg-gray-50 dark:bg-gray-900/20 border-gray-200 dark:border-gray-800'
    }
  }

  if (mirrorTopics.length === 0) {
    return (
      <div className="max-w-7xl mx-auto p-6">
        <div className="bg-gray-50 dark:bg-gray-800/50 border border-gray-200 dark:border-gray-700 rounded-lg p-8 text-center">
          <span className="text-4xl mb-4 block">📊</span>
          <h3 className="text-lg font-medium text-gray-900 dark:text-gray-100 mb-2">
            No Mirror Topics Found
          </h3>
          <p className="text-gray-600 dark:text-gray-400">
            No cluster link mirror topics are currently configured for this cluster link.
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="max-w-7xl mx-auto p-6">
      <div className="bg-white dark:bg-card border border-gray-200 dark:border-border rounded-lg overflow-hidden">
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-gray-200 dark:divide-border">
            <thead className="bg-gray-50 dark:bg-gray-800/50">
              <tr>
                <th className="w-10 px-6 py-3 text-left"></th>
                <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">
                  Topic Name
                </th>
                <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">
                  Status
                </th>
                <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">
                  Total Lag
                </th>
                <th className="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">
                  Lag Trend
                </th>
              </tr>
            </thead>
            <tbody className="bg-white dark:bg-card divide-y divide-gray-200 dark:divide-border">
              {sortedTopics.map((topic) => {
                const totalLag = topic.mirror_lags.reduce((sum, l) => sum + l.lag, 0)
                const isExpanded = expandedTopics.has(topic.mirror_topic_name)

                return (
                  <>
                    <tr
                      key={topic.mirror_topic_name}
                      className="hover:bg-gray-50 dark:hover:bg-gray-800/30"
                    >
                      <td className="px-6 py-2 whitespace-nowrap">
                        <button
                          onClick={() => toggleExpand(topic.mirror_topic_name)}
                          className="text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200 transition-transform duration-200"
                          style={{ transform: isExpanded ? 'rotate(90deg)' : 'rotate(0deg)' }}
                        >
                          ▶
                        </button>
                      </td>
                      <td className="px-6 py-2 text-sm font-medium text-gray-900 dark:text-gray-100">
                        {topic.mirror_topic_name}
                      </td>
                      <td className="px-6 py-2 whitespace-nowrap">
                        <span
                          className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium border ${getStatusColor(
                            topic.mirror_status
                          )}`}
                        >
                          {topic.mirror_status}
                        </span>
                      </td>
                      <td className="px-6 py-2 whitespace-nowrap text-sm">
                        {totalLag > 0 ? (
                          <span className="font-medium text-orange-600 dark:text-orange-400">
                            {totalLag.toLocaleString()}
                          </span>
                        ) : (
                          <span className="text-green-600 dark:text-green-400">0</span>
                        )}
                      </td>
                      <td className="px-6 py-2 whitespace-nowrap">
                        <Sparkline data={lagHistory.get(topic.mirror_topic_name) || []} />
                      </td>
                    </tr>
                    {isExpanded && (
                      <tr key={`${topic.mirror_topic_name}-details`}>
                        <td
                          colSpan={5}
                          className="px-6 py-0 bg-gray-50 dark:bg-gray-800/30"
                        >
                          <div className="py-3">
                            <table className="min-w-full">
                              <thead>
                                <tr className="border-b border-gray-200 dark:border-gray-700">
                                  <th className="px-4 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                                    Partition
                                  </th>
                                  <th className="px-4 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                                    Lag
                                  </th>
                                  <th className="px-4 py-2 text-left text-xs font-medium text-gray-500 dark:text-gray-400">
                                    Last Source Fetch Offset
                                  </th>
                                </tr>
                              </thead>
                              <tbody>
                                {[...topic.mirror_lags]
                                  .sort((a, b) => {
                                    // Sort by lag descending, then by partition ascending
                                    if (b.lag !== a.lag) {
                                      return b.lag - a.lag
                                    }
                                    return a.partition - b.partition
                                  })
                                  .map((lagInfo) => (
                                    <tr
                                      key={lagInfo.partition}
                                      className="border-b border-gray-100 dark:border-gray-700/50 last:border-0"
                                    >
                                      <td className="px-4 py-2 text-sm text-gray-700 dark:text-gray-300">
                                        {lagInfo.partition}
                                      </td>
                                      <td className="px-4 py-2 text-sm">
                                        {lagInfo.lag > 0 ? (
                                          <span className="font-medium text-orange-600 dark:text-orange-400">
                                            {lagInfo.lag.toLocaleString()}
                                          </span>
                                        ) : (
                                          <span className="text-green-600 dark:text-green-400">
                                            0
                                          </span>
                                        )}
                                      </td>
                                      <td className="px-4 py-2 text-sm text-gray-700 dark:text-gray-300">
                                        {lagInfo.last_source_fetch_offset.toLocaleString()}
                                      </td>
                                    </tr>
                                  ))}
                              </tbody>
                            </table>
                          </div>
                        </td>
                      </tr>
                    )}
                  </>
                )
              })}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
