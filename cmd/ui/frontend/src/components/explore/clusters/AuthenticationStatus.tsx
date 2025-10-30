import StatusBadge from '@/components/common/StatusBadge'
import { createStatusBadgeProps } from '@/lib/utils'

interface ClientAuthentication {
  Sasl?: {
    Iam?: { Enabled: boolean }
    Scram?: { Enabled: boolean }
  }
  Tls?: { Enabled: boolean }
  Unauthenticated?: { Enabled: boolean }
}

interface AuthenticationStatusProps {
  clientAuthentication: ClientAuthentication
  displayMode?: 'table' | 'list'
}

interface AuthenticationMethod {
  name: string
  enabled: boolean
}

export default function AuthenticationStatus({
  clientAuthentication,
  displayMode = 'table',
}: AuthenticationStatusProps) {
  const methods: AuthenticationMethod[] = [
    {
      name: 'IAM Authentication',
      enabled: clientAuthentication.Sasl?.Iam?.Enabled || false,
    },
    {
      name: 'SCRAM Authentication',
      enabled: clientAuthentication.Sasl?.Scram?.Enabled || false,
    },
    {
      name: 'TLS Authentication',
      enabled: clientAuthentication.Tls?.Enabled || false,
    },
    {
      name: 'Unauthenticated Access',
      enabled: clientAuthentication.Unauthenticated?.Enabled || false,
    },
  ]

  if (displayMode === 'list') {
    return (
      <div className="space-y-2">
        {methods.map((method) => (
          <div
            key={method.name}
            className="flex items-center justify-between p-3 bg-gray-50 dark:bg-card rounded-lg transition-colors"
          >
            <span className="font-medium text-gray-900 dark:text-gray-100">{method.name}</span>
            <StatusBadge {...createStatusBadgeProps(method.enabled)} />
          </div>
        ))}
      </div>
    )
  }

  return (
    <div className="overflow-x-auto">
      <table className="w-full text-sm">
        <thead>
          <tr className="border-b border-gray-200 dark:border-border">
            <th className="text-left py-2 font-medium text-gray-900 dark:text-gray-100">
              Authentication Method
            </th>
            <th className="text-center py-2 font-medium text-gray-900 dark:text-gray-100">
              Status
            </th>
          </tr>
        </thead>
        <tbody className="divide-y divide-gray-200 dark:divide-gray-600">
          {methods.map((method) => (
            <tr key={method.name}>
              <td className="py-2 text-gray-900 dark:text-gray-100">{method.name}</td>
              <td className="py-2 text-center">
                <StatusBadge {...createStatusBadgeProps(method.enabled)} />
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

