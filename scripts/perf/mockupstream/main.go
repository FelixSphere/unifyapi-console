// Command mockupstream is an OpenAI-compatible endpoint that returns canned
// responses, for load testing the gateway without touching a real provider.
//
// UNIFYAPI-FORK: fork-owned test infrastructure. Not part of the console build.
//
// Why this exists: loading the gateway against a live channel also loads that
// provider -- for us, a reseller that is already rate-limiting us. It also
// conflates their latency with ours, which is the opposite of what a capacity
// test is for. This stands in for the provider so the only thing under load is
// our own relay path.
//
// It deliberately does NOT model a provider faithfully. It models the cheapest
// possible upstream, so that whatever ceiling the test finds belongs to the
// gateway rather than to something behind it.
//
//	go run ./scripts/perf/mockupstream -addr :899 -latency 50ms -tokens 16
//
// Flags:
//
//	-latency   delay before responding, simulating provider think time
//	-jitter    uniform +/- randomisation on that delay
//	-tokens    completion tokens to emit
//	-chunk-ms  inter-chunk delay when streaming
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

var (
	addr     = flag.String("addr", ":899", "listen address")
	latency  = flag.Duration("latency", 50*time.Millisecond, "delay before responding")
	jitter   = flag.Duration("jitter", 0, "uniform +/- jitter on the delay")
	tokens   = flag.Int("tokens", 16, "completion tokens to emit")
	chunkMS  = flag.Int("chunk-ms", 0, "inter-chunk delay when streaming")
	verbose  = flag.Bool("v", false, "log every request")
	requests atomic.Int64
	streams  atomic.Int64
)

const word = "ready "

func think() {
	d := *latency
	if *jitter > 0 {
		d += time.Duration(rand.Int63n(int64(*jitter)*2) - int64(*jitter))
	}
	if d > 0 {
		time.Sleep(d)
	}
}

type chatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	MaxTok   int    `json:"max_tokens"`
	Messages []struct {
		Content any `json:"content"`
	} `json:"messages"`
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	requests.Add(1)
	var req chatRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	n := *tokens
	if req.MaxTok > 0 && req.MaxTok < n {
		n = req.MaxTok
	}
	if req.Model == "" {
		req.Model = "mock-model"
	}
	if *verbose {
		log.Printf("%s model=%s stream=%v tokens=%d", r.Method, req.Model, req.Stream, n)
	}

	think()

	if req.Stream {
		streams.Add(1)
		streamResponse(w, req.Model, n)
		return
	}

	body := strings.Repeat(word, n)
	writeJSON(w, map[string]any{
		"id": "chatcmpl-mock", "object": "chat.completion",
		"created": time.Now().Unix(), "model": req.Model,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": body},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens": 10, "completion_tokens": n, "total_tokens": 10 + n,
		},
	})
}

func streamResponse(w http.ResponseWriter, model string, n int) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	for i := 0; i < n; i++ {
		chunk := map[string]any{
			"id": "chatcmpl-mock", "object": "chat.completion.chunk",
			"created": time.Now().Unix(), "model": model,
			"choices": []any{map[string]any{
				"index": 0, "delta": map[string]any{"content": word},
			}},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
		if *chunkMS > 0 {
			time.Sleep(time.Duration(*chunkMS) * time.Millisecond)
		}
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func main() {
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", handleChat)
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"object": "list", "data": []any{
			map[string]any{"id": "mock-model", "object": "model", "owned_by": "mock"},
		}})
	})
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{
			"requests": requests.Load(), "streams": streams.Load(),
		})
	})

	srv := &http.Server{
		Addr:    *addr,
		Handler: mux,
		// Generous: this stands in for a provider, and must never be the thing
		// that fails first during a ramp.
		ReadTimeout:  2 * time.Minute,
		WriteTimeout: 5 * time.Minute,
		IdleTimeout:  2 * time.Minute,
	}
	log.Printf("mock upstream on %s (latency=%v jitter=%v tokens=%d chunk-ms=%d)",
		*addr, *latency, *jitter, *tokens, *chunkMS)
	log.Fatal(srv.ListenAndServe())
}
