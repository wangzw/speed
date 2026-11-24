/**
 * The human-verification block a send renders: presentational only, it
 * renders the mechanism the deployment named and reports what it obtains.
 * Focus never moves into it; the initiating control keeps and recovers it.
 */

import {
  Fragment,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
} from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import CircularProgress from '@mui/material/CircularProgress'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useUiKitTranslation } from '../internal/translation.js'
import { visuallyHiddenSx } from '../visually-hidden.js'
import { ActionButton } from './ActionRow.js'
import { AnnouncementRegion } from './AnnouncementRegion.js'

/** The surface a block serves. */
export type HumanVerificationScope = 'phone' | 'other'

/** The mechanism one surface runs, as the discovery document reports it. */
export interface HumanVerificationGate {
  /** Whether every send on this surface must present a proof. */
  readonly required: boolean
  /** The mechanism: `off`, `pow`, `captcha` or `turnstile`. */
  readonly provider: string
  /** The public widget key a Turnstile gate renders with. */
  readonly site_key?: string
  /** The action a Turnstile gate renders its widget with. */
  readonly action?: string
}

/** The discovery document's whole gate, one member per surface. */
export interface HumanVerificationGateSet {
  /** The gate for the phone surface. */
  readonly phone: HumanVerificationGate
  /** The gate for every other surface. */
  readonly other: HumanVerificationGate
}

/** The proof state the host holds. */
export type HumanVerificationStatus = 'idle' | 'working' | 'ready' | 'failed'

/** The form a self-hosted captcha challenge takes. */
export type HumanCaptchaForm = 'image' | 'slider' | 'audio'

/** One issued self-hosted captcha challenge, as the host read it. */
export interface HumanCaptchaChallenge {
  /** The challenge id a proof names, base64url without padding. */
  readonly id: string
  /** The form the challenge takes. */
  readonly form: HumanCaptchaForm
  /** The stable instruction key the server named; the block words it. */
  readonly instruction: string
  /** The digit picture of an image challenge, when the form is `image`. */
  readonly image_url?: string
  /** The background of a slider challenge, when the form is `slider`. */
  readonly background_url?: string
  /** The piece a slider challenge slides over its background. */
  readonly piece_url?: string
  /** The spoken digits of an audio challenge, when the form is `audio`. */
  readonly audio_url?: string
  /** How long the challenge stays valid, in whole seconds; a proof naming an expired challenge is refused. */
  readonly expires_in: number
}

export interface HumanVerificationProps {
  /** The surface this block serves. */
  readonly scope: HumanVerificationScope
  /** The deployment's gate; absent, off or not required renders nothing. */
  readonly gate?: HumanVerificationGateSet
  /** The token or proof the host currently holds, or null. */
  readonly proof?: string | null
  /** The mechanism's state; the token mechanism ignores it. */
  readonly status?: HumanVerificationStatus
  /** The host's issued captcha challenge, or null while none is loaded. */
  readonly captcha?: HumanCaptchaChallenge | null
  /** Receives a Turnstile token or captcha proof, or null when the widget expires or fails. */
  readonly onToken: (token: string | null) => void
  /** Asked for another attempt after a failure. */
  readonly onRetry?: () => void
  /** Asked for the next challenge in the selection in force. */
  readonly onRefreshCaptcha?: () => void
  /** Asked for the audio alternative explicitly. */
  readonly onRequestAudio?: () => void
  /** A refused accessibility request's stated wait; the entry is held while it is set. */
  readonly audioRetryAfterSeconds?: number
  /** Disables the widget or form while the surrounding send is in flight. */
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
      action?: string
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

/** A subtree in which an element cannot take focus. */
const UNFOCUSABLE_ANCESTOR = '[hidden], [inert]'

/** Hands focus back to the initiator when it is still in the document and focusable. */
function restoreInitiator(initiator: HTMLElement | null): void {
  if (initiator === null || !initiator.isConnected) {
    return
  }
  if (initiator.closest(UNFOCUSABLE_ANCESTOR) !== null) {
    return
  }
  initiator.focus()
}

/**
 * The block's one named group. It records the control that held focus
 * outside it, continues at the container when the block's own state change
 * removed the focused control, and hands focus back when the block goes away
 * with the viewer still inside.
 */
function HumanVerificationFrame({
  children,
}: {
  readonly children: ReactNode
}) {
  const { t } = useUiKitTranslation()
  const containerRef = useRef<HTMLDivElement | null>(null)
  const initiatorRef = useRef<HTMLElement | null>(null)
  const lastFocusInsideRef = useRef<HTMLElement | null>(null)
  const focusEscapedRef = useRef(false)

  // Recorded before any child effect moves focus (the audio player).
  useLayoutEffect(() => {
    const container = containerRef.current
    const active = document.activeElement
    if (container === null || !(active instanceof HTMLElement)) {
      return
    }
    if (active === document.body || container.contains(active)) {
      return
    }
    initiatorRef.current = active
  }, [])

  // Track focus inside the block, and whether it has since moved outside.
  useEffect(() => {
    const container = containerRef.current
    if (container === null) {
      return undefined
    }
    const onFocusIn = (event: FocusEvent): void => {
      const target = event.target
      if (!(target instanceof HTMLElement)) {
        return
      }
      if (container.contains(target)) {
        lastFocusInsideRef.current = target
        focusEscapedRef.current = false
      } else if (target !== document.body) {
        focusEscapedRef.current = true
      }
    }
    document.addEventListener('focusin', onFocusIn)
    return () => {
      document.removeEventListener('focusin', onFocusIn)
    }
  }, [])

  // The block's own state change removed the focused control: continue at the container.
  useLayoutEffect(() => {
    const container = containerRef.current
    const last = lastFocusInsideRef.current
    if (container === null || last === null || last.isConnected) {
      return
    }
    if (document.activeElement !== document.body || focusEscapedRef.current) {
      return
    }
    container.focus()
  })

  // The block is going away: hand focus back unless the viewer moved elsewhere.
  useLayoutEffect(() => {
    return () => {
      const last = lastFocusInsideRef.current
      if (last === null) {
        return
      }
      const active = document.activeElement
      const heldFocus =
        active === last ||
        (active === document.body &&
          !last.isConnected &&
          !focusEscapedRef.current)
      if (!heldFocus) {
        return
      }
      restoreInitiator(initiatorRef.current)
    }
  }, [])

  return (
    <Box
      ref={containerRef}
      role="group"
      aria-label={t('humanVerification.groupLabel')}
      tabIndex={-1}
      sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, width: '100%' }}
    >
      <AnnouncementRegion
        message={t('humanVerification.required')}
        sx={visuallyHiddenSx}
      />
      {children}
    </Box>
  )
}

/** The token is single-use, so the widget is reset once the host clears the consumed proof. */
function TurnstileBlock({
  siteKey,
  action,
  proof,
  onToken,
  disabled,
}: {
  readonly siteKey: string
  readonly action: string | undefined
  readonly proof: string | null
  readonly onToken: (token: string | null) => void
  readonly disabled: boolean
}) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const widgetRef = useRef<string | null>(null)
  const previousProof = useRef(proof)
  // Behind a ref: a parent that re-creates the callback must not re-render the widget.
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
          ...(action === undefined || action === '' ? {} : { action }),
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
        // The blocked script leaves the host without a token; no text is invented.
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
  }, [siteKey, action])

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
        <ActionButton
          type="button"
          size="small"
          unavailable={disabled}
          onClick={onRetry}
        >
          {t('humanVerification.retry')}
        </ActionButton>
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
      <Typography
        variant={working ? 'body2' : 'caption'}
        color="text.secondary"
      >
        {working
          ? t('humanVerification.working')
          : t('humanVerification.verified')}
      </Typography>
    </Box>
  )
}

/**
 * The instruction a challenge's stable key names. An unknown key falls back
 * to the generic sentence rather than showing a raw key.
 */
function instructionText(
  instruction: string,
  t: (key: string) => string,
): string {
  switch (instruction) {
    case 'type-the-digits':
      return t('humanVerification.captcha.instruction.typeTheDigits')
    case 'slide-the-piece':
      return t('humanVerification.captcha.instruction.slideThePiece')
    case 'type-the-digits-you-hear':
      return t('humanVerification.captcha.instruction.typeTheDigitsYouHear')
    default:
      return t('humanVerification.captcha.instruction.fallback')
  }
}

/** The media size of one image, read when the browser decodes it. */
interface ImageSize {
  readonly width: number
  readonly height: number
}

/**
 * The slider form. The range value is the piece's offset in the
 * background's natural pixels, which is the answer the server compares; the
 * piece is drawn at the same scale as the background.
 */
function CaptchaSlider({
  challenge,
  disabled,
  onAnswer,
}: {
  readonly challenge: HumanCaptchaChallenge
  readonly disabled: boolean
  readonly onAnswer: (answer: string) => void
}) {
  const { t } = useUiKitTranslation()
  const rangeId = useId()
  const [background, setBackground] = useState<ImageSize | null>(null)
  const [piece, setPiece] = useState<ImageSize | null>(null)
  const [offset, setOffset] = useState(0)
  // jsdom has no layout, so the unit tier sees the natural width.
  const frameRef = useRef<HTMLDivElement | null>(null)
  const [renderedWidth, setRenderedWidth] = useState<number | null>(null)
  useEffect(() => {
    const frame = frameRef.current
    if (frame === null) {
      return undefined
    }
    const measure = (): void => {
      setRenderedWidth(frame.getBoundingClientRect().width)
    }
    measure()
    if (typeof ResizeObserver === 'undefined') {
      return undefined
    }
    const observer = new ResizeObserver(measure)
    observer.observe(frame)
    return () => observer.disconnect()
  }, [background])
  // Until the image loads, the natural size is drawn.
  const scale =
    background !== null && renderedWidth !== null && renderedWidth > 0
      ? renderedWidth / background.width
      : 1
  const maxOffset =
    background !== null && piece !== null
      ? Math.max(0, background.width - piece.width)
      : 0
  const pieceTop =
    background !== null && piece !== null
      ? Math.max(0, Math.round((background.height - piece.height) / 2))
      : 0
  const instruction = instructionText(challenge.instruction, t)
  // A native range has no read-only semantics, so `held` is aria-disabled plus guarded handlers.
  const held = disabled || maxOffset === 0
  const answer = (): void => {
    if (held) {
      return
    }
    onAnswer(String(offset))
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1.5 }}>
      <Typography variant="body2" id={`${rangeId}-instruction`}>
        {instruction}
      </Typography>
      <Box
        ref={frameRef}
        sx={{
          position: 'relative',
          width: background?.width ?? 320,
          maxWidth: '100%',
          overflow: 'hidden',
          borderRadius: 1,
          bgcolor: 'action.hover',
        }}
      >
        <Box
          component="img"
          src={challenge.background_url}
          alt=""
          onLoad={(event) =>
            setBackground({
              width: event.currentTarget.naturalWidth,
              height: event.currentTarget.naturalHeight,
            })
          }
          sx={{ display: 'block', width: '100%' }}
        />
        <Box
          component="img"
          src={challenge.piece_url}
          alt=""
          onLoad={(event) =>
            setPiece({
              width: event.currentTarget.naturalWidth,
              height: event.currentTarget.naturalHeight,
            })
          }
          sx={{
            position: 'absolute',
            left: offset * scale,
            top: pieceTop * scale,
            width: piece === null ? undefined : piece.width * scale,
            height: piece === null ? undefined : piece.height * scale,
            filter: 'drop-shadow(0 1px 2px rgba(0, 0, 0, 0.5))',
          }}
        />
      </Box>
      <Box
        component="label"
        htmlFor={rangeId}
        sx={{ fontSize: 'body2.fontSize' }}
      >
        {t('humanVerification.captcha.sliderLabel')}
      </Box>
      <Box
        component="input"
        id={rangeId}
        type="range"
        min={0}
        max={maxOffset}
        step={1}
        value={offset}
        // Not a native `disabled`: that would drop the range out of the tab order.
        aria-disabled={held ? true : undefined}
        aria-describedby={`${rangeId}-instruction`}
        aria-keyshortcuts="Enter"
        onChange={(event) => {
          if (held) {
            return
          }
          setOffset(Number(event.currentTarget.value))
        }}
        onPointerUp={answer}
        onKeyDown={(event) => {
          if (event.key === 'Enter') {
            event.preventDefault()
            answer()
          }
        }}
        // The track spans the image frame so drag distance matches the piece's travel.
        // `accentColor` is not an sx system prop: the callback is what makes it theme-aware.
        sx={{
          width: background?.width ?? 320,
          maxWidth: '100%',
          accentColor: (theme) => theme.palette.primary.main,
        }}
      />
    </Box>
  )
}

/** Keyed by the challenge id, so a fresh challenge resets the previous answer. */
function CaptchaForm({
  scope,
  challenge,
  disabled,
  onToken,
}: {
  readonly scope: HumanVerificationScope
  readonly challenge: HumanCaptchaChallenge
  readonly disabled: boolean
  readonly onToken: (token: string | null) => void
}) {
  const { t, i18n } = useUiKitTranslation()
  const answerId = `human-verification-${scope}-${useId()}`
  const [answer, setAnswer] = useState('')
  const instruction = instructionText(challenge.instruction, t)
  // The audio player takes focus when it appears; its `lang` is the active locale.
  const audioRef = useRef<HTMLAudioElement | null>(null)
  useEffect(() => {
    if (challenge.form === 'audio') {
      audioRef.current?.focus()
    }
  }, [challenge.form])
  const audioLanguage = i18n.resolvedLanguage ?? i18n.language
  const submit = (): void => {
    const value = answer.trim()
    if (value === '' || disabled) {
      return
    }
    onToken(`${challenge.id}.${value}`)
  }

  return (
    <Box
      sx={{ display: 'flex', flexDirection: 'column', gap: 1.5, width: '100%' }}
    >
      {challenge.form === 'image' && challenge.image_url !== undefined ? (
        <Box
          component="img"
          src={challenge.image_url}
          alt={instruction}
          sx={{ display: 'block', maxWidth: '100%', borderRadius: 1 }}
        />
      ) : null}
      {challenge.form === 'audio' && challenge.audio_url !== undefined ? (
        <Box
          component="audio"
          ref={audioRef}
          controls={true}
          src={challenge.audio_url}
          lang={audioLanguage}
          tabIndex={0}
          aria-label={t('humanVerification.captcha.audioLabel')}
          sx={{ width: '100%' }}
        />
      ) : null}
      {challenge.form === 'slider' ? (
        <CaptchaSlider
          challenge={challenge}
          disabled={disabled}
          onAnswer={(value) => {
            if (!disabled) {
              onToken(`${challenge.id}.${value}`)
            }
          }}
        />
      ) : (
        <>
          <Typography variant="body2" id={`${answerId}-instruction`}>
            {instruction}
          </Typography>
          <TextField
            id={answerId}
            label={t('humanVerification.captcha.answerLabel')}
            value={answer}
            autoComplete="off"
            slotProps={{
              // Read-only, not disabled. It must go through the input slot: the
              // top-level prop would only reach FormControl.
              input: { readOnly: disabled },
              htmlInput: { 'aria-describedby': `${answerId}-instruction` },
            }}
            onChange={(event) => setAnswer(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault()
                submit()
              }
            }}
            fullWidth
          />
          <ActionButton
            type="button"
            size="small"
            unavailable={disabled || answer.trim() === ''}
            unavailableDescribedBy={
              disabled || answer.trim() !== ''
                ? undefined
                : `${answerId}-instruction`
            }
            onClick={submit}
          >
            {t('humanVerification.captcha.submit')}
          </ActionButton>
        </>
      )}
    </Box>
  )
}

/**
 * The refresh and audio entries, outside the keyed form, so a swap keeps the
 * control that asked for it mounted and busy.
 */
function CaptchaActions({
  form,
  disabled,
  switching,
  pendingAction,
  audioWaitId,
  audioWait,
  onRequestAudio,
  onRefresh,
}: {
  readonly form: HumanCaptchaForm | null
  readonly disabled: boolean
  readonly switching: boolean
  readonly pendingAction: 'refresh' | 'audio' | null
  readonly audioWaitId: string
  readonly audioWait: number | undefined
  readonly onRequestAudio: () => void
  readonly onRefresh: () => void
}) {
  const { t } = useUiKitTranslation()
  const audioBusy = switching && pendingAction === 'audio'
  const refreshBusy = switching && pendingAction === 'refresh'
  const audioHeld = disabled || switching || audioWait !== undefined
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
      <Box sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
        {/* The audio entry is the one form choice the block offers. */}
        {form !== 'audio' ? (
          <ActionButton
            type="button"
            size="small"
            busy={audioBusy}
            unavailable={audioHeld}
            unavailableDescribedBy={
              !disabled && !switching && audioWait !== undefined
                ? audioWaitId
                : undefined
            }
            onClick={onRequestAudio}
          >
            {t('humanVerification.captcha.useAudio')}
          </ActionButton>
        ) : null}
        <ActionButton
          type="button"
          size="small"
          busy={refreshBusy}
          // A swap in flight holds every entry but the one that started it.
          unavailable={disabled || (switching && pendingAction !== 'refresh')}
          onClick={onRefresh}
        >
          {t('humanVerification.captcha.refresh')}
        </ActionButton>
      </Box>
      {audioWait !== undefined ? (
        <Typography
          variant="body2"
          color="text.secondary"
          id={audioWaitId}
          role="status"
        >
          {t('humanVerification.captcha.audioWait', {
            seconds: audioWait,
          })}
        </Typography>
      ) : null}
    </Box>
  )
}

/** The challenge, the issue in progress or its retry, and the verified state. */
function CaptchaBlock({
  scope,
  challenge,
  proof,
  status,
  onToken,
  onRetry,
  onRefresh,
  onRequestAudio,
  audioRetryAfterSeconds,
  disabled,
}: {
  readonly scope: HumanVerificationScope
  readonly challenge: HumanCaptchaChallenge | null
  readonly proof: string | null
  readonly status: HumanVerificationStatus
  readonly onToken: (token: string | null) => void
  readonly onRetry?: () => void
  readonly onRefresh?: () => void
  readonly onRequestAudio?: () => void
  readonly audioRetryAfterSeconds?: number
  readonly disabled: boolean
}) {
  const { t } = useUiKitTranslation()
  const audioWaitId = useId()
  // Outlives a swap, so the audio entry keeps its place while the next challenge loads.
  const [shownForm, setShownForm] = useState<HumanCaptchaForm | null>(null)
  // Which of the two entries asked for the swap in flight.
  const [pendingAction, setPendingAction] = useState<
    'refresh' | 'audio' | null
  >(null)
  if (challenge !== null && challenge.form !== shownForm) {
    setShownForm(challenge.form)
  }
  // Keyed on the id, so a host that re-creates the object does not clear the state mid-swap.
  const challengeId = challenge?.id ?? null
  useEffect(() => {
    if (challengeId !== null || proof !== null || status === 'failed') {
      setPendingAction(null)
    }
  }, [challengeId, proof, status])
  const switching =
    challenge === null && status === 'working' && shownForm !== null
  const audioWait =
    audioRetryAfterSeconds === undefined || audioRetryAfterSeconds <= 0
      ? undefined
      : audioRetryAfterSeconds

  let content: ReactNode
  if (proof !== null) {
    content = (
      <Box
        role="status"
        sx={{ display: 'flex', alignItems: 'center', gap: 1.5, width: '100%' }}
      >
        <Typography variant="caption" color="text.secondary">
          {t('humanVerification.verified')}
        </Typography>
      </Box>
    )
  } else if (challenge === null) {
    content =
      status === 'failed' ? (
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
          <ActionButton
            type="button"
            size="small"
            unavailable={disabled}
            onClick={onRetry}
          >
            {t('humanVerification.retry')}
          </ActionButton>
        </Box>
      ) : (
        <Box
          role="status"
          sx={{ display: 'flex', alignItems: 'center', gap: 1.5, width: '100%' }}
        >
          <CircularProgress size={16} aria-hidden={true} />
          <Typography variant="body2" color="text.secondary">
            {t('humanVerification.working')}
          </Typography>
        </Box>
      )
  } else {
    content = (
      <CaptchaForm
        key={`${challenge.id}:${challenge.form}`}
        scope={scope}
        challenge={challenge}
        disabled={disabled}
        onToken={onToken}
      />
    )
  }

  // The entries keep their slot across every state, so a control that held focus is not rebuilt.
  return (
    <>
      <Fragment key="content">{content}</Fragment>
      {proof === null && (challenge !== null || switching) ? (
        <CaptchaActions
          key="actions"
          form={shownForm}
          disabled={disabled}
          switching={switching}
          pendingAction={pendingAction}
          audioWaitId={audioWaitId}
          audioWait={audioWait}
          onRequestAudio={() => {
            setPendingAction('audio')
            onRequestAudio?.()
          }}
          onRefresh={() => {
            setPendingAction('refresh')
            onRefresh?.()
          }}
        />
      ) : null}
    </>
  )
}

/** Nothing when the surface's gate requires no proof, else the named mechanism's block. */
export function HumanVerification({
  scope,
  gate,
  proof = null,
  status = 'idle',
  captcha = null,
  onToken,
  onRetry,
  onRefreshCaptcha,
  onRequestAudio,
  audioRetryAfterSeconds,
  disabled = false,
}: HumanVerificationProps) {
  const surface = gate?.[scope]
  if (surface === undefined || !surface.required || surface.provider === 'off') {
    return null
  }
  let content: ReactNode
  if (surface.provider === 'turnstile') {
    content = (
      <TurnstileBlock
        siteKey={surface.site_key ?? ''}
        action={surface.action}
        proof={proof}
        onToken={onToken}
        disabled={disabled}
      />
    )
  } else if (surface.provider === 'pow') {
    content = (
      <PowBlock status={status} onRetry={onRetry} disabled={disabled} />
    )
  } else if (surface.provider === 'captcha') {
    content = (
      <CaptchaBlock
        scope={scope}
        challenge={captcha}
        proof={proof}
        status={status}
        onToken={onToken}
        onRetry={onRetry}
        onRefresh={onRefreshCaptcha}
        onRequestAudio={onRequestAudio}
        audioRetryAfterSeconds={audioRetryAfterSeconds}
        disabled={disabled}
      />
    )
  } else {
    return null
  }
  return <HumanVerificationFrame>{content}</HumanVerificationFrame>
}
