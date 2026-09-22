/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/

// UNIFYAPI-FORK: reading back what an on-chain payout rail stores.

// splitOnChain mirrors model.SplitOnChainPayoutDetails: an on-chain rail keeps
// its account as "<NETWORK>:<address>" in one column, and the dialog shows it
// as the two fields the seller actually typed. Anything without a colon is a
// free-text account (a wire), which has no network to read off it.
export function splitOnChain(details: string): {
  network: string
  address: string
} {
  const at = details.indexOf(':')
  if (at < 0) return { network: '', address: details.trim() }
  return {
    network: details.slice(0, at).trim().toUpperCase(),
    address: details.slice(at + 1).trim(),
  }
}
