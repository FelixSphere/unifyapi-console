package model

import (
	"reflect"
	"testing"
)

// UNIFYAPI-FORK: covers the ChannelInfo.Scan driver-compatibility fix.
//
// The bug this pins: Scan did `value.([]byte)` and discarded the failed
// assertion, so a driver returning a string produced a nil slice and the row
// failed with "unexpected end of JSON input". glebarez/sqlite returns a string
// for TEXT columns, which made a SQLite deployment unable to load any channel.
// SQLite is the default when SQL_DSN is unset.

const validChannelInfo = `{"is_multi_key":true,"multi_key_size":3,"multi_key_polling_index":1}`

func assertParsed(t *testing.T, c ChannelInfo) {
	t.Helper()
	if !c.IsMultiKey {
		t.Error("IsMultiKey not parsed")
	}
	if c.MultiKeySize != 3 {
		t.Errorf("MultiKeySize = %d, want 3", c.MultiKeySize)
	}
	if c.MultiKeyPollingIndex != 1 {
		t.Errorf("MultiKeyPollingIndex = %d, want 1", c.MultiKeyPollingIndex)
	}
}

// The regression itself. Fails on the pre-fix implementation.
func TestChannelInfoScanAcceptsString(t *testing.T) {
	var c ChannelInfo
	if err := c.Scan(validChannelInfo); err != nil {
		t.Fatalf("a string from the SQLite driver must scan, got %v", err)
	}
	assertParsed(t, c)
}

func TestChannelInfoScanAcceptsBytes(t *testing.T) {
	var c ChannelInfo
	if err := c.Scan([]byte(validChannelInfo)); err != nil {
		t.Fatalf("bytes from Postgres/MySQL must scan, got %v", err)
	}
	assertParsed(t, c)
}

func TestChannelInfoScanTreatsNilAsUnset(t *testing.T) {
	// A NULL column must leave the zero value, not fail the whole row.
	var c ChannelInfo
	if err := c.Scan(nil); err != nil {
		t.Fatalf("NULL must not error, got %v", err)
	}
	if c.IsMultiKey || c.MultiKeySize != 0 {
		t.Errorf("NULL must leave the zero value, got %+v", c)
	}
}

func TestChannelInfoScanTreatsEmptyAsUnset(t *testing.T) {
	// Empty is "not set", not malformed JSON -- and rows written before this
	// column existed carry exactly that.
	for _, v := range []interface{}{"", []byte{}} {
		var c ChannelInfo
		if err := c.Scan(v); err != nil {
			t.Fatalf("empty %T must not error, got %v", v, err)
		}
		if c.IsMultiKey || c.MultiKeySize != 0 {
			t.Errorf("empty %T must leave the zero value, got %+v", v, c)
		}
	}
}

func TestChannelInfoScanReportsUnexpectedTypes(t *testing.T) {
	// The original silently swallowed this, which is how a type mismatch became
	// a confusing JSON error three layers up instead of a clear one here.
	var c ChannelInfo
	if err := c.Scan(42); err == nil {
		t.Error("an unscannable type must return an error, not be discarded")
	}
}

func TestChannelInfoScanStillRejectsMalformedJSON(t *testing.T) {
	var c ChannelInfo
	if err := c.Scan("{not json"); err == nil {
		t.Error("genuinely malformed JSON must still error")
	}
}

// Value and Scan must agree, in both the shapes a driver may hand back.
func TestChannelInfoRoundTrip(t *testing.T) {
	original := ChannelInfo{IsMultiKey: true, MultiKeySize: 5, MultiKeyPollingIndex: 2}
	v, err := original.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	raw, ok := v.([]byte)
	if !ok {
		t.Fatalf("Value returned %T, want []byte", v)
	}
	for name, in := range map[string]interface{}{"bytes": raw, "string": string(raw)} {
		var got ChannelInfo
		if err := got.Scan(in); err != nil {
			t.Fatalf("%s: Scan: %v", name, err)
		}
		if !reflect.DeepEqual(got, original) {
			t.Errorf("%s: round trip changed the value: %+v != %+v", name, got, original)
		}
	}
}
