/*
Copyright (C) 2026 FelixSphere

This file is part of a modified version of new-api, distributed under the
GNU Affero General Public License v3.0 or later. See LICENSE and NOTICE.
Upstream: https://github.com/QuantumNous/new-api
Fork changes are catalogued in BRANDING.md (AGPLv3 s.7(c) change marking).
*/
package model

// UNIFYAPI-FORK: the two templates every console starts with. They are a
// starting point: the operator refines the wording in the template editor,
// and an edit there is never overwritten (see seedAdminEmailTemplates).
//
// Copy rules they follow, from AGENTS.md: the product is "Unify API"; no
// model count or other claim that has not been checked against the catalogue
// (open issue 7); the Mythos template promises neither approval nor a price.

const (
	AdminEmailBuiltinWelcome = "welcome"
	AdminEmailBuiltinMythos  = "mythos_access"
)

func BuiltinAdminEmailTemplates() []AdminEmailTemplate {
	return []AdminEmailTemplate{
		{
			BuiltinKey: AdminEmailBuiltinWelcome,
			Name:       "Welcome",
			Subject:    "Welcome to Unify API",
			Headline:   "Welcome, {{display_name}}",
			Body: `Thanks for signing up for Unify API. We're glad you're here.

Unify API gives you one API key and one OpenAI-compatible endpoint for models from many providers. If your code already uses an OpenAI SDK, point its base URL at Unify API and keep the rest of your code as it is.

Getting started takes three steps:
1. Create an API key in your console.
2. Set your SDK's base URL to {{api_base_url}}
3. Choose a model from the model list in your console and send your first request.

The documentation walks through each step: {{docs_url}}

A few premium models, such as Claude Mythos, are available by request only. If you would like access to one of them, reply to this email and tell us a little about your use case.

If you have any questions, just reply to this email.`,
			CTALabel: "Open your console",
			CTAURL:   "{{console_url}}",
		},
		{
			BuiltinKey: AdminEmailBuiltinMythos,
			Name:       "Premium model access (Claude Mythos)",
			Subject:    "Requesting access to Claude Mythos",
			Headline:   "Access to Claude Mythos",
			Body: `Hi {{display_name}},

Thanks for your interest in Claude Mythos. On Unify API, Claude Mythos is available by request only: we enable it account by account, after a short review of how it will be used.

To request access, reply to this email and tell us:
1. Your organisation: its name, website, and what it does.
2. Your use case: what you plan to build or run with Claude Mythos.
3. Expected volume: roughly how many requests or tokens you expect per day or per month.
4. How the outputs will be used: for example internally, inside a product your customers use, or for research.

We will review your reply and let you know the outcome. If access is approved, we will enable Claude Mythos for your account ({{email}}) and confirm by email.

Thank you,
The Unify API team`,
		},
	}
}
