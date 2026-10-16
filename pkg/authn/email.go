package authn

import (
	"context"
	"time"
)

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
type EmailCodeSender interface {
	// SendCode delivers code to the address to, rendered in the language locale,
	// and valid for ttl. It returns an error when the message could not be handed
	// to a carrier: a delivery whose outcome is unknown counts as an error too,
	// and the caller answers every error the same way, a 503, without telling the
	// client whether the message left. A code reaching no log, no trace, no event
	// and no error text is the implementer's to keep.
	SendCode(ctx context.Context, to, code string, ttl time.Duration, locale string) error
}
