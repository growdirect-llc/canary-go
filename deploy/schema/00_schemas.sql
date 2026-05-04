-- 00_schemas.sql — schema layout per canonical-data-model.md §1.4
-- 15 schemas total.
--
-- Schema        Domain                  Source SDD section
-- ----------    ----------------------  -------------------
-- app           tenants + auth          §1, §10 (foundation)
-- catalog       merchandising / items   §3 Item domain
-- location      locations               §4 Location domain
-- space         space (planograms)      §4 Space subdomain
-- customer      customers               §5 Customer subdomain
-- employee      employees               §5 Employee subdomain
-- inventory     inventory + distrib.    §6 Inventory + Distribution
-- orders        orders                  §7 Orders
-- pricing       pricing                 §8 Pricing
-- finance       financial / GL          §8 Financial
-- transaction   POSLog / sales audit    §9 Transactions
-- detection     canary mechanics (LP)   §10 Loss prevention, fox, alerts
-- ledger        L402 budgets, anchors   §10 Platform mechanics
-- memory        memory bus / vector     §10 Platform mechanics
-- protocol      gateway + evidence      Phase 1.A-J (gateway, sub1, secrets)

CREATE EXTENSION IF NOT EXISTS pgcrypto;        -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS pg_trgm;         -- text search
CREATE EXTENSION IF NOT EXISTS ltree;           -- recursive hierarchy paths (catalog.product_categories.path, location.location_hierarchy.path)
CREATE EXTENSION IF NOT EXISTS btree_gist;      -- exclusion constraints with non-equality (catalog.item_vendors EXCLUDE)
-- pgvector reserved for memory schema; add when memory tables migrate

CREATE SCHEMA IF NOT EXISTS app;
CREATE SCHEMA IF NOT EXISTS catalog;
CREATE SCHEMA IF NOT EXISTS location;
CREATE SCHEMA IF NOT EXISTS space;
CREATE SCHEMA IF NOT EXISTS customer;
CREATE SCHEMA IF NOT EXISTS employee;
CREATE SCHEMA IF NOT EXISTS inventory;
CREATE SCHEMA IF NOT EXISTS orders;
CREATE SCHEMA IF NOT EXISTS pricing;
CREATE SCHEMA IF NOT EXISTS finance;
CREATE SCHEMA IF NOT EXISTS transaction;
CREATE SCHEMA IF NOT EXISTS detection;
CREATE SCHEMA IF NOT EXISTS ledger;
CREATE SCHEMA IF NOT EXISTS memory;
CREATE SCHEMA IF NOT EXISTS protocol;
