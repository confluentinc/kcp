import { useState } from 'react'
import { Filter } from 'lucide-react'
import { Tabs } from '@/components/common/Tabs'
import { Button } from '@/components/common/ui/button'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/common/ui/popover'
import { formatRetentionTime, parseCleanupPolicies } from '@/lib/utils'
import type { KafkaAdminInfo, Topic } from '@/types'

interface ClusterTopicsProps {
  kafkaAdminInfo?: KafkaAdminInfo
}

type TopicScope = 'non-internal' | 'internal'

const TOPIC_SCOPE_TABS = [
  { id: 'non-internal', label: 'Non-Internal' },
  { id: 'internal', label: 'Internal' },
]

export const ClusterTopics = ({ kafkaAdminInfo }: ClusterTopicsProps) => {
  const [topicScope, setTopicScope] = useState<TopicScope>('non-internal')
  // All Topics table filter — independent of the summary tabs above, and
  // additive (both checked shows everything, both unchecked shows nothing).
  const [showNonInternal, setShowNonInternal] = useState(true)
  const [showInternal, setShowInternal] = useState(true)
  const [showCompact, setShowCompact] = useState(true)
  const [showDelete, setShowDelete] = useState(true)

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

  const summary = kafkaAdminInfo.topics.summary

  // topics/total_partitions/compact_topics/compact_partitions only ever count
  // non-internal (not "__"-prefixed) topics, despite the plain names — the
  // internal counterparts are separate fields (internal_topics/
  // total_internal_partitions/etc). Scoped here by tab so only one set of 4
  // stats shows at a time, each labeled unambiguously either way.
  const scopedStats =
    topicScope === 'non-internal'
      ? [
          { value: summary.topics, label: 'Non-Internal Topics' },
          { value: summary.compact_topics, label: 'Compact Non-Internal Topics' },
          { value: summary.total_partitions, label: 'Non-Internal Partitions' },
          { value: summary.compact_partitions, label: 'Compact Non-Internal Partitions' },
        ]
      : [
          { value: summary.internal_topics, label: 'Internal Topics' },
          { value: summary.compact_internal_topics, label: 'Compact Internal Topics' },
          { value: summary.total_internal_partitions, label: 'Internal Partitions' },
          { value: summary.compact_internal_partitions, label: 'Compact Internal Partitions' },
        ]

  const filteredTopics = kafkaAdminInfo.topics.details.filter((topic) => {
    const scopeMatches = topic.name.startsWith('__') ? showInternal : showNonInternal
    // A topic can carry both policies at once (e.g. "compact,delete" — see
    // parseCleanupPolicies) — it matches if it has ANY checked policy, not
    // only if it has exactly the checked set.
    const policies = parseCleanupPolicies(topic.configurations['cleanup.policy'])
    const cleanupMatches =
      (showCompact && policies.includes('compact')) || (showDelete && policies.includes('delete'))
    return scopeMatches && cleanupMatches
  })

  return (
    <div className="space-y-6">
      {/* Topic Summary */}
      <div className="bg-card rounded-lg border border-border transition-colors overflow-hidden">
        <h3 className="text-xl font-semibold text-foreground px-6 pt-6">
          Topics Overview
        </h3>

        <Tabs
          tabs={TOPIC_SCOPE_TABS}
          activeId={topicScope}
          onChange={(id) => setTopicScope(id as TopicScope)}
        />

        <div className="grid grid-cols-2 md:grid-cols-4 gap-4 p-6">
          {scopedStats.map((stat) => (
            <div
              key={stat.label}
              className="bg-secondary rounded-lg p-4 transition-colors"
            >
              <div className="text-2xl font-bold text-foreground">{stat.value}</div>
              <div className="text-sm text-muted-foreground">{stat.label}</div>
            </div>
          ))}
        </div>
      </div>

      {/* Topics Table */}
      <div className="bg-card rounded-lg border border-border p-6 transition-colors overflow-visible">
        <div className="flex items-center justify-between mb-4">
          <h3 className="text-xl font-semibold text-foreground">All Topics</h3>

          <Popover>
            <PopoverTrigger asChild>
              <Button
                variant="outline"
                size="icon"
                aria-label="Filter topics"
              >
                <Filter className="h-4 w-4" />
              </Button>
            </PopoverTrigger>
            <PopoverContent
              align="end"
              className="w-52 p-3"
            >
              <div className="space-y-3">
                <div>
                  <div className="text-xs font-medium text-muted-foreground mb-1.5">Type</div>
                  <div className="space-y-2">
                    <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
                      <input
                        type="checkbox"
                        checked={showNonInternal}
                        onChange={(e) => setShowNonInternal(e.target.checked)}
                        className="h-4 w-4 rounded border-border accent-accent"
                      />
                      Non-Internal
                    </label>
                    <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
                      <input
                        type="checkbox"
                        checked={showInternal}
                        onChange={(e) => setShowInternal(e.target.checked)}
                        className="h-4 w-4 rounded border-border accent-accent"
                      />
                      Internal
                    </label>
                  </div>
                </div>

                <div className="border-t border-border pt-3">
                  <div className="text-xs font-medium text-muted-foreground mb-1.5">
                    Cleanup Policy
                  </div>
                  <div className="space-y-2">
                    <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
                      <input
                        type="checkbox"
                        checked={showCompact}
                        onChange={(e) => setShowCompact(e.target.checked)}
                        className="h-4 w-4 rounded border-border accent-accent"
                      />
                      Compact
                    </label>
                    <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer">
                      <input
                        type="checkbox"
                        checked={showDelete}
                        onChange={(e) => setShowDelete(e.target.checked)}
                        className="h-4 w-4 rounded border-border accent-accent"
                      />
                      Delete
                    </label>
                  </div>
                </div>
              </div>
            </PopoverContent>
          </Popover>
        </div>

        <div className="overflow-x-auto overflow-y-visible">
          <table className="w-full text-sm overflow-visible">
            <thead className="overflow-visible">
              <tr className="border-b border-border overflow-visible">
                <th className="text-left py-3 font-medium text-foreground">
                  Topic Name
                </th>
                <th className="text-center py-3 font-medium text-foreground">
                  Type
                </th>
                <th className="text-center py-3 font-medium text-foreground">
                  Partitions
                </th>
                <th className="text-center py-3 font-medium text-foreground">
                  Replication Factor
                </th>
                <th className="text-center py-3 font-medium text-foreground">
                  Cleanup Policy
                </th>
                <th className="text-center py-3 font-medium text-foreground">
                  Retention (ms)
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-200 dark:divide-gray-600 overflow-visible">
              {filteredTopics.map((topic: Topic, index: number) => (
                <tr key={index}>
                  <td className="py-3 text-foreground">
                    <span className="font-mono text-sm">{topic.name}</span>
                  </td>
                  <td className="py-3 text-center">
                    {topic.name.startsWith('__') ? (
                      <span className="px-2 py-1 text-xs bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200 rounded">
                        Internal
                      </span>
                    ) : (
                      <span className="px-2 py-1 text-xs bg-gray-100 text-gray-800 dark:bg-gray-800 dark:text-gray-200 rounded">
                        Non-Internal
                      </span>
                    )}
                  </td>
                  <td className="py-3 text-center text-foreground">
                    {topic.partitions}
                  </td>
                  <td className="py-3 text-center text-foreground">
                    {topic.replication_factor}
                  </td>
                  <td className="py-3 text-center">
                    <div className="flex items-center justify-center gap-1">
                      {parseCleanupPolicies(topic.configurations['cleanup.policy']).includes(
                        'compact'
                      ) && (
                        <span className="px-2 py-1 text-xs rounded bg-blue-100 text-blue-800 dark:bg-accent/20 dark:text-accent">
                          Compact
                        </span>
                      )}
                      {parseCleanupPolicies(topic.configurations['cleanup.policy']).includes(
                        'delete'
                      ) && (
                        <span className="px-2 py-1 text-xs rounded bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200">
                          Delete
                        </span>
                      )}
                    </div>
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
          {filteredTopics.length === kafkaAdminInfo.topics.details.length
            ? `Showing all ${kafkaAdminInfo.topics.details.length} topics`
            : `Showing ${filteredTopics.length} of ${kafkaAdminInfo.topics.details.length} topics`}
        </div>
      </div>
    </div>
  )
}

