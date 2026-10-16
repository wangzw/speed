/**
 * IdentifiersSection: the sign-in-identifiers surface of the account page.
 *
 * The identifiers are the account's own, read off the profile
 * (GET /auth/me, authn.profile.get): the spec ships no dedicated
 * identifier-list route, and the profile answer carries each identifier's
 * own id, kind, value and verification, so the section reads the same
 * profile query the other account surfaces read. Each row names the kind
 * (email, phone or username, mapped through a guarded vocabulary, a value
 * outside it rendering the generic identifier label) and the value, with
 * the account's verified or unverified state.
 *
 * A row whose identifier carries an id has a row-end remove action that
 * sits behind the ui-kit danger ConfirmDialog -- removing an identifier is
 * irreversible (it is re-established only by walking the route that
 * proves it again). The removal is DELETE /auth/identifiers/{id}
 * (authn.login.removeIdentifier). The account module refuses to remove the
 * account's last way to log in with a 409, whose code text renders above
 * the list; the section never computes that count client-side. The route
 * asks for a fresh authentication: a 401 authn.insufficient_user_authentication
 * opens the step-up dialog with the target identifier remembered, and a
 * won challenge re-runs that one removal exactly once. A successful
 * removal refetches the profile so the rows converge on the server's
 * answer.
 *
 * The section takes the session as its only prop -- the step-up dialog
 * drives it -- while the profile read and the removal travel on the shared
 * access token through the generated calls.
 */

import { useState } from 'react'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import Skeleton from '@mui/material/Skeleton'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  useAuthnLoginRemoveIdentifier,
} from '@speed/api-sdk'
import type { AuthSession } from '@speed/auth-core'
import {
  AsyncSection,
  ConfirmDialog,
  errorCodeOf,
  ListSkeleton,
} from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { useProfileQuery } from './internal/queries.js'
import { useStepUpGate } from './internal/step-up-gate.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'

const STEP_UP_REQUIRED = 'authn.insufficient_user_authentication'

export interface IdentifiersSectionProps {
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
}

/** The identifier kinds the authn account module records
 * (pkg/authn's IdentifierKind), one bundle key each under
 * identifiers.kind. A value outside the list renders the generic
 * "other" label, never a raw value. */
const KNOWN_IDENTIFIER_KINDS = new Set<string>([
  'email',
  'phone',
  'username',
])

/** The pending-state placeholder, read aloud as one loading announcement. */
function IdentifierListSkeleton({ label }: { readonly label: string }) {
  const widths = [
    { primary: '40%', secondary: '56%' },
    { primary: '30%', secondary: '48%' },
  ]
  return (
    <ListSkeleton
      label={label}
      rows={widths.map((width) => (
        <>
          <Skeleton variant="text" width={width.primary} />
          <Skeleton variant="text" width={width.secondary} />
        </>
      ))}
    />
  )
}

export function IdentifiersSection({ session }: IdentifiersSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, refetch } = useProfileQuery()
  const removeIdentifier = useAuthnLoginRemoveIdentifier()

  const [removeTarget, setRemoveTarget] = useState<string | null>(null)
  const [failureCode, setFailureCode] = useState<string | null>(null)

  const gate = useStepUpGate<{ readonly id: string }>((action) => {
    void remove(action.id)
  })

  const pending = isPending && data === undefined
  const rows = !pending && data !== undefined ? data.identifiers : undefined
  // A settled read that lists none, or one whose body omitted the key,
  // hands the section to the EmptyState, whose title stands in for the
  // hidden h2 header at its own level (headingLevel="h2" below) -- the
  // heading order never skips a level, and "no identifiers" is only ever
  // claimed for an answer that genuinely listed none.
  const showHeader = pending || (rows !== undefined && rows.length > 0)

  async function remove(id: string): Promise<void> {
    setFailureCode(null)
    try {
      await removeIdentifier.mutateAsync({ id })
      setRemoveTarget(null)
      await queryClient.invalidateQueries({
        queryKey: getAuthnProfileGetQueryKey(),
      })
    } catch (error) {
      setRemoveTarget(null)
      const failure = errorCodeOf(error)
      if (failure === STEP_UP_REQUIRED) {
        gate.arm({ id })
        return
      }
      setFailureCode(failure)
    }
  }

  return (
    <Box>
      {showHeader && (
        <Box sx={{ mb: 2 }}>
          <Typography variant="h5" component="h2">
            {t('identifiers.title')}
          </Typography>
        </Box>
      )}

      <AsyncSection
        pending={pending}
        payload={rows}
        empty={rows !== undefined && rows.length === 0}
        loading={<IdentifierListSkeleton label={t('identifiers.loading')} />}
        errorState={{
          title: t('identifiers.error.title'),
          description: t('identifiers.error.description'),
          retryLabel: t('identifiers.retry'),
          onRetry: () => void refetch(),
        }}
        emptyState={{
          title: t('identifiers.empty.title'),
          description: t('identifiers.empty.description'),
        }}
      >
        {(settledRows) => (
          <Box>
            <InlineError code={failureCode} />
            {/* The rows are one real list, as the sessions and bindings
                rows are: a screen-reader user hears each identifier as one
                item of a numbered set. role="list" keeps the list
                semantics under WebKit, which strips them from a
                list-style-none ul. */}
            <Box component="ul" role="list" sx={{ m: 0, p: 0, listStyle: 'none' }}>
              {settledRows.map((identifier, index) => {
                const id = identifier.id ?? null
                const kind = identifier.kind ?? ''
                const kindLabel = KNOWN_IDENTIFIER_KINDS.has(kind)
                  ? t(`identifiers.kind.${kind}`)
                  : t('identifiers.kind.other')
                const value = identifier.value ?? ''
                const verified =
                  identifier.verified_at != null &&
                  identifier.verified_at !== ''
                return (
                  <Box
                    component="li"
                    key={id ?? String(index)}
                    sx={{
                      py: 1.5,
                      minWidth: 0,
                      ...(index > 0
                        ? { borderTop: '1px solid', borderColor: 'divider' }
                        : {}),
                    }}
                  >
                    <Box
                      sx={{ display: 'flex', alignItems: 'center', gap: 1.5 }}
                    >
                      <Box sx={{ minWidth: 0 }}>
                        <Typography variant="body1" noWrap sx={{ fontWeight: 500 }}>
                          {value}
                        </Typography>
                        <Typography variant="body2" color="text.secondary">
                          {kindLabel}
                        </Typography>
                      </Box>
                      <Box
                        sx={{
                          marginLeft: 'auto',
                          flexShrink: 0,
                          display: 'flex',
                          alignItems: 'center',
                          gap: 1,
                        }}
                      >
                        <Chip
                          size="small"
                          variant="outlined"
                          color={verified ? 'primary' : 'default'}
                          label={
                            verified
                              ? t('identifiers.verified')
                              : t('identifiers.unverified')
                          }
                        />
                        {id !== null && (
                          <Button
                            variant="outlined"
                            color="error"
                            size="small"
                            disabled={removeIdentifier.isPending}
                            aria-label={t('identifiers.removeAria', {
                              value,
                              // The label is an aria-label, not HTML: the
                              // identifier's value (an email or a phone
                              // number with a '+') must reach the
                              // accessibility tree verbatim (i18next's
                              // default value escaping would embed an
                              // entity for it).
                              interpolation: { escapeValue: false },
                            })}
                            onClick={() => setRemoveTarget(id)}
                          >
                            {t('identifiers.remove')}
                          </Button>
                        )}
                      </Box>
                    </Box>
                  </Box>
                )
              })}
            </Box>
          </Box>
        )}
      </AsyncSection>

      <ConfirmDialog
        open={removeTarget !== null}
        title={t('identifiers.confirmTitle')}
        message={t('identifiers.confirmMessage')}
        variant="danger"
        confirmLabel={t('identifiers.confirmLabel')}
        confirmLoading={removeIdentifier.isPending}
        onCancel={() => setRemoveTarget(null)}
        onConfirm={() => {
          if (removeTarget !== null) {
            void remove(removeTarget)
          }
        }}
      />

      <StepUpChallenge
        open={gate.open}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
      />
    </Box>
  )
}
