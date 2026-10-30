package types

type RelayFormat string

const (
	RelayFormatOpenAI                    RelayFormat = "openai"
	RelayFormatClaude                                = "claude"
	RelayFormatGemini                                = "gemini"
	RelayFormatOpenAIResponses                       = "openai_responses"
	RelayFormatOpenAIResponsesCompaction             = "openai_responses_compaction"
	RelayFormatOpenAIAlphaSearch                     = "openai_alpha_search"
	RelayFormatOpenAIAudio                           = "openai_audio"
	RelayFormatOpenAIImage                           = "openai_image"
	RelayFormatOpenAIRealtime                        = "openai_realtime"
	RelayFormatRerank                                = "rerank"
	// RelayFormatSystemOne is TypeSafe's decision-model API: a state plus a
	// map of typed questions in, typed answers with calibrated probabilities
	// out. It is not a chat shape and is forwarded verbatim.
	RelayFormatSystemOne = "system_one"
	RelayFormatEmbedding = "embedding"

	RelayFormatTask    = "task"
	RelayFormatMjProxy = "mj_proxy"
)
