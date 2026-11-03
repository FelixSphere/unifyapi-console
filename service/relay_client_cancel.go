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
	"net/http"
)

// UNIFYAPI-FORK: stop waiting on the supplier when the customer has gone.
//
// Upstream builds relay requests with http.NewRequest, so they are not tied to
// the customer's connection. Once a supplier has sent headers that does not
// matter much -- the stream ends, or STREAMING_TIMEOUT's inactivity watchdog
// ends it. Before headers it does: nothing at all bounds that wait unless
// RELAY_STREAM_HEADER_TIMEOUT is set, and with it disabled an upstream that
// accepts the connection and never answers holds the goroutine, the upstream
// connection and a RELAY_MAX_CONCURRENT slot for as long as the upstream likes.
//
// Reproduced 2026-11-03 against the real binary with a fake supplier, the
// bound at 0 and RELAY_MAX_CONCURRENT=2: two customers gave up after 10s, both
// supplier connections stayed open, and a third customer got 503 server_busy
// 20s later. In production that is a slow walk to every one of 512 slots.
//
// DoRelayRequest cancels the supplier request if the customer's context ends
// BEFORE response headers arrive, and detaches the moment they do: a customer
// leaving mid-stream is handled exactly as it was before, so how a stream is
// read and billed after its first byte does not change.
func DoRelayRequest(client *http.Client, req *http.Request, clientCtx context.Context) (*http.Response, error) {
	if clientCtx == nil || req == nil {
		return client.Do(req)
	}
	ctx, cancel := context.WithCancel(req.Context())
	stopWatching := context.AfterFunc(clientCtx, cancel)
	resp, err := client.Do(req.WithContext(ctx))
	stopWatching()
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}
