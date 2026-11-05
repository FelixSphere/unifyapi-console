/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api (Copyright (C) 2023-2026
QuantumNous), distributed under the GNU Affero General Public License v3.
See BRANDING.md for the relationship between this fork and its upstream.

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.
*/
package service

// UNIFYAPI-FORK: the AI first responder on /support (operator request
// 2026-11-05). The agent itself runs in UnifySelfAI (POST /api/support-agent);
// this side is the channel: it asks the agent, streams the answer to the
// customer, stores it, and hands the conversation to staff when the agent
// says so. Off unless SUPPORT_AGENT_URL and SUPPORT_AGENT_SECRET are set.
//
// Trust is a shared HS256 secret used in two directions with two audiences:
//   console -> agent  aud "unifyselfai:support-agent"   (we mint)
//   agent -> console  aud "unifyapi-console:support-tools" (we verify)
// so a token minted for one direction cannot be replayed in the other. The
// customer a token names is the ONLY customer whose data the agent can read.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	supportAgentAudAgent = "unifyselfai:support-agent"
	supportAgentAudTools = "unifyapi-console:support-tools"
	supportAgentIssOurs  = "unifyapi-console"
	supportAgentIssAgent = "unifyselfai"
	supportAgentMaxLife  = 120 * time.Second
	// A customer message nobody has handled for this long is emailed to staff.
	SupportAgentSweepAfter = 2 * time.Minute
)

func supportAgentURL() string    { return strings.TrimRight(os.Getenv("SUPPORT_AGENT_URL"), "/") }
func supportAgentSecret() string { return os.Getenv("SUPPORT_AGENT_SECRET") }

// SupportAgentEnabled reports whether the AI first responder is configured.
func SupportAgentEnabled() bool {
	return supportAgentURL() != "" && len(supportAgentSecret()) >= 32
}

func mintSupportAgentToken(customerId, ticketId int) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iss": supportAgentIssOurs,
		"aud": supportAgentAudAgent,
		"sub": strconv.Itoa(customerId),
		"cid": strconv.Itoa(ticketId),
		"jti": uuid.NewString(),
		"iat": now.Unix(),
		"exp": now.Add(60 * time.Second).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(supportAgentSecret()))
}

var (
	toolsJtiMu sync.Mutex
	toolsJti   = map[string]int64{}
)

// VerifySupportToolsToken checks a token the agent minted to read one
// customer's data, and returns that customer's id.
func VerifySupportToolsToken(token string) (int, error) {
	if !SupportAgentEnabled() {
		return 0, errors.New("support agent not configured")
	}
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		return []byte(supportAgentSecret()), nil
	},
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithIssuer(supportAgentIssAgent),
		jwt.WithAudience(supportAgentAudTools),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
	)
	if err != nil || !parsed.Valid {
		return 0, errors.New("invalid token")
	}
	claims, _ := parsed.Claims.(jwt.MapClaims)
	sub, _ := claims["sub"].(string)
	jti, _ := claims["jti"].(string)
	iat, _ := claims["iat"].(float64)
	exp, _ := claims["exp"].(float64)
	customerId, err := strconv.Atoi(sub)
	if err != nil || customerId <= 0 || len(jti) < 16 {
		return 0, errors.New("invalid token")
	}
	if time.Duration(exp-iat)*time.Second > supportAgentMaxLife {
		return 0, errors.New("token lifetime too long")
	}
	toolsJtiMu.Lock()
	defer toolsJtiMu.Unlock()
	now := time.Now().Unix()
	for k, v := range toolsJti {
		if v < now {
			delete(toolsJti, k)
		}
	}
	if _, seen := toolsJti[jti]; seen {
		return 0, errors.New("token already used")
	}
	toolsJti[jti] = int64(exp)
	return customerId, nil
}

// SupportAgentTurn is one message of the conversation, as the agent sees it.
type SupportAgentTurn struct {
	Role    string `json:"role"` // customer | agent | staff
	Content string `json:"content"`
}

// SupportAgentTurns converts stored messages to the agent's roles, newest 40.
func SupportAgentTurns(msgs []model.SupportMessage) []SupportAgentTurn {
	turns := make([]SupportAgentTurn, 0, len(msgs))
	for _, m := range msgs {
		role := "customer"
		if m.FromAI {
			role = "agent"
		} else if m.FromStaff {
			role = "staff"
		}
		turns = append(turns, SupportAgentTurn{Role: role, Content: m.Body})
	}
	if len(turns) > 40 {
		turns = turns[len(turns)-40:]
	}
	return turns
}

// SupportAgentResult is what one agent run produced.
type SupportAgentResult struct {
	Answer  string
	Handoff string
}

// AskSupportAgent sends the conversation to the agent and relays each
// server-sent event to onEvent as it arrives. It returns the final answer.
// ctx should NOT be the customer's request context: the answer is stored
// even if the customer closes the page mid-stream.
func AskSupportAgent(ctx context.Context, customerId, ticketId int, turns []SupportAgentTurn, onEvent func(event string, data json.RawMessage)) (*SupportAgentResult, error) {
	token, err := mintSupportAgentToken(customerId, ticketId)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"messages": turns})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, supportAgentURL()+"/api/support-agent", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	resp, err := (&http.Client{Timeout: 3 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("support agent HTTP %d", resp.StatusCode)
	}
	result := &SupportAgentResult{}
	var event string
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := json.RawMessage(strings.TrimPrefix(line, "data: "))
			var payload struct {
				Text    string `json:"text"`
				Reason  string `json:"reason"`
				Message string `json:"message"`
			}
			_ = json.Unmarshal(data, &payload)
			switch event {
			case "answer":
				result.Answer = payload.Text
			case "handoff":
				result.Handoff = payload.Reason
			case "error":
				if onEvent != nil {
					onEvent(event, data)
				}
				return nil, errors.New("support agent: " + payload.Message)
			}
			if onEvent != nil {
				onEvent(event, data)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.Answer) == "" {
		return nil, errors.New("support agent returned no answer")
	}
	return result, nil
}

// StartSupportAgentSweep emails staff about customer messages that nobody has
// handled for SupportAgentSweepAfter -- the agent never ran (the customer
// closed the page before the console asked it) or it failed. Without the agent
// configured, staff are emailed at once, as before, and this does nothing.
func StartSupportAgentSweep() {
	if !SupportAgentEnabled() || !common.IsMasterNode {
		return
	}
	go func() {
		for {
			time.Sleep(30 * time.Second)
			sweepUnhandledSupportMessages()
		}
	}()
}

func sweepUnhandledSupportMessages() {
	rows, err := model.UnhandledSupportMessages(time.Now().Add(-SupportAgentSweepAfter).Unix(), 50)
	if err != nil {
		common.SysError("support sweep: " + err.Error())
		return
	}
	for _, r := range rows {
		NotifyStaffOfUnhandledSupportMessage(r.TicketId, r.MessageId)
	}
}

// NotifyStaffOfUnhandledSupportMessage tells staff about a customer message
// the agent did not resolve, and marks it handled so it is told only once.
func NotifyStaffOfUnhandledSupportMessage(ticketId, messageId int) {
	if err := model.MarkSupportMessageHandled(ticketId, messageId); err != nil {
		common.SysError("support: mark handled: " + err.Error())
		return
	}
	ticket, err := model.GetSupportTicket(ticketId)
	if err != nil {
		return
	}
	msgs, err := model.GetSupportMessages(ticketId)
	if err != nil || len(msgs) == 0 {
		return
	}
	body := msgs[len(msgs)-1].Body
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Id == messageId {
			body = msgs[i].Body
			break
		}
	}
	customer, err := model.GetUserById(ticket.UserId, false)
	if err != nil {
		return
	}
	NotifyStaffOfSupportMessage(ticket, customer, body, false)
}
