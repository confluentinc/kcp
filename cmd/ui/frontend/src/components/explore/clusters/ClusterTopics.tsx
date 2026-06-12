import { formatRetentionTime } from '@/lib/utils'
import type { KafkaAdminInfo, Topic } from '@/types'

interface ClusterTopicsProps {
  kafkaAdminInfo?: KafkaAdminInfo
}

export const ClusterTopics = ({ kafkaAdminInfo }: ClusterTopicsProps) => {
  if (!kafkaAdminInfo?.topics?.details) {
    return (
      <div className="bg-card rounded-lg border border-border p-6 transition-colors">
        <h3 className="text-xl font-semibold text-foreground mb-4">
          Topics Overview
        </h3>
        <p className="text-muted-foreground">
          No topic data available for this cluster.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Topic Summary */}
      <div className="bg-card rounded-lg border border-border p-6 transition-colors">
        <h3 className="text-xl font-semibold text-foreground mb-4">
          Topics Overview
        </h3>

        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
          <div className="bg-secondary rounded-lg p-4 transition-colors">
            <div className="text-2xl font-bold text-foreground">
              {kafkaAdminInfo.topics.summary.topics}
            </div>
            <div className="text-sm text-muted-foreground">Total Topics</div>
          </div>
          <div className="bg-secondary rounded-lg p-4 transition-colors">
            <div className="text-2xl font-bold text-foreground">
              {kafkaAdminInfo.topics.summary.total_partitions}
            </div>
            <div className="text-sm text-muted-foreground">Total Partitions</div>
          </div>
          <div className="bg-secondary rounded-lg p-4 transition-colors">
            <div className="text-2xl font-bold text-foreground">
              {kafkaAdminInfo.topics.summary.internal_topics}
            </div>
            <div className="text-sm text-muted-foreground">Internal Topics</div>
          </div>
          <div className="bg-secondary rounded-lg p-4 transition-colors">
            <div className="text-2xl font-bold text-foreground">
              {kafkaAdminInfo.topics.summary.compact_topics}
            </div>
            <div className="text-sm text-muted-foreground">Compact Topics</div>
          </div>
        </div>
      </div>

      {/* Topics Table */}
      <div className="bg-card rounded-lg border border-border p-6 transition-colors overflow-visible">
        <h3 className="text-xl font-semibold text-foreground mb-4">All Topics</h3>

        <div className="overflow-x-auto overflow-y-visible">
          <table className="w-full text-sm overflow-visible">
            <thead className="overflow-visible">
              <tr className="border-b border-border overflow-visible">
                <th className="text-left py-3 font-medium text-foreground">
                  Topic Name
                </th>
                <th className="text-center py-3 font-medium text-foreground">
                  Partitions
                </th>
                <th className="text-center py-3 font-medium text-foreground">
                  Replication Factor
                </th>
                <th className="text-center py-3 font-medium text-foreground">
                  Type
                </th>
                <th className="text-center py-3 font-medium text-foreground">
                  Retention (ms)
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-200 dark:divide-gray-600 overflow-visible">
              {kafkaAdminInfo.topics.details.map((topic: Topic, index: number) => (
                <tr key={index}>
                  <td className="py-3 text-foreground">
                    <div className="flex items-center">
                      <span className="font-mono text-sm">{topic.name}</span>
                      {topic.name.startsWith('__') && (
                        <span className="ml-2 px-2 py-1 text-xs bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200 rounded">
                          Internal
                        </span>
                      )}
                    </div>
                  </td>
                  <td className="py-3 text-center text-foreground">
                    {topic.partitions}
                  </td>
                  <td className="py-3 text-center text-foreground">
                    {topic.replication_factor}
                  </td>
                  <td className="py-3 text-center">
                    <span
                      className={`px-2 py-1 text-xs rounded ${
                        topic.configurations['cleanup.policy'] === 'compact'
                          ? 'bg-blue-100 text-blue-800 dark:bg-accent/20 dark:text-accent'
                          : 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
                      }`}
                    >
                      {topic.configurations['cleanup.policy'] === 'compact' ? 'Compact' : 'Delete'}
                    </span>
                  </td>
                  <td className="py-3 text-center text-foreground">
                    <div className="relative group">
                      <span className="font-mono text-xs cursor-help">
                        {topic.configurations['retention.ms'] === '-1'
                          ? '∞'
                          : parseInt(topic.configurations['retention.ms']).toLocaleString()}
                      </span>
                      {topic.configurations['retention.ms'] !== '-1' && (
                        <div className="absolute top-full left-1/2 transform -translate-x-1/2 mt-2 px-3 py-2 bg-gray-900 dark:bg-card text-white text-xs rounded-lg shadow-lg opacity-0 group-hover:opacity-100 transition-opacity duration-200 pointer-events-none whitespace-nowrap z-[9999]">
                          <div className="text-left">
                            <div>
                              {formatRetentionTime(
                                topic.configurations['retention.ms']
                              ).seconds.toLocaleString()}{' '}
                              seconds
                            </div>
                            <div>
                              {formatRetentionTime(
                                topic.configurations['retention.ms']
                              ).minutes.toLocaleString()}{' '}
                              minutes
                            </div>
                            <div>
                              {formatRetentionTime(
                                topic.configurations['retention.ms']
                              ).hours.toLocaleString()}{' '}
                              hours
                            </div>
                            <div>
                              {formatRetentionTime(
                                topic.configurations['retention.ms']
                              ).days.toLocaleString()}{' '}
                              days
                            </div>
                          </div>
                          <div className="absolute bottom-full left-1/2 transform -translate-x-1/2 w-0 h-0 border-l-4 border-r-4 border-b-4 border-transparent border-b-gray-900 dark:border-b-[#4A4956]"></div>
                        </div>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <div className="mt-4 text-sm text-muted-foreground text-center">
          Showing all {kafkaAdminInfo.topics.details.length} topics
        </div>
      </div>
    </div>
  )
}

