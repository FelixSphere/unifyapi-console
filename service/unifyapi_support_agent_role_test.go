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

import (
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// UNIFYAPI-FORK: the agent weighs staff and the super administrator's word
// more (operator 2026-11-19) and learns who is speaking only from this signed
// claim, so the claim must carry the owner's role exactly, and stay absent
// where there is none to give.
func TestSupportAgentTokenCarriesTheOwnersRole(t *testing.T) {
	t.Setenv("SUPPORT_AGENT_URL", "https://agent.example")
	t.Setenv("SUPPORT_AGENT_SECRET", strings.Repeat("k", 40))

	claimsOf := func(role int) jwt.MapClaims {
		t.Helper()
		token, err := mintSupportAgentToken(42, 7, role)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := jwt.Parse(token, func(*jwt.Token) (any, error) {
			return []byte(strings.Repeat("k", 40)), nil
		}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience(supportAgentAudAgent))
		if err != nil {
			t.Fatal(err)
		}
		return parsed.Claims.(jwt.MapClaims)
	}

	for _, role := range []int{1, 10, 100} {
		c := claimsOf(role)
		if got, ok := c["role"].(float64); !ok || int(got) != role {
			t.Fatalf("role %d: claim is %v", role, c["role"])
		}
		if c["sub"] != "42" || c["cid"] != "7" {
			t.Fatalf("role %d: sub/cid changed: %v %v", role, c["sub"], c["cid"])
		}
	}
	if _, present := claimsOf(0)["role"]; present {
		t.Fatal("role 0 must leave the claim out")
	}
}
