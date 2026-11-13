package otp

import (
	"errors"
	"fmt"
	"net/textproto"
	"os"
	"testing"
)

func TestSMTPErrorCode(t *testing.T) {
	cases := map[string]error{
		"timeout":  fmt.Errorf("smtp dial: %w", os.ErrDeadlineExceeded),
		"auth":     fmt.Errorf("%w: %w", errSMTPAuth, errors.New("unencrypted connection")),
		"rejected": fmt.Errorf("smtp rcpt: %w", &textproto.Error{Code: 550, Msg: "mailbox unavailable"}),
		"other":    errors.New("smtp not configured"),
	}
	for want, err := range cases {
		if got := SMTPErrorCode(err); got != want {
			t.Fatalf("%v: got %s, want %s", err, got, want)
		}
	}
	if got := SMTPErrorCode(&textproto.Error{Code: 535, Msg: "bad credentials"}); got != "auth" {
		t.Fatalf("535: got %s", got)
	}
}
