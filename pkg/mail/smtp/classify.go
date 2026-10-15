package smtp

import (
	"errors"
	"net/textproto"
	"strconv"

	gomail "github.com/wneessen/go-mail"

	"github.com/vislake/speed/pkg/mail"
)

// classify turns a failed send into one of the sending sentinels.
//
// The fact that decides most of it is the server's reply code and its class: a
// 4xx (transient negative) is a throttle, a 5xx (permanent negative) is a
// rejection. A connection that fell over, a timeout and an ended context are
// uncertain, because the send is not idempotent and a call that did not complete
// may still have delivered the message — the same reason every other failure
// falls to the uncertain sentinel.
func classify(err error) error {
	var proto *textproto.Error
	if errors.As(err, &proto) {
		return classified(proto.Code)
	}
	var send *gomail.SendError
	if errors.As(err, &send) {
		if code := send.ErrorCode(); code != 0 {
			return classified(code)
		}
		// A failure the library raised itself carries no server code: the call
		// did not complete, so nothing settles it either way.
		return mail.NoStatus(carrierName)
	}
	return mail.Unsettled(carrierName)
}

// classified maps one numeric reply code to its sentinel through the class
// table. The code, not the server's message text, is what is carried into the
// result: the text can quote the request and the request carries the message.
func classified(code int) error {
	return mail.Classify(replySentinels, carrierName, strconv.Itoa(code))
}

// replySentinels maps every reply code RFC 5321 gives a class to the sentinel
// that class means: 4xx is a transient negative completion, a throttle, and 5xx
// is a permanent negative completion, a rejection. A code outside both classes —
// a 2xx or 3xx arriving where an error was expected — is left to the uncertain
// default the table's absence gives it.
var replySentinels = buildSentinels()

// buildSentinels fills the class table. It is built rather than written out
// because the rule is the class, not an enumerated set: any 4xx or 5xx code the
// server can answer is covered.
func buildSentinels() map[string]error {
	const classes = 2 // 4xx and 5xx
	const codes = 100 // each class runs 00-99
	table := make(map[string]error, classes*codes)
	for code := 400; code < 600; code++ {
		if code < 500 {
			table[strconv.Itoa(code)] = mail.ErrThrottled
		} else {
			table[strconv.Itoa(code)] = mail.ErrRejected
		}
	}
	return table
}
