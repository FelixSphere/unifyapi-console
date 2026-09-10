# Video provider upgrade (2026-09-10)

All requests use the existing authenticated `POST /v1/videos` task API; poll
`GET /v1/videos/{id}` and retrieve `GET /v1/videos/{id}/content` as before.
The changes add provider protocols and model discovery. They do not create
production channels, obtain model access, or deploy the service.

## Models and channels

| Channel | New models | Native parameters in `metadata` |
| --- | --- | --- |
| Ali / DashScope | `happyhorse-1.1-t2v`, `happyhorse-1.1-i2v`, `happyhorse-1.1-r2v` | `input.media`, `parameters` (resolution, ratio, duration, watermark, seed) |
| Ali / DashScope | `wan3.0-video`, `wan3.0-video-prime` | `input.media`, `parameters` (duration can be -1) |
| MiniMax / Hailuo | `MiniMax-H3`, `MiniMax-H3-Max` | `content`, `duration`, `resolution`, `ratio` |
| Doubao / BytePlus LAS | `dreamina-seedance-2-5-260628` (official), `doubao-seedance-2-5-260628` (compatibility alias) | `content`, `omni_reference_task_type`, `output_format`, existing native fields |
| Kling | `kling-3.0`, `kling-3.0-turbo`, `kling-3.0-omni` | `contents`, `settings`, `options` |
| Vidu | `viduq3-pro`, `viduq3-pro-fast`, `viduq3-turbo`, `viduq3`, `viduq3-mix` | existing image fields plus `audio`, `off_peak`, `aspect_ratio`, `style` |
| Gemini | `veo-3.1-lite-generate-preview` | existing Veo parameters |
| Gemini | `gemini-omni-1.1-flash` | `input`, `previous_interaction_id`, `generation_config`, `response_format` |
| Jimeng | `jimeng_v30_pro`, `jimeng_v30_720p`, `jimeng_v30_1080p` | existing native parameters; query now uses the submitted request key |

MiniMax H3 uses `/v2/video_generation` and `/v2/query/video_generation/{id}`.
Kling 3 uses the new `/text-to-video/`, `/image-to-video/`, `/omni-video/`
endpoints and `/tasks?task_ids=...`. Use the official Singapore Kling base URL
`https://api-singapore.klingai.com`, or an upstream implementing that protocol.
Gemini Omni uses stored background `/v1beta/interactions`. Legacy Hailuo,
Kling, Gemini Veo and Jimeng task IDs retain their previous polling paths.

For Ali, use the endpoint matching the key's region and workspace. Wan 3's
current documented Singapore base is
`https://{WorkspaceId}.ap-southeast-1.maas.aliyuncs.com`.
Model availability requires upstream account access, independently of relay support.

The admin channel test detects registered video models (including mapped
aliases) and submits a real asynchronous generation on the selected channel.
The page polls the saved task and marks success only after generation completes.
The request uses a short text prompt by default; expand **Video test options**
to supply JSON with `prompt`, `images`, `duration`, `size`, or provider `metadata`
for models requiring reference media. These tests charge the operator's account
through the same reservation, settlement and failure-refund path as `/v1/videos`.
No API key is created, no fallback channel is used, and channel status is unchanged.
Automatic health checks still skip video generation.

If browser polling is interrupted or exceeds 20 minutes, the result stays
**Pending** with its task ID. Clicking Test again in the same page resumes that
task instead of submitting another generation. The task itself persists on the
server and is visible in task history even after the page is closed.

Vidu's reference action maps `viduq3-pro` to the upstream `viduq3` name.
Use explicit `metadata.action` for reference generation where the number of
images alone cannot identify the intended action. Vidu Q3 subjects use `metadata.subjects`, `auto_subjects`, and `audio_type`.
Q3 Mix supports image references; the official API does not support subjects
or video-reference input for that model.

## Prices and settlement

The compiled catalog now has explicit `price_unit: second` entries. The
pricing UI displays `/ second`, and task billing snapshots retain that unit.
Existing fixed per-request tasks continue to skip usage-based settlement.

| Catalog model | Base USD / second | Resolution multipliers |
| --- | --- | --- |
| HappyHorse 1.1 (all three modes) | 0.14 at 720P | 480P: 0.5; 1080P: 0.18 / 0.14 |
| MiniMax-H3 | 0.08 at 768P | 2K: 1.625 |
| MiniMax-H3-Max | 0.08 at 768P | 480P: 0.625 |
| Wan3 standard | 0.10 at 720P | 480P: 0.5; 1080P: 2 |
| Wan3 prime | 0.14 at 720P | 480P: 0.068 / 0.14; 1080P: 2 |

Ali prices are **Singapore international list prices**, not Beijing prices or
promotional discounts. Model and customer group discounts remain separate.
ModelDiscount updates and removal now also update per-unit video prices.

HappyHorse reserves its specified duration. H3 reserves output duration plus
up to 15 input-video seconds when a reference video is present; images after
the first five reserve $0.04 each for H3. H3 Max image input is free. Completion
settles measured input/output seconds and chargeable input images at the
request-time customer price. Wan3 auto-duration/video-input requests reserve
30 seconds, then settle input + output video duration. Invalid or absent usage
retains the reservation; inspect those tasks before making customer adjustments.
Failures use the existing full-refund path.

Other newly discovered models require an explicitly configured customer price
before enabling a channel. No price has been guessed from a model name.
In particular, Gemini Omni's modality-specific token settlement is **not yet
implemented**: the adapter requires an explicit fixed task price. Its official
token prices are not represented as a fake per-second catalog price.
Gemini Developer Veo Fast resolution multipliers differ from Vertex AI; the
adapters now keep those schedules separate.

Seedance 2.5 is the exception to the paragraph above: it has a compiled,
official BytePlus list-price baseline. Configure a Doubao Video channel with
`https://operator.las.ap-southeast-1.bytepluses.com` and a LAS API key. The
adapter selects `/api/v1/contents/generations/tasks` for LAS (and retains
`/api/v3` for Ark), accepts the official `dreamina-seedance-2-5-260628` model
ID, automatically translates the legacy `doubao-` alias on LAS, and settles
the completed task from upstream `usage.total_tokens`. The
baseline is USD 10.70/M tokens without video input; requests containing video
apply USD 6.40/M tokens as a `6.4/10.7` billing multiplier. These are vendor
cost baselines; customer model and group discounts remain separate.

## Examples

HappyHorse image-to-video:

```json
{
  "model": "happyhorse-1.1-i2v",
  "prompt": "A slow camera pan across the scene",
  "images": ["https://your-cdn.example/frame.png"],
  "seconds": "5",
  "size": "720P",
  "metadata": {"parameters": {"watermark": false, "seed": 0}}
}
```

MiniMax H3 text-to-video:

```json
{
  "model": "MiniMax-H3",
  "prompt": "A train passing through a mountain valley",
  "duration": 8,
  "size": "768P"
}
```

Kling Omni reference generation:

```json
{
  "model": "kling-3.0-omni",
  "prompt": "Animate the reference scene",
  "metadata": {
    "contents": [
      {"type": "prompt", "text": "Animate the reference scene"},
      {"type": "refer_image", "url": "https://your-cdn.example/scene.png", "id": "image_1"}
    ],
    "settings": {"duration": 5, "resolution": "720p", "audio": "off"}
  }
}
```

## Verification and rollout

Request/response contract tests use synthetic media URLs and local HTTP mocks;
they do not spend provider credits. They cover false/zero parameters, model
mapping protection, duration bounds, terminal failures, legacy polling, video
price discounts, reference-input settlement, and wallet/token refunds.
Pricing-page browser verification uses a local API fixture, not an operator
account. Live generation with each configured provider remains a rollout gate.

No database migration is needed: the task billing unit is in the existing JSON
billing context. Check persisted ModelPrice options for stale overrides, set
channels/keys/base URLs/model permissions, and configure the prices not covered
by the compiled catalog before making those models available. A release belongs
to the CI/CD agent after review and merge.

Sora 2 / Pro remains compatible with the existing adapter. OpenAI currently
lists Sora API removal for **2026-09-24**. Kling has announced retirement of
legacy 1.x/2.0/2.1 models on **2026-09-15**; keep old task polling but migrate
new customer requests to supported models.

## Official references

- [HappyHorse text](https://help.aliyun.com/en/model-studio/happyhorse-text-to-video-api-reference), [image](https://help.aliyun.com/en/model-studio/happyhorse-image-to-video-api-reference), [reference](https://help.aliyun.com/en/model-studio/happyhorse-reference-to-video-api-reference)
- [Wan3 API](https://help.aliyun.com/en/model-studio/wan3-video-generation-api-reference), [Ali international prices](https://www.alibabacloud.com/help/en/model-studio/model-pricing)
- [MiniMax H3 create](https://platform.minimax.io/docs/api-reference/video-generation-v2-create), [query](https://platform.minimax.io/docs/api-reference/video-generation-v2-query), [prices](https://platform.minimax.io/docs/guides/pricing-paygo)
- [Seedance task API](https://docs.volcengine.com/docs/82379/1520757?lang=zh)
- [Kling 3.0 / Omni](https://kling.ai/document-api/api/video/3-0-omni), [Turbo](https://kling.ai/document-api/api/video/3-0-turbo)
- [Vidu text](https://platform.vidu.com/docs/text-to-video), [image](https://platform.vidu.com/docs/image-to-video), [reference](https://platform.vidu.com/docs/reference-to-video)
- [Gemini Omni](https://ai.google.dev/gemini-api/docs/omni), [Interactions API](https://ai.google.dev/api/interactions-api), [Gemini pricing](https://ai.google.dev/gemini-api/docs/pricing)
- [Jimeng 3.0](https://docs.volcengine.com/docs/85621/1792707?lang=zh)
- [OpenAI deprecations](https://developers.openai.com/api/docs/deprecations)

### Flatkey MiniMax H3 channels

Use channel type **MiniMax**, Base URL `https://router.flatkey.ai` (no `/v1` suffix), and the Flatkey key. The existing `https://console.flatkey.ai` origin is also recognized without redirecting credentials to a different host. For H3/H3-Max only, these exact origins select Flatkey's `/v1/videos` submit, task query, and authenticated content endpoints. Official MiniMax and other custom origins retain their native V1/V2 protocols.

H3 requests are converted to `content`, `resolution`, `duration`, and `ratio`; the default test sends 5 seconds at `768P`. Put custom H3 fields in the video test's `metadata`, for example `{"metadata":{"resolution":"2K","duration":6}}` for H3, or `{"metadata":{"aigc_watermark":false}}`. Keep the channel parameter/header override fields empty for this integration: video task bodies do not apply channel parameter overrides. Selecting OpenAI instead only forwards generic video fields and does not perform H3 parameter conversion.

Flatkey task IDs carry an internal protocol prefix, so polling and authenticated downloads continue using the hosted task lifecycle. Failed tasks follow the existing refund path; this integration does not change baseline prices. Reference: https://flatkey.ai/models/minimax-h3 (checked 2026-09-10).

### OpenRouter video channels

Use channel type **OpenRouter** and the default Base URL (`https://openrouter.ai/api`, without `/v1`). OpenRouter video IDs are namespaced, such as `minimax/hailuo-3`, `minimax/hailuo-3-max`, or `google/veo-3.1`; use a model mapping when exposing a different public model name. Discover current IDs and capabilities with `GET https://openrouter.ai/api/v1/videos/models`.

The adapter handles `/api/v1/videos` submission (HTTP 202), pending/completed/failed job polling, string failure reasons, and authenticated content downloads. Generic gateway `size` and `seconds` become OpenRouter resolution/pixel size and duration. Native JSON fields and gateway `metadata` can carry aspect ratio, frame images, references, audio, seed, callback URL, and provider options. Metadata takes precedence for these supported fields; model mapping remains authoritative. Channel Parameter Override is not applied to video tasks.

Manual test defaults use a capability snapshot from the video models endpoint, including 2K for OpenRouter H3 (its currently supported resolution). Some editing/upscaling models require reference assets and need explicit test metadata. Configure customer video pricing separately: this change does not seed or replace prices. Duration multiplies the configured base rate; OpenRouter `usage.cost` is never substituted for customer quota. A task accepted by the provider is persisted once, and failed jobs use the existing refund path.

Sources checked 2026-09-10: https://openrouter.ai/docs/api/api-reference/video-generation/create-videos and https://openrouter.ai/docs/api/api-reference/video-generation/get-videos.
