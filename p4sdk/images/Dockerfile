
# SPDX-License-Identifier: Apache-2.0
# Copyright (c) 2024 Intel Corporation

FROM registry.access.redhat.com/ubi9/ubi:9.3-1610

ENV HTTP_PROXY $http_proxy
ENV HTTPS_PROXY $https_proxy

COPY . /src
WORKDIR /src
RUN mkdir -pv /opt/ && tar -zxvf p4.tar.gz -C /opt/ && rm -f p4.tar.gz

COPY p4 /opt/p4/rh_mvp
COPY images/entrypoint.sh /
COPY images/es2k_skip_p4.conf.template /

RUN dnf -y install \
    kmod-28-9.el9.aarch64 \
    gettext-0.21-8.el9.aarch64 \
    python3-pip-21.2.3-7.el9_3.1.noarch \
    pciutils-3.7.0-5.el9.aarch64 \
    libnl3-3.7.0-1.el9.aarch64 \
    libedit-3.1-38.20210216cvs.el9.aarch64 \
    libatomic-11.4.1-2.1.el9.aarch64 \
    && curl -L -o libconfig-1.7.2-9.el9.aarch64.rpm \
    https://mirror.stream.centos.org/9-stream/BaseOS/aarch64/os/Packages/libconfig-1.7.2-9.el9.aarch64.rpm \
    && dnf -y install libconfig-1.7.2-9.el9.aarch64.rpm && rm -f libconfig-1.7.2-9.el9.aarch64.rpm && dnf clean all
RUN python3 -m pip install --no-cache-dir \
    grpcio==1.62.1 \
    protobuf==4.25.3 \
    netaddr==1.2.1 \
    /opt/p4/p4-cp-nws/share/stratum/p4runtime/p4runtime-2023.11.0-py3-none-any.whl

WORKDIR /

ENTRYPOINT ["/entrypoint.sh"]
