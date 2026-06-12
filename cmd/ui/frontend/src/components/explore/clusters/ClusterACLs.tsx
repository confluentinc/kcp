interface ACL {
  ResourceType: string
  ResourceName: string
  ResourcePatternType: string
  Principal: string
  Host: string
  Operation: string
  PermissionType: string
}

interface ClusterACLsProps {
  acls: ACL[]
}

export const ClusterACLs = ({ acls }: ClusterACLsProps) => {
  const getPermissionBadge = (permissionType: string) => {
    const isAllow = permissionType === 'Allow'
    return (
      <span
        className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
          isAllow
            ? 'bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200'
            : 'bg-red-100 text-red-800 dark:bg-red-900 dark:text-red-200'
        }`}
      >
        {permissionType}
      </span>
    )
  }

  const getResourceTypeBadge = (resourceType: string) => {
    const colors = {
      Topic: 'bg-blue-100 text-blue-800 dark:bg-accent/20 dark:text-accent',
      Group: 'bg-purple-100 text-purple-800 dark:bg-purple-900 dark:text-purple-200',
      Cluster: 'bg-orange-100 text-orange-800 dark:bg-orange-900 dark:text-orange-200',
    }

    return (
      <span
        className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
          colors[resourceType as keyof typeof colors] ||
          'bg-gray-100 text-gray-800 dark:bg-card dark:text-gray-200'
        }`}
      >
        {resourceType}
      </span>
    )
  }

  if (!acls || acls.length === 0) {
    return (
      <div className="text-center py-12">
        <div className="text-muted-foreground text-lg">No ACLs found</div>
        <p className="text-sm text-muted-foreground mt-2">
          This cluster doesn't have any Access Control Lists configured.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold text-foreground">
          Access Control Lists ({acls.length})
        </h3>
      </div>

      <div className="overflow-x-auto">
        <table className="w-full border border-border rounded-lg">
          <thead>
            <tr className="bg-secondary">
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-border">
                Resource Type
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                Resource Name
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                Pattern Type
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                Principal
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                Host
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                Operation
              </th>
              <th className="px-4 py-3 text-left text-sm font-medium text-foreground border-b border-l border-border">
                Permission
              </th>
            </tr>
          </thead>
          <tbody>
            {acls.map((acl, index) => (
              <tr
                key={index}
                className="hover:bg-secondary transition-colors"
              >
                <td className="px-4 py-3 text-sm border-b border-border">
                  {getResourceTypeBadge(acl.ResourceType)}
                </td>
                <td className="px-4 py-3 text-sm text-foreground font-mono border-b border-l border-border">
                  {acl.ResourceName}
                </td>
                <td className="px-4 py-3 text-sm text-muted-foreground border-b border-l border-border">
                  {acl.ResourcePatternType}
                </td>
                <td className="px-4 py-3 text-sm text-foreground font-mono border-b border-l border-border">
                  {acl.Principal}
                </td>
                <td className="px-4 py-3 text-sm text-muted-foreground font-mono border-b border-l border-border">
                  {acl.Host}
                </td>
                <td className="px-4 py-3 text-sm text-muted-foreground border-b border-l border-border">
                  {acl.Operation}
                </td>
                <td className="px-4 py-3 text-sm border-b border-l border-border">
                  {getPermissionBadge(acl.PermissionType)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

