package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractBuildID(t *testing.T) {
	tests := []struct {
		name string
		page string
		want string
	}{
		{
			name: "rsbuild meta tag",
			page: `<head><meta charset="utf-8"><meta name="unifyapi-build" content="3f9c2a1d5e7b"><title>x</title></head>`,
			want: "3f9c2a1d5e7b",
		},
		{
			name: "attribute order reversed",
			page: `<meta content="local-20260905" name="unifyapi-build">`,
			want: "local-20260905",
		},
		{
			name: "other meta tags do not match",
			page: `<meta name="build-id" content="rv.0000.2k6e8r7p"><meta name="description" content="unifyapi-build">`,
			want: "",
		},
		{
			name: "empty content is no id",
			page: `<meta name="unifyapi-build" content="">`,
			want: "",
		},
		{
			name: "dev page without the tag",
			page: `<html><head></head><body></body></html>`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ExtractBuildID([]byte(tt.page)))
		})
	}
}
