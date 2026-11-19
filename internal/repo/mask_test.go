package repo

import "testing"

func TestMaskPhoneDisplay(t *testing.T) {
	if got := MaskPhoneDisplay("79265906994"); got != "+7 *** ***-69-94" {
		t.Fatalf("got %q", got)
	}
	if got := MaskPhoneDisplay("+7 999 123-45-67"); got != "+7 *** ***-45-67" {
		t.Fatalf("got %q", got)
	}
}

func TestMaskEmailDisplay(t *testing.T) {
	if got := MaskEmailDisplay("ivan@example.ru"); got != "i***@example.ru" {
		t.Fatalf("got %q", got)
	}
	if got := MaskEmailDisplay("a@b.c"); got != "a***@b.c" {
		t.Fatalf("got %q", got)
	}
}
