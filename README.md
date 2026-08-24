# nfrastack/db-backup

## About

Backup multiple types of database servers on a scheduled basis with many customizable options.

* CouchDB, InfluxDB, MySQL/MariaDB, Microsoft SQL, MongoDB, Postgres, Redis, SQLite3 support
* Dump to local filesystem or backup to S3 Compatible services, and Azure
* Multiple backup job support (DB01, DB02, DB03) - Up to DB99 for supporters. 
* Flexible scheduling (interval, cron, HHMM, datetime)
* Blackout periods to skip backups during certain hours
* Per-job credentials, storage targets, compression, encryption
* Checksum support (MD5, SHA1)
* Compression support (none, gzip, bzip2, xz, zstd)
* GPG encryption (passphrase or public key)
* Notification on failure (email, Matrix, Mattermost, Rocket.Chat, custom script)
* Zabbix monitoring metrics
* Pre/post backup hook scripts
* Interactive and CLI restore tool

## Maintainer

- [Nfrastack](https://www.nfrastack.com)

## Table of Contents

- [About](#about)
- [Maintainer](#maintainer)
- [Table of Contents](#table-of-contents)
- [Prerequisites and Assumptions](#prerequisites-and-assumptions)
- [Installation](#installation)
  - [Build from Source](#build-from-source)
  - [Prebuilt Images](#prebuilt-images)
    - [Multi Architecture](#multi-architecture)
- [Configuration](#configuration)
  - [Quick Start](#quick-start)
  - [Persistent Storage](#persistent-storage)
  - [Environment Variables](#environment-variables)
    - [Base Images used](#base-images-used)
    - [Container Options](#container-options)
    - [Job Defaults](#job-defaults)
      - [Compression Options](#compression-options)
      - [Encryption Options](#encryption-options)
      - [Scheduling Options](#scheduling-options)
      - [Default Database Options](#default-database-options)
        - [CouchDB](#couchdb)
        - [InfluxDB](#influxdb)
        - [MariaDB/MySQL](#mariadbmysql)
        - [Microsoft SQL](#microsoft-sql)
        - [MongoDB](#mongodb)
        - [Postgresql](#postgresql)
        - [Redis](#redis)
      - [Default Storage Options](#default-storage-options)
        - [Filesystem](#filesystem)
        - [S3](#s3)
        - [Azure](#azure)
      - [Hooks](#hooks)
        - [Path Options](#path-options)
        - [Pre Backup](#pre-backup)
        - [Post backup](#post-backup)
    - [Job Backup Options](#job-backup-options)
      - [Compression Options](#compression-options-1)
      - [Encryption Options](#encryption-options-1)
      - [Scheduling Options](#scheduling-options-1)
      - [Specific Database Options](#specific-database-options)
        - [CouchDB](#couchdb-1)
        - [InfluxDB](#influxdb-1)
        - [MariaDB/MySQL](#mariadbmysql-1)
        - [Microsoft SQL](#microsoft-sql-1)
        - [MongoDB](#mongodb-1)
        - [Postgresql](#postgresql-1)
        - [Redis](#redis-1)
        - [SQLite](#sqlite)
      - [Specific Storage Options](#specific-storage-options)
        - [Filesystem](#filesystem-1)
        - [S3](#s3-1)
        - [Azure](#azure-1)
      - [Hooks](#hooks-1)
        - [Path Options](#path-options-1)
        - [Pre Backup](#pre-backup-1)
        - [Post backup](#post-backup-1)
    - [Notifications](#notifications)
      - [Custom Notifications](#custom-notifications)
      - [Email Notifications](#email-notifications)
      - [Matrix Notifications](#matrix-notifications)
      - [Mattermost Notifications](#mattermost-notifications)
      - [Rocketchat Notifications](#rocketchat-notifications)
- [Maintenance](#maintenance)
  - [Shell Access](#shell-access)
  - [Manual Backups](#manual-backups)
  - [Restoring Databases](#restoring-databases)
- [Support](#support)
  - [Usage](#usage)
  - [Bugfixes](#bugfixes)
  - [Feature Requests](#feature-requests)
  - [Updates](#updates)
- [License](#license)

## Prerequisites and Assumptions

- You must have a working connection to one of the supported DB Servers and appropriate credentials

## Installation

### Build from Source

This image relies on a customized base image in order to work.

```bash
docker build \
  --build-arg BASE_IMAGE=ghcr.io/nfrastack/container-base:latest \
  -t container-db-backup:test \
  .
```

### Prebuilt Images

Feature limited builds of the image are available on the [Github Container Registry](https://github.com/nfrastack/container-db-backup/pkgs/container/container-db-backup) and [Docker Hub](https://hub.docker.com/r/nfrastack/db-backup).

To unlock advanced features, one must provide a code to be able to change specific environment variables from defaults. Support the development to gain access to a code.

To get access to the image use your container orchestrator to pull from the following locations:

```
ghcr.io/nfrastack/container-db-backup:(image_tag)
docker.io/nfrastack/db-backup:(image_tag)
```

The following image tags are available:

| Base      | Tag       |
| --------- | --------- |
| Alpine    | `:latest` |

#### Multi Architecture

Images are built for `amd64` by default, with optional support for `arm64` and other architectures.

## Configuration

### Quick Start

- The quickest way to get started is using [docker-compose](https://docs.docker.com/compose/). See the examples folder for a series of example compose.yml that can be modified for development or production use.

- Set various [environment variables](#environment-variables) to understand the capabilities of this image.
- Map [persistent storage](#persistent-storage) for access to configuration and data files for backup.

### Persistent Storage

The following directories are used for configuration and can be mapped for persistent storage.

| Directory              | Description                                                                         |
| ---------------------- | ----------------------------------------------------------------------------------- |
| `/backup`              | Backups                                                                             |
| `/assets/scripts/pre`  | _Optional_ Put custom scripts in this directory to execute before backup operations |
| `/assets/scripts/post` | _Optional_ Put custom scripts in this directory to execute after backup operations  |
| `/logs`                | _Optional_ Logfiles for backup jobs                                                 |

### Environment Variables

#### Base Images used

This image relies on a customized base image in order to work.
Be sure to view the following repositories to understand all the customizable options:

| Image                                                   | Description |
| ------------------------------------------------------- | ----------- |
| [OS Base](https://github.com/nfrastack/container-base/) | Base Image  |

Below is the complete list of available options that can be used to customize your installation.

- Variables showing an `x` under the `Adv.` column can only be set if the containers advanced functionality is enabled.

#### Container Options

| Parameter                | Description                                                                                                                      | Default         |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------- | --------------- |
| `MODE`                   | `AUTO` mode to use internal scheduling routines or `MANUAL` to simply use this as manual backups only executed by your own means | `AUTO`          |
| `USER_DBBACKUP`          | The uid that the image should read and write files as (username is `dbbackup`)                                                   | `10000`         |
| `GROUP_DBBACKUP`         | The gid that the image should read and write files as (groupname is `dbbackup`)                                                  | `10000`         |
| `LOG_PATH`               | Path to log files                                                                                                                | `/logs`         |
| `TEMP_PATH`              | Perform Backups and Compression in this temporary directory                                                                      | `/tmp/backups/` |
| `MANUAL_RUN_FOREVER`     | `TRUE` or `FALSE` if you wish to try to make the container exit after the backup                                                 | `TRUE`          |
| `DEBUG_MODE`             | If set to `true`, print copious shell script messages to the container log. Otherwise only basic messages are printed.           | `FALSE`         |
| `BACKUP_JOB_CONCURRENCY` | How many backup jobs to run concurrently                                                                                         | `1`             |

#### Job Defaults

If these are set and no other defaults or variables are set explicitly, they will be added to any of the backup jobs.

| Variable                          | Description                                                                           | Default      |
| --------------------------------- | ------------------------------------------------------------------------------------- | ------------ |
| `DEFAULT_BACKUP_LOCATION`         | Backup to `FILESYSTEM`, `blobxfer` or `S3` compatible services like S3, Minio, Wasabi | `FILESYSTEM` |
| `DEFAULT_CHECKSUM`                | Either `MD5` or `SHA1` or `NONE`                                                      | `MD5`        |
| `DEFAULT_LOG_LEVEL`               | Log output on screen and in files `INFO` `NOTICE` `ERROR` `WARN` `DEBUG`              | `notice`     |
| `DEFAULT_RESOURCE_OPTIMIZED`      | Perform operations at a lower priority to the CPU and IO scheduler                    | `FALSE`      |
| `DEFAULT_SKIP_AVAILABILITY_CHECK` | Before backing up - skip connectivity check                                           | `FALSE`      |

##### Compression Options

| Variable                               | Description                                                                                    | Default        |
| -------------------------------------- | ---------------------------------------------------------------------------------------------- | -------------- |
| `DEFAULT_COMPRESSION`                  | Use either Gzip `GZ`, Bzip2 `BZ`, XZip `XZ`, ZSTD `ZSTD` or none `NONE`                        | `ZSTD`         |
| `DEFAULT_COMPRESSION_LEVEL`            | Numerical value of what level of compression to use, most allow `1` to `9`                     | `3`            |
|                                        | except for `ZSTD` which allows for `1` to `19`                                                 |                |
| `DEFAULT_GZ_RSYNCABLE`                 | Use `--rsyncable` (gzip only) for faster rsync transfers and incremental backup deduplication. | `FALSE`        |
| `DEFAULT_ENABLE_PARALLEL_COMPRESSION`  | Use multiple cores when compressing backups `TRUE` or `FALSE`                                  | `TRUE`         |
| `DEFAULT_PARALLEL_COMPRESSION_THREADS` | Maximum amount of threads to use when compressing - Integer value e.g. `8`                     | `autodetected` |

##### Encryption Options

Encryption occurs after compression and the encrypted filename will have a `.gpg` suffix

| Variable                      | Description                                  | Default | `_FILE` |
| ----------------------------- | -------------------------------------------- | ------- | ------- |
| `DEFAULT_ENCRYPT`             | Encrypt file after backing up with GPG       | `FALSE` |         |
| `DEFAULT_ENCRYPT_PASSPHRASE`  | Passphrase to encrypt file with GPG          |         | x       |
| _or_                          |                                              |         |         |
| `DEFAULT_ENCRYPT_PUBLIC_KEY`  | Path of public key to encrypt file with GPG  |         | x       |
| `DEFAULT_ENCRYPT_PRIVATE_KEY` | Path of private key to encrypt file with GPG |         | x       |

##### Scheduling Options

| Variable                        | Description                                                                                                                                    | Default |
| ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| `DEFAULT_BACKUP_INTERVAL`       | How often to do a backup, in minutes after the first backup. Defaults to 1440 minutes, or once per day.                                        | `1440`  |
| `DEFAULT_BACKUP_BEGIN`          | What time to do the initial backup. Defaults to immediate. (`+0`)                                                                              | `+0`    |
|                                 | Must be in one of four formats:                                                                                                                |         |
|                                 | Absolute HHMM, e.g. `2330` or `0415`                                                                                                           |         |
|                                 | Relative +MM, i.e. how many minutes after starting the container, e.g. `+0` (immediate), `+10` (in 10 minutes), or `+90` in an hour and a half |         |
|                                 | Full datestamp e.g. `2023-12-21 23:30:00`                                                                                                      |         |
|                                 | Cron expression e.g. `30 23 * * *` [Understand the format](https://en.wikipedia.org/wiki/Cron) - _BACKUP_INTERVAL is ignored_                  |         |
| `DEFAULT_CLEANUP_TIME`          | Value in minutes to delete old backups (only fired when backup interval executes)                                                              | `FALSE` |
|                                 | 1440 would delete anything above 1 day old. You don't need to set this variable if you want to hold onto everything.                           |         |
| `DEFAULT_ARCHIVE_TIME`          | Value in minutes to move all files older than (x) from `DEFAULT_FILESYSTEM_PATH` to `DEFAULT_FILESYSTEM_ARCHIVE_PATH`                          |         |
| `DEFAULT_BLACKOUT_BEGIN`        | Use `HHMM` notation to start a blackout period where no backups occur eg `0420`                                                                |         |
| `DEFAULT_BLACKOUT_END`          | Use `HHMM` notation to set the end period where no backups occur eg `0430`                                                                     |         |

> You may need to wrap your `DEFAULT_BACKUP_BEGIN` value in quotes for it to properly parse. There have been reports of backups that start with a `0` get converted into a different format which will not allow the timer to start at the correct time.

##### Default Database Options

###### CouchDB

| Variable       | Description  | Default | `_FILE` |
| -------------- | ------------ | ------- | ------- |
| `DEFAULT_PORT` | CouchDB Port | `5984`  | x       |

###### InfluxDB

| Variable                 | Description                                                                                             | Default | `_FILE` |
| ------------------------ | ------------------------------------------------------------------------------------------------------- | ------- | ------- |
| `DEFAULT_PORT`           | InfluxDB Port                                                                                           |         | x       |
|                          | Version 1.x                                                                                             | `8088`  |         |
|                          | Version 2.x                                                                                             | `8086`  |         |
| `DEFAULT_INFLUX_VERSION` | What Version of Influx are you backing up from `1`.x or `2` series - amd64 and aarch/armv8 only for `2` | `2`     |         |

###### MariaDB/MySQL

| Variable                           | Description                                                                                               | Default                   | `_FILE` |
| ---------------------------------- | --------------------------------------------------------------------------------------------------------- | ------------------------- | ------- |
| `DEFAULT_PORT`                     | MySQL / MariaDB Port                                                                                      | `3306`                    | x       |
| `DEFAULT_EXTRA_BACKUP_OPTS`        | Pass extra arguments to the backup command only, add them here e.g. `--extra-command`                     |                           |         |
| `DEFAULT_EXTRA_ENUMERATION_OPTS`   | Pass extra arguments to the database enumeration command only, add them here e.g. `--extra-command`       |                           |         |
| `DEFAULT_EXTRA_OPTS`               | Pass extra arguments to the backup and database enumeration command, add them here e.g. `--extra-command` |                           |         |
| `DEFAULT_MYSQL_CLIENT`             | Choose between `mariadb` or `mysql` client to perform dump operations for compatibility purposes          | `mariadb`                 |         |
| `DEFAULT_MYSQL_EVENTS`             | Backup Events                                                                                             | `TRUE`                    |         |
| `DEFAULT_MYSQL_MAX_ALLOWED_PACKET` | Max allowed packet                                                                                        | `512M`                    |         |
| `DEFAULT_MYSQL_SINGLE_TRANSACTION` | Backup in a single transaction                                                                            | `TRUE`                    |         |
| `DEFAULT_MYSQL_STORED_PROCEDURES`  | Backup stored procedures                                                                                  | `TRUE`                    |         |
| `DEFAULT_MYSQL_ENABLE_TLS`         | Enable TLS functionality                                                                                  | `FALSE`                   |         |
| `DEFAULT_MYSQL_TLS_VERIFY`         | (optional) If using TLS (by means of MYSQL_TLS_* variables) verify remote host                            | `FALSE`                   |         |
| `DEFAULT_MYSQL_TLS_VERSION`        | What TLS `v1.1` `v1.2` `v1.3` version to utilize                                                          | `TLSv1.1,TLSv1.2,TLSv1.3` |         |
| `DEFAULT_MYSQL_TLS_CA_FILE`        | Filename to load custom CA certificate for connecting via TLS                                             | `/etc/ssl/cert.pem`       | x       |
| `DEFAULT_MYSQL_TLS_CERT_FILE`      | Filename to load client certificate for connecting via TLS                                                |                           | x       |
| `DEFAULT_MYSQL_TLS_KEY_FILE`       | Filename to load client key for connecting via TLS                                                        |                           | x       |

###### Microsoft SQL

| Variable             | Description                             | Default    | `_FILE` |
| -------------------- | --------------------------------------- | ---------- | ------- |
| `DEFAULT_PORT`       | Microsoft SQL Port                      | `1433`     | x       |
| `DEFAULT_MSSQL_MODE` | Backup `DATABASE` or `TRANSACTION` logs | `DATABASE` |         |

###### MongoDB

| Variable           | Description                                                                                                                          | Default | `_FILE` |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------ | ------- | ------- |
| `DEFAULT_AUTH`     | (Optional) Authentication Database                                                                                                   |         | x       |
| `DEFAULT_PORT`     | MongoDB Port                                                                                                                         | `27017` | x       |
| `MONGO_CUSTOM_URI` | If you wish to override the MongoDB Connection string enter it here e.g. `mongodb+srv://username:password@cluster.id.mongodb.net`    |         | x       |
|                    | This environment variable will be parsed and populate the `DB_NAME` and `DB_HOST` variables to properly build your backup filenames. |         |         |
|                    | You can override them by making your own entries                                                                                     |         |         |

###### Postgresql

| Variable                         | Description                                                                                               | Default | `_FILE` |
| -------------------------------- | --------------------------------------------------------------------------------------------------------- | ------- | ------- |
| `DEFAULT_AUTH`                   | (Optional) Authentication Database                                                                        |         | x       |
| `DEFAULT_BACKUP_GLOBALS`         | Backup Globals as part of backup procedure                                                                |         |         |
| `DEFAULT_EXTRA_BACKUP_OPTS`      | Pass extra arguments to the backup command only, add them here e.g. `--extra-command`                     |         |         |
| `DEFAULT_EXTRA_ENUMERATION_OPTS` | Pass extra arguments to the database enumeration command only, add them here e.g. `--extra-command`       |         |         |
| `DEFAULT_EXTRA_OPTS`             | Pass extra arguments to the backup and database enumeration command, add them here e.g. `--extra-command` |         |         |
| `DEFAULT_PORT`                   | PostgreSQL Port                                                                                           | `5432`  | x       |

###### Redis

| Variable                         | Description                                                                                         | Default | `_FILE` |
| -------------------------------- | --------------------------------------------------------------------------------------------------- | ------- | ------- |
| `DEFAULT_PORT`                   | Default Redis Port                                                                                  | `6379`  | x       |
| `DEFAULT_EXTRA_ENUMERATION_OPTS` | Pass extra arguments to the database enumeration command only, add them here e.g. `--extra-command` |         |         |

##### Default Storage Options

Options that are related to the value of `DEFAULT_BACKUP_LOCATION`

###### Filesystem

If `DEFAULT_BACKUP_LOCATION` = `FILESYSTEM` then the following options are used:

| Variable                             | Description                                                                                           | Default                               |
| ------------------------------------ | ----------------------------------------------------------------------------------------------------- | ------------------------------------- |
| `DEFAULT_CREATE_LATEST_SYMLINK`      | Create a symbolic link pointing to last backup in this format: `latest-(DB_TYPE)_(DB_NAME)_(DB_HOST)` | `TRUE`                                |
| `DEFAULT_FILESYSTEM_PATH`            | Directory where the database dumps are kept.                                                          | `/backup`                             |
| `DEFAULT_FILESYSTEM_PATH_PERMISSION` | Permissions to apply to backup directory                                                              | `700`                                 |
| `DEFAULT_FILESYSTEM_ARCHIVE_PATH`    | Optional Directory where the database dumps archives are kept                                         | `${DEFAULT_FILESYSTEM_PATH}/archive/` |
| `DEFAULT_FILESYSTEM_PERMISSION`      | Permissions to apply to files.                                                                        | `600`                                 |

###### S3

If `DEFAULT_BACKUP_LOCATION` = `S3` then the following options are used:

| Parameter                     | Description                                                                               | Default | `_FILE` |
| ----------------------------- | ----------------------------------------------------------------------------------------- | ------- | ------- |
| `DEFAULT_S3_BUCKET`           | S3 Bucket name e.g. `mybucket`                                                            |         | x       |
| `DEFAULT_S3_KEY_ID`           | S3 Key ID (Optional)                                                                      |         | x       |
| `DEFAULT_S3_KEY_SECRET`       | S3 Key Secret (Optional)                                                                  |         | x       |
| `DEFAULT_S3_PATH`             | S3 Pathname to save to (must NOT end in a trailing slash e.g. '`backup`')                 |         | x       |
| `DEFAULT_S3_REGION`           | Define region in which bucket is defined. Example: `ap-northeast-2`                       |         | x       |
| `DEFAULT_S3_HOST`             | Hostname (and port) of S3-compatible service, e.g. `minio:8080`. Defaults to AWS.         |         | x       |
| `DEFAULT_S3_PROTOCOL`         | Protocol to connect to `DEFAULT_S3_HOST`. Either `http` or `https`. Defaults to `https`.  | `https` | x       |
| `DEFAULT_S3_EXTRA_OPTS`       | Add any extra options to the end of the `aws-cli` process execution                       |         | x       |
| `DEFAULT_S3_CERT_CA_FILE`     | Map a volume and point to your custom CA Bundle for verification e.g. `/certs/bundle.pem` |         | x       |
| _*OR*_                        |                                                                                           |         |         |
| `DEFAULT_S3_CERT_SKIP_VERIFY` | Skip verifying self signed certificates when connecting                                   | `TRUE`  |         |

- When `DEFAULT_S3_KEY_ID` and/or `DEFAULT_S3_KEY_SECRET` is not set, will try to use IAM role assigned (if any) for uploading the backup files to S3 bucket.

###### Azure

If `DEFAULT_BACKUP_LOCATION` = `blobxfer` then the following options are used:

| Parameter                              | Description                                                         | Default             | `_FILE` |
| -------------------------------------- | ------------------------------------------------------------------- | ------------------- | ------- |
| `DEFAULT_BLOBXFER_STORAGE_ACCOUNT`     | Microsoft Azure Cloud storage account name.                         |                     | x       |
| `DEFAULT_BLOBXFER_STORAGE_ACCOUNT_KEY` | Microsoft Azure Cloud storage account key.                          |                     | x       |
| `DEFAULT_BLOBXFER_REMOTE_PATH`         | Remote Azure path                                                   | `/docker-db-backup` | x       |
| `DEFAULT_BLOBXFER_MODE`                | Azure Storage mode e.g. `auto`, `file`, `append`, `block` or `page` | `auto`              | x       |

- When `DEFAULT_BLOBXFER_MODE` is set to auto it will use blob containers by default. If the `DEFAULT_BLOBXFER_REMOTE_PATH` path does not exist a blob container with that name will be created.

> This service uploads files from backup target directory `DEFAULT_FILESYSTEM_PATH`.
> If a cleanup configuration in `DEFAULT_CLEANUP_TIME` is defined, the remote directory on Azure storage will also be cleaned automatically.

##### Hooks

###### Path Options

| Parameter                      | Description                                                                 | Default                 |
| ------------------------------ | --------------------------------------------------------------------------- | ----------------------- |
| `DEFAULT_SCRIPT_LOCATION_PRE`  | Location on filesystem inside container to execute bash scripts pre backup  | `/assets/scripts/pre/`  |
| `DEFAULT_SCRIPT_LOCATION_POST` | Location on filesystem inside container to execute bash scripts post backup | `/assets/scripts/post/` |
| `DEFAULT_PRE_SCRIPT`           | Fill this variable in with a command to execute pre backing up              |                         |
| `DEFAULT_POST_SCRIPT`          | Fill this variable in with a command to execute post backing up             |                         |

###### Pre Backup

If you want to execute a custom script before a backup starts, you can drop bash scripts with the extension of `.sh` in the location defined in `DB01_SCRIPT_LOCATION_PRE`. See the following example to utilize:

```bash
$ cat pre-script.sh
##!/bin/bash

# #### Example Pre Script
# #### $1=DBXX_TYPE (Type of Backup)
# #### $2=DBXX_HOST (Backup Host)
# #### $3=DBXX_NAME (Name of Database backed up)
# #### $4=BACKUP START TIME (Seconds since Epoch)
# #### $5=BACKUP FILENAME (Filename)

echo "${1} Backup Starting on ${2} for ${3} at ${4}. Filename: ${5}"
```

```text
## script DBXX_TYPE DBXX_HOST DBXX_NAME STARTEPOCH BACKUP_FILENAME
${f} "${backup_job_db_type}" "${backup_job_db_host}" "${backup_job_db_name}" "${backup_routines_start_time}" "${backup_job_file}"
```

Outputs the following on the console:

`mysql Backup Starting on example-db for example at 1647370800. Filename: mysql_example_example-db_202200315-000000.sql.bz2`

###### Post backup

If you want to execute a custom script at the end of a backup, you can drop bash scripts with the extension of `.sh` in the location defined in `DB01_SCRIPT_LOCATION_POST`. Also to support legacy users `/assets/custom-scripts` is also scanned and executed. See the following example to utilize:

```bash
$ cat post-script.sh
##!/bin/bash

# #### Example Post Script
# #### $1=EXIT_CODE (After running backup routine)
# #### $2=DBXX_TYPE (Type of Backup)
# #### $3=DBXX_HOST (Backup Host)
# #### $4=DBXX_NAME (Name of Database backed up)
# #### $5=BACKUP START TIME (Seconds since Epoch)
# #### $6=BACKUP FINISH TIME (Seconds since Epoch)
# #### $7=BACKUP TOTAL TIME (Seconds between Start and Finish)
# #### $8=BACKUP FILENAME (Filename)
# #### $9=BACKUP FILESIZE
# #### $10=HASH (If CHECKSUM enabled)
# #### $11=MOVE_EXIT_CODE

echo "${1} ${2} Backup Completed on ${3} for ${4} on ${5} ending ${6} for a duration of ${7} seconds. Filename: ${8} Size: ${9} bytes MD5: ${10}"
```

```text
## script EXIT_CODE DB_TYPE DB_HOST DB_NAME STARTEPOCH FINISHEPOCH DURATIONEPOCH BACKUP_FILENAME FILESIZE CHECKSUMVALUE
${f} "${exit_code}" "${dbtype}" "${backup_job_db_host}" "${backup_job_db_name}" "${backup_routines_start_time}" "${backup_routines_finish_time}" "${backup_routines_total_time}" "${backup_job_file}" "${filesize}" "${checksum_value}" "${move_exit_code}
```

Outputs the following on the console:

`0 mysql Backup Completed on example-db for example on 1647370800 ending 1647370920 for a duration of 120 seconds. Filename: mysql_example_example-db_202200315-000000.sql.bz2 Size: 7795 bytes Hash: 952fbaafa30437494fdf3989a662cd40 0`

If you wish to change the size value from bytes to megabytes set environment variable `DB01_SIZE_VALUE=megabytes`

You must make your scripts executable otherwise there is an internal check that will skip trying to run it otherwise.
If for some reason your filesystem or host is not detecting it right, use the environment variable `DB01_POST_SCRIPT_SKIP_X_VERIFY=TRUE` to bypass.

#### Job Backup Options

If `DEFAULT_` variables are set and you do not wish for the settings to carry over into your jobs, you can set the appropriate environment variable with the value of `unset`.
Otherwise, override them per backup job. Additional backup jobs can be scheduled by using `DB02_`,`DB03_`,`DB04_` ... prefixes. 

A limit of 3 can be created when not in advanced mode. 


| Parameter   | Description                                                                                    | Default | `_FILE` | Adv. |
| ----------- | ---------------------------------------------------------------------------------------------- | ------- | ------- | ---- |
| `DB01_TYPE` | Type of DB Server to backup `couch` `influx` `mysql` `mssql` `pgsql` `mongo` `redis` `sqlite3` |         |         |      |
| `DB01_HOST` | Server Hostname e.g. `mariadb`. For `sqlite3`, full path to DB file e.g. `/backup/db.sqlite3`  |         | x       |      |
| `DB01_NAME` | Schema Name e.g. `database`                                                                    |         | x       |      |
| `DB01_USER` | username for the database(s) - Can use `root` for MySQL                                        |         | x       |      |
| `DB01_PASS` | (optional if DB doesn't require it) password for the database                                  |         | x       |      | |

| Variable                       | Description                                                                                               | Default      |
| ------------------------------ | --------------------------------------------------------------------------------------------------------- | ------------ |
| `DB01_BACKUP_LOCATION`         | Backup to `FILESYSTEM`, `blobxfer` or `S3` compatible services like S3, Minio, Wasabi                     | `FILESYSTEM` |
| `DB01_CHECKSUM`                | Either `MD5` or `SHA1` or `NONE`                                                                          | `MD5`        |
| `DB01_EXTRA_BACKUP_OPTS`       | Pass extra arguments to the backup command only, add them here e.g. `--extra-command`                     |              |
| `DB01_EXTRA_ENUMERATION_OPTS`  | Pass extra arguments to the database enumeration command only, add them here e.g. `--extra-command`       |              |
| `DB01_EXTRA_OPTS`              | Pass extra arguments to the backup and database enumeration command, add them here e.g. `--extra-command` |              |
| `DB01_LOG_LEVEL`               | Log output on screen and in files `INFO` `NOTICE` `ERROR` `WARN` `DEBUG`                                  | `debug`      |
| `DB01_RESOURCE_OPTIMIZED`      | Perform operations at a lower priority to the CPU and IO scheduler                                        | `FALSE`      |
| `DB01_SKIP_AVAILABILITY_CHECK` | Before backing up - skip connectivity check                                                               | `FALSE`      |

##### Compression Options

| Variable                            | Description                                                                                    | Default        |
| ----------------------------------- | ---------------------------------------------------------------------------------------------- | -------------- |
| `DB01_COMPRESSION`                  | Use either Gzip `GZ`, Bzip2 `BZ`, XZip `XZ`, ZSTD `ZSTD` or none `NONE`                        | `ZSTD`         |
| `DB01_COMPRESSION_LEVEL`            | Numerical value of what level of compression to use, most allow `1` to `9`                     | `3`            |
|                                     | except for `ZSTD` which allows for `1` to `19`                                                 |                |
| `DB01_GZ_RSYNCABLE`                 | Use `--rsyncable` (gzip only) for faster rsync transfers and incremental backup deduplication. | `FALSE`        |
| `DB01_ENABLE_PARALLEL_COMPRESSION`  | Use multiple cores when compressing backups `TRUE` or `FALSE`                                  | `TRUE`         |
| `DB01_PARALLEL_COMPRESSION_THREADS` | Maximum amount of threads to use when compressing - Integer value e.g. `8`                     | `autodetected` |

##### Encryption Options

Encryption will occur after compression and the resulting filename will have a `.gpg` suffix

| Variable                   | Description                                  | Default | `_FILE` |
| -------------------------- | -------------------------------------------- | ------- | ------- |
| `DB01_ENCRYPT`             | Encrypt file after backing up with GPG       | `FALSE` |         |
| `DB01_ENCRYPT_PASSPHRASE`  | Passphrase to encrypt file with GPG          |         | x       |
| _or_                       |                                              |         |         |
| `DB01_ENCRYPT_PUBLIC_KEY`  | Path of public key to encrypt file with GPG  |         | x       |
| `DB01_ENCRYPT_PRIVATE_KEY` | Path of private key to encrypt file with GPG |         | x       |

##### Scheduling Options

| Variable                     | Description                                                                                                                                    | Default |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| `DB01_BACKUP_INTERVAL`       | How often to do a backup, in minutes after the first backup. Defaults to 1440 minutes, or once per day.                                        | `1440`  |
| `DB01_BACKUP_BEGIN`          | What time to do the initial backup. Defaults to immediate. (`+0`)                                                                              | `+0`    |
|                              | Must be in one of four formats:                                                                                                                |         |
|                              | Absolute HHMM, e.g. `2330` or `0415`                                                                                                           |         |
|                              | Relative +MM, i.e. how many minutes after starting the container, e.g. `+0` (immediate), `+10` (in 10 minutes), or `+90` in an hour and a half |         |
|                              | Full datestamp e.g. `2023-12-21 23:30:00`                                                                                                      |         |
|                              | Cron expression e.g. `30 23 * * *`  [Understand the format](https://en.wikipedia.org/wiki/Cron) - _BACKUP_INTERVAL is ignored_                 |         |
| `DB01_CLEANUP_TIME`          | Value in minutes to delete old backups (only fired when backup interval executes)                                                              | `FALSE` |
|                              | 1440 would delete anything above 1 day old. You don't need to set this variable if you want to hold onto everything.                           |         |
| `DB01_ARCHIVE_TIME`          | Value in minutes to move all files older than (x) from `DB01_BACKUP_FILESYSTEM_PATH`                                                           |         |
|                              | to `DB01_BACKUP_FILESYSTEM_ARCHIVE_PATH` - which is useful when pairing against an external backup system.                                     |         |
| `DB01_BLACKOUT_BEGIN`        | Use `HHMM` notation to start a blackout period where no backups occur eg `0420`                                                                |         |
| `DB01_BLACKOUT_END`          | Use `HHMM` notation to set the end period where no backups occur eg `0430`                                                                     |         |

##### Specific Database Options

###### CouchDB

| Variable    | Description  | Default | `_FILE` |
| ----------- | ------------ | ------- | ------- |
| `DB01_PORT` | CouchDB Port | `5984`  | x       |

###### InfluxDB

| Variable              | Description                                                                                             | Default | `_FILE` |
| --------------------- | ------------------------------------------------------------------------------------------------------- | ------- | ------- |
| `DB01_PORT`           | InfluxDB Port                                                                                           |         | x       |
|                       | Version 1.x                                                                                             | `8088`  |         |
|                       | Version 2.x                                                                                             | `8086`  |         |
| `DB01_INFLUX_VERSION` | What Version of Influx are you backing up from `1`.x or `2` series - amd64 and aarch/armv8 only for `2` | `2`     |         |

> Your Organization will be mapped to `DB_USER` and your root token will need to be mapped to `DB_PASS`.
> You may use `DB_NAME=ALL` to backup the entire set of databases.
> For `DB_HOST` use syntax of `http(s)://db-name`

###### MariaDB/MySQL

| Variable                        | Description                                                                                               | Default                   | `_FILE` |
| ------------------------------- | --------------------------------------------------------------------------------------------------------- | ------------------------- | ------- |
| `DB01_EXTRA_OPTS`               | Pass extra arguments to the backup and database enumeration command, add them here e.g. `--extra-command` |                           |         |
| `DB01_EXTRA_BACKUP_OPTS`        | Pass extra arguments to the backup command only, add them here e.g. `--extra-command`                     |                           |         |
| `DB01_EXTRA_ENUMERATION_OPTS`   | Pass extra arguments to the database enumeration command only, add them here e.g. `--extra-command`       |                           |         |
| `DB01_NAME`                     | Schema Name e.g. `database` or `ALL` to backup all databases the user has access to.                      |                           |         |
|                                 | Backup multiple by separating with commas eg `db1,db2`                                                    |                           | x       |
| `DB01_NAME_EXCLUDE`             | If using `ALL` - use this to exclude databases separated via commas from being backed up                  |                           | x       |
| `DB01_SPLIT_DB`                 | If using `ALL` - use this to split each database into its own file as opposed to one singular file        | `FALSE`                   |         |
| `DB01_PORT`                     | MySQL / MariaDB Port                                                                                      | `3306`                    | x       |
| `DB01_MYSQL_EVENTS`             | Backup Events for                                                                                         | `TRUE`                    |         |
| `DB01_MYSQL_MAX_ALLOWED_PACKET` | Max allowed packet                                                                                        | `512M`                    |         |
| `DB01_MYSQL_SINGLE_TRANSACTION` | Backup in a single transaction                                                                            | `TRUE`                    |         |
| `DB01_MYSQL_STORED_PROCEDURES`  | Backup stored procedures                                                                                  | `TRUE`                    |         |
| `DB01_MYSQL_ENABLE_TLS`         | Enable TLS functionality                                                                                  | `FALSE`                   |         |
| `DB01_MYSQL_TLS_VERIFY`         | (optional) If using TLS (by means of MYSQL_TLS_* variables) verify remote host                            | `FALSE`                   |         |
| `DB01_MYSQL_TLS_VERSION`        | What TLS `v1.1` `v1.2` `v1.3` version to utilize                                                          | `TLSv1.1,TLSv1.2,TLSv1.3` |         |
| `DB01_MYSQL_TLS_CA_FILE`        | Filename to load custom CA certificate for connecting via TLS                                             | `/etc/ssl/cert.pem`       | x       |
| `DB01_MYSQL_TLS_CERT_FILE`      | Filename to load client certificate for connecting via TLS                                                |                           | x       |
| `DB01_MYSQL_TLS_KEY_FILE`       | Filename to load client key for connecting via TLS                                                        |                           | x       |

###### Microsoft SQL

| Variable          | Description                             | Default    | `_FILE` |
| ----------------- | --------------------------------------- | ---------- | ------- |
| `DB01_PORT`       | Microsoft SQL Port                      | `1433`     | x       |
| `DB01_MSSQL_MODE` | Backup `DATABASE` or `TRANSACTION` logs | `DATABASE` |         |

###### MongoDB

| Variable                | Description                                                                                                                          | Default | `_FILE` |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------ | ------- | ------- |
| `DB01_AUTH`             | (Optional) Authentication Database                                                                                                   |         |         |
| `DB01_PORT`             | MongoDB Port                                                                                                                         | `27017` | x       |
| `DB01_MONGO_CUSTOM_URI` | If you wish to override the MongoDB Connection string enter it here e.g. `mongodb+srv://username:password@cluster.id.mongodb.net`    |         | x       |
|                         | This environment variable will be parsed and populate the `DB_NAME` and `DB_HOST` variables to properly build your backup filenames. |         |         |
|                         | You can override them by making your own entries                                                                                     |         |         |

###### Postgresql

| Variable                      | Description                                                                                               | Default | `_FILE` |
| ----------------------------- | --------------------------------------------------------------------------------------------------------- | ------- | ------- |
| `DB01_AUTH`                   | (Optional) Authentication Database                                                                        |         |         |
| `DB01_BACKUP_GLOBALS`         | Backup Globals after backing up database (forces `TRUE` if `_NAME=ALL`)                                   | `FALSE` |         |
| `DB01_EXTRA_OPTS`             | Pass extra arguments to the backup and database enumeration command, add them here e.g. `--extra-command` |         |         |
| `DB01_EXTRA_BACKUP_OPTS`      | Pass extra arguments to the backup command only, add them here e.g. `--extra-command`                     |         |         |
| `DB01_EXTRA_ENUMERATION_OPTS` | Pass extra arguments to the database enumeration command only, add them here e.g. `--extra-command`       |         |         |
| `DB01_NAME`                   | Schema Name e.g. `database` or `ALL` to backup all databases the user has access to.                      |         |         |
|                               | Backup multiple by separating with commas eg `db1,db2`                                                    |         | x       |
| `DB01_SPLIT_DB`               | If using `ALL` - use this to split each database into its own file as opposed to one singular file        | `FALSE` |         |
| `DB01_PORT`                   | PostgreSQL Port                                                                                           | `5432`  | x       |

###### Redis

| Variable                 | Description                                                                                               | Default | `_FILE` |
| ------------------------ | --------------------------------------------------------------------------------------------------------- | ------- | ------- |
| `DB01_EXTRA_OPTS`        | Pass extra arguments to the backup and database enumeration command, add them here e.g. `--extra-command` |         |         |
| `DB01_EXTRA_BACKUP_OPTS` | Pass extra arguments to the backup command only, add them here e.g. `--extra-command`                     |         |         |
| `DB01_PORT`              | Redis Port                                                                                                | `6379`  | x       |

###### SQLite

| Variable    | Description                                              | Default | `_FILE` |
| ----------- | -------------------------------------------------------- | ------- | ------- |
| `DB01_HOST` | Enter the full path to DB file e.g. `/backup/db.sqlite3` |         | x       |

##### Specific Storage Options

Options that are related to the value of `DB01_BACKUP_LOCATION`

###### Filesystem

If `DB01_BACKUP_LOCATION` = `FILESYSTEM` then the following options are used:

| Variable                          | Description                                                                                           | Default                            |
| --------------------------------- | ----------------------------------------------------------------------------------------------------- | ---------------------------------- |
| `DB01_CREATE_LATEST_SYMLINK`      | Create a symbolic link pointing to last backup in this format: `latest-(DB_TYPE)-(DB_NAME)-(DB_HOST)` | `TRUE`                             |
| `DB01_FILESYSTEM_PATH`            | Directory where the database dumps are kept.                                                          | `/backup`                          |
| `DB01_FILESYSTEM_PATH_PERMISSION` | Permissions to apply to backup directory                                                              | `700`                              |
| `DB01_FILESYSTEM_ARCHIVE_PATH`    | Optional Directory where the database dumps archives are kept                                         | `${DB01_FILESYSTEM_PATH}/archive/` |
| `DB01_FILESYSTEM_PERMISSION`      | Directory and File permissions to apply to files.                                                     | `600`                              |

###### S3

If `DB01_BACKUP_LOCATION` = `S3` then the following options are used:

| Parameter                  | Description                                                                               | Default | `_FILE` |
| -------------------------- | ----------------------------------------------------------------------------------------- | ------- | ------- |
| `DB01_S3_BUCKET`           | S3 Bucket name e.g. `mybucket`                                                            |         | x       |
| `DB01_S3_KEY_ID`           | S3 Key ID (Optional)                                                                      |         | x       |
| `DB01_S3_KEY_SECRET`       | S3 Key Secret (Optional)                                                                  |         | x       |
| `DB01_S3_PATH`             | S3 Pathname to save to (must NOT end in a trailing slash e.g. '`backup`')                 |         | x       |
| `DB01_S3_REGION`           | Define region in which bucket is defined. Example: `ap-northeast-2`                       |         | x       |
| `DB01_S3_HOST`             | Hostname (and port) of S3-compatible service, e.g. `minio:8080`. Defaults to AWS.         |         | x       |
| `DB01_S3_PROTOCOL`         | Protocol to connect to `DB01_S3_HOST`. Either `http` or `https`. Defaults to `https`.     | `https` | x       |
| `DB01_S3_EXTRA_OPTS`       | Add any extra options to the end of the `aws-cli` process execution                       |         | x       |
| `DB01_S3_CERT_CA_FILE`     | Map a volume and point to your custom CA Bundle for verification e.g. `/certs/bundle.pem` |         | x       |
| _*OR*_                     |                                                                                           |         |         |
| `DB01_S3_CERT_SKIP_VERIFY` | Skip verifying self signed certificates when connecting                                   | `TRUE`  |         |

> When `DB01_S3_KEY_ID` and/or `DB01_S3_KEY_SECRET` is not set, will try to use IAM role assigned (if any) for uploading the backup files to S3 bucket.

###### Azure

If `DB01_BACKUP_LOCATION` = `blobxfer` then the following options are used:

| Parameter                              | Description                                                         | Default             | `_FILE` |
| -------------------------------------- | ------------------------------------------------------------------- | ------------------- | ------- |
| `DB01_BLOBXFER_STORAGE_ACCOUNT`        | Microsoft Azure Cloud storage account name.                         |                     | x       |
| `DB01_BLOBXFER_STORAGE_ACCOUNT_KEY`    | Microsoft Azure Cloud storage account key.                          |                     | x       |
| `DB01_BLOBXFER_REMOTE_PATH`            | Remote Azure path                                                   | `/docker-db-backup` | x       |
| `DB01_BLOBXFER_REMOTE_MODE`            | Azure Storage mode e.g. `auto`, `file`, `append`, `block` or `page` | `auto`              | x       |

- When `DEFAULT_BLOBXFER_MODE` is set to auto it will use blob containers by default. If the `DEFAULT_BLOBXFER_REMOTE_PATH` path does not exist a blob container with that name will be created.

> This service uploads files from backup directory `DB01_BACKUP_FILESYSTEM_PATH`.
> If a cleanup configuration in `DB01_CLEANUP_TIME` is defined, the remote directory on Azure storage will also be cleaned automatically.

##### Hooks

###### Path Options

| Parameter                   | Description                                                                 | Default                 |
| --------------------------- | --------------------------------------------------------------------------- | ----------------------- |
| `DB01_SCRIPT_LOCATION_PRE`  | Location on filesystem inside container to execute bash scripts pre backup  | `/assets/scripts/pre/`  |
| `DB01_SCRIPT_LOCATION_POST` | Location on filesystem inside container to execute bash scripts post backup | `/assets/scripts/post/` |
| `DB01_PRE_SCRIPT`           | Fill this variable in with a command to execute pre backing up              |                         |
| `DB01_POST_SCRIPT`          | Fill this variable in with a command to execute post backing up             |                         |

###### Pre Backup

If you want to execute a custom script before a backup starts, you can drop bash scripts with the extension of `.sh` in the location defined in `DB01_SCRIPT_LOCATION_PRE`. See the following example to utilize:

```bash
$ cat pre-script.sh
##!/bin/bash

# #### Example Pre Script
# #### $1=DB01_TYPE (Type of Backup)
# #### $2=DB01_HOST (Backup Host)
# #### $3=DB01_NAME (Name of Database backed up)
# #### $4=BACKUP START TIME (Seconds since Epoch)
# #### $5=BACKUP FILENAME (Filename)

echo "${1} Backup Starting on ${2} for ${3} at ${4}. Filename: ${5}"
```

```text
## script DB01_TYPE DB01_HOST DB01_NAME STARTEPOCH BACKUP_FILENAME
${f} "${backup_job_db_type}" "${backup_job_db_host}" "${backup_job_db_name}" "${backup_routines_start_time}" "${backup_job_filename}"
```

Outputs the following on the console:

`mysql Backup Starting on example-db for example at 1647370800. Filename: mysql_example_example-db_202200315-000000.sql.bz2`

###### Post backup

If you want to execute a custom script at the end of a backup, you can drop bash scripts with the extension of `.sh` in the location defined in `DB01_SCRIPT_LOCATION_POST`. Also to support legacy users `/assets/custom-scripts` is also scanned and executed. See the following example to utilize:

```bash
$ cat post-script.sh
##!/bin/bash

# #### Example Post Script
# #### $1=EXIT_CODE (After running backup routine)
# #### $2=DB_TYPE (Type of Backup)
# #### $3=DB_HOST (Backup Host)
# #### $4=DB_NAME (Name of Database backed up)
# #### $5=BACKUP START TIME (Seconds since Epoch)
# #### $6=BACKUP FINISH TIME (Seconds since Epoch)
# #### $7=BACKUP TOTAL TIME (Seconds between Start and Finish)
# #### $8=BACKUP FILENAME (Filename)
# #### $9=BACKUP FILESIZE
# #### $10=HASH (If CHECKSUM enabled)
# #### $11=MOVE_EXIT_CODE

echo "${1} ${2} Backup Completed on ${3} for ${4} on ${5} ending ${6} for a duration of ${7} seconds. Filename: ${8} Size: ${9} bytes MD5: ${10}"
```

```text
## script EXIT_CODE DB_TYPE DB_HOST DB_NAME STARTEPOCH FINISHEPOCH DURATIONEPOCH BACKUP_FILENAME FILESIZE CHECKSUMVALUE
${f} "${exit_code}" "${dbtype}" "${dbhost}" "${dbname}" "${backup_routines_start_time}" "${backup_routines_finish_time}" "${backup_routines_total_time}" "${backup_job_filename}" "${filesize}" "${checksum_value}" "${move_exit_code}
```

Outputs the following on the console:

`0 mysql Backup Completed on example-db for example on 1647370800 ending 1647370920 for a duration of 120 seconds. Filename: mysql_example_example-db_202200315-000000.sql.bz2 Size: 7795 bytes Hash: 952fbaafa30437494fdf3989a662cd40 0`

If you wish to change the size value from bytes to megabytes set environment variable `DB01_SIZE_VALUE=megabytes`

You must make your scripts executable otherwise there is an internal check that will skip trying to run it otherwise.
If for some reason your filesystem or host is not detecting it right, use the environment variable `DB01_POST_SCRIPT_SKIP_X_VERIFY=TRUE` to bypass.

#### Notifications

This image has capabilities on sending notifications via a handful of services when a backup job fails. This is a global option that cannot be individually set per backup job.

| Parameter              | Description                                                                       | Default |
| ---------------------- | --------------------------------------------------------------------------------- | ------- |
| `ENABLE_NOTIFICATIONS` | Enable Notifications                                                              | `FALSE` |
| `NOTIFICATION_TYPE`    | `CUSTOM` `EMAIL` `MATRIX` `MATTERMOST` `ROCKETCHAT` - Separate Multiple by commas |         |

##### Custom Notifications

The following is sent to the custom script. Use how you wish:

````text
$1 unix timestamp
$2 logfile
$3 errorcode
$4 subject
$5 body/error message
````

| Parameter                    | Description                                             | Default |
| ---------------------------- | ------------------------------------------------------- | ------- |
| `NOTIFICATION_CUSTOM_SCRIPT` | Path and name of custom script to execute notification. |         |

##### Email Notifications

See more details in the base image listed above for more mail environment variables.

| Parameter   | Description                                                                               | Default | `_FILE` |
| ----------- | ----------------------------------------------------------------------------------------- | ------- | ------- |
| `MAIL_FROM` | What email address to send mail from for errors                                           |         |         |
| `MAIL_TO`   | What email address to send mail to for errors. Send to multiple by separating with comma. |         |         |
| `SMTP_HOST` | What SMTP server to use for sending mail                                                  |         | x       |
| `SMTP_PORT` | What SMTP port to use for sending mail                                                    |         | x       |

##### Matrix Notifications

Fetch a `MATRIX_ACCESS_TOKEN`:

````bash
curl -XPOST -d '{"type":"m.login.password", "user":"myuserid", "password":"mypass"}' "https://matrix.org/_matrix/client/r0/login"
````

Copy the JSON response `access_token` that will look something like this:

````json
{"access_token":"MDAxO...blahblah","refresh_token":"MDAxO...blahblah","home_server":"matrix.org","user_id":"@myuserid:matrix.org"}
````

| Parameter             | Description                                                                              | Default | `_FILE` |
| --------------------- | ---------------------------------------------------------------------------------------- | ------- | ------- |
| `MATRIX_HOST`         | URL (<https://matrix.example.com>) of Matrix Homeserver                                  |         | x       |
| `MATRIX_ROOM`         | Room ID eg `\!abcdef:example.com` to send to. Send to multiple by separating with comma. |         | x       |
| `MATRIX_ACCESS_TOKEN` | Access token of user authorized to send to room                                          |         | x       |

##### Mattermost Notifications

| Parameter                | Description                                                                                  | Default | `_FILE` |
| ------------------------ | -------------------------------------------------------------------------------------------- | ------- | ------- |
| `MATTERMOST_WEBHOOK_URL` | Full URL to send webhook notifications to                                                    |         | x       |
| `MATTERMOST_RECIPIENT`   | Channel or User to send Webhook notifications to. Send to multiple by separating with comma. |         | x       |
| `MATTERMOST_USERNAME`    | Username to send as eg `tiredofit`                                                           |         | x       |

##### Rocketchat Notifications

| Parameter                | Description                                                                                  | Default | `_FILE` |
| ------------------------ | -------------------------------------------------------------------------------------------- | ------- | ------- |
| `ROCKETCHAT_WEBHOOK_URL` | Full URL to send webhook notifications to                                                    |         | x       |
| `ROCKETCHAT_RECIPIENT`   | Channel or User to send Webhook notifications to. Send to multiple by separating with comma. |         | x       |
| `ROCKETCHAT_USERNAME`    | Username to send as eg `tiredofit`                                                           |         | x       |

## Maintenance

### Shell Access

For debugging and maintenance purposes you may want to access the container's shell.

```bash
docker exec -it (whatever your container name is) bash
```

### Manual Backups

Manual Backups can be performed by entering the container and typing `backup-now`. This will execute all the backup tasks that are scheduled by means of the `BACKUPXX_` variables. Alternatively if you wanted to execute a job on its own you could simply type `backup01-now` (or whatever your number would be). There is no concurrency, and jobs will be executed sequentially.

- For Kubernetes CronJob usage: set `MODE=MANUAL` and `MANUAL_RUN_FOREVER=FALSE`. Disable `CONTAINER_ENABLE_SCHEDULING`. The container will start, execute a backup, and exit cleanly.

### Restoring Databases

Entering in the container and executing `restore` will execute a menu based script to restore your backups - MariaDB, Postgres, and Mongo supported.

You will be presented with a series of menus allowing you to choose:

- What file to restore
- What type of DB Backup
- What Host to restore to
- What Database Name to restore to
- What Database User to use
- What Database Password to use
- What Database Port to use

The image will try to do auto detection based on the filename for the type, hostname, and database name.
The image will also allow you to use environment variables or Docker secrets used to backup the images

The script can also be executed skipping the interactive mode by using the following syntax:

```text
restore <filename> <db_type> <db_hostname> <db_name> <db_user> <db_pass> <db_port>
```

If you only enter some of the arguments you will be prompted to fill them in.

## Support

These images were built to serve a specific need in a production environment and gradually have had more functionality added based on requests from the community.

### Usage

- The [Discussions board](../../discussions) is a great place for working with the community on tips and tricks of using this image.
- [Sponsor me](https://www.tiredofit.ca/sponsor) for personalized support

### Bugfixes

- Please, submit a [Bug Report](issues/new) if something isn't working as expected. I'll do my best to issue a fix in short order.

### Feature Requests

- Feel free to submit a feature request, however there is no guarantee that it will be added, or at what timeline.
- [Sponsor me](https://www.tiredofit.ca/sponsor) regarding development of features.

### Updates

- Best effort to track upstream changes, More priority if I am actively using the image in a production environment.
- [Sponsor me](https://www.tiredofit.ca/sponsor) for up to date releases.

## License

MIT. See [LICENSE](LICENSE) for more details.
