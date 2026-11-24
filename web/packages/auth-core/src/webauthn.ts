/**
 * webauthn: the passkey ceremony of a flow step. Drives
 * navigator.credentials.create/get and serializes the credential into the answer
 * JSON, decoding option fields in and re-encoding the credential out.
 */

/** What a `webauthn` step's options ask the client to do. */
export type WebauthnStep = 'totp' | 'register' | 'assert'

/** Why a ceremony did not produce a credential. */
export type WebauthnFailure =
  | 'unsupported' // no WebAuthn here, or the page is not a secure context
  | 'cancelled' // the user dismissed the prompt, or the ceremony timed out
  | 'already-registered' // the authenticator already holds an excluded credential
  | 'failed' // the ceremony itself failed

/** A ceremony that did not produce a credential. */
export class WebauthnError extends Error {
  readonly reason: WebauthnFailure

  constructor(reason: WebauthnFailure, message: string) {
    super(message)
    this.name = 'WebauthnError'
    this.reason = reason
  }
}

/** Read a step's options: TOTP's otpauth_uri, a registration's creation options, or an assertion's request options. */
export function webauthnStepOf(options: unknown): WebauthnStep {
  if (typeof options !== 'object' || options === null) {
    return 'assert'
  }
  const record = options as Record<string, unknown>
  if ('otpauth_uri' in record) {
    return 'totp'
  }
  return 'user' in record ? 'register' : 'assert'
}

/**
 * Drives the ceremony a step's options describe and returns the credential JSON.
 * Throws WebauthnError for a TOTP step, an unavailable API, cancellation, an
 * already-registered authenticator or a failed ceremony; `signal` is best effort.
 */
export async function runWebauthn(
  options: unknown,
  signal?: AbortSignal,
): Promise<string> {
  const step = webauthnStepOf(options)
  if (step === 'totp') {
    throw new WebauthnError('failed', 'the step is a TOTP enrollment, not a passkey ceremony')
  }
  const credentials = (
    globalThis.navigator as { credentials?: CredentialsContainer } | undefined
  )?.credentials
  if (credentials === undefined || typeof credentials.create !== 'function') {
    throw new WebauthnError('unsupported', 'WebAuthn is unavailable in this browser')
  }
  const abortable = signal === undefined ? {} : { signal }
  try {
    const credential =
      step === 'register'
        ? await credentials.create({
            ...abortable,
            publicKey: decodeCreation(options),
          })
        : await credentials.get({
            ...abortable,
            publicKey: decodeRequest(options),
          })
    if (credential === null) {
      throw new WebauthnError('cancelled', 'no credential was produced')
    }
    const publicKey = credential as PublicKeyCredential
    return step === 'register'
      ? serializeAttestation(publicKey)
      : serializeAssertion(publicKey)
  } catch (error) {
    throw toWebauthnError(error)
  }
}

type EncodedCreation = {
  challenge?: unknown
  user?: { id?: unknown } & Record<string, unknown>
  excludeCredentials?: readonly ({ id?: unknown } & Record<string, unknown>)[]
  [key: string]: unknown
}

type EncodedRequest = {
  challenge?: unknown
  allowCredentials?: readonly ({ id?: unknown } & Record<string, unknown>)[]
  [key: string]: unknown
}

function decodeCreation(options: unknown): PublicKeyCredentialCreationOptions {
  const { challenge, user, excludeCredentials, ...rest } = (options ?? {}) as EncodedCreation
  const decoded = {
    ...rest,
    challenge: toBuffer(challenge, 'challenge'),
    user: { ...(user ?? {}), id: toBuffer(user?.id, 'user.id') },
    excludeCredentials: (excludeCredentials ?? []).map((c) => ({
      ...c,
      id: toBuffer(c.id, 'excludeCredentials.id'),
    })),
  }
  return decoded as unknown as PublicKeyCredentialCreationOptions
}

function decodeRequest(options: unknown): PublicKeyCredentialRequestOptions {
  const { challenge, allowCredentials, ...rest } = (options ?? {}) as EncodedRequest
  const decoded = {
    ...rest,
    challenge: toBuffer(challenge, 'challenge'),
    allowCredentials: (allowCredentials ?? []).map((c) => ({
      ...c,
      id: toBuffer(c.id, 'allowCredentials.id'),
    })),
  }
  return decoded as unknown as PublicKeyCredentialRequestOptions
}

function serializeAttestation(credential: PublicKeyCredential): string {
  const response = credential.response as AuthenticatorAttestationResponse
  return JSON.stringify({
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      attestationObject: bufferToBase64url(response.attestationObject),
      transports: response.getTransports?.() ?? [],
    },
    clientExtensionResults: credential.getClientExtensionResults(),
    authenticatorAttachment: credential.authenticatorAttachment ?? undefined,
  })
}

function serializeAssertion(credential: PublicKeyCredential): string {
  const response = credential.response as AuthenticatorAssertionResponse
  return JSON.stringify({
    id: credential.id,
    rawId: bufferToBase64url(credential.rawId),
    type: credential.type,
    response: {
      clientDataJSON: bufferToBase64url(response.clientDataJSON),
      authenticatorData: bufferToBase64url(response.authenticatorData),
      signature: bufferToBase64url(response.signature),
      userHandle:
        response.userHandle === null ? undefined : bufferToBase64url(response.userHandle),
    },
    clientExtensionResults: credential.getClientExtensionResults(),
    authenticatorAttachment: credential.authenticatorAttachment ?? undefined,
  })
}

function toBuffer(value: unknown, field: string): ArrayBuffer {
  if (typeof value !== 'string') {
    throw new WebauthnError('failed', `the ${field} is not a base64url string`)
  }
  return base64urlToBuffer(value)
}

function base64urlToBuffer(value: string): ArrayBuffer {
  const base64 = value.replace(/-/g, '+').replace(/_/g, '/')
  const pad = (4 - (base64.length % 4)) % 4
  const binary = atob(base64 + '='.repeat(pad))
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes.buffer
}

function bufferToBase64url(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let binary = ''
  for (let i = 0; i < bytes.length; i += 1) {
    binary += String.fromCharCode(bytes[i] ?? 0)
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

/**
 * Classifies a thrown value as the ceremony's answer. The name is read off any
 * object, since a dismissed prompt is a DOMException.
 */
function toWebauthnError(error: unknown): WebauthnError {
  if (error instanceof WebauthnError) {
    return error
  }
  const name =
    typeof error === 'object' && error !== null && 'name' in error
      ? String((error as { name: unknown }).name)
      : ''
  const message = error instanceof Error ? error.message : String(error)
  if (name === 'NotAllowedError' || name === 'AbortError') {
    return new WebauthnError('cancelled', message)
  }
  if (name === 'NotSupportedError' || name === 'SecurityError') {
    return new WebauthnError('unsupported', message)
  }
  // A creation whose excludeCredentials matches an existing credential is rejected this way.
  if (name === 'InvalidStateError') {
    return new WebauthnError('already-registered', message)
  }
  return new WebauthnError('failed', message)
}
