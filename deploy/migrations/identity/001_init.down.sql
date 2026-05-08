-- 001_init.down.sql — drop the identity DB schema.
--
-- Reverses 001_init.up.sql. Drops in dependency order
-- (refresh_tokens → person_credentials → persons).

DROP TABLE IF EXISTS public.refresh_tokens;
DROP TABLE IF EXISTS public.person_credentials;
DROP TABLE IF EXISTS public.persons;
