ARG HOME_SERVER_BASE_IMAGE=ghcr.io/home-server-project/home-server-base-10:stable
ARG JUSTVOXEL_BASE_REPOSITORY=ghcr.io/home-server-project/justvoxel-base
ARG JUSTVOXEL_VM_REPOSITORY=ghcr.io/home-server-project/justvoxel-vm
ARG SUPERFILE_PACKAGE_IMAGE=ghcr.io/home-server-project/superfile:stable
ARG GLANCES_PACKAGE_IMAGE=ghcr.io/home-server-project/glances:stable

FROM ${SUPERFILE_PACKAGE_IMAGE} AS superfile-package
FROM ${GLANCES_PACKAGE_IMAGE} AS glances-package

FROM scratch AS ctx
COPY build_files /build_files
COPY build_artifacts /build_artifacts
COPY system_files /system_files
COPY docs /docs
COPY templates /templates
COPY runtime /runtime
COPY mjust /mjust
COPY --from=superfile-package /rpms /superfile-rpms
COPY --from=glances-package /rpms /glances-rpms
COPY cosign.pub /cosign.pub

FROM ${HOME_SERVER_BASE_IMAGE} AS justvoxel-base
ARG JUSTVOXEL_BASE_REPOSITORY
ARG JUSTVOXEL_VM_REPOSITORY

LABEL containers.bootc=1 \
      ostree.bootable=1 \
      org.opencontainers.image.vendor="Home Server Project" \
      org.opencontainers.image.source="https://github.com/home-server-project/justvoxel-base" \
      org.opencontainers.image.title="JustVoxel VM" \
      org.opencontainers.image.description="VM-ready JustVoxel Minecraft server appliance base" \
      io.home-server-project.justvoxel.role="base" \
      io.home-server-project.justvoxel.variant="vm" \
      io.home-server-project.justvoxel.base="home-server-base-10" \
      io.home-server-project.justvoxel.base-channel="stable" \
      io.home-server-project.justvoxel.base-profile="almalinux-10-minimal-plus" \
      io.home-server-project.justvoxel.status="development"

RUN --mount=type=bind,from=ctx,source=/,target=/ctx \
    --mount=type=tmpfs,dst=/run \
    --mount=type=tmpfs,dst=/tmp \
    /ctx/build_files/build-common.sh

RUN --mount=type=bind,from=ctx,source=/,target=/ctx \
    --mount=type=tmpfs,dst=/tmp \
    IMAGE_REPOSITORY="${JUSTVOXEL_BASE_REPOSITORY}" \
    IMAGE_ADDITIONAL_TRUST_REPOSITORIES="${JUSTVOXEL_VM_REPOSITORY}" \
    IMAGE_PRETTY_NAME="JustVoxel VM 10" \
    IMAGE_VARIANT="JustVoxel VM" \
    IMAGE_VARIANT_ID="justvoxel-vm" \
    /ctx/build_files/finalize-image.sh

RUN /usr/libexec/justvoxel/health/common \
    && bootc container lint --fatal-warnings

STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
