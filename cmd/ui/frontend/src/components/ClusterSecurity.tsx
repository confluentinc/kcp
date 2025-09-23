interface ClusterSecurityProps {
  provisioned: any
  brokerInfo: any
}

export default function ClusterSecurity({ provisioned, brokerInfo }: ClusterSecurityProps) {
  const getStatusBadge = (enabled: boolean, label: string) => (
    <span
      className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
        enabled ? 'bg-green-100 text-green-800' : 'bg-red-100 text-red-800'
      }`}
    >
      {enabled ? '✅' : '❌'} {label}
    </span>
  )

  return (
    <div className="space-y-6">
      {/* Security Overview */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
        <div className="bg-white rounded-lg border p-6">
          <h3 className="text-xl font-semibold text-gray-900 mb-4">🔐 Authentication Methods</h3>
          <div className="space-y-3">
            <div className="flex items-center justify-between p-3 bg-gray-50 rounded-lg">
              <span className="font-medium">IAM Authentication</span>
              {getStatusBadge(provisioned.ClientAuthentication?.Sasl?.Iam?.Enabled || false, 'IAM')}
            </div>
            <div className="flex items-center justify-between p-3 bg-gray-50 rounded-lg">
              <span className="font-medium">SCRAM Authentication</span>
              {getStatusBadge(
                provisioned.ClientAuthentication?.Sasl?.Scram?.Enabled || false,
                'SCRAM'
              )}
            </div>
            <div className="flex items-center justify-between p-3 bg-gray-50 rounded-lg">
              <span className="font-medium">TLS Authentication</span>
              {getStatusBadge(provisioned.ClientAuthentication?.Tls?.Enabled || false, 'TLS')}
            </div>
            <div className="flex items-center justify-between p-3 bg-gray-50 rounded-lg">
              <span className="font-medium">Unauthenticated Access</span>
              {getStatusBadge(
                provisioned.ClientAuthentication?.Unauthenticated?.Enabled || false,
                'Unauth'
              )}
            </div>
          </div>
        </div>

        <div className="bg-white rounded-lg border p-6">
          <h3 className="text-xl font-semibold text-gray-900 mb-4">🔒 Encryption Settings</h3>
          <div className="space-y-4">
            <div>
              <h4 className="font-medium text-gray-900 mb-2">Encryption at Rest</h4>
              <div className="bg-gray-50 rounded-lg p-3">
                <div className="text-sm text-gray-600">KMS Key ID:</div>
                <div className="font-mono text-xs bg-gray-200 px-2 py-1 rounded mt-1">
                  {provisioned.EncryptionInfo?.EncryptionAtRest?.DataVolumeKMSKeyId?.split(
                    '/'
                  ).pop() || 'Not configured'}
                </div>
              </div>
            </div>
            <div>
              <h4 className="font-medium text-gray-900 mb-2">Encryption in Transit</h4>
              <div className="space-y-2">
                <div className="flex justify-between">
                  <span className="text-sm text-gray-600">Client-Broker:</span>
                  <span className="font-medium">
                    {provisioned.EncryptionInfo?.EncryptionInTransit?.ClientBroker ||
                      'Not configured'}
                  </span>
                </div>
                <div className="flex justify-between">
                  <span className="text-sm text-gray-600">In-Cluster:</span>
                  {getStatusBadge(
                    provisioned.EncryptionInfo?.EncryptionInTransit?.InCluster || false,
                    'Enabled'
                  )}
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Network Security */}
      <div className="bg-white rounded-lg border p-6">
        <h3 className="text-xl font-semibold text-gray-900 mb-4">🌐 Network Security</h3>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div>
            <h4 className="font-medium text-gray-900 mb-2">Security Groups</h4>
            <div className="space-y-1">
              {(brokerInfo.SecurityGroups || []).map((sg: string, index: number) => (
                <div
                  key={index}
                  className="font-mono text-xs bg-gray-100 px-2 py-1 rounded"
                >
                  {sg}
                </div>
              ))}
            </div>
          </div>
          <div>
            <h4 className="font-medium text-gray-900 mb-2">Availability Zones</h4>
            <div className="space-y-1">
              {(brokerInfo.ZoneIds || []).map((zone: string, index: number) => (
                <div
                  key={index}
                  className="text-sm bg-blue-100 text-blue-800 px-2 py-1 rounded"
                >
                  {zone}
                </div>
              ))}
            </div>
          </div>
          <div>
            <h4 className="font-medium text-gray-900 mb-2">Public Access</h4>
            {getStatusBadge(
              brokerInfo.ConnectivityInfo?.PublicAccess?.Type !== 'DISABLED',
              'Public Access'
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
