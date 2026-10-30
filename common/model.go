package common

import "strings"

var (
	// OpenAIResponseOnlyModels is a list of models that are only available for OpenAI responses.
	OpenAIResponseOnlyModels = []string{
		"o3-pro",
		"o3-deep-research",
		"o4-mini-deep-research",
	}
	ImageGenerationModels = []string{
		"dall-e-3",
		"dall-e-2",
		"gpt-image-1",
		"prefix:imagen-",
		"flux-",
		"flux.1-",
	}
	OpenAITextModels = []string{
		"gpt-",
		"o1",
		"o3",
		"o4",
		"chatgpt",
	}
	// UNIFYAPI-FORK: models that generate video and have no chat endpoint at
	// all. Matched case-insensitively as substrings, the way
	// ImageGenerationModels already is.
	//
	// Kept as names rather than read off the catalog because common cannot
	// import setting/ratio_setting -- that package imports common -- and
	// because the catalog does not carry the distinction anyway: MiniMax-H3 is
	// priced per second of video, while the Seedance rows are priced per token,
	// so there is no single field to key on.
	VideoGenerationModels = []string{
		"minimax-h3",
		"hailuo",
		"seedance",
		"wan3.0-video",
		"happyhorse-1.1-",
	}
)

func IsOpenAIResponseOnlyModel(modelName string) bool {
	for _, m := range OpenAIResponseOnlyModels {
		if strings.Contains(modelName, m) {
			return true
		}
	}
	return false
}

func IsImageGenerationModel(modelName string) bool {
	modelName = strings.ToLower(modelName)
	for _, m := range ImageGenerationModels {
		if strings.Contains(modelName, m) {
			return true
		}
		if strings.HasPrefix(m, "prefix:") && strings.HasPrefix(modelName, strings.TrimPrefix(m, "prefix:")) {
			return true
		}
	}
	return false
}

// IsVideoGenerationModel reports whether a model only generates video.
//
// Checked against the live catalog on 2026-09-25: these patterns match exactly
// MiniMax-H3 and seedance-2.5 out of the 54 models on a channel, and nothing
// else.
func IsVideoGenerationModel(modelName string) bool {
	modelName = strings.ToLower(modelName)
	for _, m := range VideoGenerationModels {
		if strings.Contains(modelName, m) {
			return true
		}
	}
	return false
}

func IsOpenAITextModel(modelName string) bool {
	modelName = strings.ToLower(modelName)
	for _, m := range OpenAITextModels {
		if strings.Contains(modelName, m) {
			return true
		}
	}
	return false
}
