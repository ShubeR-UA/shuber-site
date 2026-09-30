package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"shuber-site/internal"
	"shuber-site/internal/auth"
	"shuber-site/internal/db"
	httpapi "shuber-site/internal/http"
	"shuber-site/internal/storage"
)

func main() {
	cfg := internal.LoadConfig()

	var (
		store *db.Store
		err   error
	)

	var blobs *storage.R2Client
	if cfg.StorageBackend == "r2" {
		blobs = storage.NewR2Client(cfg.R2InternalURL)
		store, err = db.NewRemote(blobs, context.Background(), cfg.DataKey)
	} else {
		if err := os.MkdirAll(cfg.MediaDir, 0755); err != nil {
			log.Fatal(err)
		}
		store, err = db.New(cfg.DataFile)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	svc := &auth.Service{Store: store, SessionTTLSeconds: int(cfg.SessionTTL.Seconds())}
	seedTracks(store)

	if cfg.AdminLogin != "" {
		if _, ok := store.User(cfg.AdminLogin); !ok && cfg.AdminPassword != "" {
			if _, err := svc.Register(cfg.AdminLogin, cfg.AdminPassword); err != nil {
				log.Fatal(err)
			}
		}
		if _, ok := store.User(cfg.AdminLogin); ok {
			if err := svc.PromoteToAdmin(cfg.AdminLogin); err != nil {
				log.Fatal(err)
			}
		}
	}

	handler := httpapi.New(svc, httpapi.Options{
		MediaDir:       cfg.MediaDir,
		CookieSecure:   cfg.CookieSecure,
		MaxUploadBytes: cfg.MaxUploadBytes,
		Production:     cfg.AppEnv == "production",
		BlobStore:      blobs,
	}).Routes()

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       120 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	log.Printf("ShubeR server listening on %s (env=%s storage=%s)", cfg.Addr, cfg.AppEnv, cfg.StorageBackend)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func seedTracks(store *db.Store) {
	if len(store.Tracks()) != 0 {
		return
	}
	defaults := []db.Track{
		{ID: "01", Title: "Наш старый Discord", Subtitle: "там всё ещё горит один канал", BPM: 92, Mood: "nostalgia"},
		{ID: "02", Title: "ShubeR.exe", Subtitle: "own world / own rules", BPM: 148, Mood: "digital"},
		{ID: "03", Title: "Alt+F4", Subtitle: "закрыть лишнее и начать сначала", BPM: 150, Mood: "restart"},
		{ID: "04", Title: "22:04", Subtitle: "время, которое почему-то помнишь", BPM: 78, Mood: "late night"},
		{ID: "05", Title: "Без крыльев", Subtitle: "теперь между нами небо стало землёй", BPM: 84, Mood: "bittersweet"},
		{ID: "06", Title: "На красный", Subtitle: "я иду на красный", BPM: 154, Mood: "rush"},
	}
	for _, t := range defaults {
		if err := store.UpsertTrack(t); err != nil {
			log.Fatal(err)
		}
	}
}
