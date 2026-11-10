package api

import (
	"os"
	"testing"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/staff"
	"golang.org/x/crypto/bcrypt"
)

func TestMain(m *testing.M) {
	staff.BcryptCost = bcrypt.MinCost
	os.Exit(m.Run())
}
