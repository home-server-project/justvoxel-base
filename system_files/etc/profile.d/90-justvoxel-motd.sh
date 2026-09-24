# Show the JustVoxel welcome only in interactive shells.
# Each user can disable it with: mjust welcome-off
case "$-" in
    *i*)
        if [ -z "${JUSTVOXEL_MOTD_SOURCED:-}" ]; then
            JUSTVOXEL_MOTD_SOURCED=1
            export JUSTVOXEL_MOTD_SOURCED
            if [ -d "${HOME:-}" ] \
                && [ ! -e "${HOME}/.config/justvoxel/no-welcome" ] \
                && [ -x /usr/libexec/justvoxel/motd ]; then
                timeout 3s /usr/libexec/justvoxel/motd || true
            fi
        fi
        ;;
esac
