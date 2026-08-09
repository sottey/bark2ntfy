package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	Listen    string
	NtfyURL   string
	NtfyTopic string
	NtfyToken string
}

type BarkPush struct {
	Title      string   `json:"title"`
	Subtitle   string   `json:"subtitle"`
	Body       string   `json:"body"`
	DeviceKey  string   `json:"device_key"`
	DeviceKeys []string `json:"device_keys"`
	Level      string   `json:"level"`
	Group      string   `json:"group"`
	URL        string   `json:"url"`
	Sound      string   `json:"sound"`
	Icon       string   `json:"icon"`
}

type BarkResponse struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

func main() {
	cfg := Config{
		Listen:    getenv("LISTEN", ":8080"),
		NtfyURL:   strings.TrimRight(getenv("NTFY_URL", "https://ntfy.cott.us"), "/"),
		NtfyTopic: getenv("NTFY_TOPIC", "1panel"),
		NtfyToken: os.Getenv("NTFY_TOKEN"),
	}

	if cfg.NtfyToken == "" {
		log.Fatal("NTFY_TOKEN is required")
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("pong"))
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, BarkResponse{
			Code:      200,
			Message:   "success",
			Timestamp: time.Now().Unix(),
		})
	})

	mux.HandleFunc("/push", func(w http.ResponseWriter, r *http.Request) {
		handlePush(cfg, w, r)
	})

	// Bark v1-compatible URL forms:
	//
	// /DEVICE_KEY/body
	// /DEVICE_KEY/title/body
	//
	// Also accepts query parameters such as:
	// ?title=...
	// ?body=...
	// ?group=...
	// ?url=...
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handleLegacy(cfg, w, r)
	})

	log.Printf("bark2ntfy listening on %s", cfg.Listen)
	log.Printf("ntfy target: %s/%s", cfg.NtfyURL, cfg.NtfyTopic)

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           requestLogger(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Fatal(server.ListenAndServe())
}

func handlePush(cfg Config, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeBarkError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var push BarkPush

	decoder := json.NewDecoder(io.LimitReader(r.Body, 1024*1024))
	if err := decoder.Decode(&push); err != nil {
		writeBarkError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if strings.TrimSpace(push.Body) == "" {
		writeBarkError(w, http.StatusBadRequest, "body is required")
		return
	}

	if err := publishNtfy(cfg, push); err != nil {
		log.Printf("ntfy publish failed: %v", err)
		writeBarkError(w, http.StatusBadGateway, "notification delivery failed")
		return
	}

	writeBarkSuccess(w)
}

func handleLegacy(cfg Config, w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		w.Header().Set("Content-Type", "application/json")
		writeJSON(w, http.StatusOK, map[string]any{
			"name":    "bark2ntfy",
			"status":  "ok",
			"message": "Bark-compatible ntfy bridge",
		})
		return
	}

	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeBarkError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	parts := splitPath(r.URL.Path)

	if len(parts) < 2 {
		writeBarkError(w, http.StatusBadRequest, "invalid Bark request")
		return
	}

	push := BarkPush{
		DeviceKey: parts[0],
		Group:     r.URL.Query().Get("group"),
		URL:       r.URL.Query().Get("url"),
		Sound:     r.URL.Query().Get("sound"),
		Icon:      r.URL.Query().Get("icon"),
		Level:     r.URL.Query().Get("level"),
	}

	switch len(parts) {
	case 2:
		push.Body = parts[1]

	default:
		push.Title = parts[1]
		push.Body = strings.Join(parts[2:], "/")
	}

	// Explicit query parameters override path values.
	if value := r.URL.Query().Get("title"); value != "" {
		push.Title = value
	}

	if value := r.URL.Query().Get("body"); value != "" {
		push.Body = value
	}

	if strings.TrimSpace(push.Body) == "" {
		writeBarkError(w, http.StatusBadRequest, "body is required")
		return
	}

	if err := publishNtfy(cfg, push); err != nil {
		log.Printf("ntfy publish failed: %v", err)
		writeBarkError(w, http.StatusBadGateway, "notification delivery failed")
		return
	}

	writeBarkSuccess(w)
}

func publishNtfy(cfg Config, push BarkPush) error {
	target := cfg.NtfyURL + "/" + url.PathEscape(cfg.NtfyTopic)

	req, err := http.NewRequest(
		http.MethodPost,
		target,
		bytes.NewBufferString(push.Body),
	)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+cfg.NtfyToken)

	if push.Title != "" {
		req.Header.Set("Title", sanitizeHeader(push.Title))
	}

	if push.Group != "" {
		req.Header.Set("Tags", sanitizeHeader(push.Group))
	}

	if push.URL != "" {
		req.Header.Set("Click", sanitizeHeader(push.URL))
	}

	switch strings.ToLower(push.Level) {
	case "critical":
		req.Header.Set("Priority", "max")
	case "timesensitive":
		req.Header.Set("Priority", "high")
	case "passive":
		req.Header.Set("Priority", "low")
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf(
			"ntfy returned HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		)
	}

	return nil
}

func splitPath(path string) []string {
	rawParts := strings.Split(strings.Trim(path, "/"), "/")

	var result []string

	for _, part := range rawParts {
		if part == "" {
			continue
		}

		decoded, err := url.PathUnescape(part)
		if err != nil {
			decoded = part
		}

		result = append(result, decoded)
	}

	return result
}

func sanitizeHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func writeBarkSuccess(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, BarkResponse{
		Code:      200,
		Message:   "success",
		Timestamp: time.Now().Unix(),
	})
}

func writeBarkError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, BarkResponse{
		Code:      status,
		Message:   message,
		Timestamp: time.Now().Unix(),
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("response encode error: %v", err)
	}
}

func getenv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}

	return fallback
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s from %s", r.Method, r.URL.RequestURI(), r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}
