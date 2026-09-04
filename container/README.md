# nfrastack/db-backup (container)

```text
             .o88o.                                 .                       oooo
             888 `"                               .o8                       `888
ooo. .oo.   o888oo  oooo d8b  .oooo.    .oooo.o .o888oo  .oooo.    .ooooo.   888  oooo
`888P"Y88b   888    `888""8P `P  )88b  d88(  "8   888   `P  )88b  d88' `"Y8  888 .8P'
 888   888   888     888      .oP"888  `"Y88b.    888    .oP"888  888        888888.
 888   888   888     888     d8(  888  o.  )88b   888 . d8(  888  888   .o8  888 `88b.
o888o o888o o888o   d888b    `Y888""8o 8""888P'   "888" `Y888""8o `Y8bod8P' o888o o888o
```

Container packaging for db-backup - scheduled database backups configured entirely through environment variables.

This image wraps the [db-backup](https://github.com/nfrastack/db-backup) binary (`dbb`) for container use. Set `DB01_*` style environment variables and a `db-backup.yml` is generated and run by the built in scheduler daemon - or set `SETUP_TYPE=MANUAL` and mount your own configuration file for advanced setups.

## Installation

Prebuilt images are available on [GitHub Container Registry](https://github.com/nfrastack/db-backup/pkgs/container/db-backup) and [Docker Hub](https://hub.docker.com/r/nfrastack/db-backup):

```text
ghcr.io/nfrastack/db-backup:(image_tag)
docker.io/nfrastack/db-backup:(image_tag)
```

| Base   | Tag       |
| ------ | --------- |
| Alpine | `:latest` |

Images are built for `amd64` by default, with optional support for `arm64` and other architectures.

## Quick Start

The quickest way to get started is [docker-compose](https://docs.docker.com/compose/) - see [contrib/compose/compose.yml](../contrib/compose/compose.yml) for a working example:

```yaml
services:
  db-backup:
    image: docker.io/nfrastack/db-backup:latest
    volumes:
      - ./backup:/backup
    environment:
      - DB01_TYPE=mysql
      - DB01_HOST=db.internal
      - DB01_USER=backup
      - DB01_PASS=secret
      - DB01_NAME=testdb
      - DB01_BACKUP_BEGIN=0230
    restart: always
```

### Persistent Storage

| Directory      | Description                         |
| -------------- | ----------------------------------- |
| `/backup`      | Backups and state                   |
| `/config`      | _Optional_ Config file directory    |
| `/logs`        | _Optional_ Logfiles for backup jobs |

### Behavior Notes

- The `dbb` process runs as the non-root `dbbackup` user (uid `10000`). Anything mounted into the container that it must read (YAML configs, TLS/CA files, age identities, restore archives) must be readable by that uid.
- With `SETUP_TYPE=AUTO` (default) the config is regenerated from your `DB*` env vars at startup. An existing config file in `$CONFIG_PATH` is never overwritten; env vars are then ignored with a warning.
- `MODE=MANUAL` keeps the container alive so you can invoke `dbb` by hand; with `MANUAL_RUN_FOREVER=FALSE` every job runs once at startup and the container exits (Kubernetes CronJob friendly).

The complete environment variable reference lives at [nfrastack.com/db-backup/container](https://nfrastack.com/db-backup/container).

## Maintenance

### dbb binary

The application binary lives at `/app/dbb`

### Shell Access

```bash
docker exec -it (container name) bash
```

### Manual Backups

Enter the container and run:

- `backup-now` - run every job once
- `backup-now <jobname>` - run a single job once (eg `backup-now db01`)
- `backup01-now`, `backup02-now`, ... - run the job at that index once

### Restoring Databases

Enter the container and type `restore` for an interactive restore, or use `docker exec <container> /app/dbb --container --config /config/db-backup.yml restore` with `RESTORE_*` variables set to generate the `restore:` block automatically.

## Upgrading from 4.x to v5

See the [upgrade guide](https://nfrastack.com/db-backup/upgrade) for deprecated and removed environment variables and everything that changed.

## Documentation

Full documentation lives at [nfrastack.com/db-backup](https://nfrastack.com/db-backup) containing guides, recipes, CLI reference, configuration reference, and licensing.

## Support

If you found a bug, please submit a [Bug Report](https://github.com/nfrastack/db-backup/issues/new).

For implementation help, consulting, or Supported Edition licenses, reach out at [hello@nfrastack.com](mailto:hello+dbbgh@nfrastack.com) or visit [nfrastack.com/db-backup/support](https://nfrastack.com/db-backup/support).

## License

The container image - its packaging, runtime scripts and configuration - is licensed under the MIT License.

The bundled `dbb` binary is licensed separately from the packaging: the Community Edition under the GNU Affero General Public License v3.0 or later (AGPL-3.0-or-later), and the Supported Edition code (`supported/`) under the proprietary Nfrastack Supporter License v1 (NSLv1). See [LICENSE](../LICENSE) and [`LICENSES/`](../LICENSES).

## Copyright

Copyright (C) 2026 [Nfrastack](https://nfrastack.com)
