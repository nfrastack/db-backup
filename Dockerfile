ARG DISTRO=alpine
ARG DISTRO_VARIANT=3.21-7.10.31

FROM docker.io/tiredofit/${DISTRO}:${DISTRO_VARIANT}
LABEL maintainer="nfrastack (github.com/nfrastack)"

ENV INFLUX1_CLIENT_VERSION=1.8.0 \
    INFLUX2_CLIENT_VERSION=2.7.5 \
    MSODBC_VERSION=18.6.1.1-1 \
    MSSQL_VERSION=18.6.1.1-1 \
    MYSQL_VERSION=mysql-8.4.8 \
    MYSQL_REPO_URL=https://github.com/mysql/mysql-server \
    NEO4J_VERSION=5.26.0 \
    AWS_CLI_VERSION=1.44.56 \
    POSTGRES_VERSION=18.3 \
    CONTAINER_ENABLE_MESSAGING=TRUE \
    CONTAINER_ENABLE_MONITORING=TRUE \
    IMAGE_NAME="nfrastack/container-db-backup" \
    IMAGE_REPO_URL="https://github.com/nfrastack/container-db-backup/"

RUN source /assets/functions/00-container && \
    set -ex && \
    addgroup -g 70 postgres && \
    adduser -S -D -H -h /var/lib/postgresql -s /bin/sh -G postgres -u 70 postgres && \
    mkdir -p /var/lib/postgresql && \
    chown -R postgres:postgres /var/lib/postgresql && \
    \
    package update && \
    package upgrade && \
    package install .postgres-build-deps \
                    bison \
                    clang19 \
                    coreutils \
                    dpkg-dev \
                    dpkg \
                    flex \
                    g++ \
                    gcc \
                    icu-dev \
                    libc-dev \
                    libedit-dev \
                    libxml2-dev \
                    libxslt-dev \
                    linux-headers \
                    llvm19-dev \
                    lz4-dev \
                    make \
                    openldap-dev \
                    openssl-dev \
                    perl-dev \
                    perl-ipc-run \
                    perl-utils \
                    python3-dev \
                    tcl-dev \
                    util-linux-dev \
                    zlib-dev \
                    zstd-dev \
                    && \
   \
   package install .postgres-run-deps \
                    icu-data-full \
                    libpq-dev \
                    llvm19 \
                    musl-locales \
                    openssl \
                    zstd-libs \
                    && \
   \
   mkdir -p /usr/src/postgres && \
   curl -sSL https://ftp.postgresql.org/pub/source/v$POSTGRES_VERSION/postgresql-$POSTGRES_VERSION.tar.bz2 | tar xvfj - --strip 1 -C /usr/src/postgres && \
   cd /usr/src/postgres && \
   awk '$1 == "#define" && $2 == "DEFAULT_PGSOCKET_DIR" && $3 == "\"/tmp\"" { $3 = "\"/var/run/postgresql\""; print; next } { print }' src/include/pg_config_manual.h > src/include/pg_config_manual.h.new && \
   grep '/var/run/postgresql' src/include/pg_config_manual.h.new && \
   mv src/include/pg_config_manual.h.new src/include/pg_config_manual.h && \
   export LLVM_CONFIG="/usr/lib/llvm19/bin/llvm-config" && \
   export CLANG=clang-19  && \
    ./configure \
        --build="$(dpkg-architecture --query DEB_BUILD_GNU_TYPE)" \
        --prefix=/usr/local \
        --with-includes=/usr/local/include \
        --with-libraries=/usr/local/lib \
        --with-system-tzdata=/usr/share/zoneinfo \
        --with-pgport=5432 \
        --disable-rpath \
        --enable-integer-datetimes \
        --enable-thread-safety \
        --enable-tap-tests \
        --with-gnu-ld \
        --with-icu \
        --with-ldap \
        --with-libxml \
        --with-libxslt \
        --with-llvm \
        --with-lz4 \
        --with-openssl \
        --with-perl \
        --with-python \
        --with-tcl \
        --with-uuid=e2fs \
        --with-zstd \
        && \
    make -j "$(nproc)" world-bin && \
    make install-world-bin && \
    make -j "$(nproc)" -C contrib && \
    make -C contrib/ install && \
    runDeps="$( \
		scanelf --needed --nobanner --format '%n#p' --recursive /usr/local \
			| tr ',' '\n' \
			| sort -u \
			| awk 'system("[ -e /usr/local/lib/" $1 " ]") == 0 { next } { print "so:" $1 }' \
			| grep -v -e perl -e python -e tcl \
            )"; \
	package install .postgres-additional-deps \
                    $runDeps \
	               && \
	\
    package remove \
                    .postgres-build-deps \
                    && \
    package cleanup && \
    find /usr/local -name '*.a' -delete && \
    rm -rf \
            /root/.cache \
            /root/go \
	       /usr/local/share/doc \
	       /usr/local/share/man \
            /usr/src/* \
            && \
    \
    set -ex && \
    addgroup -S -g 10000 dbbackup && \
    adduser -S -D -H -u 10000 -G dbbackup -g "Tired of I.T! DB Backup" dbbackup && \
    \
    package update && \
    package upgrade && \
    package install .db-backup-build-deps \
                    build-base \
                    bzip2-dev \
                    cargo \
                    cmake \
                    git \
                    go \
                    libarchive-dev \
                    libtirpc-dev \
                    openssl-dev \
                    libffi-dev \
                    ncurses-dev \
                    python3-dev \
                    py3-pip \
                    xz-dev \
                    && \
    \
    package install .db-backup-run-deps \
                    bzip2 \
                    coreutils \
                    gpg \
                    gpg-agent \
                    groff \
                    jq \
                    libarchive \
                    libtirpc \
                    mariadb-client \
                    mariadb-connector-c \
                    mongodb-tools \
                    ncurses \
                    openjdk17-jre-headless \
                    openssl \
                    pigz \
                    pixz \
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
                    && \
    \
    echo ""
    RUN set -ex && \
    source /assets/functions/00-container && \
    mkdir -p /opt/microsoft/msodbcsql18/ && \
    touch /opt/microsoft/msodbcsql18/ACCEPT_EULA && \
    case "$(uname -m)" in \
	    "x86_64" ) mssql=true ; mssql_arch=amd64; influx2=true ; influx_arch=amd64; ;; \
        "arm64" | "aarch64" ) mssql=true ; mssql_arch=arm64; influx2=true ; influx_arch=arm64 ;; \
        *) sleep 0.1 ;; \
    esac; \
    \
    if [ "${mssql,,}" = "true" ] ; then \
        curl -O https://download.microsoft.com/download/9dcab408-e0d4-4571-a81a-5a0951e3445f/msodbcsql18_${MSODBC_VERSION}_${mssql_arch}.apk ; \
        curl -O https://download.microsoft.com/download/b60bb8b6-d398-4819-9950-2e30cf725fb0/mssql-tools18_${MSSQL_VERSION}_${mssql_arch}.apk ; \
        echo y | apk add --allow-untrusted msodbcsql18_${MSODBC_VERSION}_${mssql_arch}.apk mssql-tools18_${MSSQL_VERSION}_${mssql_arch}.apk ; \
    else \
        echo >&2 "Detected non x86_64 or ARM64 build variant, skipping MSSQL installation" ; \
    fi; \
    \
    if [ "${influx2,,}" = "true" ] ; then \
        curl -sSL https://dl.influxdata.com/influxdb/releases/influxdb2-client-${INFLUX2_CLIENT_VERSION}-linux-${influx_arch}.tar.gz | tar xvfz - --strip=1 -C /usr/src/ ; \
        chmod +x /usr/src/influx ; \
        mv /usr/src/influx /usr/sbin/ ; \
    else \
        echo >&2 "Unable to build Influx 2 on this system" ; \
    fi ; \
    \
    mkdir -p /usr/src/neo4j && \
    curl -sSL https://dist.neo4j.org/neo4j-community-${NEO4J_VERSION}-unix.tar.gz | tar xzf - --strip-components=1 -C /usr/src/neo4j && \
    mkdir -p /opt/neo4j/bin /opt/neo4j/lib && \
    cp /usr/src/neo4j/bin/cypher-shell /opt/neo4j/bin/ && \
    cp /usr/src/neo4j/lib/*.jar /opt/neo4j/lib/ && \
    chmod +x /opt/neo4j/bin/cypher-shell && \
    ln -sf /opt/neo4j/bin/cypher-shell /usr/local/bin/cypher-shell && \
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
    package remove .db-backup-build-deps && \
    package cleanup && \
    rm -rf \
            /*.apk \
            /etc/logrotate.d/* \
            /root/.cache \
            /root/go \
            /tmp/* \
            /usr/src/*

COPY install  /
