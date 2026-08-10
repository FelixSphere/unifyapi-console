package setting

import (
	"encoding/json"

	"github.com/QuantumNous/new-api/common"
)

var Chats = []map[string]string{
	//{
	//	"ChatGPT Next Web 官方示例": "https://app.nextchat.dev/#/?settings={\"key\":\"{key}\",\"url\":\"{address}\"}",
	//},
	{
		"Cherry Studio": "cherrystudio://providers/api-keys?v=1&data={cherryConfig}",
	},
	{
		"AionUI": "aionui://provider/add?v=1&data={aionuiConfig}",
	},
	{
		"流畅阅读": "fluentread",
	},
	// "CC Switch": "ccswitch" is deliberately absent. The value is a bare token
	// with no scheme, so the browser resolved it against the current origin and
	// served the SPA's 404 at /ccswitch. It also carried no key or endpoint, so
	// it could not have imported anything even if it opened. The working
	// integration is the dialog on the API keys page, which builds a full
	// ccswitch://v1/import URL.
	{
		"DeepChat": "deepchat://provider/install?v=1&data={deepchatConfig}",
	},
	{
		"Lobe Chat 官方示例": "https://chat-preview.lobehub.com/?settings={\"keyVaults\":{\"openai\":{\"apiKey\":\"{key}\",\"baseURL\":\"{address}/v1\"}}}",
	},
	{
		"AI as Workspace": "https://aiaw.app/set-provider?provider={\"type\":\"openai\",\"settings\":{\"apiKey\":\"{key}\",\"baseURL\":\"{address}/v1\",\"compatibility\":\"strict\"}}",
	},
	{
		"AMA 问天": "ama://set-api-key?server={address}&key={key}",
	},
	{
		"OpenCat": "opencat://team/join?domain={address}&token={key}",
	},
}

func UpdateChatsByJsonString(jsonString string) error {
	Chats = make([]map[string]string, 0)
	return json.Unmarshal([]byte(jsonString), &Chats)
}

func Chats2JsonString() string {
	jsonBytes, err := json.Marshal(Chats)
	if err != nil {
		common.SysLog("error marshalling chats: " + err.Error())
		return "[]"
	}
	return string(jsonBytes)
}
