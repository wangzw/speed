/**
 * HumanVerification: the block a send renders when the deployment requires
 * a human-verification proof on its surface.
 *
 * Presentational only: the block renders the mechanism the deployment
 * named for `scope` and reports what it obtains upward. The proof-of-work
 * solve, the captcha challenge and the proof's lifetime belong to the host
 * (auth-core's `useHumanVerification`), so this component carries no
 * request, no session and no business rule; a host that runs another
 * mechanism gates its send with its own state and renders nothing here.
 *
 *  - `off` (or a gate that requires nothing) renders nothing.
 *  - `turnstile` renders Cloudflare's widget in explicit-render mode,
 *    under the action the gate names, and yields the token it calls back
 *    with; an expired or failed widget yields null. Once the host consumes
 *    a token and clears `proof`, the widget is reset so the next send can
 *    obtain a fresh one.
 *  - `pow` shows the work in progress and then its outcome. The solve is
 *    the host's; `onRetry` asks for another attempt after a failure.
 *  - `captcha` renders the self-hosted challenge the host issued: the
 *    image form shows the picture and an answer field, the slider form
 *    shows the background and piece behind a native range input so it is
 *    keyboard operable, and the audio form plays the spoken digits for a
 *    screen-reader user, moving focus to the player and naming its
 *    language. The server chooses between the image and slider forms, so
 *    the block offers no form choice: the one accessibility entry asks
 *    for the audio alternative, and a refusal that states a wait holds
 *    that entry and shows the wait. A refresh keeps the selection. The
 *    image and audio forms yield `<challenge id>.<answer>` from their
 *    answer field's submit (or Enter in it); the slider is the answer
 *    itself, yielding the same shape when it is released, or when Enter
 *    is pressed after the keyboard moved it. A failed issue offers one
 *    retry.
 *
 * The gate is the discovery document's `human` member (`{phone, other}`),
 * so a host passes it unmodified; `scope` names the surface this block
 * serves. The id prefix carries the scope, so two blocks on one page --
 * such as a step-up dialog over an account section -- never share the
 * answer field's id.
 *
 * Whatever the mechanism, the block renders inside one named group. It
 * never moves focus into itself: the control that asked while the proof
 * is pending keeps focus. It records that control when the group appears,
 * announces once (through the kit's delayed live region) that completing
 * the block continues on its own, keeps focus at the group when the
 * block's own state change removes the control that held it, and hands
 * focus back to the recorded control when the group goes away and the
 * viewer was still inside. The refresh and audio entries stand outside
 * the keyed challenge form so a swap keeps the control that asked for it
 * mounted and busy. The block's own buttons are ActionButtons: `busy`
 * while their action runs, `unavailable` when the host's send is in
 * flight or their precondition is unmet.
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
  readonly phone: HumanVerificationGate
  readonly other: HumanVerificationGate
}

/** The proof state the host holds. */
export type HumanVerificationStatus = 'idle' | 'working' | 'ready' | 'failed'

/** The form a self-hosted captcha challenge takes. */
export type HumanCaptchaForm = 'image' | 'slider' | 'audio'

/** One issued self-hosted captcha challenge, as the host read it. */
export interface HumanCaptchaChallenge {
  readonly id: string
  readonly form: HumanCaptchaForm
  /** The stable instruction key the server named; the block words it. */
  readonly instruction: string
  readonly image_url?: string
  readonly background_url?: string
  readonly piece_url?: string
  readonly audio_url?: string
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
  /** Receives a Turnstile token or a captcha proof, or null when the
   * widget expires or fails. */
  readonly onToken: (token: string | null) => void
  /** Asked for another attempt after a failure. */
  readonly onRetry?: () => void
  /** Asked for a fresh captcha challenge in the selection in force: the
   * server's rotation, or the audio alternative once it was chosen. */
  readonly onRefreshCaptcha?: () => void
  /** The accessibility entry: asked for the audio alternative explicitly. */
  readonly onRequestAudio?: () => void
  /** The wait a refused accessibility request stated, in whole seconds.
   * While it is set the entry is held and the wait is shown. */
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

/** Hands focus back to the control that asked for the block, when that
 * control is still in the document and still focusable. */
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
 * The block's one container: the named group every mechanism renders
 * inside, the announcement inserted a frame after it appears, and the
 * focus hand-off around it.
 *
 * The block never moves focus into itself. It records the control that
 * held focus outside it when the container appeared (an automatic send
 * has none, because focus is on the body), continues at this container
 * when a control inside it left the document because of the block's own
 * state change, and hands focus back to the recorded control when the
 * container goes away while the viewer was still inside it. Focus the
 * viewer has moved elsewhere is left alone.
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

  // The control that asked for the block. Recorded in the commit the
  // container first renders in, before any focus a child moves in its
  // own effect (the audio player).
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

  // The viewer's focus travelling inside the block, and whether it has
  // since moved to an element outside it.
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

  // A control inside the block left the document because the block's own
  // state changed (the answer was submitted, the retry started another
  // attempt): continue at the container. A control the viewer is still on
  // is untouched.
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

  // The block is going away: hand focus back to the control that asked
  // for it, unless the viewer has moved focus somewhere else.
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

/** The Turnstile widget. Its token is single-use, so the widget is reset
 * once the host clears the proof it consumed. */
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

/** The instruction a challenge's stable key names, in this bundle's words.
 * An unknown key is a contract the client does not yet word; the generic
 * sentence keeps the form usable rather than showing a raw key. */
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

/** The slider form: the background with the piece over the gap it was cut
 * from, moved by a native range input. The range value is the piece's
 * offset in the background's own pixels, which is the answer the server
 * compares. The background scales down with the container, so the piece
 * and its offset are drawn at the same scale: the position the viewer
 * aligns maps back to the natural-width offset the server compares,
 * within its tolerance, at any rendered width. */
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
  // The background's rendered width, measured after layout and whenever
  // the container resizes. jsdom has no layout and no ResizeObserver, so
  // the unit tier sees the natural width and the browser tier proves the
  // scaling.
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
  // The rendered width against the background's natural width. Until the
  // image loads (or where no layout exists) the natural size is drawn.
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
  // The host's send is in flight, or the piece's travel is not known yet:
  // either way the slider has no answer to give. A native range has no
  // read-only semantics (`readonly` is ignored on it), so the control keeps
  // the read-only treatment's promise -- focusable, reported unavailable,
  // its interactions swallowed -- by carrying `aria-disabled` and guarding
  // the handlers itself, instead of taking the native disable that would
  // drop focus.
  const held = disabled || maxOffset === 0
  // The slider is the answer: releasing it (or pressing Enter after the
  // keyboard has moved it) hands the offset to the host, so aligning the
  // piece is the whole interaction and no separate confirmation stands
  // between the viewer and the next step.
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
        // Not a native `disabled`: that would take the range out of the tab
        // order and move a focused control's focus away. The held state is
        // reported with `aria-disabled`, and the handlers below decline the
        // interactions it covers.
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
        // The track spans exactly the image frame, not the whole card: the
        // drag distance then corresponds to the piece's travel at every
        // rendered width. It shrinks with the container the same way the
        // frame does (`maxWidth: '100%'`), while the instruction and label
        // stay full width.
        //
        // The native control paints its thumb and filled track with the
        // accent; the token keeps that accent on the active theme's
        // primary instead of the browser's default blue. `accentColor` is
        // not one of the sx system props, so its theme path is read
        // through the callback (a plain 'primary.main' string is emitted
        // as-is and the browser drops it).
        sx={{
          width: background?.width ?? 320,
          maxWidth: '100%',
          accentColor: (theme) => theme.palette.primary.main,
        }}
      />
    </Box>
  )
}

/** One issued captcha challenge and the answer control its form takes. The
 * component is keyed by the challenge id, so a fresh challenge resets the
 * answer the previous one held. */
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
  // The audio form receives focus when it appears, so a screen-reader user
  // lands on the player without hunting for it. The element names the
  // language its digits are spoken in, which is the active locale the
  // challenge was requested with.
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
              // A write in flight takes the field read-only, not natively
              // disabled: the field stays focusable, and the ADR gives the
              // form's inputs the read-only treatment. TextField forwards
              // `readOnly` to the input slot, which puts it on the input
              // element; the top-level prop would only reach FormControl.
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
 * The refresh and audio entries, and the wait a refusal states. They stand
 * outside the keyed challenge form, so a challenge swap keeps the control
 * that asked for it mounted: it carries `busy` while the next challenge is
 * requested, and the other entry is `unavailable`.
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
        {/* The image and slider forms are the server's rotation, so the
            block offers no form choice. The audio alternative is the one
            accessibility entry, held while a refusal states a wait. */}
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
          // A swap is in flight: the one that started it is busy, and every
          // other entry, this one included, is held until it lands. The
          // refresh used to stay available when the audio entry or the host
          // started the swap, so it could layer a second request onto it.
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

/** The self-hosted captcha: the host's challenge when it holds one, the
 * issue in progress or its retry when it does not, and the verified state
 * once the host holds the proof the block yielded. */
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
  // The form the current or most recent challenge took. It outlives a
  // swap, so the audio entry keeps its place while the next challenge is
  // requested.
  const [shownForm, setShownForm] = useState<HumanCaptchaForm | null>(null)
  // Which of the two entries asked for the swap in flight.
  const [pendingAction, setPendingAction] = useState<
    'refresh' | 'audio' | null
  >(null)
  if (challenge !== null && challenge.form !== shownForm) {
    setShownForm(challenge.form)
  }
  // A challenge arriving means the swap the entry asked for has landed, a
  // failure means it did not, and a proof means the block is done: in each
  // case no entry has a swap in flight any more. The id is the dependency,
  // so a host that re-creates the object does not clear the state mid-swap.
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

  // The entries keep their slot across every state, so a control that
  // held focus while the challenge was replaced is not rebuilt.
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

/**
 * The human-verification block: nothing when the surface's gate requires
 * none, Cloudflare's widget for `turnstile`, the solve's state for `pow`,
 * and the issued challenge for `captcha`.
 */
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
