package db

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

type User struct {
	ID           int64  `json:"id"`
	Login        string `json:"login"`
	PasswordHash string `json:"password_hash"`
	Role         string `json:"role"`
	CreatedAt    string `json:"created_at"`
}

type Session struct {
	TokenHash string `json:"token_hash"`
	UserID    int64  `json:"user_id"`
	ExpiresAt string `json:"expires_at"`
}

type Track struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle"`
	BPM       int    `json:"bpm"`
	Mood      string `json:"mood"`
	AudioPath string `json:"audio_path"`
	CoverPath string `json:"cover_path"`
	CreatedAt string `json:"created_at"`
}

type Data struct {
	NextUserID int64              `json:"next_user_id"`
	Users      map[string]User    `json:"users"`
	Sessions   map[string]Session `json:"sessions"`
	Tracks     map[string]Track   `json:"tracks"`
}

type BlobStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	PutBytes(ctx context.Context, key string, data []byte, contentType string) error
}

type Store struct {
	mu      sync.Mutex
	data    Data
	load    func() ([]byte, error)
	persist func([]byte) error
}

func New(path string) (*Store, error) {
	s := &Store{}
	s.load = func() ([]byte, error) {
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return b, err
	}
	s.persist = func(b []byte) error {
		if dir := dirOf(path); dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return err
			}
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, b, 0600); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}
	return s.init(nil)
}

func NewRemote(blobs BlobStore, ctx context.Context, key string) (*Store, error) {
	s := &Store{}
	s.load = func() ([]byte, error) { return blobs.Get(ctx, key) }
	s.persist = func(b []byte) error { return blobs.PutBytes(ctx, key, b, "application/json; charset=utf-8") }
	return s.init(ctx)
}

func (s *Store) init(_ context.Context) (*Store, error) {
	s.data = Data{NextUserID: 1, Users: map[string]User{}, Sessions: map[string]Session{}, Tracks: map[string]Track{}}
	b, err := s.load()
	if err == nil {
		if err := json.Unmarshal(b, &s.data); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if s.data.Users == nil {
		s.data.Users = map[string]User{}
	}
	if s.data.Sessions == nil {
		s.data.Sessions = map[string]Session{}
	}
	if s.data.Tracks == nil {
		s.data.Tracks = map[string]Track{}
	}
	if s.data.NextUserID < 1 {
		s.data.NextUserID = 1
	}
	return s, nil
}

func (s *Store) Close() error   { return s.persistNow() }
func (s *Store) Snapshot() Data { s.mu.Lock(); defer s.mu.Unlock(); return s.data }

func (s *Store) AddUser(login, hash string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Users[login]; ok {
		return User{}, errors.New("login already exists")
	}
	u := User{ID: s.data.NextUserID, Login: login, PasswordHash: hash, Role: "user", CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	s.data.NextUserID++
	s.data.Users[login] = u
	return u, s.persistLocked()
}
func (s *Store) SetUserRole(login, role string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.data.Users[login]
	if !ok {
		return errors.New("user not found")
	}
	u.Role = role
	s.data.Users[login] = u
	return s.persistLocked()
}
func (s *Store) User(login string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.data.Users[login]
	return u, ok
}
func (s *Store) UserByID(id int64) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.data.Users {
		if u.ID == id {
			return u, true
		}
	}
	return User{}, false
}
func (s *Store) SaveSession(sess Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Sessions[sess.TokenHash] = sess
	return s.persistLocked()
}
func (s *Store) Session(tokenHash string) (Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.data.Sessions[tokenHash]
	return sess, ok
}
func (s *Store) DeleteSession(tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Sessions, tokenHash)
	return s.persistLocked()
}
func (s *Store) UpsertTrack(t Track) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Tracks[t.ID] = t
	return s.persistLocked()
}
func (s *Store) DeleteTrack(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Tracks, id)
	return s.persistLocked()
}
func (s *Store) Tracks() []Track {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Track, 0, len(s.data.Tracks))
	for _, t := range s.data.Tracks {
		out = append(out, t)
	}
	return out
}

func (s *Store) persistNow() error { s.mu.Lock(); defer s.mu.Unlock(); return s.persistLocked() }
func (s *Store) persistLocked() error {
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return s.persist(b)
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			if i == 0 {
				return string(path[0])
			}
			return path[:i]
		}
	}
	return "."
}
