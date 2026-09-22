/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package typesafe

// ModelList prefills a new TypeSafe channel. The vendor's own versioned id is
// `jev-1.13.0`; `jev-1.13` is the name sold here and the one the catalog
// prices, so a channel talking straight to TypeSafe needs a model mapping to
// the dotted form. `jev-latest` and `jev-preview` are the vendor's aliases and
// are deliberately NOT sold: an alias silently changes which model -- and
// which price -- a customer is buying.
var ModelList = []string{
	"jev-1.13",
}
