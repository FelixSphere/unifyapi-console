/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package service

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

// UNIFYAPI-FORK: the one place a chosen channel is checked against the limits
// that can refuse a request -- the user's own allowance on the channel
// (model/unifyapi_channel_user_rate_limit.go) and the channel's hard limit
// (model/unifyapi_channel_hard_rate_limit.go). Every path that picks a
// channel for a new attempt calls it: selection, affinity, a pinned key and
// retry.

type ChannelAdmission int

const (
	ChannelAdmitted ChannelAdmission = iota
	ChannelHardLimited
	ChannelUserLimited
)

const (
	userLimitedChannelsKey = "unifyapi_user_limited_channels"
	userLimitRefusalKey    = "unifyapi_user_limit_refusal"
)

// AdmitSelectedChannel counts this attempt against the channel and says
// whether it may be sent.
//
// The user's allowance is checked first and given back if the channel then
// turns out to be full. The other order would let one user who is over their
// own limit use up a hard-limited channel's slots, which are never given
// back, with requests that are not served.
func AdmitSelectedChannel(c *gin.Context, channel *model.Channel) ChannelAdmission {
	userId := c.GetInt("id")
	if refusal := model.AdmitChannelUserRequest(c, userId, channel); refusal != nil {
		limited := UserLimitedChannels(c)
		if limited == nil {
			limited = map[int]struct{}{}
			c.Set(userLimitedChannelsKey, limited)
		}
		limited[channel.Id] = struct{}{}
		// The caller can be served again as soon as the first of the
		// channels that refused reopens.
		if earlier := ChannelUserLimitRefusal(c); earlier == nil || refusal.RetryAfter < earlier.RetryAfter {
			c.Set(userLimitRefusalKey, refusal)
		}
		return ChannelUserLimited
	}
	if !model.AdmitChannelRequest(c, channel) {
		model.ReleaseChannelUserRequest(c, userId, channel)
		return ChannelHardLimited
	}
	return ChannelAdmitted
}

// UserLimitedChannels are the channels this request has already been refused
// on because of its user's allowance. Selection leaves them out.
func UserLimitedChannels(c *gin.Context) map[int]struct{} {
	if c == nil {
		return nil
	}
	limited, _ := c.Get(userLimitedChannelsKey)
	out, _ := limited.(map[int]struct{})
	return out
}

// ChannelUserLimitRefusal is why, for the answer to the caller.
func ChannelUserLimitRefusal(c *gin.Context) *model.ChannelUserLimitRefusal {
	if c == nil {
		return nil
	}
	refusal, _ := c.Get(userLimitRefusalKey)
	out, _ := refusal.(*model.ChannelUserLimitRefusal)
	return out
}
