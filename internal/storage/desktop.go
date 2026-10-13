package storage

import (
	"database/sql"
	"time"
)

type PowerTarget struct {
	AccountID int64  `json:"account_id"`
	DesktopID string `json:"desktop_id"`
}
type PowerItem struct {
	PowerTarget
	AccountName string `json:"account_name"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	Position    int    `json:"position"`
}
type PowerJob struct {
	ID        int64       `json:"id"`
	Action    string      `json:"action"`
	Status    string      `json:"status"`
	CreatedAt string      `json:"created_at"`
	Items     []PowerItem `json:"items"`
}

func (s *Store) AddPowerJob(action string, targets []PowerTarget) (int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	r, err := tx.Exec("INSERT INTO power_jobs(action,status,created_at) VALUES(?,'running',?)", action, Now())
	if err != nil {
		return 0, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return 0, err
	}
	for i, t := range targets {
		if _, err = tx.Exec("INSERT INTO power_items(job_id,position,account_id,desktop_id,status) VALUES(?,?,?,?,'queued')", id, i, t.AccountID, t.DesktopID); err != nil {
			return 0, err
		}
	}
	return id, tx.Commit()
}
func (s *Store) PowerJob(id int64) (PowerJob, error) {
	var j PowerJob
	err := s.DB.QueryRow("SELECT id,action,status,created_at FROM power_jobs WHERE id=?", id).Scan(&j.ID, &j.Action, &j.Status, &j.CreatedAt)
	if err != nil {
		return j, err
	}
	rows, err := s.DB.Query("SELECT p.position,p.account_id,p.desktop_id,p.name,p.status,p.message,COALESCE(a.name,'') FROM power_items p LEFT JOIN accounts a ON a.id=p.account_id WHERE p.job_id=? ORDER BY p.position", id)
	if err != nil {
		return j, err
	}
	defer rows.Close()
	j.Items = []PowerItem{}
	for rows.Next() {
		var v PowerItem
		if err := rows.Scan(&v.Position, &v.AccountID, &v.DesktopID, &v.Name, &v.Status, &v.Message, &v.AccountName); err != nil {
			return j, err
		}
		j.Items = append(j.Items, v)
	}
	return j, rows.Err()
}
func (s *Store) SetPowerItem(id int64, pos int, name, status, message string) error {
	_, err := s.DB.Exec("UPDATE power_items SET name=?,status=?,message=? WHERE job_id=? AND position=?", name, status, message, id, pos)
	return err
}
func (s *Store) FinishPowerJob(id int64, status string) error {
	_, err := s.DB.Exec("UPDATE power_jobs SET status=? WHERE id=?", status, id)
	return err
}
func (s *Store) InterruptPowerJobs() error {
	_, err := s.DB.Exec(`UPDATE power_items SET status='unknown',message='服务重启，指令可能已提交，请刷新云机状态后核对' WHERE status='submitting';
 UPDATE power_items SET status='cancelled',message='服务重启，未提交的操作已取消' WHERE status IN ('queued','validating');
 UPDATE power_jobs SET status='interrupted' WHERE status='running';`)
	return err
}
func (s *Store) SetDesktopHold(t PowerTarget, until int64, reason string) error {
	_, err := s.DB.Exec(`INSERT INTO desktop_holds(account_id,desktop_id,until_unix,reason) VALUES(?,?,?,?) ON CONFLICT(account_id,desktop_id) DO UPDATE SET until_unix=excluded.until_unix,reason=excluded.reason`, t.AccountID, t.DesktopID, until, reason)
	return err
}
func (s *Store) ClearDesktopHold(t PowerTarget) error {
	_, err := s.DB.Exec("DELETE FROM desktop_holds WHERE account_id=? AND desktop_id=?", t.AccountID, t.DesktopID)
	return err
}
func (s *Store) DesktopHold(t PowerTarget) (string, error) {
	var reason string
	var until int64
	err := s.DB.QueryRow("SELECT until_unix,reason FROM desktop_holds WHERE account_id=? AND desktop_id=?", t.AccountID, t.DesktopID).Scan(&until, &reason)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if until > 0 && until <= time.Now().Unix() {
		return "", nil
	}
	return reason, nil
}
func (s *Store) DesktopHoldSnapshot(t PowerTarget) (int64, string, error) {
	var until int64
	var reason string
	err := s.DB.QueryRow("SELECT until_unix,reason FROM desktop_holds WHERE account_id=? AND desktop_id=?", t.AccountID, t.DesktopID).Scan(&until, &reason)
	if err == sql.ErrNoRows {
		return 0, "", nil
	}
	return until, reason, err
}
func (s *Store) ExpireDesktopHolds() ([]int64, error) {
	rows, err := s.DB.Query("SELECT DISTINCT account_id FROM desktop_holds WHERE until_unix>0 AND until_unix<=?", time.Now().Unix())
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	_, err = s.DB.Exec("DELETE FROM desktop_holds WHERE until_unix>0 AND until_unix<=?", time.Now().Unix())
	return ids, err
}
