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
package model

// SupportFeedbackMessage deliberately excludes account identity, email and
// internal metadata. Only an authorized operator may read this signal feed.
type SupportFeedbackMessage struct {
	Id        int    `json:"id"`
	TicketId  int    `json:"ticket_id"`
	Body      string `json:"body"`
	CreatedAt int64  `json:"created_at"`
}

// ListSupportFeedback reads immutable customer messages in cursor order.
// Unlike opening the operator inbox, it never advances anyone's read marker.
// IDs are allocation order, not commit order: a polling consumer must restart
// complete sweeps and deduplicate IDs to reconcile transactions committed late.
func ListSupportFeedback(after, limit int) ([]SupportFeedbackMessage, error) {
	var messages []SupportFeedbackMessage
	err := DB.Model(&SupportMessage{}).Select("id", "ticket_id", "body", "created_at").
		Where("id > ? AND from_staff = ? AND from_ai = ?", after, false, false).
		Order("id").Limit(limit).Find(&messages).Error
	if messages == nil {
		messages = []SupportFeedbackMessage{}
	}
	return messages, err
}
