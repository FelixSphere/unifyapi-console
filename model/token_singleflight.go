/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

import (
	"golang.org/x/sync/singleflight"
)

// Every relay request authorises by looking its token up in Redis, and
// GetTokenByKey falls through to Postgres whenever that lookup does not
// answer -- a miss, a timeout, a restart, Redis being down. Upstream's
// comment on that branch is literally "Don't return error - fall through to
// DB", which is the right call for availability and the wrong one for load:
// with no coalescing, N concurrent requests carrying the SAME token become N
// identical Postgres queries.
//
// That is a cache-stampede amplifier pointed at the weakest part of this
// deployment. Postgres runs in a container on the same 2-vCPU box as the
// console, and the pool is capped at max_connections minus headroom (77 at
// the time of writing, see db_pool_ceiling.go). A Redis blip during a traffic
// spike therefore converts into a thundering herd on a database that cannot
// widen: requests queue on the pool, latency climbs, and the relay starts
// timing out -- all from a cache layer whose whole purpose was to keep that
// load away.
//
// singleflight collapses the herd: concurrent lookups of one key share a
// single query, and the rest wait for its result instead of issuing their
// own. Keys are independent, so unrelated tokens are unaffected.
//
// Scope is deliberately narrow. Only the CACHE-FALLBACK path goes through
// here. An explicit fromDB=true read -- what a caller uses after writing, to
// see its own write -- is left alone: sharing an in-flight query that began
// before that write could hand the caller the pre-write row, and a rare
// stale read is a worse trade than a rare duplicate query.
var tokenLookupGroup singleflight.Group

// loadTokenFromDBOnce reads a token from the database, collapsing concurrent
// reads of the same key into one query.
//
// Each caller gets its own copy. singleflight hands every waiter the same
// value, and callers of GetTokenByKey treat the result as theirs -- sharing
// one *Token across goroutines would turn a load fix into a data race.
func loadTokenFromDBOnce(key string) (*Token, error) {
	v, err, _ := tokenLookupGroup.Do(key, func() (any, error) {
		var token *Token
		if err := DB.Where(commonKeyCol+" = ?", key).First(&token).Error; err != nil {
			return nil, err
		}
		return token, nil
	})
	if err != nil {
		return nil, err
	}
	shared, ok := v.(*Token)
	if !ok || shared == nil {
		return nil, nil
	}
	clone := *shared
	return &clone, nil
}
