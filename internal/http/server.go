package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"shuber-site/internal/auth"
	"shuber-site/internal/db"
	"shuber-site/internal/storage"
)

type Options struct {
	MediaDir       string
	CookieSecure   bool
	MaxUploadBytes int64
	Production     bool
	BlobStore      *storage.R2Client
}

type Server struct {
	Auth           *auth.Service
	MediaDir       string
	CookieSecure   bool
	MaxUploadBytes int64
	Production     bool
	BlobStore      *storage.R2Client
	loginLimiter   *rateLimiter
}

type credentials struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

type ctxKey string

const identityKey ctxKey = "identity"

func New(a *auth.Service, opts Options) *Server {
	maxUpload := opts.MaxUploadBytes
	if maxUpload <= 0 {
		maxUpload = 200 << 20
	}
	return &Server{
		Auth:           a,
		MediaDir:       opts.MediaDir,
		CookieSecure:   opts.CookieSecure,
		MaxUploadBytes: maxUpload,
		Production:     opts.Production,
		BlobStore:      opts.BlobStore,
		loginLimiter:   newRateLimiter(10, time.Minute),
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/tracks", s.listTracks)
	mux.HandleFunc("POST /api/register", s.register)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.me)
	mux.HandleFunc("GET /api/admin", s.admin)
	mux.HandleFunc("POST /api/admin/tracks", s.createTrack)
	mux.HandleFunc("DELETE /api/admin/tracks/{id}", s.deleteTrack)
	if s.BlobStore == nil {
		mux.Handle("/media/", http.StripPrefix("/media/", http.FileServer(http.Dir(s.MediaDir))))
	}
	mux.Handle("/", http.FileServer(http.Dir("web/static")))
	return withSession(s.Auth, securityHeaders(s), s.csrfGuard(mux))
}

func securityHeaders(s *Server) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
			if s.Production {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
				w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isStateChanging(r.Method) {
			if !s.sameOrigin(r) {
				http.Error(w, "cross-origin request rejected", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isStateChanging(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = r.Header.Get("X-Forwarded-Host")
	}
	return strings.EqualFold(origin, scheme+"://"+host)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ok": true, "service": "shuber"})
}

func (s *Server) listTracks(w http.ResponseWriter, r *http.Request) {
	ts := s.Auth.Store.Tracks()
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
	writeJSON(w, map[string]any{"tracks": ts})
}

func decodeCred(r *http.Request) (credentials, error) {
	var in credentials
	dec := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	err := dec.Decode(&in)
	return in, err
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	return strings.Split(r.RemoteAddr, ":")[0]
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimiter.Allow("register:" + clientIP(r)) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	in, err := decodeCred(r)
	if err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	id, err := s.Auth.Register(in.Login, in.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, id)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimiter.Allow("login:" + clientIP(r)) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	in, err := decodeCred(r)
	if err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	id, token, err := s.Auth.Login(in.Login, in.Password)
	if err != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "shuber_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   s.Auth.SessionTTLSeconds,
	})
	writeJSON(w, id)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("shuber_session"); err == nil {
		s.Auth.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "shuber_session", Value: "", Path: "/", HttpOnly: true, Secure: s.CookieSecure, MaxAge: -1, SameSite: http.SameSiteLaxMode})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	id, ok := identityFromRequest(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, id)
}

func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	id, ok := identityFromRequest(r)
	if !ok || id.Role != "admin" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	writeJSON(w, map[string]any{"message": "admin area", "identity": id, "track_count": len(s.Auth.Store.Tracks())})
}

func requireAdmin(r *http.Request) (auth.Identity, bool) {
	id, ok := identityFromRequest(r)
	return id, ok && id.Role == "admin"
}

func sanitizeName(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	var b strings.Builder
	for _, r := range v {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "file"
	}
	return b.String()
}

func saveUploadLocal(f multipart.File, h *multipart.FileHeader, dir, stem string, allowed map[string]bool, maxBytes int64) (string, error) {
	defer f.Close()
	if h.Size > maxBytes {
		return "", fmt.Errorf("file exceeds %d MB", maxBytes/(1<<20))
	}
	ext := strings.ToLower(filepath.Ext(h.Filename))
	if !allowed[ext] {
		return "", fmt.Errorf("unsupported file type %s", ext)
	}
	name := stem + ext
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	dst, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err = io.Copy(dst, io.LimitReader(f, maxBytes+1)); err != nil {
		return "", err
	}
	return "/media/" + filepath.Base(name), nil
}

func contentTypeForExt(ext string) string {
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	switch ext {
	case ".m4a":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".webp":
		return "image/webp"
	default:
		return "application/octet-stream"
	}
}

func (s *Server) saveUpload(r *http.Request, f multipart.File, h *multipart.FileHeader, key string, allowed map[string]bool, maxBytes int64) (string, error) {
	ext := strings.ToLower(filepath.Ext(h.Filename))
	if !allowed[ext] {
		_ = f.Close()
		return "", fmt.Errorf("unsupported file type %s", ext)
	}
	if h.Size > maxBytes {
		_ = f.Close()
		return "", fmt.Errorf("file exceeds %d MB", maxBytes/(1<<20))
	}
	if s.BlobStore == nil {
		return saveUploadLocal(f, h, s.MediaDir, key, allowed, maxBytes)
	}
	defer f.Close()
	if err := s.BlobStore.Put(r.Context(), "media/"+key+ext, io.LimitReader(f, maxBytes+1), h.Size, contentTypeForExt(ext)); err != nil {
		return "", err
	}
	return "/media/" + key + ext, nil
}

func (s *Server) createTrack(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(r); !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxUploadBytes)
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		http.Error(w, "bad form or file too large", http.StatusBadRequest)
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if id == "" {
		id = fmt.Sprintf("%02d", len(s.Auth.Store.Tracks())+1)
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "title required", http.StatusBadRequest)
		return
	}
	bpm, _ := strconv.Atoi(r.FormValue("bpm"))
	mood := strings.TrimSpace(r.FormValue("mood"))
	subtitle := strings.TrimSpace(r.FormValue("subtitle"))
	audioPath := ""
	coverPath := ""
	audioAllowed := map[string]bool{".mp3": true, ".ogg": true, ".wav": true, ".m4a": true, ".aac": true, ".flac": true}
	coverAllowed := map[string]bool{".webp": true, ".jpg": true, ".jpeg": true, ".png": true}
	safeID := sanitizeName(id)
	if f, h, err := r.FormFile("audio"); err == nil {
		var saveErr error
		audioPath, saveErr = s.saveUpload(r, f, h, "audio/track-"+safeID, audioAllowed, s.MaxUploadBytes)
		if saveErr != nil {
			http.Error(w, saveErr.Error(), http.StatusBadRequest)
			return
		}
	}
	if f, h, err := r.FormFile("cover"); err == nil {
		var saveErr error
		coverPath, saveErr = s.saveUpload(r, f, h, "covers/cover-"+safeID, coverAllowed, min64(s.MaxUploadBytes, 10<<20))
		if saveErr != nil {
			http.Error(w, saveErr.Error(), http.StatusBadRequest)
			return
		}
	}
	t := db.Track{ID: id, Title: title, Subtitle: subtitle, BPM: bpm, Mood: mood, AudioPath: audioPath, CoverPath: coverPath, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	if err := s.Auth.Store.UpsertTrack(t); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, t)
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func mediaKey(mediaPath string) string {
	v := strings.TrimPrefix(mediaPath, "/media/")
	return "media/" + strings.TrimLeft(v, "/")
}

func (s *Server) deleteTrack(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireAdmin(r); !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	snap := s.Auth.Store.Snapshot()
	if t, ok := snap.Tracks[id]; ok {
		if s.BlobStore != nil {
			if t.AudioPath != "" {
				_ = s.BlobStore.Delete(r.Context(), mediaKey(t.AudioPath))
			}
			if t.CoverPath != "" {
				_ = s.BlobStore.Delete(r.Context(), mediaKey(t.CoverPath))
			}
		} else {
			if t.AudioPath != "" {
				_ = os.Remove(filepath.Join(s.MediaDir, filepath.Base(t.AudioPath)))
			}
			if t.CoverPath != "" {
				_ = os.Remove(filepath.Join(s.MediaDir, filepath.Base(t.CoverPath)))
			}
		}
	}
	if err := s.Auth.Store.DeleteTrack(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func withSession(a *auth.Service, nextMiddleware func(http.Handler) http.Handler, next http.Handler) http.Handler {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("shuber_session"); err == nil {
			if id, err := a.Authenticate(c.Value); err == nil {
				r = r.WithContext(context.WithValue(r.Context(), identityKey, id))
			}
		}
		next.ServeHTTP(w, r)
	})
	return nextMiddleware(h)
}

func identityFromRequest(r *http.Request) (auth.Identity, bool) {
	v := r.Context().Value(identityKey)
	id, ok := v.(auth.Identity)
	return id, ok
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

type rateLimiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	clients map[string]bucket
}

type bucket struct {
	start time.Time
	count int
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window, clients: make(map[string]bucket)}
}

func (l *rateLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b := l.clients[key]
	if b.start.IsZero() || now.Sub(b.start) >= l.window {
		l.clients[key] = bucket{start: now, count: 1}
		return true
	}
	if b.count >= l.max {
		return false
	}
	b.count++
	l.clients[key] = b
	return true
}
