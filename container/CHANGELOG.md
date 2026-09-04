## 5.0.0 2026-09-01 <code at nfrastack dot com>

   ### Added
      - Refactored to use `dbb` binary instead of old 3rd party tools
      - Additional environment variables to support new features, logging, etc
      - Automatic Configuration and Manual config toggle
      - `file://` and `env://` secret resolution

   ### Changed
      - dbb scheduler replaces bash routines

   ### Removed
      - Deprecated variable migration code (`DB_DUMP_*`, `ENABLE_CHECKSUM`, `EXTRA_DUMP_OPTS`)
      - All 3rd party binaries related to db engines, compression, encryption, storage.


## 4.9beta 2026-07-28 <code at nfrastack dot com>

   ### Changed
      - Port to nfrastack/base image
      - Alpine 3.24
      - Mysql 9.7.0


## 4.1.100 2026-03-13 <dave at tiredofit dot ca>

   ### Changed
      - Repush of 4.1.99


## 4.1.99 2026-03-12 <dave at tiredofit dot ca>

This the final tiredofit/db-backup release - All development and releases will reside under the nfrastack/container-db-backup namespace going forward.

   ### Added
      - Postgreaql 18 support
      - AWS CLI 1.44.56
      - MySQL 8.4.8
      - MSSQL 18.6.1-1


## 4.1.21 2025-08-08 <dave at tiredofit dot ca>

   ### Changed
      - Fix for 4.1.20 release inadvertently failing backups (credit tlex@github)


## 4.1.20 2025-07-23 <dave at tiredofit dot ca>

   ### Changed
      - Fix backup error checking routines #417 - credit alteriks@github


## 4.1.19 2025-05-28 <dave at tiredofit dot ca>

   ### Changed
      - Force overwrite manual scripts as opposed to append (#414)


## 4.1.18 2025-05-12 <dave at tiredofit dot ca>

   ### Changed
      - Fix MongoDB restore from not dropping DB each time before restore except explicitly told (credit logicoa@github)


## 4.1.17 2025-04-17 <dave at tiredofit dot ca>

   ### Changed
      - Fix issue with Postgres database cleanup when ALL databases being backed up as one file (SPLIT_DB=FALSE)


## 4.1.16 2025-02-21 <dave at tiredofit dot ca>

   ### Added
      - Update to tiredofit/alpine:7.10.28
      - Support TLS connectivity with restore script (credit fermion2020@github)


## 4.1.15 2025-01-29 <dave at tiredofit dot ca>

   ### Added
      - Add support for username and password support when checking for connectivity to couchdb (credit: JvSomeren)

   ### Changed
      - Fix issue with couchdb compression routines


## 4.1.14 2025-01-21 <dave at tiredofit dot ca>

   ### Changed
      - Downgrade AWS Client to 1.36.40 due to incompatibilities with providers with 1.37x. for time being


## 4.1.13 2025-01-21 <dave at tiredofit dot ca>

   ### Added
      - Update MySQL client to 8.4.4
      - Update AWS Client to 1.37.2

   ### Changed
      - Seperate MySQL and MariaDB TLS Configurationf for arguments that have deviated


## 4.1.12 2024-12-13 <dave at tiredofit dot ca>

   ### Changed
      - Fix for 4.1.11


## 4.1.11 2024-12-13 <dave at tiredofit dot ca>

   ### Changed
      - Fix when backing up 'ALL' databases with MariaDB


## 4.1.10 2024-12-12 <dave at tiredofit dot ca>

   ### Added
      - Use tiredofit/alpine:3.21-7.10.27 base
      - Use the actual binary name when dumping mariadb and mysql databases
      - Silence warnings that are appearing due to filenames, ssl warnings re MariaDB / MySQL


## 4.1.9 2024-11-07 <dave at tiredofit dot ca>

   ### Added
      - Pin to tiredofit/alpine:edge-7.10.19
      - MySQL 8.4.3 client
      - MSSQL and MSODBC 18.4.1.1-1
      - Mysql 11.x Support
      - Influx2 Client 2.7.5
      - AWS Client 1.35.13
      - Postgresql 17.x Support


## 4.1.8 2024-10-29 <dave at tiredofit dot ca>

Rebuild using 4.1.4 sources - ignore any versions of 4.1.5-4.1.7

   ### Added


## 4.1.4 2024-08-13 <dave at tiredofit dot ca>

Please note that if using encryption using a passphrase, you may be encountering issues with manual decryption. This release fixes that.
If you try to manually decrypt and your passphrase fails. Try wrapping it in single (') or double (") quotes.

   ### Changed
      - Fix for stray quotes appearing inside of ENCRYPT_PASSPHRASE variables


## 4.1.3 2024-07-05 <dave at tiredofit dot ca>

   ### Changed
      - Rebuild to support tiredofit/alpine:7.10.0


## 4.1.2 2024-07-02 <effectivelywild@github>

   ### Added
      - Add support for Azure Blob containers
      - Fix timestamps when comparing previous backups
      - Resolve unnecessary read operations in Azure
      - Resolve issues with backup cleanup operations in Azure


## 4.1.1 2024-06-19 <dave at tiredofit dot ca>

   ### Changed
      - Fix issue where postgresql globals when backing up ALL not being deleted (#352)


## 4.1.0 2024-05-25 <dave at tiredofit dot ca>

    Note that arm/v7 builds have been removed from this release going forward

   ### Added
      - Introduce DEFAULT/DBXX_MYSQL_CLIENT option to use mariadb or mysql for client dumping to solve incompatibility issues
      - Alpine 3.20 Base
      - MariaDB 10.11.8 Client
      - AWS Client 1.32.113
      - MySQL Client 8.4.0


## 4.0.35 2024-01-14 <dave at tiredofit dot ca>

   ### Changed
      - Fix issue with emaail notifications and not being able to add from statement


## 4.0.34 2024-01-02 <dave at tiredofit dot ca>

   ### Changed
      - Change the way architectures are detected to re-enable backups with MSSQL and Influx2


## 4.0.33 2023-12-18 <dave at tiredofit dot ca>

   ### Changed
      - Allow _OPTS variabls to contain spaces
      - Switch references of _DUMP_OPTS to _BACKUP_OPTS


## 4.0.32 2023-12-15 <dave at tiredofit dot ca>

   ### Changed
      - Fix issue with directories not properly being backed up (InfluxDB)


## 4.0.31 2023-12-12 <dave at tiredofit dot ca>

   ### Changed
      - Support backing up databases with spaces in them


## 4.0.30 2023-12-11 <dave at tiredofit dot ca>

   ### Added
      - Seperate each job with its own temporary folder for isolation and to better cleanup jobs that backup as a directory instead of a flat file


## 4.0.29 2023-12-04 <dave at tiredofit dot ca>

   ### Changed
      - Skip blobxfer if either account or key is not present


## 4.0.28 2023-12-04 <dave at tiredofit dot ca>

   ### Changed
      - AWS Cli 1.31.5
      - Switch to using PIP for installing AWS-Cli to remove deprecation warnings


## 4.0.27 2023-12-04 <dave at tiredofit dot ca>

   ### Changed
      - Switch to using actual crontab for cron expressions


## 4.0.26 2023-11-30 <dave at tiredofit dot ca>

   ### Added
      - AWS CLI 1.31.4


## 4.0.25 2023-11-29 <dave at tiredofit dot ca>

   ### Changed
      - Fix #297 - Add parameters to blobxfer to restore functionality


## 4.0.24 2023-11-28 <dave at tiredofit dot ca>

   ### Changed
      - Fix issue with cron parsing and 0 being a value getting clobbered by sort command


## 4.0.23 2023-11-28 <dave at tiredofit dot ca>

   ### Changed
      - Resolve issue with custom notification scripts not executing


## 4.0.22 2023-11-25 <dave at tiredofit dot ca>

   ### Changed
      - Move cleanup_old_data routines to happen within backup_ function to properly accomodate for globals, and ALL DB_NAME use cases


## 4.0.21 2023-11-22 <dave at tiredofit dot ca>

   ### Changed
      - Fix for SQLite backups not being cleaned up properly due to a malformed base


## 4.0.20 2023-11-21 <dave at tiredofit dot ca>

   ### Changed
      - Update base image to support S6 Overlay 3.1.6.2 to solve shutdown issues specifically with MODE=MANUAL and MANUAL_RUN_FOREVER=TRUE
      - Add some safety nets for Manual scheduling


## 4.0.19 2023-11-20 <dave at tiredofit dot ca>

   ### Changed
      - Make adjustments to cron scheduling feature to be able to handle whitespace properly"


## 4.0.18 2023-11-18 <joergmschulz@github>

   ### Changed
      - Fix loading msmtp configuration


## 4.0.17 2023-11-17 <dave at tiredofit dot ca>

   ### Changed
      - Provide more details when notifying via instant messages


## 4.0.16 2023-11-17 <dave at tiredofit dot ca>

   ### Changed
      - Switch to using msmtp instead of s-mail for notify()


## 4.0.15 2023-11-16 <dave at tiredofit dot ca>

   ### Changed
      - Fix cleanup of old backups


## 4.0.14 2023-11-13 <dave at tiredofit dot ca>

   ### Changed
      - Bugfix when PRE/POST scripts found not giving legacy warning
      - Run pre / post scripts as root


## 4.0.13 2023-11-12 <dave at tiredofit dot ca>

   ### Changed
      - Check for any quotes if using MONGO_CUSTOM_URI and remove


## 4.0.12 2023-11-12 <dave at tiredofit dot ca>

   ### Changed
      - Allow creating schedulers if _MONGO_CUSTOM_URI is set and _DB_HOST blank


## 4.0.11 2023-11-11 <dave at tiredofit dot ca>

   ### Changed
      - Resolve issue with backing up ALL databases with PGSQL and MySQL


## 4.0.10 2023-11-11 <dave at tiredofit dot ca>

   ### Changed
      - Change environment variable parsing routines to properly accomodate for Passwords containing '=='


## 4.0.9 2023-11-11 <dave at tiredofit dot ca>

   ### Changed
      - Fix issue with quotes being wrapped around _PASS variables


## 4.0.8 2023-11-11 <dave at tiredofit dot ca>

   ### Changed
      - Tidy up file_encryption() routines
      - Change environment variable _ENCRYPT_PUBKEY to _ENCRYPT_PUBLIC_KEY
      - Add new environment variable _ENCRYPT_PRIVATE_KEY


## 4.0.7 2023-11-11 <dave at tiredofit dot ca>

   ### Added
      - Add seperate permissions for _FILESYSTEM_PATH

   ### Changed
      - More output and debugging additions
      - SQLite3 now backs up without running into file permission/access problems
      - Cleanup old sqlite backups from temp directory
      - Handle multiple SQLite3 backups concurrently


## 4.0.6 2023-11-10 <dave at tiredofit dot ca>

   ### Added
      - Add additional DEBUG_ statements

   ### Changed
      - Fix issue with Influx DB not properly detecting the correct version


## 4.0.5 2023-11-10 <dave at tiredofit dot ca>

   ### Added
      - Add undocumented DBBACKUP_USER|GROUP environment variables for troubleshooting permissions
      - Add more verbosity when using DEBUG_ statements

   ### Changed
      - Change _FILESYSTEM_PERMISSION to 600 from 700


## 4.0.4 2023-11-09 <dave at tiredofit dot ca>

   ### Added
      - Add support for restoring from different DB_ variables in restore script


## 4.0.3 2023-11-09 <dave at tiredofit dot ca>

   ### Changed
      - Resolve issue with _MYSQL_TLS_CERT_FILE not being read


## 4.0.2 2023-11-09 <dave at tiredofit dot ca>

   ### Changed
      - Properly use custom _S3_HOST variables


## 4.0.1 2023-11-09 <dave at tiredofit dot ca>

   ### Changed
      - Restore - Stop using DB_DUMP_TARGET and instead browse using DEFAULT_BACKUP_PATH


## 4.0.0 2023-11-08 <dave at tiredofit dot ca>

This is the fourth major release to the DB Backup image which started as a basic MySQL backup service in early 2017. With each major release brings enhancements, bugfixes, removals along with breaking changes and this one is no different.

This release brings functionality requested by the community such as multiple host backup support by means of independent scheduler tasks,blackout periods, better resource usage, better security via file encryption, file permissions, and more verbosity via log files. , and also merges contributions from other developers.

Upgrading to this image should for the most part work for most users, but will involve event upgrading environment variables as the formathas changed significantly. Old variables should continue to work, however are unsupported and will be removed with the `4.3.0` release, whenever that will be.

A significant amount of development hours were put in to accomodate for feature requests by the community. If you are using this in a commercial setting or find this image valuable, please consider sponsoring my work for a period of time or engaging in a private support offering. More details at https://www.tiredofit.ca/sponsor

   ### Added

      - Backup Multiple Hosts in same image all with different options (scheduling, compression, destination, cleanup) (Use `DBXX_option` variables)
      - Backup limits how many backup jobs run concurrently
      - Backup Scheduling now allows using a timestamp (eg `Dec 12 2023 03:00:00`) - credit benvia@github
      - Backup Scheduling now allows using a cron expression (e.g `00 03 * * *`)
      - Backup Blackout period to skip backing up during a period of time
      - Backup runs as dedicated user (no longer root)
      - Backup can have specific file permissions set upon completion (eg `700` or `rwx------`)
      - Backups can run with reduced priority mode to allow for fair scheduling across system
      - Backups - MySQL/MariaDB now has ability to backup events
      - Backups - Microsoft SQL server now has option to backup transaction logs
      - Backups - Postgres now backs up globals  - credit oscarsiles@github
      - Backups with Azure synchronize storage before upload - credit eoehen@github
      - Encrypt backup file with a passphrase or a GPG Public Key ability
      - Log backup jobs to file along with log rotation
      - Notification support upon job failure via Email, Mattermost, Matrix, Rocketchat
      - Zabbix Metrics now auto discovers new jobs
      - Zabbix Metrics sends metrics relating to backed up filename, checksum hash, and the duration of backup/compression time, checksum time, encryption time
      - New Debug Capabilities

   ### Changed

      - Reworked Documentation
      - Reworked all functions and renamed all variables
      - Many variables now use a prefix of `DEFAULT_` to operate on all backup jobs
      - Can be overridden per backup job by setting `DB_<option>` or to unset default variable `DB_<option>=unset`
      - Renamed variables and terms
         - `_DUMP_LOCATION` -> `_BACKUP_LOCATION`
         - `_DUMP_BEGIN` -> `_BACKUP_BEGIN`
         - `_DUMP_FREQ` -> `_BACKUP_INTERVAL`
         - `_DUMP_TARGET`` -> `_FILESYSTEM_PATH`
         - `_DUMP_ARCHIVE`` -> `_FILESYSTEM_PATH`
         - `EXTRA_DUMP_OPTS`` -> `_EXTRA_BACKUP_OPTS`
         - `TEMP_LOCATION`` -> `TEMP_PATH`
      - Backups - AWS CLI updated to 1.29.78
      - Backups - InfluxDB 2 Client version updated to 2.7.3
      - Backups - Microsoft SQL server now compresses files post initial backup
      - Backups - Manual backups handle aborting gracefully
      - Checksum routines now complete in half the time
      - Checksum variable now supports "NONE"
      - Zabbix metrics sending occurs in one process as opposed to singular
      - Cleanup Only cleanup files that match same backup name pattern
      - Cleanup/Archive uses relative path instead of absolute with creating_latest_symlink
      - A handful of code optimizations and cleanup

   ### Removed
      - `ENABLE_CHECKSUM` - has been wrapped into `_CHECKSUM=none`


## 3.12.0 2023-10-29 <alwynpan@github>

   ### Added
      - Add DB_AUTH functionalith for Postgresql databases


## 3.12.0 2023-10-29 <alwynpan>

   ### Added
      - Add DB_AUTH environment for PGSQL


## 3.11.1 2023-10-23 <dave at tiredofit dot ca>

### Added

      - Switch to tiredofit/alpine:edge for newer packages
      - Postgresql 16 support

### Changed

      - Add --break-system-packages flag to pip when installing blobxfer

## 3.11.0 2023-10-12 <dave at tiredofit dot ca>

### Added

      - Introduce aarch64 (ARMv8) support for Microsoft SQL Server backups
      - Microsoft ODBC Driver 18.3.2.1-1
      - Microsoft SQL Client 18.3.1.1-1

## 3.10.5 2023-10-11 <dave at tiredofit dot ca>

### Added

      - Add option to drop existing data from MongoDB restore

### Changed

      - Fix some capabilities of not being able to select mongodb manually to restore

## 3.10.4 2023-10-11 <thomas-negrault@github>

### Changed

      - Use authentication database for MongoDB restores

## 3.10.3 2023-10-11 <thomas-negrault@github>

### Changed

      - Change sorting for restore script

## 3.10.2 2023-09-14 <pimjansen@github>

### Changed

      - Update to wording when sending files to blobxfer
      - Remove --databases flag when backing up a single mysql/mariadb backup which allows to omit the "USE <db_name>" statement in the backup allowing for better restores

## 3.10.1 2023-09-13 <dave at tiredofit dot ca>

### Changed

      - Bugfix to 3.10.0 with syntax error revolving around unbraced variable

## 3.10.0 2023-09-13 <jcdirks@github>

### Added

      - Add EXTRA_DUMP_OPTS and EXTRA_ENUMERATION_OPTS to add different arguments when checking for databases, vs doing the actual backup

## 3.9.12 2023-09-04 <dave at tiredofit dot ca>

### Changed

      - Perform additional checks for ENABLE_CHECKSUM=FALSE and skip executing actions for S3/BlobXfer

## 3.9.11 2023-08-24 <dave at tiredofit dot ca>

### Changed

      - AWS CLI 2.13.9

## 3.9.10 2023-08-23 <dave at tiredofit dot ca>

### Changed

      - Stop trying to move a non existent checksum file when ENABLE_CHECKSUM=FALSE

## 3.9.9 2023-08-21 <dave at tiredofit dot ca>

### Changed

      - Start compiling aws-cli instead of from packages to continue to support arm/v7

## 3.9.8 2023-08-20 <ToshY@github>

### Changed

      - Restore armv7 and aarch64 builds

## 3.9.7 2023-07-18 <dave at tiredofit dot ca>

### Changed

      - Cleanup check_exit_code parameter and reduce duplicate output

## 3.9.6 2023-06-16 <dave at tiredofit dot ca>

### Changed

      - Resolve issues introduced with 3.9.3. Split exit codes to be specific for backing up and moving. Uses parameter $11 for post backup scripts

## 3.9.5 2023-06-13 <dave at tiredofit dot ca>

### Changed

      - Start building Influx DB v1 manually due to being removed from Alpine repositories

## 3.9.4 2023-06-13 <dave at tiredofit dot ca>

### Added

      - Add ability to use --rsyncable argument to zstd archives

## 3.9.3 2023-06-05 <dave at tiredofit dot ca>

### Added

      - Add notification if blobxfer/s3 upload fails (credit @alwynpan)
      - Add zip package

## 3.9.2 2023-05-10 <dave at tiredofit dot ca>

### Changed

      - Alpine 3.18 base

## 3.9.1 2023-05-03 <dave at tiredofit dot ca>

### Changed

      - Properly allow multiple _FILE environment variables to execute solving an issue with MySQL backups
      - Fix _FILE functionality for DB_NAME variable

## 3.9.0 2023-04-26 <dave at tiredofit dot ca>

### Added

      - Add support for _FILE environment variables

## 3.8.5 2023-04-11 <tpansino@github>

### Changed

      - Fix SQLite3, Influx, and MSSQL backups failing due to malformed/non existent ltarget

## 3.8.4 2023-04-06 <dave at tiredofit dot ca>

### Changed

      - Fix issue with Influx2 and MSSQL clients not installing properly

## 3.8.3 2023-03-30 <dave at tiredofit dot ca>

### Changed

      - Patchup for 3.8.2

## 3.8.2 2023-03-30 <dave at tiredofit dot ca>

### Changed

      - Patchup for 3.8.1

## 3.8.1 2023-03-30 <dave at tiredofit dot ca>

### Changed

      - Cleanup Dockerfile
      - Fix issue with DB_ARCHIVE_TIME not firing correctly

## 3.8.0 2023-03-27 <dave at tiredofit dot ca>

### Added

      - Introduce DB_DUMP_TARGET_ARCHIVE which works with DB_ARCHIVE_TIME to move backups older than (x) minutes from DB_DUMP_TARGET to DB_DUMP_TARGET_ARCHIVE for use with external backup systems and custom exclude rules
      - Introduce CREATE_LATEST_SYMLINK which creates a symbolic link in DB_DUMP_TARGET of `latest-(DB_TYPE)-(DB_NAME)-(DB_HOST)`

## 3.7.7 2023-03-20 <codemonium@github>

### Changed

      - Simplify pg_isready usage

## 3.7.6 2023-03-14 <toshy@github>

### Changed

      - Remove EXTRA_OPT variable from MySQL/MariaDB check

## 3.7.5 2023-03-02 <dave at tiredofit dot ca>

### Added

      - Add support for Docker Swarm mode Secrets for BLOBXFER_STORAGE_ACCOUNT_*_FILE

## 3.7.4 2023-02-22 <gbe0@github>

### Changed

      - Fix when running in MANUAL_RUN_FOREVER mode looping

## 3.7.3 2022-12-20 <dave at tiredofit dot ca>

### Changed

      - Make S3_KEY_ID and S3_KEY_SECRET optional should IAM roles be used (Credit to alwynpan@github)

## 3.7.2 2022-12-19 <dave at tiredofit dot ca>

### Changed

      - Bugfix for 3.7.1

## 3.7.1 2022-12-19 <dave at tiredofit dot ca>

### Changed

      - Add MYSQL_ENABLE_TLS environment variable to switch on and off

### Reverted

      - Set default for MYSQL_TLS_CA_FILE to accomodate for most use cases

## 3.7.0 2022-12-16 <dave at tiredofit dot ca>

### Added

      - Introduce support for connecting via TLS to MySQL / MariaDB Hosts with MYSQL_TLS_* variables - See README for more details

### Changed

      - Fix for cleaning up file systems that are syncing to Azure via blobxfer

## 3.6.1 2022-11-23 <dave at tiredofit dot ca>

### Added

      - Switch to Alpine 3.17 base
      - Switch to OpenSSL instead of LibreSSL

## 3.6.0 2022-11-21 <dave at tiredofit dot ca>

### Added

      - Postgresql 15 Support

## 3.5.6 2022-11-15 <dave at tiredofit dot ca>

### Changed

      - Add failure if DB_TYPE empty or malformed

## 3.5.5 2022-10-18 <dave at tiredofit dot ca>

### Changed

      - Fix for S3 backups and trailing slashes (@greena13)

## 3.5.4 2022-10-13 <dave at tiredofit dot ca>

### Changed

      - Fix for Influx DB 1 backups when compression enabled

## 3.5.3 2022-10-12 <dave at tiredofit dot ca>

### Changed

      - Remove build dependencies for blobxfer making image size smaller
      - Remove silencing commands limiting Postgres backups from working without DEBUG_MODE=TRUE

## 3.5.2 2022-10-11 <dave at tiredofit dot ca>

### Added

      - Update Influxdb client to 2.4.0 (jauderho@github)

## 3.5.1 2022-10-11 <dave at tiredofit dot ca>

### Changed

      - Tighten up cleanup routines to not call blobxfer unless absolutely necessary

## 3.5.0 2022-10-10 <dave at tiredofit dot ca>

### Added

      - Blobxfer / Microsoft Azure Support (credit: eoehen@github)
      - Introduce MONGO_CUSTOM_URI environment variable for those not wanting to use DB_* variables

### Changed

      - Force filenames to be in lowercase
      - Fix S3 Database cleanups (credit greenatwork@github)
      - Remove MONGO_DB_TYPE variable as MONGO_CUSTOM_URI overrides
      - Fix MSSQL Backups (credit eoehen@github)
      - Seperate examples for MySQL and MSSQL

## 3.4.2 2022-09-19 <dave at tiredofit dot ca>

### Changed

      - Skip availability check for Mongo Atlas connections

## 3.4.1 2022-09-13 <dave at tiredofit dot ca>

### Added

- Introduce environment variables for SCRIPT_LOCATION_POST and SCRIPT_LOCATION_PRE for better separation

### Deprecated

- Introduce deprecation warning for the custom script paths `/assets/custom-scripts` and `/assets/custom-scripts/pre`. These
  paths will continue to work for now but support may be removed in the next major version release. To support the new
  default paths your scripts should be moved as follows:

  | Script Type | Old Path (Deprecated)        | New Environment Variable | Environment Value Default |
  | ----------- | ---------------------------- | ------------------------ | ------------------------- |
  | Pre         | `/assets/custom-scripts/pre` | SCRIPT_LOCATION_PRE      | `/assets/scripts/pre`     |
  | Post        | `/assets/custom-scripts`     | SCRIPT_LOCATION_POST     | `/assets/scripts/post`    |

## 3.4.0 2022-09-12 <dave at tiredofit dot ca>

### Added

      - Add GZ_RSYNCABLE environment variable for better rsync compatibility (Credit teun95@github)
      - Add Pre Backup Script Support
      - Add MongoDB Atlas Support

### Changed

      - Fix Default Port for Influx 2 DB Hosts

## 3.3.12 2022-08-15 <dave at tiredofit dot ca>

### Changed

      - arifer612@github contributed a fix for incorrect case of "filesize" variable when using post backup scripts

## 3.3.11 2022-07-22 <khoazero123@github>

### Fixed

      - Restore script not properly detecting postgres backups

## 3.3.10 2022-07-19 <dave at tiredofit dot ca>

### Changed

      - Remove MSSQL install packages properly

## 3.3.9 2022-07-09 <fardeau@github>

### Fixed

      - Remaining work on interactive mode for entering port on restore script

## 3.3.8 2022-07-09 <dave at tiredofit dot ca>

### Added

      - MSSQL Client Tools 18.0.1.1-1

## 3.3.7 2022-06-23 <dave at tiredofit dot ca>

### Changed

      - Allow overrides to actually override with the restore script

## 3.3.6 2022-06-23 <dave at tiredofit dot ca>

### Changed

      - Fix for restore script when using all 7 arguments

## 3.3.5 2022-06-08 <dave at tiredofit dot ca>

### Changed

      - Fix DB Port parameter not being able to be input in restore script
      - Fix MongoDB restore questionnaire

## 3.3.4 2022-06-03 <rozdzynski@github>

### Fixed

      - S3 backups failing with special characters in filename

## 3.3.3 2022-05-24 <dave at tiredofit dot ca>

### Added

      - Alpine 3.16 base

## 3.3.2 2022-05-02 <dave at tiredofit dot ca>

### Added

      - Add POST_SCRIPT_SKIP_X_VERIFY environment variables to allow for more host compatibility for post scripts

## 3.3.1 2022-04-30 <dave at tiredofit dot ca>

### Changed

      - Compressing silently was causing 0 byte backups

## 3.3.0 2022-04-30 <dave at tiredofit dot ca>

### Added

      - Ability to auto clean old S3 / Minio Hosts like what occurs on filesystem
      - Alert user how to turn off Zabbix Monitoring if fails
      - Allow Zabbix Monitoring to work with S3
      - Silence some more compression statements

### Changed

      - Fix for Redis not backing up properly
      - Start sending checksums for S3 Outputs
      - Cleanup some code functions
      - FIx Container Log Level always in DEBUG

## 3.2.4 2022-04-21 <dave at tiredofit dot ca>

### Changed

      - Add -portable flag when backing up Influx

## 3.2.3 2022-04-21 <dave at tiredofit dot ca>

### Changed

      - Fix for bucket / db name InfluxDB 1.xx
      - Minor aesthetics, spacing, spelling

## 3.2.2 2022-04-21 <dave at tiredofit dot ca>

### Changed

      - Restore script properly parses DB_PORT entry
      - Influx Database ready performs different checks dependent on V1/V2
      - Stop using weird database lowercase variables unnecessarily breaking Docker Secrets

## 3.2.1 2022-04-03 <dave at tiredofit dot ca>

### Changed

      - Fix a variety of issues iwth 3.2.0 relating to InfluxDB

## 3.2.0 2022-03-31 <dave at tiredofit dot ca>

### Added

      - Install InfluxDB2 Client alongside version 1 (amd64 and arm64)
      - Introduce InfluxDB 2 backup support
      - Introduce multiple compression types other than Gzip for Influx 1/2
      - Introduce compression for MSSQL backups

### Changed

      - Testing for Host availability for CouchDB and InfluxDB

## 3.1.3 2022-03-30 <dave at tiredofit dot ca>

### Changed

      - Fix for MariaDB not sending database name to post script
      - Check for executible bit on post scripts both via environment variable and /assets/custom
      - SPLIT_DB defaulted to TRUE

## 3.1.2 2022-03-29 <dave at tiredofit dot ca>

### Changed

      - Fix for blank Notice when individual backup is completed (time taken)

## 3.1.1 2022-03-28 <dave at tiredofit dot ca>

### Changed

      - Resolve some issues with backups of Mongo and others not saving the proper timestamp

## 3.1.0 2022-03-23 <dave at tiredofit dot ca>

### Added

      - Backup multiple databases by separating with comma eg db1,db2
      - Backup ALL databases bu setting DB_NAME to ALL
      - Exclude databases from being backed up comma separated when DB_NAME is all eg DB_NAME_EXCLUDE=db3,db4
      - Backup timers execute per database, not per the whole script run
      - Post scripts run after each database backup
      - Checksum does not occur when database backup failed
      - Database cleanup does not occur when any databases backups fail throughout session
      - MongoDB now supported with 'restore' script - Credit to piemonkey@github
      - Lots of reshuffling, optimizations with script due to botched 3.0 release

### Changed

      - ZSTD replaces GZ as default compression type
      - Output is cleaner when backups are occurring

## 3.0.16 2022-03-23 <dave at tiredofit dot ca>

### Changed

      - Fix for SPLIT_DB not looping through all database names properly

## 3.0.15 2022-03-22 <dave at tiredofit dot ca>

### Changed

      - Rework compression function
      - Fix for Bzip compression failing

## 3.0.14 2022-03-22 <dave at tiredofit dot ca>

### Changed

      - Rearrange Notice stating when next backup is going to start

## 3.0.13 2022-03-21 <dave at tiredofit dot ca>

### Added

      - Add compression levels to debug mode

## 3.0.12 2022-03-21 <dave at tiredofit dot ca>

### Added

      - Throw Errors for MANUAL mode when certain other CONTAINER_* services are enabled

## 3.0.11 2022-03-21 <dave at tiredofit dot ca>

### Changed

      - Fix for Parallel Compression

## 3.0.10 2022-03-21 <dave at tiredofit dot ca>

### Changed

      - Fix for restore script not taking "custom" usernames or passwords

## 3.0.9 2022-03-21 <dave at tiredofit dot ca>

### Changed

      - Switch to using parallel versions of compression tools all the time, yet explicitly state the threads in use (1 or ++)

## 3.0.8 2022-03-21 <dave at tiredofit dot ca>

### Added

      - Add PARALLEL_COMPRESSION_THREADS environment variable to limit amount of threads when compressing - Currently auto detects however many processors are available to the container

## 3.0.7 2022-03-21 <dave at tiredofit dot ca>

### Reverted

      - Strip unused LOG directives

## 3.0.6 2022-03-21 <dave at tiredofit dot ca>

### Changed

      - Fix for parallel compression

## 3.0.5 2022-03-21 <dave at tiredofit dot ca>

### Changed

      - Add more detail regarding manual modes

## 3.0.4 2022-03-21 <dave at tiredofit dot ca>

### Changed

      - Fix for 3.0.3

## 3.0.3 2022-03-21 <dave at tiredofit dot ca>

### Changed

      - Add documentation for Manual mode
      - Revert Compression variables

## 3.0.2 2022-03-18 <dave at tiredofit dot ca>

### Changed

      - Cleanup of Zabbix Agent options
      - Updated Zabbix template
      - Split apart S3 options for better debugging and also cleaned up their variables
      - Fixed issue with post scripts not outputting proper backup start time
      - Cleaned up some notifications
      - Rearranged code

## 3.0.1 2022-03-17 <dave at tiredofit dot ca>

### Changed

      - Fix for Environment variable not reading correctly for restore script for DB TYPE

## 3.0.0 2022-03-17 <dave at tiredofit dot ca>

### Added

      - Rewrote entire image
      - Ability to choose which file hash after backup (MD5 or SHA1)
      - Restore Script (execute 'restore' in container)
      - Allow to map custom CA certs for S3 backups
      - Allow to skip certificate certification for S3 Backups
      - Revamped Logging and parameters - File logs also exist in /var/log/container/container.log
      - Added more functionality to send to Zabbix to track start, end, duration and status
      - Ability to backup stored procedures for MySQL / MariaDB
      - Ability to backup as a single transaction for MySQL / MariaDB
      - Ability to execute "manually" and still allow container to execute to accommodate for Kubernetes cron usage

### Changed

      - Environment variables have changed! Specifically relating to COMPRESSION, PARALLEL COMPRESSION, CHECKSUMs

## 2.12.0 2022-03-16 <dave at tiredofit dot ca>

### Changed

      - Last release of 2.x series
      - Fix timer for backups that take excessively long time, and allow it to start repetitively at the same time daily. What was happening is that if a backup took 30 minutes, time would shift by 30 minutes daily eventually taking backups mid day.

## 2.11.5 2022-03-15 <dave at tiredofit dot ca>

### Added

      - Add additional debug statements

## 2.11.4 2022-03-15 <dave at tiredofit dot ca>

### Added

      - Add debug statement around the scheduling component

## 2.11.3 2022-02-09 <dave at tiredofit dot ca>

### Changed

      - Rework to support new base image

## 2.11.2 2022-02-09 <dave at tiredofit dot ca>

### Changed

      - Refresh base image

## 2.11.1 2022-01-20 <jacksgt@github>

### Changed

      - Modernized S3 variables and sanity checks
      - Change exit code to 0 when executing a manual backup

## 2.11.0 2022-01-20 <dave at tiredofit dot ca>

### Added

      - Add capability to select `TEMP_LOCATION` for initial backup and compression before backup completes to avoid filling system memory

### Changed

      - Cleanup for MariaDB/MySQL DB ready routines that half worked in 2.10.3
      - Code cleanup

## 2.10.3 2022-01-07 <dave at tiredofit dot ca>

### Changed

      - Change the way MariaD/MySQL connectivity check is performed to allow for better compatibility without requiring the DB_USER to have PROCESS privileges

## 2.10.2 2021-12-28 <dave at tiredofit dot ca>

### Changed

      - Remove logrotate configuration for redis which shouldn't exist in the first place

## 2.10.1 2021-12-22 <milenkara@github>

### Added

     - Allow for choosing region when backing up to S3

## 2.10.0 2021-12-22 <dave at tiredofit dot ca>

### Changed

      - Revert back to Postgresql 14 from packages as its now in the repositories
      - Fix for Zabbix Monitoring

## 2.9.7 2021-12-15 <dave at tiredofit dot ca>

### Changed

      - Fixup for Zabbix Autoagent registration

## 2.9.6 2021-12-03 <alexbarcello@githuba>

### Changed

      - Fix for S3 Minio backup targets
      - Fix for annoying output on certain target time print conditions

## 2.9.5 2021-12-07 <dave at tiredofit dot ca>

### Changed

      - Fix for 2.9.3

## 2.9.4 2021-12-07 <dave at tiredofit dot ca>

### Added

      - Add Zabbix auto register support for templates

## 2.9.3 2021-11-24 <dave at tiredofit dot ca>

### Added

      - Alpine 3.15 base

## 2.9.2 2021-10-22 <teenigma@github>

### Fixed

      - Fix compression failing on Redis backup

## 2.9.1 2021-10-15 <sbrunecker@github>

### Fixed

      - Allow MariaDB 8.0 servers to be backed up
      - Fixed DB available check getting stuck with empty password

## 2.9.0 2021-10-15 <dave at tiredofit dot ca>

### Added

      - Postgresql 14 Support (compiled)
      - MSSQL 17.8.1.1

## 2.8.2 2021-10-15 <dave at tiredofit dot ca>

### Changed

      - Change to using aws cli from Alpine repositories (fixes #81)

## 2.8.1 2021-09-01 <dave at tiredofit dot ca>

### Changed

      - Modernize image with updated environment variables from upstream

## 2.8.0 2021-08-27 <dave at tiredofit dot ca>

### Added

      - Alpine 3.14 Base

### Changed

      - Fix for syntax error in 2.7.0 Release (Credit the1ts@github)
      - Cleanup image and leftover cache with AWS CLI installation

## 2.7.0 2021-06-17 <dave at tiredofit dot ca>

### Added

      - MongoDB Authentication Database support (DB_AUTH)

## 2.6.1 2021-06-08 <jwillmer@github>

### Changed

      - Fix for Issue #14 - SPLIT_DB=TRUE was not working for Postgres DB server

## 2.6.0 2021-02-19 <tpansino@github>

### Added

      - SQLite support

## 2.5.1 2021-02-14 <dave at tiredofit dot ca>

### Changed

      - Fix xz backups with `PARALLEL_COMPRESION=TRUE`

## 2.5.0 2021-01-25 <dave at tiredofit dot ca>

### Added

      - Multi Platform Build Variants (ARMv7 AMD64 AArch64)

### Changed

      - Alpine 3.13 Base
      - Compile Pixz as opposed to relying on testing repository
      - MSSQL Support only available under AMD64. Container exits if any other platform detected when MSSQL set to be backed up.

## 2.4.0 2020-12-07 <dave at tiredofit dot ca>

### Added

      - Switch back to packges for Postgresql (now 13.1)

## 2.3.2 2020-11-14 <dave at tiredofit dot ca>

### Changed

      - Reapply S6-Overlay into filesystem as Postgresql build is removing S6 files due to edge containing S6 overlay

## 2.3.1 2020-11-11 <bambi73@github>

### Fixed

      - Multiple Influx DB's not being backed up correctly

## 2.3.0 2020-10-15 <dave at tiredofit dot ca>

### Added

      - Microsoft SQL Server support (experimental)

### Changed

      - Compiled Postgresql 13 from source to backup psql/13 hosts

## 2.2.2 2020-09-22 <tpansino@github>

### Fixed

      - Patch for 2.2.0 release fixing Docker Secrets Support. Was skipping password check.

## 2.2.1 2020-09-17 <alwynpan@github>

### Fixed

      - On-demand/Manual backup with `backup-now` was throwing errors not being able to find a proper date

## 2.2.0 2020-09-14 <alwynpan@github>

### Fixed

      - Allow to use MariaDB and MongoDBs with no username and password while still allowing Docker Secrets
      - Changed source of Alpine package repositories

## 2.1.1 2020-09-01 <zicklag@github>

### Fixed

      - Add eval to POST_SCRIPT execution

## 2.1.0 2020-08-29 <dave at tiredofit dot ca>

### Added

      - Add Exit Code variable to be used for custom scripts - See README.md for placement
      - Add POST_SCRIPT environment variable to execute command instead of relying on custom script

## 2.0.0 2020-06-17 <dave at tiredofit dot ca>

### Added

      - Reworked compression routines to remove dependency on temporary files
      - Changed the way that MongoDB compression works - only supports GZ going forward

### Changed

      - Code cleanup (removed function prefixes, added verbosity)

### Reverted

      - Removed Rethink Support

## 1.23.0 2020-06-15 <dave at tiredofit dot ca>

### Added

      - Add zstd compression support
      - Add choice of compression level

## 1.22.0 2020-06-10 <dave at tiredofit dot ca>

### Added

      - Added EXTRA_OPTS variable to all backup commands to pass extra arguments

## 1.21.3 2020-06-10 <dave at tiredofit dot ca>

### Changed

      - Fix `backup-now` manual script due to services.available change

## 1.21.2 2020-06-08 <dave at tiredofit dot ca>

### Added

      - Change to support tiredofit/alpine base image 5.0.0

## 1.21.1 2020-06-04 <dave at tiredofit dot ca>

### Changed

      - Bugfix to initialization routine

## 1.21.0 2020-06-03 <dave at tiredofit dot ca>

### Added

      - Add S3 Compatible Storage Support

### Changed

      - Switch some variables to support tiredofit/alpine base image better
      - Fix issue with parallel compression not working correctly

## 1.20.1 2020-04-24 <dave at tiredofit dot ca>

### Changed

      - Fix Auto Cleanup routines when using `root` as username

## 1.20.0 2020-04-22 <dave at tiredofit dot ca>

### Added

      - Docker Secrets Support for DB_USER and DB_PASS variables

## 1.19.0 2020-04-22 <dave at tiredofit dot ca>

### Added

      - Custom Script support to execute upon completion of backup

## 1.18.2 2020-04-08 <hyun007 @ github>

### Changed

      - Rework to allow passwords with spaces in them for MariaDB / MySQL

## 1.18.1 2020-03-14 <dave at tiredofit dot ca>

### Changed

      - Allow for passwords with spaces in them for MariaDB / MySQL

## 1.18.0 2019-12-29 <dave at tiredofit dot ca>

### Added

      - Update image to support new tiredofit/alpine base images

## 1.17.3 2019-12-12 <dave at tiredofit dot ca>

### Changed

      - Quiet down Zabbix Agent

## 1.17.2 2019-12-12 <dave at tiredofit dot ca>

### Changed

      - Re Enable ZABBIX

## 1.17.1 2019-12-10 <dave at tiredofit dot ca>

### Changed

      - Fix spelling mistake in container initialization

## 1.17.0 2019-12-09 <dave at tiredofit dot ca>

### Changed

      - Stop compiling mongodb-tools as it is back in Alpine:edge repositories
      - Cleanup Code

## 1.16 - 2019-06-16 - <dave at tiredofit dot ca>

- Check to see if Database Exists before performing backup
- Fix for MysQL/MariaDB custom ports - Credit to <spumer@github>

## 1.15 - 2019-05-24 - <claudioaltamura @ github>

- Added abaility to backup password protected Redis Hosts

## 1.14 - 2019-04-20 - <dave at tiredofit dot ca>

- Switch to using locally built mongodb-tools from tiredofit/mongo-builder due to Alpine removing precompiled packages from repositories

## 1.13 - 2019-03-09 - <dave at tiredofit dot ca>

- Fixed Postgres backup without SPLIT_DB enabled (credit MelwinKfr@github)
- Added DB_PORT reference to properly backup Postgres with non default ports (thanks Maxximus007@github)

## 1.12 - 2019-03-01 - <stevetodorov at github>

- Fix for XZ Compression failing

## 1.11 - 2018-11-19 - <skylord123 at github>

- Fix for Urnary Operator Error

## 1.10 - 2018-11-19 - <dave at tiredofit dot ca>

- Fix for InfluxDB for backing up and supporting DB_PORT variable - Thanks skylord123@github

## 1.9 - 2018-11-03 - <dave at tiredofit dot ca>

- Switch from OpenSSL to LibreSSL

## 1.8 - 2018-07-18 - <dave at tiredofit dot ca>

- Fix warnings on startup related to 1.7 Changes

## 1.7 - 2018-06-06 - <dave at tiredofit dot ca>

- Added ability for Manual Backup (enter container and type `backup-now`)

## 1.6 - 2018-02-26 - <dave at tiredofit dot ca>

- Add Parallel Compression mode (Default TRUE

## 1.5 - 2018-01-28 - <dave at tiredofit dot ca>

- Add Zabbix Checks

## 1.4 - 2017-11-17 - <dave at tiredofit dot ca>

- Switch to Packages Postgres

## 1.31 - 2017-11-17 - <dave at tiredofit dot ca>

- Fix to SPLIT_DB Postgresql Backup

## 1.3 - 2017-10-25 - <dave at tiredofit dot ca>

- Remove Alpine postgres package and recompile version 10

## 1.2 - 2017-10-19 - <dave at tiredofit dot ca>

- Syntax Error Fix
- Fix some environment variables for Postgres and Redis

## 1.1 - 2017-09-14 - <dave at tiredofit dot ca>

- Added CouchDB

## 1.0 - 2017-09-14 - <dave at tiredofit dot ca>

- Initial Release
- Alpine:Edge
