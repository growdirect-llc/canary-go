-- Migration 027: operator-set Min/Max thresholds per (tenant, item, location).
-- Used by the replenishment trigger (GRO-799) to decide when to generate
-- a replenishment task. display_min = floor below which a pull is triggered;
-- display_max = target on-hand quantity for the pull.
-- NULL display_max → trigger still fires; quantity_to_pull defaults to display_min.

CREATE TABLE app.inventory_thresholds (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id     uuid        NOT NULL REFERENCES app.tenants(id),
  item_id       uuid        NOT NULL REFERENCES catalog.items(id),
  location_id   uuid        NOT NULL REFERENCES location.locations(id),
  display_min   numeric(14,4) NOT NULL DEFAULT 1,
  display_max   numeric(14,4),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, item_id, location_id)
);

CREATE INDEX idx_thresholds_tenant ON app.inventory_thresholds(tenant_id);
CREATE INDEX idx_thresholds_item_loc ON app.inventory_thresholds(item_id, location_id);
