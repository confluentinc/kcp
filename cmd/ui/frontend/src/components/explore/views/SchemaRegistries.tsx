import { useState, useMemo } from 'react'
import { ChevronDown, ChevronRight } from 'lucide-react'
import { Button } from '@/components/common/ui/button'
import { Tabs } from '@/components/common/Tabs'
import type { SchemaRegistriesState, GlueSchemaRegistry, GlueSchema, GlueSchemaVersion } from '@/types/api/state'

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

interface ConfluentSchemaRegistry {
  type: string
  url: string
  subjects: SchemaSubject[]
}

interface SchemaRegistriesProps {
  schemaRegistriesState: SchemaRegistriesState
}

export const SchemaRegistries = ({ schemaRegistriesState }: SchemaRegistriesProps) => {
  const confluentRegistries = schemaRegistriesState?.confluent_schema_registry ?? []
  const glueRegistries = schemaRegistriesState?.aws_glue ?? []
  const totalCount = confluentRegistries.length + glueRegistries.length

  const tabs = useMemo(() => {
    const result = []
    if (confluentRegistries.length > 0) {
      result.push({ id: 'confluent', label: `Confluent Schema Registry (${confluentRegistries.length})` })
    }
    if (glueRegistries.length > 0) {
      result.push({ id: 'glue', label: `AWS Glue (${glueRegistries.length})` })
    }
    return result
  }, [confluentRegistries.length, glueRegistries.length])

  const [activeTab, setActiveTab] = useState(tabs[0]?.id ?? 'confluent')

  if (totalCount === 0) {
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
    <div className="space-y-0">
      <div className="px-6 pt-6 pb-4 bg-white dark:bg-card">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
          Schema Registries ({totalCount})
        </h3>
      </div>

      <Tabs
        tabs={tabs}
        activeId={activeTab}
        onChange={setActiveTab}
      />

      <div className="p-6 space-y-6">
        {activeTab === 'confluent' && confluentRegistries.map((registry, registryIndex) => (
          <ConfluentRegistryCard
            key={`confluent-${registry.url}-${registryIndex}`}
            registry={registry}
            registryIndex={registryIndex}
          />
        ))}

        {activeTab === 'glue' && glueRegistries.map((registry, registryIndex) => (
          <GlueRegistryCard
            key={`glue-${registry.registry_name}-${registry.region}-${registryIndex}`}
            registry={registry}
            registryIndex={registryIndex}
          />
        ))}
      </div>
    </div>
  )
}

// --- Confluent Schema Registry Card ---

const ConfluentRegistryCard = ({
  registry,
  registryIndex,
}: {
  registry: ConfluentSchemaRegistry
  registryIndex: number
}) => {
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

  return (
    <div className="bg-white dark:bg-card border border-gray-200 dark:border-border rounded-lg shadow-sm transition-colors">
      {/* Registry Header */}
      <div className="p-6 border-b border-gray-200 dark:border-border bg-gray-50 dark:bg-card">
        <div className="flex items-start justify-between">
          <div className="flex-1">
            <div className="flex items-center gap-3 mb-2">
              <h4 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
                Confluent Schema Registry
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
            const subjectKey = `confluent-${registryIndex}-${subjectIndex}`
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
                      {isExpanded
                        ? <ChevronDown className="h-4 w-4 text-gray-400 dark:text-gray-500 flex-shrink-0" />
                        : <ChevronRight className="h-4 w-4 text-gray-400 dark:text-gray-500 flex-shrink-0" />
                      }
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
                                    {isVersionExpanded
                                      ? <ChevronDown className="h-3.5 w-3.5 text-gray-400 dark:text-gray-500 flex-shrink-0" />
                                      : <ChevronRight className="h-3.5 w-3.5 text-gray-400 dark:text-gray-500 flex-shrink-0" />
                                    }
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
  )
}

// --- AWS Glue Schema Registry Card ---

const GlueRegistryCard = ({
  registry,
  registryIndex,
}: {
  registry: GlueSchemaRegistry
  registryIndex: number
}) => {
  const [expandedSchemas, setExpandedSchemas] = useState<Set<string>>(new Set())
  const [expandedVersions, setExpandedVersions] = useState<Set<string>>(new Set())

  const toggleSchema = (schemaKey: string) => {
    const newExpanded = new Set(expandedSchemas)
    if (newExpanded.has(schemaKey)) {
      newExpanded.delete(schemaKey)
    } else {
      newExpanded.add(schemaKey)
    }
    setExpandedSchemas(newExpanded)
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

  return (
    <div className="bg-white dark:bg-card border border-gray-200 dark:border-border rounded-lg shadow-sm transition-colors">
      {/* Registry Header */}
      <div className="p-6 border-b border-gray-200 dark:border-border bg-gray-50 dark:bg-card">
        <div className="flex items-start justify-between">
          <div className="flex-1">
            <div className="flex items-center gap-3 mb-2">
              <h4 className="text-xl font-semibold text-gray-900 dark:text-gray-100">
                AWS Glue Schema Registry
              </h4>
              <span className="px-2 py-1 text-xs font-medium bg-orange-100 text-orange-800 dark:bg-orange-900/30 dark:text-orange-300 rounded-full">
                {registry.schemas?.length ?? 0} schemas
              </span>
            </div>
            <div className="space-y-1">
              <p className="text-sm text-gray-500 dark:text-gray-400">
                Registry: {registry.registry_name}
              </p>
              <p className="text-sm text-gray-500 dark:text-gray-400">
                Region: {registry.region}
              </p>
              <p className="text-xs text-gray-400 dark:text-gray-500 font-mono">
                {registry.registry_arn}
              </p>
            </div>
          </div>
        </div>
      </div>

      {/* Schemas */}
      <div className="p-6">
        <h5 className="font-medium text-gray-900 dark:text-gray-100 mb-4">Schemas</h5>

        {(!registry.schemas || registry.schemas.length === 0) ? (
          <p className="text-sm text-gray-400 dark:text-gray-500">No schemas found in this registry.</p>
        ) : (
          <div className="space-y-4">
            {registry.schemas.map((schema: GlueSchema, schemaIndex: number) => {
              const schemaKey = `glue-${registryIndex}-${schemaIndex}`
              const isExpanded = expandedSchemas.has(schemaKey)

              return (
                <div
                  key={schemaKey}
                  className="border border-gray-200 dark:border-border rounded-lg"
                >
                  {/* Schema Header */}
                  <div className="p-4 bg-gray-50 dark:bg-card border-b border-gray-200 dark:border-border">
                    <button
                      onClick={() => toggleSchema(schemaKey)}
                      className="w-full text-left flex items-center justify-between"
                    >
                      <div className="flex items-center gap-3">
                        {isExpanded
                          ? <ChevronDown className="h-4 w-4 text-gray-400 dark:text-gray-500 flex-shrink-0" />
                          : <ChevronRight className="h-4 w-4 text-gray-400 dark:text-gray-500 flex-shrink-0" />
                        }
                        <h6 className="font-medium text-gray-900 dark:text-gray-100">
                          {schema.schema_name}
                        </h6>
                        <span className="px-2 py-1 text-xs font-medium bg-gray-200 text-gray-700 dark:bg-gray-600 dark:text-gray-300 rounded-full">
                          {schema.data_format}
                        </span>
                        {schema.latest_version && (
                          <span className="px-2 py-1 text-xs font-medium bg-green-100 text-green-800 dark:bg-green-900 dark:text-green-200 rounded-full">
                            v{schema.latest_version.version_number}
                          </span>
                        )}
                      </div>
                      <div className="text-sm text-gray-500 dark:text-gray-400">
                        {schema.versions?.length ?? 0} version
                        {(schema.versions?.length ?? 0) !== 1 ? 's' : ''}
                      </div>
                    </button>
                  </div>

                  {/* Schema Content */}
                  {isExpanded && (
                    <div className="p-4 space-y-4">
                      {/* Latest Version */}
                      {schema.latest_version && (
                        <div>
                          <div className="flex items-center justify-between mb-2">
                            <h6 className="font-medium text-gray-900 dark:text-gray-100">
                              Latest Version (v{schema.latest_version.version_number})
                            </h6>
                            <Button
                              onClick={() =>
                                copyToClipboard(formatSchema(schema.latest_version!.schema_definition))
                              }
                              variant="outline"
                              size="sm"
                            >
                              Copy Schema
                            </Button>
                          </div>
                          <div className="flex items-center gap-2 mb-2">
                            <span className="px-2 py-1 text-xs font-medium bg-gray-200 text-gray-700 dark:bg-gray-600 dark:text-gray-300 rounded-full">
                              {schema.latest_version.status}
                            </span>
                          </div>
                          <textarea
                            readOnly
                            value={formatSchema(schema.latest_version.schema_definition)}
                            className="w-full h-32 p-3 text-sm font-mono bg-gray-50 dark:bg-card border border-gray-200 dark:border-border rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 dark:text-gray-100"
                          />
                        </div>
                      )}

                      {/* Version History */}
                      {schema.versions && schema.versions.length > 0 && (
                        <div>
                          <h6 className="font-medium text-gray-900 dark:text-gray-100 mb-3 block">
                            Version History
                          </h6>
                          <div className="space-y-2">
                            {schema.versions.map((version: GlueSchemaVersion, versionIndex: number) => {
                              const versionKey = `${schemaKey}-${versionIndex}`
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
                                        {isVersionExpanded
                                          ? <ChevronDown className="h-3.5 w-3.5 text-gray-400 dark:text-gray-500 flex-shrink-0" />
                                          : <ChevronRight className="h-3.5 w-3.5 text-gray-400 dark:text-gray-500 flex-shrink-0" />
                                        }
                                        <span className="font-medium text-gray-900 dark:text-gray-100">
                                          Version {version.version_number}
                                        </span>
                                        <span className="px-2 py-1 text-xs font-medium bg-gray-200 text-gray-700 dark:bg-gray-600 dark:text-gray-300 rounded-full">
                                          {version.data_format}
                                        </span>
                                      </div>
                                      <span className="text-sm text-gray-500 dark:text-gray-400">
                                        {version.status}
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
                                            copyToClipboard(formatSchema(version.schema_definition))
                                          }
                                          variant="outline"
                                          size="sm"
                                        >
                                          Copy Schema
                                        </Button>
                                      </div>
                                      <textarea
                                        readOnly
                                        value={formatSchema(version.schema_definition)}
                                        className="w-full h-24 p-3 text-sm font-mono bg-gray-50 dark:bg-card border border-gray-200 dark:border-border rounded-lg focus:outline-none focus:ring-2 focus:ring-blue-500 dark:text-gray-100"
                                      />
                                    </div>
                                  )}
                                </div>
                              )
                            })}
                          </div>
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}

// --- Utility functions ---

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
