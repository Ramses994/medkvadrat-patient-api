package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOTP_AdultFilter_AdultAndChild(t *testing.T) {
	child := time.Date(2015, 5, 1, 0, 0, 0, 0, time.UTC)
	adult := time.Date(1990, 5, 1, 0, 0, 0, 0, time.UTC)
	s, lite, sent := otpService(t, []otpRow{
		{id: 11, email: "a@example.com", birth: adult},
		{id: 12, email: "a@example.com", birth: child},
	})
	res, err := requestOTP(s)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := s.OTPVerify(context.Background(), res.RequestID, sent["a@example.com"])
	if err != nil {
		t.Fatal(err)
	}
	if verified.Access == "" || len(verified.Candidates) != 0 || tokenPatient(t, lite) != 11 {
		t.Fatalf("want single adult login, got %+v patient=%d", verified, tokenPatient(t, lite))
	}
}

func TestOTP_AdultFilter_OnlyChildLikeUnknown(t *testing.T) {
	child := time.Date(2015, 5, 1, 0, 0, 0, 0, time.UTC)
	s, _, _ := otpService(t, []otpRow{{id: 12, email: "a@example.com", birth: child}})
	unknown, _, _ := otpService(t, []otpRow{})
	statusChild, bodyChild, _ := postOTPRequest(t, s)
	statusUnk, bodyUnk, _ := postOTPRequest(t, unknown)
	if statusChild != http.StatusOK || statusUnk != http.StatusOK {
		t.Fatalf("status child=%d unk=%d", statusChild, statusUnk)
	}
	if bodyChild != bodyUnk {
		t.Fatalf("bodies differ:\n%s\n%s", bodyChild, bodyUnk)
	}
}

func TestOTP_AdultFilter_Exactly18Today(t *testing.T) {
	loc := time.FixedZone("MSK", 3*3600)
	today := time.Now().In(loc)
	born := time.Date(today.Year()-18, today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	s, lite, sent := otpService(t, []otpRow{{id: 11, email: "a@example.com", birth: born}})
	res, err := requestOTP(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OTPVerify(context.Background(), res.RequestID, sent["a@example.com"]); err != nil {
		t.Fatal(err)
	}
	if tokenPatient(t, lite) != 11 {
		t.Fatalf("patient=%d", tokenPatient(t, lite))
	}
}

func TestOTP_AdultFilter_NoBirthHidden(t *testing.T) {
	s, _, _ := otpService(t, []otpRow{{id: 11, email: "a@example.com", noDefault: true, birth: nil}})
	unknown, _, _ := otpService(t, []otpRow{})
	_, bodyNoBirth, _ := postOTPRequest(t, s)
	_, bodyUnk, _ := postOTPRequest(t, unknown)
	if bodyNoBirth != bodyUnk {
		t.Fatalf("bodies differ:\n%s\n%s", bodyNoBirth, bodyUnk)
	}
}

func TestOTP_Delivery_PilotAnswersAlike(t *testing.T) {
	cases := map[string][]otpRow{
		"in list":  {{id: 11, email: "a@example.com"}},
		"no card":  {},
		"no email": {{id: 11, email: ""}},
	}
	bodies := map[string]string{}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			s, _, sent := otpService(t, rows)
			pilotMode(s)
			status, body, _ := postOTPRequest(t, s)
			if status != http.StatusOK {
				t.Fatalf("status=%d", status)
			}
			waitOTPMail(s)
			if len(sent) != 0 {
				t.Fatalf("mails=%d", len(sent))
			}
			if !strings.Contains(body, `"delivery":"pilot"`) {
				t.Fatalf("body=%s", body)
			}
			bodies[name] = body
		})
	}
	if bodies["in list"] == "" || bodies["in list"] != bodies["no card"] || bodies["in list"] != bodies["no email"] {
		t.Fatalf("bodies differ:\n%s\n%s\n%s", bodies["in list"], bodies["no card"], bodies["no email"])
	}
}

func TestOTP_Delivery_ProdIsEmail(t *testing.T) {
	s, _, _ := otpService(t, []otpRow{{id: 11, email: "a@example.com"}})
	res, err := requestOTP(s)
	if err != nil || res.Delivery != "email" {
		t.Fatalf("delivery=%q err=%v", res.Delivery, err)
	}
}
