// Trace one explicit installation without changing the reusable sharing link.
export function installationLink(source: string, trace: string) {
  const link = new URL(source)
  const address = link.searchParams.get('url')
  if (!address) return source // Demo links do not invoke a device installer.
  const manifest = new URL(address)
  manifest.searchParams.set('trace_id', trace)
  link.searchParams.set('url', manifest.toString())
  return link.toString()
}

export function installationTrace() {
  const alphabet = 'abcdefghijklmnopqrstuvwxyz0123456789'
  const values = crypto.getRandomValues(new Uint32Array(7))
  return Array.from(values, (value) => alphabet[value % alphabet.length]).join('')
}
