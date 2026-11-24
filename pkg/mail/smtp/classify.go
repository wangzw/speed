package smtp

import (
	"errors"
	"net/textproto"
	"strconv"

	gomail "github.com/wneessen/go-mail"

	"github.com/vislake/speed/pkg/mail"
)

// classify turns a failed send into a sending sentinel: a 4xx reply is a
// throttle, a 5xx reply a rejection, and an incomplete call is uncertain.
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
		return mail.NoStatus(carrierName)
	}
	return mail.Unsettled(carrierName)
}

// classified maps one numeric reply code to its sentinel; the code, not the
// server's message text, is carried because the text can quote the message.
func classified(code int) error {
	return mail.Classify(replySentinels, carrierName, strconv.Itoa(code))
}

var replySentinels = buildSentinels()

func buildSentinels() map[string]error {
	const classes = 2
	const codes = 100
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
