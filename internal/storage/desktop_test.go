package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPowerJobRestartNeverReplaysRequests(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, err := s.AddPowerJob("reboot", []PowerTarget{{1, "a"}, {1, "b"}, {1, "c"}})
	if err != nil {
		t.Fatal(err)
	}
	s.SetPowerItem(id, 0, "a", "accepted", "ok")
	s.SetPowerItem(id, 1, "b", "submitting", "")
	if err = s.InterruptPowerJobs(); err != nil {
		t.Fatal(err)
	}
	j, err := s.PowerJob(id)
	if err != nil || j.Status != "interrupted" || j.Items[0].Status != "accepted" || j.Items[1].Status != "unknown" || j.Items[2].Status != "cancelled" {
		t.Fatal(j, err)
	}
}

func TestPowerJobReadsAccountDisplayNameAndRetainsDeletedAccountHistory(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.DB.Exec("INSERT INTO accounts(id,name,username,password_encrypted,device_code,created_at,updated_at) VALUES(1,'显示名称','u','p','d','n','n')"); err != nil {
		t.Fatal(err)
	}
	id, err := s.AddPowerJob("poweron", []PowerTarget{{1, "desktop"}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.PowerJob(id)
	if err != nil || job.Items[0].AccountName != "显示名称" {
		t.Fatal(job, err)
	}
	s.DB.Exec("UPDATE accounts SET name='新名称' WHERE id=1")
	job, err = s.PowerJob(id)
	if err != nil || job.Items[0].AccountName != "新名称" {
		t.Fatal(job, err)
	}
	s.DB.Exec("DELETE FROM accounts WHERE id=1")
	job, err = s.PowerJob(id)
	if err != nil || len(job.Items) != 1 || job.Items[0].AccountName != "" || job.Items[0].AccountID != 1 {
		t.Fatal(job, err)
	}
}
func TestDesktopHoldPersistenceAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB.Exec("INSERT INTO accounts(id,name,username,password_encrypted,device_code,created_at,updated_at) VALUES(1,'n','u','p','d','n','n')")
	if err != nil {
		t.Fatal(err)
	}
	target := PowerTarget{1, "a"}
	if err = s.SetDesktopHold(target, 0, "shutdown"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if hold, err := s.DesktopHold(target); err != nil || hold != "shutdown" {
		t.Fatal(hold, err)
	}
	s.SetDesktopHold(PowerTarget{1, "b"}, time.Now().Add(-time.Minute).Unix(), "expired")
	ids, err := s.ExpireDesktopHolds()
	if err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatal(ids, err)
	}
	if hold, _ := s.DesktopHold(target); hold != "shutdown" {
		t.Fatal("indefinite hold expired")
	}
}
