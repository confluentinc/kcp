import { useMemo, useState, useEffect } from 'react'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

interface MetricBucket {
  start: string
  end: string
  data: {
    bytes_in_per_sec_avg: number
    bytes_out_per_sec_avg: number
    messages_in_per_sec_avg: number
    connection_count?: number
    client_connection_count?: number
    cpu_idle?: number
    cpu_user?: number
    cpu_system?: number
    cpu_io_wait?: number
    heap_memory_after_gc?: number
    kafka_data_logs_disk_used?: number
    kafka_app_logs_disk_used?: number
    network_rx_errors?: number
    network_rx_dropped?: number
    network_tx_errors?: number
    network_tx_dropped?: number
    leader_count?: number
    partition_count?: number
    request_handler_avg_idle_percent?: number
    replication_bytes_in_per_sec?: number
    replication_bytes_out_per_sec?: number
    volume_read_bytes?: number
    volume_write_bytes?: number
    volume_read_ops?: number
    volume_write_ops?: number
    volume_queue_length?: number
    volume_total_read_time?: number
    volume_total_write_time?: number
    burst_balance?: number
    memory_free?: number
    memory_used?: number
    memory_buffered?: number
    memory_cached?: number
    active_controller_count?: number
    global_topic_count?: number
    global_partition_count?: number
    tcp_connections?: number
    request_throttle_time?: number
    request_throttle_queue_size?: number
    cpu_credit_balance?: number
  }
}

interface ClusterMetricsProps {
  cluster: {
    name: string
    metrics: {
      broker_az_distribution: string
      kafka_version: string
      enhanced_monitoring: string
      start_window_date: string
      end_window_date: string
      buckets: MetricBucket[]
    }
  }
}

export default function ClusterMetrics({ cluster }: ClusterMetricsProps) {
  const [isLoading, setIsLoading] = useState(true)

  // Process metrics data for table format
  const metricsData = useMemo(() => {
    const buckets = cluster.metrics.buckets || []

    // Define metrics with their labels and formatters
    const metricDefinitions = [
      {
        key: 'bytes_in_per_sec_avg',
        label: 'Bytes In/Sec',
        formatter: (v: number) => `${v?.toFixed(2) || '0'} B/s`,
      },
      {
        key: 'bytes_out_per_sec_avg',
        label: 'Bytes Out/Sec',
        formatter: (v: number) => `${v?.toFixed(2) || '0'} B/s`,
      },
      {
        key: 'messages_in_per_sec_avg',
        label: 'Messages/Sec',
        formatter: (v: number) => `${v?.toFixed(2) || '0'} msg/s`,
      },
      {
        key: 'connection_count',
        label: 'Connections',
        formatter: (v: number) => v?.toLocaleString() || '0',
      },
      {
        key: 'client_connection_count',
        label: 'Client Connections',
        formatter: (v: number) => v?.toLocaleString() || '0',
      },
      {
        key: 'cpu_idle',
        label: 'CPU Idle %',
        formatter: (v: number) => `${v?.toFixed(1) || '0'}%`,
      },
      {
        key: 'cpu_user',
        label: 'CPU User %',
        formatter: (v: number) => `${v?.toFixed(1) || '0'}%`,
      },
      {
        key: 'cpu_system',
        label: 'CPU System %',
        formatter: (v: number) => `${v?.toFixed(1) || '0'}%`,
      },
      {
        key: 'heap_memory_after_gc',
        label: 'Heap Memory %',
        formatter: (v: number) => `${v?.toFixed(1) || '0'}%`,
      },
      {
        key: 'kafka_data_logs_disk_used',
        label: 'Data Disk Used %',
        formatter: (v: number) => `${v?.toFixed(1) || '0'}%`,
      },
      {
        key: 'kafka_app_logs_disk_used',
        label: 'App Disk Used %',
        formatter: (v: number) => `${v?.toFixed(1) || '0'}%`,
      },
      {
        key: 'memory_used',
        label: 'Memory Used',
        formatter: (v: number) => `${((v || 0) / 1024 / 1024 / 1024).toFixed(2)} GB`,
      },
      {
        key: 'memory_free',
        label: 'Memory Free',
        formatter: (v: number) => `${((v || 0) / 1024 / 1024 / 1024).toFixed(2)} GB`,
      },
      {
        key: 'volume_read_ops',
        label: 'Volume Read Ops',
        formatter: (v: number) => v?.toLocaleString() || '0',
      },
      {
        key: 'volume_write_ops',
        label: 'Volume Write Ops',
        formatter: (v: number) => v?.toLocaleString() || '0',
      },
      {
        key: 'global_topic_count',
        label: 'Global Topics',
        formatter: (v: number) => v?.toLocaleString() || '0',
      },
      {
        key: 'global_partition_count',
        label: 'Global Partitions',
        formatter: (v: number) => v?.toLocaleString() || '0',
      },
      {
        key: 'burst_balance',
        label: 'EBS Burst Balance %',
        formatter: (v: number) => `${v?.toFixed(1) || '0'}%`,
      },
      {
        key: 'tcp_connections',
        label: 'TCP Connections',
        formatter: (v: number) => v?.toLocaleString() || '0',
      },
    ]

    // Prepare time period headers
    const timeHeaders = buckets.map((bucket) => ({
      start: bucket.start,
      end: bucket.end,
      label: new Date(bucket.start).toLocaleDateString('en-US', {
        month: 'short',
        year: 'numeric',
      }),
      fullDate: new Date(bucket.start).toLocaleDateString('en-US', {
        month: 'long',
        year: 'numeric',
      }),
    }))

    return {
      buckets,
      metricDefinitions,
      timeHeaders,
    }
  }, [cluster.metrics.buckets])

  // Handle loading state - no artificial delays
  useEffect(() => {
    setIsLoading(true)
    // Use requestAnimationFrame to allow React to render, then immediately finish loading
    const frame = requestAnimationFrame(() => {
      setIsLoading(false)
    })

    return () => cancelAnimationFrame(frame)
  }, [cluster.metrics.buckets])

  if (isLoading) {
    return (
      <div className="space-y-6">
        {/* Loading Table */}
        <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
          <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
            ⚡ Cluster Metrics
          </h3>
          <div className="flex items-center justify-center h-64">
            <div className="flex flex-col items-center space-y-4">
              <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-500"></div>
              <p className="text-gray-500 dark:text-gray-400">Processing metrics data...</p>
            </div>
          </div>
        </div>
      </div>
    )
  }

  if (!metricsData.buckets.length) {
    return (
      <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
          ⚡ Cluster Metrics
        </h3>
        <p className="text-gray-500 dark:text-gray-400">
          No metrics data available for this cluster.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      {/* Metrics Table */}
      <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
          ⚡ Cluster Metrics
        </h3>

        <div className="overflow-auto border border-gray-200 dark:border-gray-600 rounded-lg max-h-96">
          <div className="min-w-max">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="sticky left-0 bg-white dark:bg-gray-800 z-10 w-48 min-w-48 max-w-48 border-r border-gray-200 dark:border-gray-600">
                    <div className="font-semibold text-gray-900 dark:text-gray-100">Metric</div>
                  </TableHead>
                  {metricsData.timeHeaders.map((header, index) => (
                    <TableHead
                      key={index}
                      className="text-center w-32 min-w-32 max-w-32 border-r border-gray-200 dark:border-gray-600"
                      title={header.fullDate}
                    >
                      <div className="font-semibold text-gray-900 dark:text-gray-100">
                        {header.label}
                      </div>
                    </TableHead>
                  ))}
                </TableRow>
              </TableHeader>
              <TableBody>
                {metricsData.metricDefinitions.map((metric) => (
                  <TableRow
                    key={metric.key}
                    className="hover:bg-gray-50 dark:hover:bg-gray-700"
                  >
                    <TableCell className="sticky left-0 bg-white dark:bg-gray-800 z-10 font-medium w-48 min-w-48 max-w-48 border-r border-gray-200 dark:border-gray-600">
                      <div className="text-gray-900 dark:text-gray-100 truncate">
                        {metric.label}
                      </div>
                    </TableCell>
                    {metricsData.buckets.map((bucket, index) => (
                      <TableCell
                        key={index}
                        className="text-center w-32 min-w-32 max-w-32 border-r border-gray-200 dark:border-gray-600"
                      >
                        <div className="text-gray-900 dark:text-gray-100 font-mono text-sm">
                          {metric.formatter((bucket.data as any)[metric.key])}
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

      {/* Cluster Configuration */}
      <div className="bg-white dark:bg-gray-800 rounded-lg border border-gray-200 dark:border-gray-700 p-6 transition-colors">
        <h3 className="text-xl font-semibold text-gray-900 dark:text-gray-100 mb-4">
          ⚙️ Metrics Configuration
        </h3>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
            <div className="text-sm text-gray-600 dark:text-gray-400">Kafka Version</div>
            <div className="font-medium text-lg text-gray-900 dark:text-gray-100">
              {cluster.metrics.kafka_version}
            </div>
          </div>
          <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
            <div className="text-sm text-gray-600 dark:text-gray-400">Enhanced Monitoring</div>
            <div className="font-medium text-lg text-gray-900 dark:text-gray-100">
              {cluster.metrics.enhanced_monitoring}
            </div>
          </div>
          <div className="bg-gray-50 dark:bg-gray-700 rounded-lg p-4 transition-colors">
            <div className="text-sm text-gray-600 dark:text-gray-400">AZ Distribution</div>
            <div className="font-medium text-lg text-gray-900 dark:text-gray-100">
              {cluster.metrics.broker_az_distribution}
            </div>
          </div>
        </div>

        <div className="mt-4 p-4 bg-blue-50 dark:bg-blue-900 rounded-lg transition-colors">
          <div className="text-sm text-blue-800 dark:text-blue-200">
            <strong>Metrics Period:</strong>{' '}
            {new Date(cluster.metrics.start_window_date).toLocaleDateString()} to{' '}
            {new Date(cluster.metrics.end_window_date).toLocaleDateString()}
          </div>
          <div className="text-sm text-blue-600 dark:text-blue-300 mt-1">
            {metricsData.buckets.length} data points collected
          </div>
        </div>
      </div>
    </div>
  )
}
