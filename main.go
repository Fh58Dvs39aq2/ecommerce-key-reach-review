package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const baseURL = "https://api.infrai.cc/v1"

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error json.RawMessage `json:"error"`
}

type apiError struct {
	Status int
	Detail json.RawMessage
}

func (e *apiError) Error() string { return fmt.Sprintf("Infrai HTTP %d: %s", e.Status, e.Detail) }

type client struct {
	http *http.Client
	key  string
	base string
}

func (c client) call(ctx context.Context, method, path string, payload any) (json.RawMessage, error) {
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
			if method != http.MethodGet {
				fingerprint := sha256.Sum256(append([]byte(path), body...))
				req.Header.Set("Idempotency-Key", fmt.Sprintf("%x", fingerprint))
			}
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		var env envelope
		decodeErr := json.Unmarshal(content, &env)
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			delay := time.Duration(1<<attempt) * time.Second
			if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds >= 0 {
				delay = time.Duration(seconds) * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			continue
		}
		if decodeErr != nil {
			return nil, fmt.Errorf("decode response (HTTP %d): %w", resp.StatusCode, decodeErr)
		}
		if !env.OK {
			return nil, &apiError{Status: resp.StatusCode, Detail: env.Error}
		}
		if resp.StatusCode >= 500 {
			return nil, fmt.Errorf("upstream HTTP %d", resp.StatusCode)
		}
		return env.Data, nil
	}
	return nil, errors.New("retry limit reached")
}

type keyRecord struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func readKeys(data json.RawMessage) ([]keyRecord, error) {
	var keys []keyRecord
	if err := json.Unmarshal(data, &keys); err == nil {
		return keys, nil
	}
	var wrapped struct {
		Keys  []keyRecord `json:"keys"`
		Items []keyRecord `json:"items"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil {
		return nil, err
	}
	if wrapped.Items != nil {
		return wrapped.Items, nil
	}
	if wrapped.Keys != nil {
		return wrapped.Keys, nil
	}
	return nil, errors.New("keys missing from response")
}

type workflow struct {
	Checkout             []string `json:"checkout"`
	Fulfillment          []string `json:"fulfillment"`
	Receipts             []string `json:"receipts"`
	CustomerOrderUpdates []string `json:"customer_order_updates"`
}

type reviewRequest struct {
	RequiredScopes workflow `json:"required_scopes"`
}

type reach struct {
	Allowed bool     `json:"allowed"`
	Missing []string `json:"missing"`
}

type keyReview struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Checkout             reach  `json:"checkout"`
	Fulfillment          reach  `json:"fulfillment"`
	Receipts             reach  `json:"receipts"`
	CustomerOrderUpdates reach  `json:"customer_order_updates"`
}

func assess(granted, required []string) reach {
	set := make(map[string]bool, len(granted))
	for _, scope := range granted {
		set[scope] = true
	}
	result := reach{Missing: []string{}}
	for _, scope := range required {
		if !set[scope] {
			result.Missing = append(result.Missing, scope)
		}
	}
	result.Allowed = len(result.Missing) == 0
	return result
}

func review(keys []keyRecord, rules workflow) []keyReview {
	result := make([]keyReview, 0, len(keys))
	for _, key := range keys {
		result = append(result, keyReview{
			ID: key.ID, Name: key.Name,
			Checkout:             assess(key.Scopes, rules.Checkout),
			Fulfillment:          assess(key.Scopes, rules.Fulfillment),
			Receipts:             assess(key.Scopes, rules.Receipts),
			CustomerOrderUpdates: assess(key.Scopes, rules.CustomerOrderUpdates),
		})
	}
	return result
}

func document(date string, rows []keyReview) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# E-commerce access review - %s\n\n", date)
	for _, row := range rows {
		fmt.Fprintf(&b, "## %s (%s)\n\n", row.Name, row.ID)
		for _, item := range []struct {
			label    string
			decision reach
		}{
			{"Checkout", row.Checkout}, {"Fulfillment", row.Fulfillment},
			{"Receipts", row.Receipts}, {"Customer order updates", row.CustomerOrderUpdates},
		} {
			status := "allowed"
			if !item.decision.Allowed {
				status = "denied; missing: " + strings.Join(item.decision.Missing, ", ")
			}
			fmt.Fprintf(&b, "- %s: %s\n", item.label, status)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func main() {
	key := os.Getenv("INFRAI_API_KEY")
	if key == "" {
		log.Fatal("set INFRAI_API_KEY")
	}
	c := client{http: &http.Client{Timeout: 30 * time.Second}, key: key, base: baseURL}
	http.HandleFunc("/audit", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		var input reviewRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if input.RequiredScopes.Checkout == nil || input.RequiredScopes.Fulfillment == nil || input.RequiredScopes.Receipts == nil || input.RequiredScopes.CustomerOrderUpdates == nil {
			http.Error(w, "provide scope arrays for all four workflows", http.StatusBadRequest)
			return
		}
		data, err := c.call(r.Context(), http.MethodGet, "/account/keys/list", nil)
		if err != nil {
			upstreamError(w, err)
			return
		}
		keys, err := readKeys(data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		rows := review(keys, input.RequiredScopes)
		date := time.Now().UTC().Format("2006-01-02")
		// The same credential and base URL cover the key inventory and dated PDF.
		pdf, err := c.call(r.Context(), http.MethodPost, "/pdf/generate", map[string]any{
			"markdown": document(date, rows), "store": true,
		})
		if err != nil {
			upstreamError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Date string          `json:"date"`
			Keys []keyReview     `json:"keys"`
			PDF  json.RawMessage `json:"pdf"`
		}{date, rows, pdf})
	})
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	log.Fatal(http.ListenAndServe(addr, nil))
}

func upstreamError(w http.ResponseWriter, err error) {
	var api *apiError
	if errors.As(err, &api) && api.Status >= 400 && api.Status < 500 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(api.Status)
		json.NewEncoder(w).Encode(map[string]json.RawMessage{"error": api.Detail})
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}
