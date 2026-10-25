/**
 * HumanVerification: the block a send renders when the deployment requires
 * a human-verification proof.
 *
 * Presentational only: the block renders the mechanism the deployment
 * named and reports what it obtains upward. The proof-of-work solve and
 * the proof's lifetime belong to the host (auth-core's
 * `useHumanVerification`), so this component carries no request, no
 * session and no business rule; a host that runs another mechanism gates
 * its send with its own state and renders nothing here.
 *
 *  - `off` (or a gate that requires nothing) renders nothing.
 *  - `turnstile` renders Cloudflare's widget in explicit-render mode and
 *    yields the token it calls back with; an expired or failed widget
 *    yields null. Once the host consumes a token and clears `proof`, the
 *    widget is reset so the next send can obtain a fresh one.
 *  - `pow` shows the work in progress and then its outcome. The solve is
 *    the host's; `onRetry` asks for another attempt after a failure.
 *
 * The gate is the wire shape the discovery document carries
 * (`site_key`), so a host can pass the document's `human` member
 * unmodified.
 */

import { useEffect, useRef } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import CircularProgress from '@mui/material/CircularProgress'
import Typography from '@mui/material/Typography'
import { useUiKitTranslation } from '../internal/translation.js'

/** The discovery document's human-verification gate. */
export interface HumanVerificationGate {
  /** Whether every send of a verification message must present a proof. */
  readonly required: boolean
  /** The mechanism: `off`, `pow` or `turnstile`. */
  readonly provider: string
  /** The public widget key a Turnstile gate renders with. */
  readonly site_key?: string
}

/** The proof state the host holds. */
export type HumanVerificationStatus = 'idle' | 'working' | 'ready' | 'failed'

export interface HumanVerificationProps {
  /** The deployment's gate; absent, off or not required renders nothing. */
  readonly gate?: HumanVerificationGate
  /** The token or proof the host currently holds, or null. */
  readonly proof?: string | null
  /** The proof-of-work state; the token mechanism ignores it. */
  readonly status?: HumanVerificationStatus
  /** Receives a Turnstile token, or null when the widget expires or
   * fails. */
  readonly onToken: (token: string | null) => void
  /** Asked for another proof-of-work attempt after a failure. */
  readonly onRetry?: () => void
  /** Disables the widget while the surrounding send is in flight. */
  readonly disabled?: boolean
}

/** Cloudflare's explicit-render script. */
const TURNSTILE_SCRIPT_SRC =
  'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit'

/** The narrow Turnstile surface this component calls. */
interface TurnstileApi {
  render(
    container: HTMLElement,
    options: {
      sitekey: string
      callback: (token: string) => void
      'expired-callback'?: () => void
      'error-callback'?: () => void
      theme?: 'light' | 'dark' | 'auto'
    },
  ): string
  reset(widgetId: string): void
  remove(widgetId: string): void
}

declare global {
  interface Window {
    turnstile?: TurnstileApi
  }
}

/** One script load per page, shared by every block on it. */
let turnstileLoader: Promise<TurnstileApi> | null = null

/** Load Cloudflare's widget API, or reuse the one the page already has. */
function loadTurnstile(): Promise<TurnstileApi> {
  const existing = typeof window === 'undefined' ? undefined : window.turnstile
  if (existing !== undefined) {
    return Promise.resolve(existing)
  }
  if (turnstileLoader !== null) {
    return turnstileLoader
  }
  turnstileLoader = new Promise<TurnstileApi>((resolve, reject) => {
    const script = document.createElement('script')
    script.src = TURNSTILE_SCRIPT_SRC
    script.async = true
    script.defer = true
    script.addEventListener('load', () => {
      const api = window.turnstile
      if (api === undefined) {
        turnstileLoader = null
        reject(new Error('the Turnstile script loaded without its API'))
        return
      }
      resolve(api)
    })
    script.addEventListener('error', () => {
      turnstileLoader = null
      reject(new Error('the Turnstile script could not be loaded'))
    })
    document.head.appendChild(script)
  })
  return turnstileLoader
}

/** The Turnstile widget. Its token is single-use, so the widget is reset
 * once the host clears the proof it consumed. */
function TurnstileBlock({
  siteKey,
  proof,
  onToken,
  disabled,
}: {
  readonly siteKey: string
  readonly proof: string | null
  readonly onToken: (token: string | null) => void
  readonly disabled: boolean
}) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const widgetRef = useRef<string | null>(null)
  const previousProof = useRef(proof)
  // The host's callback behind a ref: a parent that re-creates the
  // function on every render must not tear the widget down and render a
  // second one.
  const onTokenRef = useRef(onToken)
  onTokenRef.current = onToken

  useEffect(() => {
    const container = containerRef.current
    if (container === null || siteKey === '') {
      return undefined
    }
    let active = true
    void loadTurnstile()
      .then((api) => {
        if (!active || containerRef.current !== container) {
          return
        }
        widgetRef.current = api.render(container, {
          sitekey: siteKey,
          callback: (token) => {
            onTokenRef.current(token === '' ? null : token)
          },
          'expired-callback': () => {
            onTokenRef.current(null)
          },
          'error-callback': () => {
            onTokenRef.current(null)
          },
          theme: 'auto',
        })
      })
      .catch(() => {
        // The widget cannot render (the script was blocked or failed). The
        // host keeps the send disabled while it holds no token; no text is
        // invented for a mechanism the page cannot reach.
      })
    return () => {
      active = false
      const api = window.turnstile
      const widget = widgetRef.current
      widgetRef.current = null
      if (api !== undefined && widget !== null) {
        api.remove(widget)
      }
    }
  }, [siteKey])

  useEffect(() => {
    if (previousProof.current !== null && proof === null) {
      const api = window.turnstile
      const widget = widgetRef.current
      if (api !== undefined && widget !== null) {
        api.reset(widget)
      }
    }
    previousProof.current = proof
  }, [proof])

  return (
    <Box
      ref={containerRef}
      sx={{
        width: '100%',
        pointerEvents: disabled ? 'none' : undefined,
        opacity: disabled ? 0.6 : undefined,
      }}
    />
  )
}

/** The proof-of-work progress and its failure's retry. */
function PowBlock({
  status,
  onRetry,
  disabled,
}: {
  readonly status: HumanVerificationStatus
  readonly onRetry?: () => void
  readonly disabled: boolean
}) {
  const { t } = useUiKitTranslation()
  if (status === 'failed') {
    return (
      <Box
        sx={{
          display: 'flex',
          alignItems: 'center',
          gap: 1.5,
          flexWrap: 'wrap',
        }}
      >
        <Typography variant="body2" color="error" role="alert">
          {t('humanVerification.failed')}
        </Typography>
        <Button
          type="button"
          size="small"
          variant="outlined"
          disabled={disabled}
          onClick={onRetry}
        >
          {t('humanVerification.retry')}
        </Button>
      </Box>
    )
  }
  const working = status !== 'ready'
  return (
    <Box
      role="status"
      sx={{ display: 'flex', alignItems: 'center', gap: 1.5, width: '100%' }}
    >
      {working ? (
        <CircularProgress size={16} aria-hidden={true} />
      ) : null}
      <Typography variant="body2" color="text.secondary">
        {working
          ? t('humanVerification.working')
          : t('humanVerification.verified')}
      </Typography>
    </Box>
  )
}

/**
 * The human-verification block: nothing when the gate requires none,
 * Cloudflare's widget for `turnstile`, and the solve's state for `pow`.
 */
export function HumanVerification({
  gate,
  proof = null,
  status = 'idle',
  onToken,
  onRetry,
  disabled = false,
}: HumanVerificationProps) {
  if (gate === undefined || !gate.required || gate.provider === 'off') {
    return null
  }
  if (gate.provider === 'turnstile') {
    return (
      <TurnstileBlock
        siteKey={gate.site_key ?? ''}
        proof={proof}
        onToken={onToken}
        disabled={disabled}
      />
    )
  }
  if (gate.provider === 'pow') {
    return <PowBlock status={status} onRetry={onRetry} disabled={disabled} />
  }
  return null
}
