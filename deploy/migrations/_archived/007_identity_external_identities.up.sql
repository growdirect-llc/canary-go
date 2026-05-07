-- 007_identity_external_identities.up.sql
CREATE TABLE IF NOT EXISTS app.external_identities (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID        NOT NULL REFERENCES app.merchants(id),
    entity_type TEXT        NOT NULL
                            CHECK (entity_type IN ('employee','location','device','product','customer')),
    entity_id   UUID        NOT NULL,
    source_code TEXT        NOT NULL REFERENCES app.source_systems(code),
    external_id TEXT        NOT NULL,
    is_primary  BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by  UUID,
    modified_by UUID,
    CONSTRAINT uq_ext_id_merchant_source_entity
        UNIQUE (merchant_id, source_code, entity_type, external_id),
    CONSTRAINT uq_ext_id_merchant_entity_source
        UNIQUE (merchant_id, entity_type, entity_id, source_code)
);

CREATE INDEX IF NOT EXISTS idx_ext_id_reverse
    ON app.external_identities (merchant_id, entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_ext_id_merchant ON app.external_identities (merchant_id);
