package consents

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/medkvadrat/medkvadrat-patient-api/internal/db"
	"github.com/medkvadrat/medkvadrat-patient-api/internal/store"
)

func openStore(t *testing.T) *Store {
	t.Helper()
	sqlDB, err := db.OpenSQLite(filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := store.Migrate(context.Background(), sqlDB); err != nil {
		t.Fatal(err)
	}
	s := New(sqlDB)
	s.now = func() time.Time { return time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC) }
	return s
}

func countRows(t *testing.T, s *Store, patientID int64) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM bot_consents WHERE patient_id = ?`, patientID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAccept_SameVersionIsIdempotent(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	in := Input{PatientID: 10, MaxUserID: 20, Kind: KindService, TextVersion: "service-v1", Action: "accept"}
	first, _, err := s.Apply(ctx, in)
	if err != nil || first.State != StateActive || first.TextVersion != "service-v1" {
		t.Fatalf("first %+v err=%v", first, err)
	}
	second, _, err := s.Apply(ctx, in)
	if err != nil || second.State != StateActive || second.At != first.At {
		t.Fatalf("second %+v err=%v", second, err)
	}
	if countRows(t, s, 10) != 1 {
		t.Fatalf("rows=%d", countRows(t, s, 10))
	}
}

func TestAccept_VersionChangeRevokesOldInOneTransaction(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if _, _, err := s.Apply(ctx, Input{PatientID: 10, MaxUserID: 20, Kind: KindService, TextVersion: "service-v1", Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	s.failBeforeInsert = func() error { return errors.New("stop") }
	_, _, err := s.Apply(ctx, Input{PatientID: 10, MaxUserID: 21, Kind: KindService, TextVersion: "service-v2", Action: "accept"})
	if err == nil {
		t.Fatal("expected rollback")
	}
	service, _, err := s.Current(ctx, 10)
	if err != nil || service.State != StateActive || service.TextVersion != "service-v1" {
		t.Fatalf("after rollback %+v err=%v", service, err)
	}
	if countRows(t, s, 10) != 1 {
		t.Fatalf("rows=%d", countRows(t, s, 10))
	}

	s.failBeforeInsert = nil
	service, _, err = s.Apply(ctx, Input{PatientID: 10, MaxUserID: 21, Kind: KindService, TextVersion: "service-v2", Action: "accept"})
	if err != nil {
		t.Fatal(err)
	}
	if service.State != StateActive || service.TextVersion != "service-v2" {
		t.Fatalf("new %+v", service)
	}
	if countRows(t, s, 10) != 2 {
		t.Fatalf("rows=%d", countRows(t, s, 10))
	}
	var revoked string
	if err := s.db.QueryRow(`SELECT revoked_at FROM bot_consents WHERE patient_id = 10 AND text_version = 'service-v1'`).Scan(&revoked); err != nil || revoked == "" {
		t.Fatalf("old revoked=%q err=%v", revoked, err)
	}
}

func TestRevoke_WhenNothingIsActive(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	service, _, err := s.Apply(ctx, Input{PatientID: 10, MaxUserID: 20, Kind: KindService, TextVersion: "service-v1", Action: "revoke"})
	if err != nil || service.State != StateNone {
		t.Fatalf("%+v err=%v", service, err)
	}
	if countRows(t, s, 10) != 0 {
		t.Fatalf("rows=%d", countRows(t, s, 10))
	}
	if _, _, err := s.Apply(ctx, Input{PatientID: 10, MaxUserID: 20, Kind: KindService, TextVersion: "service-v1", Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, Input{PatientID: 10, MaxUserID: 20, Kind: KindService, TextVersion: "service-v1", Action: "revoke"}); err != nil {
		t.Fatal(err)
	}
	service, _, err = s.Apply(ctx, Input{PatientID: 10, MaxUserID: 20, Kind: KindService, TextVersion: "service-v1", Action: "revoke"})
	if err != nil || service.State != StateRevoked {
		t.Fatalf("second revoke %+v err=%v", service, err)
	}
	if countRows(t, s, 10) != 1 {
		t.Fatalf("rows=%d", countRows(t, s, 10))
	}
}

func TestPurgeDoesNotDeleteConsents(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if _, _, err := s.Apply(ctx, Input{PatientID: 10, MaxUserID: 20, Kind: KindAds, TextVersion: "ads-v1", Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PurgeClosedWaitlist(ctx, s.db, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if countRows(t, s, 10) != 1 {
		t.Fatal("purge deleted a consent")
	}
}

func TestServiceStates_OneQueryForManyPatients(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if _, _, err := s.Apply(ctx, Input{PatientID: 10, MaxUserID: 1, Kind: KindService, TextVersion: "service-v1", Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, Input{PatientID: 12, MaxUserID: 1, Kind: KindService, TextVersion: "service-v1", Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Apply(ctx, Input{PatientID: 12, MaxUserID: 1, Kind: KindService, TextVersion: "service-v1", Action: "revoke"}); err != nil {
		t.Fatal(err)
	}
	before := s.Reads.Load()
	got, err := s.ServiceStates(ctx, []int64{10, 11, 12, 10})
	if err != nil {
		t.Fatal(err)
	}
	if s.Reads.Load()-before != 1 {
		t.Fatalf("queries=%d", s.Reads.Load()-before)
	}
	if got[10] != StateActive || got[12] != StateRevoked {
		t.Fatalf("%v", got)
	}
	if _, ok := got[11]; ok {
		t.Fatal("patient without rows should be absent")
	}
}

func TestAdsRows_SkipService(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	_, ads, err := s.Apply(ctx, Input{PatientID: 10, MaxUserID: 1, Kind: KindService, TextVersion: "service-v1", Action: "accept"})
	if err != nil || ads.State != StateNone {
		t.Fatalf("ads %+v err=%v", ads, err)
	}
	if _, _, err := s.Apply(ctx, Input{PatientID: 11, MaxUserID: 1, Kind: KindAds, TextVersion: "ads-v1", Action: "accept"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.AdsRows(ctx)
	if err != nil || len(rows) != 1 || rows[0].PatientID != 11 || rows[0].RevokedAt != "" {
		t.Fatalf("%+v err=%v", rows, err)
	}
}
