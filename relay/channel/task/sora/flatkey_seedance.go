package sora

import (
	"net/url"
	"strconv"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// FlatKey uses the OpenAI video lifecycle but Seedance's native content array.
// https://flatkey.ai/models/seedance-2.5
func isFlatkeySeedance(base, model string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return false
	}
	if !strings.EqualFold(u.Host, "router.flatkey.ai") && !strings.EqualFold(u.Host, "console.flatkey.ai") {
		return false
	}
	return strings.HasPrefix(model, "seedance-") || strings.HasPrefix(model, "doubao-seedance-") || strings.HasPrefix(model, "dreamina-seedance-")
}

func normalizeFlatkeySeedance(body map[string]interface{}, req relaycommon.TaskSubmitReq) {
	// Native fields can be supplied directly by API clients or through metadata
	// in the dashboard test. Never flatten arbitrary metadata into routing fields.
	for _, key := range []string{"content", "resolution", "ratio", "generate_audio", "camera_fixed", "return_last_frame", "seed", "frames", "watermark", "draft", "omni_reference_task_type", "output_format"} {
		if _, exists := body[key]; !exists {
			if value, ok := req.Metadata[key]; ok {
				body[key] = value
			}
		}
	}
	content, ok := body["content"].([]interface{})
	if !ok && body["content"] != nil {
		return
	} // Keep malformed native input for upstream validation.
	hasText := false
	for _, item := range content {
		if entry, ok := item.(map[string]interface{}); ok && entry["type"] == "text" {
			hasText = true
		}
	}
	if !hasText && strings.TrimSpace(req.Prompt) != "" {
		content = append(content, map[string]interface{}{"type": "text", "text": req.Prompt})
	}
	for _, image := range req.Images {
		content = append(content, map[string]interface{}{"type": "image_url", "image_url": map[string]interface{}{"url": image}})
	}
	body["content"] = content
	if _, exists := body["resolution"]; !exists {
		switch strings.ToLower(strings.TrimSpace(req.Size)) {
		case "480p", "720p", "1080p":
			body["resolution"] = strings.ToLower(strings.TrimSpace(req.Size))
		}
	}
	// Match the same seconds-before-duration precedence used by EstimateBilling.
	if req.Seconds != "" {
		if seconds, err := strconv.Atoi(req.Seconds); err == nil && seconds > 0 {
			body["duration"] = seconds
		}
	} else if req.Duration > 0 {
		body["duration"] = req.Duration
	}
	for _, key := range []string{"prompt", "images", "image", "input_reference", "metadata", "seconds"} {
		delete(body, key)
	}
	if _, exists := body["resolution"]; exists {
		delete(body, "size")
	}
}
