-- 00_schemas.sql — schema layout per canonical-data-model.md §1.4
-- 14 schemas total. Single-letter names per ARTS / TOM convention; multi-letter
-- for platform-internal namespaces.
--
-- Letter   Domain                  Source SDD section
-- ------   ----------------------  -------------------
-- app      tenants + auth          §1, §10 (foundation)
-- m        merchandising / items   §3 Item domain
-- l        locations               §4 Location domain
-- s        space (planograms)      §4 Space subdomain
-- c        customers               §5 Customer subdomain
-- e        employees               §5 Employee subdomain
-- i        inventory + distrib.    §6 Inventory + Distribution
-- o        orders                  §7 Orders
-- p        pricing                 §8 Pricing
-- f        financial / GL          §8 Financial
-- t        POSLog / sales audit    §9 Transactions
-- q        canary mechanics (LP)   §10 Loss prevention, fox, alerts
-- ledger   L402 budgets, anchors   §10 Platform mechanics
-- memory   memory bus / vector     §10 Platform mechanics
-- protocol gateway + evidence      Phase 1.A-J (gateway, sub1, secrets)

CREATE EXTENSION IF NOT EXISTS pgcrypto;        -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS pg_trgm;         -- text search
CREATE EXTENSION IF NOT EXISTS ltree;           -- recursive hierarchy paths (m.product_categories.path, l.location_hierarchy.path)
CREATE EXTENSION IF NOT EXISTS btree_gist;      -- exclusion constraints with non-equality (m.item_vendors EXCLUDE)
-- pgvector reserved for memory schema; add when memory tables migrate

CREATE SCHEMA IF NOT EXISTS app;
CREATE SCHEMA IF NOT EXISTS m;
CREATE SCHEMA IF NOT EXISTS l;
CREATE SCHEMA IF NOT EXISTS s;
CREATE SCHEMA IF NOT EXISTS c;
CREATE SCHEMA IF NOT EXISTS e;
CREATE SCHEMA IF NOT EXISTS i;
CREATE SCHEMA IF NOT EXISTS o;
CREATE SCHEMA IF NOT EXISTS p;
CREATE SCHEMA IF NOT EXISTS f;
CREATE SCHEMA IF NOT EXISTS t;
CREATE SCHEMA IF NOT EXISTS q;
CREATE SCHEMA IF NOT EXISTS ledger;
CREATE SCHEMA IF NOT EXISTS memory;
CREATE SCHEMA IF NOT EXISTS protocol;
