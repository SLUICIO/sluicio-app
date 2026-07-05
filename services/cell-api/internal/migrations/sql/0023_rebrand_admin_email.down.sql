-- SPDX-License-Identifier: FSL-1.1-Apache-2.0
UPDATE users
SET    email      = 'admin@conduit.local',
       updated_at = now()
WHERE  email      = 'admin@sluicio.local';
