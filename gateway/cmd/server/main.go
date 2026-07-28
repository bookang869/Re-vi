package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/bookang869/Re-vi/gateway/internal/alerts"
	"github.com/bookang869/Re-vi/gateway/internal/config"
	"github.com/bookang869/Re-vi/gateway/internal/dispatcher"
	"github.com/bookang869/Re-vi/gateway/internal/github"
	"github.com/bookang869/Re-vi/gateway/internal/queue"
	"github.com/bookang869/Re-vi/gateway/internal/victorialogs"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

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

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	logsClient := victorialogs.NewClient(cfg.VictoriaLogsURL)
	mux.HandleFunc("/v1/alerts", alerts.NewHandler(cfg.WebhookSecret, q, logsClient, cfg.Mode))
	mux.HandleFunc("/v1/digest/entry", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
	})

	log.Printf("gateway listening on %s (mode=%s)", cfg.ListenAddr, cfg.Mode)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatal(err)
	}
}
