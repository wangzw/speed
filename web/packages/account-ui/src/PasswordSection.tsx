/**
 * PasswordSection: the change-password surface of the account page.
 *
 * The write is PUT /api/v1/auth/password (authn.login.changePassword, the
 * account self-service route of the login module). On success the server
 * rotates the current session's token and revokes the account's other
 * sessions, so the section refetches the session list after a change and
 * reports the sign-out in its success notice.
 *
 * The account's `password_set` flag decides whether a current password is
 * asked for: an account opened through a provider holds no password, so
 * the current-password field is shown only when the profile says one is
 * set, and a provider-only account changes to its first password with the
 * new-password fields alone. The profile read is the same GET /api/v1/auth/me the
 * other account surfaces read.
 *
 * Two client-side guards run before the request -- a non-empty new
 * password and a matching confirmation -- and render on their own fields;
 * a refusal from the server resolves through the code whitelist into the
 * banner. A 400 bad-request on this route is the password policy, which is
 * the route's only other 400 (the client has already checked the fields
 * the answer could name), so it renders the section's policy sentence
 * instead of the generic bad-request text. A wrong current password is the
 * 401 login-failed the route answers, distinct from the step-up 401. The
 * route asks for a fresh authentication: a 401
 * authn.insufficient_user_authentication opens the step-up dialog with the
 * typed values remembered, and a won challenge re-runs that one change
 * exactly once. A successful change clears the fields and fills the
 * role="status" live region.
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
import { useStepUpGate } from '@speed/auth-core'
import {
  AsyncSection,
  errorCodeOf,
  FORM_MAX_WIDTH,
  SectionCard,
  visuallyHiddenSx,
} from '@speed/ui-kit'
import { InlineError } from './internal/inline-error.js'
import { useProfileQuery } from './internal/queries.js'
import { StepUpChallenge } from './internal/step-up-challenge.js'
import { useAccountUiTranslation } from './internal/translation.js'

const STEP_UP_REQUIRED = 'authn.insufficient_user_authentication'

export interface PasswordSectionProps {
  /** The session whose step-up flow the dialog drives. */
  readonly session: AuthSession
  /**
   * What to render when the account holds neither a username nor an
   * email address -- the state in which `PUT
   * /api/v1/auth/password` answers 409 `login-identifier-required`.
   * `'form'` (the default) keeps the form: the server's refusal renders
   * the code's text. `'explain'` renders the explanation up front
   * instead of the form, for a page that already knows the account
   * cannot set a password yet.
   */
  readonly withoutLoginName?: 'form' | 'explain'
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

export function PasswordSection({
  session,
  withoutLoginName = 'form',
}: PasswordSectionProps) {
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
  // set, and the success text must still be the first-password one. A first
  // password and a change differ only in the verb: the server rotates this
  // session and revokes the account's other sessions in both cases.
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
  // A username or an email address is the login name a password is set
  // against; verification does not enter the judgement, because an email
  // identifier is a login name whether or not its ownership was proved.
  // An answer that did not list identifiers is not read as "none": the
  // explanation needs a settled, explicit answer, and the server's 409
  // remains the authority.
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
    <>
      <SectionCard title={t('password.title')}>
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
        {() =>
          explainWithoutLoginName ? (
            <Typography variant="body2" color="text.secondary">
              {t('password.loginNameRequired')}
            </Typography>
          ) : (
          <Box
            component="form"
            noValidate
            onSubmit={submit}
            sx={{
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'flex-start',
              gap: 2,
              maxWidth: FORM_MAX_WIDTH,
            }}
          >
            <Alert
              severity="success"
              role="status"
              sx={changed ? { width: '100%' } : visuallyHiddenSx}
            >
              {/* The first password is set, not changed, and the server
                  signs the other devices out either way. */}
              {changed
                ? t(firstPassword ? 'password.changedFirst' : 'password.changed')
                : ''}
            </Alert>

            {failureCode === 'authn.bad_request' ? (
              <Alert severity="error" role="alert" sx={{ width: '100%' }}>
                {t('password.policyFailure')}
              </Alert>
            ) : (
              <InlineError code={failureCode} />
            )}

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
          )
        }
      </AsyncSection>
      </SectionCard>

      <StepUpChallenge
        open={gate.open}
        session={session}
        onSuccess={gate.succeed}
        onCancel={gate.cancel}
      />
    </>
  )
}
