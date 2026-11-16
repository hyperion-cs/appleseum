type Version = number[]
export type OsRange = { minimum: Version; before?: Version }
export type OsCompatibility = 'compatible' | 'incompatible' | 'unknown'

function version(value: string): Version | null {
  return /^\d+(?:[._]\d+){0,2}$/.test(value) ? value.split(/[._]/).map(Number) : null
}

function compare(a: Version, b: Version) {
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    const difference = (a[i] ?? 0) - (b[i] ?? 0)
    if (difference) return difference
  }
  return 0
}

// iPadOS presents itself as a Mac; only its touch screen tells them apart.
function ios(userAgent: string, touchPoints: number) {
  return /iPhone|iPad|iPod/.test(userAgent) || (/Macintosh/.test(userAgent) && touchPoints > 1)
}

export function iosSafari(userAgent: string, touchPoints: number) {
  return (
    ios(userAgent, touchPoints) &&
    /Version\/[\d.]+.*Safari\//.test(userAgent) &&
    !/CriOS|FxiOS|EdgiOS|OPiOS|DuckDuckGo|YaBrowser|GSA\//.test(userAgent)
  )
}

export function deviceOs(userAgent: string, touchPoints: number): OsRange | null {
  if (!ios(userAgent, touchPoints)) return null
  const safari = /Version\/(\d+)/.exec(userAgent)
  const nativeSafari = /Safari\//.test(userAgent) && !/CriOS|FxiOS|EdgiOS|OPiOS/.test(userAgent)
  // Safari 26+ freezes the OS token. Its version establishes a lower bound,
  // but cannot reliably establish the installed OS's minor or patch version.
  if (nativeSafari && safari && Number(safari[1]) >= 26) return { minimum: [26] }
  const reported = /(?:CPU(?: iPhone)? OS|iPhone OS) ([\d_]+)/.exec(userAgent)
  const minimum = reported && version(reported[1]!)
  if (!minimum) return null
  // Other iOS browsers and embedded views may also inherit a frozen token.
  if (minimum[0] === 18 && (minimum[1] ?? 0) >= 6 && (!nativeSafari || !safari)) return { minimum }
  const before = [...minimum]
  before[before.length - 1]!++
  return { minimum, before }
}

export function osCompatibility(required: string, device: OsRange | null): OsCompatibility {
  const minimum = version(required)
  if (!minimum || !device) return 'unknown'
  if (compare(device.minimum, minimum) >= 0) return 'compatible'
  if (device.before && compare(device.before, minimum) <= 0) return 'incompatible'
  return 'unknown'
}
