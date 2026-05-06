# Canary Go

Store operations platform for independent retailers on NCR Counterpoint and RapidPOS. Written in Go. ARTS-native canonical data model.

**[canary.growdirect.io](https://canary.growdirect.io)** — architecture vault and SDD index  
**[demo.growdirect.io](https://demo.growdirect.io)** — live Square OAuth demo  

---

## What this is

Canary is the operational layer that sits above the POS. It handles replenishment, receiving, loss prevention, task management, inventory, and reporting for stores running NCR Counterpoint or RapidPOS — connecting a store to its suppliers, staff, and financials.

Three accountability rails close the gaps that retail operations historically accept as unavoidable:

- **Operational** — no unknown loss. Every inventory, transaction, and labor event has a node in the closed graph.
- **Financial** — no unauthorized spend. Open-to-buy is a funded Lightning wallet gated by L402. The agent cannot overspend OTB because it cannot pay for the tool call.
- **Evidentiary** — no unanchored record. Every Fox case hash is published to a public L2 blockchain. The evidence chain is verifiable by any third party.

## MCP autodiscovery

The gateway exposes a public discovery document at:

```
GET https://demo.growdirect.io/.well-known/mcp.json
```

Returns the MCP endpoint, auth scheme, tool count, and module list. 28 tools across 7 domain modules (alert, analytics, asset, customer, employee, returns, report). The `POST /mcp` endpoint is API-key gated.

## Architecture

29 Go services. Chi HTTP router. pgx/v5. Valkey streams. PostgreSQL 17 with pgvector.

Event path: webhook receipt → HMAC validation → Valkey stream → Triple Subscriber (seal → parse → detect) → pgvector intelligence layer → MCP tool surface.

Full architecture documentation: **[canary.growdirect.io/sdds/](https://canary.growdirect.io/sdds/)**

## OpenAPI spec

Auto-generated from the canonical retail data model. 135 paths, 261 component schemas, 65 canonical entities.

```
services/canary-protocol/openapi/openapi.yaml
```

Do not hand-edit. See `services/canary-protocol/openapi/README.md` for regeneration instructions.

## Status

Active build. Square OAuth sandbox demo live. Wave 1 (LP Core) UI in progress.

Patent application 63/991,596 — *Universal Event Notarization, Six-Node Architecture* (filed 2026-02-26).

## License

Apache-2.0. Copyright 2026 GrowDirect LLC.
