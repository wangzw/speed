/**
 * PasswordSection: the change-password surface of the account page.
 *
 * The write is PUT /api/v1/auth/password (authn.login.changePassword, the
 * account self-service route of the login module). On success the server
 * rotates the current session's token and revokes the account's other
 * sessions, so the section refetches the session list after a change and
 * reports the sign-out in its success notice. Because the route replaces
 * the calling session's credential, the change runs inside an
 * identity-transition window (session.beginIdentityTransition): a read
 * that lands with the outgoing credential and answers anonymous is
 * discarded instead of ending the session, and the window closes once the
 * refetch that follows the write has settled.
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
 * each submit starts by clearing the settled result, so a server failure
 * from the previous attempt never stands beside this attempt's field
 * error. A refusal from the server resolves through the code whitelist
 * into the section's write-feedback region beneath the form. A 400
 * bad-request on this route is the password policy, which is the route's
 * only other 400 (the client has already checked the fields the answer
 * could name), so it renders the section's policy sentence instead of the
 * generic bad-request text. A failed current-password check is the 401
 * login-failed the route answers, distinct from the step-up 401; the
 * server answers it for a wrong password and, silently, while the
 * account's back-off is in force, so it renders the section's own sentence
 * without naming a cause rather than the code's sign-in text, which names
 * identifiers and verification codes that do not apply to this write.
 * Those two section sentences are the scope's only non-code failure
 * source: they render where the region's failure does and clear with it,
 * so the section still shows one result at a time. The route asks for a
 * fresh authentication: a 401 authn.insufficient_user_authentication opens
 * the step-up dialog with the typed values remembered, and a won challenge
 * re-runs that one change exactly once. A successful change clears the
 * fields and fills the region's role="status" live region with a message
 * naming the password (and, for a first password, that it was set rather
 * than changed).
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
  /**
   * The host's current path, sent as the step-up flow's `return_to`: a
   * redirect method's round trip lands back on the page that started it, so
   * that page reads the refusal. Omitted, the deployment's default return
   * target applies.
   */
  readonly returnPath?: string
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
  returnPath,
}: PasswordSectionProps) {
  const { t } = useAccountUiTranslation()
  const queryClient = useQueryClient()
  const { data, isPending, isFetching, error, refetch } = useProfileQuery()
  const changePassword = useAuthnLoginChangePassword()

  const [current, setCurrent] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  // The client-side field error: 'missingNew' on the new-password field,
  // 'mismatch' on the confirmation. Server refusals go to the banner.
  const [fieldError, setFieldError] = useState<
    'missingNew' | 'mismatch' | null
  >(null)
  const feedback = useWriteFeedback()
  // The section's two code-specific sentences (the password policy and the
  // rejected current password) are the scope's non-code failure source:
  // they render where the region's failure does, and only one result shows
  // at a time.
  const [sectionFailure, setSectionFailure] = useState<
    'policy' | 'currentRejected' | null
  >(null)

  const gate = useStepUpGate<ChangeAction>((action) => {
    void change(action)
  })

  const pending = isPending && data === undefined
  // In flight through the whole attempt, including the refetches that follow
  // the mutation, so no control changes context under a pending scope. The
  // attempt locks the fields and marks the submit button busy; none of them
  // is natively disabled, because a focused control that becomes
  // unfocusable drops the reader's focus.
  const busy = changePassword.isPending || feedback.pending
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

  function change(action: ChangeAction): Promise<void> {
    // Read before the write: the profile refetch that follows reports the
    // password as set, and the success text must still be the first-password
    // one. A first password and a change differ only in the verb: the server
    // rotates this session and revokes the account's other sessions in both
    // cases.
    const settingFirst = !passwordSet
    return feedback.run(async () => {
      setSectionFailure(null)
      setFieldError(null)
      try {
        // The route rotates this session's token as it commits, so the
        // window is open before the write and closes once the refetch that
        // follows it has settled: a read still carrying the outgoing
        // credential is discarded instead of ending the session.
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
            // A change rotates this session's token and revokes the others;
            // the account's password_set may also have flipped, so refetch
            // both.
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
          // The write's only 400 is the password policy.
          setSectionFailure('policy')
          return null
        }
        if (code === 'authn.login_failed') {
          // The write's only login-failed is a failed current-password
          // check -- answered for a wrong password and, silently, while the
          // account's back-off is in force, so the sentence names no cause.
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
      // The client guard owns this attempt's result; a settled server result
      // from the previous attempt must not stand beside the field error.
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

  // Both the submit button's own guard and this form guard judge by the same
  // state: while the attempt is in flight, an activation -- a click, or the
  // Enter a field submits with -- reaches no second request.
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

            {/* The form's one action closes its row on the right, the same
                shape the other account-ui forms use. */}
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

            {/* One scope's result, below the form it confirms. */}
            <WriteFeedbackRegion
              outcome={feedback.outcome}
              sx={{ alignSelf: 'stretch' }}
            />

            {/* The section's two code-specific sentences share the result
                position: at most one of them and the region's failure shows
                at a time. */}
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
