import type {
  MetricsApiResponse,
  MetricsQueryParams,
  CostsApiResponse,
  CostsQueryParams,
  StateUploadRequest,
  StateUploadResponse,
  ApiErrorResponse,
} from '@/types/api'
import { API_ENDPOINTS, REQUEST_TIMEOUT } from '@/constants'

/**
 * Custom error class for API errors
 */
export class ApiError extends Error {
  status?: number
  response?: ApiErrorResponse

  constructor(message: string, status?: number, response?: ApiErrorResponse) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.response = response
  }
}

/**
 * Request configuration options
 */
export interface RequestConfig {
  signal?: AbortSignal
  timeout?: number
}

const BASE_URL = ''

/**
 * Builds query string from parameters
 */
function buildQueryString(params: Record<string, string | Date | undefined>): string {
  const searchParams = new URLSearchParams()

  Object.entries(params).forEach(([key, value]) => {
    if (value !== undefined && value !== null) {
      if (value instanceof Date) {
        searchParams.append(key, value.toISOString())
      } else {
        searchParams.append(key, String(value))
      }
    }
  })

  return searchParams.toString()
}

/**
 * Performs a fetch request with error handling
 */
async function request<T>(
  endpoint: string,
  options: RequestInit = {},
  config?: RequestConfig
): Promise<T> {
  const url = `${BASE_URL}${endpoint}`
  const controller = new AbortController()
  const signal = config?.signal || controller.signal

  // Set timeout if specified, otherwise use default
  const finalConfig = { timeout: REQUEST_TIMEOUT, ...config }
  let timeoutId: NodeJS.Timeout | undefined
  if (finalConfig.timeout) {
    timeoutId = setTimeout(() => controller.abort(), finalConfig.timeout)
  }

  try {
    const response = await fetch(url, {
      ...options,
      signal,
      headers: {
        'Content-Type': 'application/json',
        ...options.headers,
      },
    })

    // Clear timeout if request completed
    if (timeoutId) {
      clearTimeout(timeoutId)
    }

    if (!response.ok) {
      let errorData: ApiErrorResponse | undefined
      try {
        errorData = await response.json()
      } catch {
        // If response is not JSON, use status text
      }

      throw new ApiError(
        errorData?.message || `HTTP ${response.status}: ${response.statusText}`,
        response.status,
        errorData
      )
    }

    return response.json() as Promise<T>
  } catch (error) {
    if (timeoutId) {
      clearTimeout(timeoutId)
    }

    if (error instanceof ApiError) {
      throw error
    }

    if (error instanceof Error && error.name === 'AbortError') {
      throw new ApiError('Request timeout or cancelled', 408)
    }

    throw new ApiError(error instanceof Error ? error.message : 'Unknown error occurred', 0)
  }
}

/**
 * GET request helper
 */
async function get<T>(
  endpoint: string,
  params?: Record<string, string | Date | undefined>,
  config?: RequestConfig
): Promise<T> {
  let url = endpoint
  if (params) {
    const queryString = buildQueryString(params)
    if (queryString) {
      url += `?${queryString}`
    }
  }

  return request<T>(url, { method: 'GET' }, config)
}

/**
 * POST request helper
 */
async function post<T>(endpoint: string, data?: unknown, config?: RequestConfig): Promise<T> {
  return request<T>(
    endpoint,
    {
      method: 'POST',
      body: data ? JSON.stringify(data) : undefined,
    },
    config
  )
}

/**
 * Metrics API functions
 */
const metrics = {
  /**
   * Get metrics for a specific cluster
   */
  async getMetrics(
    region: string,
    cluster: string,
    params?: MetricsQueryParams,
    config?: RequestConfig
  ): Promise<MetricsApiResponse> {
    const queryParams: Record<string, string | Date | undefined> = {}
    if (params?.startDate) {
      queryParams.startDate =
        params.startDate instanceof Date ? params.startDate : new Date(params.startDate)
    }
    if (params?.endDate) {
      queryParams.endDate =
        params.endDate instanceof Date ? params.endDate : new Date(params.endDate)
    }

    return get<MetricsApiResponse>(
      `${API_ENDPOINTS.METRICS}/${encodeURIComponent(region)}/${encodeURIComponent(cluster)}`,
      queryParams,
      config
    )
  },
}

/**
 * Costs API functions
 */
const costs = {
  /**
   * Get costs for a specific region
   */
  async getCosts(
    region: string,
    params?: CostsQueryParams,
    config?: RequestConfig
  ): Promise<CostsApiResponse> {
    const queryParams: Record<string, string | Date | undefined> = {}
    if (params?.startDate) {
      queryParams.startDate =
        params.startDate instanceof Date ? params.startDate : new Date(params.startDate)
    }
    if (params?.endDate) {
      queryParams.endDate =
        params.endDate instanceof Date ? params.endDate : new Date(params.endDate)
    }

    return get<CostsApiResponse>(`${API_ENDPOINTS.COSTS}/${encodeURIComponent(region)}`, queryParams, config)
  },
}

/**
 * State API functions
 */
const state = {
  /**
   * Upload state file data
   */
  async uploadState(
    data: StateUploadRequest,
    config?: RequestConfig
  ): Promise<StateUploadResponse> {
    return post<StateUploadResponse>(API_ENDPOINTS.UPLOAD_STATE, data, config)
  },
}

/**
 * Wizard API functions
 */
const wizard = {
  /**
   * Generate Terraform files from wizard data
   */
  async generateTerraform<T = unknown>(
    endpoint: string,
    wizardData: Record<string, unknown>,
    config?: RequestConfig
  ): Promise<T> {
    return post<T>(endpoint, wizardData, config)
  },
}

/**
 * Main API client that aggregates all endpoint clients
 */
export const apiClient = {
  metrics,
  costs,
  state,
  wizard,
}
