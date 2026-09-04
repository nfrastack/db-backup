# Example NixOS configuration using the db-backup flake module.
#
{ config, lib, pkgs, ... }:
{
  imports = [
    # add here if you import the module in your_configuration.nix instead of the flake:
    # inputs.db-backup.nixosModules.default
  ];

  services.db-backup = {
    enable = true;
    user = "dbbackup";                 # Run as a dedicated user, see below for creation.
    group = "dbbackup";                # The scheduler writes to the backup directory, so make sure it owns the output path.

    # configFile = "/etc/db-backup.yml";  # default is the store path
    manageConfig = true;                  # Set manageConfig = false to provide your own config with above path

    # Optional: Supported Edition license via raw settings
    # settings.license = {
    #   file = "/run/secrets/db-backup.lic";
    # };

    log = {
      level = "info";
      #type = "console";               # log.type: console|file|both (default console).
                                       # Under systemd the timestamp prefix is suppressed on the journal stream.
                                       # file/both keep timestamps in the log file.
      timings = true;
    };

    defaults = {
      checksum = "sha1";
      compression = {
        type = "zstd";
        level = 6;
      };
      storage = {
        backend = "filesystem";
        path = "/var/backups";
        file_mode = "600";
        dir_mode = "700";
        user = "dbbackup";
        group = "dbbackup";
      };
      retention = {
        daily = 7;
        weekly = 4;
        monthly = 6;
      };
    };

    # Connections, databases, backup, storage encryption, maintenance, archive
    profiles = {
      connections = {
        production = {
          type = "mysql";
          host = "mysql.db.internal";
          port = 3306;
          user = "backup";
          pass = "file:///run/secrets/db01_pass";
        };
        dev = {
          type = "postgres";
          host = "pg.db.internal";
          port = 5432;
          user = "backup";
          pass = "file:///run/secrets/db02_pass";
        };
      };
      databases = {
        prod = {
          connection = "production";
          include = [ "appname" "reports" ];
        };
        archive = {
          connection = "dev";
          include = [ "ALL" ];
          exclude = [ "devdata" "secretdb" ];
        };
      };
      storage = {
        s3 = {
          backend = "s3";
          bucket = "backups";
          path = "nfrastack-db-backup/";
          region = "us-east-1";
          key_id = "file:///run/secrets/s3_key_id";
          key_secret = "file:///run/secrets/s3_key_secret";
        };
      };
      encryption = {
        age = {
          type = "age";
          passphrase = "file:///run/secrets/age_pass";
        };
      };
    };

    jobs = [
      {
        name = "daily-mysql";
        database = "prod";
        schedule.time = "02:30";
        compression.type = "zstd";
        retention.within = "7d";
        storage = "s3";
        encryption = "age";
      }
      {
        name = "weekly-postgres";
        database = "archive";
        schedule.days = [ "friday" ];
        schedule.time = "23:00";
        storage = "s3";
      }
    ];
  };

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

  environment.systemPackages = [ # Optionally ship the CLI so sysops can run dbb dump/restore/list.
    config.services.db-backup.package
  ];
}
