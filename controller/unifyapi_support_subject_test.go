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
package controller

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// A subject becomes an email Subject header and an inbox row, so line breaks
// and tabs collapse to single spaces before it is stored. Found in the
// v0.1.147 release review: a typed newline reached the notification subject.
func TestASubjectIsOneLine(t *testing.T) {
	h := setupSupportTest(t)
	id := h.open(t, "Invoice\r\nBcc: someone@example.com\n\tplease", "body")
	_, out := h.do(t, h.customer, http.MethodGet, "/api/support/tickets/"+strconv.Itoa(id), nil)
	subject := out["data"].(map[string]any)["ticket"].(map[string]any)["subject"].(string)
	require.Equal(t, "Invoice Bcc: someone@example.com please", subject)
}
