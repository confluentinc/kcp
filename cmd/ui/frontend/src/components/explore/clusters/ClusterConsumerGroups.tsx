import { Fragment, useState } from 'react'
import type { KafkaAdminInfo } from '@/types'

interface ClusterConsumerGroupsProps {
  kafkaAdminInfo?: KafkaAdminInfo
}

const badgeBase =
  'inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium'

// Soft, light palette: light blue / green / grey / yellow. One hue per group
// type; states reuse the same soft tones (green=healthy, yellow=transitional,
// grey=idle/gone).
const typeColors: Record<string, string> = {
  classic: 'bg-blue-50 text-blue-700 dark:bg-blue-900/40 dark:text-blue-200',
  consumer: 'bg-green-50 text-green-700 dark:bg-green-900/40 dark:text-green-200',
  share: 'bg-gray-100 text-gray-600 dark:bg-gray-700/40 dark:text-gray-300',
  streams: 'bg-yellow-50 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-200',
}

const stateColors: Record<string, string> = {
  Stable: 'bg-green-50 text-green-700 dark:bg-green-900/40 dark:text-green-200',
  Empty: 'bg-gray-100 text-gray-600 dark:bg-gray-700/40 dark:text-gray-300',
  Dead: 'bg-gray-100 text-gray-500 dark:bg-gray-800/40 dark:text-gray-400',
  PreparingRebalance:
    'bg-yellow-50 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-200',
  CompletingRebalance:
    'bg-yellow-50 text-yellow-700 dark:bg-yellow-900/40 dark:text-yellow-200',
}

const fallbackColor = 'bg-gray-100 text-gray-600 dark:bg-gray-700/40 dark:text-gray-300'

const labelFor = (value: string) => (value === '' ? 'unknown' : value)

const TypeBadge = ({ type }: { type: string }) => (
  <span className={`${badgeBase} ${typeColors[type] || fallbackColor}`}>
    {labelFor(type)}
  </span>
)

const StateBadge = ({ state }: { state: string }) => (
  <span className={`${badgeBase} ${stateColors[state] || fallbackColor}`}>
    {labelFor(state)}
  </span>
)

export const ClusterConsumerGroups = ({ kafkaAdminInfo }: ClusterConsumerGroupsProps) => {
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const [showTypes, setShowTypes] = useState(false)

  const consumerGroups = kafkaAdminInfo?.consumer_groups

  if (!consumerGroups || !consumerGroups.details || consumerGroups.details.length === 0) {
    return (
      <div className="text-center py-12">
        <div className="text-muted-foreground text-lg">No consumer groups found</div>
        <p className="text-sm text-muted-foreground mt-2">
          This cluster doesn't have any consumer groups, or consumer group discovery
          was skipped for this scan.
        </p>
      </div>
    )
  }

  const summary = consumerGroups.summary
  const details = consumerGroups.details

  const byType = summary?.by_type ?? {}
  const byState = summary?.by_state ?? {}
  const totalGroups = summary?.total ?? details.length
  const stableGroups = byState['Stable'] ?? 0
  const deadGroups = byState['Dead'] ?? 0
  const typeStats = [
    { label: 'Classic', value: byType['classic'] ?? 0 },
    { label: 'Consumer', value: byType['consumer'] ?? 0 },
    { label: 'Share', value: byType['share'] ?? 0 },
    { label: 'Streams', value: byType['streams'] ?? 0 },
  ]

  const toggle = (groupId: string) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(groupId)) {
        next.delete(groupId)
      } else {
        next.add(groupId)
      }
      return next
    })
  }

  return (
    <div className="space-y-6">
      {/* Overview box — matches the Topics Overview styling (header + stat tiles) */}
      <div className="bg-card rounded-lg border border-border transition-colors overflow-hidden">
        <h3 className="text-xl font-semibold text-foreground px-6 pt-6">
          Consumer Groups Overview
        </h3>

        {/* Total + the two state tiles (Stable / Dead) */}
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 px-6 pt-6">
          <div className="bg-secondary rounded-lg p-4 transition-colors">
            <div className="text-2xl font-bold text-foreground">{totalGroups}</div>
            <div className="text-sm text-muted-foreground">Total Groups</div>
          </div>
          <div className="bg-secondary rounded-lg p-4 transition-colors">
            <div className="text-2xl font-bold text-foreground">{stableGroups}</div>
            <div className="text-sm text-muted-foreground">Stable Groups</div>
          </div>
          <div className="bg-secondary rounded-lg p-4 transition-colors">
            <div className="text-2xl font-bold text-foreground">{deadGroups}</div>
            <div className="text-sm text-muted-foreground">Dead Groups</div>
          </div>
        </div>

        {/* Expandable per-type breakdown (same stat-tile styling, no badges) */}
        <div className="px-6 pb-6 pt-4">
          <button
            type="button"
            onClick={() => setShowTypes((v) => !v)}
            className="inline-flex items-center gap-1 text-sm font-medium text-foreground hover:text-muted-foreground transition-colors"
            aria-expanded={showTypes}
          >
            <span>{showTypes ? '▾' : '▸'}</span>
            {showTypes ? 'Hide group types' : 'Show group types'}
          </button>
          {showTypes && (
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mt-4">
              {typeStats.map((s) => (
                <div key={s.label} className="bg-secondary rounded-lg p-4 transition-colors">
                  <div className="text-2xl font-bold text-foreground">{s.value}</div>
                  <div className="text-sm text-muted-foreground">{s.label}</div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>

      <h3 className="text-lg font-semibold text-foreground">Groups</h3>

      <div className="overflow-x-auto">
        <table className="w-full border border-border rounded-lg">
          <thead>
            <tr className="bg-secondary">
              <th className="w-8 px-4 py-3 border-b border-border" />
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-border">
                Group ID
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                Type
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                State
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                Protocol
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                Coordinator
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                Members
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                Topics
              </th>
            </tr>
          </thead>
          <tbody>
            {details.map((group) => {
              const isOpen = expanded.has(group.group_id)
              return (
                <Fragment key={group.group_id}>
                  <tr
                    className="hover:bg-secondary transition-colors cursor-pointer"
                    onClick={() => toggle(group.group_id)}
                  >
                    <td className="px-4 py-3 text-sm text-muted-foreground border-b border-border align-top">
                      <span className="inline-block select-none">{isOpen ? '▾' : '▸'}</span>
                    </td>
                    <td className="px-4 py-3 text-sm text-foreground font-mono border-b border-border align-top">
                      {group.group_id}
                      {!group.detail_complete && (
                        <span
                          className="ml-2 text-xs text-yellow-600 dark:text-yellow-400"
                          title="This group type cannot be fully described by the current Kafka client; member assignments may be incomplete."
                        >
                          ⚠ partial
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-sm border-b border-l border-border align-top">
                      <TypeBadge type={group.type} />
                    </td>
                    <td className="px-4 py-3 text-sm border-b border-l border-border align-top">
                      <StateBadge state={group.state} />
                    </td>
                    <td className="px-4 py-3 text-sm text-muted-foreground border-b border-l border-border align-top">
                      {group.protocol_type || '—'}
                    </td>
                    <td className="px-4 py-3 text-sm text-muted-foreground font-mono border-b border-l border-border align-top">
                      {group.coordinator || '—'}
                    </td>
                    <td className="px-4 py-3 text-sm text-muted-foreground border-b border-l border-border align-top">
                      {group.members?.length ?? 0}
                    </td>
                    <td className="px-4 py-3 text-sm text-muted-foreground border-b border-l border-border align-top">
                      {group.topics?.length ?? 0}
                    </td>
                  </tr>
                  {isOpen && (
                    <tr className="bg-secondary/40">
                      <td />
                      <td colSpan={7} className="px-4 py-4 border-b border-border">
                        {group.topics && group.topics.length > 0 && (
                          <div className="mb-3">
                            <span className="text-xs font-medium text-foreground">Topics: </span>
                            <span className="text-xs text-muted-foreground font-mono">
                              {group.topics.join(', ')}
                            </span>
                          </div>
                        )}
                        {!group.detail_complete && (
                          <p className="mb-3 text-xs text-yellow-600 dark:text-yellow-400">
                            ⚠ Partial detail: this group's type ({labelFor(group.type)})
                            cannot be fully described by the current Kafka client, so member
                            assignments may be empty.
                          </p>
                        )}
                        {group.members && group.members.length > 0 ? (
                          <table className="w-full border border-border rounded-md">
                            <thead>
                              <tr className="bg-secondary">
                                <th className="px-3 py-2 text-left text-xs font-medium text-foreground border-b border-border">
                                  Client ID
                                </th>
                                <th className="px-3 py-2 text-left text-xs font-medium text-foreground border-b border-l border-border">
                                  Client Host
                                </th>
                                <th className="px-3 py-2 text-left text-xs font-medium text-foreground border-b border-l border-border">
                                  Group Instance ID
                                </th>
                                <th className="px-3 py-2 text-left text-xs font-medium text-foreground border-b border-l border-border">
                                  Assigned Topics
                                </th>
                              </tr>
                            </thead>
                            <tbody>
                              {group.members.map((member) => (
                                <tr key={member.member_id}>
                                  <td className="px-3 py-2 text-xs text-foreground font-mono border-b border-border">
                                    {member.client_id || '—'}
                                  </td>
                                  <td className="px-3 py-2 text-xs text-muted-foreground font-mono border-b border-l border-border">
                                    {member.client_host || '—'}
                                  </td>
                                  <td className="px-3 py-2 text-xs text-muted-foreground font-mono border-b border-l border-border">
                                    {member.group_instance_id || '—'}
                                  </td>
                                  <td className="px-3 py-2 text-xs text-muted-foreground border-b border-l border-border">
                                    {member.assigned_topics && member.assigned_topics.length > 0
                                      ? member.assigned_topics.join(', ')
                                      : '—'}
                                  </td>
                                </tr>
                              ))}
                            </tbody>
                          </table>
                        ) : (
                          <p className="text-xs text-muted-foreground">
                            No active members (the group is {labelFor(group.state)}).
                          </p>
                        )}
                      </td>
                    </tr>
                  )}
                </Fragment>
              )
            })}
          </tbody>
        </table>
      </div>
    </div>
  )
}
