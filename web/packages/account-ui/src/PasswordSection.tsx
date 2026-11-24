/**
 * PasswordSection: the change-password surface of the account page.
 */

import { useState } from 'react'
import type { FormEvent } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Skeleton from '@mui/material/Skeleton'
import TextField from '@mui/material/TextField'
import Typography from '@mui/material/Typography'
import { useQueryClient } from '@tanstack/react-query'
import {
  getAuthnProfileGetQueryKey,
  getAuthnSessionListQueryKey,
  useAuthnLoginChangePassword,
} from '@speed/api-sdk'
import type { AuthSession } from '@speed/auth-core'
import { useStepUpGate } from '@speed/auth-core'
import {
  ActionButton,
  errorCodeOf,
  FORM_MAX_WIDTH,
  SectionCard,
  useActivationGuard,
  useWriteFeedback,
} from '@speed/ui-kit'
import { AccountAsyncSection } from './internal/async-section.js'
import { runIdentityTransition } from './internal/identity-transition.js'
import { useProfileQuery } from './internal/queries.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'
import { WriteFeedbackRegion } from './internal/write-feedback.js'

/**
 * The host supplies the session and return path and chooses how an account
 * without a login name is presented; the section changes the password
 * through a step-up dialog.
 */
export interface PasswordSectionProps {
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
  /** What to render when the account has no username or email address, the
   * state in which the route answers 409 `login-identifier-required`.
   * `'form'` (the default) keeps the form and renders the code's text on
   * refusal; `'explain'` renders the explanation up front. */
  readonly withoutLoginName?: 'form' | 'explain'
  /** The host's current path, sent as the step-up flow's `return_to`. */
  readonly returnPath?: string
}

interface ChangeAction {
  readonly current: string
  readonly newPassword: string
}

function PasswordSkeleton({ label }: { readonly label: string }) {
  return (
    <Box role="status" aria-label={label} aria-busy="true">
      <Skeleton variant="text" width="32%" />
      <Skeleton variant="text" width="48%" />
    </Box>
  )
}

/**
 * The change-password surface: changes the password through a step-up
 * dialog and refetches the account's session list when it settles.
 */
export function PasswordSection({
  session,
  withoutLoginName = 'form',
  returnPath,
}: PasswordSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const changePassword = useAuthnLoginChangePassword()

  const [current, setCurrent] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  // Server refusals go to the banner.
  const [fieldError, setFieldError] = useState<
    'missingNew' | 'mismatch' | null
  >(null)
  const feedback = useWriteFeedback()
  // The policy and rejected-current-password sentences share the region's
  // position, so only one result shows at a time.
  const [sectionFailure, setSectionFailure] = useState<
    'policy' | 'currentRejected' | null
  >(null)

  const gate = useStepUpGate<ChangeAction>((action) => {
    void change(action)
  })

  const pending = isPending && data === undefined
  // In flight through the whole attempt, including the refetches that follow.
  // Nothing is natively disabled: a focused control that turns unfocusable
  // drops the reader's focus.
  const busy = changePassword.isPending || feedback.pending
  // An answer that did not say a password is set reads as one that did; the
  // server validates the current password regardless.
  const passwordSet = data?.password_set !== false
  // Whether an identifier is a login name does not depend on its proof. An
  // answer that did not list identifiers is not read as "none".
  const loginNamePresent =
    data?.identifiers === undefined
      ? true
      : data.identifiers.some(
          (identifier) =>
            identifier.kind === 'username' || identifier.kind === 'email',
        )
  const explainWithoutLoginName =
    withoutLoginName === 'explain' &&
    data !== undefined &&
    data.identifiers !== undefined &&
    !loginNamePresent

  function clearFields(): void {
    setCurrent('')
    setNewPassword('')
    setConfirm('')
  }

  function change(action: ChangeAction): Promise<void> {
    // Read before the write: the refetch that follows reports the password as
    // set, but the success text must still be the first-password one.
    const settingFirst = !passwordSet
    return feedback.run(async () => {
      setSectionFailure(null)
      setFieldError(null)
      try {
        return await runIdentityTransition(
          session,
          () =>
            changePassword.mutateAsync({
              data: {
                ...(passwordSet ? { current_password: action.current } : {}),
                new_password: action.newPassword,
              },
            }),
          async () => {
            clearFields()
            // The change rotates this session's token and may flip
            // password_set, so refetch both.
            await Promise.all([
              queryClient.invalidateQueries({
                queryKey: getAuthnSessionListQueryKey(),
              }),
              queryClient.invalidateQueries({
                queryKey: getAuthnProfileGetQueryKey(),
              }),
            ])
            return t(
              settingFirst ? 'password.changedFirst' : 'password.changed',
            )
          },
        )
      } catch (error) {
        if (gate.intercept(error, action)) {
          return null
        }
        const code = errorCodeOf(error)
        if (code === 'authn.bad_request') {
          setSectionFailure('policy')
          return null
        }
        if (code === 'authn.login_failed') {
          // Answered for a wrong password and, silently, during back-off, so
          // the sentence names no cause.
          setSectionFailure('currentRejected')
          return null
        }
        throw error
      }
    })
  }

  function submit(event: FormEvent): void {
    event.preventDefault()
    if (newPassword === '') {
      // A settled result from the previous attempt must not stand beside the
      // field error.
      feedback.reset()
      setSectionFailure(null)
      setFieldError('missingNew')
      return
    }
    if (confirm !== newPassword) {
      feedback.reset()
      setSectionFailure(null)
      setFieldError('mismatch')
      return
    }
    void change({ current, newPassword })
  }

  // While the attempt is in flight, neither a click nor a field's Enter
  // reaches a second request.
  const guardedSubmit = useActivationGuard(submit, { busy })

  return (
    <>
      <SectionCard title={t('password.title')}>
        <AccountAsyncSection
        pending={pending}
        payload={data}
        empty={false}
        error={error}
        refreshing={isFetching}
        loading={<PasswordSkeleton label={t('password.loading')} />}
        errorState={{
          title: t('password.error.title'),
          description: t('password.error.description'),
          retryLabel: t('password.retry'),
          onRetry: () => void refetch(),
        }}
      >
        {() =>
          explainWithoutLoginName ? (
            <Typography variant="body2" color="text.secondary">
              {t('password.loginNameRequired')}
            </Typography>
          ) : (
          <Box
            component="form"
            noValidate
            onSubmit={guardedSubmit}
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'flex-start',
              gap: 2,
              maxWidth: FORM_MAX_WIDTH,
            }}
          >
            {passwordSet && (
              <TextField
                fullWidth
                type="password"
                autoComplete="current-password"
                label={t('password.current.label')}
                value={current}
                slotProps={{ htmlInput: { readOnly: busy } }}
                onChange={(event) => setCurrent(event.target.value)}
              />
            )}

            <TextField
              fullWidth
              type="password"
              autoComplete="new-password"
              label={t('password.new.label')}
              value={newPassword}
              slotProps={{ htmlInput: { readOnly: busy } }}
              error={fieldError === 'missingNew'}
              helperText={
                fieldError === 'missingNew'
                  ? t('password.missingNew')
                  : passwordSet
                    ? t('password.new.helper')
                    : t('password.new.helperFirst')
              }
              onChange={(event) => {
                setNewPassword(event.target.value)
                setFieldError(null)
              }}
            />

            <TextField
              fullWidth
              type="password"
              autoComplete="new-password"
              label={t('password.confirm.label')}
              value={confirm}
              slotProps={{ htmlInput: { readOnly: busy } }}
              error={fieldError === 'mismatch'}
              helperText={
                fieldError === 'mismatch' ? t('password.mismatch') : undefined
              }
              onChange={(event) => {
                setConfirm(event.target.value)
                setFieldError(null)
              }}
            />

            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'flex-end',
                alignSelf: 'stretch',
              }}
            >
              <ActionButton type="submit" emphasis="primary" busy={busy}>
                {t(passwordSet ? 'password.submit' : 'password.submitFirst')}
              </ActionButton>
            </Box>

            <WriteFeedbackRegion
              outcome={feedback.outcome}
              sx={{ alignSelf: 'stretch' }}
            />

            {sectionFailure !== null && (
              <Alert severity="error" role="alert" sx={{ width: '100%' }}>
                {t(
                  sectionFailure === 'policy'
                    ? 'password.policyFailure'
                    : 'password.currentRejected',
                )}
              </Alert>
            )}
          </Box>
          )
        }
      </AccountAsyncSection>
      </SectionCard>

      <StepUpChallenge
        open={gate.open}
        reason={gate.reason}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
        returnPath={returnPath}
      />
    </>
  )
}
