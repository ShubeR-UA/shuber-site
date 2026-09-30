package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"shuber-site/internal/db"
)

type Identity struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Role  string `json:"role"`
}
type Service struct {
	Store             *db.Store
	SessionTTLSeconds int
}

func derivePassword(password string, salt []byte) []byte {
	iterations := 150_000
	blockIndex := []byte{0, 0, 0, 1}
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte(password))
	mac.Write(blockIndex)
	u := mac.Sum(nil)
	out := append([]byte(nil), u...)
	for i := 1; i < iterations; i++ {
		mac = hmac.New(sha256.New, salt)
		mac.Write(u)
		u = mac.Sum(nil)
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return out
}
func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	dk := derivePassword(password, salt)
	return base64.RawStdEncoding.EncodeToString(append(salt, dk...)), nil
}
func checkPassword(encoded, password string) bool {
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(raw) != 48 {
		return false
	}
	want := derivePassword(password, raw[:16])
	return hmac.Equal(want, raw[16:])
}
func valid(login, password string) bool {
	return len(strings.TrimSpace(login)) >= 3 && len(password) >= 6
}
func (s *Service) Register(login, password string) (Identity, error) {
	if !valid(login, password) {
		return Identity{}, errors.New("login >= 3 chars, password >= 6 chars")
	}
	h, err := hashPassword(password)
	if err != nil {
		return Identity{}, err
	}
	u, err := s.Store.AddUser(strings.TrimSpace(login), h)
	if err != nil {
		return Identity{}, err
	}
	return Identity{u.ID, u.Login, u.Role}, nil
}
func (s *Service) PromoteToAdmin(login string) error { return s.Store.SetUserRole(login, "admin") }
func (s *Service) Login(login, password string) (Identity, string, error) {
	u, ok := s.Store.User(strings.TrimSpace(login))
	if !ok || !checkPassword(u.PasswordHash, password) {
		return Identity{}, "", errors.New("invalid credentials")
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return Identity{}, "", err
	}
	raw := base64.RawURLEncoding.EncodeToString(token)
	th := sha256.Sum256([]byte(raw))
	if err := s.Store.SaveSession(db.Session{TokenHash: hex.EncodeToString(th[:]), UserID: u.ID, ExpiresAt: time.Now().Add(time.Duration(s.SessionTTLSeconds) * time.Second).UTC().Format(time.RFC3339)}); err != nil {
		return Identity{}, "", err
	}
	return Identity{u.ID, u.Login, u.Role}, raw, nil
}
func (s *Service) Authenticate(token string) (Identity, error) {
	if token == "" {
		return Identity{}, errors.New("unauthenticated")
	}
	h := sha256.Sum256([]byte(token))
	sess, ok := s.Store.Session(hex.EncodeToString(h[:]))
	if !ok {
		return Identity{}, errors.New("unauthenticated")
	}
	exp, err := time.Parse(time.RFC3339, sess.ExpiresAt)
	if err != nil || time.Now().After(exp) {
		return Identity{}, errors.New("session expired")
	}
	u, ok := s.Store.UserByID(sess.UserID)
	if !ok {
		return Identity{}, errors.New("user missing")
	}
	return Identity{u.ID, u.Login, u.Role}, nil
}
func (s *Service) Logout(token string) {
	if token == "" {
		return
	}
	h := sha256.Sum256([]byte(token))
	_ = s.Store.DeleteSession(hex.EncodeToString(h[:]))
}
