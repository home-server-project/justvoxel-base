FROM scratch
ARG MANAGEMENT_INPUT_IDENTITY
ARG HOME_SERVER_BASE_IDENTITY
ARG SOURCE_COMMIT
LABEL org.opencontainers.image.title="JustVoxel Management Agent internal build package" \
      org.opencontainers.image.description="Internal verified build artifact for JustVoxel Base assembly" \
      org.opencontainers.image.source="https://github.com/home-server-project/justvoxel-base" \
      org.opencontainers.image.revision="${SOURCE_COMMIT}" \
      io.home-server-project.justvoxel.management-input="${MANAGEMENT_INPUT_IDENTITY}" \
      io.home-server-project.justvoxel.management-base="${HOME_SERVER_BASE_IDENTITY}" \
      io.home-server-project.justvoxel.management-api="v1"
COPY management-agent /artifacts/management-agent
