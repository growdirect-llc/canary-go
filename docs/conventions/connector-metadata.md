---
title: Connector Metadata Convention
date: 2026-05-10
status: draft
owners: product, design, engineering
related:
  - docs/research/open-commerce-component-patterns-2026-05-10.md
  - docs/decisions/ui-retail-vocabulary.md
  - docs/decisions/ui-status-taxonomy.md
---

# Connector Metadata Convention

## Purpose

Connector and integration screens must be factual, reviewable, and
marketplace-ready. A merchant should understand what the connector does, what
data it touches, what state it is in, what it requires, and what to do next
before authorizing it.

## Contract

Future Go view models for connector or integration surfaces should be able to
answer every required field in this table before the screen ships.

| Field | Required | Meaning | Example |
|---|---:|---|---|
| `key` | yes | Stable internal connector id | `square` |
| `name` | yes | Visible connector name | `Square` |
| `summary` | yes | One factual sentence of merchant value | `Sync locations, tenders, transactions, and item references from Square.` |
| `category` | yes | Connector category | `POS`, `Ecommerce`, `Payments`, `Accounting`, `Inventory`, `Identity` |
| `state` | yes | Current lifecycle, health, or sync state | `available`, `connected`, `syncing`, `degraded`, `disconnected`, `blocked`, `unsupported` |
| `primaryActionLabel` | yes | Main action | `Connect`, `Manage`, `Resume sync` |
| `primaryActionHref` | yes | Main action URL | `/connect?source=square` |
| `permissions` | yes | Merchant-readable permissions with read/write direction and justification | `Read transactions to build proof timelines.` |
| `sensitiveData` | yes | PII, payment-adjacent, or security data touched | `customer email`, `payment reference`, `employee id` |
| `dataBoundary` | yes | How Canary handles sensitive or payment-adjacent data | `reference-only`, `stores`, `processes`, `transmits`, `tokenized`, `out-of-scope` |
| `compatibility` | yes | Regions, devices, plans, business types, or source versions | `Requires Square locations with Orders API access.` |
| `support` | yes | Support path or owner | `Canary support`, docs URL, partner URL |
| `pricingOrRequirements` | no | Cost, plan, prerequisite, or no-extra-cost statement | `Requires Square seller account.` |
| `lastSyncAt` | no | Last successful sync timestamp | `2026-05-10T18:30:00Z` |
| `nextSyncAt` | no | Next scheduled sync timestamp | `2026-05-10T18:45:00Z` |
| `sourceSystem` | no | External system of record | `Square` |
| `identifiers` | no | External ids shown for diagnostics | `location_id`, `merchant_id` |

## Permission shape

Each permission should be structured enough for review before OAuth,
API-key entry, or partner authorization.

| Field | Meaning |
|---|---|
| `label` | Merchant-readable permission |
| `direction` | `read`, `write`, or `read-write` |
| `dataCategory` | Item, transaction, customer, employee, payment reference, location, inventory, or configuration |
| `sensitive` | Whether the permission touches sensitive data |
| `justification` | One sentence explaining why Canary needs it |
| `required` | Whether the connector can work without it |

## Data-boundary labels

Use these labels when a connector touches sensitive or payment-adjacent data.
They should map to `data-boundary` status language in UI review.

| Label | Meaning |
|---|---|
| `reference-only` | Canary stores an external reference but does not process the underlying sensitive data. |
| `stores` | Canary stores the data in its own database. |
| `processes` | Canary performs business logic over the data. |
| `transmits` | Canary sends the data to another service. |
| `tokenized` | Canary stores or uses a token instead of raw sensitive data. |
| `out-of-scope` | Canary does not touch the sensitive data category. |

## Identifier rule

Connector metadata must keep merchant labels and source-system identifiers
distinct. A connector may show SKU, GTIN, barcode, source-system id, location
id, merchant id, OIDC client id, or OAuth scope only when that identifier helps
setup, support, compliance, or troubleshooting.

## AtlasView mapping

AtlasView may author generic capability, policy, manifest, connector, and
standards-anchor metadata. Canary should render the merchant-facing connector
view from that substrate without making AtlasView a runtime dependency for the
merchant screen.

## Review rule

No connector screen should ship unless every required field above can be
answered. Missing metadata means the product contract is not ready, even if the
OAuth or API code works.
