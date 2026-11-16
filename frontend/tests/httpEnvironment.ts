import axios from 'axios'

const origin = 'http://app.example.test'
const bodies = new WeakMap<Request, BodyInit | null>()

// Resolve browser-relative URLs in Node and keep existing transport fixtures observable.
class BrowserRequest extends Request {
  constructor(input: RequestInfo | URL, init?: RequestInit) {
    super(typeof input === 'string' ? new URL(input, origin) : input, init)
    bodies.set(this, init?.body ?? null)
  }
}

axios.defaults.env = {
  ...axios.defaults.env,
  Request: BrowserRequest,
  fetch: (input: Request) => {
    const url = new URL(input.url)
    const init: RequestInit = {
      method: input.method,
      headers: Object.fromEntries(input.headers),
      credentials: input.credentials,
      signal: input.signal,
    }
    const body = bodies.get(input)
    if (body !== undefined && body !== null) init.body = body
    return fetch(url.pathname + url.search, init)
  },
}
