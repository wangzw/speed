#!/bin/sh
# The container entrypoint: render the deployment configuration, seed the
# database, hand the process to the server.
#
# Rendering happens here rather than at build time because two things are not
# known until the Machine exists: the origin the app is reached at (derived
# from FLY_APP_NAME, or given by DEMO_PUBLIC_URL for a custom domain) and the
# operator's username, address and password (secrets). The rendered file lands
# on the volume, so a restart rewrites it from the current values instead of
# leaving a stale copy in an image layer.
#
# Seeding runs on every start. It is idempotent by contract: an account,
# tenant, role or setting already present is left as it stands, so a restart
# neither duplicates nor overwrites what an operator changed through the
# console. It also applies the migrations, which is why no separate step runs
# before it.
#
# The process runs as an unprivileged user. The volume arrives owned by root,
# so the entrypoint takes ownership before dropping; su-exec replaces itself
# with the server, leaving the server as PID 1 to receive Fly's SIGTERM.

set -eu

template=/app/deploy/fly.yaml.tmpl
config=/data/config.yaml

die() {
    printf 'deploy: %s\n' "$*" >&2
    exit 1
}

require() {
    if [ -z "$2" ]; then
        die "$1 is not set. Set it as a Fly secret before deploying:
    flyctl secrets set $1=... --app \$FLY_APP_NAME"
    fi
}

# The configuration is YAML and the placeholders sit inside double-quoted
# scalars, so a value that would end the scalar or start an escape sequence is
# refused rather than rendered into a different document.
check_scalar() {
    case $2 in
        *'"'* | *\\*)
            die "$1 contains a double quote or a backslash, which the deployment configuration cannot carry"
            ;;
    esac
    # grep reads a line at a time, so a line break never reaches its pattern
    # and is matched here instead.
    case $2 in
        *'
'*)
            die "$1 contains a line break"
            ;;
    esac
    if printf '%s' "$2" | LC_ALL=C grep -q '[[:cntrl:]]'; then
        die "$1 contains a control character"
    fi
}

if [ -n "${DEMO_PUBLIC_URL:-}" ]; then
    public_origin=$DEMO_PUBLIC_URL
else
    [ -n "${FLY_APP_NAME:-}" ] || die "neither DEMO_PUBLIC_URL nor FLY_APP_NAME is set, so the deployment has no origin to serve from"
    public_origin="https://${FLY_APP_NAME}.fly.dev"
fi

case $public_origin in
    https://?*) ;;
    http://?*)
        die "DEMO_PUBLIC_URL must be an https origin: the authentication gateway refuses an insecure public URL"
        ;;
    *)
        die "DEMO_PUBLIC_URL must be an origin such as https://app.fly.dev"
        ;;
esac

public_host=${public_origin#*://}
case $public_host in
    '' | */* | *\?* | *\#* | *' '*)
        die "DEMO_PUBLIC_URL must carry a host and no path, query or space"
        ;;
esac
# The WebAuthn relying party id is a domain: it carries no port.
public_domain=${public_host%%:*}
[ -n "$public_domain" ] || die "DEMO_PUBLIC_URL carries no host"

require DEMO_OPERATOR_USERNAME "${DEMO_OPERATOR_USERNAME:-}"
require DEMO_OPERATOR_EMAIL "${DEMO_OPERATOR_EMAIL:-}"
require DEMO_OPERATOR_PASSWORD "${DEMO_OPERATOR_PASSWORD:-}"
require DEMO_DB_ENCRYPTION_KEY "${DEMO_DB_ENCRYPTION_KEY:-}"

check_scalar DEMO_OPERATOR_USERNAME "$DEMO_OPERATOR_USERNAME"
check_scalar DEMO_OPERATOR_EMAIL "$DEMO_OPERATOR_EMAIL"
check_scalar DEMO_OPERATOR_PASSWORD "$DEMO_OPERATOR_PASSWORD"
check_scalar DEMO_DB_ENCRYPTION_KEY "$DEMO_DB_ENCRYPTION_KEY"

# The deployment build adds mail.smtp and authn.emailcode, so registration
# cannot start without a relay. The host and the sender are required; the
# port and the TLS posture have defaults, and an empty username or password
# means the relay is used without AUTH.
require DEMO_SMTP_HOST "${DEMO_SMTP_HOST:-}"
require DEMO_SMTP_FROM "${DEMO_SMTP_FROM:-}"

check_scalar DEMO_SMTP_HOST "$DEMO_SMTP_HOST"
check_scalar DEMO_SMTP_FROM "$DEMO_SMTP_FROM"
check_scalar DEMO_SMTP_USERNAME "${DEMO_SMTP_USERNAME:-}"
check_scalar DEMO_SMTP_PASSWORD "${DEMO_SMTP_PASSWORD:-}"

smtp_port=${DEMO_SMTP_PORT:-587}
smtp_tls=${DEMO_SMTP_TLS:-starttls}
case $smtp_port in
    '' | *[!0-9]*) die "DEMO_SMTP_PORT must be a whole number" ;;
esac
case $smtp_tls in
    starttls | implicit | none) ;;
    *) die "DEMO_SMTP_TLS must be starttls, implicit or none" ;;
esac

# The deployment build adds the Google and GitHub login methods, so each
# application's identifier and secret is required: a method named in the roster
# with an empty credential fails the startup. The redirect URI each provider
# must hold is the public origin, the capability's base path and the method's
# callback path: $public_origin/api/v1/auth/callback/google and .../github.
require DEMO_GOOGLE_CLIENT_ID "${DEMO_GOOGLE_CLIENT_ID:-}"
require DEMO_GOOGLE_CLIENT_SECRET "${DEMO_GOOGLE_CLIENT_SECRET:-}"
require DEMO_GITHUB_CLIENT_ID "${DEMO_GITHUB_CLIENT_ID:-}"
require DEMO_GITHUB_CLIENT_SECRET "${DEMO_GITHUB_CLIENT_SECRET:-}"

check_scalar DEMO_GOOGLE_CLIENT_ID "$DEMO_GOOGLE_CLIENT_ID"
check_scalar DEMO_GOOGLE_CLIENT_SECRET "$DEMO_GOOGLE_CLIENT_SECRET"
check_scalar DEMO_GITHUB_CLIENT_ID "$DEMO_GITHUB_CLIENT_ID"
check_scalar DEMO_GITHUB_CLIENT_SECRET "$DEMO_GITHUB_CLIENT_SECRET"

# The human-verification gate has two surfaces with a mode of their own. The
# phone surface guards the four sends that address a phone number: SMS login,
# the SMS second factor, phone enrollment and the SMS channel of the anonymous
# password reset. The other surface guards every send that does not address a
# phone number, such as an email reset code. The phone surface runs the
# self-hosted captcha and the other surface the self-hosted proof of work unless
# the deployment names a mode: both mechanisms are self-hosted and need nothing
# rendered but the mode, so the demo reaches no third party. The turnstile mode
# on either surface needs the widget's site key and the secret that
# authenticates siteverify, so both are required only when a selected surface
# is turnstile. A deployment that runs both surfaces off or on a self-hosted
# mechanism carries no Turnstile credential, and the expected hostname is
# always this deployment's own public domain.
humanverify_mode=${DEMO_HUMANVERIFY_MODE:-pow}
humanverify_phone_mode=${DEMO_HUMANVERIFY_PHONE_MODE:-captcha}
case $humanverify_mode in
    off | pow | captcha | turnstile) ;;
    *) die "DEMO_HUMANVERIFY_MODE must be off, pow, captcha or turnstile" ;;
esac
case $humanverify_phone_mode in
    off | pow | captcha | turnstile) ;;
    *) die "DEMO_HUMANVERIFY_PHONE_MODE must be off, pow, captcha or turnstile" ;;
esac
# The Turnstile values are rendered only when a surface reads them, so a gate
# that runs off or on a self-hosted mechanism carries no credential in the
# rendered configuration even when one is present in the environment.
turnstile_site_key=
turnstile_secret_key=
if [ "$humanverify_mode" = turnstile ] || [ "$humanverify_phone_mode" = turnstile ]; then
    require DEMO_TURNSTILE_SITE_KEY "${DEMO_TURNSTILE_SITE_KEY:-}"
    require DEMO_TURNSTILE_SECRET_KEY "${DEMO_TURNSTILE_SECRET_KEY:-}"
    check_scalar DEMO_TURNSTILE_SITE_KEY "$DEMO_TURNSTILE_SITE_KEY"
    check_scalar DEMO_TURNSTILE_SECRET_KEY "$DEMO_TURNSTILE_SECRET_KEY"
    turnstile_site_key=$DEMO_TURNSTILE_SITE_KEY
    turnstile_secret_key=$DEMO_TURNSTILE_SECRET_KEY
fi

mkdir -p /data/var /data/objects

# The replacement is literal. awk's gsub is not used because "&" in a
# replacement means the matched text, and an operator password may contain it.
awk -v origin="$public_origin" \
    -v domain="$public_domain" \
    -v username="$DEMO_OPERATOR_USERNAME" \
    -v email="$DEMO_OPERATOR_EMAIL" \
    -v password="$DEMO_OPERATOR_PASSWORD" \
    -v key="$DEMO_DB_ENCRYPTION_KEY" \
    -v smtp_host="$DEMO_SMTP_HOST" \
    -v smtp_port="$smtp_port" \
    -v smtp_tls="$smtp_tls" \
    -v smtp_username="${DEMO_SMTP_USERNAME:-}" \
    -v smtp_password="${DEMO_SMTP_PASSWORD:-}" \
    -v smtp_from="$DEMO_SMTP_FROM" \
    -v google_id="$DEMO_GOOGLE_CLIENT_ID" \
    -v google_secret="$DEMO_GOOGLE_CLIENT_SECRET" \
    -v github_id="$DEMO_GITHUB_CLIENT_ID" \
    -v github_secret="$DEMO_GITHUB_CLIENT_SECRET" \
    -v humanverify_mode="$humanverify_mode" \
    -v humanverify_phone_mode="$humanverify_phone_mode" \
    -v turnstile_site_key="$turnstile_site_key" \
    -v turnstile_secret_key="$turnstile_secret_key" '
    function replace(line, needle, repl,   out, i) {
        out = ""
        while ((i = index(line, needle)) > 0) {
            out = out substr(line, 1, i - 1) repl
            line = substr(line, i + length(needle))
        }
        return out line
    }
    {
        line = $0
        line = replace(line, "__PUBLIC_ORIGIN__", origin)
        line = replace(line, "__PUBLIC_DOMAIN__", domain)
        line = replace(line, "__OPERATOR_USERNAME__", username)
        line = replace(line, "__OPERATOR_EMAIL__", email)
        line = replace(line, "__OPERATOR_PASSWORD__", password)
        line = replace(line, "__ENCRYPTION_KEY__", key)
        line = replace(line, "__SMTP_HOST__", smtp_host)
        line = replace(line, "__SMTP_PORT__", smtp_port)
        line = replace(line, "__SMTP_TLS__", smtp_tls)
        line = replace(line, "__SMTP_USERNAME__", smtp_username)
        line = replace(line, "__SMTP_PASSWORD__", smtp_password)
        line = replace(line, "__SMTP_FROM__", smtp_from)
        line = replace(line, "__GOOGLE_CLIENT_ID__", google_id)
        line = replace(line, "__GOOGLE_CLIENT_SECRET__", google_secret)
        line = replace(line, "__GITHUB_CLIENT_ID__", github_id)
        line = replace(line, "__GITHUB_CLIENT_SECRET__", github_secret)
        line = replace(line, "__HUMANVERIFY_MODE__", humanverify_mode)
        line = replace(line, "__HUMANVERIFY_PHONE_MODE__", humanverify_phone_mode)
        line = replace(line, "__TURNSTILE_SITE_KEY__", turnstile_site_key)
        line = replace(line, "__TURNSTILE_SECRET_KEY__", turnstile_secret_key)
        print line
    }
' "$template" >"$config.new"

mv "$config.new" "$config"
# Non-recursive: everything under the volume is written by the program after
# this point, so walking a large object store buys nothing.
chown demo:demo /data /data/var /data/objects "$config"
chmod 0600 "$config"

# These are template inputs, not configuration items: the program reports any
# variable under its prefix that no item reads, and each of them would earn a
# diagnostic on every start.
unset DEMO_OPERATOR_USERNAME DEMO_OPERATOR_EMAIL DEMO_OPERATOR_PASSWORD DEMO_DB_ENCRYPTION_KEY DEMO_PUBLIC_URL \
    DEMO_SMTP_HOST DEMO_SMTP_PORT DEMO_SMTP_TLS DEMO_SMTP_USERNAME DEMO_SMTP_PASSWORD DEMO_SMTP_FROM \
    DEMO_GOOGLE_CLIENT_ID DEMO_GOOGLE_CLIENT_SECRET DEMO_GITHUB_CLIENT_ID DEMO_GITHUB_CLIENT_SECRET \
    DEMO_HUMANVERIFY_MODE DEMO_HUMANVERIFY_PHONE_MODE DEMO_TURNSTILE_SITE_KEY DEMO_TURNSTILE_SECRET_KEY

su-exec demo /usr/local/bin/demo --config file:///data/config.yaml sqlite seed
exec su-exec demo /usr/local/bin/demo --config file:///data/config.yaml sqlite serve
