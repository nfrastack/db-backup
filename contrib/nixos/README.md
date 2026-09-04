# NixOS

db-backup ships a Nix flake with a NixOS module that runs the `dbb scheduler`
as a systemd service and generates the `db-backup.yml` configuration from Nix
options declaratively.

> The most up to date module lives in the source repository at [`contrib/nixos/`](https://github.com/nfrastack/db-backup/tree/main/contrib/nixos).

## Add the flake as an input

```nix
{
  inputs.db-backup.url = "github:nfrastack/db-backup";

  outputs = { self, nixpkgs, db-backup }: {
    nixosConfigurations.host = nixpkgs.lib.nixosSystem {
      modules = [
        db-backup.nixosModules.default
        ./configuration.nix
      ];
    };
  };
}
```

## Enable the service

Import `db-backup.nixosModules.default` and enable `services.db-backup`:

```nix
{ config, pkgs, ... }:
{
  services.db-backup = {
    enable = true;
    user = "dbbackup";
    group = "dbbackup";
    # defaults + profiles + jobs go here
  };
}
```

## Options (`services.db-backup`)

| Option           | Type              | Default          | Description                                                                                                  |
| ---------------- | ----------------- | ---------------- | ------------------------------------------------------------------------------------------------------------ |
| `enable`         | bool              | `false`          | Enable the service + config generation                                                                       |
| `service.enable` | bool              | `true`           | Enable the systemd service (set `false` to only generate the config file)                                    |
| `package`        | package           | flake's Go build | The `db-backup` package to use                                                                               |
| `manageConfig`   | bool              | `true`           | Generate `configFile` from options below (disable to manage the YAML yourself)                               |
| `configFile`     | string            | nix store        | Path of the YAML config read by the scheduler. When `manageConfig = true` (default)                          |
|                  |                   |                  | this is a Nix store path the systemd unit reads it directly.                                                 |
|                  |                   |                  | To write the config to `/etc/db-backup.yml` (or any non store path)                                          |
|                  |                   |                  | set `services.db-backup.configFile = "/etc/db-backup.yml"` and the module will                               |
|                  |                   |                  | generate config at path during activation.                                                                   |
| `stateDir`       | string or null    | `/var/lib/dbb`   | Runtime state dir                                                                                            |
| `user`           | string            | `root`           | User the scheduler runs as                                                                                   |
| `group`          | string            | value of `user`  | Group the scheduler runs as                                                                                  |
| `settings`       | attrs             | `{}`             | Raw top-level YAML merged over the typed options                                                             |
| `log`            | submodule         | `{}`             | Logging configuration (`log:` section)                                                                       |
| `defaults`       | submodule         | `{}`             | Defaults applied to every job (`defaults:` section)                                                          |
| `profiles`       | attrs of anything | `{}`             | Free-form profiles (`connections`, `databases`, `backup`, `storage`, `encryption`, `maintenance`, `archive`) |
| `jobs`           | list of anything  | `[]`             | Free-form job list (`jobs:` section)                                                                         |

### `log` options

`level`, `format`, `type`, `path`, `user`, `group`, `colour`, `prefix`,
`prefix_format`, `session_id`, `run_id`, `timings`, `size`, `timezone`, `utc`.

### `defaults` options

- `backup` - `strategy`, `type`, `filename`, `create_latest`, `schema_only`, `full_every`, `full_after`
- `compression` - `type`, `level`
- `checksum`
- `storage` - `backend`, `bucket`, `path`, `endpoint`, `region`, `key_id`, `key_secret`, `account`, `key`, `url`, `pass`, `file_mode`, `dir_mode`, `user`, `group`, `tls`
- `tls`
- `retention` - `last`, `within`, `hourly`, `daily`, `weekly`, `monthly`, `yearly`
- `archive`
- `hooks` - `pre`, `post`

## Secrets

Use the `file://` / `env://` secret prefixes supported by every value field to keep passphrases and keys out of the store:

```nix
services.db-backup.defaults.storage.key_secret = "file:///run/secrets/s3_key";
services.db-backup.profiles.connections.prod.pass = "file:///run/secrets/db_pass";
```

## Users, groups, and storage directories

The scheduler writes to the backup directory, so it needs a dedicated user and
writable state/backup paths. The module runs as the `user`/`group` you set
(typically `dbbackup`). Create the user and the directories with `systemd
tmpfiles`:

```nix
users = {
  users.dbbackup = {
    isSystemUser = true;
    group = "dbbackup";
    home = "/var/lib/dbb";
    createHome = true;
  };
  groups.dbbackup = { };
};

systemd.tmpfiles.rules = [
  "d /var/backups 0700 dbbackup dbbackup - -"
  "d /var/lib/dbb 0700 dbbackup dbbackup - -"
];
```

## Systemd service

The generated unit runs `dbb -c <configFile> scheduler` as a daemon with
`Restart=always` and journal output. Setting `services.db-backup.configFile` to a regular
will read that instead. The `dbb` binary called via the service auto detects systemd and suppresses the
timestamp prefix on the journal stream. You can set `log.type` to `file`, and `both` and timestamps will be written
to the log file.
