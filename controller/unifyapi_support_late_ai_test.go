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
package controller

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

// Late replies must not overwrite a newer closure, staff reply or customer turn.
func TestLateSupportAIReplyDoesNotOverwriteNewerConversationState(t *testing.T) {
	for _, change := range []string{"closed", "staff-replied", "customer-followup"} {
		t.Run(change, func(t *testing.T) {
			h := setupSupportTest(t)
			ticket, err := model.CreateSupportTicket(h.customer.Id, "Support request", "Original customer question")
			require.NoError(t, err)
			before, err := model.GetSupportMessages(ticket.Id)
			require.NoError(t, err)
			original := before[0].Id
			switch change {
			case "closed":
				require.NoError(t, model.SetSupportTicketStatus(ticket.Id, model.SupportStatusClosed))
			case "staff-replied":
				_, err = model.AddSupportMessage(ticket.Id, h.staff.Id, true, "A person has answered")
				require.NoError(t, err)
			case "customer-followup":
				_, err = model.AddSupportMessage(ticket.Id, h.customer.Id, false, "New context while AI was running")
				require.NoError(t, err)
			}
			expected, err := model.GetSupportTicket(ticket.Id)
			require.NoError(t, err)
			messages, err := model.GetSupportMessages(ticket.Id)
			require.NoError(t, err)
			_, err = model.AddSupportAIMessage(ticket.Id, "Stale AI answer", false, original)
			assert.Error(t, err, "late reply must be refused rather than inserted")
			after, err := model.GetSupportTicket(ticket.Id)
			require.NoError(t, err)
			assert.Equal(t, expected.Status, after.Status)
			assert.Equal(t, expected.HandledMessageId, after.HandledMessageId)
			afterMessages, err := model.GetSupportMessages(ticket.Id)
			require.NoError(t, err)
			assert.Equal(t, messages, afterMessages)
		})
	}
}
