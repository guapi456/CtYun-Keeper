package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/storage"
)

const viewerCloseGrace = 15 * time.Second
const viewerHeartbeatGrace = 150 * time.Second

func (s *Server) viewerPulseOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "null" {
		return true
	}
	scheme := "http"
	if r.TLS != nil || (s.secure && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	return strings.EqualFold(origin, scheme+"://"+r.Host)
}
func (s *Server) queueViewerResumeLocked(target storage.PowerTarget) {
	if target.AccountID <= 0 || target.DesktopID == "" {
		return
	}
	if s.viewerResumes == nil {
		s.viewerResumes = map[storage.PowerTarget]bool{}
	}
	s.viewerResumes[target] = true
}
func (s *Server) collectViewerResumes(now time.Time) []storage.PowerTarget {
	s.viewerMu.Lock()
	defer s.viewerMu.Unlock()
	for id, v := range s.viewers {
		ended := !now.Before(v.Expires) || (!v.ClosedAt.IsZero() && now.Sub(v.ClosedAt) >= viewerCloseGrace) || now.Sub(v.LastSeen) >= viewerHeartbeatGrace
		if ended {
			s.queueViewerResumeLocked(v.Login.Target)
			delete(s.viewers, id)
		}
	}
	var result []storage.PowerTarget
	for target := range s.viewerResumes {
		active := false
		for _, v := range s.viewers {
			if v.Login.Target == target {
				active = true
				break
			}
		}
		if !active {
			result = append(result, target)
		}
	}
	return result
}
func (s *Server) startViewerLifecycle() {
	s.updateWG.Add(1)
	go func() {
		defer s.updateWG.Done()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case now := <-ticker.C:
				s.viewerOpMu.Lock()
				for _, target := range s.collectViewerResumes(now) {
					if s.manager != nil && s.manager.ResumeDesktopBrowser(target) != nil {
						continue
					}
					s.viewerMu.Lock()
					delete(s.viewerResumes, target)
					s.viewerMu.Unlock()
				}
				s.viewerOpMu.Unlock()
			}
		}
	}()
}
