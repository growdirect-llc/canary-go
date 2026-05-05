-- Migration 026: directed task queue for mobile operators.
-- Supports three task types: receiving, replenishment, cycle_count.
-- Status machine: queued → assigned → in_progress → complete → verified.
-- Side exits: skipped (with reason), cancelled.

CREATE TABLE app.directed_tasks (
  id                  uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid        NOT NULL REFERENCES app.tenants(id),
  task_type           text        NOT NULL
                                  CHECK (task_type IN ('receiving', 'replenishment', 'cycle_count')),
  priority            int         NOT NULL DEFAULT 3
                                  CHECK (priority BETWEEN 1 AND 5),
  status              text        NOT NULL DEFAULT 'queued'
                                  CHECK (status IN ('queued','assigned','in_progress',
                                                    'complete','verified','skipped','cancelled')),
  item_id             uuid        REFERENCES catalog.items(id),
  location_id         uuid        REFERENCES location.locations(id),
  zone_id             uuid        REFERENCES location.location_zones(id),
  quantity            numeric(14,4),
  source_location_id  uuid        REFERENCES location.locations(id),  -- replenishment: pull-from location
  assignee_id         uuid        REFERENCES employee.employees(id),
  assigned_at         timestamptz,
  started_at          timestamptz,
  completed_at        timestamptz,
  verified_at         timestamptz,
  estimated_seconds   int,
  skip_reason         text,
  source_ref          text,       -- caller-supplied correlation id (PO #, movement id, etc.)
  attributes          jsonb       NOT NULL DEFAULT '{}',
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_tasks_tenant_status ON app.directed_tasks(tenant_id, status);
CREATE INDEX idx_tasks_tenant_next   ON app.directed_tasks(tenant_id, priority, created_at)
    WHERE status = 'queued';
CREATE INDEX idx_tasks_assignee      ON app.directed_tasks(assignee_id)
    WHERE assignee_id IS NOT NULL;
CREATE INDEX idx_tasks_item          ON app.directed_tasks(item_id, location_id)
    WHERE status IN ('queued','assigned','in_progress');

-- task_exceptions: log damage, wrong-qty, blocked-location, etc.
CREATE TABLE app.task_exceptions (
  id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id      uuid        NOT NULL REFERENCES app.directed_tasks(id),
  tenant_id    uuid        NOT NULL REFERENCES app.tenants(id),
  reason_code  text        NOT NULL
               CHECK (reason_code IN ('damage','wrong_qty','location_blocked',
                                      'item_not_found','other')),
  note         text,
  reported_by  uuid        REFERENCES employee.employees(id),
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_task_exceptions_task ON app.task_exceptions(task_id);
