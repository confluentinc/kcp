import { useState } from 'react'
import { Button } from '@/components/common/ui/button'

interface SchemaVersion {
  schema: string
  id: number
  subject: string
  version: number
  schemaType?: string
}

interface SchemaSubject {
  name: string
  schema_type: string
  versions: SchemaVersion[]
  latest_schema: SchemaVersion
}

interface SchemaRegistry {
  type: string
  url: string
  subjects: SchemaSubject[]
}

interface SchemaRegistriesProps {
  schemaRegistries: SchemaRegistry[]
}

export default function SchemaRegistries({ schemaRegistries }: SchemaRegistriesProps) {
  const [expandedSubjects, setExpandedSubjects] = useState<Set<string>>(new Set())
  const [expandedVersions, setExpandedVersions] = useState<Set<string>>(new Set())

  const toggleSubject = (subjectKey: string) => {
    const newExpanded = new Set(expandedSubjects)
    if (newExpanded.has(subjectKey)) {
      newExpanded.delete(subjectKey)
    } else {
      newExpanded.add(subjectKey)
    }
    setExpandedSubjects(newExpanded)
  }

  const toggleVersion = (versionKey: string) => {
    const newExpanded = new Set(expandedVersions)
    if (newExpanded.has(versionKey)) {
      newExpanded.delete(versionKey)
    } else {
      newExpanded.add(versionKey)
    }
    setExpandedVersions(newExpanded)
  }

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text)
  }

  const formatSchema = (schema: string) => {
    try {
      return JSON.stringify(JSON.parse(schema), null, 2)
    } catch {
      return schema
    }
  }

  if (!schemaRegistries || schemaRegistries.length === 0) {
    return (
      <div className="text-center py-12">
        <div className="text-gray-500 dark:text-gray-400 text-lg">No schema registries found</div>
        <p className="text-sm text-gray-400 dark:text-gray-500 mt-2">
          No schema registries were discovered in the KCP state file.
        </p>
      </div>
    )
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Schema Registries ({schemaRegistries.length})
        </h3>
      </div>

      <div className="space-y-6">
        {schemaRegistries.map((registry, registryIndex) => (
          <div
            key={`${registry.type}-${registry.url}-${registryIndex}`}
            className="bg-white dark:bg-card border border-gray-200 dark:border-border rounded-lg shadow-sm transition-colors"
          >
            {/* Registry Header */}
            <div className="p-6 border-b border-gray-200 dark:border-border bg-gray-50 dark:bg-card">
              <div className="flex items-start justify-between">
                <div className="flex-1">
                  <div className="flex items-center gap-3 mb-2">
                    <h4 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
                      {registry.type === 'confluent' ? 'Confluent Schema Registry' : registry.type}
                    </h4>
                    <span className="px-2 py-1 text-xs font-medium bg-blue-100 text-blue-800 dark:bg-accent/20 dark:text-accent rounded-full">
                      {registry.subjects.length} subjects
                    </span>
                  </div>
                  <p className="text-sm text-gray-500 dark:text-gray-400">URL: {registry.url}</p>
                </div>
              </div>
            </div>

            {/* Subjects */}
            <div className="p-6">
              <h5 className="font-medium text-gray-900 dark:text-gray-100 mb-4">Schema Subjects</h5>

              <div className="space-y-4">
                {registry.subjects.map((subject, subjectIndex) => {
                  const subjectKey = `${registryIndex}-${subjectIndex}`
                  const isExpanded = expandedSubjects.has(subjectKey)

                  return (
                    <div
                      key={subjectKey}
                      className="border border-gray-200 dark:border-border rounded-lg"
                    >
                      {/* Subject Header */}
                      <div className="p-4 bg-gray-50 dark:bg-card border-b border-gray-200 dark:border-border">
                        <button
                          onClick={() => toggleSubject(subjectKey)}
                          className="w-full text-left flex items-center justify-between"
                        >
                          <div className="flex items-center gap-3">
                            <div
                              className={`w-2 h-2 rounded-full ${
                                isExpanded ? 'bg-blue-600' : 'bg-gray-400'
                              }`}
                            ></div>
                            <h6 className="font-medium text-gray-900 dark:text-gray-100">
                              {subject.name}
                            </h6>
                            <span className="px-2 py-1 text-xs font-medium bg-gray-200 text-gray-700 dark:bg-gray-600 dark:text-gray-300 rounded-full">
                              {subject.schema_type}
                            </span>
                            <span className="px-2 py-1 text-xs font-medium bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200 rounded-full">
                              v{subject.latest_schema.version}
                            </span>
                          </div>
                          <div className="text-sm text-gray-500 dark:text-gray-400">
                            {subject.versions.length} version
                            {subject.versions.length !== 1 ? 's' : ''}
                          </div>
                        </button>
                      </div>

                      {/* Subject Content */}
                      {isExpanded && (
                        <div className="p-4 space-y-4">
                          {/* Latest Schema */}
                          <div>
                            <div className="flex items-center justify-between mb-2">
                              <h6 className="font-medium text-gray-900 dark:text-gray-100">
                                Latest Schema (v{subject.latest_schema.version})
                              </h6>
                              <Button
                                onClick={() =>
                                  copyToClipboard(formatSchema(subject.latest_schema.schema))
                                }
                                variant="outline"
                                size="sm"
                              >
                                Copy Schema
                              </Button>
                            </div>
                            <textarea
                              readOnly
                              value={formatSchema(subject.latest_schema.schema)}
                              className="w-full h-32 p-3 text-sm font-mono bg-gray-50 dark:bg-card border border-gray-200 dark:border-border rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 dark:text-gray-100"
                            />
                          </div>

                          {/* Version History */}
                          <div>
                            <h6 className="font-medium text-gray-900 dark:text-gray-100 mb-3 block">
                              Version History
                            </h6>
                            <div className="space-y-2">
                              {subject.versions.map((version, versionIndex) => {
                                const versionKey = `${subjectKey}-${versionIndex}`
                                const isVersionExpanded = expandedVersions.has(versionKey)

                                return (
                                  <div
                                    key={versionKey}
                                    className="border border-gray-200 dark:border-border rounded-md"
                                  >
                                    <button
                                      onClick={() => toggleVersion(versionKey)}
                                      className="w-full text-left p-3 hover:bg-gray-50 dark:hover:bg-gray-700 transition-colors"
                                    >
                                      <div className="flex items-center justify-between">
                                        <div className="flex items-center gap-2">
                                          <div
                                            className={`w-1.5 h-1.5 rounded-full ${
                                              isVersionExpanded ? 'bg-blue-500' : 'bg-gray-400'
                                            }`}
                                          ></div>
                                          <span className="font-medium text-gray-900 dark:text-gray-100">
                                            Version {version.version}
                                          </span>
                                          {version.schemaType && (
                                            <span className="px-2 py-1 text-xs font-medium bg-gray-200 text-gray-700 dark:bg-gray-600 dark:text-gray-300 rounded-full">
                                              {version.schemaType}
                                            </span>
                                          )}
                                        </div>
                                        <span className="text-sm text-gray-500 dark:text-gray-400">
                                          ID: {version.id}
                                        </span>
                                      </div>
                                    </button>

                                    {isVersionExpanded && (
                                      <div className="p-3 border-t border-gray-200 dark:border-border bg-gray-50 dark:bg-card">
                                        <div className="flex items-center justify-between mb-2">
                                          <span className="text-sm font-medium text-gray-900 dark:text-gray-100">
                                            Schema Definition
                                          </span>
                                          <Button
                                            onClick={() =>
                                              copyToClipboard(formatSchema(version.schema))
                                            }
                                            variant="outline"
                                            size="sm"
                                          >
                                            Copy Schema
                                          </Button>
                                        </div>
                                        <textarea
                                          readOnly
                                          value={formatSchema(version.schema)}
                                          className="w-full h-24 p-3 text-sm font-mono bg-gray-50 dark:bg-card border border-gray-200 dark:border-border rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 dark:text-gray-100"
                                        />
                                      </div>
                                    )}
                                  </div>
                                )
                              })}
                            </div>
                          </div>
                        </div>
                      )}
                    </div>
                  )
                })}
              </div>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
