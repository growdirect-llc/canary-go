-- 04_c_e_customers_employees.sql — Customer + Employee Domain
-- Source: docs/sdds/go-handoff/canonical-data-model.md §5 (lines 1476-1875)
-- Schemas: c, e

-- customer.customers — Customer master (sparse-by-default; supports anonymous walk-in through B2B accounts)
CREATE TABLE customer.customers (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  customer_code   text,                                  -- merchant-assigned (loyalty number, account number); nullable for anonymous walk-in
  customer_type   text NOT NULL DEFAULT 'individual',    -- individual | business | household | guest
  first_name      text,
  last_name       text,
  display_name    text,                                  -- computed or business name
  email           text,                                  -- primary email (PII tier 2)
  phone           text,                                  -- primary phone (PII tier 2; E.164 format)
  birth_date      date,                                  -- for age-restriction verification + birthday promos (PII tier 3)
  preferred_language text DEFAULT 'en-US',
  marketing_opt_in   boolean NOT NULL DEFAULT false,     -- explicit consent
  primary_address jsonb DEFAULT '{}',                    -- {line1, line2, city, region, postal_code, country}
  attributes      jsonb NOT NULL DEFAULT '{}',           -- demographics, segments, merchant-defined
  status          text NOT NULL DEFAULT 'active',        -- active | inactive | suppressed | merged
  merged_into     uuid REFERENCES customer.customers(id),       -- for dedup / merge events
  external_ids    jsonb DEFAULT '{}',                    -- {pos_native_id, square_id, stripe_customer_id, etc.}
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, customer_code) DEFERRABLE INITIALLY DEFERRED
);

CREATE INDEX idx_customers_tenant ON customer.customers(tenant_id);
CREATE INDEX idx_customers_email ON customer.customers(tenant_id, lower(email)) WHERE email IS NOT NULL AND status = 'active';
CREATE INDEX idx_customers_phone ON customer.customers(tenant_id, phone) WHERE phone IS NOT NULL AND status = 'active';
CREATE INDEX idx_customers_status ON customer.customers(status) WHERE status != 'active';
CREATE INDEX idx_customers_attributes ON customer.customers USING gin(attributes);
CREATE INDEX idx_customers_external_ids ON customer.customers USING gin(external_ids);

-- customer.customer_addresses — Multi-address per customer (B2B ship-to, billing, mailing)
CREATE TABLE customer.customer_addresses (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  customer_id     uuid NOT NULL REFERENCES customer.customers(id) ON DELETE CASCADE,
  address_type    text NOT NULL DEFAULT 'shipping',      -- shipping | billing | mailing | service | pickup
  recipient_name  text,
  line_1          text NOT NULL,
  line_2          text,
  city            text NOT NULL,
  region          text,                                   -- state/province/county
  postal_code     text,
  country         text NOT NULL DEFAULT 'US',             -- ISO 3166 alpha-2
  latitude        numeric(10,7),
  longitude       numeric(10,7),
  is_default      boolean NOT NULL DEFAULT false,
  attributes      jsonb NOT NULL DEFAULT '{}',
  status          text NOT NULL DEFAULT 'active',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT one_default_per_type EXCLUDE (customer_id WITH =, address_type WITH =) WHERE (is_default = true AND status = 'active')
);

CREATE INDEX idx_addresses_tenant ON customer.customer_addresses(tenant_id);
CREATE INDEX idx_addresses_customer ON customer.customer_addresses(customer_id);
CREATE INDEX idx_addresses_type_default ON customer.customer_addresses(customer_id, address_type) WHERE is_default = true;

-- customer.loyalty_memberships — Loyalty membership (multi-program, denormalized points balance)
CREATE TABLE customer.loyalty_memberships (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  customer_id         uuid NOT NULL REFERENCES customer.customers(id) ON DELETE CASCADE,
  program_code        text NOT NULL DEFAULT 'default',   -- merchant may run multiple programs
  membership_number   text NOT NULL,                     -- the loyalty card / member ID
  enrollment_date     date NOT NULL DEFAULT CURRENT_DATE,
  tier                text DEFAULT 'standard',           -- standard | silver | gold | platinum | etc.
  points_balance      bigint NOT NULL DEFAULT 0,         -- current available points
  points_lifetime     bigint NOT NULL DEFAULT 0,         -- cumulative earned (informational)
  birth_date          date,                              -- for birthday promos (denormalized from customer for query speed)
  preferences         jsonb DEFAULT '{}',                -- communication prefs, category interests
  attributes          jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'active',    -- active | suspended | expired | closed
  expires_at          timestamptz,                        -- if program has expiration
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, program_code, membership_number),
  UNIQUE (tenant_id, customer_id, program_code)            -- one membership per customer per program
);

CREATE INDEX idx_loyalty_tenant ON customer.loyalty_memberships(tenant_id);
CREATE INDEX idx_loyalty_customer ON customer.loyalty_memberships(customer_id);
CREATE INDEX idx_loyalty_member_lookup ON customer.loyalty_memberships(tenant_id, membership_number) WHERE status = 'active';
CREATE INDEX idx_loyalty_tier ON customer.loyalty_memberships(tier) WHERE status = 'active';

-- employee.employees — Employee master (no pay rate stored; nullable user_id for cashiers without login)
CREATE TABLE employee.employees (
  id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id           uuid NOT NULL REFERENCES app.tenants(id),
  user_id             uuid REFERENCES app.users(id),         -- if employee has a Canary login (managers, supervisors); nullable for cashiers without login
  employee_code       text NOT NULL,                          -- POS cashier number, badge ID
  first_name          text NOT NULL,
  last_name           text NOT NULL,
  display_name        text,
  email               text,                                   -- work email (PII tier 2)
  phone               text,                                   -- (PII tier 2)
  hire_date           date NOT NULL,
  termination_date    date,
  employment_status   text NOT NULL DEFAULT 'active',        -- active | on_leave | terminated | seasonal | applicant
  pay_type            text,                                   -- hourly | salaried | contract | tipped (no actual pay rate stored — sensitive)
  attributes          jsonb NOT NULL DEFAULT '{}',           -- merchant-defined fields (badge color, training certs)
  external_ids        jsonb DEFAULT '{}',                    -- payroll system ID, POS-native cashier ID
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, employee_code)
);

CREATE INDEX idx_employees_tenant ON employee.employees(tenant_id);
CREATE INDEX idx_employees_user ON employee.employees(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_employees_status ON employee.employees(employment_status) WHERE employment_status != 'active';
CREATE INDEX idx_employees_external_ids ON employee.employees USING gin(external_ids);

-- employee.employee_role_assignments — Effective-dated role assignments (cashier, manager, etc.)
CREATE TABLE employee.employee_role_assignments (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  employee_id     uuid NOT NULL REFERENCES employee.employees(id) ON DELETE CASCADE,
  role_code       text NOT NULL,                              -- cashier | shift_lead | manager | gm | inventory_lead | etc.
  effective_start date NOT NULL DEFAULT CURRENT_DATE,
  effective_end   date,                                       -- NULL = current
  attributes      jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, employee_id, role_code, effective_start)
);

CREATE INDEX idx_emp_roles_tenant ON employee.employee_role_assignments(tenant_id);
CREATE INDEX idx_emp_roles_employee ON employee.employee_role_assignments(employee_id);
CREATE INDEX idx_emp_roles_active ON employee.employee_role_assignments(employee_id, role_code) WHERE effective_end IS NULL;

-- employee.employee_location_assignments — Effective-dated employee-to-location with single-primary EXCLUDE
CREATE TABLE employee.employee_location_assignments (
  id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id       uuid NOT NULL REFERENCES app.tenants(id),
  employee_id     uuid NOT NULL REFERENCES employee.employees(id) ON DELETE CASCADE,
  location_id     uuid NOT NULL REFERENCES location.locations(id) ON DELETE CASCADE,
  assignment_type text NOT NULL DEFAULT 'home',                 -- home | rotating | temporary | floating
  effective_start date NOT NULL DEFAULT CURRENT_DATE,
  effective_end   date,
  is_primary      boolean NOT NULL DEFAULT false,
  attributes      jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, employee_id, location_id, effective_start),
  CONSTRAINT one_primary_per_employee EXCLUDE (employee_id WITH =) WHERE (is_primary = true AND effective_end IS NULL)
);

CREATE INDEX idx_emp_loc_tenant ON employee.employee_location_assignments(tenant_id);
CREATE INDEX idx_emp_loc_employee ON employee.employee_location_assignments(employee_id);
CREATE INDEX idx_emp_loc_location ON employee.employee_location_assignments(location_id);
CREATE INDEX idx_emp_loc_active ON employee.employee_location_assignments(employee_id, location_id) WHERE effective_end IS NULL;
