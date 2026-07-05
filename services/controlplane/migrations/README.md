# Control-plane migrations

Postgres schema migrations for the control plane.

Files follow the `NNNN_short_name.{up,down}.sql` convention so they're
compatible with golang-migrate, goose, and dbmate without us committing
to a runner yet.

This schema is **preliminary**. It captures the v1 shape but will evolve
as we implement features. Treat early migrations as squashable until we
ship a first version externally.
