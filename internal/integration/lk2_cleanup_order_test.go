package integration

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// An early Fatal still cancels the visit before close. The child process is the Fatal.
func TestCleanupCancelsBeforeClose(t *testing.T) {
	if os.Getenv("LK2_CLEANUP_CHILD") == "1" {
		t.Cleanup(func() { fmt.Println("LK2_EVENT close") })
		t.Cleanup(func() { fmt.Println("LK2_EVENT cancel") })
		fmt.Println("LK2_EVENT created")
		t.Fatal("stop early")
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCleanupCancelsBeforeClose", "-test.count=1")
	cmd.Env = append(os.Environ(), "LK2_CLEANUP_CHILD=1")
	out, err := cmd.CombinedOutput()
	text := string(out)
	created := strings.Index(text, "LK2_EVENT created")
	cancel := strings.Index(text, "LK2_EVENT cancel")
	closed := strings.Index(text, "LK2_EVENT close")
	if created < 0 || cancel < created || closed < cancel {
		t.Fatalf("order err=%v\n%s", err, text)
	}
	if err == nil {
		t.Fatal("child should stop on Fatal")
	}
}
