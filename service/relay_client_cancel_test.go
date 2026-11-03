/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api (Copyright (C) 2023-2026
QuantumNous), distributed under the GNU Affero General Public License v3.
See BRANDING.md for the relationship between this fork and its upstream.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// supplier stands in for an upstream. headersAfter < 0 means never answer.
// gone is closed when the supplier sees its request cancelled.
func supplier(t *testing.T, headersAfter time.Duration, body string) (url string, gone chan struct{}) {
	t.Helper()
	gone = make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var wait <-chan time.Time
		if headersAfter >= 0 {
			wait = time.After(headersAfter)
		}
		select {
		case <-wait:
		case <-r.Context().Done():
			close(gone)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		time.Sleep(150 * time.Millisecond) // body arrives after headers
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, gone
}

func relayReq(t *testing.T, url string) *http.Request {
	req, err := http.NewRequest("POST", url, nil) // as upstream builds it: no context
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	return req
}

// The defect: a customer who gives up before the first token must not leave
// the relay waiting on a supplier that never answers.
func TestCustomerLeavingBeforeFirstTokenStopsTheSupplierRequest(t *testing.T) {
	url, gone := supplier(t, -1, "")
	clientCtx, customerLeaves := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, customerLeaves)

	start := time.Now()
	_, err := DoRelayRequest(&http.Client{}, relayReq(t, url), clientCtx)
	if err == nil {
		t.Fatal("expected the request to end when the customer left")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("relay kept waiting %v after the customer left", took)
	}
	select {
	case <-gone:
	case <-time.After(2 * time.Second):
		t.Fatal("the supplier never saw the request cancelled: the connection is still held")
	}
}

// What must NOT change: once the first byte is in, a customer leaving does not
// cut the supplier stream, so how it is read and billed stays as it was.
func TestCustomerLeavingAfterHeadersDoesNotCutTheStream(t *testing.T) {
	url, _ := supplier(t, 0, "data: hello\n\n")
	clientCtx, customerLeaves := context.WithCancel(context.Background())

	resp, err := DoRelayRequest(&http.Client{}, relayReq(t, url), clientCtx)
	if err != nil {
		t.Fatal(err)
	}
	customerLeaves() // headers are in; the body has not arrived yet
	got, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(got) != "data: hello\n\n" {
		t.Fatalf("body after headers must be unaffected, got %q err %v", got, err)
	}
}

// A slow but healthy first token completes while the customer is still there:
// this helper adds no time limit of its own.
func TestASlowFirstTokenCompletesWhileTheCustomerWaits(t *testing.T) {
	url, _ := supplier(t, 1500*time.Millisecond, "data: late\n\n")
	resp, err := DoRelayRequest(&http.Client{}, relayReq(t, url), context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(got) != "data: late\n\n" {
		t.Fatalf("got %q", got)
	}
}

// Non-streaming relays pass no customer context and get plain client.Do.
func TestWithoutACustomerContextItIsPlainClientDo(t *testing.T) {
	url, _ := supplier(t, 0, "{}")
	resp, err := DoRelayRequest(&http.Client{}, relayReq(t, url), nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(got) != "{}" {
		t.Fatalf("got %q", got)
	}
}
