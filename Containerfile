ARG HOME_SERVER_BASE_IMAGE=ghcr.io/home-server-project/home-server-base-10:stable-docker
ARG JUSTVOXEL_BASE_REPOSITORY=ghcr.io/home-server-project/justvoxel-plus-base
ARG SUPERFILE_PACKAGE_IMAGE=ghcr.io/home-server-project/superfile:stable
ARG PLAYIT_PACKAGE_IMAGE=ghcr.io/home-server-project/playit:stable
ARG GLANCES_PACKAGE_IMAGE=ghcr.io/home-server-project/glances:stable

FROM ${SUPERFILE_PACKAGE_IMAGE} AS superfile-package
FROM ${GLANCES_PACKAGE_IMAGE} AS glances-package
FROM ${PLAYIT_PACKAGE_IMAGE} AS playit-package

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
COPY --from=playit-package /rpms /playit-rpms
COPY cosign.pub /cosign.pub

FROM ${HOME_SERVER_BASE_IMAGE} AS justvoxel-base
ARG JUSTVOXEL_BASE_REPOSITORY

LABEL containers.bootc=1 \
      ostree.bootable=1 \
      org.opencontainers.image.vendor="Home Server Project" \
      org.opencontainers.image.source="https://github.com/home-server-project/justvoxel-base" \
      org.opencontainers.image.title="JustVoxel Plus Base" \
      org.opencontainers.image.description="VM-ready JustVoxel Plus host for Pterodactyl and Drydock" \
      io.home-server-project.justvoxel.role="base" \
      io.home-server-project.justvoxel.variant="plus-base" \
      io.home-server-project.justvoxel.base="home-server-base-10" \
      io.home-server-project.justvoxel.base-channel="stable" \
      io.home-server-project.justvoxel.container-runtime="docker" \
      io.home-server-project.justvoxel.base-profile="almalinux-10-minimal-plus" \
      io.home-server-project.justvoxel.status="development"

RUN --mount=type=bind,from=ctx,source=/,target=/ctx \
    --mount=type=tmpfs,dst=/run \
    --mount=type=tmpfs,dst=/tmp \
    /ctx/build_files/build-common.sh

RUN --mount=type=bind,from=ctx,source=/,target=/ctx \
    --mount=type=tmpfs,dst=/tmp \
    IMAGE_REPOSITORY="${JUSTVOXEL_BASE_REPOSITORY}" \
    IMAGE_PRETTY_NAME="JustVoxel Plus Base 10" \
    IMAGE_VARIANT="JustVoxel Plus Base" \
    IMAGE_VARIANT_ID="justvoxel-plus-base" \
    /ctx/build_files/finalize-image.sh

RUN /usr/libexec/justvoxel/health/common \
    && bootc container lint --fatal-warnings

STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
