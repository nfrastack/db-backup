# nfrastack/db-backup (container)
Container packaging for db-backup - scheduled database backups configured entirely through environment variables.

This image wraps the [db-backup](https://github.com/nfrastack/db-backup) binary (`dbb`) for container use. Set `DB01_*` style environment variables and a `db-backup.yml` is generated and run by the built in scheduler daemon - or set `SETUP_TYPE=MANUAL` and mount your own configuration file for advanced setups.

The complete environment variable reference lives at [nfrastack.com/db-backup/container](https://nfrastack.com/db-backup/container).

## Documentation

Full documentation lives at [nfrastack.com/db-backup](https://nfrastack.com/db-backup) containing guides, recipes, CLI reference, configuration reference, and licensing.

## License

The container image - its packaging, runtime scripts and configuration - is licensed under the MIT License.

The bundled `dbb` binary is licensed separately from the packaging: the Community Edition under the GNU Affero General Public License v3.0 or later (AGPL-3.0-or-later), and the Supported Edition code (`supported/`) under the proprietary Nfrastack Supporter License v1 (NSLv1). See [LICENSE](../LICENSE) and [`LICENSES/`](../LICENSES).

## Copyright

Copyright (C) 2026 [Nfrastack](https://nfrastack.com)
