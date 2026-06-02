package audit

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	requestPath = "/internal/new-api/audit/request"
	usagePath   = "/internal/new-api/audit/usage"
)

type RequestEvent struct {
	RequestId     string `json:"request_id"`
	CreatedAt     string `json:"created_at"`
	UserId        int    `json:"user_id"`
	Username      string `json:"username"`
	TokenId       int    `json:"token_id"`
	TokenName     string `json:"token_name"`
	ModelName     string `json:"model_name"`
	RequestPath   string `json:"request_path"`
	RelayFormat   string `json:"relay_format"`
	IsStream      bool   `json:"is_stream"`
	PromptHash    string `json:"prompt_hash"`
	PromptPreview string `json:"prompt_preview"`
	PromptText    string `json:"prompt_text"`
}

type UsageEvent struct {
	RequestId         string `json:"request_id"`
	CreatedAt         string `json:"created_at"`
	UserId            int    `json:"user_id"`
	Username          string `json:"username"`
	TokenId           int    `json:"token_id"`
	TokenName         string `json:"token_name"`
	ModelName         string `json:"model_name"`
	PromptTokens      int    `json:"prompt_tokens"`
	CompletionTokens  int    `json:"completion_tokens"`
	Quota             int    `json:"quota"`
	ChannelId         int    `json:"channel_id"`
	Group             string `json:"group"`
	UseTimeSeconds    int    `json:"use_time_seconds"`
	IsStream          bool   `json:"is_stream"`
	UpstreamRequestId string `json:"upstream_request_id"`
}

type config struct {
	enabled  bool
	endpoint string
	secret   string
	timeout  time.Duration
	excluded map[string]struct{}
}

type outboundEvent struct {
	path string
	body []byte
}

var (
	initOnce sync.Once
	cfg      config
	queue    chan outboundEvent
)

func EnabledForToken(tokenName string) bool {
	c := getConfig()
	if !c.enabled {
		return false
	}
	_, excluded := c.excluded[tokenName]
	return !excluded
}

func HashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func PreviewText(text string, limit int) string {
	compact := strings.Join(strings.Fields(strings.ReplaceAll(text, "\r", "\n")), " ")
	if len(compact) <= limit {
		return compact
	}
	return compact[:limit] + "..."
}

func EnqueueRequest(event RequestEvent) {
	enqueue(requestPath, event.TokenName, event)
}

func EnqueueUsage(event UsageEvent) {
	enqueue(usagePath, event.TokenName, event)
}

func enqueue(path string, tokenName string, payload interface{}) {
	c := getConfig()
	if !c.enabled {
		return
	}
	if _, excluded := c.excluded[tokenName]; excluded {
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("audit: marshal event failed: %v", err)
		return
	}
	select {
	case queue <- outboundEvent{path: path, body: body}:
	default:
		log.Printf("audit: queue full, dropping event path=%s", path)
	}
}

func getConfig() config {
	initOnce.Do(initConfig)
	return cfg
}

func initConfig() {
	queueSize := getenvInt("AUDIT_QUEUE_SIZE", 10000)
	cfg = config{
		enabled:  getenvBool("AUDIT_ENABLED", false),
		endpoint: strings.TrimRight(os.Getenv("AUDIT_ENDPOINT"), "/"),
		secret:   os.Getenv("AUDIT_SECRET"),
		timeout:  time.Duration(getenvInt("AUDIT_TIMEOUT_MS", 800)) * time.Millisecond,
		excluded: parseExcluded(os.Getenv("AUDIT_EXCLUDED_TOKEN_NAMES")),
	}
	if queueSize <= 0 {
		queueSize = 10000
	}
	queue = make(chan outboundEvent, queueSize)
	if !cfg.enabled {
		return
	}
	if cfg.endpoint == "" || cfg.secret == "" {
		cfg.enabled = false
		log.Printf("audit: disabled because AUDIT_ENDPOINT or AUDIT_SECRET is empty")
		return
	}
	go worker(cfg)
}

func worker(c config) {
	client := &http.Client{Timeout: c.timeout}
	for event := range queue {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		signature := sign(c.secret, ts, event.body)
		req, err := http.NewRequest(http.MethodPost, c.endpoint+event.path, bytes.NewReader(event.body))
		if err != nil {
			log.Printf("audit: build request failed: %v", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Audit-Timestamp", ts)
		req.Header.Set("X-Audit-Signature", signature)
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("audit: send failed: %v", err)
			continue
		}
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		if resp.StatusCode >= 400 {
			log.Printf("audit: endpoint returned status=%d path=%s", resp.StatusCode, event.path)
		}
	}
}

func sign(secret string, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func getenvBool(name string, defaultValue bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func getenvInt(name string, defaultValue int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return defaultValue
	}
	return value
}

func parseExcluded(raw string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result[item] = struct{}{}
		}
	}
	return result
}
