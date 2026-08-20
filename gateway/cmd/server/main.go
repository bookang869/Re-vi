package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/alerts"
	"github.com/bookang869/Re-vi/gateway/internal/config"
	"github.com/bookang869/Re-vi/gateway/internal/digest"
	"github.com/bookang869/Re-vi/gateway/internal/digestcron"
	"github.com/bookang869/Re-vi/gateway/internal/dispatcher"
	"github.com/bookang869/Re-vi/gateway/internal/github"
	"github.com/bookang869/Re-vi/gateway/internal/metrics"
	"github.com/bookang869/Re-vi/gateway/internal/queue"
	"github.com/bookang869/Re-vi/gateway/internal/runs"
	"github.com/bookang869/Re-vi/gateway/internal/slack"
	"github.com/bookang869/Re-vi/gateway/internal/store"
	"github.com/bookang869/Re-vi/gateway/internal/victorialogs"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	runStore, err := store.Open(cfg.SQLitePath)
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer runStore.Close()
	log.Printf("store: opened %s", cfg.SQLitePath)

	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	q, err := queue.Connect(connectCtx, cfg.NATSURL)
	cancel()
	if err != nil {
		log.Fatalf("nats: %v", err)
	}
	defer q.Close()
	log.Printf("nats: connected, stream %s and KV buckets ready", queue.StreamName)

	gh := github.NewClient(cfg.GitHubTokenDispatch, cfg.GitHubOwner, cfg.GitHubRepo)
	consumeCtx, err := dispatcher.Run(context.Background(), q, gh, cfg.Mode)
	if err != nil {
		log.Fatalf("dispatcher: %v", err)
	}
	defer consumeCtx.Stop()
	log.Printf("dispatcher: consuming %s, dispatching to %s/%s", queue.StreamName, cfg.GitHubOwner, cfg.GitHubRepo)

	sc := slack.NewClient(cfg.SlackWebhookURL)
	digestcron.Run(context.Background(), q, sc, cfg.DigestTime)
	log.Printf("digestcron: scheduled for %s local daily", cfg.DigestTime)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/metrics", metrics.Handler)
	logsClient := victorialogs.NewClient(cfg.VictoriaLogsURL)
	mux.HandleFunc("/v1/alerts", alerts.NewHandler(cfg.WebhookSecret, q, logsClient, cfg.Mode, runStore))
	mux.HandleFunc("/v1/digest/entry", digest.NewHandler(cfg.WebhookSecret, q, runStore))
	mux.HandleFunc("GET /v1/runs/{alert_id}", runs.NewHandler(cfg.WebhookSecret, runStore))

	log.Printf("gateway listening on %s (mode=%s)", cfg.ListenAddr, cfg.Mode)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatal(err)
	}
}
