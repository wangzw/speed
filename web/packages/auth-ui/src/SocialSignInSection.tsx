/** The social channels of the sign-in surface: one button per redirect
 * provider. */

import { useRef, useState } from 'react'
import type { ReactNode } from 'react'
import Box from '@mui/material/Box'
import Divider from '@mui/material/Divider'
import type { AuthSession } from '@speed/auth-core'
import { useAuthUiTranslation } from './internal/translation.js'
import { methodLabel } from './internal/labels.js'
import { ActionButton, errorCodeOf } from '@speed/ui-kit'
import {
  ChannelFeedbackRegion,
  useChannelFeedback,
} from './internal/write-feedback.js'
import { isOpenFlow } from './internal/flow.js'
import { useGroupHeadingVariant } from './internal/group-heading.js'
import { allowedToStart } from './internal/start-gate.js'
import type { SignInBeforeStart } from './internal/start-gate.js'

/** A redirect sign-in method's name. The set is the deployment's, so this is a
 * method name rather than a closed union. */
export type SocialProvider = string

/** One configured channel. */
export interface SocialProviderConfig {
  /** The redirect method's name. */
  readonly provider: SocialProvider
  /** The method's brand name where the discovery document carries one. */
  readonly label?: string
  /** The host callback route sent as the flow's `return_to`; empty lets the
   * server use its configured default. */
  readonly redirectUri: string
  /** The provider's own mark, rendered before its label. A config without one
   * falls back to the section's `providerIcon` lookup. */
  readonly icon?: ReactNode
}

/** The host supplies the session, the provider list and the callbacks; the
 * section owns the per-provider busy state. */
export interface SocialSignInSectionProps {
  /** The session that begins each channel's login flow. */
  readonly session: AuthSession
  /** The channels to render, in the order the host wants. */
  readonly providers: readonly SocialProviderConfig[]
  /**
   * Receives one channel's authorization URL once the flow hands it over; the
   * host navigates. Omit to run the section without a follow-up.
   */
  readonly onAuthorizeUrl?: (provider: SocialProvider, authorizeUrl: string) => void
  /**
   * The provider's mark for one channel, rendered before its label. A config's
   * own `icon` wins; this lookup covers provider sets the host does not
   * enumerate.
   */
  readonly providerIcon?: (provider: SocialProvider) => ReactNode
  /**
   * Decides whether a channel's flow may begin. Returning false starts
   * nothing and renders no error; only the first step is gated.
   */
  readonly beforeStart?: SignInBeforeStart
}

/** The protocol code for an answer that is not the expected redirect step: a
 * contract-violating 2xx is not a successful start. */
const UNEXPECTED_ANSWER = 'client.protocol'

/**
 * One outlined button per configured provider. Clicking one begins that
 * method's flow and reports the resulting authorization URL through
 * `onAuthorizeUrl`; a refusal renders in the block's one feedback region.
 */
export function SocialSignInSection({
  session,
  providers,
  onAuthorizeUrl,
  providerIcon,
  beforeStart,
}: SocialSignInSectionProps) {
  const { t } = useAuthUiTranslation()
  const headingVariant = useGroupHeadingVariant()
  // One busy slot per provider: an earlier flight's return must never unlock
  // another provider's still-pending button.
  const [busyProviders, setBusyProviders] = useState<
    ReadonlySet<SocialProvider>
  >(new Set())
  const feedback = useChannelFeedback()
  // The failure banner belongs to the newest attempt only.
  const attemptsRef = useRef(0)

  const authorize = async (config: SocialProviderConfig): Promise<void> => {
    const provider = config.provider
    // Take the busy slot before the gate resolves too, so a second click
    // cannot pass an async gate and begin a second flow.
    setBusyProviders((previous) => new Set(previous).add(provider))
    if (!(await allowedToStart(beforeStart, { method: provider }))) {
      setBusyProviders((previous) => {
        const next = new Set(previous)
        next.delete(provider)
        return next
      })
      return
    }
    const attempt = ++attemptsRef.current
    feedback.reset()
    let authorizeUrl: string | null = null
    let failure: string | null = null
    try {
      const reply = await session.startFlow({
        method: provider,
        binding: 'cookie',
        ...(config.redirectUri === '' ? {} : { return_to: config.redirectUri }),
      })
      authorizeUrl =
        isOpenFlow(reply) && reply.next.kind === 'redirect'
          ? reply.next.url
          : null
      if (authorizeUrl === null) {
        failure = UNEXPECTED_ANSWER
      }
    } catch (error) {
      failure = errorCodeOf(error)
    } finally {
      setBusyProviders((previous) => {
        const next = new Set(previous)
        next.delete(provider)
        return next
      })
    }
    // An older attempt settling after a newer one started must not paint
    // over the newer attempt's outcome.
    if (failure !== null) {
      if (attempt === attemptsRef.current) {
        feedback.fail(failure)
      }
      return
    }
    if (authorizeUrl !== null) {
      try {
        onAuthorizeUrl?.(provider, authorizeUrl)
      } catch {
        // A throwing host callback is not an authorization failure: the URL
        // was built, and the throw stays out of this click handler.
      }
    }
  }

  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1, width: '100%' }}>
      <Divider textAlign="center" sx={{ color: 'text.secondary' }}>
        {t(
          headingVariant === 'plain' ? 'social.titleFirst' : 'social.title',
        )}
      </Divider>
      {providers.map((config) => {
        const icon = config.icon ?? providerIcon?.(config.provider)
        return (
          <ActionButton
            key={config.provider}
            fullWidth
            startIcon={icon}
            busy={busyProviders.has(config.provider)}
            onClick={() => void authorize(config)}
          >
            {methodLabel({ name: config.provider, label: config.label }, t)}
          </ActionButton>
        )
      })}
      <ChannelFeedbackRegion feedback={feedback} />
    </Box>
  )
}
