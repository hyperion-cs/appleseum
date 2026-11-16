// Only the server's verified challenge opens the code form. Unknown failures
// return to the password form; a rejected code can retry its existing challenge.
export function loginFailure(code: string) {
  const requested = code === 'two_factor_required'
  const retryCode = code === 'verification_code_rejected'
  return {
    twoFactor: requested || retryCode,
    keepPassword: requested || retryCode,
    errorCode: requested ? '' : code,
  }
}
