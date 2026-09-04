{
  description = "db-backup - Database backup tool - https://nfrastack.com/db-backup";

  inputs.nixpkgs.url = "nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      version = "5.0.0";
      expectedGoVersion = "1.26.7";

      buildDate =
        let
          dateFile = nixpkgsFor.x86_64-linux.runCommand "build-date" {
            nativeBuildInputs = [ nixpkgsFor.x86_64-linux.coreutils ];
          } ''
            date -u +%Y-%m-%dT%H:%M:%SZ > $out
          '';
        in builtins.readFile dateFile;
      buildChannel = "edge";
      buildCommit =
        if self ? dirtyShortRev && self.dirtyShortRev != null then self.dirtyShortRev
        else if self ? shortRev && self.shortRev != null then self.shortRev
        else "unknown";

      supportedSystems = [
        "x86_64-linux"
        "aarch64-linux"
        "aarch64-darwin"
      ];

      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
      nixpkgsFor = forAllSystems (system: import nixpkgs {
        inherit system;
        overlays = [
          (final: prev: {
            go = final.go_1_26;
          })
        ];
      });
      src = self;

      removeNulls = value:
        if builtins.isAttrs value then
          nixpkgs.lib.filterAttrs (n: v: v != null) (nixpkgs.lib.mapAttrs (_: v: removeNulls v) value)
        else if builtins.isList value then
          map removeNulls value
        else
          value;
    in {
      packages = forAllSystems (system:
        let pkgs = nixpkgsFor.${system};
        in {
          db-backup = pkgs.buildGoModule {
            pname = "db-backup";
            version = version;
            inherit src;
            modRoot = "src";

            go = pkgs.go;

            meta = {
              description = "Database backup tool supporting MySQL, PostgreSQL, MongoDB, MSSQL, InfluxDB, CouchDB, Redis, SQLite";
              homepage = "https://github.com/nfrastack/db-backup";
              license = "AGPL-3.0-or-later";
              mainProgram = "dbb";
              maintainers = [{
                name = "nfrastack";
                email = "code@nfrastack.com";
                github = "nfrastack";
              }];
            };

            ldflags = [
              "-s"
              "-w"
              "-X main.Version=${version}"
              "-X main.buildDate=${buildDate}"
              "-X main.buildChannel=${buildChannel}"
              "-X main.buildCommit=${buildCommit}"
            ];

            vendorHash = "sha256-p4x8WPjRS7Q/46HR1Xj83Zk+DE46UIVOLsUIUeVBvcM=";
          };
        });

      devShells = forAllSystems (system:
        let pkgs = nixpkgsFor.${system};
        in pkgs.mkShell {
          buildInputs = with pkgs; [
            gnumake
            go
            yq-go
          ];
        });

      devShell = forAllSystems (system: self.devShells.${system});

      defaultPackage = forAllSystems (system: self.packages.${system}.db-backup);

      nixosModules.default = { config, lib, pkgs, ... }:
        let
          cfg = config.services.db-backup;
          fmt = pkgs.formats.yaml { };

          optStr = lib.mkOption {
            type = lib.types.nullOr lib.types.str;
            default = null;
          };
          optInt = lib.mkOption {
            type = lib.types.nullOr lib.types.int;
            default = null;
          };
          optBool = lib.mkOption {
            type = lib.types.nullOr lib.types.bool;
            default = null;
          };
          optList = lib.mkOption {
            type = lib.types.nullOr (lib.types.listOf lib.types.str);
            default = null;
          };

          tlsOptions = {
            enable = optBool;
            ca_file = optStr;
            cert_file = optStr;
            key_file = optStr;
            verify = optBool;
            version = optStr;
          };

          retentionOptions = {
            last = optInt;
            within = optStr;
            hourly = optInt;
            daily = optInt;
            weekly = optInt;
            monthly = optInt;
            yearly = optInt;
          };

          storageOptions = {
            backend = optStr;
            bucket = optStr;
            path = optStr;
            endpoint = optStr;
            region = optStr;
            key_id = optStr;
            key_secret = optStr;
            account = optStr;
            key = optStr;
            url = optStr;
            pass = optStr;
            file_mode = optStr;
            dir_mode = optStr;
            user = optStr;
            group = optStr;
            tls = lib.mkOption {
              type = lib.types.nullOr (lib.types.submodule {
                options = tlsOptions;
              });
              default = null;
            };
          };

          configData = removeNulls (
            (if cfg.defaults != { } then { defaults = cfg.defaults; } else { })
            // (if cfg.log != { } then { log = cfg.log; } else { })
            // (if cfg.profiles != { } then { profiles = cfg.profiles; } else { })
            // (if cfg.jobs != [ ] then { jobs = cfg.jobs; } else { })
            // mergedSettings
          );

          mergedSettings = cfg.settings // (if cfg.stateDir != null then
            { state = (cfg.settings.state or { }) // { dir = cfg.stateDir; }; }
          else { }) // (if cfg.tempDir != null then
            { temp_dir = cfg.tempDir; }
          else { });

          configFile = fmt.generate "db-backup.yml" configData;
        in {
          options.services.db-backup = {
            enable = lib.mkEnableOption "database backup scheduler";

            service.enable = lib.mkOption {
              type = lib.types.bool;
              default = true;
              description = "Enable the systemd service for db-backup";
            };

            package = lib.mkOption {
              type = lib.types.package;
              default = self.packages.${pkgs.system}.db-backup;
              description = "db-backup package to use";
            };

            manageConfig = lib.mkOption {
              type = lib.types.bool;
              default = true;
              description = "Generate configFile from the module options and keep the result in the Nix store. When false, configFile is treated as a regular filesystem path that you are responsible for writing.";
            };

            configFile = lib.mkOption {
              type = lib.types.str;
              default = toString configFile;
              defaultText = lib.literalExpression "the generated YAML in the Nix store";
              description = "Path to the YAML configuration file read by the scheduler. Set manageConfig=false and provide your own path to source hand written config instead.";
            };

            stateDir = lib.mkOption {
              type = lib.types.nullOr lib.types.str;
              default = "/var/lib/dbb";
              description = "Directory for runtime state (usage stats, instance_id, and eventual future features. If you use impermanence make sure this is added to your configuration.";
            };

            tempDir = lib.mkOption {
              type = lib.types.nullOr lib.types.str;
              default = null;
              description = "Spool directory for storage temp files (remote backends stage the full artifact here before upload). Unset falls back to $TMPDIR, then the OS temp directory.";
            };

            user = lib.mkOption {
              type = lib.types.str;
              default = "root";
              description = "User the scheduler runs as";
            };

            group = lib.mkOption {
              type = lib.types.str;
              default = cfg.user;
              description = "Group the scheduler runs as";
            };

            settings = lib.mkOption {
              type = lib.types.attrsOf lib.types.anything;
              default = { };
              description = "Raw top level YAML settings";
            };

            log = lib.mkOption {
              type = lib.types.submodule {
                options = {
                  level = optStr;
                  format = optStr;
                  type = optStr;
                  path = optStr;
                  user = optStr;
                  group = optStr;
                  colour = optBool;
                  prefix = optBool;
                  prefix_format = optStr;
                  session_id = optBool;
                  run_id = optBool;
                  timings = optBool;
                  size = optStr;
                  timezone = optStr;
                  utc = optBool;
                };
              };
              default = { };
              description = "Logging configuration";
            };

            defaults = lib.mkOption {
              type = lib.types.submodule {
                options = {
                  backup = lib.mkOption {
                    type = lib.types.nullOr (lib.types.submodule {
                      options = {
                        strategy = optStr;
                        type = optStr;
                        filename = optStr;
                        create_latest = optBool;
                        schema_only = optBool;
                        full_every = optInt;
                        full_after = optStr;
                      };
                    });
                    default = null;
                  };
                  compression = lib.mkOption {
                    type = lib.types.nullOr (lib.types.submodule {
                      options = {
                        type = optStr;
                        level = optInt;
                      };
                    });
                    default = null;
                  };
                  checksum = optStr;
                  storage = lib.mkOption {
                    type = lib.types.nullOr (lib.types.submodule { options = storageOptions; });
                    default = null;
                  };
                  tls = lib.mkOption {
                    type = lib.types.nullOr (lib.types.submodule { options = tlsOptions; });
                    default = null;
                  };
                  retention = lib.mkOption {
                    type = lib.types.nullOr (lib.types.submodule { options = retentionOptions; });
                    default = null;
                  };
                  archive = lib.mkOption {
                    type = lib.types.nullOr (lib.types.submodule {
                      options = {
                        last = optInt;
                        within = optStr;
                        path = optStr;
                        retention = lib.mkOption {
                          type = lib.types.nullOr (lib.types.submodule { options = retentionOptions; });
                          default = null;
                        };
                        # a storage profile name (string) or an inline storage block
                        storage = lib.mkOption {
                          type = lib.types.nullOr lib.types.anything;
                          default = null;
                        };
                      };
                    });
                    default = null;
                  };
                  hooks = lib.mkOption {
                    type = lib.types.nullOr (lib.types.submodule {
                      options = {
                        pre = optList;
                        post = optList;
                      };
                    });
                    default = null;
                  };
                };
              };
              default = { };
              description = "Default settings applied to every job (the 'defaults:' section of db-backup.yml).";
            };

            profiles = lib.mkOption {
              type = lib.types.attrsOf lib.types.anything;
              default = { };
              description = "Freeform profile definitions (connections, databases, backup, storage, encryption, maintenance, archive) under 'profiles:'";
            };

            jobs = lib.mkOption {
              type = lib.types.listOf lib.types.anything;
              default = [ ];
              description = "Freeform job list under 'jobs:'";
            };
          };

          config = lib.mkIf cfg.enable {
            environment.systemPackages = [ cfg.package ];

            system.activationScripts.db-backup-config = lib.mkIf (
              cfg.manageConfig
              && configData != { }
              && lib.hasPrefix "/etc/" cfg.configFile
            ) {
              deps = [ ];
              text = ''
                install -m 0600 -o ${cfg.user} -g ${cfg.group} ${configFile} ${cfg.configFile}
              '';
            };

            systemd.tmpfiles.rules =
              (lib.optional (cfg.stateDir != null)
                "d ${cfg.stateDir} 0700 ${cfg.user} ${cfg.group} - -")
              ++ (lib.optional (cfg.tempDir != null)
                "d ${cfg.tempDir} 0700 ${cfg.user} ${cfg.group} - -");

            systemd.services.db-backup = lib.mkIf cfg.service.enable {
              description = "DB Backup scheduler";
              wantedBy = [ "multi-user.target" ];
              after = [ "network.target" ];
              restartTriggers = [ cfg.package configFile ];
              serviceConfig = {
                Type = "simple";
                ExecStart = "${cfg.package}/bin/dbb -c ${cfg.configFile} scheduler";
                User = cfg.user;
                Group = cfg.group;
                Restart = "always";
                RestartSec = "10s";
                StandardOutput = "journal";
                StandardError = "journal";
                SyslogIdentifier = "db-backup";
              };
            };
          };
        };
    };
}
