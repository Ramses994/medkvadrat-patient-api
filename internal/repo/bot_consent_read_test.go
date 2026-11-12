package repo

import (
	"os"
	"strings"
	"testing"
)

func TestMessengerNotifyUsesTheReminderColumn(t *testing.T) {
	src, err := os.ReadFile("bot_consent_read.go")
	if err != nil {
		t.Fatal(err)
	}
	reminders, err := os.ReadFile("reminders.go")
	if err != nil {
		t.Fatal(err)
	}
	needle := "CAST(r.MESSENGER_NOTIFY AS int) AS MESSENGER_NOTIFY"
	if !strings.Contains(string(src), needle) || !strings.Contains(string(reminders), needle) {
		t.Fatal("messenger notify select drifted from reminders")
	}
	if !strings.Contains(string(src), `sql.Named("patient_id", patientID)`) {
		t.Fatal("patient id must be a named parameter")
	}
	if SQLLooksMutating(string(src)) {
		t.Fatal("consent read must stay read-only")
	}
}
