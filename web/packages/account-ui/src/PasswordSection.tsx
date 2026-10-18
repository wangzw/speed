/**
 * PasswordSection: the change-password surface of the account page.
 *
 * The write is PUT /auth/password (authn.login.changePassword, the
 * account self-service route of the login module). On success the server
 * rotates the current session's token and revokes the account's other
 * sessions, so the section refetches the session list after a change and
 * reports the sign-out in its success notice.
 *
 * The account's `password_set` flag decides whether a current password is
 * asked for: an account opened through a provider holds no password, so
 * the current-password field is shown only when the profile says one is
 * set, and a provider-only account changes to its first password with the
 * new-password fields alone. The profile read is the same GET /auth/me the
 * other account surfaces read.
 *
 * Two client-side guards run before the request -- a non-empty new
 * password and a matching confirmation -- and render on their own fields;
 * a refusal from the server resolves through the code whitelist into the
 * banner (a wrong current password is the 401 login-failed the route
 * answers, distinct from the step-up 401). The route asks for a fresh
 * authentication: a 401 authn.insufficient_user_authentication opens the
 * step-up dialog with the typed values remembered, and a won challenge
 * re-runs that one change exactly once. A successful change clears the
 * fields and fills the role="status" live region.
 */

import { useState } from 'react'
import type { FormEvent } from 'react'
import Alert from '@mui/material/Alert'
import Box from '@mui/material/Box'
import Button from '@mui/material/Button'
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
import { AsyncSection, errorCodeOf, visuallyHiddenSx } from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { useProfileQuery } from './internal/queries.js'
import { useStepUpGate } from './internal/step-up-gate.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'

const STEP_UP_REQUIRED = 'authn.insufficient_user_authentication'

export interface PasswordSectionProps {
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
}

/** The submit's typed values: what a won step-up re-runs. */
interface ChangeAction {
  readonly current: string
  readonly newPassword: string
}

/** The pending-state placeholder, read aloud as one loading announcement. */
function PasswordSkeleton({ label }: { readonly label: string }) {
  return (
    <Box role="status" aria-label={label} aria-busy="true">
      <Skeleton variant="text" width="32%" />
      <Skeleton variant="text" width="48%" />
    </Box>
  )
}

export function PasswordSection({ session }: PasswordSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, refetch } = useProfileQuery()
  const changePassword = useAuthnLoginChangePassword()

  const [current, setCurrent] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  // The client-side field error: 'missingNew' on the new-password field,
  // 'mismatch' on the confirmation. Server refusals go to the banner.
  const [fieldError, setFieldError] = useState<
    'missingNew' | 'mismatch' | null
  >(null)
  const [failureCode, setFailureCode] = useState<string | null>(null)
  const [changed, setChanged] = useState(false)
  // Whether the completed change set the account's first password. It is read
  // before the write: the profile refetch that follows reports the password as
  // set, and the success text must still be the first-password one -- no other
  // device was signed out by setting a password that did not exist.
  const [firstPassword, setFirstPassword] = useState(false)

  const gate = useStepUpGate<ChangeAction>((action) => {
    void change(action)
  })

  const pending = isPending && data === undefined
  // The form is withheld only when the answer says no password is set (a
  // provider-only account); an answer that did not say reads as one that
  // did, so a scalar the surface cannot decide never hides the form -- the
  // server validates the current password regardless.
  const passwordSet = data?.password_set !== false

  function clearFields(): void {
    setCurrent('')
    setNewPassword('')
    setConfirm('')
  }

  async function change(action: ChangeAction): Promise<void> {
    setFailureCode(null)
    setChanged(false)
    setFieldError(null)
    const settingFirst = !passwordSet
    try {
      await changePassword.mutateAsync({
        data: {
          ...(passwordSet ? { current_password: action.current } : {}),
          new_password: action.newPassword,
        },
      })
      clearFields()
      setFirstPassword(settingFirst)
      setChanged(true)
      // A change rotates this session's token and revokes the others; the
      // account's password_set may also have flipped, so refetch both.
      await Promise.all([
        queryClient.invalidateQueries({
          queryKey: getAuthnSessionListQueryKey(),
        }),
        queryClient.invalidateQueries({
          queryKey: getAuthnProfileGetQueryKey(),
        }),
      ])
    } catch (error) {
      const code = errorCodeOf(error)
      if (code === STEP_UP_REQUIRED) {
        gate.arm(action)
        return
      }
      setFailureCode(code)
    }
  }

  function submit(event: FormEvent): void {
    event.preventDefault()
    if (newPassword === '') {
      setFieldError('missingNew')
      setChanged(false)
      return
    }
    if (confirm !== newPassword) {
      setFieldError('mismatch')
      setChanged(false)
      return
    }
    void change({ current, newPassword })
  }

  return (
    <Box>
      <Box sx={{ mb: 2 }}>
        <Typography variant="h5" component="h2">
          {t('password.title')}
        </Typography>
      </Box>

      <AsyncSection
        pending={pending}
        payload={data}
        empty={false}
        loading={<PasswordSkeleton label={t('password.loading')} />}
        errorState={{
          title: t('password.error.title'),
          description: t('password.error.description'),
          retryLabel: t('password.retry'),
          onRetry: () => void refetch(),
        }}
      >
        {() => (
          <Box
            component="form"
            noValidate
            onSubmit={submit}
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'flex-start',
              gap: 2,
              maxWidth: 600,
            }}
          >
            <Alert
              severity="success"
              role="status"
              sx={changed ? { width: '100%' } : visuallyHiddenSx}
            >
              {/* A first password is set, not changed, and no other device
                  was signed out by a change's rotation. */}
              {changed
                ? t(firstPassword ? 'password.changedFirst' : 'password.changed')
                : ''}
            </Alert>

            <InlineError code={failureCode} />

            {passwordSet && (
              <TextField
                fullWidth
                type="password"
                autoComplete="current-password"
                label={t('password.current.label')}
                value={current}
                disabled={changePassword.isPending}
                onChange={(event) => setCurrent(event.target.value)}
              />
            )}

            <TextField
              fullWidth
              type="password"
              autoComplete="new-password"
              label={t('password.new.label')}
              value={newPassword}
              disabled={changePassword.isPending}
              error={fieldError === 'missingNew'}
              helperText={
                fieldError === 'missingNew'
                  ? t('password.missingNew')
                  : t('password.new.helper')
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
              disabled={changePassword.isPending}
              error={fieldError === 'mismatch'}
              helperText={
                fieldError === 'mismatch' ? t('password.mismatch') : undefined
              }
              onChange={(event) => {
                setConfirm(event.target.value)
                setFieldError(null)
              }}
            />

            <Button
              type="submit"
              variant="contained"
              disabled={changePassword.isPending}
            >
              {t(passwordSet ? 'password.submit' : 'password.submitFirst')}
            </Button>
          </Box>
        )}
      </AsyncSection>

      <StepUpChallenge
        open={gate.open}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
      />
    </Box>
  )
}
