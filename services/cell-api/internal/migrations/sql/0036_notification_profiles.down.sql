ALTER TABLE integrations DROP COLUMN IF EXISTS notification_profile_id;
DROP TABLE IF EXISTS notification_profile_channels;
DROP TABLE IF EXISTS notification_profiles;
