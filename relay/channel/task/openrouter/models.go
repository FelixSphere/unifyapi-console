package openrouter

// Snapshot of https://openrouter.ai/api/v1/videos/models, 2026-09-10.
// Capabilities only; upstream SKU prices are not customer billing prices.
var defaults = map[string]struct {
	Duration   int
	Resolution string
}{
	"minimax/hailuo-3-max":                 {5, "768p"},
	"alibaba/wan-3.0-prime":                {5, "720p"},
	"alibaba/wan-3.0":                      {5, "720p"},
	"heygen/avatar-iv":                     {0, "720p"},
	"black-forest-labs/flux-video-upscale": {0, ""},
	"bytedance/seedance-2.0-mini":          {5, "720p"},
	"bytedance/seedance-2.5":               {5, "720p"},
	"black-forest-labs/flux-3-video":       {5, "720p"},
	"minimax/hailuo-3":                     {5, "2K"},
	"runway/aleph-2":                       {0, ""},
	"runway/gen-4.5":                       {5, "720p"},
	"x-ai/grok-imagine-video-1.5":          {5, "720p"},
	"alibaba/happyhorse-1.1":               {5, "720p"},
	"alibaba/happyhorse-1.0":               {5, "720p"},
	"x-ai/grok-imagine-video":              {5, "720p"},
	"kwaivgi/kling-v3.0-pro":               {5, "720p"},
	"kwaivgi/kling-v3.0-std":               {5, "720p"},
	"google/veo-3.1-fast":                  {4, "720p"},
	"google/veo-3.1-lite":                  {8, "720p"},
	"kwaivgi/kling-video-o1":               {5, "720p"},
	"minimax/hailuo-2.3":                   {6, "1080p"},
	"bytedance/seedance-2.0-fast":          {5, "720p"},
	"alibaba/wan-2.7":                      {5, "720p"},
	"bytedance/seedance-2.0":               {5, "720p"},
	"alibaba/wan-2.6":                      {5, "720p"},
	"bytedance/seedance-1-5-pro":           {5, "720p"},
	"openai/sora-2-pro":                    {4, "720p"},
	"google/veo-3.1":                       {4, "720p"},
}

func TestDefaults(name string) (int, string, bool) {
	d, ok := defaults[name]
	return d.Duration, d.Resolution, ok
}
