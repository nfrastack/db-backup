# SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
#
# SPDX-License-Identifier: MIT

ARG \
    BASE_IMAGE

FROM ${BASE_IMAGE}

LABEL \
        org.opencontainers.image.title="DB Backup" \
        org.opencontainers.image.description="Backup multiple databases on a scheduled basis with many customizable options" \
        org.opencontainers.image.url="https://hub.docker.com/r/nfrastack/db-backup" \
        org.opencontainers.image.documentation="https://github.com/nfrastack/container-db-backup/blob/main/README.md" \
        org.opencontainers.image.source="https://github.com/nfrastack/container-db-backup.git" \
        org.opencontainers.image.authors="Nfrastack <code@nfrastack.com>" \
        org.opencontainers.image.vendor="Nfrastack <https://www.nfrastack.com>" \
        org.opencontainers.image.licenses="MIT"

ARG \
    AWS_CLI_VERSION=1.44.56 \
    INFLUX1_CLIENT_VERSION=1.8.0 \
    INFLUX2_CLIENT_VERSION=2.7.5 \
    MSODBC_VERSION=18.6.1.1-1 \
    MSSQL_VERSION=18.6.1.1-1 \
    MYSQL_VERSION=mysql-9.7.2 \
    MYSQL_REPO_URL=https://github.com/mysql/mysql-server

COPY CHANGELOG.md /usr/src/container/CHANGELOG.md
COPY LICENSE /usr/src/container/LICENSE
COPY README.md /usr/src/container/README.md

ENV \
    CONTAINER_ENABLE_SCHEDULING=TRUE \
    IMAGE_NAME="nfrastack/db-backup" \
    IMAGE_REPO_URL="https://github.com/nfrastack/container-db-backup/"

RUN echo "" && \
    source /container/base/functions/container/build && \
    container_build_log image && \
    set -ex && \
    create_user dbbackup 10000 dbbackup 10000 /data/backup && \
    \
    DBBACKUP_BUILD_DEPS_ALPINE=" \
                                    build-base \
                                    bzip2-dev \
                                    cargo \
                                    cmake \
                                    curl-dev \
                                    git \
                                    go \
                                    libarchive-dev \
                                    libffi-dev \
                                    libtirpc-dev \
                                    ncurses-dev \
                                    openssl-dev \
                                    python3-dev \
                                    py3-pip \
                                    xz-dev \
                                " \
                                && \
    DBBACKUP_RUN_DEPS_ALPINE=" \
                                    bzip2 \
                                    coreutils \
                                    gpg \
                                    gpg-agent \
                                    groff \
                                    libarchive \
                                    libtirpc \
                                    mariadb-client \
                                    mariadb-connector-c \
                                    mongodb-tools \
                                    ncurses \
                                    openssl \
                                    pigz \
                                    pixz \
                                    postgresql18-client \
                                    pv \
                                    py3-botocore \
                                    py3-colorama \
                                    py3-cryptography \
                                    py3-docutils \
                                    py3-jmespath \
                                    py3-rsa \
                                    py3-setuptools \
                                    py3-s3transfer \
                                    py3-yaml \
                                    python3 \
                                    redis \
                                    sqlite \
                                    xz \
                                    zip \
                                    zstd \
                               " \
                               && \
    \
    package update && \
    package upgrade && \
    package install \
                    DBBACKUP_BUILD_DEPS \
                    DBBACKUP_RUN_DEPS \
                    && \
    \
    case "$(uname -m)" in \
        "x86_64" ) \
            go_arch="amd64" ; \
            influx2_arch="amd64" ; \
            influx2_install="true" ; \
            mssql_arch="amd64" ; \
            mssql_install="true" ; \
        ;; \
        "arm64" | "aarch64" ) \
            go_arch="arm64" ; \
            influx2_arch="arm64" ; \
            influx2_install="true" ; \
            mssql_arch="arm64" ; \
            mssql_install="true" ; \
        ;; \
        *) \
            : \
        ;; \
    esac ; \
    \
    if [ "${influx2_install}" = "true" ] ; then \
        curl -sSL https://dl.influxdata.com/influxdb/releases/influxdb2-client-${INFLUX2_CLIENT_VERSION}-linux-${influx2_arch}.tar.gz | tar xvfz - --strip=1 -C /usr/sbin/ ; \
        chmod +x /usr/sbin/influx ; \
    else \
        echo >&2 "Unable to build Influx 2 on this system" ; \
    fi ; \
    \
    if [ "${mssql_install}" = "true" ] ; then \
        mkdir -p /opt/microsoft/msodbcsql18/ && \
        touch /opt/microsoft/msodbcsql18/ACCEPT_EULA && \
        curl -sSL -O https://download.microsoft.com/download/9dcab408-e0d4-4571-a81a-5a0951e3445f/msodbcsql18_${MSODBC_VERSION}_${mssql_arch}.apk && \
        curl -sSL -O https://download.microsoft.com/download/b60bb8b6-d398-4819-9950-2e30cf725fb0/mssql-tools18_${MSSQL_VERSION}_${mssql_arch}.apk && \
        echo y | apk add --allow-untrusted msodbcsql18_${MSODBC_VERSION}_${mssql_arch}.apk mssql-tools18_${MSSQL_VERSION}_${mssql_arch}.apk && \
        rm -f msodbcsql18_${MSODBC_VERSION}_${mssql_arch}.apk mssql-tools18_${MSSQL_VERSION}_${mssql_arch}.apk ; \
    else \
        echo >&2 "Detected non x86_64 or ARM64 build variant, skipping MSSQL installation" ; \
    fi; \
    \
    clone_git_repo https://github.com/influxdata/influxdb "${INFLUX1_CLIENT_VERSION}" && \
    go build -o /usr/sbin/influxd ./cmd/influxd && \
    strip /usr/sbin/influxd && \
    \
    clone_git_repo "${MYSQL_REPO_URL}" "${MYSQL_VERSION}" && \
    cmake \
        -DCMAKE_BUILD_TYPE=MinSizeRel \
        -DCMAKE_INSTALL_PREFIX=/opt/mysql \
        -DFORCE_INSOURCE_BUILD=1 \
        -DWITHOUT_SERVER:BOOL=ON \
        && \
    make -j$(nproc) install && \
    \
    pip3 install --break-system-packages awscli==${AWS_CLI_VERSION} && \
    pip3 install --break-system-packages blobxfer && \
    \
    mkdir -p /usr/src/pbzip2 && \
    curl -sSL https://launchpad.net/pbzip2/1.1/1.1.13/+download/pbzip2-1.1.13.tar.gz | tar xvfz - --strip=1 -C /usr/src/pbzip2 && \
    cd /usr/src/pbzip2 && \
    make && \
    make install && \
    \
    package remove \
                    DBBACKUP_BUILD_DEPS \
                    && \
    package cleanup && \
    rm -rf \
            /root/.cache \
            /root/go \
            /tmp/* \
            /usr/src/*

COPY rootfs /
