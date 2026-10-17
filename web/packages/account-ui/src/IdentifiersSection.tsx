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
 * the list; the section never computes that count client-side.
 *
 * Below the list is the add face: POST /auth/identifiers
 * (authn.login.addIdentifier) with a kind and a value. A username is a
 * login name with no ownership to prove, so it is written in one step. An
 * email is a contact address, so the add sends a code to it (the answer is
 * state "pending") and the code is submitted through POST
 * /auth/identifiers/verify (authn.login.verifyIdentifier); only the
 * matching code writes the address, with the proof. The email branch is
 * unavailable on a deployment with no email channel (a 503), and both
 * writes revoke the account's other sessions, so a success refetches the
 * profile and the session list.
 *
 * Every write asks for a fresh authentication: a 401
 * authn.insufficient_user_authentication opens the step-up dialog with the
 * write's inputs remembered, and a won challenge re-runs that one write
 * exactly once. A successful write refetches the profile so the rows
 * converge on the server's answer.
 *
 * The section takes the session as its only prop -- the step-up dialog
 * drives it -- while the profile read and the writes travel on the shared
 * access token through the generated calls.
 */

import { useState } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
import Chip from '@mui/material/Chip'
import MenuItem from '@mui/material/MenuItem'
import Skeleton from '@mui/material/Skeleton'
import Stack from '@mui/material/Stack'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  getAuthnSessionListQueryKey,
  useAuthnLoginAddIdentifier,
  useAuthnLoginRemoveIdentifier,
  useAuthnLoginVerifyIdentifier,
} from '@speed/api-sdk'
import type { AuthSession } from '@speed/auth-core'
import {
  AsyncSection,
  ConfirmDialog,
  errorCodeOf,
  ListSkeleton,
  visuallyHiddenSx,
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

/** The kinds the add face offers here: a username and an email. A phone is
 * added through its own two-step surface (the SMS routes), not this one. */
type AddKind = 'email' | 'username'
const ADD_KINDS: readonly AddKind[] = ['email', 'username']

/** The write a step-up dialog is armed for, so a won challenge re-runs
 * exactly it: a removal, the add that sends the code, or the code's
 * verification. */
type SectionAction =
  | { readonly step: 'remove'; readonly id: string }
  | { readonly step: 'add'; readonly kind: AddKind; readonly value: string }
  | { readonly step: 'verify'; readonly code: string }

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
  const addIdentifier = useAuthnLoginAddIdentifier()
  const verifyIdentifier = useAuthnLoginVerifyIdentifier()

  const [removeTarget, setRemoveTarget] = useState<string | null>(null)
  const [failureCode, setFailureCode] = useState<string | null>(null)

  // The add face's own state. addOpen shows the form; addKind picks the
  // kind; addValue holds the entry; sentTo is the address a code went to,
  // or null while the value is being entered; code is the answer. added
  // fills the live region once a write lands.
  const [addOpen, setAddOpen] = useState(false)
  const [addKind, setAddKind] = useState<AddKind>('email')
  const [addValue, setAddValue] = useState('')
  const [sentTo, setSentTo] = useState<string | null>(null)
  const [code, setCode] = useState('')
  const [added, setAdded] = useState(false)

  const addBusy =
    addIdentifier.isPending || verifyIdentifier.isPending
  const writeBusy = removeIdentifier.isPending

  const gate = useStepUpGate<SectionAction>((action) => {
    switch (action.step) {
      case 'remove':
        void remove(action.id)
        return
      case 'add':
        void add(action.kind, action.value)
        return
      case 'verify':
        void verify(action.code)
        return
    }
  })

  const pending = isPending && data === undefined
  const rows = !pending && data !== undefined ? data.identifiers : undefined
  // A settled read that lists none, or one whose body omitted the key,
  // hands the section to the EmptyState, whose title stands in for the
  // hidden h2 header at its own level (headingLevel="h2" below) -- the
  // heading order never skips a level, and "no identifiers" is only ever
  // claimed for an answer that genuinely listed none.
  const showHeader = pending || (rows !== undefined && rows.length > 0)

  /** Refetch what a write changes: the profile's identifiers, and the
   * session list, because these routes revoke the account's other
   * sessions. */
  async function invalidateAfterWrite(): Promise<void> {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: getAuthnProfileGetQueryKey() }),
      queryClient.invalidateQueries({
        queryKey: getAuthnSessionListQueryKey(),
      }),
    ])
  }

  function closeAdd(): void {
    setAddOpen(false)
    setSentTo(null)
    setAddValue('')
    setCode('')
  }

  async function remove(id: string): Promise<void> {
    setFailureCode(null)
    try {
      await removeIdentifier.mutateAsync({ id })
      setRemoveTarget(null)
      await invalidateAfterWrite()
    } catch (error) {
      setRemoveTarget(null)
      const failure = errorCodeOf(error)
      if (failure === STEP_UP_REQUIRED) {
        gate.arm({ step: 'remove', id })
        return
      }
      setFailureCode(failure)
    }
  }

  async function add(kind: AddKind, value: string): Promise<void> {
    setFailureCode(null)
    setAdded(false)
    try {
      const result = await addIdentifier.mutateAsync({ data: { kind, value } })
      if (result.state === 'pending') {
        // An address the deployment can reach: the code is on its way and
        // the answer is the next step.
        setSentTo(value)
        setCode('')
        return
      }
      closeAdd()
      setAdded(true)
      await invalidateAfterWrite()
    } catch (error) {
      const failure = errorCodeOf(error)
      if (failure === STEP_UP_REQUIRED) {
        gate.arm({ step: 'add', kind, value })
        return
      }
      setFailureCode(failure)
    }
  }

  async function verify(nextCode: string): Promise<void> {
    setFailureCode(null)
    setAdded(false)
    try {
      await verifyIdentifier.mutateAsync({ data: { code: nextCode } })
      closeAdd()
      setAdded(true)
      await invalidateAfterWrite()
    } catch (error) {
      const failure = errorCodeOf(error)
      if (failure === STEP_UP_REQUIRED) {
        gate.arm({ step: 'verify', code: nextCode })
        return
      }
      setFailureCode(failure)
    }
  }

  const valueLabel =
    addKind === 'email'
      ? t('identifiers.add.value.labelEmail')
      : t('identifiers.add.value.labelUsername')
  const valueHelper =
    addKind === 'email'
      ? t('identifiers.add.value.helperEmail')
      : t('identifiers.add.value.helperUsername')

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
                            disabled={writeBusy}
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

      <Box sx={{ mt: 2 }}>
        <Alert
          severity="success"
          role="status"
          sx={added ? { mb: 1 } : visuallyHiddenSx}
        >
          {added ? t('identifiers.add.added') : ''}
        </Alert>

        {!addOpen ? (
          <Button
            variant="outlined"
            size="small"
            disabled={writeBusy || addBusy}
            onClick={() => {
              setFailureCode(null)
              setAdded(false)
              setAddOpen(true)
            }}
          >
            {t('identifiers.add.open')}
          </Button>
        ) : (
          <>
            <Typography variant="subtitle1" component="h3" sx={{ mb: 1 }}>
              {t('identifiers.add.title')}
            </Typography>
            {/* Banners for the add face live here; the removal's banner sits
                above the list. This one is only shown while the form is. */}
            <InlineError code={failureCode} />
            <Box
              component="form"
              noValidate
              onSubmit={(event) => {
                event.preventDefault()
                if (sentTo === null) {
                  void add(addKind, addValue)
                } else {
                  void verify(code)
                }
              }}
              sx={{
                display: 'flex',
                flexDirection: 'column',
                alignItems: 'flex-start',
                gap: 2,
                maxWidth: 600,
                mt: 1,
              }}
            >
              {sentTo === null ? (
                <>
                  <TextField
                    select
                    fullWidth
                    label={t('identifiers.add.kind.label')}
                    value={addKind}
                    disabled={addBusy}
                    onChange={(event) =>
                      setAddKind(event.target.value as AddKind)
                    }
                  >
                    {ADD_KINDS.map((option) => (
                      <MenuItem key={option} value={option}>
                        {t(`identifiers.add.kind.${option}`)}
                      </MenuItem>
                    ))}
                  </TextField>
                  <TextField
                    fullWidth
                    type={addKind === 'email' ? 'email' : 'text'}
                    autoComplete={addKind === 'email' ? 'email' : 'username'}
                    label={valueLabel}
                    helperText={valueHelper}
                    value={addValue}
                    disabled={addBusy}
                    onChange={(event) => setAddValue(event.target.value)}
                  />
                  <Stack direction="row" spacing={1}>
                    <Button
                      type="submit"
                      variant="contained"
                      disabled={addBusy || addValue.trim() === ''}
                    >
                      {addKind === 'email'
                        ? t('identifiers.add.submitEmail')
                        : t('identifiers.add.submitUsername')}
                    </Button>
                    <Button type="button" disabled={addBusy} onClick={closeAdd}>
                      {t('identifiers.add.cancel')}
                    </Button>
                  </Stack>
                </>
              ) : (
                <>
                  <Typography variant="body2" color="text.secondary">
                    {t('identifiers.add.sent', {
                      value: sentTo,
                      interpolation: { escapeValue: false },
                    })}
                  </Typography>
                  <TextField
                    fullWidth
                    inputMode="numeric"
                    autoComplete="one-time-code"
                    label={t('identifiers.add.code.label')}
                    value={code}
                    disabled={addBusy}
                    onChange={(event) => setCode(event.target.value)}
                  />
                  <Stack direction="row" spacing={1}>
                    <Button
                      type="submit"
                      variant="contained"
                      disabled={addBusy || code.trim() === ''}
                    >
                      {t('identifiers.add.verify')}
                    </Button>
                    <Button
                      type="button"
                      disabled={addBusy}
                      onClick={() => {
                        setSentTo(null)
                        setCode('')
                        setFailureCode(null)
                      }}
                    >
                      {t('identifiers.add.change')}
                    </Button>
                  </Stack>
                </>
              )}
            </Box>
          </>
        )}
      </Box>

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
