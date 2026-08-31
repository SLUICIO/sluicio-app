<!-- SPDX-License-Identifier: Apache-2.0 -->

# Renewing an Enterprise license without a restart

An Enterprise license can reach a cell two ways, and only one of them can be renewed in place.

`SLUICIO_LICENSE_KEY` carries the token inline. It is read once at startup, so replacing it means editing the environment and recreating the container. cell-api runs as a single replica, so that is a short outage of the UI and of alert evaluation.

`SLUICIO_LICENSE_FILE` points at a file holding the token. cell-api re-reads that file on an interval (`LICENSE_RELOAD_INTERVAL`, default `1m`), so a renewal is a file copy and nothing restarts.

The inline variable wins when both are set, and the cell says so at startup - otherwise somebody edits the file, watches nothing happen, and has no way to tell why.

## Why this matters for term-length licenses

A license issued for the contract period is renewed as often as the contract runs. A quarterly agreement means four new keys a year, per customer, each one landing on a machine you may not control. The minting takes seconds; arranging a restart with the customer's operations team does not.

## Setting it up

With the server compose, put the token in a directory and point the two variables at it:

```
LICENSE_DIR=/opt/sluicio/license
SLUICIO_LICENSE_FILE=/etc/sluicio/license/license.key
```

`LICENSE_DIR` is the host directory; it is mounted read-only at `/etc/sluicio/license`. It is a directory rather than a single file on purpose: a bind mount of a file that does not exist yet is silently created as a directory, which fails confusingly.

Renewing is then:

```bash
cp license.key /opt/sluicio/license/license.key
```

Within the reload interval the cell logs `license reloaded` with the new customer, plan and expiry. Nothing restarts, no notification is lost, and no alert evaluation is missed.

## What happens when the file is wrong

Every failure keeps the license already in force. A file that is missing, empty or half-written is a copy in progress, not a statement that the customer stopped being entitled, and disabling their Enterprise features over one would be an outage you caused yourself. The same applies to a token signed by the wrong key: it is rejected and the running license stays.

A file that stays broken is retried every interval - that is how correcting it takes effect without a restart - but reported only when the problem changes, so one bad token does not write the same warning every minute for as long as the cell runs.

A license that was already invalid when the cell started is retried too, so fixing a typo in the first install does not need the restart this exists to avoid.

## Kubernetes

The Helm chart currently injects the token as an environment variable from a Secret, which cannot be reloaded in place - updating a Secret does not update an env var in a running container. Mounting the same Secret as a file and setting `SLUICIO_LICENSE_FILE` would make renewals land on their own, since Kubernetes does refresh mounted Secret volumes. Not wired up yet.
