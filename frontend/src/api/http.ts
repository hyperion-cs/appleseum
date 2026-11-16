import axios, {
  type AxiosAdapter,
  type AxiosRequestConfig,
  type AxiosResponse,
  AxiosError,
} from 'axios'
import { demoEnabled } from '../platform/browser.ts'
import { credentials } from './credentials.ts'
import { ApiError } from './errors.ts'

type Method = 'GET' | 'POST' | 'PUT' | 'DELETE'
type RequestOptions = Pick<
  AxiosRequestConfig,
  'adapter' | 'responseType' | 'onUploadProgress' | 'signal'
> & { body?: unknown; headers?: Record<string, string> }

let demo: Promise<ReturnType<typeof import('./demo.ts').createDemo>> | undefined

export function loadDemo() {
  return (demo ??= import('./demo.ts').then((module) => module.createDemo()))
}

const demoAdapter: AxiosAdapter = async (config) => {
  const body = typeof config.data === 'string' ? JSON.parse(config.data) : config.data
  const response = (await loadDemo()).request(config.url!, config.method?.toUpperCase(), body)
  const data =
    config.responseType === 'stream'
      ? response.body
      : config.responseType === 'blob'
        ? await response.blob()
        : await response.text()
  return {
    data,
    status: response.status,
    statusText: response.statusText,
    headers: Object.fromEntries(response.headers),
    config,
  }
}

export async function request<T = unknown>(
  method: Method,
  path: string,
  { body, headers, ...options }: RequestOptions = {},
) {
  const sent = { ...headers, ...credentials.headers(path, method) }
  let malformed = false
  const response = await axios
    .request<T>({
      baseURL: '/api',
      url: path,
      method,
      data: body,
      adapter: demoEnabled ? demoAdapter : 'fetch',
      responseType: 'json',
      transitional: { silentJSONParsing: false },
      validateStatus: () => true,
      ...options,
      headers: sent,
    })
    .catch((error: unknown) => {
      if (axios.isCancel(error)) throw new DOMException('Request canceled', 'AbortError')
      // A body that is not JSON fails the request but keeps its response headers.
      if (
        axios.isAxiosError(error) &&
        error.code === AxiosError.ERR_BAD_RESPONSE &&
        error.response
      ) {
        malformed = true
        return error.response as AxiosResponse<T>
      }
      throw error
    })
  if (response.headers['x-appleseum-session-reset'] === '1') credentials.reset()
  if (malformed || response.status < 200 || response.status >= 300) {
    const binary = options.responseType === 'stream' || options.responseType === 'blob'
    const error = ApiError.from(
      binary
        ? await new Response(response.data as BodyInit).json().catch(() => null)
        : response.data,
    )
    const key = sent['X-Session-Key']
    if (error.code === 'session_key_required' && key && key === credentials.key) credentials.reset()
    throw error
  }
  return response
}

async function json<T>(method: Method, path: string, body?: unknown, signal?: AbortSignal) {
  const response = await request<T>(method, path, { body, signal })
  return response.status === 202 || response.status === 204 ? (undefined as T) : response.data
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => json<T>('GET', path, undefined, signal),

  post: <T = void>(path: string, body?: unknown, signal?: AbortSignal) =>
    json<T>('POST', path, body, signal),

  put: <T>(path: string, body: unknown, signal?: AbortSignal) => json<T>('PUT', path, body, signal),

  delete: (path: string) => json<void>('DELETE', path),
}
