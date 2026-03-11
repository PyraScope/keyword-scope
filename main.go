package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v3"
)

type Config struct {
	IntervalSeconds int      `yaml:"interval_seconds"`
	Targets         []Target `yaml:"targets"`
}

type Target struct {
	Name            string `yaml:"name"`
	URL             string `yaml:"url"`
	Keyword         string `yaml:"keyword"`
	CaseInsensitive bool   `yaml:"case_insensitive"`
	TimeoutSeconds  int    `yaml:"timeout_seconds"`
	MaxBytes        int64  `yaml:"max_bytes"`
}

const (
	defaultIntervalSeconds = 30
	defaultTimeoutSeconds  = 10
	defaultMaxBytes        = int64(5 * 1024 * 1024) // 5MB
)

var (
	keywordFound = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "keyword_found",
			Help: "Whether the keyword was found on the target URL (1 = found, 0 = not found)",
		},
		[]string{"name", "url", "keyword"},
	)

	keywordCheckErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "keyword_check_errors_total",
			Help: "Number of errors while checking a target URL for a keyword",
		},
		[]string{"name", "url"},
	)

	keywordLastCheck = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "keyword_last_check_timestamp_seconds",
			Help: "Unix timestamp of the last keyword check per target",
		},
		[]string{"name", "url"},
	)
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to YAML config")
	listenAddr := flag.String("listen", ":8182", "HTTP listen address")
	flag.Parse()

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if len(cfg.Targets) == 0 {
		log.Fatalf("config has no targets")
	}

	interval := time.Duration(cfg.IntervalSeconds) * time.Second
	if cfg.IntervalSeconds <= 0 {
		interval = defaultIntervalSeconds * time.Second
	}

	prometheus.MustRegister(keywordFound, keywordCheckErrors, keywordLastCheck)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	for i := range cfg.Targets {
		t := cfg.Targets[i]
		applyDefaults(&t)
		wg.Add(1)
		go func(target Target) {
			defer wg.Done()
			runChecker(ctx, target, interval)
		}(t)
	}

	http.Handle("/metrics", promhttp.Handler())

	server := &http.Server{
		Addr:              *listenAddr,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", *listenAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	cancel()
	wg.Wait()
}

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyDefaults(t *Target) {
	if t.TimeoutSeconds <= 0 {
		t.TimeoutSeconds = defaultTimeoutSeconds
	}
	if t.MaxBytes <= 0 {
		t.MaxBytes = defaultMaxBytes
	}
}

func runChecker(ctx context.Context, target Target, interval time.Duration) {
	client := &http.Client{
		Timeout: time.Duration(target.TimeoutSeconds) * time.Second,
	}

	// Run an initial check immediately.
	checkOnce(ctx, client, target)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkOnce(ctx, client, target)
		}
	}
}

func checkOnce(ctx context.Context, client *http.Client, target Target) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		keywordCheckErrors.WithLabelValues(target.Name, target.URL).Inc()
		return
	}

	resp, err := client.Do(req)
	if err != nil {
		keywordCheckErrors.WithLabelValues(target.Name, target.URL).Inc()
		return
	}
	defer resp.Body.Close()

	limitReader := io.LimitReader(resp.Body, target.MaxBytes)
	body, err := io.ReadAll(limitReader)
	if err != nil {
		keywordCheckErrors.WithLabelValues(target.Name, target.URL).Inc()
		return
	}

	keyword := target.Keyword
	content := string(body)
	if target.CaseInsensitive {
		keyword = strings.ToLower(keyword)
		content = strings.ToLower(content)
	}

	found := strings.Contains(content, keyword)
	if found {
		keywordFound.WithLabelValues(target.Name, target.URL, target.Keyword).Set(1)
	} else {
		keywordFound.WithLabelValues(target.Name, target.URL, target.Keyword).Set(0)
	}
	log.Printf("check result name=%s url=%s keyword=%q found=%t", target.Name, target.URL, target.Keyword, found)
	keywordLastCheck.WithLabelValues(target.Name, target.URL).Set(float64(time.Now().Unix()))
}

func init() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if env := os.Getenv("KEYWORD_EXPORTER_VERSION"); env != "" {
		log.Printf("keyword exporter version %s", env)
	}
	if env := os.Getenv("KEYWORD_EXPORTER_INFO"); env != "" {
		log.Print(env)
	}
	if env := os.Getenv("KEYWORD_EXPORTER_WELCOME"); env != "" {
		fmt.Println(env)
	}
}
