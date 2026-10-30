/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Gemini content URL carries the channel's Google API key as ?key=, and
// the video proxy logs that URL on every failure path. These tests pin the
// one property that matters: the secret does not appear in what is logged.

func TestRedactURLSecretsHidesTheGeminiKey(t *testing.T) {
	const secret = "AIzaSy-THIS-IS-THE-CHANNEL-KEY"
	in := "https://generativelanguage.googleapis.com/v1beta/files/abc:download?alt=media&key=" + secret

	out := redactURLSecrets(in)

	assert.NotContains(t, out, secret, "the channel key must never reach the log")
	assert.Contains(t, out, "key=REDACTED")
	assert.Contains(t, out, "alt=media", "unrelated parameters stay, so the log is still diagnosable")
	assert.Contains(t, out, "generativelanguage.googleapis.com/v1beta/files/abc:download",
		"host and path are what an operator needs to see")
}

func TestRedactURLSecretsIsCaseInsensitiveAndCoversCommonSpellings(t *testing.T) {
	for _, name := range []string{"key", "Key", "KEY", "api_key", "apikey", "ApiKey"} {
		out := redactURLSecrets("https://example.com/v?" + name + "=s3cr3t&x=1")
		assert.NotContains(t, out, "s3cr3t", "parameter %q leaked", name)
		assert.Contains(t, out, "x=1")
	}
}

// TestRedactURLSecretsLeavesCleanURLsByteIdentical is the no-regression
// property. A URL without a secret must come back exactly as it went in --
// no re-encoding, no reordering -- so operators comparing a logged URL to a
// real one never see a phantom difference.
func TestRedactURLSecretsLeavesCleanURLsByteIdentical(t *testing.T) {
	for _, in := range []string{
		"https://api.minimax.io/v1/files/retrieve?file_id=123",
		"https://example.com/v1/videos/task_9/content",
		"https://example.com/path?z=1&a=2",
		"data:video/mp4;base64,AAAA",
		"",
	} {
		assert.Equal(t, in, redactURLSecrets(in))
	}
}

func TestRedactURLSecretsToleratesGarbage(t *testing.T) {
	// A URL the proxy is about to fail to parse must still be printable so
	// the parse-failure log line is useful. Returned unchanged, never panics.
	garbage := "://not a url at all %zz"
	require.NotPanics(t, func() { redactURLSecrets(garbage) })
	assert.Equal(t, garbage, redactURLSecrets(garbage))
}
