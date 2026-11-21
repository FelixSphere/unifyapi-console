package model

// ChannelModelNames lists every model that is on a channel, whether or not
// the channel or the ability is enabled. The channel editor offers them as
// suggestions, so an id typed once does not have to be typed again.
func ChannelModelNames() []string {
	var models []string
	DB.Table("abilities").Distinct("model").Pluck("model", &models)
	return models
}
