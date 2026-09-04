# nfrastack/db-backup

Consistent, scheduled database backups for nine engines with a single, secret friendly binary.

db-backup is a cross platform tool that backs up, restores, verifies, and maintains databases across multiple engines using one unified configuration syntax. Whether you are calling it from your own scripts or using the built-in multi-job scheduler, db-backup fits servers, containers, and CI pipelines alike.

## Features

- Multi engine - MySQL, MariaDB, PostgreSQL, MongoDB, Microsoft SQL Server, Redis, SQLite3, InfluxDB, CouchDB
- Backup strategies - full, incremental, differential, schema only
- Table selection - include/exclude filters, per table schema only
- Storage - filesystem, S3 compatible, WebDAV, Azure Blob, Google Cloud Storage
- Encryption at rest - AGE, GPG/OpenPGP, OpenSSL AES-256 recipients or passphrases
- Scheduling - cron, intervals, clock times, natural language, day filters, blackout windows
- Retention - keep last, time window, and per period tiers (hourly/daily/weekly/monthly/yearly)
- Compression - zstd, gzip, bzip2, xz with configurable levels and parallel threading
- Restore - CLI, interactive guided mode, config driven profiles
- Integrity - checksums, JSON sidecar metadata per backup
- Maintenance  analyze, optimize, vacuum, reindex, compact, memory purge on succesful backup or independently.
- Hooks & observability - pre/post hooks, live progress, detailed logging via text or json

Most features work out of the box in the free Community edition. Some advanced features require a [Supporter license](https://nfrastack.com/db-backup/editions).

## Get Started

Run your first backup with the container:

```bash
docker run --rm \
  -e DB01_TYPE=mysql \
  -e DB01_HOST=db-internal \
  -e DB01_USER=backup \
  -e DB01_PASS=secret \
  -e DB01_NAME=testdb \
  -v ./backups:/backup \
  docker.io/nfrastack/db-backup
```

Or grab a [precompiled binary](https://github.com/nfrastack/db-backup/releases), build [from source](https://nfrastack.com/db-backup/install), or use the [NixOS module](https://nfrastack.com/db-backup/nixos) and use the configuration files to perform advanced functions.

## Documentation

All documentation lives at [nfrastack.com/db-backup](https://nfrastack.com/db-backup):

| Section                                                     | Contents                                                         |
| ----------------------------------------------------------- | ---------------------------------------------------------------- |
| [Install](https://nfrastack.com/db-backup/install)          | Containers, binaries, source, NixOS module                       |
| [Guides](https://nfrastack.com/db-backup/guides/quickstart) | Quickstart, configuration, jobs, profiles, encryption, lifecycle |
| [Recipes](https://nfrastack.com/db-backup/recipes/backup)   | Copy-paste examples for every command                            |
| [Reference](https://nfrastack.com/db-backup/reference/cli)  | CLI flags, config keys, engines, sidecars                        |
| [Container](https://nfrastack.com/db-backup/container)      | Image usage, environment variables, upgrading                    |
| [Editions](https://nfrastack.com/db-backup/editions)        | Community vs Supported feature comparison                        |

## Editions

db-backup uses an open-core model. The Community Edition is free and open source under AGPL-3.0-or-later. Upgrading to a Supporter Edition is licensed under a proprietary commercial license and unlocks advanced features while directly funding ongoing development. See the full [edition comparison](https://nfrastack.com/db-backup/editions).

## Support

If you found a bug, please submit a [Bug Report](https://github.com/nfrastack/db-backup/issues/new). Usage questions will be closed as `not-a-bug`.

For implementation help, consulting, or Supported edition licenses, reach out at [hello@nfrastack.com](mailto:hello@nfrastack.com) or visit [nfrastack.com/db-backup/support](https://nfrastack.com/db-backup/support).

## License

This project is dual licensed:

- The [GNU Affero General Public License v3.0 or later](LICENSE) is a free software license covering the Community Edition ensuring your freedom to use, modify, and distribute the software with the condition that modified versions are distributed under the same license.
- The proprietary [Nfrastack Supporter License v1 (NSLv1)](https://nfrastack.com/db-backup/license/nsl) governs the Supported Edition (`supported/`), unlocking advanced capabilities along with license compliance and direct support.

Each file in this project carries an SPDX license identifier following the [REUSE guidelines](https://reuse.software/); the full texts of both licenses are available in the [LICENSES](LICENSES) directory.

The container image packaging (`container/`) is licensed separately under the MIT License - see [container/LICENSE](container/LICENSE), the bundled `dbb` binary inside the image is licensed as described above, not under MIT.

## Copyright

Copyright (C) 2026 [Nfrastack](https://nfrastack.com)
