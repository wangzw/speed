package authn

import (
	"context"
	"errors"
	"time"
)

// ErrEmailUncertain reports a verification-code send whose delivery is not known:
// the carrier call failed in a way that leaves open whether the message left. A
// channel wraps this sentinel when it cannot certify that nothing was handed
// over.
//
// The login module answers the same 503 whatever the channel returns, so this
// sentinel tells the client nothing. It is the seam's classification for the
// budget: a failure that certifies nothing was sent gives back the counts the
// attempt reserved, while an uncertain one keeps them because the message may
// have gone out.
var ErrEmailUncertain = errors.New("authn: whether the verification email was delivered is not known")

// EmailCodeSender is the channel a self-service email verification code goes out
// over. It is the sending half only and nothing more: the login module draws the
// code, holds the pending record and checks the answer, and this seam renders the
// message and delivers it. It is a host-provided capability like Provisioner —
// the repository ships no email channel, so a deployment that wants a self-service
// email address to be addable provides one, over whatever carrier it has.
//
// The interface lives beside the other authentication seams and its contract is
// the sending of one code to one address. It is taken by the login module as
// optional; without it an email address cannot be added on the self-service face,
// and the route answers that the deployment offers no channel.
//
// A channel may also publish CertifiesNotSent(err error) bool, reporting whether
// a failure certifies that no message was handed to a carrier. The bundled
// channel does, and the login module reads it to decide whether the attempt gives
// back the send budget it reserved. A channel that does not publish it is treated
// as uncertain: its failures keep the budget, because the message may have left.
type EmailCodeSender interface {
	// SendCode delivers code to the address to, rendered in the language locale,
	// and valid for ttl. It returns an error when the message could not be handed
	// to a carrier: a delivery whose outcome is unknown counts as an error too and
	// is wrapped in ErrEmailUncertain, and the caller answers every error the same
	// way, a 503, without telling the client whether the message left. A code
	// reaching no log, no trace, no event and no error text is the implementer's
	// to keep.
	SendCode(ctx context.Context, to, code string, ttl time.Duration, locale string) error
}
