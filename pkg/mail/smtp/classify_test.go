package smtp

import (
	"errors"
	"strconv"
	"testing"

	"github.com/vislake/speed/pkg/mail"
)

// TestTheReplyClassTableCoversEachClassAndDefaultsToUncertain checks the reply
// class table's boundaries, with other codes falling to ErrUncertain.
func TestTheReplyClassTableCoversEachClassAndDefaultsToUncertain(t *testing.T) {
	cases := []struct {
		code int
		want error
	}{
		{400, mail.ErrThrottled},
		{421, mail.ErrThrottled},
		{451, mail.ErrThrottled},
		{499, mail.ErrThrottled},
		{500, mail.ErrRejected},
		{554, mail.ErrRejected},
		{599, mail.ErrRejected},
		{200, mail.ErrUncertain},
		{250, mail.ErrUncertain},
		{354, mail.ErrUncertain},
		{600, mail.ErrUncertain},
	}
	for _, c := range cases {
		t.Run(strconv.Itoa(c.code), func(t *testing.T) {
			if err := classified(c.code); !errors.Is(err, c.want) {
				t.Fatalf("the code %d gave %v, want %v", c.code, err, c.want)
			}
		})
	}
}
