-- Whether an integration policy also grants its member services (#28).
--
-- Granting an integration used to be lowered to its member service
-- names, which discarded the integration's identity. After that a grant
-- of ONE integration was indistinguishable from a grant of every
-- integration those services carry, so on a runtime hosting several
-- flows a user given one integration could read all of them, telemetry
-- included. That is an access-control defect, not a display problem.
--
-- Integrations are now recorded as themselves, and the services beneath
-- them are a separate, explicit grant. Seeing an integration and seeing
-- the service under it are different things: an operator responsible for
-- one flow usually has no business reading the runtime's other traffic,
-- and often no interest in the service as an object at all.
--
-- DEFAULT TRUE, deliberately, and this is the only reason the column has
-- a default at all. Every policy that exists when this runs was written
-- under the old meaning, where an integration grant did carry its
-- services. Defaulting to false would narrow all of them on upgrade and
-- silently remove access somebody is relying on. New policies are
-- created with false by the API, which is least privilege for anything
-- written from here on.

ALTER TABLE group_access_policies
  ADD COLUMN IF NOT EXISTS grant_services BOOLEAN NOT NULL DEFAULT TRUE;
