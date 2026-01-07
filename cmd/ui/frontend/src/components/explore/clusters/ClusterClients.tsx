interface DiscoveredClient {
  composite_key?: string
  client_id?: string
  role?: string
  topic?: string
  auth?: string
  principal?: string
  timestamp?: string
}

interface ClusterClientsProps {
  clients?: DiscoveredClient[]
}

export const ClusterClients = ({ clients }: ClusterClientsProps) => {
  console.log('clients', clients)

  const getRoleBadge = (role: string) => {
    const colors: Record<string, string> = {
      producer: 'bg-blue-100 text-blue-800 dark:bg-blue-900 dark:text-blue-200',
      consumer: 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200',
    }

    const normalizedRole = role?.toLowerCase() || ''
    const colorClass = colors[normalizedRole] || 'bg-gray-100 text-gray-800 dark:bg-card dark:text-gray-200'

    return (
      <span
        className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${colorClass}`}
      >
        {role || 'N/A'}
      </span>
    )
  }

  const getAuthBadge = (auth: string) => {
    const colors: Record<string, string> = {
      iam: 'bg-orange-100 text-orange-800 dark:bg-orange-900 dark:text-orange-200',
      sasl_scram: 'bg-cyan-100 text-cyan-800 dark:bg-cyan-900 dark:text-cyan-200',
      tls: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900 dark:text-emerald-200',
      unauthenticated: 'bg-yellow-100 text-yellow-800 dark:bg-yellow-900 dark:text-yellow-200',
    }

    const normalizedAuth = auth?.toLowerCase() || ''
    const colorClass = colors[normalizedAuth] || 'bg-gray-100 text-gray-800 dark:bg-card dark:text-gray-200'

    return (
      <span
        className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${colorClass}`}
      >
        {auth || 'N/A'}
      </span>
    )
  }

  if (!clients || clients.length === 0) {
    return (
      <div className="text-center py-12">
        <div className="text-gray-500 dark:text-gray-400 text-lg">No clients found</div>
        <p className="text-sm text-gray-400 dark:text-gray-500 mt-2">
          This cluster doesn't have any discovered clients.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Discovered Clients ({clients.length})
        </h3>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full border border-gray-200 dark:border-border rounded-lg">
          <thead>
            <tr className="bg-gray-50 dark:bg-card">
              <th className="px-4 py-3 text-left text-sm font-medium text-gray-900 dark:text-gray-100 border-b border-gray-200 dark:border-border">
                Client ID
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-gray-900 dark:text-gray-100 border-b border-l border-gray-200 dark:border-border">
                Topic
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-gray-900 dark:text-gray-100 border-b border-l border-gray-200 dark:border-border">
                Role
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-gray-900 dark:text-gray-100 border-b border-l border-gray-200 dark:border-border">
                Auth
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-gray-900 dark:text-gray-100 border-b border-l border-gray-200 dark:border-border">
                Principal
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-gray-900 dark:text-gray-100 border-b border-l border-gray-200 dark:border-border">
                Timestamp
              </th>
            </tr>
          </thead>
          <tbody>
            {clients.map((client, index) => (
              <tr
                key={client.composite_key || index}
                className="hover:bg-gray-50 dark:hover:bg-gray-700 transition-colors"
              >
                <td className="px-4 py-3 text-sm text-gray-900 dark:text-gray-100 font-mono border-b border-gray-200 dark:border-border">
                  {client.client_id || 'N/A'}
                </td>
                <td className="px-4 py-3 text-sm text-gray-900 dark:text-gray-100 font-mono border-b border-l border-gray-200 dark:border-border">
                  {client.topic || 'N/A'}
                </td>
                <td className="px-4 py-3 text-sm border-b border-l border-gray-200 dark:border-border">
                  {getRoleBadge(client.role || '')}
                </td>
                <td className="px-4 py-3 text-sm border-b border-l border-gray-200 dark:border-border">
                  {getAuthBadge(client.auth || '')}
                </td>
                <td className="px-4 py-3 text-sm text-gray-900 dark:text-gray-100 font-mono border-b border-l border-gray-200 dark:border-border">
                  {client.principal || 'N/A'}
                </td>
                <td className="px-4 py-3 text-sm text-gray-600 dark:text-gray-400 border-b border-l border-gray-200 dark:border-border">
                  {client.timestamp
                    ? new Date(client.timestamp).toLocaleString()
                    : 'N/A'}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

